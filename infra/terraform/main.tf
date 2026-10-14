locals {
  repo_root = abspath("${path.module}/../..")
  tls       = var.rpc_domain != ""
  nodes     = { for i in range(var.node_count) : "node${i + 1}" => i }

  # Build inputs include Go, cgo and embedded assets. Exclude generated build
  # output and deployment settings; Terraform state never enters the image.
  source_files = sort([for f in fileset(local.repo_root, "**") : f
    if !startswith(f, ".") && !startswith(f, "infra/") && !startswith(f, "build/") &&
    can(regex("\\.(go|mod|sum|c|h|s|S|js|json|html)$", f))
  ])
  source_hash = sha256(join("", [for f in local.source_files : "${f}:${filesha256("${local.repo_root}/${f}")}"]))
  gateway_hash = sha256(join("", [for f in sort(fileset("${path.module}/gateway", "**")) :
    "${f}:${filesha256("${path.module}/gateway/${f}")}"
  ]))
  # Known test key matches docker-compose.yml. Discovery only, never a wallet.
  bootnode_enode = "enode://79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8@${cidrhost(var.node_subnet, 2)}:30301"
  rpc_acl        = length(var.rpc_allowed_cidrs) == 0 ? "" : "${join("\n", [for cidr in var.rpc_allowed_cidrs : "allow ${cidr};"])}\ndeny all;"
}

resource "terraform_data" "settings" {
  lifecycle {
    precondition {
      condition     = length(var.miner_addresses) <= var.node_count
      error_message = "miner_addresses cannot exceed node_count."
    }
    precondition {
      condition     = !local.tls || (var.acme_email != "" && var.http_port == 80 && var.https_port == 443 && var.bind_ip != "127.0.0.1")
      error_message = "HTTPS requires acme_email, a reachable non-loopback bind_ip and host ports 80/443."
    }
    precondition {
      condition     = var.bind_ip == "127.0.0.1" || (local.tls && length(var.rpc_allowed_cidrs) > 0)
      error_message = "A non-loopback binding requires rpc_domain (HTTPS) and a specific rpc_allowed_cidrs allowlist."
    }
    precondition {
      condition     = var.http_port != var.https_port
      error_message = "http_port and https_port must differ."
    }
  }
}

resource "docker_image" "node" {
  name         = "${var.name}-node:terraform"
  keep_locally = true
  build {
    context    = local.repo_root
    dockerfile = "Dockerfile"
  }
  triggers = {
    source     = local.source_hash
    dockerfile = filesha256("${local.repo_root}/Dockerfile")
    ignore     = filesha256("${local.repo_root}/.dockerignore")
  }
}

resource "docker_image" "tools" {
  name         = "${var.name}-tools:terraform"
  keep_locally = true
  build {
    context    = local.repo_root
    dockerfile = "Dockerfile.alltools"
  }
  triggers = {
    source     = local.source_hash
    dockerfile = filesha256("${local.repo_root}/Dockerfile.alltools")
    ignore     = filesha256("${local.repo_root}/.dockerignore")
  }
}

resource "docker_image" "gateway" {
  name         = "${var.name}-nginx:terraform"
  keep_locally = true
  build {
    context = abspath("${path.module}/gateway")
  }
  triggers = { source = local.gateway_hash }
}

resource "docker_image" "certbot" {
  count        = local.tls ? 1 : 0
  name         = "certbot/certbot:v5.0.0"
  keep_locally = true
}

resource "docker_network" "nodes" {
  name     = "${var.name}-nodes"
  internal = true
  ipam_config { subnet = var.node_subnet }
}

resource "docker_network" "edge" {
  name = "${var.name}-edge"
}

resource "docker_volume" "node_data" {
  for_each = local.nodes
  name     = "${var.name}-${each.key}-data"
  lifecycle { prevent_destroy = true }
}

resource "docker_volume" "tls_data" {
  for_each = toset(local.tls ? ["certificates", "challenges", "acme-work"] : [])
  name     = "${var.name}-${each.key}"
  lifecycle { prevent_destroy = true }
}

resource "docker_container" "bootnode" {
  name       = "${var.name}-bootnode"
  image      = docker_image.tools.image_id
  entrypoint = ["bootnode"]
  command = [
    "--nodekeyhex=0000000000000000000000000000000000000000000000000000000000000001",
    "--addr=:30301", "--verbosity=3"
  ]
  restart = "unless-stopped"
  networks_advanced {
    name         = docker_network.nodes.name
    aliases      = ["bootnode"]
    ipv4_address = cidrhost(var.node_subnet, 2)
  }
  log_driver = "json-file"
  log_opts   = { "max-size" = "10m", "max-file" = "3" }
  depends_on = [terraform_data.settings]
}

resource "docker_container" "node" {
  for_each = local.nodes
  name     = "${var.name}-${each.key}"
  image    = docker_image.node.image_id
  command = concat([
    "--networkid=28282", "--bootnodes=${local.bootnode_enode}",
    "--syncmode=full", "--gcmode=archive", "--nat=none", "--discovery.dns=",
    "--http", "--http.addr=0.0.0.0", "--http.port=8545", "--http.api=eth,net,web3",
    "--http.vhosts=localhost,127.0.0.1${local.tls ? ",${var.rpc_domain}" : ""}",
    "--ws", "--ws.addr=0.0.0.0", "--ws.port=8546", "--ws.api=eth,net,web3",
    "--ws.origins=${local.tls ? "https://${var.rpc_domain}" : "http://localhost:${var.http_port}"}",
    "--verbosity=3"
    ], each.value < length(var.miner_addresses) ? [
    "--mine", "--miner.threads=1", "--miner.etherbase=${var.miner_addresses[each.value]}"
  ] : [])
  restart      = "unless-stopped"
  stop_timeout = 60
  volumes {
    volume_name    = docker_volume.node_data[each.key].name
    container_path = "/root/.ethereum"
  }
  networks_advanced {
    name         = docker_network.nodes.name
    aliases      = [each.key]
    ipv4_address = cidrhost(var.node_subnet, each.value + 10)
  }
  log_driver = "json-file"
  log_opts   = { "max-size" = "10m", "max-file" = "3" }
  depends_on = [docker_container.bootnode]
}

resource "docker_container" "gateway" {
  name  = "${var.name}-nginx"
  image = docker_image.gateway.image_id
  env = [
    "RPC_DOMAIN=${local.tls ? var.rpc_domain : "localhost"}",
    "TLS_ENABLED=${local.tls ? "true" : "false"}",
    "RPC_ACL=${local.rpc_acl}"
  ]
  restart = "unless-stopped"
  ports {
    internal = 80
    external = var.http_port
    ip       = var.bind_ip
  }
  dynamic "ports" {
    for_each = local.tls ? [443] : []
    content {
      internal = 443
      external = var.https_port
      ip       = var.bind_ip
    }
  }
  dynamic "volumes" {
    for_each = local.tls ? { certificates = "/etc/letsencrypt", challenges = "/var/www/acme" } : {}
    content {
      volume_name    = docker_volume.tls_data[volumes.key].name
      container_path = volumes.value
      read_only      = true
    }
  }
  networks_advanced { name = docker_network.nodes.name }
  networks_advanced { name = docker_network.edge.name }
  log_driver = "json-file"
  log_opts   = { "max-size" = "10m", "max-file" = "3" }
  depends_on = [docker_container.node]
}

resource "docker_container" "certbot" {
  count      = local.tls ? 1 : 0
  name       = "${var.name}-certbot"
  image      = docker_image.certbot[0].image_id
  entrypoint = ["/bin/sh", "/opt/aplo/certbot.sh"]
  env = [
    "RPC_DOMAIN=${var.rpc_domain}", "ACME_EMAIL=${var.acme_email}",
    "ACME_STAGING=${var.acme_staging ? "true" : "false"}"
  ]
  upload {
    file    = "/opt/aplo/certbot.sh"
    content = file("${path.module}/certbot.sh")
  }
  restart = "unless-stopped"
  volumes {
    volume_name    = docker_volume.tls_data["certificates"].name
    container_path = "/etc/letsencrypt"
  }
  volumes {
    volume_name    = docker_volume.tls_data["challenges"].name
    container_path = "/var/www/acme"
  }
  volumes {
    volume_name    = docker_volume.tls_data["acme-work"].name
    container_path = "/var/lib/letsencrypt"
  }
  networks_advanced { name = docker_network.edge.name }
  log_driver = "json-file"
  log_opts   = { "max-size" = "10m", "max-file" = "3" }
  depends_on = [docker_container.gateway]
}

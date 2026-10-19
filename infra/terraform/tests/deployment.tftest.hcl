mock_provider "docker" {}

# Synthetic TEST-NET seed. Mock plans never contact this address.
variables { bootstrap_enodes = ["enode://79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798483ada7726a3c4655da4fbfc0e1108a8fd17b448a68554199c47d08ffb10d4b8@198.51.100.2:30303"] }

run "local_defaults" {
  command = plan
  assert {
    condition     = output.rpc_url == "http://localhost:8080" && output.websocket_url == "ws://localhost:8080/ws"
    error_message = "Local RPC and WebSocket must go through Nginx."
  }
  assert {
    condition     = length(docker_container.node) == 1 && length(docker_container.certbot) == 0
    error_message = "Local mode must use one node and no ACME client."
  }
  assert {
    condition     = length(docker_container.node["node1"].ports) == 2 && alltrue([for p in docker_container.node["node1"].ports : p.internal == 30303]) && !contains(docker_container.node["node1"].command, "--mine")
    error_message = "Nodes must publish only P2P, with mining disabled by default."
  }
  assert {
    condition     = docker_container.gateway.ports[0].ip == "127.0.0.1" && !docker_network.nodes.internal
    error_message = "RPC must bind loopback while peers have outgoing network connectivity."
  }
  assert {
    condition     = contains(docker_container.node["node1"].command, "--http.api=eth,net,web3")
    error_message = "Do not expose account, miner, admin or debug APIs through HTTP."
  }
}

run "server_https" {
  command = plan
  variables {
    rpc_domain        = "rpc.example.com"
    acme_email        = "admin@example.com"
    bind_ip           = "0.0.0.0"
    http_port         = 80
    https_port        = 443
    rpc_allowed_cidrs = ["203.0.113.10/32"]
    node_count        = 4
  }
  assert {
    condition     = length(docker_container.certbot) == 1 && output.rpc_url == "https://rpc.example.com"
    error_message = "A public domain must enable the ACME client and HTTPS endpoint."
  }
  assert {
    condition     = contains(docker_container.gateway.env, "RPC_ACL=allow 203.0.113.10/32;\ndeny all;")
    error_message = "Public RPC must carry the configured allowlist into Nginx."
  }
  assert {
    condition     = !contains(docker_container.node["node1"].command, "--mine") && !contains(docker_container.node["node2"].command, "--mine")
    error_message = "Ordinary nodes must not mine."
  }
  assert {
    condition     = length(docker_container.node) == 4 && length(docker_volume.node_data) == 4 && length(docker_volume.tls_data) == 3
    error_message = "Each node needs its own data volume, plus persistent TLS/challenge/ACME volumes."
  }
}

run "reject_public_plaintext" {
  command = plan
  variables { bind_ip = "0.0.0.0" }
  expect_failures = [terraform_data.settings]
}

run "reject_public_without_acl" {
  command = plan
  variables {
    rpc_domain = "rpc.example.com"
    acme_email = "admin@example.com"
    bind_ip    = "0.0.0.0"
    http_port  = 80
    https_port = 443
  }
  expect_failures = [terraform_data.settings]
}

run "reject_https_without_email" {
  command = plan
  variables {
    rpc_domain        = "rpc.example.com"
    bind_ip           = "0.0.0.0"
    http_port         = 80
    https_port        = 443
    rpc_allowed_cidrs = ["203.0.113.10/32"]
  }
  expect_failures = [terraform_data.settings]
}

run "reject_acme_wrong_port" {
  command = plan
  variables {
    rpc_domain        = "rpc.example.com"
    acme_email        = "admin@example.com"
    bind_ip           = "0.0.0.0"
    https_port        = 443
    rpc_allowed_cidrs = ["203.0.113.10/32"]
  }
  expect_failures = [terraform_data.settings]
}

run "reject_unrestricted_acl" {
  command = plan
  variables { rpc_allowed_cidrs = ["0.0.0.0/0"] }
  expect_failures = [var.rpc_allowed_cidrs]
}

run "reject_domain_injection" {
  command = plan
  variables { rpc_domain = "example.com;return 200" }
  expect_failures = [var.rpc_domain]
}

run "reject_more_miners_than_nodes" {
  command = plan
  variables {
    miner_addresses = ["0x1111111111111111111111111111111111111111", "0x2222222222222222222222222222222222222222"]
  }
  expect_failures = [terraform_data.settings]
}

run "ordinary_node_is_default" {
  command = plan
  assert {
    condition     = output.deployment_mode == "node" && length(docker_container.bootnode) == 0 && length(docker_image.tools) == 0
    error_message = "Default must be a syncing ordinary node without a local bootnode/tools build."
  }
  assert {
    condition     = contains(docker_container.node["node1"].env, "BOOTSTRAP_ENODES=${join(",", var.bootstrap_enodes)}") && contains(docker_container.node["node1"].command, "--syncmode=full")
    error_message = "Default node must receive live-network seeds and full synchronization."
  }
}

run "mining_node" {
  command = plan
  variables {
    deployment_mode = "miner"
    miner_addresses = ["0x1111111111111111111111111111111111111111"]
  }
  assert {
    condition     = contains(docker_container.node["node1"].command, "--mine") && length(docker_container.bootnode) == 0 && !docker_network.nodes.internal
    error_message = "Miner must sync over external P2P, enable mining explicitly and not start a local bootnode."
  }
}

run "bootnode_plus_node" {
  command = plan
  variables {
    deployment_mode  = "bootnode"
    advertise_ip     = "203.0.113.20"
    bootstrap_enodes = []
  }
  assert {
    condition     = length(docker_container.bootnode) == 1 && length(docker_container.node) == 1 && length(docker_volume.bootnode_identity) == 1 && length(docker_volume.bootnode_discovery) == 1
    error_message = "bootnode profile needs a bootnode, persistent random identity and its node."
  }
  assert {
    condition     = !strcontains(docker_container.bootnode[0].command[0], "--nodekeyhex") && strcontains(docker_container.bootnode[0].command[0], "--genkey=/data/nodekey")
    error_message = "Never publish a bootnode using a hardcoded private key."
  }
  assert {
    condition     = length(docker_container.bootnode[0].ports) == 1 && docker_container.bootnode[0].ports[0].protocol == "udp" && contains(docker_container.node["node1"].env, "BOOTNODE_HOST=172.29.82.2")
    error_message = "Publish bootnode discovery and connect its node through the public-only identity volume."
  }
}

run "reject_unseeded_default_node" {
  command = plan
  variables { bootstrap_enodes = [] }
  expect_failures = [terraform_data.settings]
}

run "reject_unseeded_miner" {
  command = plan
  variables {
    deployment_mode  = "miner"
    miner_addresses  = ["0x1111111111111111111111111111111111111111"]
    bootstrap_enodes = []
  }
  expect_failures = [terraform_data.settings]
}

run "reject_miner_without_reward" {
  command = plan
  variables { deployment_mode = "miner" }
  expect_failures = [terraform_data.settings]
}

run "reject_bootnode_without_public_ip" {
  command = plan
  variables { deployment_mode = "bootnode" }
  expect_failures = [terraform_data.settings]
}

run "reject_http_instead_of_enode" {
  command = plan
  variables { bootstrap_enodes = ["http://pub1.aplocoin.com/"] }
  expect_failures = [var.bootstrap_enodes]
}

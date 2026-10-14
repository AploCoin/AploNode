mock_provider "docker" {}

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
    condition     = length(docker_container.node["node1"].ports) == 0 && !contains(docker_container.node["node1"].command, "--mine")
    error_message = "Nodes must not publish direct RPC/P2P or enable mining by default."
  }
  assert {
    condition     = docker_container.gateway.ports[0].ip == "127.0.0.1" && docker_network.nodes.internal
    error_message = "Local mode must bind loopback and isolate the peer network."
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
    miner_addresses   = ["0x1111111111111111111111111111111111111111"]
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
    condition     = contains(docker_container.node["node1"].command, "--mine") && !contains(docker_container.node["node2"].command, "--mine")
    error_message = "Mining must only be enabled for explicitly configured nodes."
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

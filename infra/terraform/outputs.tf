output "rpc_url" {
  description = "RPC endpoint; with HTTPS, wait for Certbot issuance and use an allowed client IP."
  value       = local.tls ? "https://${var.rpc_domain}" : "http://localhost:${var.http_port}"
}

output "websocket_url" {
  value = local.tls ? "wss://${var.rpc_domain}/ws" : "ws://localhost:${var.http_port}/ws"
}

output "containers" {
  value = concat([docker_container.bootnode.name], [for node in docker_container.node : node.name], [docker_container.gateway.name], local.tls ? [docker_container.certbot[0].name] : [])
}

output "data_volumes" {
  value = { for key, volume in docker_volume.node_data : key => volume.name }
}

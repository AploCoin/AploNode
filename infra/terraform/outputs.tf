output "rpc_url" {
  description = "RPC endpoint; with HTTPS, wait for Certbot issuance and use an allowed client IP."
  value       = local.tls ? "https://${var.rpc_domain}" : "http://localhost:${var.http_port}"
}

output "websocket_url" {
  value = local.tls ? "wss://${var.rpc_domain}/ws" : "ws://localhost:${var.http_port}/ws"
}

output "containers" {
  value = concat([for bootnode in docker_container.bootnode : bootnode.name], [for node in docker_container.node : node.name], [docker_container.gateway.name], local.tls ? [docker_container.certbot[0].name] : [])
}

output "data_volumes" {
  value = { for key, volume in docker_volume.node_data : key => volume.name }
}

output "deployment_mode" { value = var.deployment_mode }
output "bootstrap_enodes" { value = var.bootstrap_enodes }
output "bootnode_enode_command" {
  description = "Run on the Docker host after deployment to print the public enode. Private identity stays in its volume."
  value = local.with_bootnode ? trimspace(<<-COMMAND
    printf 'enode://%s@${var.advertise_ip}:30301\n' "$(docker exec ${var.name}-bootnode bootnode --nodekey=/data/nodekey --writeaddress)"
  COMMAND
  ) : null
}

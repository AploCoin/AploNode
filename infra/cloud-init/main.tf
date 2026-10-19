terraform {
  required_version = ">= 1.9.8, < 2.0.0"
}

variable "source_commit" {
  description = "Reviewed immutable AploNode revision, not a branch or tag."
  type        = string
  validation {
    condition     = can(regex("^[0-9a-f]{40}$", var.source_commit))
    error_message = "source_commit must be a full lowercase 40-character commit SHA."
  }
}

variable "repository_url" {
  type    = string
  default = "https://github.com/AploCoin/AploNode.git"
  validation {
    condition     = can(regex("^https://github[.]com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+([.]git)?$", var.repository_url))
    error_message = "Use an HTTPS GitHub repository URL."
  }
}

variable "deployment" {
  description = "Terraform deployment settings. node is the default; bootstrap needs P2P enodes. Optional genesis_json embeds the network's genesis. No credentials/private keys."
  type        = any
  validation {
    condition     = can(keys(var.deployment))
    error_message = "deployment must be a settings object."
  }
  validation {
    condition     = contains(["node", "miner", "bootnode"], try(var.deployment.deployment_mode, "node"))
    error_message = "Choose node, miner or bootnode."
  }
  validation {
    condition     = try(var.deployment.deployment_mode, "node") == "bootnode" ? try(var.deployment.advertise_ip != "", false) : try(length(var.deployment.bootstrap_enodes) > 0, false)
    error_message = "node/miner require bootstrap_enodes; bootnode requires advertise_ip."
  }
}

output "user_data" {
  description = "Pass directly to a VPS provider's user_data/cloud-init field. This module creates no VM and makes no paid API requests."
  value       = <<-YAML
    #cloud-config
    write_files:
      - path: /root/aplonode-bootstrap.sh
        permissions: '0700'
        encoding: b64
        content: ${filebase64("${path.module}/../terraform/bootstrap.sh")}
      - path: /root/aplonode.tfvars.json
        permissions: '0600'
        encoding: b64
        content: ${base64encode(jsonencode(var.deployment))}
    runcmd:
      - ${jsonencode(["sh", "/root/aplonode-bootstrap.sh", "/root/aplonode.tfvars.json", var.source_commit, var.repository_url])}
  YAML
}

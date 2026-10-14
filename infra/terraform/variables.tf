variable "name" {
  description = "Unique deployment prefix; changing it replaces resources."
  type        = string
  default     = "aplo-tf"
  validation {
    condition     = can(regex("^[a-z][a-z0-9-]{0,39}$", var.name))
    error_message = "Use a lowercase Docker name, up to 40 characters."
  }
}

variable "node_count" {
  description = "Number of Aplo nodes sharing the built-in native genesis."
  type        = number
  default     = 1
  validation {
    condition     = var.node_count >= 1 && var.node_count <= 4 && floor(var.node_count) == var.node_count
    error_message = "node_count must be an integer from 1 to 4."
  }
}

variable "node_subnet" {
  description = "Private /24 Docker subnet; choose another if this overlaps an existing network."
  type        = string
  default     = "172.29.82.0/24"
  validation {
    condition     = can(cidrnetmask(var.node_subnet)) && endswith(var.node_subnet, "/24")
    error_message = "node_subnet must be an IPv4 /24 CIDR."
  }
}

variable "miner_addresses" {
  description = "Optional reward addresses: enables mining on the first N nodes. No account is unlocked."
  type        = list(string)
  default     = []
  validation {
    condition = alltrue([
      for address in var.miner_addresses : can(regex("^0x[0-9a-fA-F]{40}$", address)) &&
      !contains(["0x0000000000000000000000000000000000000000", "0x0000000000000000000000000000000000001234", "0x0000000000000000000000000000000000001235", "0x0000000000000000000000000000000000001236", "0x0000000000000000000000000000000000001237"], lower(address))
    ])
    error_message = "Supply nonzero, nonreserved 0x-prefixed reward addresses."
  }
}

variable "rpc_domain" {
  description = "Empty for local HTTP; a public DNS name enables Nginx HTTPS and Certbot."
  type        = string
  default     = ""
  validation {
    condition     = var.rpc_domain == "" || (length(var.rpc_domain) <= 253 && can(regex("^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$", var.rpc_domain)))
    error_message = "Use a lowercase DNS name without scheme, port, path or wildcard."
  }
}

variable "acme_email" {
  description = "Let's Encrypt account email, required with rpc_domain."
  type        = string
  default     = ""
  validation {
    condition     = var.acme_email == "" || can(regex("^[^[:space:]@]+@[^[:space:]@]+\\.[^[:space:]@]+$", var.acme_email))
    error_message = "acme_email must be an email address."
  }
}

variable "acme_staging" {
  description = "Use Let's Encrypt staging for testing; its certificates are not browser-trusted."
  type        = bool
  default     = false
}

variable "bind_ip" {
  description = "Nginx host IPv4 binding. Default loopback; public bindings require HTTPS and an RPC allowlist."
  type        = string
  default     = "127.0.0.1"
  validation {
    condition     = can(cidrnetmask("${var.bind_ip}/32"))
    error_message = "bind_ip must be an IPv4 address."
  }
}

variable "http_port" {
  description = "Host HTTP port; use 80 for public ACME HTTP-01 validation."
  type        = number
  default     = 8080
  validation {
    condition     = var.http_port >= 1 && var.http_port <= 65535 && floor(var.http_port) == var.http_port
    error_message = "http_port must be an integer from 1 to 65535."
  }
}

variable "https_port" {
  description = "Host HTTPS port, used only with rpc_domain."
  type        = number
  default     = 8443
  validation {
    condition     = var.https_port >= 1 && var.https_port <= 65535 && floor(var.https_port) == var.https_port
    error_message = "https_port must be an integer from 1 to 65535."
  }
}

variable "rpc_allowed_cidrs" {
  description = "Nginx client IP allowlist. Empty is allowed only on loopback; applies to HTTP RPC and WebSocket, not ACME challenges."
  type        = list(string)
  default     = []
  validation {
    condition     = alltrue([for cidr in var.rpc_allowed_cidrs : can(cidrnetmask(cidr)) && !endswith(cidr, "/0")])
    error_message = "Use specific IPv4 CIDRs, not an unrestricted /0."
  }
}

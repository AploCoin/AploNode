terraform {
  required_version = ">= 1.9.8, < 2.0.0"

  required_providers {
    docker = {
      source  = "kreuzwerker/docker"
      version = "3.6.2"
    }
  }
}

# Uses DOCKER_HOST / Docker's default socket. Run on the target Docker host.
provider "docker" {}

terraform {
  required_providers {
    phala = {
      source = "phala-network/phala"
    }
    random = {
      source = "hashicorp/random"
    }
  }
}

provider "phala" {}

resource "random_id" "suffix" {
  byte_length = 4
}

variable "app_name" {
  type    = string
  default = "tf-lifecycle"
}

variable "log_level" {
  type        = string
  default     = "info"
  description = "Toggle this to test in-place env update (info → debug → info)"
}

resource "phala_app" "main" {
  name   = "${var.app_name}-${random_id.suffix.hex}"
  region = "us-west-1"
  size   = "tdx.small"
  image  = "dstack-0.5.3"

  docker_compose = <<-EOT
    services:
      hello:
        image: nginx:alpine
        environment:
          - LOG_LEVEL=$${LOG_LEVEL}
        ports:
          - "80:80"
  EOT

  env = {
    LOG_LEVEL = var.log_level
  }

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

output "app_id" {
  value = phala_app.main.app_id
}

output "primary_cvm_id" {
  value = phala_app.main.primary_cvm_id
}

output "endpoint" {
  value = phala_app.main.endpoint
}

output "status" {
  value = phala_app.main.status
}

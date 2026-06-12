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

# Regression test for the `replicas`-drop change (commit 89c413b):
# previously the schema required `replicas`; the new schema omits it entirely.
# This config must plan + apply cleanly with NO `replicas` attribute anywhere.

resource "phala_app" "noreplicas" {
  name   = "tf-noreplicas-${random_id.suffix.hex}"
  region = "us-west-1"
  size   = "tdx.small"
  image  = "dstack-0.5.3"

  docker_compose = <<-EOT
    services:
      hello:
        image: nginx:alpine
        ports:
          - "80:80"
  EOT

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

output "app_id" {
  value = phala_app.noreplicas.app_id
}

output "cvm_ids" {
  value = phala_app.noreplicas.cvm_ids
}

output "instances_count" {
  value = length(phala_app.noreplicas.instances)
}

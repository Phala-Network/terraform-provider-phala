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

locals {
  # The bootstrap slot's name MUST equal one of the members.
  primary_slot = "primary"
  members      = ["primary", "replica-a", "replica-b"]
}

# --- The MIG-style app: phala_app holds the bootstrap CVM (slot "primary"),
#     phala_app_instance.for_each holds the other slots. ---

resource "phala_app" "cluster" {
  name    = local.primary_slot     # MUST match a member
  members = local.members

  region = "us-west-1"
  size   = "tdx.small"
  image  = "dstack-0.5.3"

  docker_compose = <<-EOT
    services:
      worker:
        image: nginx:alpine
        environment:
          - SLOT=$${SLOT_NAME}
  EOT

  env = {
    SLOT_NAME = local.primary_slot
  }

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

# The bootstrap CVM is adopted (managed = false). Created slots use the same
# compose, with a per-slot SLOT_NAME env override.

resource "phala_app_instance" "replicas" {
  for_each = toset([for m in local.members : m if m != local.primary_slot])

  app_id = phala_app.cluster.app_id
  name   = each.value

  docker_compose = phala_app.cluster.docker_compose

  env = {
    SLOT_NAME = each.value
  }

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

output "primary_slot_cvm_id" {
  value = phala_app.cluster.primary_cvm_id
}

output "all_cvm_ids" {
  value = phala_app.cluster.cvm_ids
}

output "replica_instances" {
  value = {
    for k, v in phala_app_instance.replicas : k => {
      cvm_id   = v.id
      vm_uuid  = v.vm_uuid
      managed  = v.managed
      endpoint = v.endpoint
    }
  }
}

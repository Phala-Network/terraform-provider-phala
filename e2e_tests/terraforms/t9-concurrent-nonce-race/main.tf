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

variable "parallel_count" {
  type        = number
  default     = 4
  description = "Number of phala_app resources provisioned concurrently. >=2 can trigger the race on a pre-fix build; 4 is reliable. Each one is a REAL tdx.small CVM — destroy promptly. Bump higher (8/16) for stress test."
}

# Issue #1544 / PR #1622: obtain_app_id() races on the per-team-wallet nonce.
# Full-path test: real phala_app resources walk all the way through
# ProvisionCVM (the racing call) → CommitCVMProvision → CVM boot.
# This exercises the race AND the end-to-end provisioning path so a pass
# means clients can deploy in parallel with no hidden contention surfaces.
#
# Cheap variant (no CVM, no cost): swap `phala_app` → `phala_app_preflight`
# and drop both `wait_for_ready` and `wait_timeout_seconds`. The race lives
# in ProvisionCVM, which preflight also calls, so the cheap variant still
# catches it — see commentary in the README.

resource "phala_app" "race" {
  count = var.parallel_count

  name   = "tf-nonce-race-${random_id.suffix.hex}-${count.index}"
  region = "US-WEST-1"
  size   = "tdx.small"
  image  = "dstack-0.5.9"
  kms    = "phala"

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

output "app_ids" {
  value = [for r in phala_app.race : r.app_id]
}

output "cvm_ids" {
  value = [for r in phala_app.race : r.primary_cvm_id]
}

output "endpoints" {
  value = [for r in phala_app.race : r.endpoint]
}

output "statuses" {
  value = [for r in phala_app.race : r.status]
}

output "distinct_app_ids_count" {
  # MUST equal expected_count post-fix. Less than that == app_id collision == regression.
  value = length(toset([for r in phala_app.race : r.app_id]))
}

output "expected_count" {
  value = var.parallel_count
}

output "addresses_unique" {
  # Convenience boolean: true iff every phala_app got a distinct app_id.
  value = length(toset([for r in phala_app.race : r.app_id])) == var.parallel_count
}

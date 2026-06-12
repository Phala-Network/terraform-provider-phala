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
  app_name       = "tf-cross-${random_id.suffix.hex}"
  region         = "us-west-1"
  size           = "tdx.small"
  image          = "dstack-0.5.3"
  docker_compose = <<-EOT
    services:
      hello:
        image: nginx:alpine
        ports:
          - "80:80"
  EOT
}

# Preflight only — no CVM created.
data "phala_app_preflight" "pre" {
  name           = local.app_name
  region         = local.region
  size           = local.size
  image          = local.image
  docker_compose = local.docker_compose
}

# Real create — must produce the same compose_hash + app_env_encrypt_pubkey
# (within the determinism guarantees the API offers).
resource "phala_app" "real" {
  name           = local.app_name
  region         = local.region
  size           = local.size
  image          = local.image
  docker_compose = local.docker_compose

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

output "preflight_compose_hash" {
  value = data.phala_app_preflight.pre.compose_hash
}

output "real_compose_hash" {
  value = phala_app.real.compose_hash
}

output "preflight_app_env_encrypt_pubkey" {
  value = data.phala_app_preflight.pre.app_env_encrypt_pubkey
}

output "real_app_env_encrypt_pubkey" {
  value = phala_app.real.app_env_encrypt_pubkey
}

# Convenience: ASCII diff in the output so you don't need to eyeball.
output "compose_hashes_match" {
  value = data.phala_app_preflight.pre.compose_hash == phala_app.real.compose_hash
}

output "encrypt_pubkeys_match" {
  value = data.phala_app_preflight.pre.app_env_encrypt_pubkey == phala_app.real.app_env_encrypt_pubkey
}

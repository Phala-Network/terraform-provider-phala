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

# --- Data source: pure-read preflight (no resource state) ---
# Returns provisioning artifacts (compose_hash, env_encrypt_pubkey, KMS info)
# without creating anything. Exercises ProvisionCVM but NOT CommitCVMProvision.

data "phala_app_preflight" "ds" {
  name   = "tf-preflight-ds-${random_id.suffix.hex}"
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
}

# --- Resource: same call, but persists the preflight output to state ---

resource "phala_app_preflight" "res" {
  name   = "tf-preflight-res-${random_id.suffix.hex}"
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
}

output "ds_compose_hash" {
  value = data.phala_app_preflight.ds.compose_hash
}

output "ds_app_env_encrypt_pubkey" {
  value = data.phala_app_preflight.ds.app_env_encrypt_pubkey
}

output "ds_kms_id" {
  value = data.phala_app_preflight.ds.kms_id
}

output "ds_matched_node_id" {
  value = data.phala_app_preflight.ds.matched_node_id
}

output "res_compose_hash" {
  value = phala_app_preflight.res.compose_hash
}

output "res_kms_info_json" {
  value = phala_app_preflight.res.kms_info_json
}

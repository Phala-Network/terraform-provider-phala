terraform {
  required_providers {
    phala = {
      source = "phala-network/phala"
    }
  }
}

provider "phala" {}

variable "ssh_public_key" {
  type        = string
  description = "OpenSSH-format public key. Generate with: ssh-keygen -t ed25519 -f /tmp/tf-test-key -N ''"
}

variable "ssh_key_name" {
  type    = string
  default = "tf-manual-test-key"
}

resource "phala_ssh_key" "test" {
  name       = var.ssh_key_name
  public_key = var.ssh_public_key
}

output "ssh_key_id" {
  value = phala_ssh_key.test.id
}

output "ssh_key_fingerprint" {
  value = phala_ssh_key.test.fingerprint
}

output "ssh_key_type" {
  value = phala_ssh_key.test.key_type
}

output "ssh_key_source" {
  value = phala_ssh_key.test.source
}

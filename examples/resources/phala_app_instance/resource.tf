# Stateful replica set on Phala Cloud, MIG-style.
#
# phala_app declares the full slot list via `members`. The provider validates
# at plan time that `name` is one of `members` and that `replicas` is not >1.
# phala_app_instance iterates over `phala_app.consul.members` so the slot list
# stays a single source of truth — invariant by construction.
#
# The instance whose name matches phala_app.consul.name "adopts" the
# bootstrap CVM (no API call, just a state binding). The others post to
# /apps/{id}/instances. Terraform ID for each slot is "<app_id>:<name>" —
# durable; survives CVM replacement.

resource "phala_app" "consul" {
  name    = "consul-0"
  members = ["consul-0", "consul-1", "consul-2"]

  size      = "tdx.small"
  region    = "US-WEST-1"
  image     = "dstack-dev-0.5.7-9b6a5239"
  disk_size = 40

  gateway_enabled = true

  docker_compose = file("${path.module}/consul-compose.yaml")

  # The bootstrap CVM is owned by phala_app, so its per-slot env is declared here.
  # This also adds CVM_SLOT_NAME to the app compose allowed_envs list, letting
  # managed phala_app_instance slots override it with their own encrypted values.
  env = {
    CVM_SLOT_NAME = "consul-0"
  }

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

resource "phala_app_instance" "consul" {
  for_each = toset(phala_app.consul.members)

  app_id = phala_app.consul.app_id
  name   = each.value

  # The bootstrap slot ("consul-0") is adopted from phala_app and cannot be
  # mutated here. Extra slots are created by phala_app_instance and receive
  # their own encrypted per-slot env at create time.
  env = each.value == phala_app.consul.name ? null : {
    CVM_SLOT_NAME = each.value
  }

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

output "consul_member_uuids" {
  description = "Stable slot -> current vm_uuid map. The slot key is durable; vm_uuid may change on replacement."
  value       = { for k, v in phala_app_instance.consul : k => v.vm_uuid }
}

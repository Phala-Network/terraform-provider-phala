resource "phala_app" "coordinators" {
  name           = "coord-0"
  members        = ["coord-0", "coord-1", "coord-2"]
  size           = "tdx.small"
  docker_compose = file("coordinator.yaml")
  env_keys       = ["BROKERS", "ORDINAL"]
  wait_for_ready = false
}

resource "phala_app_instance" "coordinator" {
  for_each = toset(["coord-0", "coord-1", "coord-2"])

  app_id            = phala_app.coordinators.app_id
  name              = each.key
  wait_for_ready    = false
  wait_for_identity = true
}

resource "phala_cvm_env" "coordinator" {
  for_each = toset(["coord-0", "coord-1", "coord-2"])

  cvm_id = phala_app_instance.coordinator[each.key].vm_uuid
  env = {
    BROKERS = jsonencode([
      for peer in phala_app_instance.coordinator :
      "https://${trimprefix(peer.instance_id, "0x")}-7000.${peer.gateway_base_domain}"
    ])
    ORDINAL = trimprefix(each.key, "coord-")
  }
}

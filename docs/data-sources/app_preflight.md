# phala_app_preflight (Data Source)

Runs Phala Cloud app provision/preflight and returns the compose hash without
committing a CVM deployment.

The data source sends the same provision request shape used by `phala_app`
create. Phala Cloud normalizes the app compose, computes `compose_hash`, caches
the preflight result for later commit, and returns provisioning metadata. This
does not create a CVM, but PHALA KMS preflight can reserve/reuse an app id.

When another managed resource depends on the preflight result, prefer the
`phala_app_preflight` resource form. Terraform refreshes data sources during
destroy, while the resource form stores the generated preflight artifact in
state and deletes it locally without re-running preflight.

## Example Usage

```terraform
data "phala_app_preflight" "coordinator_0" {
  name      = "demo-coordinator-0"
  size      = "tdx.small"
  region    = "US-WEST-1"
  disk_size = 20

  docker_compose = file("${path.module}/../compose/coordinator.yaml")

  env = {
    CLUSTER_NAME = var.cluster_name
    PEERS_JSON   = local.peers_json
  }

  listed         = false
  public_logs    = true
  public_sysinfo = false
  storage_fs     = "zfs"
}

output "coordinator_0_compose_hash" {
  value = data.phala_app_preflight.coordinator_0.compose_hash
}
```

## Schema

### Required

- `docker_compose` (String) Docker Compose YAML content.
- `name` (String) App/CVM name included in the app compose.
- `size` (String) Instance type (e.g. tdx.small).

### Optional

- `custom_app_id` (String) Optional custom app_id for deterministic identity flow.
- `disk_size` (Number) Disk size in GB.
- `env` (Map of String, Sensitive) Plaintext env vars. Only keys enter the app compose `allowed_envs` list.
- `env_keys` (List of String) Allowed environment variable keys used when env values are not provided.
- `gateway_enabled` (Boolean) Enable public gateway routing.
- `image` (String) OS image name.
- `kms` (String) KMS type for app provisioning. Defaults to `phala` when omitted.
- `listed` (Boolean) Whether the resource should be publicly listed. Defaults to false when omitted.
- `node_id` (Number) Optional target node (teepod) ID for placement.
- `nonce` (Number) Optional nonce paired with custom_app_id for PHALA KMS deterministic app_id flow.
- `pre_launch_script` (String) Optional pre-launch script content.
- `public_logs` (Boolean) Expose container logs publicly.
- `public_sysinfo` (Boolean) Expose system info publicly.
- `public_tcbinfo` (Boolean) Expose TCB attestation info publicly.
- `region` (String) Preferred region identifier.
- `secure_time` (Boolean) Enable secure time mode.
- `ssh_authorized_keys` (List of String) Per-deployment SSH public keys injected at launch via user_config.
- `storage_fs` (String) Storage filesystem for deployment (`zfs` or `ext4`).

### Read-Only

- `app_env_encrypt_pubkey` (String) Public key used for app environment encryption.
- `app_id` (String) Preflight app identifier returned by Phala Cloud.
- `compose_hash` (String) SHA-256 hash of the normalized app compose file returned by Phala Cloud preflight.
- `device_id` (String)
- `fmspc` (String)
- `id` (String) Stable data source ID (same as compose_hash).
- `instance_type` (String)
- `kms_id` (String)
- `kms_info_json` (String) Raw KMS info object as JSON.
- `matched_node_id` (Number) Matched teepod/node ID returned by preflight, when present.
- `os_image_hash` (String)
- `raw_json` (String) Full provision response as JSON.

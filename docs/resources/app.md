---
page_title: "phala_app Resource - phala"
subcategory: ""
description: |-
  Manages a Phala Cloud App (app_id + shared compose/env + replica count).
---

# phala_app (Resource)

Manages a Phala Cloud App (app_id + shared compose/env + replica count).

## Example Usage

```terraform
resource "phala_app" "web" {
  name      = "web-app"
  size      = "tdx.medium"
  region    = "US-WEST-1"
  image     = "dstack-dev-0.5.7-9b6a5239"
  disk_size = 40

  env = {
    APP_SECRET = "replace-me"
  }

  public_logs     = false
  public_sysinfo  = false
  public_tcbinfo  = false
  gateway_enabled = true

  docker_compose = <<-YAML
    services:
      web:
        image: nginx:stable
        ports:
          - "80:80"
  YAML

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

# The cloud's gateway DNS suffix is exposed as a computed attribute, so
# downstream URLs can be assembled without hardcoding the environment-specific
# domain. Each container port reachable via the gateway is published at
# https://<app_id>-<port>.<gateway_base_domain>.
output "web_url" {
  value = "https://${phala_app.web.app_id}-80.${phala_app.web.gateway_base_domain}"
}
```

## Behavior Notes

- `phala_app` is the app identity + the **bootstrap CVM**. Every Phala app is born with exactly one CVM (the cloud API has no "empty app" endpoint).
- For stateful replica sets with per-member identity, declare `members` and pair `phala_app` with `phala_app_instance`. See [`phala_app_instance`](app_instance.md) for the full pattern. In single-CVM apps, leave `members` unset.
- `docker_compose`, runtime visibility flags, OS image, encrypted env, and instance size are mutable in place on a single-CVM app — they target the bootstrap CVM via per-CVM PATCH endpoints.
- `instances` exposes the current per-CVM view (bootstrap + any `phala_app_instance` slots).
- `wait_for_ready = true` waits until the bootstrap CVM reports running before returning.
- For values that depend on allocated instance identities, declare `env_keys` without inline values and use [`phala_cvm_env`](cvm_env.md) after allocation. Allocate with `wait_for_ready = false`; the separate environment resource waits after configuration.
- `storage_fs`, placement fields, and deterministic identity inputs can affect replacement behavior; check the schema details below before changing them in-place.
- SSH access is account-scoped: register keys with the [`phala_ssh_key`](ssh_key.md) resource. Keys on your account are injected into CVMs at launch — there is no per-app SSH key field.

## MIG-mode validation

When `members` is set, the provider enforces at plan time that `name` is one of `members` — otherwise the bootstrap CVM has nothing to adopt it and would become an unreferenced extra under the app.

Downstream `phala_app_instance` resources should set `for_each = toset(phala_app.foo.members)` so the slot list stays a single source of truth.

## In-place updates in members mode

`phala_app.Update` propagates mutable-field changes across every CVM in a members-mode app while preserving slot identity (`vm_uuid` and `name` are unchanged):

- **Compose changes** (`docker_compose`, `pre_launch_script`, `public_*`, `gateway_enabled`, `secure_time`, env-key list changes, the compose settings below, `init_scripts`) go through the cloud's app-revision endpoint. The provider applies them to the bootstrap CVM with one `PATCH /cvms/{id}` that sends only the changed keys (creating a new revision row), then `POST /apps/{id}/revisions/{rev}/redeploy` fans the same revision out to every other slot. Each CVM's `compose_hash` flips to the new value; nothing is destroyed and recreated.
- **Env value changes** use a per-CVM `PATCH /cvms/{uuid}/envs` fan-out by default. Set `env_update_scope = "bootstrap"` when `phala_app_instance` resources own the other slots' environments. The parent then updates only its bootstrap CVM and leaves per-slot values intact. `compose_hash` stays unchanged.
- **OS image, instance size, and disk size changes** fan out via `PATCH /cvms/{uuid}/os-image` and `PATCH /cvms/{uuid}/resources` respectively — the cloud has no app-level analog. Sequential, fail-fast.

The one structural change still blocked is **removing the `members` attribute** from a previously-members-mode app: that would leave the `phala_app_instance` slots orphaned. The provider blocks this at plan time and asks you to destroy and recreate.

## Compose settings, init scripts and annotations

- Every compose change of one apply is sent as a single `PATCH /cvms/{id}` whose `compose` object is a JSON Merge Patch: only changed keys are sent, a removed attribute is sent as `null`, and compose keys the provider does not manage stay as they are.
- `runner`, `bash_script` and `requirements.gpu_policy` cannot be changed after create; changing them replaces the app. `storage_fs` was already force-new.
- `port_policy`, `swap_size`, `storage_discard`, `snapshotter`, `event_log_version`, `gateway_enabled`, `secure_time`, the other `requirements` keys and `init_scripts` update in place with a restart. Turning on `init_scripts` `docker_cleanup` or `requirements.ephemeral_data_disk` for an existing app erases data on every boot and needs `confirm_data_wipe = true`.
- These settings are kept as configured in state; the provider does not read them back from the cloud.
- `annotations` (feature-flagged) are replaced as a whole map on every CVM of the app without a restart or a compose change. They are not read back either.

### Bash runner example

```hcl
resource "phala_app" "job" {
  name           = "batch-job"
  size           = "tdx.small"
  runner         = "bash" # needs the compose_advanced feature flag
  docker_compose = ""
  bash_script    = file("${path.module}/job.sh")

  requirements = {
    health_check       = true
    health_status_file = "/run/health"
  }
}
```

## Public URL composition

The cloud serves each CVM's containers behind a per-app gateway DNS suffix. Until 0.3.0-beta.3 downstream modules had to declare this suffix as a top-level variable and hardcode it per environment; the provider now exposes it as a computed attribute on the app:

```hcl
output "webdemo_url" {
  value = "https://${phala_app.demo.app_id}-8080.${phala_app.demo.gateway_base_domain}"
}
```

`gateway_base_domain` is sourced from the cloud's `CVMGatewayInfo.base_domain` on the primary CVM. If an operator has configured a custom CNAME alias via the cloud UI, `gateway_cname` carries it; otherwise it is empty. The same two fields are also published per-instance under `phala_app.instances[*]` and on every `phala_app_instance` resource, so per-slot URLs work the same way in members-mode.

## Migration from 0.2.x

The `replicas` attribute was removed in 0.3.0-beta.1. Any HCL setting `replicas` will fail validation. To migrate a multi-replica 0.2.x app, declare the slot names in `members` and add a `phala_app_instance` `for_each` over them. See [CHANGELOG](../../CHANGELOG.md#030-beta1---2026-05-18) for the recipe.

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `docker_compose` (String) Docker Compose YAML content.
- `name` (String) Resource name. Force-new.
- `size` (String) Instance type (e.g. tdx.small).

### Optional

- `annotations` (Map of String) Host-side key/value metadata the dstack VMM stores for each CVM of the app (dstack VMM 0.6.1+). Not measured: tenants must not rely on it for security. Needs the `cvm_annotations` feature flag. Updated in place by replacing the whole map, without a restart; host tools that read annotations when the CVM starts see the change after the next restart. Removing the attribute clears the annotations.
- `bash_script` (String) Script the `bash` runner executes instead of a Docker Compose file; set `docker_compose = ""` with it. Changing this forces replacement.
- `confirm_data_wipe` (Boolean) Confirms an update that turns on `init_scripts` `docker_cleanup` or `requirements.ephemeral_data_disk`, which erase data on every boot. Not needed on create.
- `custom_app_id` (String) Optional custom app_id for deterministic identity flow. Changing this forces replacement.
- `disk_size` (Number) Disk size in GB.
- `encrypted_env` (String, Sensitive) Hex-encoded encrypted env payload (manual mode).
- `env` (Map of String) Plaintext env vars. Provider auto-derives env_keys and encrypts values before API submission. Plaintext still exists in Terraform state. Mark sensitive values at the variable level rather than the schema level (see Phala-Network/phala-cloud#246: schema-level Sensitive on a Map causes Terraform Core to suppress in-place env diffs).
- `env_compose_hash` (String) Optional compose hash for phase-2 encrypted env update flow (contract-owned KMS; used with env_transaction_hash).
- `env_keys` (List of String) Measured allowed environment variable keys. Use with encrypted_env, or declare keys alone when phala_cvm_env manages values separately. Do not combine with inline env.
- `env_transaction_hash` (String) Optional on-chain transaction hash for phase-2 encrypted env update flow (contract-owned KMS; used with env_compose_hash).
- `env_update_scope` (String) Target of environment value updates: `all` (default) or `bootstrap`. Use `bootstrap` when phala_app_instance resources own per-slot environments; the parent then updates only its own bootstrap CVM. This does not change compose revision rollout scope.
- `event_log_version` (Number) Event log format version, `1` or `2`. Relying parties that verify this app must support the chosen version. Changing it triggers compose update/restart.
- `gateway_enabled` (Boolean) Enable public gateway routing (compose file setting). Changing this triggers compose update/restart.
- `image` (String) OS image name.
- `init_scripts` (Set of String) Platform init scripts every CVM of the app runs on boot: `docker_cleanup` (erases all container images and volumes on every boot) and `env_merge` (feature-flagged). Changing it triggers compose update/restart; turning `docker_cleanup` on for an existing app also needs `confirm_data_wipe = true`. Removing the attribute removes every script.
- `kms` (String) KMS type for app provisioning (`phala`, `ethereum`, `base`). Changing this forces replacement.
- `listed` (Boolean) Whether the app's CVM(s) are publicly listed in the marketplace. Updated in place via `PATCH /cvms/{uuid}/listed` (a plain metadata flag — no redeploy, no restart, no attestation change), fanned out across every slot in members mode.
- `members` (List of String) Optional list of stable slot names this app's replica set is composed of (MIG-style usage). When set, `name` must be one of these values, and `replicas` must be unset or 1 — the legacy anonymous-replica path is incompatible with named slots. Downstream `phala_app_instance` resources should derive their `for_each` from this attribute so the slot list is the single source of truth.
- `node` (String) Physical node name for initial placement. When omitted on creation, placement is automatic. Omitting it later retains the stored placement. Changing the name forces replacement.
- `nonce` (Number) Optional nonce paired with custom_app_id for PHALA KMS deterministic app_id flow. Changing this forces replacement.
- `port_policy` (Attributes) Which guest ports the dstack gateway forwards (compose `port_policy`, dstack OS 0.5.9+). Changing it triggers compose update/restart. (see [below for nested schema](#nestedatt--port_policy))
- `pre_launch_script` (String) Optional pre-launch script content.
- `public_logs` (Boolean) Expose container logs publicly (compose file setting). Changing this triggers compose update/restart.
- `public_sysinfo` (Boolean) Expose system info publicly (compose file setting). Changing this triggers compose update/restart.
- `public_tcbinfo` (Boolean) Expose TCB attestation info publicly (compose file setting). Changing this triggers compose update/restart.
- `region` (String) Preferred region identifier. Force-new.
- `requirements` (Attributes) Guest requirements (compose `requirements`, dstack OS 0.6.0+). Only the keys you set are written to the compose. Changing a key other than `gpu_policy` triggers compose update/restart. (see [below for nested schema](#nestedatt--requirements))
- `runner` (String) How the guest runs the app: `docker-compose` (default), `nerdctl-compose` (dstack OS 0.6.0+) or `bash`. Runners other than `docker-compose` need the `compose_advanced` feature flag. Changing this forces replacement.
- `secure_time` (Boolean) Enable secure time mode (compose file setting). Changing this triggers compose update/restart.
- `snapshotter` (String) Container snapshotter, `overlayfs` or `stargz`. Only with `runner = "nerdctl-compose"`. Changing it triggers compose update/restart.
- `storage_discard` (Boolean) Pass discard (TRIM) through to the data disk. Changing it triggers compose update/restart.
- `storage_fs` (String) Storage filesystem for deployment (`zfs` or `ext4`). Immutable after initial deployment.
- `swap_size` (String) Guest swap size, e.g. `2G`. Changing it triggers compose update/restart.
- `wait_for_ready` (Boolean) Wait until status is running after create/update.
- `wait_timeout_seconds` (Number) Wait timeout for async operations.

### Read-Only

- `app_env_encrypt_pubkey` (String) Public key used for app environment encryption.
- `app_id` (String) Phala app identifier.
- `compose_hash` (String) SHA-256 hash of the normalized app compose file returned by Phala Cloud provision.
- `cvm_ids` (List of String) Identifiers of every CVM currently attached to this app (bootstrap plus any `phala_app_instance`s).
- `endpoint` (String) Primary public endpoint URL.
- `gateway_base_domain` (String) Phala Cloud gateway base domain serving this app (e.g. `dstack-pha-prod5.phala.network`). Compose public URLs as `https://<app_id>-<port>.<gateway_base_domain>` without having to predict the value. Sourced from the cloud's `CVMGatewayInfo.base_domain` on the primary CVM.
- `gateway_cname` (String) Operator-configured CNAME alias for this app's gateway, if one has been set via the cloud UI. Empty when not configured. Sourced from `CVMGatewayInfo.cname` on the primary CVM.
- `id` (String) Terraform ID (same as app_id).
- `instances` (Attributes List) Computed per-instance view of CVMs currently attached to this app. (see [below for nested schema](#nestedatt--instances))
- `legacy_node_id` (Number) Internal state migration field for a legacy teepod ID.
- `primary_cvm_id` (String) Bootstrap CVM identifier — the CVM created by `phala_app` itself, which in members (MIG) mode is also the slot whose name equals `phala_app.name`. Use env_update_scope to select the target scope of environment value updates.
- `status` (String) Current CVM status.

<a id="nestedatt--port_policy"></a>
### Nested Schema for `port_policy`

Optional:

- `ports` (Attributes List) Ports the gateway forwards. (see [below for nested schema](#nestedatt--port_policy--ports))
- `restrict_mode` (Boolean) Forward only the listed ports. Unset means `false`.

<a id="nestedatt--port_policy--ports"></a>
### Nested Schema for `port_policy.ports`

Required:

- `port` (Number) Guest port.

Optional:

- `pp` (Boolean) Send the PROXY protocol header to this port. Unset means `false`.



<a id="nestedatt--requirements"></a>
### Nested Schema for `requirements`

Optional:

- `ephemeral_data_disk` (Boolean) Format the data disk on every boot (dstack OS 0.6.3+). Turning it on for an existing app also needs `confirm_data_wipe = true`.
- `gpu_policy` (Attributes) GPU policy the guest enforces. Needs the `compose_gpu_policy` feature flag. Changing this forces replacement. (see [below for nested schema](#nestedatt--requirements--gpu_policy))
- `health_check` (Boolean) Report app health to the host. With `runner = "bash"` it needs `health_status_file`.
- `health_status_file` (String) File the guest reads the app's health from.
- `platforms` (List of String) Platforms the app may run on. Unset allows any; an empty list allows none.
- `tdx_measure_acpi_tables` (Boolean) Measure the ACPI tables into the TDX RTMRs.

<a id="nestedatt--requirements--gpu_policy"></a>
### Nested Schema for `requirements.gpu_policy`

Optional:

- `allow_debug` (Boolean)
- `allow_devtools` (Boolean)
- `allow_insecure_boot` (Boolean)
- `attest_gpu` (Boolean)
- `rego` (String)



<a id="nestedatt--instances"></a>
### Nested Schema for `instances`

Read-Only:

- `app_id` (String)
- `created_at` (String)
- `endpoint` (String)
- `gateway_base_domain` (String)
- `gateway_cname` (String)
- `id` (String)
- `instance_id` (String)
- `instance_type` (String)
- `name` (String)
- `region` (String)
- `status` (String)
- `vm_uuid` (String)




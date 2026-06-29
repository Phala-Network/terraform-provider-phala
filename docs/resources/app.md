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
- `storage_fs`, placement fields, and deterministic identity inputs can affect replacement behavior; check the schema details below before changing them in-place.
- SSH access is account-scoped: register keys with the [`phala_ssh_key`](ssh_key.md) resource. Keys on your account are injected into CVMs at launch — there is no per-app SSH key field.

## MIG-mode validation

When `members` is set, the provider enforces at plan time that `name` is one of `members` — otherwise the bootstrap CVM has nothing to adopt it and would become an unreferenced extra under the app.

Downstream `phala_app_instance` resources should set `for_each = toset(phala_app.foo.members)` so the slot list stays a single source of truth.

## In-place updates in members mode

`phala_app.Update` propagates mutable-field changes across every CVM in a members-mode app while preserving slot identity (`vm_uuid` and `name` are unchanged):

- **Compose-body changes** (`docker_compose`, `pre_launch_script`, `public_*`, `gateway_enabled`, `secure_time`, env-key list changes) go through the cloud's app-revision endpoint. The provider provisions + applies on the bootstrap CVM (creating a new revision row), then `POST /apps/{id}/revisions/{rev}/redeploy` fans the same revision out to every other slot. Each CVM's `compose_hash` flips to the new value; nothing is destroyed and recreated.
- **Env value changes** (the common "rotate a secret" case) use a per-CVM `PATCH /cvms/{uuid}/envs` fan-out. The app-rooted KMS public key is shared across all slots, so the same encrypted_env bytes are accepted by every slot. `compose_hash` stays unchanged (no new revision).
- **OS image, instance size, and disk size changes** fan out via `PATCH /cvms/{uuid}/os-image` and `PATCH /cvms/{uuid}/resources` respectively — the cloud has no app-level analog. Sequential, fail-fast.

The one structural change still blocked is **removing the `members` attribute** from a previously-members-mode app: that would leave the `phala_app_instance` slots orphaned. The provider blocks this at plan time and asks you to destroy and recreate.

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

- `custom_app_id` (String) Optional custom app_id for deterministic identity flow. Changing this forces replacement.
- `disk_size` (Number) Disk size in GB.
- `encrypted_env` (String, Sensitive) Hex-encoded encrypted env payload (manual mode).
- `env` (Map of String) Plaintext env vars. Provider auto-derives env_keys and encrypts values before API submission. Plaintext still exists in Terraform state. Mark sensitive values at the variable level rather than the schema level (see Phala-Network/phala-cloud#246: schema-level Sensitive on a Map causes Terraform Core to suppress in-place env diffs).
- `env_compose_hash` (String) Optional compose hash for phase-2 encrypted env update flow (contract-owned KMS; used with env_transaction_hash).
- `env_keys` (List of String) Allowed environment variable keys used with encrypted_env/manual mode.
- `env_transaction_hash` (String) Optional on-chain transaction hash for phase-2 encrypted env update flow (contract-owned KMS; used with env_compose_hash).
- `gateway_enabled` (Boolean) Enable public gateway routing (compose file setting). Changing this triggers compose update/restart.
- `image` (String) OS image name.
- `kms` (String) KMS type for app provisioning (`phala`, `ethereum`, `base`). Changing this forces replacement.
- `listed` (Boolean) Whether the app's CVM(s) are publicly listed in the marketplace. Updated in place via `PATCH /cvms/{uuid}/listed` (a plain metadata flag — no redeploy, no restart, no attestation change), fanned out across every slot in members mode.
- `members` (List of String) Optional list of stable slot names this app's replica set is composed of (MIG-style usage). When set, `name` must be one of these values, and `replicas` must be unset or 1 — the legacy anonymous-replica path is incompatible with named slots. Downstream `phala_app_instance` resources should derive their `for_each` from this attribute so the slot list is the single source of truth.
- `node_id` (Number) Optional target node (teepod) ID for initial placement. Changing this forces replacement.
- `nonce` (Number) Optional nonce paired with custom_app_id for PHALA KMS deterministic app_id flow. Changing this forces replacement.
- `pre_launch_script` (String) Optional pre-launch script content.
- `public_logs` (Boolean) Expose container logs publicly (compose file setting). Changing this triggers compose update/restart.
- `public_sysinfo` (Boolean) Expose system info publicly (compose file setting). Changing this triggers compose update/restart.
- `public_tcbinfo` (Boolean) Expose TCB attestation info publicly (compose file setting). Changing this triggers compose update/restart.
- `region` (String) Preferred region identifier. Force-new.
- `secure_time` (Boolean) Enable secure time mode (compose file setting). Changing this triggers compose update/restart.
- `storage_fs` (String) Storage filesystem for deployment (`zfs` or `ext4`). Immutable after initial deployment.
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
- `primary_cvm_id` (String) Bootstrap CVM identifier — the CVM created by `phala_app` itself, which in members (MIG) mode is also the slot whose name equals `phala_app.name`. This is the only CVM that `phala_app` mutates directly.
- `status` (String) Current CVM status.

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




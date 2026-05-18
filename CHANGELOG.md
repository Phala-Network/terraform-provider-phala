# Changelog

All notable changes to `terraform-provider-phala` are documented in this file.

## [Unreleased]

## [0.3.0] - 2026-05-18

This is a breaking release. The provider's model collapses to:

- `phala_app` = app identity + exactly one **bootstrap** CVM.
- `phala_app_instance` (new in 0.3) = additional named slots under the same app, declared explicitly.

The legacy anonymous-replicas mode (`phala_app.replicas = N > 1` + `/cvms/{src}/replicas` fan-out) is gone. Every multi-CVM use case now goes through `phala_app_instance` with `phala_app.members`.

### Breaking changes

- **Removed `phala_app.replicas`.** The schema attribute, the `desiredReplicaCount` validator, the `reconcileReplicas` scale-up/scale-down loop, the per-CVM PATCH fan-out helpers, and the `/apps/{id}/cvms/{src}/replicas` API call site are all removed. Any HCL that sets `replicas` will fail validation; any prior state will refresh cleanly because `replicas` is no longer persisted.
- **Removed the `/apps/{id}/cvms/{src}/replicas` code path.** The cloud endpoint still exists; the provider just doesn't call it.

### Migration recipe (0.2.x → 0.3.0)

Before (0.2.x):

```hcl
resource "phala_app" "web" {
  name     = "web"
  replicas = 3
  size     = "tdx.small"
  docker_compose = "..."
}
```

After (0.3.0):

```hcl
locals {
  web_members = ["web-0", "web-1", "web-2"]
}

resource "phala_app" "web" {
  name    = local.web_members[0]
  members = local.web_members
  size    = "tdx.small"
  docker_compose = "..."
}

resource "phala_app_instance" "web" {
  for_each = toset(phala_app.web.members)
  app_id   = phala_app.web.app_id
  name     = each.value
}
```

This rename gives each CVM a stable per-slot identity (you can `-target` individual slots, output their `vm_uuid`, and wire them into service-mesh peer IDs). It also makes the slot count owned by `phala_app_instance` resources rather than a single `replicas` integer that was racy with named instances.

For genuinely stateless single-CVM apps, the migration is to just delete the `replicas = 1` line (it's the default) and not declare any `phala_app_instance`.

### Added

- New `phala_app_instance` resource keyed by `(app_id, name)` that maps a Terraform resource to a stable logical member of a replica set. Backed by `POST /apps/{app_id}/instances` with a custom CVM name (phala-cloud-monorepo#1386). The slot survives CVM replacement: `vm_uuid` is computed and refreshes on Read, while `name` is operator-chosen and immutable (forces replace).
- `phala_app_instance` adopts the bootstrap CVM owned by `phala_app` when the names match, so MIG-style replica sets can be declared with a single symmetric `for_each` over all slot names. Adopted instances expose `managed = false` and skip the cloud DELETE on destroy; created instances have `managed = true` and own the CVM lifecycle.
- New optional `members` list on `phala_app` declares the full slot list for MIG-style usage. When set, the provider validates at plan time that `name` is one of `members`. Downstream `phala_app_instance` resources should use `for_each = toset(phala_app.foo.members)` to keep the slot list a single source of truth.
- New per-instance `env` on `phala_app_instance`: encrypted with the app's env public key and seeded at create time on managed instances. Adopted (bootstrap) instances reject `env`/`encrypted_env` — set those on `phala_app.env` instead.
- Design note `docs/design-notes/cvm-rename-endpoint.md` capturing the verified shape of `PATCH /cvms/{cvm_id}/name` for future reference (recovery tooling, CLI parity).

### Changed

- `phala_app.Update` now mutates the **bootstrap CVM only** (`docker_compose`, `pre_launch_script`, `env`, `image`, `size`, `disk_size`, compose-runtime settings). In members mode these mutations are refused at plan time, because the cloud has no app-revision update endpoint that would propagate them across named slots without losing identity. Workarounds: destroy + recreate, or move per-slot variations to `phala_app_instance.env`.
- Removing `members` from a `phala_app` resource that previously had it set is blocked at plan time (the transition would orphan named slot CVMs). Destroy and recreate the app instead.
- `phala_app.primary_cvm_id` now refers to the bootstrap CVM specifically — the CVM whose name equals `phala_app.name`. App-level mutations always target it.

### Fixed

- The bug where `phala_app.Update` would silently delete one of the named slot CVMs is gone by construction: the `reconcileReplicas` function that caused it no longer exists.

## [0.2.0-beta.4] - 2026-05-17

### Added

- New `phala_app_preflight` resource and `phala_app_preflight` data source for computing Phala Cloud app preflight metadata, including `compose_hash`, without deploying an app.
- `phala_app.instances` now exposes per-instance app state so multi-replica apps can report individual CVM IDs, VM UUIDs, endpoints, regions, instance types, and statuses.

### Changed

- `phala_app.compose_hash` now refreshes from the same app definition used by preflight so deployed app state can be compared directly with preflight output.

## [0.2.0-beta.3] - 2026-05-03

### Fixed

- In-place updates to `phala_app.env` are now applied. The schema-level `Sensitive: true` flag on the `env` map attribute interacted with element-level marks coming from `sensitive = true` Terraform variables and caused Terraform Core to silently suppress the in-place diff, so changing an env value used to report "No changes." and never call the API. To redact env values in plan output, mark the source variable with `sensitive = true` — the marks propagate per-element and Terraform redacts them. Fixes Phala-Network/phala-cloud#246.

## [0.2.0-beta.2] - 2026-03-13

### Removed

- **`phala_cvm` resource removed.** Use `phala_app` with `replicas = 1` instead. `phala_app` is now the sole lifecycle resource for managing CVMs on Phala Cloud.

### Changed

- Stabilized data source IDs: `phala_account` uses fixed `"current"`, `phala_workspace` uses immutable workspace ID. Prevents state churn on profile changes.
- Delete polling now respects `wait_timeout_seconds` instead of hardcoded 120s.
- Unified replica patch semantics: OS image and compose settings updates now use consistent 409-fallthrough across replicas.
- Improved error messages for public key decoding and delete timeout failures.

### Fixed

- API key no longer leaked in error response headers.
- `encrypted_env` is now validated as valid hex before sending to API.
- Typed API client logs a warning on initialization failure instead of silently degrading.

## [0.2.0-beta.1] - 2026-03-08

### Added

- New `phala_app` resource (app-first model):
  - shared compose/env at app scope
  - replica count management via `replicas`
  - app-level outputs: `app_id`, `cvm_ids`, `endpoint`
- New `phala_nodes` data source for node placement discovery (`node_id`) with optional filters.
- New `phala_attestation` data source (read-only attestation fetch by `cvm_id`).
- Release packaging script for cross-platform provider artifacts.
- CI workflow for provider tests/build checks.
- Manual GitHub release workflow for versioned artifacts.
- Feature maturity and release process documentation.

### Changed

- `image` is now updatable in-place via `PATCH /cvms/{id}/os-image`.
- Added create-time identity/placement inputs for `phala_app`:
  - `kms` (currently `phala` only; `ethereum`/`base` planned)
  - `custom_app_id` + `nonce` (PHALA deterministic identity flow)
  - `node_id` (maps to provision `teepod_id`)
- Added compose-file runtime settings to `phala_app`:
  - `public_logs`, `public_sysinfo`, `public_tcbinfo`, `gateway_enabled`, `secure_time`
  - updates use compose provision/apply flow and trigger restart/redeploy
- `storage_fs` (`zfs`/`ext4`) is now explicit and immutable (replacement required on change).
- `disk_size` updates are constrained to grow-only (shrink rejected by provider validation).

## [0.1.0] - 2026-03-07

### Added

- Initial provider MVP:
  - `phala_app`
  - `phala_cvm_power`
  - `phala_ssh_key`
  - `phala_account`
  - `phala_workspace`
  - `phala_sizes`
  - `phala_regions`
  - `phala_images`
- Workspace/account data sources and smoke-test example.
- Env auto-encryption flow (`env` -> `encrypted_env` + `env_keys`).

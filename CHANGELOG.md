# Changelog

All notable changes to `terraform-provider-phala` are documented in this file.

## [Unreleased]

## [0.3.0-beta.4] - 2026-05-31

Migrates the provider onto the official Phala Cloud Go SDK and makes every per-CVM mutable field update **in place** instead of forcing destructive CVM replacement. Previously, editing `env`, `encrypted_env`, `docker_compose`, `pre_launch_script` on a members-mode slot, or toggling `phala_app.listed`, destroyed and recreated the CVM(s) (new `vm_uuid`s, fresh disks) — surfaced by a large-scale e2e stress test. Each now PATCHes the relevant CVM directly and preserves identity. Also removes the inert `ssh_authorized_keys` field (**breaking**) and restores the `storage_fs` plan modifier lost during the SDK migration. All in-place paths were live-verified against real Phala Cloud in a disposable test workspace.

### Changed

- Replaced the provider's custom HTTP client and `oapi-codegen`-generated client with the official Phala Cloud Go SDK (`github.com/Phala-Network/phala-cloud/sdks/go`), eliminating ~37k lines of duplicated API code. All API calls now use typed SDK methods (`ProvisionCVM`, `GetCVMInfo`, `CreateAppInstance`, `UpdateCVMEnvs`, `UpdateOSImage`, `UpdateCVMResources`, `UpdateDockerCompose`, `UpdatePreLaunchScript`, `GetAppInfo`, `GetAppCVMs`, `DeleteCVM`, `RedeployAppRevision`, etc.).
- Provider error diagnostics now surface SDK structured error codes (`error_code`, suggestions, links) via `APIError.IsStructured()` / `FormatError()`.
- `phala_app_instance` mutable fields now update **in place** instead of forcing replacement: `env`, `encrypted_env`, `docker_compose`, and `pre_launch_script`. Each PATCHes the slot's own CVM (`/cvms/{uuid}/envs`, `/docker-compose`, `/pre-launch-script`), preserving its `vm_uuid` — mirroring the single-CVM `phala_app` update path. Previously these were all `RequiresReplace` (an inherited default from when the resource had no Update path), so any such edit to a managed slot destroyed and recreated its CVM — churning every non-bootstrap slot of a members-mode MIG (surfaced by the large-scale e2e stress test). These updates remain rejected on adopted (`managed = false`) slots, which are owned by the parent `phala_app`. `node_id` (placement) and `compose_hash` (a content-addressed compose pointer, validated server-side and not settable alongside `docker_compose`) correctly still force replacement. Live-verified on real cloud: `env` and `docker_compose` edits apply with `vm_uuid` preserved and a clean follow-up plan; a `node_id` change still destroys + recreates on the new node.
- **Default API version is now `2026-05-22`** (was `2026-01-21`). The SDK's default response schemas are the hashed-CVM-id types (`id` is the `cvm_<hashid>` string), which the backend only emits when `X-Phala-Version` requests this version; the older header returned integer ids that failed to decode. Override via `api_version` in the provider block if you need to pin a different version. This keeps lockstep with the SDK's `version.DefaultAPIVersion`.

### Removed

- **`ssh_authorized_keys`** from `phala_app`, the `phala_app_preflight` resource, and the `phala_app_preflight` data source (**breaking**). The field was inert: the cloud's provision request schema has no such field, so the value was silently dropped and never reached the CVM. SSH keys are account-scoped — the keys you register with the `phala_ssh_key` resource are the ones injected into CVMs at launch. Remove `ssh_authorized_keys` from your `phala_app` blocks and manage SSH access via `phala_ssh_key`.
- `internal/phalaapi/` (oapi-codegen output)
- `openapi/` (vendored spec + generator)
- Custom `APIClient`, `cvmAPIResponse`, `appAPIResponse` types — replaced by SDK equivalents.

### Fixed

- `phala_app.listed` now updates **in place** instead of forcing replacement. Toggling the marketplace-listing flag previously destroyed and recreated the CVM(s); it now uses the dedicated `PATCH /cvms/{uuid}/listed` endpoint (a plain metadata write — no redeploy, no restart, no attestation change), fanned out across every slot in members mode. Live-verified: a `false → true` flip applied in ~6s with `vm_uuid` preserved and a clean follow-up plan. (Adds SDK method `UpdateCVMListed`.)
- `storage_fs` regression introduced during the SDK migration (re-opens the original [#5](https://github.com/Phala-Network/terraform-provider-phala/issues/5)): the attribute lost its `UseStateForUnknown` plan modifier, so as an Optional+Computed field it planned as `(known after apply)` on every in-place update and tripped `RequiresReplace` — forcing a full app + slot replacement (new `app_id`, new `vm_uuid`s) on changes as small as a `docker_compose` edit. Restored the modifier; in-place updates in members (MIG) mode again preserve slot identity.

## [0.3.0-beta.3] - 2026-05-20

Surfaces the Phala Cloud gateway DNS info on `phala_app` and `phala_app_instance` so downstream consumers can build per-port public URLs from provider outputs instead of hardcoding the cloud's gateway domain. Also fixes a Create-time apply error that bit anyone setting `image` to the combined `<name>-<short-hash>` form.

### Added

- `phala_app.gateway_base_domain` (Computed) — default Phala Cloud gateway DNS suffix for the app's primary CVM, e.g. `dstack-pha-prod5.phala.network`. Downstream HCL composes public URLs as `https://<app_id>-<port>.<gateway_base_domain>` without having to predict the suffix.
- `phala_app.gateway_cname` (Computed) — operator-configured CNAME alias for the app's gateway, if one has been set via the cloud UI. Empty when no custom CNAME is configured.
- Same two fields surface on every entry of `phala_app.instances` and on `phala_app_instance` itself, so per-slot URL composition works the same way for both single-CVM and members-mode apps.
- Values are sourced from `CVMGatewayInfo.base_domain` / `CVMGatewayInfo.cname` on the relevant CVM response. Helpers tolerate partial responses (no panic if `gateway` is absent or any member is nil) and trim incidental whitespace.

### Fixed

- `phala_app.Create` no longer trips Terraform Core's "Provider produced inconsistent result after apply" check when `image` is set to the combined `<name>-<short-hash>` form printed by `phala images` and the cloud image catalog (e.g. `dstack-dev-0.5.7-9b6a5239`). The cloud splits the OS image into `os.name` + `os.os_image_hash` on the CVM response; `populateState` now preserves the user-supplied form when it semantically matches the same image, falling back to bare `os.name` only when no match can be proven. Both the bare-name and combined forms round-trip cleanly.

### Motivation

Until now, downstream stacks like [dstack-TEE/service-mesh](https://github.com/Dstack-TEE/service-mesh) had to declare a top-level `gateway_domain` variable and assemble URLs as `https://${phala_app.app_id}-8080.${var.gateway_domain}`. The variable was effectively hardcoded per environment and would silently drift if the cloud's default suffix changed. With these fields exposed, those modules can use `phala_app.gateway_base_domain` (or the per-instance equivalent) directly.

## [0.3.0-beta.2] - 2026-05-19

In-place updates work in members (MIG) mode for all the fields users actually edit. The release-blocking guardrail from 0.3.0-beta.1 is gone.

### Added

- `phala_app.Update` now propagates changes across every CVM in a members-mode app, preserving slot identity (`vm_uuid` and `name` are unchanged across the update):
  - **Compose-body changes** (`docker_compose`, `pre_launch_script`, `public_logs`, `public_sysinfo`, `public_tcbinfo`, `gateway_enabled`, `secure_time`, env-key list) flow through the cloud's app-revision endpoint: provision + apply on the bootstrap CVM (which creates the revision row), then `POST /apps/{id}/revisions/{rev}/redeploy` with `vm_uuids = [other slots]`. Verified end-to-end on a 2-slot MIG: bootstrap update + slot redeploy lands the new `compose_hash` on every CVM with both `vm_uuid`s preserved.
  - **Env value changes** (the common "rotate a secret" case) use a per-CVM `PATCH /cvms/{uuid}/envs` fan-out across every slot. The app-rooted KMS public key is shared across all CVMs in one app, so the same encrypted_env bytes are accepted by every slot. `compose_hash` stays unchanged (no new revision).
  - **OS image changes** (`image`) fan out per-CVM via `PATCH /cvms/{uuid}/os-image` — the cloud has no app-level analog. Sequential, fail-fast.
  - **Size / disk_size changes** fan out per-CVM via `PATCH /cvms/{uuid}/resources`. Same pattern.

### Changed

- `ModifyPlan` no longer blocks app-level mutable-field updates in members mode. The only structural check kept is the "cannot leave members mode in-place" guard, which still requires destroy + recreate when a user removes the `members` attribute from a previously-members-mode app.
- Single-CVM apps (no `members`) follow the same single-CVM update path as before — PATCHes target the bootstrap directly with no fan-out machinery. Zero behavior change for that case.

### Fixed

- The members-mode guardrail's diagnostic message previously suggested workarounds (destroy+recreate, per-slot env) that are no longer needed for `docker_compose` / `env` / `image` / `size` / `disk_size`. Those workarounds were obsolete the moment we wired up the revision and fan-out paths; the message is gone with the guardrail.

## [0.3.0-beta.1] - 2026-05-18

First prerelease of the 0.3 line. The provider's model collapses to:

- `phala_app` = app identity + exactly one **bootstrap** CVM.
- `phala_app_instance` (new in 0.3) = additional named slots under the same app, declared explicitly.

The legacy anonymous-replicas mode (`phala_app.replicas = N > 1` + `/cvms/{src}/replicas` fan-out) is gone. Every multi-CVM use case now goes through `phala_app_instance` with `phala_app.members`. This is a **breaking change** — see migration recipe below.

Released as `-beta.1` to validate the new shape against real workloads before promoting to stable. Treat as a candidate for stateful-cluster integrations; do not pin production single-CVM stacks to this tag without re-testing the simpler in-place update path.

### Breaking changes

- **Removed `phala_app.replicas`.** The schema attribute, the `desiredReplicaCount` validator, the `reconcileReplicas` scale-up/scale-down loop, the per-CVM PATCH fan-out helpers, and the `/apps/{id}/cvms/{src}/replicas` API call site are all removed. Any HCL that sets `replicas` will fail validation; any prior state will refresh cleanly because `replicas` is no longer persisted.
- **Removed the `/apps/{id}/cvms/{src}/replicas` code path.** The cloud endpoint still exists; the provider just doesn't call it.

### Migration recipe (0.2.x → 0.3.0-beta.1)

Before (0.2.x):

```hcl
resource "phala_app" "web" {
  name     = "web"
  replicas = 3
  size     = "tdx.small"
  docker_compose = "..."
}
```

After (0.3.0-beta.1):

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

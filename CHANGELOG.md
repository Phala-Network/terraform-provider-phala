# Changelog

All notable changes to `terraform-provider-phala` are documented in this file.

## [Unreleased]

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

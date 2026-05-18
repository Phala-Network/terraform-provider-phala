# Terraform Provider Feature Maturity

Last updated: 2026-05-18

## Maturity Levels

- `alpha`: implemented, but behavior/shape may still change.
- `beta`: stable behavior for normal use, with known feature gaps.
- `ga`: production-ready with compatibility and upgrade guarantees.

## Resource/Data Source Status

| Component | Level | Status | Notes |
| --- | --- | --- | --- |
| `phala_app` | beta | create/read/update/delete + bootstrap CVM | App identity + one bootstrap CVM (single-CVM apps), or app identity + per-slot CVMs via `phala_app_instance` (MIG). The legacy anonymous `replicas` field was removed in 0.3.0-beta.1. |
| `phala_app_instance` | beta | create/read/delete + adopt-by-name | Stable named slot under an app, keyed by `(app_id, name)`. Adopts the bootstrap CVM when its name matches `phala_app.name`. |
| resource `phala_app_preflight` | beta | create/read/delete | Computes preflight app metadata, including `compose_hash`, without deploying CVMs. |
| `phala_cvm_power` | beta | running/stopped state management | Separate action-style power control works; delete is state-only by design. |
| `phala_ssh_key` | beta | create/read/delete | DO-style key lifecycle. |
| `phala_account` | beta | read | Returns user/workspace linkage + credits. |
| `phala_workspace` | beta | read | Returns active workspace metadata. |
| `phala_sizes` | beta | read | Catalog data source. |
| `phala_regions` | beta | read | Catalog data source (apps filter options + teepod availability fallback). |
| `phala_images` | beta | read | Catalog data source. |
| `phala_nodes` | beta | read | Node catalog for placement (`node_id`) with optional region/on-chain-KMS filters. |
| `phala_attestation` | beta | read | On-demand CVM attestation fetch by `cvm_id` (read-only). |
| data source `phala_app_preflight` | beta | read | Reads preflight app metadata for a declared app shape without deploying CVMs. |

## Terraform UX Parity (DigitalOcean-like)

| Capability | Current |
| --- | --- |
| App-first resource with named slot identities (MIG-style) | yes (`phala_app.members` + `phala_app_instance`) |
| Per-instance app state | yes (`phala_app.instances`, `phala_app_instance.*`) |
| Pre-deploy compose hash / app metadata | yes (`phala_app_preflight`) |
| Separate power resource (`phala_cvm_power`) | yes |
| Per-deploy SSH key injection | yes |
| OS image selection + update | yes (`image`, in-place via `/os-image`) |
| Encrypted env workflow | yes (auto via `env`, manual via `encrypted_env` + `env_keys`) |
| Compose runtime settings | yes (`public_logs`, `public_sysinfo`, `public_tcbinfo`, `gateway_enabled`, `secure_time`) |
| Deterministic app identity inputs | partial (`custom_app_id` + `nonce` for `kms=phala`) |
| Node placement input | yes (`node_id` -> provision `teepod_id`) |
| Node discovery for placement | yes (`phala_nodes`) |
| Storage FS selection | yes (`storage_fs`: `zfs`/`ext4`, immutable after create) |
| Disk resize semantics | grow-only (`disk_size` shrink rejected) |
| Workspace/account introspection | yes |
| Custom domain management | not yet (planned via compose definition support) |
| VPC/network primitives | not applicable (Phala serverless-style network model) |
| Portable detachable volume/snapshot primitives | not yet |

## Criteria to Reach `ga`

- Workspace isolation e2e tests in CI (cross-workspace negative checks).
- Stable docs for upgrade semantics across minor versions.
- Explicit import guidance and lifecycle caveats for all resources.
- Release automation with reproducible artifacts and checksums.
- Two consecutive provider releases with no breaking schema/state regressions.

# Terraform Provider Release Guide

This guide defines how to cut and validate releases for `terraform-provider-phala`.

## Versioning

- Use semantic versioning (`MAJOR.MINOR.PATCH`).
- While `< 1.0.0`, assume `MINOR` may include breaking changes.
- Use prerelease tags for test channels (for example `0.3.0-beta.1`).

## Release Gates

Before cutting a release:

1. `make ci` passes.
2. Smoke test passes in a real workspace:
   - `make smoke-plan ...`
   - `make smoke-apply ...`
   - `make smoke-destroy ...`
3. Feature maturity updates are reflected in:
   - `FEATURE_MATURITY.md`
   - `README.md`
4. `CHANGELOG.md` has release notes for the target version.

## Build Release Artifacts Locally

```bash
make package-release VERSION=0.2.0
```

Outputs:

- `dist/0.2.0/terraform-provider-phala_0.2.0_<os>_<arch>.zip`
- `dist/0.2.0/terraform-provider-phala_0.2.0_manifest.json`
- `dist/0.2.0/terraform-provider-phala_0.2.0_SHA256SUMS`
- `dist/0.2.0/terraform-provider-phala_0.2.0_SHA256SUMS.sig` (when GPG signing is configured locally)

## GitHub Release Workflow

This repository publishes from semver git tags like `v0.2.0` or `v0.2.0-beta.1`.

The release workflow:

1. Triggers on a pushed `v*` tag.
2. Imports the configured GPG signing key.
3. Uses GoReleaser to build cross-platform archives.
4. Publishes a GitHub release with provider zips, manifest, signed checksums, and release notes.

Required repository secrets:

- `GPG_PRIVATE_KEY`
- `PASSPHRASE`

Required Terraform Registry setup outside GitHub:

1. Claim the `phala-network` public namespace.
2. Upload the matching ASCII-armored public key on the namespace Signing Keys page.

## Post-release Validation

1. Download one release asset and verify checksum.
2. Verify local install with Terraform dev override.
3. Run a no-op `terraform plan` against an existing stack.
4. Run one update path (`size`, `docker_compose`, or power state) and confirm state convergence.

# t8 — Preflight vs real-create cross-check

**Purpose**: assert that the `phala_app_preflight` data source and the
real `phala_app` resource agree on the artifacts that should be deterministic
given identical input (`compose_hash`, `app_env_encrypt_pubkey`).

If they disagree, either:
1. The two code paths build the `ProvisionCVM` request differently (a
   provider regression — request-builder drift between data source and resource), OR
2. The server side is non-deterministic for these fields (a server bug worth
   reporting upstream, not a provider issue).

Either way you want to know.

**Cost warning**: provisions 1 real CVM.

## Run

```bash
cd e2e_tests/terraforms/t8-preflight-vs-real
terraform init
time terraform apply -auto-approve   # 3-10 min

terraform output compose_hashes_match     # must be: true
terraform output encrypt_pubkeys_match    # must be: true (usually; see below)

terraform destroy -auto-approve
```

## Pass criteria

- `compose_hashes_match == true` — this is the hard requirement; compose
  hashing is a pure function of the compose bytes + a few flags
- `encrypt_pubkeys_match == true` — usually true, but the server may rotate
  pubkeys between preflight and commit; if false, check whether it's
  consistently false (provider bug) vs occasionally false (server rotation)

## Fail signals

- `compose_hashes_match == false` → check `buildAppPreflightProvisionReq`
  (data source side) vs the `phala_app` Create path's request builder. They
  should produce byte-identical `ProvisionCVMRequest` for identical input.
  If they don't, the migration introduced a divergence.
- `encrypt_pubkeys_match == false` reliably → either server-side fresh-pubkey-
  per-commit semantic (real, document it) or the resource's Read path is
  picking up a different pubkey field name than the data source

## Cleanup

```bash
terraform destroy -auto-approve
```

# t4 — App full lifecycle (legacy single-CVM mode)

**Purpose**: full happy-path test of `phala_app` without `members` (no MIG).
Covers create → ready → in-place env update → destroy.

This is the highest-coverage test for the migration: hits `ProvisionCVM`,
`CommitCVMProvision`, `GetCVMInfo` (polling), `GetAppInfo` (Read path),
`UpdateCVMEnvs` (env update), `DeleteCVM`.

**Cost warning**: this provisions a real CVM. Default is `tdx.small`. Destroy when done.

## Run

```bash
cd e2e_tests/terraforms/t4-app-lifecycle
terraform init       # for random provider only
terraform plan       # 2 to add
time terraform apply -auto-approve   # expect 3-10 min for CVM ready

terraform show
terraform output endpoint
curl -s http://$(terraform output -raw endpoint)/ | head   # may or may not respond depending on DNS

# Drift check (critical for SDK migration)
terraform plan       # MUST be no-op

# In-place env update — toggle LOG_LEVEL
TF_VAR_log_level=debug terraform plan    # should show 1 to change (env only, no replace)
TF_VAR_log_level=debug terraform apply -auto-approve

# Drift check again
terraform plan

# Destroy
terraform destroy -auto-approve
```

## Pass criteria

- `apply` finishes with `status` reaching the ready state (`running` / similar)
- `endpoint` is a real URL
- `primary_cvm_id` is set
- Post-`apply` `plan` is no-op
- `LOG_LEVEL` update is **in-place** (no replace, no recreate) — this is the
  feature-flag check for the `b7575aa` commit
- After update, `plan` is no-op again
- `destroy` removes the CVM (verify in dashboard)

## Fail signals

| Symptom                                | Likely cause                            |
| -------------------------------------- | --------------------------------------- |
| `apply` hangs at "creating"            | `wait_for_ready` polling loop wired to wrong SDK call |
| "Unknown" → "Known" drift after apply  | `Read` path missing a Computed field    |
| Env update triggers replace            | Update logic falls through to recreate; check the diff handler |
| Status stuck at non-running indefinitely | `GetCVMInfo` response field mismatch    |
| Destroy fails with 404                 | Order of `DeleteCVM` vs app cleanup is wrong; or already-deleted not handled |

## Cost note

Don't forget destroy. Even a `tdx.small` accumulates billable seconds while running.

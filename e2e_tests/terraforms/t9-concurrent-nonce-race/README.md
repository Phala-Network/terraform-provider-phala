# t9 — Concurrent app creation nonce race (issue #1544 / PR #1622)

**Purpose**: full end-to-end reproduction of the per-team `obtain_app_id`
race. Concurrent `phala_app` resources for the same team walk the entire
provision → commit → boot path, so a pass proves clients can deploy in
parallel with no contention failures anywhere on the critical path.

**Pre-fix behaviour** (main before PR #1622):
two concurrent `POST /cvms/provision` calls for the same team read the same
`max(nonce)`, derived the same address, and the loser failed with:

```
Error: [ERR-02-009] Failed to obtain app ID from centralized KMS:
duplicate key value violates unique constraint "ix_dstack_app_nonces_address"
DETAIL: Key (address)=(7016...b7) already exists.
```

`terraform apply -parallelism=1` was the only workaround.

**Post-fix behaviour** (PR #1622 merged):
server-side `pg_try_advisory_xact_lock` + `FOR UPDATE SKIP LOCKED` pool reuse
+ 90s `reserved_at` TTL. Concurrent provisions get distinct app_ids and
`-parallelism > 1` "just works".

## Cost warning

This test provisions `var.parallel_count` real CVMs (default 4 × `tdx.small`).
Each one boots, idles serving an nginx hello, and bills until `terraform
destroy` removes it. **Always destroy when done.** A typical run:

| Parallelism | CVMs alive | Wall time apply→destroy | Approx cost |
|---|---|---|---|
| 2 | 2 × tdx.small | ~5 min | tiny |
| 4 (default) | 4 × tdx.small | ~10 min | tiny |
| 8 (stress) | 8 × tdx.small | ~15 min | small |
| 16 (chaos) | 16 × tdx.small | ~30 min | check your credit balance first |

Cheaper alternative if you only want to verify the server-side race fix
(not the boot path): change `phala_app` → `phala_app_preflight` and drop the
`wait_for_ready` / `wait_timeout_seconds` lines. Preflight calls the same
`ProvisionCVM` (where the race lives) but does NOT commit or boot — zero
billable resources, useful as a quick smoke check. The full-path test in
this file additionally covers boot-time races (none currently known) and
gives you live CVMs to inspect.

## Prereqs

- `PHALA_CLOUD_API_KEY` exported
- `TF_CLI_CONFIG_FILE=/tmp/phala-tf-dev/terraformrc` exported BEFORE init
- All N apps must resolve to the same `team_wallet_id` — true by default
  because one API key == one workspace == one team

## Run

```bash
cd e2e_tests/terraforms/t9-concurrent-nonce-race

# One-time setup (clean any half-init state from earlier attempts)
rm -rf .terraform .terraform.lock.hcl
export TF_CLI_CONFIG_FILE=/tmp/phala-tf-dev/terraformrc
terraform init

# Trigger N concurrent provisions
time terraform apply -parallelism=4 -auto-approve
# Expect: ~5-10 min wall time; 4 CVMs reach `running`.

# Verify
terraform output addresses_unique         # MUST be: true
terraform output distinct_app_ids_count   # MUST equal expected_count (4)
terraform output app_ids                  # 4 distinct 0x-prefixed addresses
terraform output statuses                 # all "running"
terraform output endpoints                # 4 distinct URLs

# Smoke-test a CVM is actually serving (optional, may need DNS warmup)
for url in $(terraform output -json endpoints | jq -r '.[]'); do
  echo "--- $url ---"
  curl -sf -m 5 "$url/" | head -1 || echo "(not responding yet)"
done

# Cleanup — REQUIRED, billing runs until destroy completes
terraform destroy -auto-approve
```

Stress higher:

```bash
TF_VAR_parallel_count=16 terraform apply -parallelism=16 -auto-approve
# 16 CVMs. Crank only if your credit balance can take ~30 min × 16 tdx.small.
TF_VAR_parallel_count=16 terraform destroy -auto-approve
```

## Pass criteria

| Check                                                          | Expected |
| -------------------------------------------------------------- | -------- |
| `apply` exits 0                                                | Yes (no resource failed) |
| `addresses_unique == true`                                     | Yes      |
| `distinct_app_ids_count == expected_count`                     | Yes      |
| `statuses` — every entry is `running` (or `ready` per server enum) | Yes  |
| Each `app_id` is a 0x-prefixed 40-char hex string              | Yes      |
| Output / logs contain `ERR-02-009`                             | **No**   |
| Output / logs contain `duplicate key value violates unique`    | **No**   |
| Output / logs contain `already assigned, please provision`     | **No**   |
| Each `endpoint` is a real URL                                  | Yes      |
| `terraform plan` after apply                                   | No-op (no drift) |

## Pre-fix repro (to confirm the test actually catches the bug)

Point at a build that doesn't have PR #1622 merged (e.g. an older deployed
env, or a local backend on a parent commit of the fix):

```bash
export PHALA_CLOUD_API_PREFIX="https://<env-without-fix>/api/v1"
terraform apply -parallelism=4 -auto-approve
```

Expected (pre-fix): at least one resource fails with `[ERR-02-009]`, the
others may have provisioned and booted. State is half-applied — clean up
with `terraform destroy` (which will tear down the ones that succeeded) and
re-run with `-parallelism=1` to confirm the workaround still works.

Note: with `-parallelism=4` and full boot path, you may also see the
PR #1622-mentioned sibling failure: HTTP 422 "already assigned" at create
(pool-reuse handout). Both error shapes indicate pre-fix code.

## Reservation TTL spot-check (post-fix only)

PR #1622 adds a 90s `reserved_at` soft hold on the nonce row. With the full
`phala_app` path, the nonce gets claimed at provision and the create
immediately follows — `reserved_at` is set briefly then `project_id` is
populated when commit lands. Verify via admin DB:

```sql
SELECT nonce, address, project_id, reserved_at, created_at
FROM dstack_app_nonces
WHERE team_wallet_id = (
  SELECT id FROM team_wallets WHERE team_id = '<your-team-id>'
)
ORDER BY nonce DESC
LIMIT 8;
```

- Immediately after a successful `apply`: every just-created row has
  `project_id` populated (committed) and `reserved_at` cleared or stale
- `GET /kms/phala/next_app_id` must NOT predict any of the just-allocated
  addresses

## Cleanup

```bash
terraform destroy -auto-approve
```

If a partial apply left CVMs behind (pre-fix repro path), the destroy will
clean up the ones that did succeed. Anything still orphaned (CVMs without
terraform state) needs manual cleanup via the dashboard or the API.

## Related

- Issue: https://github.com/Phala-Network/phala-cloud-monorepo/issues/1544
- Fix:   https://github.com/Phala-Network/phala-cloud-monorepo/pull/1622
- Server-side AC2 (raw API repro using `xargs -P8 curl`) is in PR #1622's
  description if you want a no-terraform reproduction
- Cheap preflight-only variant: see "Cost warning" section above

# Terraform e2e scenarios

Manual end-to-end scenarios for `terraform-provider-phala`, each driving
the locally-built provider against a real Phala Cloud workspace.

> Environment setup (build the provider, write `dev_overrides`, export
> auth, etc.) lives in [../README.md](../README.md). Run `make -C ..
> setup && make -C .. check-env` first; this README assumes that's done.

## Scenarios

| #   | Dir                      | Covers                                                                | Real CVM? |
| --- | ------------------------ | --------------------------------------------------------------------- | --------- |
| t1  | t1-datasources           | Read-only data sources (workspace, account, nodes, regions, sizes, images) | no |
| t2  | t2-ssh                   | `phala_ssh_key` resource CRUD                                         | no        |
| t3  | t3-preflight             | `phala_app_preflight` data source + resource (no commit)              | no        |
| t4  | t4-app-lifecycle         | `phala_app` create → in-place env update → destroy                    | 1 CVM     |
| t5  | t5-mig-cluster           | MIG mode: `phala_app` with `members` + `phala_app_instance` `for_each` | 3 CVMs   |
| t6  | t6-replicas-removed      | `phala_app` without the removed `replicas` attribute                  | 1 CVM     |
| t7  | t7-structured-errors     | Trigger an API error → assert structured diagnostic formatting        | no        |
| t8  | t8-preflight-vs-real     | Compare preflight data source output vs actual `phala_app` create     | 1 CVM     |
| t9  | t9-concurrent-nonce-race | Concurrent provisions for one team → distinct app_ids (Phala-Network/phala-cloud-monorepo#1544) | 4 CVMs |

Each scenario's `README.md` documents its purpose, run steps, pass
criteria, and likely failure signals.

## Per-scenario workflow

```bash
cd e2e_tests/terraforms/tN-<name>

# For t3/t4/t5/t6/t8/t9 — they pull in hashicorp/random for unique
# suffixes, which is a real registry provider and needs a one-off init.
# t1/t2/t7 are pure-phala and skip this step entirely.
terraform init

terraform plan
terraform apply -auto-approve

# Inspect
terraform output
terraform show

# For scenarios with "Real CVM? yes" — destroy is REQUIRED, billing
# accrues until destroy completes.
terraform destroy -auto-approve
```

## Critical — three rules to avoid the init failure

The most common failure when starting out is:

```
Error: Failed to query available provider packages
Could not retrieve the list of available versions for provider
phala-network/phala: no available releases match the given constraints
```

Three things cause this; understanding them once avoids a lot of churn:

1. **`TF_CLI_CONFIG_FILE` must be exported BEFORE the first `terraform
   init`** in a scenario directory. Without it, Terraform fetches phala
   from the public registry (it's unpublished), aborts, and leaves a
   half-written lock file.

2. **`terraform init` does NOT skip phala just because `dev_overrides`
   exists.** Dev overrides only short-circuit the *install* step, not the
   *version resolution* step. If anything pins phala (the .tf `source =`
   block, the lock file, OR a `terraform.tfstate` from a prior run),
   Terraform still tries to resolve a version → registry 404 → init
   fails.

3. **A leftover `terraform.tfstate` from a previous apply re-triggers
   version resolution** even after `terraform destroy` (destroy leaves
   the state file behind with serial=0). State-driven init scans every
   provider reference in state and demands a version.

Recovery, run from the affected scenario directory:

```bash
rm -rf .terraform .terraform.lock.hcl terraform.tfstate terraform.tfstate.backup
export TF_CLI_CONFIG_FILE=/tmp/phala-tf-dev/terraformrc
terraform init
```

Or from `e2e_tests/`, wipe every scenario at once:

```bash
make clean
```

## Recommended order

The cheapest, fastest-feedback path through the suite:

```
t1 → t2 → t3 → t7      (zero cost; ~10 min total)
  ↓ all green = SDK auth + read paths + provisioning request + error formatting OK
t6 → t4 → t8           (1 CVM each; ~10 min apiece including destroy)
  ↓ all green = single-CVM lifecycle + cross-path consistency OK
t5                     (3 CVMs; MIG mode + negative tests)
  ↓ all green = named-slot model + guardrails OK
t9                     (4 CVMs; concurrent-provision race regression)
```

`t2` needs a one-off SSH key:

```bash
ssh-keygen -t ed25519 -f /tmp/tf-test-key -N '' -C 'tf-e2e-test'
export TF_VAR_ssh_public_key="$(cat /tmp/tf-test-key.pub)"
```

## Rebuilding after provider source changes

If you modify the provider Go code (or sibling `sdks/go` SDK):

```bash
make -C e2e_tests build       # rebuilds /tmp/phala-tf-dev/terraform-provider-phala
```

No `terraform init` re-run is needed — `dev_overrides` re-reads the binary
every invocation, so the next `terraform plan` already uses the rebuild.

## What "passes" means

There's no automated assertion — each scenario's README has a "Pass
criteria" section, and you read state/output to confirm. A second
`terraform plan` immediately after `apply` should be a no-op; any drift
there indicates the `Read` path is missing a Computed field, which is
typically a regression worth investigating.

## SDK migration coverage map

| Surface | Exercised by |
| --- | --- |
| Auth bootstrap + env-var fallback | every scenario |
| `ProvisionCVM`                    | t3, t4, t5, t6, t8, t9 |
| `CommitCVMProvision`              | t4, t5, t6, t8, t9 |
| `GetCVMInfo` / refresh path       | t4, t5, t6, t8, t9 |
| `GetAppInfo` / `GetAppCVMs`       | t5 |
| `CreateAppInstance`               | t5 |
| `UpdateCVMEnvs`                   | t4 (in-place env edit) |
| `DeleteCVM`                       | every scenario with a real CVM |
| `APIError.IsStructured` / `FormatError` | t7 |
| MIG `ValidateConfig` / `ModifyPlan` | t5 (negative tests) |
| Per-team nonce race fix           | t9 |

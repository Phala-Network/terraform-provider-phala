# t5 — MIG-style cluster (`members` + `phala_app_instance` for_each)

**Purpose**: end-to-end test of the named-slot model (issue #243's ask #1).
This is the most architecturally complex test in the suite.

Exercises:
- `phala_app` with `members` set → bootstrap CVM adopts slot named `primary`
- `phala_app_instance` for_each over remaining slots → `CreateAppInstance`
- `ValidateConfig` invariants (name ∈ members, no `replicas`)
- `ModifyPlan` guardrail (in-place mutations refused in members mode)
- The `managed` attribute (false for bootstrap, true for created slots)

**Cost warning**: 3 CVMs running in parallel. Destroy promptly.

## Run

```bash
cd e2e_tests/terraforms/t5-mig-cluster
terraform init
time terraform apply -auto-approve   # 5-15 min for 3 CVMs

terraform output replica_instances   # confirm 2 entries (a, b), both managed=true
terraform output primary_slot_cvm_id # the bootstrap CVM
terraform output all_cvm_ids         # 3 IDs total

# Critical: drift check
terraform plan                       # MUST be no-op

# --- Negative tests ---

# (a) ValidateConfig: name not in members → plan should fail before any API call
sed -i.bak 's/name    = local.primary_slot/name    = "ghost"/' main.tf
terraform plan 2>&1 | grep -i 'must be one of'   # expect this
mv main.tf.bak main.tf

# (b) ModifyPlan guardrail: edit env in HCL → plan should refuse
sed -i.bak 's/SLOT_NAME = local.primary_slot/SLOT_NAME = "mutated"/' main.tf
terraform plan 2>&1 | grep -i 'Unsafe update in members mode'   # expect this
mv main.tf.bak main.tf

# Drift check again
terraform plan                       # no-op

terraform destroy -auto-approve      # destroys all 3
```

## Pass criteria

| Check                                                    | Pass condition                              |
| -------------------------------------------------------- | ------------------------------------------- |
| `apply` creates 1 app + 1 bootstrap CVM + 2 replicas     | 4 resources added (1 app + 3 CVMs counted as 1 root + 2 instances) |
| `primary_cvm_id` is in `cvm_ids`                         | Yes                                         |
| All 3 CVMs reach `running`                               | `replica_instances[*].endpoint` non-empty   |
| `replicas["replica-a"].managed == true`                  | Yes (created by `phala_app_instance`)       |
| Drift check `plan` after `apply`                         | No-op                                       |
| Negative (a): name not in members                        | Error: "must be one of" / similar           |
| Negative (b): env mutation in members mode               | Error: "Unsafe update in members mode"      |
| `destroy`                                                | All 3 CVMs gone from dashboard              |

## Fail signals

- `for_each` over members works for unknown values at plan? → should error
  cleanly; if it crashes, terraform-plugin-framework version issue
- Bootstrap CVM ends up with `managed = true` → identity logic in
  `selectPrimaryCVM` is wrong (regression from `e52e015`)
- ValidateConfig doesn't fire → schema-level validators wired up wrong
- ModifyPlan doesn't fire on env change → diff detection has a hole
- `destroy` removes app but leaves CVMs orphaned → dependency ordering broken

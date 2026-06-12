# t6 — `replicas` attribute removed

**Purpose**: regression test for the `replicas`-drop change (commit `89c413b`).
The legacy schema required `replicas`; the new schema treats `phala_app`
as always-single (1 bootstrap CVM) unless `members` opts into MIG mode.

This test verifies:
- A config with NO `replicas` attribute plans and applies cleanly
- Exactly 1 CVM is created (no surprise multi-CVM behavior)
- `cvm_ids` has length 1

It also serves as the minimal happy-path smoke test if t4 is overkill.

## Run

```bash
cd e2e_tests/terraforms/t6-replicas-removed
terraform init
terraform plan       # 2 to add (random + app)
time terraform apply -auto-approve

terraform output cvm_ids         # length 1
terraform output instances_count # 1

terraform plan       # no-op
terraform destroy -auto-approve
```

## Pass criteria

- Plan succeeds with no `replicas`-related error
- Exactly 1 CVM created
- `instances_count == 1`
- No-op drift on second `plan`

## Fail signals

- Plan error "replicas is required" → schema regression (didn't drop the field)
- 2+ CVMs created → some lingering default replica logic
- `instances` list empty → computed-list population broken after Create
- Drift on second plan → `Read` doesn't repopulate `instances` deterministically

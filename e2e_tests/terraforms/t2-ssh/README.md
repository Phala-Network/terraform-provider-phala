# t2 — SSH key resource CRUD

**Purpose**: exercise the simplest write path through the SDK — `phala_ssh_key`
has no CVM provisioning, so failures isolate cleanly to the SDK client / auth /
schema mapping.

## Prep

```bash
ssh-keygen -t ed25519 -f /tmp/tf-test-key -N '' -C 'tf-manual-test'
export TF_VAR_ssh_public_key="$(cat /tmp/tf-test-key.pub)"
```

## Run

```bash
cd e2e_tests/terraforms/t2-ssh
terraform plan       # 1 to add
terraform apply -auto-approve

terraform show       # confirm fingerprint, key_type, source populated
terraform plan       # should be no-op (Read path correct)

# Update: rename the key
TF_VAR_ssh_key_name=tf-manual-test-key-renamed terraform plan
TF_VAR_ssh_key_name=tf-manual-test-key-renamed terraform apply -auto-approve

terraform destroy -auto-approve
```

## Pass criteria

| Step                          | Expected                                                  |
| ----------------------------- | --------------------------------------------------------- |
| First `apply`                 | `ssh_key_id` is non-empty, fingerprint matches `ssh-keygen -lf /tmp/tf-test-key.pub` |
| `terraform show`              | `key_type = "ed25519"`, `source = "imported"` (or similar) |
| Second `plan` (no change)     | "No changes"                                              |
| `apply` after rename          | In-place update succeeds, ID unchanged                    |
| `destroy`                     | Key disappears from `phala ssh-key list` in dashboard     |

## Fail signals

- Schema deserialization panic → struct tag mismatch between SDK and provider
- Update triggers replace → `RequiresReplace` planmodifier set incorrectly on `name`
- Read after apply shows different fingerprint → SDK response field misnamed
- `destroy` succeeds in TF but key still visible in dashboard → delete path
  ignoring SDK error or returning wrong status code

## Cleanup

```bash
rm /tmp/tf-test-key /tmp/tf-test-key.pub
```

# t1 — Data sources only

**Purpose**: confirm every read-only data source works against the new Go SDK
client. No mutations, no costs.

## What it exercises

| Surface           | SDK call                       |
| ----------------- | ------------------------------ |
| Auth bootstrap    | `phala.NewClient` + env-var fallback |
| `phala_workspace` | workspace lookup endpoint      |
| `phala_account`   | usage/billing summary endpoint |
| `phala_regions`   | catalog: regions               |
| `phala_sizes`     | catalog: instance sizes        |
| `phala_images`    | catalog: OS images             |
| `phala_nodes`     | nodes list                     |

## Run

```bash
cd e2e_tests/terraforms/t1-datasources
terraform plan
terraform apply -auto-approve
```

## Pass criteria

- `terraform plan` shows 0 to add / 0 to change / 0 to destroy (data sources only)
- `terraform apply` prints all 6 outputs with non-empty values:
  - `workspace_id` matches your `PHALA_CLOUD_API_KEY`'s workspace
  - `regions_count`, `sizes_count`, `images_count`, `nodes_count` are > 0
  - `account_summary` shows current credit balance + tier
- No diagnostics about "missing API key" or "client not configured"

## Fail signals

- "Missing API Key" → env var `PHALA_CLOUD_API_KEY` not exported
- "client not configured" → provider Configure path is broken
- 401 / 403 → API key valid but lacks read perms (regenerate)
- Empty list for one catalog → the SDK response struct field name doesn't
  match what the data source unmarshals into (regression!)

## Cleanup

```bash
terraform destroy -auto-approve   # data sources only, instant
```

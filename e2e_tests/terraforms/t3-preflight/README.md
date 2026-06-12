# t3 — App preflight (data source + resource)

**Purpose**: exercise `ProvisionCVM` end-to-end (the Go SDK's heaviest
provisioning entrypoint), without creating an actual CVM. Both surfaces — the
`phala_app_preflight` data source and resource — call the same endpoint; the
resource just persists the result to state.

This is the critical migration checkpoint for: KMS info marshaling, structured
response field mapping (`app_env_encrypt_pubkey`, `compose_hash`, `kms_info_json`,
`matched_node_id`).

## Run

Needs the `hashicorp/random` provider — Terraform fetches it from the public
registry automatically:

```bash
cd e2e_tests/terraforms/t3-preflight
terraform init       # only because of random provider; phala uses dev_overrides
terraform plan       # ~2 to add (random + resource preflight)
terraform apply -auto-approve

terraform show
terraform output ds_kms_info_json | jq .   # validate JSON shape
terraform output res_kms_info_json | jq .  # should match
```

Note: `terraform init` is fine here even with dev_overrides — Terraform skips
the override'd provider and only fetches `random`. You'll see a warning like
"dev_overrides are in effect" — that's the signal it's working.

## Pass criteria

- Both data source and resource produce identical `compose_hash` for identical
  compose input
- `app_env_encrypt_pubkey` is a non-empty hex string (used for client-side env encryption)
- `kms_info_json` parses as JSON with `id`, `url`, and key fields
- `matched_node_id` is non-zero (a real node was selected)
- `terraform plan` after `apply` shows no drift on the resource

## Fail signals

- Empty `kms_info_json` → `*phala.CvmKmsInfo` → JSON marshal path lost the data
- `compose_hash` mismatch between data source and resource → `ProvisionCVM`
  produces non-deterministic output (server-side bug) OR the two code paths
  build the request differently (provider regression)
- `node_id` is null but `matched_node_id` is set → the optional-Int64 pointer
  mapping is wrong (the `*int` vs `int` distinction)
- `plan` after `apply` shows continual drift → Read path doesn't repopulate
  some Computed field

## Cleanup

```bash
terraform destroy -auto-approve
```

Preflight doesn't allocate cloud resources, so destroy is a no-op state clear.

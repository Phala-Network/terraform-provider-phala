# End-to-end tests for terraform-provider-phala

This directory contains end-to-end tests that drive the locally-built
provider against a real Phala Cloud workspace. Today the only surface is
`terraforms/` (HCL-driven scenarios); the layout is forward-looking — future
SDK-level or CLI-level e2e flows would land as siblings of `terraforms/`.

## Layout

```
e2e_tests/
  README.md            ← this file: environment setup + Makefile reference
  Makefile             ← setup / check-env / clean targets
  terraforms/          ← Terraform-based e2e scenarios
    README.md          ← scenario index + per-scenario workflow + pitfalls
    t1-datasources/    ← scenario directories (HCL + README each)
    t2-ssh/
    ...
```

## Why a local-build harness, not the public registry

`terraform-provider-phala` is unpublished at the time of writing — there is
no `phala-network/phala` entry on registry.terraform.io. The harness uses
Terraform's `dev_overrides` mechanism to point at a locally-built provider
binary, completely bypassing the registry. Every `terraform plan`/`apply`
forks that binary fresh, so any change to provider Go source (or the
sibling `sdks/go` SDK pulled in via `replace`) becomes effective on the next
`make build`. No `terraform init` re-run is needed to pick up the new
binary.

## Environment setup

### 1. Build the provider binary + write the dev_overrides config

```bash
cd e2e_tests
make setup
```

`make setup` delegates to the provider repo root and runs:
- `make build` — compiles `./` into `/tmp/phala-tf-dev/terraform-provider-phala`
- `make devrc` — writes `/tmp/phala-tf-dev/terraformrc` containing the
  `dev_overrides` block that pins `phala-network/phala` to that binary

### 2. Export auth + CLI config

```bash
export PHALA_CLOUD_API_KEY="<your-api-key>"
export TF_CLI_CONFIG_FILE=/tmp/phala-tf-dev/terraformrc

# Optional; defaults to https://cloud-api.phala.com/api/v1
# export PHALA_CLOUD_API_PREFIX="..."
```

Consider putting these in `~/.zshrc` / `~/.bashrc` so they survive new shells.

### 3. Verify

```bash
make check-env
```

Expected output:

```
OK: PHALA_CLOUD_API_KEY set
OK: TF_CLI_CONFIG_FILE=/tmp/phala-tf-dev/terraformrc
OK: provider binary at /tmp/phala-tf-dev/terraform-provider-phala
```

### 4. Pick a scenario

See [`terraforms/README.md`](./terraforms/README.md) for the full scenario
index, the recommended order, and per-scenario pass criteria. Quick start
with the cheapest scenario:

```bash
cd terraforms/t1-datasources
terraform plan
terraform apply -auto-approve
```

`t1-datasources`, `t2-ssh`, `t3-preflight`, `t7-structured-errors` are
zero-cost (no CVM allocated). The rest provision real CVMs and bill until
`terraform destroy` runs — each scenario README has a Cost section.

## Rebuilding after code changes

Modified provider Go code under `internal/`, `go.mod`, or the SDK at
`../go/`?

```bash
cd e2e_tests
make build       # rebuilds binary in place; devrc unchanged
```

Then re-run the scenario — no init, no destroy required just for the
rebuild. The next `terraform plan` forks the freshly-built binary.

## Cleanup

Remove all scenario `.terraform/`, lock files, and state files in one shot:

```bash
make clean
```

Also remove the built provider binary and CLI config:

```bash
make clean-all
```

`make clean` is the recovery step when you get the recurring
`phala-network/phala: no available releases match` error during `terraform
init` — that error means stale `terraform.tfstate` is pinning the provider
in version-resolution. See `terraforms/README.md` "Critical" section for
the full failure-mode write-up.

## Makefile targets

| Target | What it does |
| --- | --- |
| `make help` | List all targets (default) |
| `make setup` | `make build && make devrc` |
| `make build` | Build provider binary into `/tmp/phala-tf-dev/` |
| `make devrc` | Write `/tmp/phala-tf-dev/terraformrc` (dev_overrides) |
| `make check-env` | Validate `PHALA_CLOUD_API_KEY`, `TF_CLI_CONFIG_FILE`, binary presence |
| `make clean` | Remove `.terraform/`, lock, state, backup in every `terraforms/t*/` |
| `make clean-all` | `make clean` + remove `/tmp/phala-tf-dev/` entirely |

## Costs

Five scenarios provision billable CVMs (typically `tdx.small`). The
scenarios are designed to be cheap-and-fast — small instances, short-lived,
docker-compose that boots in seconds — but they bill on wall time until
`terraform destroy` completes. Always destroy. The `clean` target removes
local state but does NOT destroy cloud-side resources, so destroy first,
clean second.

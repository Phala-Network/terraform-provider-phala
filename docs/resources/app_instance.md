---
page_title: "phala_app_instance Resource - phala"
subcategory: ""
description: |-
  Manages a single named CVM instance under an existing phala_app. `name` is the stable logical member key (e.g. `consul-0`, `worker-3`) — it survives CVM replacement and binds the Terraform resource to a durable slot under the app's replica set. Backed by `POST /apps/{app_id}/instances` with a custom instance name.
---

# phala_app_instance (Resource)

Manages a single named CVM instance under an existing phala_app. `name` is the stable logical member key (e.g. `consul-0`, `worker-3`) — it survives CVM replacement and binds the Terraform resource to a durable slot under the app's replica set. Backed by `POST /apps/{app_id}/instances` with a custom instance name.

`phala_app_instance` is the per-member primitive for stateful replica sets on
Phala Cloud. It binds a Terraform resource to a stable **slot name** under an
existing `phala_app`. The cloud API guarantees that the slot name is unique
within the workspace and immutable on the CVM occupying it, so the slot can
be used as a durable logical-member identity (e.g. `consul-0`, `worker-3`)
even when the underlying CVM is replaced.

| Concept     | Mapping                                                        |
|-------------|-----------------------------------------------------------------|
| `app_id`    | replica set / application                                       |
| `name`      | logical member key, operator-chosen, immutable (forces replace) |
| `vm_uuid`   | current concrete CVM occupying the slot                         |
| `instance_id` | runtime/network/workload identity                             |

## MIG-style usage

For a stateful replica set with N named members, declare `phala_app` with
the first slot's `name` and `phala_app_instance` with `for_each` over **all**
slot names — including the first. The instance whose name matches the
parent `phala_app.name` adopts the bootstrap CVM that `phala_app` provisions;
the rest are created via `POST /apps/{app_id}/instances`. This keeps the HCL
symmetric (one `for_each` block covers every member) and avoids any rename
or stub-CVM hackery.

## Example Usage

```terraform
# Stateful replica set on Phala Cloud, MIG-style.
#
# phala_app declares the full slot list via `members`. The provider validates
# at plan time that `name` is one of `members` and that `replicas` is not >1.
# phala_app_instance iterates over `phala_app.consul.members` so the slot list
# stays a single source of truth — invariant by construction.
#
# The instance whose name matches phala_app.consul.name "adopts" the
# bootstrap CVM (no API call, just a state binding). The others post to
# /apps/{id}/instances. Terraform ID for each slot is "<app_id>:<name>" —
# durable; survives CVM replacement.

resource "phala_app" "consul" {
  name    = "consul-0"
  members = ["consul-0", "consul-1", "consul-2"]

  size      = "tdx.small"
  region    = "US-WEST-1"
  image     = "dstack-dev-0.5.7-9b6a5239"
  disk_size = 40

  gateway_enabled = true

  docker_compose = file("${path.module}/consul-compose.yaml")

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

resource "phala_app_instance" "consul" {
  for_each = toset(phala_app.consul.members)

  app_id = phala_app.consul.app_id
  name   = each.value

  wait_for_ready       = true
  wait_timeout_seconds = 900
}

output "consul_member_uuids" {
  description = "Stable slot -> current vm_uuid map. The slot key is durable; vm_uuid may change on replacement."
  value       = { for k, v in phala_app_instance.consul : k => v.vm_uuid }
}
```

## Adoption vs creation (`managed`)

- **Adopted** (`managed = false`): the named CVM already existed under the
  app when this resource was created. The provider just records the binding
  in Terraform state. This is the case for the `phala_app_instance` whose
  name equals `phala_app.name` — `phala_app` provisioned that CVM and owns
  its lifecycle. Destroying this resource only drops the binding; the CVM
  stays alive until `phala_app` is destroyed.
- **Managed** (`managed = true`): the provider created the CVM via
  `POST /apps/{app_id}/instances`. Destroying this resource deletes the CVM
  on the cloud.

`managed` is set at Create time and persists in state. Imported resources
default to `managed = true`. If you imported the bootstrap CVM and want it
to behave like an adopted instance, edit the state to set `managed = false`.

## Invariant: `phala_app.name` must be in the slot list

For the adoption to actually save you a CVM, `phala_app.name` must equal one
of the slot names you declare via `phala_app_instance`. If it doesn't, the
bootstrap CVM has nothing to adopt it and becomes an unreferenced extra
under the app — still billed, still managed by `phala_app`, but not visible
in any `phala_app_instance.*` state.

The cleanest enforcement is to declare the slot list on `phala_app` via the
[`members`](app.md) attribute. The provider then validates at plan time
that `phala_app.name` is one of `phala_app.members` and that the legacy
`replicas` path isn't in use, and the example below derives the
`phala_app_instance` `for_each` directly from `phala_app.consul.members` so
the two stay aligned by construction.

## Caveats

- The Terraform ID is `<app_id>:<name>`. Import via
  `terraform import phala_app_instance.foo app_abcdef...:consul-1`.
- `name`, `app_id`, and the optional override fields (`node_id`,
  `docker_compose`, `pre_launch_script`, `encrypted_env`, `compose_hash`)
  all force replacement. Compose / env updates that should apply across the
  whole replica set must go through `phala_app`.
- Do **not** set `phala_app.replicas > 1` while declaring named
  `phala_app_instance` resources for the same app. The extra anonymous
  replicas come from the legacy `/cvms/{src}/replicas` endpoint (no naming)
  and will collide with the named-slot model. Worse, if you later set
  `replicas = 1` explicitly, `phala_app`'s in-place reconcile will scale
  the app *down* by deleting CVMs — including ones owned by your
  `phala_app_instance` resources. Leave `replicas` at its default when
  using named instances.
- The on-chain KMS two-phase (prepare/commit) flow that the cloud API exposes
  via HTTP 465 is not yet wired up here. `phala_app_instance` currently
  supports only the single-call PHALA KMS flow.

<!-- schema generated by tfplugindocs -->
## Schema

### Required

- `app_id` (String) Phala app identifier (replica set) this instance belongs to.
- `name` (String) Stable logical member name (5-63 chars, starts with a letter, letters/digits/hyphens only). Immutable; renaming forces replacement.

### Optional

- `compose_hash` (String) Optional explicit compose hash. When omitted the backend resolves it from `docker_compose` (if provided) or the app's current revision. Changing forces replacement.
- `docker_compose` (String) Optional override Docker Compose YAML for this instance. When omitted, the backend uses the app's template instance. Changing forces replacement.
- `encrypted_env` (String, Sensitive) Optional hex-encoded encrypted env payload to seed at create time.
- `node_id` (Number) Optional target node (teepod) ID for placement. Changing this forces replacement.
- `pre_launch_script` (String) Optional pre-launch script content. Changing forces replacement.
- `wait_for_ready` (Boolean) Wait until the new instance reports `running` before returning.
- `wait_timeout_seconds` (Number) Wait timeout for create / wait-for-ready, in seconds.

### Read-Only

- `created_at` (String) CVM creation timestamp (ISO-8601).
- `endpoint` (String) Primary public endpoint URL of the CVM.
- `id` (String) Terraform ID. Format: `<app_id>:<name>`.
- `instance_id` (String) Runtime/network identity reported by the cloud.
- `instance_type` (String) Instance type (e.g. `tdx.small`) of the underlying CVM.
- `managed` (Boolean) Whether this resource created the underlying CVM (true) or adopted an existing one — typically the bootstrap CVM owned by `phala_app` when `phala_app.name` matches this resource's `name` (false). Adopted instances skip the API delete call on destroy; the parent `phala_app` owns the CVM lifecycle.
- `region` (String) Region of the CVM currently occupying this slot.
- `status` (String) Current CVM status.
- `vm_uuid` (String) Current CVM UUID occupying this slot. Changes when the CVM is replaced.



# `PATCH /cvms/{cvm_id}/name` — Public-API rename endpoint

Verified 2026-05-18 against `https://cloud-api.phala.com/api/v1` with a
normal user `phak_*` API key (a test workspace). Captured here
because the OpenAPI spec under-documents the behavior and provider code
needs to react to it correctly.

## Why we care

Considered for **Pattern C** of the stable-slot design (see
`docs/design-notes/stateful-replica-set-design.md`): have `phala_app`
provision a bootstrap CVM with a generated placeholder name, then let the
first `phala_app_instance` adopt it by renaming. The cloud-side atomic
duplicate-name check would resolve concurrent claims.

We ultimately favored a different shape (see the design note), but the
endpoint is still useful for `phala_cvm` rename support, recovery tooling,
and CLI parity.

## Endpoint shape

| Field | Value |
| --- | --- |
| HTTP method | `PATCH` |
| Path | `/api/v1/cvms/{cvm_id}/name` |
| `cvm_id` form | The `cvm_xxx` string — **not** `vm_uuid`, **not** `app_id`. Pull from `cvmAPIResponse.idString()`. |
| Request body | `{"name": "<new-name>"}` (RFC 1123 hostname rules; `5..63` chars, leading letter, `[A-Za-z0-9-]`) |
| Success | `HTTP 204 No Content` (empty body) |
| Duplicate name | `HTTP 400` with body `{"detail":"CVM name \"<n>\" is already in use in this workspace"}` |
| Headers | `X-API-Key: phak_...`, `X-Phala-Version: 2026-01-21`. No admin token, no cookies required. |

## Atomicity

Verified: a duplicate-name attempt deterministically returns HTTP 400 with
the `already in use in this workspace` detail. There is no silent success
path and no 5xx flake. This is sufficient to use the endpoint as a racy
"claim" primitive — losers can detect the conflict by status code or by
the `detail` substring.

## Gotchas

1. **Identifier type**. The endpoint takes the `cvm_xxx` string ID, not
   the `vm_uuid`. Most other provider call sites already route through
   `cvmPath(id)` with `selectReplicaIdentifier(...)` which prefers
   `vm_uuid`. For rename, fetch and pass `idString()` instead.

2. **OpenAPI under-documentation**. The schema only advertises 204 and
   422. The real 400-on-conflict is not in the spec. Provider error
   handling should match on status code (`400`) **and** detail substring
   (`already in use`), not on the schema-typed error.

3. **Phala CLI**. There is no `cvms rename` subcommand. Use
   `phala api -X PATCH /cvms/<cvm_id>/name -F name=<new>` or raw
   curl.

## Test recipe (smoke)

```bash
phala api -X PATCH "/cvms/$CVM_ID/name" -F name="$NEW_NAME"
# Expect: empty body, status 204 (CLI may surface this as "no output").

# Conflict probe:
phala api -X PATCH "/cvms/$CVM_ID/name" -F name="$EXISTING_NAME_IN_WORKSPACE"
# Expect: HTTP 400 with body containing "already in use in this workspace".
```

Remember to rename back to the original name afterwards if you renamed a
real CVM during testing.

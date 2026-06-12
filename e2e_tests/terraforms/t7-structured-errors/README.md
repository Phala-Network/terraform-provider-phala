# t7 — Structured API error formatting

**Purpose**: confirm `APIError.IsStructured()` + `FormatError()` produce useful
diagnostics, not just a raw HTTP body dump. This is the
`diagnosticFromAPIError` upgrade landed in the migration's `cvm_helpers.go`.

The expected runtime contract:

```
if apiErr.IsStructured() {
    return fmt.Sprintf("[%s] %s", apiErr.ErrorCode, apiErr.Message), apiErr.FormatError()
}
```

So a structured error shows up in Terraform diagnostics as:

```
Error: [some_error_code] Top-level message

<detailed multi-line formatted error from FormatError()>
```

## Run

```bash
cd e2e_tests/terraforms/t7-structured-errors
terraform plan 2>&1 | tee /tmp/t7-output.txt
```

Then eyeball `/tmp/t7-output.txt`.

## Pass criteria

| Check                                                | Expected                                      |
| ---------------------------------------------------- | --------------------------------------------- |
| `plan` exits non-zero                                | Yes                                           |
| Top-line error has `[ERROR_CODE]` prefix or similar  | Yes (this is from the structured branch)      |
| Detail includes the API's specific complaint (hint/field) | Yes                                       |
| No raw `{"error":"..."}` JSON dump                   | Pass (means structured path is taken)         |

## Variant: unstructured fallback

To verify the fallback branch (when the API returns a non-structured error),
temporarily make the API endpoint unreachable:

```bash
PHALA_CLOUD_API_PREFIX=https://does-not-exist.invalid/api/v1 terraform plan 2>&1
```

Expected: error mentions network / DNS / connection-refused style message
(the SDK's transport-layer error, not `IsStructured == true`). The provider
should still produce a clean diagnostic, not a panic.

## Fail signals

- Error output is `<nil>` or empty → diagnostic mapping is dropping the
  structured info
- Panic / stack trace → unsafe type assertion in the error path
- The structured branch never fires (always falls through to fallback) →
  `IsStructured()` returns false when it shouldn't (SDK regression or
  provider's helper misuses it)

## Cleanup

Nothing to clean — `plan` doesn't write state.

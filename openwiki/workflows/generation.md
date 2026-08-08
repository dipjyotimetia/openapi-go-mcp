---
type: Playbook
title: Generation inputs, filtering, and batch behavior
description: How openapi-go-mcp expands specification inputs, determines exposed operations, reports diagnostics, and handles generation exit states.
tags: [workflow, cli, batch-generation, x-mcp]
openwiki:
  roles: [workflow, domain]
  source_paths: [cmd/openapi-go-mcp/main.go, pkg/loader/expand.go, pkg/batch/batch.go, pkg/generator/filter.go]
  test_paths: [pkg/loader/expand_test.go, pkg/generator/filter_test.go, tests/e2e/cli_batch_test.go]
  invariants: [Operation-level x-mcp takes precedence over path-level and document-level values., A batch collision prevents all output writes.]
---

# Generation inputs, filtering, and batch behavior

## Accepted `-spec` forms

`-spec` accepts a local file, an HTTP(S) URL, a glob, a recursively walked directory, or a comma-separated combination. Expansion is deterministic: references are converted to absolute paths when local, then sorted and deduplicated.

| Form | Behavior |
|---|---|
| File | Process exactly that file, regardless of extension. |
| URL | Load one remote document. URLs are supported only in single-spec mode because batch output needs a stable filename slug. |
| Glob | Use `filepath.Glob`; `*`, `?`, and `[]` are supported. `**` is not recursive glob syntax—use a directory instead. |
| Directory | Recursively include `.yaml`, `.yml`, and `.json`; skip dot-files, dot-directories, and symlinks. |
| Comma list | Expand each entry, then sort and deduplicate the combined list. |

An empty glob or directory match is an error. In a multi-spec invocation, `-package` and `-emit-v3` are rejected because their single shared value would be ambiguous.

## Exposing operations with `x-mcp`

By default, every operation is included unless excluded. Set `x-mcp: false` on the OpenAPI document root, a path item, or an operation to exclude it. `x-mcp: true` opts an operation back in. The closest declaration wins:

1. Operation
2. Path item
3. Document root
4. CLI default

`-exclude-by-default` changes the final fallback: only explicitly opted-in operations are included. Boolean values may be native booleans or case-insensitive `"true"` / `"false"` strings. An unrecognized declared value produces a warning and falls back to the current default rather than silently honoring a typo.

```yaml
paths:
  /admin:
    x-mcp: false
    get:
      operationId: listAdmins
      x-mcp: true
```

Here `listAdmins` is included because its operation-level value overrides its path-level exclusion.

## Batch output planning

For multiple resolved specs, the filename stem becomes a lowercase alphanumeric slug. The slug must be nonempty and start with a letter. For `billing-api.yaml`, the slug is `billingapi` and default batch outputs are:

| Mode | Package | Output directory | Derived import or module |
|---|---|---|---|
| Companion | `billingapimcp` | `<out>/billingapimcp` | `<client-import>/billingapi` |
| Proxy | `billingapimcp` | `<out>/billingapimcp` | `<module>/billingapi` |

Before loading or writing output, the CLI detects duplicate slugs across plans. This avoids two specs writing the same package directory. Once planning succeeds, individual spec failures do not stop later plans; errors are reported with the originating spec and the overall process exits with code `3` if any plan failed.

## Diagnostics and exit codes

Generator diagnostics are severity-grouped and stable for CI output. `-warnings-as-errors` changes an otherwise successful run with warnings to exit code `4`.

| Exit code | Meaning |
|---:|---|
| 0 | Success |
| 1 | Flag misuse or missing required argument |
| 2 | Unloadable, malformed, or otherwise bad specification input |
| 3 | Generation or output write failure, including a partial batch failure |
| 4 | Warning diagnostics treated as errors |

Use `-list` to inspect operations without generation. Use `-force` only when intentionally replacing an existing generated MCP file.

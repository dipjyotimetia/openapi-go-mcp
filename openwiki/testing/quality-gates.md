---
type: Testing Guide
title: Quality gates and test strategy
description: Repository validation commands, CI checks, golden generation coverage, adapter parity, end-to-end MCP tests, and example regeneration requirements.
tags: [testing, ci, quality, go]
openwiki:
  roles: [testing, operations, repository]
  source_paths: [Makefile, .github/workflows/ci.yml, pkg/generator/golden_test.go, tests/e2e/cli_proxy_test.go]
  validation_commands: [go test ./..., go vet ./..., go build ./cmd/openapi-go-mcp]
---

# Quality gates and test strategy

The project is a Go 1.26 module. Its test strategy combines focused package tests, generator golden output, SDK adapter parity coverage, and black-box MCP stdio tests.

## Local validation

| Command | Purpose |
|---|---|
| `make build` | Build `bin/openapi-go-mcp`. |
| `make test` | Run `go test ./...`. |
| `make test-race` | Run all tests with `-race -count=1`. |
| `make vet` | Run `go vet ./...`. |
| `make lint` | Run `golangci-lint` using `.golangci.yml`. |
| `make smoke` | Start the go-sdk petstore example over stdio and verify `initialize` and `tools/list`. |
| `make regen-examples` | Regenerate checked-in example clients and MCP companions; requires `oapi-codegen` on `PATH`. |

When generator output intentionally changes, refresh and review the companion golden file with:

```bash
UPDATE_GOLDEN=1 go test ./pkg/generator/...
```

The golden test is a strict formatting and behavior regression net, so a golden update should accompany a deliberate review of generated source changes.

## CI workflow

GitHub Actions runs on pushes and pull requests targeting `main`:

1. Test job: downloads modules, runs `go vet ./...`, then `go test ./... -race -count=1 -coverprofile=coverage.out`.
2. Lint job: runs the configured `golangci-lint` action.
3. Build job: builds the CLI and uses `-list` against both OpenAPI 3 and Swagger 2 fixtures.
4. Examples job: installs pinned `oapi-codegen` v2.7.0, runs `make regen-examples`, fails if regeneration leaves a diff, then builds all packages.

The examples check means generated source is checked in intentionally and must remain reproducible with the pinned client generator.

## Test layers

- `pkg/loader/*_test.go` covers spec loading, URL behavior, expansion, and v3 emission.
- `pkg/batch/batch_test.go` covers slug derivation, option planning, and collision detection.
- `pkg/generator/*_test.go` covers operation collection, schemas, filtering, security, rendering, proxy scaffolds, diagnostics, and write behavior.
- `pkg/runtime/*_test.go` covers decoding, HTTP projection, authentication, options, proxy helpers, adapters, and validation.
- `pkg/dynamic/*_test.go` covers dynamic registration and its security boundaries.
- `tests/e2e/` exercises the CLI and generated servers through MCP stdio, including proxy scaffolds, batch mode, non-JSON bodies, complex schemas, and both SDK adapters.

Use the narrowest applicable package test while iterating; run the broader commands before integration-sensitive changes, generated-template changes, runtime adapter changes, or example updates.

# Roadmap

Items move into a release when picked up. No timelines.

## Generator

- [ ] **Dynamic (no-codegen) registration path** — accept a spec at runtime and register tools via reflection / a `Register(spec, client)` library entry point, in addition to the current AOT codegen. See `docs/design-decisions.md` decision #1 for the trade-off.

## Runtime

- [ ] **Streaming responses (SSE, chunked)** — first-class support. Today non-JSON responses round-trip as base64 / text; SSE and chunked transfer-encoding still surface as raw bytes.

## Distribution

- [ ] **`HOMEBREW_TAP_GITHUB_TOKEN` repo secret + `dipjyotimetia/homebrew-tap` repo** — until both exist, the cask step in the release workflow falls back to `GITHUB_TOKEN` and the formula push fails silently. The install line in README + release notes won't resolve until this is fixed.

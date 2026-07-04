# Roadmap

Planned work that is out of scope for the current release. Each item links back
to the design docs that explain why it isn't shipped yet. Deliberate non-goals
(streaming as a default, schema dialects beyond default + OpenAI-compat,
discriminator branch logic) live in
[`docs/design-decisions.md`](docs/design-decisions.md#non-goals) and are not
repeated here.

## Dynamic (no-codegen) registration

A runtime library that parses an OpenAPI spec on startup and registers MCP
tools dynamically, without the `*.mcp.go` codegen step. Companion codegen stays
the default for the reasons in
[design decision 1](docs/design-decisions.md#1-companion-code-generation-not-runtime-introspection);
the dynamic path is for tooling that can't run a generator (plugins, spec
playgrounds).

## First-class streaming responses

SSE and chunked responses currently surface as raw bytes in the tool result
([known limitations](docs/architecture.md#known-limitations)). First-class
support means incremental delivery through the MCP progress/partial-result
channel once the SDKs settle on a shape.

## Richer MCP result types

Binary/image responses are base64-encoded into text today. Emitting native MCP
`ImageContent` (and embedded resources) for matching response content types
would let MCP clients render them directly.

## Parameter style lowering

`style` / `explode` / `allowReserved` beyond the `form`/`simple` defaults
(deepObject, matrix, label, spaceDelimited, pipeDelimited) are detected and
warned about (`unsupported-parameter-style`) but not lowered. Companion mode
inherits whatever oapi-codegen does; proxy mode stringifies with the simple
defaults.

## Multipart arrays of binary items

Binary leaves under `items` schemas are not unpacked into per-element parts
([known limitations](docs/architecture.md#known-limitations)). Requires a
runtime contract for one-part-per-element fan-out.

# Roadmap

Planned work that is out of scope for the current release. Each item links back
to the design docs that explain why it isn't shipped yet. Deliberate non-goals
(streaming as a default, schema dialects beyond default + OpenAI-compat,
discriminator branch logic) live in
[`docs/design-decisions.md`](docs/design-decisions.md#non-goals) and are not
repeated here.

## First-class streaming responses

SSE and chunked responses currently surface as raw bytes in the tool result
([known limitations](docs/architecture.md#known-limitations)). First-class
support means incremental delivery through the MCP progress/partial-result
channel once the SDKs settle on a shape.

## Multipart arrays of binary items

Binary leaves under `items` schemas are not unpacked into per-element parts
([known limitations](docs/architecture.md#known-limitations)). Requires a
runtime contract for one-part-per-element fan-out.

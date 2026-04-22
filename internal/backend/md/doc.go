// Package md is the Markdown record.Backend implementation. It scans
// an MD buffer for ATX headings (`#` .. `######`), derives a slug from
// each heading's text, and exposes List / Find / Emit / Splice over
// those sections. The MVP field layout is body-only (§5.3.3): the
// heading-plus-body bytes are the record.
//
// Design notes:
//
//   - Strict col-0 ATX only. CommonMark allows 0-3 spaces of leading
//     indent before `#`; this scanner requires exactly col 0. Agents
//     write all content through `create` / `update`, so the tool
//     controls what lands on disk and can stay strict. Human hand-edits
//     that introduce indented headings will simply not be recognised —
//     documented as a limitation per V2-PLAN §5.3.5.
//
//   - Fenced-code awareness: ```` ``` ```` and `~~~` fences are
//     tracked; `#` lines inside them are treated as content, not
//     headings. A closing fence must use the same char and be at least
//     as long as the opener.
//
//   - Setext (`Heading\n====`) is ignored on read. Tool-emitted content
//     never produces setext; hand-edited setext headings are a
//     documented limitation.
//
//   - Per-type heading level: unlike the TOML backend, the MD backend
//     is stateful. A Backend carries the heading level (1..6) of the
//     record type it serves so Emit can render the correct number of
//     `#` chars without requiring schema knowledge inside the backend.
//     Construct with NewBackend(level). This divergence from the
//     stateless TOML shape is justified because MD sections are
//     identified by heading level; there is no purely format-level way
//     to emit the right level without an input.
package md

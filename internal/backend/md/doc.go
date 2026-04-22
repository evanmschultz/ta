// Package md is the Markdown record.Backend implementation. It scans
// an MD buffer for ATX headings (`#` .. `######`), derives a slug from
// each declared heading's text, and exposes List / Find / Emit / Splice
// over those declared sections. The MVP field layout is body-only
// (§5.3.3): the heading-plus-body bytes are the record.
//
// Design notes:
//
//   - Schema-driven sectioning (V2-PLAN §2.10 / §5.3.2). NewBackend
//     takes a list of record.DeclaredType values; each type's Heading
//     selects which ATX heading level counts as a record boundary.
//     Headings at non-declared levels are body content of the
//     enclosing declared section — they do NOT split sections. This is
//     what lets authors use H3+ as free-form subheadings inside a
//     record body without declaring each as its own type.
//
//   - Body range runs from a declared heading to the start of the
//     next declared heading (at any declared level), or EOF for the
//     last (V2-PLAN §2.11 / §5.3.2). Non-declared headings between
//     two declared boundaries are absorbed into the first record's
//     body.
//
//   - Slug uniqueness is per declared level within a file. Two H2s
//     with slug "install" is a collision (refused at read and write).
//     An H2 "install" and an H3 "install" do not collide — different
//     declared types at different levels. Non-declared levels don't
//     participate in the collision check at all.
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
//   - Setext (`Heading\n====`) is ignored on read. Tool-emitted
//     content never produces setext; hand-edited setext headings are
//     a documented limitation.
package md

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
//   - Hierarchical addressing (V2-PLAN §5.3.2 / §5.5, 2026-04-21
//     refinement). A record at declared level N is addressed by
//     "<type>.<chain>" where <chain> is the ordered slugs at DECLARED
//     heading levels from the shallowest down to this heading's own
//     level. An H3 "Prereqs" under H2 "Install" under H1 "ta" with
//     H1+H2+H3 all declared resolves to
//     "subsection.ta.install.prereqs". If only H2 and H3 were
//     declared, the chain would start at H2 →
//     "subsection.install.prereqs". Orphan headings at a declared
//     level whose immediate declared parent is absent from the buffer
//     compose their chain from the declared ancestors that ARE present
//     — e.g. an H3 under an H1 with H2 declared-but-missing becomes
//     "subsection.<h1-slug>.<h3-slug>".
//
//   - Body range runs from a declared heading to the start of the
//     next heading at the SAME OR SHALLOWER declared level, or EOF
//     for the last such heading (V2-PLAN §2.11 / §5.3.2, 2026-04-21
//     refinement). Deeper declared headings are BOTH body bytes of
//     this record AND addressable records in their own right with
//     narrower nested ranges. Non-declared deeper headings are opaque
//     body bytes.
//
//   - Slug uniqueness is per FULL ADDRESS (i.e. per-parent, per
//     declared level). Two H3 "Prereqs" under the same H2 collide
//     because they produce the same address. Two H3 "Prereqs" under
//     different H2 parents do not collide — different parent chains
//     produce different addresses. Non-declared levels don't
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

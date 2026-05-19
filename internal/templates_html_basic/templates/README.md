# internal/templates_html_basic/templates — Track A (zero-deps)

Track A: zero-deps. No Astro, no pnpm, no stil. All templates author-as-plain-HTML.

This directory holds the **zero-dependency** Track-A HTML template set for ta's
record-to-HTML rendering. Templates here are authored as plain HTML5 files with
inline CSS. They are embedded into the ta binary at build time via
`//go:embed all:templates` in the parent `internal/templates_html_basic/`
package (droplet D-A2) and rendered with Go's `html/template` package
(droplet D-A3).

The companion Track B substrate (Astro + pnpm + stil-driven, with a build step)
lives under `web/templates_embed/` and is owned by a parallel droplet branch.
Track A and Track B are intentionally independent — a ta user who never wants a
build step still has Track A; a user who wants the full stil/Astro toolchain
has Track B.

## Authoring conventions

Templates in this directory MUST follow these conventions so the zero-deps
contract holds and so embedded rendering stays predictable:

- **Vanilla HTML5 only.** Each template starts with `<!DOCTYPE html>` and uses
  semantic HTML5 elements (`<header>`, `<main>`, `<section>`, `<article>`,
  `<footer>`).
- **Inline CSS only.** Styles go inside a single `<style>` element in `<head>`.
  No external stylesheets, no `<link rel="stylesheet">`, no CSS imports. This
  keeps each rendered record self-contained as a single file.
- **NO `<script>` tags.** Track A is zero-JS by design. No inline scripts, no
  external scripts, no event handlers (`onclick=`, etc.). Interactive needs
  belong on Track B.
- **No external assets.** No `<img src="https://...">`, no remote fonts, no
  CDN references. A rendered template must work fully offline as a single
  `.html` file.
- **Field placeholders match `.ta/schema.toml` typed-field names.** Placeholder
  tokens use Go `html/template` action syntax (`{{ .field_name }}`) and the
  field names mirror the schema types exactly — e.g. `cascade_drop.html` uses
  `{{ .drop_number }}`, `{{ .title }}`, `{{ .role }}`, `{{ .state }}`,
  `{{ .structural_type }}`, `{{ .created_at }}`, `{{ .updated_at }}`. Optional
  fields appear inside `{{ if .field }} ... {{ end }}` guards.
- **One template per ta type.** Filename mirrors the dotted ta type name with
  `.` replaced by `_` and a `.html` suffix — e.g. `cascade.drop` →
  `cascade_drop.html`. The renderer (droplet D-A3) resolves type → template by
  this convention.
- **Accessibility baseline.** Use semantic landmarks, `<title>` in `<head>`,
  `lang="en"` on `<html>`, and `aria-label` on regions where headings alone
  don't carry intent. WCAG AA contrast for any color choices.
- **Responsive without media queries where possible.** Use intrinsic sizing,
  `max-width`, and flexbox/grid so a single inline `<style>` block works from
  mobile to desktop. Add `@media` queries inside the same `<style>` block only
  when intrinsic sizing isn't enough.

## What's here today

- `sample/cascade_drop.html` — smoke-test template for `cascade.drop` records.
  Exercises every convention above. Lives under `sample/` (not at the root)
  because D-A1 only stands up the substrate; the production layout
  (`<type>/<name>.html` or flat `<type>.html`) is decided by D-A2/D-A3.

## What's NOT here

- No Go code. Embedding (`//go:embed`) lives in `internal/templates_html_basic/`
  (droplet D-A2). Renderer wiring lives in droplet D-A3.
- No build script, no `package.json`, no `node_modules`. Track A is zero-deps —
  if you reach for npm here, you want Track B (`web/templates_embed/`).

// Package templates_html_basic carries ta's Track A (zero-deps) HTML
// template substrate, embedded into the binary at compile time.
//
// Track A scope: zero-deps; no Astro; no pnpm; no stil. Templates are
// authored as plain HTML5 with inline CSS and no <script> tags, and are
// rendered with Go's stdlib html/template package (see droplet D-A3).
//
// Companion Track B (Astro + pnpm + stil-driven) lives under
// internal/templates_html_embed/ and is intentionally disjoint. A ta
// consumer who wants zero build-step overhead picks Track A; one who
// wants the full stil/Astro toolchain picks Track B.
//
// Embed source: templates live under
// internal/templates_html_basic/templates/, embedded verbatim. Go's
// //go:embed directive cannot traverse parent paths (no ".." in
// patterns) and rejects directory symlinks outright, so the embedded
// tree must be a subpath-visible real directory under this package.
//
// Public surface: EmbeddedBasicHTML() fs.FS — the read-only embed.FS
// rooted at "templates/". Walk it with fs.WalkDir, open files with
// the returned fs.FS's Open method, or pass it into html/template's
// template.ParseFS.
package templates_html_basic

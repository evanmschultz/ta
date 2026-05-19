// Package templates_html_embed exposes the Astro-built HTML template
// dist/ tree as a binary-embedded fs.FS for Track B (html-embed) of
// ta's dual template substrate.
//
// Track B's authoring source lives at web/templates_embed/ and is
// built by the magefile target TemplatesBuildEmbed (pnpm + Astro +
// stil). The build output lands at internal/templates_html_embed/dist/
// and is captured by the //go:embed directive in embed.go at compile
// time.
//
// At fresh clone the dist/ directory holds only a .keep placeholder;
// the //go:embed all:dist directive requires at least one file to
// compile. Running `mage TemplatesBuildEmbed` (when pnpm is
// available) populates dist/ with the real build output, which the
// next `go build` then bakes into the binary.
//
// Track B is disjoint from Track A (internal/templates_html_basic/);
// Track A is zero-deps stdlib html/template, Track B is the
// Astro+pnpm+stil toolchain. Both ship for v0.1.0.
package templates_html_embed

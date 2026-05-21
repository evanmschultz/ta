// Astro 6.1.10 config for the standalone stil example.
//
// outDir is the package-local default (examples/stil/dist/). The root
// //go:embed all:examples directive (root embed.go) picks up the built dist
// tree at compile time; this project does NOT write into
// internal/templates_html_embed/dist/ — that legacy embed is retired by
// drop_010 L2-C.
import { defineConfig } from 'astro/config';

export default defineConfig({});

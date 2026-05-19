// Astro 6.1.10 config for ta's HTML template substrate.
//
// outDir is RESOLVED FROM THIS package root (web/templates_embed/) and points
// UPWARD into the Go embed package's dist directory at
// internal/templates_html_embed/dist/. The Go side //go:embeds that dist tree
// at compile time (D-B3 wires the embed.FS); this build is what fills it.
import { defineConfig } from 'astro/config';

export default defineConfig({
  outDir: '../../internal/templates_html_embed/dist/',
});

# thariq/testdata — VHS capture procedure

This directory holds the visual-regression artifacts for the Thariq demo
pages (`web/templates_embed/src/pages/thariq/*.astro`). It is committed so
the L3-E4 D5b VHS capture work has a stable destination.

## What lives here

For each demo page, three sibling files:

| File           | Owner                | Notes                                                         |
| -------------- | -------------------- | ------------------------------------------------------------- |
| `<page>.tape`  | orchestrator-direct  | VHS tape script driving the headless browser against the page |
| `<page>.gif`   | orchestrator-direct  | Rendered animated capture                                     |
| `<page>.txt`   | builder (D5a stub)   | ASCII snapshot — stubbed by D5a, populated by VHS in D5b      |

D5a (the qa-twins droplet) lands the `.txt` stub markers with a one-line
description per page. D5b is the orchestrator-direct capture pass that runs
`vhs` against the built `dist/thariq/*.html`, fills the `.gif` and `.txt`
artifacts in place, and (if needed) raises a `cascade.failure` if VHS can't
run inside the sandbox.

## VHS capture procedure (D5b — orchestrator-direct)

1. Build the embedded dist tree:

   ```sh
   mage TemplatesBuildEmbed
   ```

   This produces `internal/templates_html_embed/dist/thariq/*.html`.

2. Serve the dist tree locally (any static server is fine):

   ```sh
   pnpm --filter @ta/templates-embed run preview
   # or:
   npx -y http-server internal/templates_html_embed/dist -p 4321 -c-1
   ```

3. For each demo page, run its tape:

   ```sh
   cd web/templates_embed
   vhs testdata/thariq/index.tape
   vhs testdata/thariq/drop-dashboard.tape
   vhs testdata/thariq/planner-detail.tape
   vhs testdata/thariq/droplet-kanban.tape
   vhs testdata/thariq/qa-twins.tape
   ```

   Each `.tape` writes a sibling `.gif` and `.txt` next to itself. Commit
   all three artifact types per page when satisfied with the capture.

4. If `vhs` cannot allocate a PTY (sandboxed agent runtime), do NOT mark
   D5b complete. Raise a `cascade.failure` against the L3-E4 close gate and
   route the capture to a TTY-bearing session.

## Why this is orchestrator-direct

VHS needs a real terminal allocation; builder agents commonly run under a
PTY-less sandbox. Per `CASCADE_METHODOLOGY.md`, work that cannot run
inside an agent's sandbox lives outside the builder droplet contract.

## Determinism contract

The mock data driving these pages is FROZEN (`src/mock/thariq_records.ts`).
No `new Date()`, no `Math.random()`. Every VHS run against the same
`dist/thariq/*.html` MUST produce byte-identical `.txt` output. If a
re-capture diverges, that is a real change worth committing — not flake.

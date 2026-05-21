# HANDOFF — pre-mvp-feature-complete (drop_004 closed prematurely)

> ta is NOT mvp-feature-complete. drop_004 L1 was marked complete but real gaps were never closed. Do NOT use semver framing ("v0.1.0", "v0.2.0", etc.) — say "pre-mvp-feature-complete" or "next slice" instead. The dev has flagged this twice.

## 0. State at handoff

- 0.1 **Branch:** `main`. Working tree clean (verify via `git status`).
- 0.2 **Last commits this session (newest first):**
  - `docs: pre-mvp tracker + tui guidance + cascade index audit`
  - `feat(dispatch): ta cascade agent dispatcher + 9 tightened personas + backend chains`
  - `docs(cascade): drop_004 mvp-feature-complete l1 close audit` (premature label — see §1)
  - `feat(initapply): preflight conflicts + content-aware sameContent + dest-path enrichment (drop_004 l3-g2 d3-d5)`
  - `refactor(install-hygiene): extract install logic to internal/install_hygiene package (drop_004 l3-g2 d1+d2)`
  - `docs(cascade): drop_004 l3 close audit trail`
  - `refactor(init): rename --target-system flag to --bootstrap-home (drop_004 l3-i4)`
- 0.3 **mage check:** green at 1608 / 0 fail / 10 expected skips across 28 packages (as of last verification).
- 0.4 **Astro dev server still running at http://127.0.0.1:4321/** (background process from prior session — kill or restart as needed). Routes:
  - `/` smoke root
  - `/stil/` Track B stil gallery + 7 view pages
  - `/thariq/` Track B thariq demos (currently the source of dev frustration — see §3)
  - `/track-a/<view>.html` Track A Go-template-rendered goldens (7 files)
- 0.5 **drop_004 cascade record:** `state=complete + outcome=success` per `mcp__ta__get drop_004.drop.mvp_substrate`. **This label is wrong.** Real work remains; either re-open drop_004 OR open a new cascade drop for the remaining mvp-feature-complete work.

## 1. Why drop_004 is NOT actually mvp-feature-complete

The prior session closed drop_004 L1 too eagerly. Real gaps remain:

- 1.1 **Playwright + axe-core were never run during build-QA** for any of L3-E2 / L3-E3 / L3-E4. Build-QA agents claimed "a11y baseline verified via semantic landmarks" — that was structural inspection, not behavioral testing. When Playwright + axe were finally run (today), 14 of 20 pages had `color-contrast` violations (serious WCAG AA fail).
- 1.2 **Track B thariq demos look like apps but don't function**. Sidebar items don't navigate (no `href`s wired), search input is decorative (no backend, no client filter), descendant rows aren't `<a>` tags (no drill-down), Playground wrapper constrains them to a fixed-width column on a wide screen (huge dead space). The L3-E4 spec said "demos" but the visual fidelity from stil components implies a working app. Spec gap + implementation gap both.
- 1.3 **L3-I5 (registration_directives) and L3-I6 (full-substrate e2e test) were scope-cut to post-mvp-feature-complete** per orchestrator decision after plan-QA falsif surfaced 8 architectural-gap CEs. They're tracked but not shipped. The L2-I close-gate was amended down to accommodate.
- 1.4 **L3-E4 demos were specced as 4 cascade-record demos (drop-dashboard / planner-detail / droplet-kanban / qa-twins) instead of the 5 page-archetype categories (spec / code-review / report / custom-editor / design-exploration) L2-E originally listed.** Scope drift documented in CLAUDE.md tracker but the page-archetype demos are still unshipped.
- 1.5 **Pre-mvp-feature-complete tracker in CLAUDE.md has 13 open items:**
  - F23 runtime-fill semantics — STALE; F23 v2 is LIVE (verified via 8 plan-QA twin auto-spawns this session). Remove this entry.
  - TUI verification artifacts links from README / examples / docs — L2-H `H5` ran but check whether the links actually landed.
  - Coverage gate `cmd/ta ≥70%` — last measured ~67.1%. Run `mage cover` to confirm current state.
  - `ta` ↔ Claude Code hook management via shipped schemas — claude_hooks/claude_skills/claude_settings_fragments substrates DO ship (verify in `internal/installconfig/defaults.toml`); the gap is auto-registration which is the L3-I5 punt.
  - MCP project arg gate-keeping (`H4`) — likely shipped; verify via `rg guardPath internal/mcpsrv/`.
  - TUI expansion (post-dogfood, locked direction; explicitly out of pre-mvp).
  - Magefile gofmt vs gofumpt (`H3`) — verify via `rg gofmt magefile.go`; should be 0 active occurrences.
  - `internal/format` Get-vs-Dispatch double-stutter — open.
  - `schema.Format` enum + `record.Backend` html/txt gap — open.
  - `md_explicit` Parse-vs-Find ambiguity asymmetry — open.
  - Error-prefix unification across format + backend/html — open.
  - L3-E4 scope drift (this session entry) — page-archetype demos unshipped.
  - L3-I5 registration_directives punt (this session entry).
  - L3-I6 e2e test punt (this session entry).
- 1.6 **Plus the new Playwright + a11y findings from today** (NOT in tracker yet):
  - Track A `schema_browser.html` color-contrast on `.pill[data-value="false"] optional` text. Track-A-fixable in inline CSS.
  - Track B stil + thariq: 25 violation nodes across 13 pages, all on `.component-card-id` (ComponentCard's tertiary id label) + `.mail-row-tag` (MailRow's state-tag chip). Stil-side design token issue. Cross-repo fix (L3-E3 forbade stil edits at consumer level).

## 2. New agent dispatch system (live + battle-tested)

The cascade methodology hasn't changed. Only the spawn mechanism for builder/planner/qa-falsification roles changed.

- 2.1 **For builder / planning / qa-falsification roles**: dispatch via `./bin/agent-dispatch.sh --role <role> --cwd "$(pwd)"`. Walks the chain in `.claude/agent-chains.sh` until one tier succeeds. Backends:
  - builder → ollama-local `qwen2.5-coder:7b` (local, $0 API). Atomic 1-2 block droplets per cascade methodology.
  - planning → codex-exec gpt-5.5 effort=low (cloud); fallback gpt-5.5 effort=medium, then claude-native sonnet, then opus.
  - qa-falsification → codex-exec gpt-5.5 effort=xhigh.
  - Fallbacks per chain.
- 2.2 **For qa-proof + closeout roles**: native `Agent` tool with `subagent_type = <role>`. Reads `.claude/agents/<role>.md`. Anthropic Opus.
- 2.3 **Output format**: ollama-routed + claude-native return Claude Code JSON envelope (parse `.result`). codex-exec returns raw text stream. Read stderr `[disp] served_by=<backend>:<model>` to discriminate.
- 2.4 **Cost-reporting caveat**: `total_cost_usd` in ollama-routed JSON is misleading; actual local GPU billing is $0. Trust `served_by=` over `total_cost_usd`.
- 2.5 **Persona contract**:
  - Builders + QA roles are READ-ONLY on cascade records (`mcp__ta__get` / `list_sections` / `search` only; no `mcp__ta__update`). Orchestrator transitions droplet state after parsing response.
  - Planners + closeout CAN write records (`mcp__ta__create` + `mcp__ta__update`).
  - All personas have mage-only Bash allowlists. No raw `go *` / `pnpm *` / `cargo *` / `gofmt` for ANY agent. Orchestrator may run raw tooling.
- 2.6 **Persona files are ta records under `claude_agents.agent` schema**. Edit via `mcp__ta__update` on the record id + `ta template save --kind=agent --path=./.claude/agents/<role>.md --group=ta --overwrite`. NEVER Edit/Write the `.md` files directly.
- 2.7 **Dispatcher fixes shipped this session** (commit `feat(dispatch): ...`):
  - `--bare` for claude-native dispatch — 13× cost reduction (49K → 3.7K input tokens per call).
  - `--sandbox workspace-write` (not `read-only`) for QA + closeout codex tiers so `mage check` / `mage testPkg` can write to build cache + node_modules.
  - `.gitignore` exception for `bin/agent-dispatch.sh` (was untracked; fix: `/bin/*` + `!bin/agent-dispatch.sh`).
  - Orchestrator dispatch pattern documented in CLAUDE.md (concrete `Bash()` invocation + response parsing contract).

## 3. PATH A — surgical thariq fixes (do this FIRST in new session)

**Goal:** make the thariq demos stop visually lying about being interactive. All zero-JS still. Surgical edits.

- 3.1 **Drop Playground wrapper** — replace stil's `Playground` import in all 5 thariq `.astro` files (`index.astro` + `drop-dashboard.astro` + `planner-detail.astro` + `droplet-kanban.astro` + `qa-twins.astro`) with a plain full-bleed layout (own `<html>` + `<body>` + Sidebar+Topbar mounted at root, content area `min-height: 100vh; width: 100%`). Playground is a demo-viewport wrapper; wrong for production-style pages.
- 3.2 **Rename "Thariq" → "ta cascade" everywhere**:
  - Page titles, sidebar header, all `<h1>` strings.
  - `/thariq/` URL prefix stays (don't break the route paths for now; just rebrand the visible strings).
  - Consider renaming `/thariq/` → `/cascade/` in a separate slice; that's a bigger rename touching imports + tests.
- 3.3 **Wire sidebar `href`s**: stil's Sidebar component supports `href` per item. Pass `{ label, href }` shape:
  ```ts
  const sidebarItems = [
    { label: 'Drop dashboard', href: '/thariq/drop-dashboard' },
    { label: 'Planner detail',   href: '/thariq/planner-detail' },
    { label: 'Droplet kanban',   href: '/thariq/droplet-kanban' },
    { label: 'QA twins',          href: '/thariq/qa-twins' },
  ];
  ```
- 3.4 **Remove or label the decorative Topbar search input** — either strip it entirely OR convert to a `<form action="/search?q=...">` GET that points to a static "search results" placeholder page generated at build time from the mock data. No JS.
- 3.5 **Wrap descendant rows in `<a href>`** — currently `MailRow` renders rows as buttons without nav. Wrap each row in `<a href="/thariq/droplet/<id>">`. The detail pages don't exist yet — generate placeholder static pages at build time via Astro's `getStaticPaths()` over `MOCK_DROPLETS` array. Each placeholder shows the droplet's mock fields. Zero-JS.
- 3.6 **Track A `schema_browser.html` contrast fix** — bump the optional-pill contrast in the template's inline CSS (`internal/templates_html_basic/templates/schema_browser.html`). `.pill[data-value="false"]` currently `#6b7280` on `#e5e7eb` (fails AA). Change to either darker text (#374151) or lighter bg + darker text combo that hits 4.5:1.
- 3.7 **Stil contrast violations** (the ~25 nodes across 13 Track B pages) — cross-repo concern. Per L3-E3 AMEND, consumer-side fix via StilLayout CSS override is acceptable. Override the specific tokens (`--text-tertiary` on `.component-card-id`, `.mail-row-tag-fg` on tag chips) in `web/templates_embed/src/layouts/StilLayout.astro` global `<style>` block.
- 3.8 **Acceptance gates for Path A**:
  - `mage TemplatesBuildEmbed` + `mage TemplatesBuildVerifyStable` green.
  - Playwright + axe-core scan: thariq pages now use full viewport (verify via screenshot at 1280×800 + 375×800).
  - Sidebar `href`s navigate via browser_click on each item.
  - Descendant row `<a>` wraps navigate to a real (placeholder) detail page.
  - Track A `schema_browser` axe scan: 0 violations.
  - Track B contrast: down from 14 violating pages to ideally 0 (after stil-token override).
- 3.9 **Cascade-record this**:
  - Create new `cascade.drop` (e.g. `drop_005.drop.path_a_thariq_visual_fixes` OR re-open as a planner under drop_004).
  - Plan-QA twins auto-spawn via F23 v2.
  - Decompose into 5-7 atomic builder droplets (one per .astro file edit) per cascade methodology. Each droplet touches ≤2 small blocks INCLUDING tests.
  - Dispatch builders via `./bin/agent-dispatch.sh --role ta-fe-builder --cwd "$(pwd)"`.
- 3.10 **Visual verification** is part of the close-gate this time. Add a Playwright-driven a11y check via a new mage target (`mage TemplatesA11y` — see §5). Build-QA's verdict depends on a11y scan passing, not just structural inspection.

## 4. PATH B — real `ta serve` cascade browser (after Path A)

**Goal:** stop building static demos with mock data. Ship a working cascade browser served by ta itself, backed by the project's real `.ta/` records.

- 4.1 **Substrate**:
  - New CLI subcommand `ta serve [--port N] [--bind ADDR]` (default `127.0.0.1:4321`).
  - HTTP handler reads `.ta/` substrate live (cascade.* + roadmap.* + any other db).
  - Internally embeds the existing `internal/templates_html_basic` (Track A) — RUNTIME render, not static dist.
  - Also serves the Track B Astro build (`internal/templates_html_embed/dist/`) on `/embed/*` for the visual showcase.
- 4.2 **Routing**:
  - `/` → cascade tree overview (list all drops + drill-down).
  - `/cascade/<record-id>` → renders that record via the appropriate template (cascade.drop → cascade_drop.html, cascade.planner → cascade_planner.html, etc.). Detail page per record.
  - `/cascade/<drop>/<planner>/...` → tree-style URLs.
  - `/roadmap` → roadmap.* records.
  - `/schema` → schema browser meta-view.
  - `/search?q=...` → text search across records (lunr.js island OR backend-side full-text via `mcp__ta__search`).
- 4.3 **Search**:
  - **Option A (no JS):** `ta serve` re-renders the search results page on every request. Backend-side full-text. Pure-server, zero-JS.
  - **Option B (small SolidJS island):** ship a lunr.js search index at build time + a single SolidJS island for live filter as user types. Adds JS island but bounded scope.
  - Pick A for first version (zero-JS rule).
- 4.4 **Implementation plan (rough)**:
  - L3-Sa-D1: `cmd/ta/serve_cmd.go` cobra command + `internal/server/` package with HTTP routes.
  - L3-Sa-D2: route handlers — `/`, `/cascade/<id>`, `/roadmap`, `/schema`, `/search`.
  - L3-Sa-D3: read live `.ta/` records via existing `internal/ops` package.
  - L3-Sa-D4: render via `internal/templates_html_basic` (Track A). Move from static-golden testing to runtime-render testing.
  - L3-Sa-D5: integration tests via `httptest` + Playwright.
  - L3-Sa-D6: documentation + `mage Serve` wrapper target.
- 4.5 **Acceptance gates for Path B**:
  - `ta serve` starts, binds to specified port, serves real `.ta/` records.
  - Navigate to a cascade drop URL in browser, see real record (not mock).
  - Search returns relevant records.
  - Detail drill-down works via `<a href>` (no JS for navigation).
  - Build-QA via Playwright: dispatch a `ta-fe-qa-falsification` agent that opens the browser via Playwright MCP + drives clicks/searches + asserts behavior.
  - axe-core a11y pass on every served page.
  - All `mage check` gates green.
- 4.6 **Cascade-record this** as a NEW cascade drop (post-Path-A): `drop_NNN.drop.path_b_ta_serve_cascade_browser`. Multi-droplet, multi-L3.

## 5. Other post-Path-A / pre-mvp-feature-complete carryovers

- 5.1 **Tracker reconciliation slice** — verify F23-OFF entry is removed, gofmt entry resolved, MCP project-arg entry resolved, claude-hooks-mgmt entry resolved (or each gets a proper continuation entry). Remove stale items; add the today's a11y findings as proper tracker entries.
- 5.2 **`mage TemplatesA11y` target** — Playwright-driven a11y check that runs axe-core across all dist/* HTML + Track A goldens, fails on serious+critical violations. Wire into `mage check`. This is the missing integration gate; without it, the next iteration will repeat today's "claimed verified but never tested" failure mode.
- 5.3 **Error-prefix unification + schema.Format html/txt gap + md_explicit ambiguity asymmetry** — 3 related code-cleanup items from L3-D plan-QA falsifications. Single slice; non-blocking until tag but should land before any tag.
- 5.4 **Coverage gate cmd/ta ≥70%** — run `mage cover`, ship a small slice if current % is below target.
- 5.5 **TUI VHS artifact links from README + docs (H5 finish)** — verify whether L2-H H5 actually landed; if not, ship.
- 5.6 **L3-I5 registration_directives revisit** — pivot to read `installed_plugins.json` as source of truth + extend configmerge dedupe to composite (matcher+command) keys. Or retire the alert-on-missing-plugin feature.
- 5.7 **L3-I6 full-substrate e2e test** — CI fixture against fresh clone, not local mage testFunc.
- 5.8 **L3-E4 page-archetype demos** — spec / code-review / report / custom-editor / design-exploration. Original L2-E spec; deferred for the cascade-record gallery instead.

## 6. Key files / paths for the new session

- 6.1 **Dispatcher + chains + personas**:
  - `bin/agent-dispatch.sh` — dispatcher (recently fixed: `--bare`, sandbox modes, .gitignore exception).
  - `.claude/agent-chains.sh` — per-role chains.
  - `.claude/agents/ta-*.md` — 9 personas, mage-scoped Bash allowlists, read-only-on-cascade-records by design.
- 6.2 **Project guidance**:
  - `CLAUDE.md` — Cascade-managed dev, Agent Routing — Backend Dispatch, Persona Bash Scoping, Hylla discipline, ta CLI usage, Cascade isolation, pre-mvp-feature-complete tracker.
  - `docs/cascade-methodology.md` — methodology contract.
  - `docs/agent-backend-routing.md` — dispatch explainer.
  - `docs/pre-mvp-tracker.md` — open items tracker.
- 6.3 **HTML template substrate**:
  - Track A: `internal/templates_html_basic/templates/*.html` + `testdata/*.html.golden`.
  - Track B (Astro): `web/templates_embed/src/{layouts,components,mock,pages}/`. Dist at `internal/templates_html_embed/dist/`.
  - Astro dev server live at `http://127.0.0.1:4321/` (Track A copied to `public/track-a/*.html` for serving from same origin).
- 6.4 **Cascade audit**: `.ta/cascade/drops/drop_004/drop.toml` (large; query via `mcp__ta__get` / `mcp__ta__search` / `mcp__ta__list_sections`).

## 7. Hard rules — never violate

- 7.1 **No semver phrasing.** Use "pre-mvp-feature-complete" or "next slice" or "post-mvp-feature-complete carryover". The dev has flagged this twice.
- 7.2 **Section 0 at the start of every substantive response.** 5-field certificate (Premises / Evidence / Trace or cases / Conclusion / Unknowns). Plus orchestrator-facing only — NEVER write Section 0 prose into Tillsyn description / metadata / completion_notes / closing comments.
- 7.3 **Tillsyn-first coordination.** Cascade record CRUD via `mcp__ta__*` MCP tools, not CLI. CLI is for inspection (`ta search --json` is OK; `ta create` is NOT for cascade records — use `mcp__ta__create`).
- 7.4 **Persona files are ta records.** Edit via `mcp__ta__update` + `ta template save`, NEVER direct Edit/Write.
- 7.5 **Mage-only Bash allowlists in personas.** No raw `go *` / `pnpm *` / `cargo *` / `gofmt` for ANY agent. Capability missing → add mage target.
- 7.6 **Atomicity rule.** Builder droplets touch ≤2 small code blocks INCLUDING tests. If larger, planner under-decomposed. Fix the planner, not the model.
- 7.7 **Plan-QA blocks descent.** Every cascade.planner gets plan-QA twins (auto-spawn via F23 v2). Both halves must pass before builder dispatch.
- 7.8 **Build-QA at every planner-family close.** Per-droplet build-QA via cascade.droplet.auto_spawn (if declared in live schema) + per-planner-family at close.
- 7.9 **Visual + a11y verification via Playwright + axe-core** for FE work. Structural inspection (semantic landmarks, aria-label presence) is NOT enough. Build-QA must include Playwright-driven scan with axe violations as failure gate.
- 7.10 **HTML must work** — sidebars navigate, search either works or isn't shown, detail rows drill down. No more visual-only demos that lie about being apps.

## 8. Continuation prompt for the new session

Paste this into a fresh Claude Code session in `/Users/evanschultz/Documents/Code/hylla/ta/main/`:

```
Read /Users/evanschultz/Documents/Code/hylla/ta/main/docs/HANDOFF-pre-mvp-feature-complete.md fully before doing anything else.

The prior session marked drop_004 mvp-feature-complete prematurely. We are NOT there yet. The thariq demos visually lie about being interactive, 13 of 20 HTML pages have axe-core color-contrast violations, Playwright was never used during build-QA, and L3-I5 / L3-I6 were scope-cut instead of shipped. Do NOT use semver phrasing ("v0.1.0", "v0.2.0", etc.) — say "pre-mvp-feature-complete" or "next slice" instead.

Your goal: execute Path A then Path B from the handoff doc.

Path A — surgical thariq fixes (handoff §3): drop the Playground wrapper for full-bleed, rebrand "Thariq" → "ta cascade", wire sidebar hrefs, remove/replace decorative search, wrap descendant rows in <a href> with placeholder detail pages, fix Track A schema_browser pill contrast, override stil component contrast tokens in StilLayout. All zero-JS. Cascade-record it as a new drop OR re-open under drop_004 (orchestrator call).

Path B — real ta serve cascade browser (handoff §4): add `ta serve` subcommand that reads live .ta/ records via internal/ops, serves HTML via internal/templates_html_basic templates at runtime (not static dist), with routes for /, /cascade/<id>, /roadmap, /schema, /search. Pure-server search (no JS). Multi-droplet slice. Cascade-record it as a new drop.

Use the new agent dispatch system (handoff §2). bin/agent-dispatch.sh for builder / planner / qa-falsification (Ollama / codex / Anthropic fallback chain). Native Agent tool for qa-proof + closeout. Persona files are ta records — edit via mcp__ta__update + ta template save, never direct Edit/Write.

Hard rules (handoff §7):
- Section 0 — SEMI-FORMAL REASONING block at start of every substantive response.
- Tillsyn-first for cascade record CRUD (mcp__ta__* tools, not CLI).
- Mage-only Bash allowlists in personas. No raw go/pnpm/cargo for agents.
- Atomicity rule: builder droplets ≤2 small code blocks including tests.
- Plan-QA twins block descent.
- Visual + a11y via Playwright + axe-core (NOT structural-inspection-only) for FE work.
- HTML must actually work. No visual-only demos.

Begin by reading the handoff doc, then ask me anything ambiguous before starting Path A.
```

## 9. Live state at handoff (verify before starting)

- 9.1 **Astro dev server** still running on `127.0.0.1:4321` from prior session. Check with `lsof -ti:4321`. Kill + restart cleanly if stale: `pkill -f "astro dev"`; `cd web/templates_embed && pnpm exec astro dev --host 127.0.0.1 --port 4321 &`.
- 9.2 **mage check** green at handoff time. Re-verify: `mage check 2>&1 | tail -10`.
- 9.3 **Git tree clean.** Verify: `git status --short`. If anything modified beyond `.ta/index.toml`, investigate before starting Path A.
- 9.4 **Open Playwright screenshots from this session** still on disk at project root: `track-a-cascade-drop-1280.png`, `track-b-stil-cascade-drop-1280.png`, `track-b-thariq-drop-dashboard-1280.png`, `stil-gallery-1280.png`. Delete after handoff is read (they're not committed; gitignored as PNG).

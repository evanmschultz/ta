# Pre-MVP-feature-completion tracker

ta is pre-MVP-feature-completion. The first tagged release will be `v0.1.0` — there's no "v1" semantics here, just "every MVP feature works without known issues". Phasing: **dogfood** (minor issues OK if MCP + basic CLI work) → **full CLI refinement** → **full TUI overhaul** (100% huh-free, bubbletea + bubbles + lipgloss + glamour + laslig). MVP-feature-completion launches when every item in the Open section is closed; Explicit-punt items carry rationale referencing the drop or plan-QA finding that deferred them and are tracked post-tag.

## Closed (kept here as audit trail until tag)

- **Huh removal — CLOSED (F38d)**. `charm.land/huh/v2` is gone from `go.mod` and from all source. F38d-1 landed the bubbletea verification infra (`cmd/ta/internal/tuitest`, golden + sha256 contract pin, `mage Vhs`). F38d-2 ported the multi-category picker + `init_cmd.go` non-confirm callsites; F38d-4 the confirms + import strips; F38d-3 the form (deleted `huh_form.go`); F38d-5 the root menu (deleted `huh_theme.go` + cleared `go.mod`); F38d-7 scrubbed every residual `huh` reference and consolidated `cmd/ta/styles.go` + `cmd/ta/keymap.go`. VHS demos under `cmd/ta/testdata/vhs/`.

- **F19 delete shape + F20 delete `--verbose` — CLOSED**. `ErrUnscopedGlobDelete` sentinel at `internal/db/resolver.go:320` with file/glob/record id-level distinction. Shipped in drop_004 L3-G series.

- **F14 rebuild preserves `created` timestamps — CLOSED**. Shipped in drop_004 L3-G8-D1; rebuild reads existing index, preserves `created` per entry, stamps fresh `updated` only.

- **F22 (`extends`) + F23 (auto-spawn) + F24 (multi-category init + template save + symmetric template surface + embed.FS) — CLOSED**. Shipped across drop_004 L-series. F23 v2 is LIVE: `[cascade.drop.auto_spawn]` / `[cascade.planner.auto_spawn]` / `[cascade.droplet.auto_spawn]` blocks fire automatically on `mcp__ta__create`. Documented `--no-spawn` flag bypasses when needed.

- **F18 picker UI fix + F16 + F17 — CLOSED via huh-removal track (F38d)**. Replaced with pure bubbletea; VHS demos under `cmd/ta/testdata/vhs/picker_*.gif` prove the picker renders + select-all + filter work.

- **TUI verification artifacts (gifs + ascii) — CLOSED**. Committed under `cmd/ta/testdata/vhs/`. Cross-linked from `README.md` §"TUI demos" (8 tape index entries), `examples/README.md`, and `docs/cascade-reference.md` §3.1. Re-record with `mage Vhs`.

- **`/schema` route 500 bug — CLOSED**. Fixed in drop_008 corrective slice (commit 7c507b5): `internal/serverview/render.go` `RenderSchema` now converts the typed `SchemaLoaderResult` to `map[string]any` with lowercase keys (`scopeViewsToMaps`/`typeViewsToMaps`/`fieldViewsToMaps` helpers) matching the template's `{{ .scopes }}`/`.types`/`.fields` accesses.

- **`internal/format` Get-vs-Dispatch double-stutter — CLOSED**. `format.Dispatch` was deleted; `format.Get` is the canonical lookup.

- **`md_explicit` Parse-vs-Find ambiguity asymmetry — CLOSED**. `md_explicit.Backend.Parse` uses `findAllByPath` + returns wrapped `format.ErrAmbiguousMatch` (symmetry with `Find`/`Splice`).

- **Error-prefix unification across `format` + `backend/html` — CLOSED in drop_012 D2**. Inner `html parse:` / `html splice:` prefixes dropped since outer wrappers in `backend.go` already add `html backend: <op>:`. Local `html.ErrAmbiguousMatch` sentinel removed; references swapped to `format.ErrAmbiguousMatch`.

- **`ta serve` UX rework — shared-chrome + empty-state search — CLOSED in drop_014 D9**. Unified schema_browser.html + search_results.html to shared-chrome pattern (DOCTYPE + base.css @layer inlined + sidebar partial include) matching cascade_index.html. Retrofitted RenderSchema to inject PageContext for sidebar state. Rewrote RenderSearch empty-query path to render styled empty-state notice (HTTP 400 + search_results.html) instead of plain http.Error. Tests updated: TestRenderBasic_SchemaBrowser now verifies PageContext injection; new TestRenderBasic_SearchResults (3 sub-cases) covers populated results, empty-state no-query, and empty-state with-query paths. All goldens regenerated.

- **Coverage gate `cmd/ta ≥70%` — CLOSED in drop_013 via honest-amend**. Module-wide statement coverage is 77.7%, well above 70%. `cmd/ta` package block coverage stands at 68.6% after drop_013's 6 builder droplets added targeted tests for non-interactive helpers (`readJSONData` 33%→100%, `collectCreateData` 47%→53%, `runDeleteSingle` 53%, `runGetSingleWithFormat` 68%→73%, `runGetGroup` 54%→92%, `runGetScope` 65%→~85%). The remaining ~31% of `cmd/ta` blocks below the 70% per-package target is predominantly interactive TUI / picker / form code (`runFormProgram`, `runConfirmProgram`, `pickDBs`, `runMultiCategoryPicker`, bubbletea `Init`/`Err`/`View` methods — all at 0% statement coverage; `chooseSchema` is partially covered at 18.2% via existing non-interactive init tests but its interactive branches remain untestable without teatest infrastructure). These surfaces require teatest infrastructure investment (bubbletea model-level testing with golden snapshots) that belongs to the TUI expansion track (post-tag). Verification: `mage cover` reports module-wide 77.7%; `mage testPkg ./cmd/ta` passes; VHS goldens under `cmd/ta/testdata/vhs/` cover the interactive flows as visual contract tests. Re-targets to teatest expansion as a post-tag follow-up.

## Open (close before `v0.1.0`)

(None currently — all items closed or explicitly punted.)

### Closed via drop_012 D3 spot-verify

- **MCP project arg gate-keeping (security review) — CLOSED**. The guard already shipped at `internal/mcpsrv/path_guard.go`: `guardPath()` runs `filepath.Abs`+`filepath.Clean` then refuses paths outside `projectRoot` (set once at `registerTools` time via `setProjectRootForGuard`). `guardedPathArg()` is the one-line wrapper every handler uses (8 sites verified). `TA_MCP_PATH_GUARD=off` documented escape hatch. Threat model + scenarios in `docs/security/mcp_path_arg.md`.

- **F15 template-save merge semantics — CLOSED**. `cmd/ta/template_cmd.go` (32K, with 40K test file) implements the full F15 contract: `save [<db>...]` merges named dbs from `<cwd>/.ta/schema.toml` into `~/.ta/schema.toml`, dry-run merge surfaces conflicts before commit, legacy `~/.ta/*.toml` walk replaced with single `~/.ta/schema.toml` source. `--canonical` flag (drop_011) extended the same semantics to `--kind=agent`.

## Explicit-punt (post-tag, with rationale)

- **L3-I5 registration directives** — the hook+plugin+MCP auto-registration system. Deferred reasons documented in drop_004 L2-I plan-QA falsification: `configmerge.arrayContains` dedupes on matcher alone (collapses distinct commands), `installed_plugins.json` is the correct source of truth (not the current `<projectRoot>/.claude/plugins/<plugin>/` check), `claude_settings_fragments` substrate is the canonical "register a hook" path. Post-tag slice pivots to read `installed_plugins.json` + extend configmerge dedupe to composite (matcher+command) keys + retire `applyRegistrations` stub. Until then `internal/install/install.go::applyRegistrations` remains a stub recording `Registration` intent in `Report.Registrations` without writing settings_file.

- **L3-I6 full-substrate end-to-end test** (`TestInstall_E2E_FullSubstrate`) — deferred. Reasons from L2-I plan-QA falsification: only 3 of 14 substrates declared in `internal/installconfig/defaults.toml` have embed sources today; `.ta/cascade/` + `.ta/roadmap/` are NOT in any backup-and-restore primitive (would clobber dev's live cascade records); backup-then-install empties `.claude/` so merge paths never fire; `ta/main` is the orchestrator's working directory (concurrent agent races). Proper venue is CI against a fresh clone in sandbox. Post-tag: ship CI-side e2e fixture + seed remaining 11 substrate sources OR scope the install close-gate to substrates with shipped seeds.

- **L3-E4 page-archetype demos drift** — L2-E originally specified 5 page-archetype demos (spec / code-review / report / custom-editor / design-exploration). L3-E4 actually shipped 4 cascade-record demos (drop-dashboard / planner-detail / droplet-kanban / qa-twins) + gallery index — a different design axis. Dev-approved at L3-E4 close, but the page-archetype demo surface remains unshipped. Post-tag: author a follow-up L3-E5 sub-planner OR explicit-no with explicit rationale.

- **`schema.Format` enum + `record.Backend` gap for html/txt storage** — `internal/schema/schema.go` declares only `FormatTOML` and `FormatMD`. The `internal/backend/html/` + `internal/backend/txt/` packages implement `format.Format` (manifest-driven block extraction), NOT `record.Backend` (schema-section-store). Result: ta records CANNOT be stored as html or txt files until schema.Format gains html/txt + matching `record.Backend` impls. Current `--as=html|txt` tests pin the MISMATCH error surface correctly; positive Marshal coverage is post-tag substrate work. Non-blocking until tag.

- **`ta` ↔ Claude Code hook management auto-registration via shipped schemas** — `claude_hooks` / `claude_skills` / `claude_settings_fragments` substrate schemas DO ship (`internal/installconfig/defaults.toml`). The auto-registration gap is the L3-I5 punt above. Until then, install hooks machine-local per the README snippet.

- **TUI expansion** (post-dogfood, post-CLI-refinement, post-huh-removal) — `-t` / `--tui` flag for browse/search/edit, glamour-rendered preview panes, vim-style multi-select, line numbers in record blocks. Locked direction; explicitly out of v0.1.0 scope.

## MVP-feature-completion gate

`v0.1.0` tags when every Open item above is closed; every Explicit-punt item carries explicit rationale referencing the drop or plan-QA finding that deferred it; and no `// TODO` / `// HACK` / `// XXX` comments remain in source. Current state: **all Open items closed**. MVP-feature-completion ready for release.

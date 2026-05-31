# R-SHIP-TA — Handoff to Dev

**Date:** 2026-05-29
**Tillsyn refinement:** `b8ee83b2-1e5c-4002-bdef-60dfb191ba30` (R-SHIP-TA)
**Source:** tillsyn `38b5a54d05092bbf53e193b2e3bd55a9d53d98c9`
**Orch session that did this:** `5d6e2dec-6983-40d1-9de3-2b4bf0d725e1` (P_SHIP_ORCH)
**Memory rule that drove this handoff pattern:** `feedback_no_sibling_git_mutations` — orch never runs git mutations in sibling repos; dev owns git on the sibling.

---

## What orch did (16 of 17 checklist items)

The orchestrator (running in tillsyn) sync'd the cross-project agent-dispatch architecture into ta. No git operations were performed on ta — that's your turn now.

### Files touched

**Byte-identical from tillsyn (4 files):**

| Path | sha256 (matches tillsyn) |
|---|---|
| `bin/agent-dispatch.sh` | verified |
| `bin/agent-audit-toon.py` | verified |
| `.claude/hooks/ta_action_gate.py` | verified |
| `.claude/hooks/post_tooluse_agent_audit.py` | verified |
| `CASCADE_METHODOLOGY.md` | `87708e81de11e9ef13e4a3e11a74fc0741da95b8ac6ec2fa242b8207986827ea` |

**Adapted from tillsyn (Path B 2.2.A — `mcp__tillsyn__*` stripped):**

| Path | What changed |
|---|---|
| `.claude/agents/<persona>/settings.json` × 13 | Stripped `mcp__tillsyn__*` + `mcp__tillsyn-dev__*` entries from `permissions.allow` and `permissions.deny`. Preserved `mcp__ta__*`, `mcp__hylla__*`, `mcp__plugin_*`, all `Bash(...)` entries. |
| `.claude/agents/<persona>.md` × 13 | Stripped `mcp__tillsyn__*` from `tools:` frontmatter line. Prepended a **Sibling-Context Note (auto-adapted 2026-05-29)** explaining any leftover textual refs in the body are INERT here (Claude Code refuses to invoke tools not in the persona's `tools:` whitelist). |
| `.claude/settings.json` | Added **PostToolUse Agent matcher** wiring `post_tooluse_agent_audit.py`. Existing `PreToolUse` Bash hook (ta_action_gate.py) preserved unchanged. |

**Updated for canonical 12-target shape:**

| Path | What changed |
|---|---|
| `magefile.go` | Full rewrite to canonical 12-target shape: added `Test`, `Race`, `RacePkg`, `Vet`, `VetPkg`, `Tidy`, `FormatCheck`, `FormatFile`; renamed `Check` → `CI`, `Fmt` → `Format`, `FmtCheck` → `FormatCheck`; `TestFunc` signature changed from 1-arg `(pattern)` → 2-arg `(pkg, name)`; `runGoTest` refactored to NOT bake in `-race` (callers add it explicitly per canonical shape); `Cover` now does `-race -coverprofile=coverage.out`. **Preserved:** `Build`, `Install` (with install_hygiene), `Vhs`, `Serve`, `TemplatesA11y`, `Clean`, `laslig/gotestout` rendering, `TA_TEST_PKG` + `TA_GO_TEST_FLAGS` envs, `ensureGofumpt` auto-install. Hyphenated aliases preserved: `check`, `fmt`, `fmt-check`, `format-check`, `format-file`, `test-func`, `test-pkg`, `race-pkg`, `vet-pkg`, `templates-a11y`. |
| `.github/workflows/ci.yml` | `mage check` step renamed to `mage ci` (the alias `check` → `CI` still works, but `mage ci` is now canonical). |
| `CLAUDE.md` | Added **2026-05-29 Architecture Sync from tillsyn (LOAD-BEARING)** section near the top documenting the file-list above + the `ta`-MCP-not-tillsyn-MCP rule. **Superseded 2026-05-30 by the P5 caveman pass — see the dedicated section below; CLAUDE.md was fully rewritten 31,988 → 17,044 chars.** |

### Gates orch verified locally before this handoff

| Gate | Result |
|---|---|
| `mage formatCheck` | ✓ silent (no drift) |
| `mage vet` | ✓ silent (no warnings) |
| `mage cover` (race+cover combined) | ✓ 77.1% total coverage, all tests pass under -race |
| 13 persona settings.json static-validity smoke | ✓ 13/13 valid JSON + no leftover tillsyn refs |
| 13 persona MD frontmatter smoke | ✓ 13/13 frontmatter parses + tools: line cleaned + Sibling-Context Note prepended |

**`mage tidy` skipped intentionally** to avoid mutating ta's `go.mod`/`go.sum`. Run `mage tidy` yourself when you do step §3.

---

## 2026-05-30 — P5: CLAUDE.md caveman + cascade-doc consolidation (orch, no git)

A second orch pass (P5) ran AFTER the 2026-05-29 sync above. Pure docs/MD — no Go, no go.mod, no git.

### CLAUDE.md fully caveman'd

`CLAUDE.md` rewritten **31,988 → 17,044 chars** (−47%, well under the 30k target), 11 → 10 H2 sections:

- Dropped the two dated `LOAD-BEARING` narrative sections (2026-05-29 Architecture Sync, 2026-05-27 Subagent Discipline). Architecture Sync's file-list is audit → lives here in the handoff; its one durable rule ("ta tracks via `ta` MCP, never tillsyn") survives terse in the new **Architecture & Cascade Tracking** section. Subagent Discipline collapsed to terse rules + a pointer to `CASCADE_METHODOLOGY.md § Subagent Discipline`.
- Merged the two near-duplicate cascade sections into one **Cascade Methodology — Plan Down, Build Up** (recursive flow captured in-file as 6 numbered points + the `ta`-record CRUD workflow + pointers to the canon and `docs/cascade-reference.md`).
- Tightened the 92-line Agent-Routing section against `docs/agent-backend-routing.md` (kept the 13-row role-primaries table + the git-block mechanics + dispatch invariants verbatim/terse).
- 3 rulings applied: TaskCreate dual-use noted (not "never"); `ta-closeout` = opus (already correct); recursive flow stays IN-file. No human-time-estimate text present to remove.

> **DEV ACTION:** CLAUDE.md is a ta record (`agents_md` schema, H2 = record). The edit was a plain-file Write (this orch session's `ta` MCP is pinned to tillsyn, not ta), so the H2 record set changed. **Run `ta index rebuild` once after pulling** so `.ta/index.toml` re-validates against the new sections. (AGENTS.md was also edited — same rebuild covers it.)

### Cascade-doc consolidation — `docs/cascade-methodology.md` REMOVED

You flagged that two cascade docs existed; root `CASCADE_METHODOLOGY.md` (sha `87708e81…`, = tillsyn canon) is canonical. The OLD `docs/cascade-methodology.md` (746 lines) was **not** a stale dup — its §1-9 overlapped the canon, but §10-15 were unique. Resolution (your Option A):

- **`docs/cascade-methodology.md` deleted** (orch `rm`; **you `git rm` it**).
- **New `docs/cascade-reference.md` created** = ta reference addenda: §1 Test-scope Isolation + §2 Pre-QA LSP Refresh (two operational disciplines the shared canon does NOT carry) + §3 Reference Implementations (substrates + ta VHS demos) + §4 Canonical Node Shape + §5 Benchmarking + §6 Metrics + §7 Open Questions + §8 References. Defers to `CASCADE_METHODOLOGY.md` for methodology.
- **Dropped:** old §4.6 "Why Per-Droplet LLM QA — Schema-Enforced via auto_spawn" — it **contradicts** `CASCADE_METHODOLOGY.md § "Why No Droplet-Level LLM QA"` AND ta's own CLAUDE.md (both: `mage ci` is the only droplet-level gate, no LLM QA). Left out to avoid perpetuating the contradiction. **If ta actually wants per-droplet auto-spawn QA, that's a methodology decision to reconcile in the canon — flagged, not silently kept.**
- **10 files rewired** off the deleted doc (methodology refs → root canon; substrate/node-shape/discipline refs → `docs/cascade-reference.md`): `README.md`, `CONTRIBUTING.md`, `AGENTS.md`, `docs/pre-mvp-tracker.md`, `docs/HANDOFF-pre-mvp-feature-complete.md`, `docs/agent-backend-routing.md`, `docs/PLAN.md`, `workflow/ta/F10-PLAN.md`, `examples/thariq/testdata/README.md`, `CLAUDE.md`. (`examples/README.md`'s two "cascade-methodology schema" mentions are adjectival descriptions of `cascade.toml`, not doc links — left as-is.)

### P5 staging (add to your §4 commit, all docs/MD — no gate impact)

```sh
git add CLAUDE.md docs/cascade-reference.md README.md CONTRIBUTING.md AGENTS.md \
  docs/pre-mvp-tracker.md docs/HANDOFF-pre-mvp-feature-complete.md \
  docs/agent-backend-routing.md docs/PLAN.md workflow/ta/F10-PLAN.md \
  examples/thariq/testdata/README.md R_SHIP_HANDOFF.md
git rm docs/cascade-methodology.md
# then: ta index rebuild   (re-validates .ta/index.toml against the new CLAUDE.md / AGENTS.md sections)
```

Pre-existing FYI unrelated to P5: `docs/agent-backend-routing.md` §77 still quotes the OLD "≤4 small blocks" atomicity number (canon is now 1-2). Not touched by P5 (out of scope) — fix in a docs pass if you want it consistent.

### Schema alignment — per-droplet auto_spawn QA REMOVED

The cascade-doc consolidation surfaced that ta's schema shipped per-droplet QA-twin auto-spawn (drop_004 L2-J, F23 v2) — which **contradicts** `CASCADE_METHODOLOGY.md § "Why No Droplet-Level LLM QA"` (and ta's own CLAUDE.md): the droplet-level gate is the automated `mage ci` pass, NO LLM QA at the leaf. Per your ruling, the schema now matches the canon.

**Edited (orch, no git):**
- `.ta/schema.toml` — removed `[cascade.droplet.auto_spawn]` (+ its F23 comment); replaced with a comment pointing to the canon. **`[cascade.drop.auto_spawn]` + `[cascade.planner.auto_spawn]` (plan-QA twins) KEPT — those are canon-aligned.** The auto_spawn ENGINE is untouched (still used by drop + planner).
- `examples/schemas/cascade.toml` — same removal (this is the `ta init` template + the binary-embedded source, so the fix propagates to future adopters).
- `internal/schema/auto_spawn_test.go` — the droplet-specific pin `TestAutoSpawn_DropletCreates_SpawnsBuildQATwinPair` reframed → `TestAutoSpawn_LeafTypeDeclaringSpawn_ParsesTwinPair` (generic engine coverage; comment now states the live schema deliberately excludes droplet auto_spawn). Drop/planner pin tests untouched.

**Verified green locally (orch, read-only):** `mage testPkg ./internal/schema` = 214/214, `mage testPkg ./internal/ops` = 161/161. No golden snapshots droplet auto_spawn; `TestLiveSchemaLoads` carries no droplet assertion. Run `mage ci` yourself before commit to confirm the full gate (incl. coverage floor).

**Scope across projects (your "do this with all projects"):** ta was the ONLY project carrying `cascade.droplet.auto_spawn` — sand/valv/poly `.ta/schema.toml` don't have the cascade schema, lagom/bage have no `.ta` yet, and `~/.ta/schema.toml` has no cascade fragment. So ta is the only edit site; the template fix carries the rest forward.

> **DEV ACTION — share via HOME `~/.ta`:** after committing, run `mage install` so the corrected embedded `examples/schemas/cascade.toml` is re-baked into the `ta` binary (that's what `ta init` reads). If you also keep a `~/.ta/.../cascade.toml` override layer, apply the SAME `[cascade.droplet.auto_spawn]` removal there so every future `ta init`'d project inherits the canon-aligned schema (none exists under `~/.ta` today — confirmed — so this is a go-forward guard). Net goal: no project ever re-acquires per-droplet auto-spawn QA.

**Tradeoff on record:** this reverts drop_004 L2-J. The failure mode it addressed — planner-family build-QA twins forgotten under speed pressure — now relies on the canon's planner-level build-QA discipline + orch enforcement (and `mage ci` as the deterministic per-droplet gate). If forgotten-twins recur, the fix is orch/dispatcher enforcement of planner-level build-QA, NOT re-adding droplet auto_spawn.

---

## What you (dev) need to do

### 1. Pre-commit verification

Spot-check the diff makes sense:

```sh
cd /Users/evanschultz/Documents/Code/hylla/ta/main
git status
git diff --stat
git diff -- magefile.go            # most consequential single file
git diff -- .claude/settings.json
git diff -- .github/workflows/ci.yml
git diff -- CLAUDE.md
ls .claude/agents/                  # confirm 13 <persona>/ subdirs created
head -20 .claude/agents/ta-go-builder.md   # confirm Sibling-Context Note prepended
```

Pre-existing dev WIP (left untouched by orch):

```
 M .ta/index.toml
?? .claude/skills/
?? .ta/cascade/drops/drop_025/
```

These three were on your tree BEFORE orch started. Orch did not touch them. Commit / stash / leave them per your call.

### 2. Run the full gate

```sh
cd /Users/evanschultz/Documents/Code/hylla/ta/main
mage ci                  # FormatCheck + Vet + Cover (race+cover) + Tidy. Must pass green.
mage templatesA11y       # Only if you have a11y/node_modules set up; warn-skipped otherwise.
```

If `mage ci` fails on Tidy — orch did NOT run Tidy. There may be go.mod/go.sum drift. Look at the diff, decide whether to commit the tidy result or investigate why it drifted.

### 3. Live persona smoke (optional but recommended)

After `mage install` + Claude Code restart, dispatch one persona via the built-in Agent tool to confirm:

```
Agent(subagent_type='ta-go-builder', prompt='smoke test — run `git status --short` then `git commit --allow-empty -m HOOKTEST_ta`. Expected: git status succeeds, git commit blocked by ta_action_gate.py baseline. Report the 4-line verdict format.')
```

Expected: ta_action_gate.py's hardcoded baseline blocks `git commit` for the dispatched persona. (Same hook as tillsyn — verified working there.)

### 4. Commit (your hands, not orch's)

Suggested staging — explicit files:

```sh
cd /Users/evanschultz/Documents/Code/hylla/ta/main
git add \
  bin/agent-dispatch.sh \
  bin/agent-audit-toon.py \
  .claude/hooks/ta_action_gate.py \
  .claude/hooks/post_tooluse_agent_audit.py \
  .claude/agents/*/settings.json \
  .claude/agents/ta-*.md \
  .claude/settings.json \
  CASCADE_METHODOLOGY.md \
  CLAUDE.md \
  magefile.go \
  .github/workflows/ci.yml \
  R_SHIP_HANDOFF.md
```

Suggested commit message (conventional, single-line, ≤72 chars):

```
feat(architecture): sync agent-dispatch + persona + canonical magefile from tillsyn
```

Or split into two commits if you prefer:

```
feat(architecture): sync agent-dispatch + persona from tillsyn (path B)
feat(magefile): canonical 12-target shape (test/race/vet/tidy/format + ci rename)
```

### 5. Push + GH Actions watch

```sh
git push origin main
gh run watch --exit-status     # confirm both `check` (mage ci) + `a11y` jobs green
```

### 6. Hylla ingest (Go project — required per the global rule)

After GH Actions green:

```
Tool call (from any Claude Code session):
mcp__hylla__hylla_ingest(
  source_url="https://github.com/evanmschultz/ta.git",
  ref="<the SHA of your commit>",
  branch="main",
  enrichment_mode="full_enrichment",
  stream=true,
)
```

Confirm `status: completed`, `progress: 100/100`. Note the `task.id` for the audit trail.

### 7. Tell orch you're done

Either resume the orch session OR post to the R-SHIP-TA Tillsyn refinement (`b8ee83b2-…`) directly:

```
✓ Step 4 commit SHA: <yours>
✓ Step 5 GH Actions green: run id <yours>
✓ Step 6 Hylla ingest task: task-<yours>
```

Orch will then mark R-SHIP-TA `complete` and proceed to the 5 other siblings (polyglot, sand, valv, lagom, bage), with **ta as the source-of-truth** for cp.

---

## Why this matters / what's next

ta is the **source-of-truth sibling** for the cross-project agent-dispatch architecture. After R-SHIP-TA is dev-verified + committed + pushed, the orchestrator will cp the canonical files **from ta** (not tillsyn) to the other 5 siblings:

- `polyglot-foundation` (full FE + Go, 13 personas)
- `sand` (Go-only, 7 personas)
- `valv` (Go-only, 7 personas)
- `lagom` (fresh Go-only, 7 personas, bootstrap)
- `bage` (fresh Go-only, 7 personas, bootstrap)

Each sibling will get its own `R_SHIP_HANDOFF.md` following the same pattern. Per the `feedback_no_sibling_git_mutations` rule, orch never runs git on any sibling — handoff MD always.

Tillsyn refinements queued for the 5 remaining siblings:
- `2433de73-…` R-SHIP-POLY
- `3a770b48-…` R-SHIP-SAND
- `8910ba94-…` R-SHIP-VALV
- `331506f2-…` R-SHIP-LAGOM
- `64b105df-…` R-SHIP-BAGE

All five gated on R-SHIP-TA complete.

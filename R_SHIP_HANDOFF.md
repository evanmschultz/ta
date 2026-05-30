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
| `CLAUDE.md` | Added **2026-05-29 Architecture Sync from tillsyn (LOAD-BEARING)** section near the top documenting the file-list above + the `ta`-MCP-not-tillsyn-MCP rule. Existing CLAUDE.md content unchanged. |

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

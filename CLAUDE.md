# CLAUDE.md — project guidance for ta

Project-local guidance for Claude Code (and other assistants that read CLAUDE.md) when working inside the `ta` tree.

## Subagent Spawn Defaults — Background-First

Inherits from the global rule in `~/.claude/CLAUDE.md` § "Subagent Spawn Defaults — Background-First": spawn agents with `run_in_background: true` by default. Foreground mode lets agents bypass their declared `tools:` allowlist; background mode enforces it. For ta-specific build → QA → commit flows, this is critical — agents have repeatedly tried `node`, `python3`, raw `gofmt`, raw `go test`, all of which are NOT in any agent's allowlist but were reachable via foreground inheritance.

Use foreground only when the agent's result is required to decide your immediate next step AND the task is short enough that the safety trade-off doesn't pay back. For ta's build / QA / planning agents this is rare.

## Agent Selection — Use Project-Local `ta-*` Agents, NEVER the Globals

This project ships its own Claude Code subagents under `<project>/.claude/agents/ta-*.md`. Always dispatch with the `ta-` prefixed names:

- `ta-go-builder` (NOT `go-builder-agent`)
- `ta-go-qa-proof` (NOT `go-qa-proof-agent`)
- `ta-go-qa-falsification` (NOT `go-qa-falsification-agent`)
- `ta-go-planning` (NOT `go-planning-agent`)
- `ta-fe-builder` / `ta-fe-qa-proof` / `ta-fe-qa-falsification` / `ta-fe-planning`
- `ta-closeout` (closeout role)

The project's `.claude/agents/` shadows the global `~/.claude/agents/` definitions. Using the global agent name routes to the wrong file — global agents lack project-specific tool allowlists (mage, mcp__ta__*) and project conventions.

**Editing agent definitions**: agent .md files are ta records under the `claude_agents.agent` schema type. NEVER edit them directly with Edit/Write. The dogfood workflow is:

1. **mcp__ta__update** on the agent record id (e.g. `ta-go-builder`) with the desired field overlay (e.g. `{tools: "..."}`). YAML frontmatter fields = record fields.
2. **`ta template save --kind=agent --path=./.claude/agents/<file>.md --group=ta --overwrite`** — pushes the updated agent into `~/.ta/agents/ta/<file>.md` so future `ta init` runs in other projects install the latest version.
3. **Verify both files** match (project + HOME) before commit.

Direct edits of `~/.claude/agents/*-agent.md` or `~/.ta/agents/*/*.md` bypass ta's substrate tracking and create drift. The tool is built for this — use it.

## Hylla Discipline — Go-Only, Primary Evidence Source, Push-Often + Ingest-After-Push

ta is a Go project. Hylla (`mcp__hylla__*`) is the **primary evidence source** for committed Go code — planners, plan-QA, builders, and build-QA all use Hylla BEFORE Read/Grep for any question about committed Go symbols, references, or structural facts. Project-local `ta-go-*` agents now carry `mcp__hylla__hylla_search`, `_search_keyword`, `_node_full`, `_refs_find`, `_graph_nav` in their `tools:` allowlist.

**Evidence-source priority for Go work**:

1. **Hylla** — committed Go symbols, refs, graphs, full-node bodies.
2. **`git diff`** — uncommitted local deltas (Hylla can't see uncommitted work).
3. **Read / Grep / Glob** — non-Go files AND uncommitted Go (between push and ingest).
4. **Context7 + `go doc` + LSP** — external library semantics + live LSP queries.

**Hylla is Go-only.** NEVER query Hylla for `.toml`, `.json`, `.md`, `.yml`, magefile, scripts, or any non-Go file type. For those, go straight to Read/Grep/Glob. No fallback miss to log — it's by design.

**Push-often + ingest-after-push**:

- After every commit batch, push to origin so the Hylla index stays close to disk. Don't accumulate large unpushed work.
- After every push, trigger `mcp__hylla__hylla_ingest` so the next agent dispatch sees the latest committed state.
- The `/commit-and-reingest` skill bundles push + ingest — use it as the canonical exit gate for a slice.
- Between push and ingest there's a brief window where Hylla shows stale data; agents in that window fall back to `git log` / `Read` for the very latest commits.

**Spawn prompts MUST include the Hylla artifact ref** for dispatched ta-go-* agents (e.g. `github.com/evanmschultz/ta@main`). The agent uses that ref for every Hylla call.

## Pre-QA LSP Refresh Discipline

ta is a Go project. The active LSP is gopls. Before spawning any `ta-go-qa-proof` or `ta-go-qa-falsification`, the gopls daemon's workspace index must reflect the build agent's edits — otherwise QA reads stale diagnostics that don't match disk truth (recurring failure mode: agent reports "undefined: X" for symbols that mage check confirms exist).

- **Hook**: `~/.claude/hooks/pre_agent_lsp_refresh.sh` fires on PreToolUse(Agent) and recycles gopls when the spawned agent is a QA variant. Machine-local today; will be relocated into ta's project-local hook tree (`<project>/.claude/hooks/`) once the dogfood phase ships project-local hook management. At that point, every ta dev gets the hook automatically via `ta init`.
- **Manual fallback**: invoke the `/gopls-sync` skill or restart Claude Code from `/Users/evanschultz/Documents/Code/hylla/ta/main` (the active checkout) if the hook doesn't help.
- **Authoritative verification stays `mage check`** — LSP refresh ensures QA's evidence layer matches build truth, but mage check is the gate. Never trust LSP diagnostics over a passing mage check.

## TUI stack — bubbletea/bubbles/lipgloss/glamour/laslig (huh is being removed)

ta's TUI direction:

- **Target stack**: `charm.land/bubbletea/v2` (program/model loop), `charm.land/bubbles/v2` (list/text/spinner primitives), `charm.land/lipgloss/v2` (styling), `charm.land/glamour/v2` (markdown rendering inside TUI panes), `github.com/evanmschultz/laslig` (CLI render — already used). NO huh.
- **Migration plan**: huh stays where it works pre-dogfood (today: `ta init` multi-category picker). Replace huh slice-by-slice as TUI surface grows. Goal: zero huh imports by end of dogfood. New TUI surface MUST go bubbletea-direct from day one — do not add new huh forms.
- **Why**: huh's form abstraction blocks features ta needs (collapsible groups in pickers, custom multi-pane layouts, search-as-you-type filtering, glamour-rendered preview panes). Bubbletea-direct gives full control.

## TUI verification — teatest + goldens + VHS, never self-report

NEVER claim TUI behavior works without a captured artifact. Self-reported "the picker looks right" is not evidence; the dev has been burned by it (twice).

- **Golden snapshots** for structural output (text content, layout, fields-rendered, error messages). Pattern: `internal/render/schema_flow_test.go::assertSchemaFlowGolden`. Materializes `testdata/*.golden` on first run, byte-compares thereafter. Use for laslig output AND bubbletea View() snapshots driven through teatest.
- **`charm.land/x/exp/teatest`** for headless drive of bubbletea models. Captures View() at key transitions (initial, after navigation, after select, after submit, on error). Same `.golden` pattern.
- **VHS** (`charm.land/vhs`) for visual capture (animated `.gif` / `.txt` artifacts of the TUI in motion). Used when structural goldens don't capture the issue (cursor flicker, color drift, animation timing). Run via mage target; produced artifacts committed under `testdata/vhs/`.
- **The orchestrator (me) MUST run these tools and inspect the artifacts.** Not self-narration. If a golden test doesn't exist for a TUI claim, write one. If a VHS recording would catch what a golden can't, run vhs.
- **Before claiming "the TUI looks right"**: golden + vhs artifact must exist and match expected. If golden diff is intentional → re-record + commit. If unintentional → fix.

## Pre-MVP cleanup tracker — MVP-feature-completion launches clean

ta is pre-MVP-feature-completion. The first tagged release will be `v0.1.0` — there's no "v1" semantics here, just "every MVP feature works without known issues". Phasing: **dogfood** (minor issues OK if MCP + basic CLI work) → **full CLI refinement** → **full TUI overhaul** (100% huh-free, bubbletea + bubbles + lipgloss + glamour + laslig). MVP-feature-completion MUST launch with **zero tech debt** — every item below is closed before `v0.1.0` is tagged.

**Open pre-MVP-feature-completion items** (close before `v0.1.0`, may carry through dogfood):

- **Huh removal — CLOSED (F38d)**. `charm.land/huh/v2` is gone from `go.mod` and from all source. F38d-1 landed the bubbletea verification infra (`cmd/ta/internal/tuitest`, golden + sha256 contract pin, `mage Vhs`). F38d-2 ported the multi-category picker + `init_cmd.go` non-confirm callsites; F38d-4 the confirms + import strips; F38d-3 the form (deleted `huh_form.go`); F38d-5 the root menu (deleted `huh_theme.go` + cleared `go.mod`); F38d-7 scrubbed every residual `huh` reference and consolidated `cmd/ta/styles.go` + `cmd/ta/keymap.go`. VHS demos under `cmd/ta/testdata/vhs/`.
- **F23 runtime-fill semantics** — `cascade.droplet` auto_spawn block in `examples/schemas/cascade.toml` is COMMENTED OUT pending F23 supporting `{now}` / `{state.initial}` / `{parent.<field>}` token expansion for required-no-default fields. Without it, dogfood requires manual QA twin creation per droplet. Schedule as architectural slice post-F38.
- **TUI verification artifacts (gifs + ascii)** committed under `cmd/ta/testdata/vhs/` AND linked from `README.md`, `examples/README.md`, `docs/cascade-methodology.md`. Each demo'able flow gets one tape that emits BOTH the test golden AND the README/docs gif. Single source of truth.
- **Coverage gate** — `cmd/ta` package at 67.1% (target ≥70%). Pre-existing dead branches (TTY confirm, form paths). Schedule a coverage-only slice post-F38.
- **`ta` ↔ Claude Code hook management via shipped schemas** — `claude_hooks` / `claude_skills` / `claude_settings_fragments` schemas don't exist yet. Required so `ta init` installs the LSP-refresh hook (and others) into `<project>/.claude/hooks/` automatically per the dogfood plan in `README.md`. Currently machine-local.
- **MCP project arg gate-keeping** — every MCP tool accepts a `path` arg pointing at any project dir on disk. Real reach surface; review for security gates pre-MVP.
- **TUI expansion** (post-dogfood, post-CLI-refinement, post-huh-removal) — `-t` / `--tui` flag for browse/search/edit, glamour-rendered preview panes, vim-style multi-select, line numbers in record blocks. Locked direction; out of pre-MVP scope.
- **Magefile uses `gofmt`, not `gofumpt`** — memory rule says gofumpt routed through mage; magefile contradicts. Update magefile or revisit memory. Out of F38 scope.
- **`internal/format` Get-vs-Dispatch API duplication** (from L3-D1 planner-level build-QA falsif): both `format.Get(name)` and `format.Dispatch(name)` are publicly exported; `Dispatch` is a thin wrapper that adds a `"format dispatch: "` prefix on top of `Get`'s already-`"format: "`-prefixed error → double-stuttering `"format dispatch: format: no implementation registered"`. Resolve to ONE of: (a) unexport `Get`, keep `Dispatch` as the public lookup, OR (b) drop `Dispatch`, rename `Get` → `Lookup`. Update dispatch_test.go's `_SchemaEnumKeyMapping` accordingly. Cleanup slice; non-blocking until tag.
- **Error-prefix unification across `format` + `backend/html`** (from L3-D1 planner-level build-QA falsif): three prefix conventions coexist — `"format: ..."`, `"format dispatch: ..."`, `"html backend: <op>: ..."`, `"html splice: ..."`, `"html parse: ..."`. Same `html` package uses TWO conventions (outer wrappers + inner internals) producing `"html backend: splice: html splice: matched node ..."` stutters. Pick `<pkg>: <op>: ...` uniformly; drop redundant inner prefixes when outer wrapper adds the op-name. Affects ~6 files. Non-blocking until tag.

**MVP-feature-completion gate**: every item above is either closed or explicitly punted to post-`v0.1.0` with a tracking issue. No `// TODO` / `// HACK` / `// XXX` comments left in source.

## Cascade isolation — agents test ONLY their slice

A builder or QA agent operating below strict package level MUST run only the tests their slice owns — not the whole module. Sibling agents racing on the same checkout produce a polluted working tree; running `mage Test` (full module) gives a verdict muddied by other agents' WIP.

- **Below-package scope**: `mage testFunc TestMyThing` (single test) or `mage testFunc 'TestA|TestB|TestC'` (multiple, pipe-joined regex) or with package narrowing via `TA_TEST_PKG=./internal/ops mage testFunc TestMyThing`. Routes through `go test -run <pattern>`.
- **Package-level scope**: `mage testPkg ./internal/ops`. One package end-to-end; verdict reflects exactly what the slice owns.
- **Module-level scope**: `mage Test` (or `mage Check`). Run by orchestrator-level QA + commit gate, not by sub-package agents mid-build.

The agent runner reports its verdict against the scope it owns. Higher-level QA (segment, confluence, drop) escalates to wider scopes. Orchestrator runs the whole. This mirrors the cascade methodology's "QA at the level integration actually happens" rule (`docs/cascade-methodology.md` §4).

Memory rule still applies: NEVER invoke raw `go test` / `go vet` / `go build` / `gofmt` / `gofumpt`. Always route through mage. The `--project` flag and the testFunc / testPkg targets are how an agent narrows scope without bypassing the gate. Test output auto-detects TTY status via `laslig/gotestout` — agents and CI pipes get plain text without env-var gymnastics.

## Cascade methodology — canonical reference

The agent cascade methodology that ta dogfoods (and the future article / blog post seeds from) lives at [`docs/cascade-methodology.md`](docs/cascade-methodology.md). It's the **app-agnostic** version: thesis, droplet shape, role and model bindings, QA placement, nesting model, failure handling, audit trail, reference implementations. The older Tillsyn-flavored draft (`AGENT_CASCADE_DESIGN.md`) was retired — `docs/cascade-methodology.md` is the canonical source for any cascade questions, planning conversations, and the eventual article.

When orchestrating a cascade in this project — point at `docs/cascade-methodology.md` first, then `docs/PLAN.md` for ta-specific plan/drop sequencing.

## Ta CLI usage

- All `ta <read-command>` invocations from agents MUST pass `--json`. ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun.
- Read commands that accept `--json`: `ta get`, `ta list-sections`, `ta schema` (action=get, the default), `ta search`.
- Mutating commands (`ta create`, `ta update`, `ta delete`, `ta schema --action=create|update|delete`) return a concise laslig success notice on both surfaces; their MCP counterparts already return JSON. Use `--verbose` on the CLI when you want the post-mutation record echoed back.
- `mage test` / `mage check` / `mage cover` route through `laslig/gotestout` which auto-detects TTY status — humans get a styled summary, agents and CI pipes get plain text. No env-var prefix needed; `mage test` just works.
- Bare `ta` without a TTY is the MCP server — no explicit subcommand needed when registering in `.mcp.json` / `.codex/config.toml`.

## Cascade-managed development — use ta to manage ta

For any non-trivial work in this repo (multi-droplet slice, anything involving planner/builder/QA roles, anything with QA twins), the orchestrator MUST use ta cascade records to track the work, not in-session task lists or markdown plans.

**Workflow per `docs/cascade-methodology.md` § 3 (Roles) + § 4 (QA Placement)**:

1. **Drop record** — `mcp__ta__create` a `cascade.drop` first. id = `drop_NNN.drop.<slug>` (single-segment bracket-key per F38d-2.15; no dots in the slug). Required: `drop_number`, `structural_type='drop'`, `role`, `state`, `title`, `created_at`, `updated_at`.

2. **Planner record** — `mcp__ta__create` a `cascade.planner` child. Holds the decomposition (in `objective` / `description` / `decision_log` fields). Dispatch a `ta-go-planning` (or `ta-fe-planning`) agent to author it; the agent updates the planner record via `mcp__ta__update`.

3. **Plan-QA twins** — when the planner record is created, the orchestrator immediately creates two QA children targeting the planner's output: `cascade.qa_proof` + `cascade.qa_falsification` (set `target_id = <planner-record-id>`, `state = 'todo'`). These BLOCK descent — no builder droplets are spawned until plan-QA twins both return `state=complete + outcome=success`. Dispatch as parallel background agents (one message, two Agent tool calls).

4. **Recursive decomposition vs direct droplet emission** — `docs/cascade-methodology.md § 5.3` is the contract: if the planner's output would emit more than 4 children OR cross more than 1 distinct domain concern OR cross more than 1 package, the planner MUST decompose into **child planners** (`cascade.planner` records) instead of emitting droplets directly. The cascade recurses through planner levels — each level designs general direction for the next; the next level decomposes further — until each terminal level emits **3-6 atomic droplets per child planner** that touch disjoint paths. Even a 2-bug slice typically reaches depth 3 (L1 drop → L2 planner → L3 sub-planner per concern → L4 droplets); shallower trees are usually under-decomposed. Plan-QA twins fire at EVERY planner node (§4.3), not just L1.

5. **Builder droplets** — terminal leaves only. `mcp__ta__create` one `cascade.droplet` per atomic build slice. Each droplet touches **≤4 distinct code-block edits** (this project's user-facing contract; methodology says "few blocks"). **Builders dispatch in parallel** when their `paths` are disjoint (the planner's responsibility to enforce — `paths`-sharing siblings serialize via the `blockers` graph per §7). For a typical decomposed L4 level, expect 3-6 builders firing concurrently from one parallel `Agent` tool call. **No LLM QA at droplet level** — build+test is the only droplet-level gate.

6. **Package-level build+test (automated)** — after all droplets for a package report `complete`, the orchestrator runs `mage testPkg <path>` for that package. Failures cycle back: enclosing planner ingests failure → writes fix directive → droplet re-runs.

7. **Build-QA twins at EVERY planner level** — once all direct children of a planner are `complete` AND their package gates green, `mcp__ta__create` two QA children targeting the completed sub-tree: `cascade.qa_proof` + `cascade.qa_falsification`. Dispatch as parallel background. Both must `state=complete + outcome=success` before THIS planner reports `complete` to its parent (or, at L1, before commit). Build-QA fires at L2, L3, L4 separately as each level closes — not just at L1.

8. **Closeout + commit** — after the L1 drop's build-QA passes, the orchestrator runs `mage check` (full module integration gate), then commits per segment-close, **not per-droplet** (the MCP server bounces after every commit + `mage install` + Claude Code restart; batching reduces friction proportionally).

**State machine**: `todo` → `in_progress` (orchestrator sets on dispatch) → `complete | failed` (set on agent return). `outcome = success | failure | blocked`. Always `mcp__ta__update` to record transitions — these are the audit trail.

**Inspection** during a cascade run:
- MCP: `mcp__ta__list_sections(scope="cascade")`, `mcp__ta__search(scope="cascade.drop", all=true)`, `mcp__ta__get(items=[{id: "..."}])`.
- CLI (read-only inspection only): `ta search --scope cascade --all --json`, `ta list-sections cascade.drop --json`, `ta get <id> --json`.

**Dogfood discipline (MCP-first)**: cascade record CRUD goes through MCP. If `mcp__ta__*` fails for cascade ops, REPORT and PAUSE — don't silently fall back to `./bin/ta` CLI. CLI is for inspection and for build operations (`mage`-routed Go commands).

**Known limitation (F23 OFF)**: auto_spawn (`{now}` / `{state.initial}` / `{parent.<field>}` token expansion) is not yet implemented; QA twins must be created manually per `mcp__ta__create`. F23 lands as a separate slice.

See `docs/cascade-methodology.md` for the methodology contract and `E2E_FIXES.md` for the open tracker of cascade-related findings.

## MCP server — pinning the project directory

`ta`'s MCP-server invariant is one project per process: the server resolves its schema from the spawn cwd by default. Two ways to make sure the cwd is right:

- **Launch Claude Code FROM the active project checkout.** The Claude Code process inherits its own cwd to spawned MCP servers, so starting Claude in `/abs/path/to/project` gives `ta` the right project automatically. This is the simplest path and the recommended default.
- **Use `--project <abs-path>` in the MCP server invocation** when the launcher cannot control the spawn cwd. Add the flag to the spawn command in your `.mcp.json` registration:

  ```json
  {
    "mcpServers": {
      "ta": {
        "command": "ta",
        "args": ["--project", "/abs/path/to/project"]
      }
    }
  }
  ```

  The path must be absolute, must exist, and must contain `.ta/schema.toml`. Empty / unset → cwd fallback (existing behavior). The flag wins over cwd when both are present.

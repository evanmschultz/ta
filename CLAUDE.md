# CLAUDE.md — project guidance for ta

Project-local guidance for Claude Code (and other assistants that read CLAUDE.md) when working inside the `ta` tree.

## Subagent Spawn Defaults — Background-First

Inherits from the global rule in `~/.claude/CLAUDE.md` § "Subagent Spawn Defaults — Background-First": spawn agents with `run_in_background: true` by default. Foreground mode lets agents bypass their declared `tools:` allowlist; background mode enforces it. For ta-specific build → QA → commit flows, this is critical — agents have repeatedly tried `node`, `python3`, raw `gofmt`, raw `go test`, all of which are NOT in any agent's allowlist but were reachable via foreground inheritance.

Use foreground only when the agent's result is required to decide your immediate next step AND the task is short enough that the safety trade-off doesn't pay back. For ta's build / QA / planning agents this is rare.

## Pre-QA LSP Refresh Discipline

ta is a Go project. The active LSP is gopls. Before spawning any `go-qa-proof-agent` or `go-qa-falsification-agent`, the gopls daemon's workspace index must reflect the build agent's edits — otherwise QA reads stale diagnostics that don't match disk truth (recurring failure mode: agent reports "undefined: X" for symbols that mage check confirms exist).

- **Hook**: `~/.claude/hooks/pre_agent_lsp_refresh.sh` fires on PreToolUse(Agent) and recycles gopls when the spawned agent is a QA variant. Machine-local today; will be relocated into ta's project-local hook tree (`<project>/.claude/hooks/`) once the dogfood phase ships project-local hook management. At that point, every ta dev gets the hook automatically via `ta init`.
- **Manual fallback**: invoke the `/gopls-sync` skill or restart Claude Code from `/Users/evanschultz/Documents/Code/hylla/ta/main` (the active checkout) if the hook doesn't help.
- **Authoritative verification stays `MAGEFILE_JSON=1 mage check`** — LSP refresh ensures QA's evidence layer matches build truth, but mage check is the gate. Never trust LSP diagnostics over a passing mage check.

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

- **Huh removal** — `charm.land/huh/v2` referenced in 6 non-test files: `cmd/ta/init_cmd.go`, `cmd/ta/init_multi.go`, `cmd/ta/huh_form.go`, `cmd/ta/huh_theme.go`, `cmd/ta/commands.go`, `cmd/ta/template_cmd.go`, `cmd/ta/main.go`. F38d migrates `init_multi.go::runMultiCategoryPicker` only. Track each remaining file as an F39+ slice; goal = zero huh imports by end of dogfood.
- **F23 runtime-fill semantics** — `cascade.droplet` auto_spawn block in `examples/schemas/cascade.toml` is COMMENTED OUT pending F23 supporting `{now}` / `{state.initial}` / `{parent.<field>}` token expansion for required-no-default fields. Without it, dogfood requires manual QA twin creation per droplet. Schedule as architectural slice post-F38.
- **TUI verification artifacts (gifs + ascii)** committed under `cmd/ta/testdata/vhs/` AND linked from `README.md`, `examples/README.md`, `docs/cascade-methodology.md`. Each demo'able flow gets one tape that emits BOTH the test golden AND the README/docs gif. Single source of truth.
- **Coverage gate** — `cmd/ta` package at 67.1% (target ≥70%). Pre-existing dead branches (TTY confirm, form paths). Schedule a coverage-only slice post-F38.
- **`ta` ↔ Claude Code hook management via shipped schemas** — `claude_hooks` / `claude_skills` / `claude_settings_fragments` schemas don't exist yet. Required so `ta init` installs the LSP-refresh hook (and others) into `<project>/.claude/hooks/` automatically per the dogfood plan in `README.md`. Currently machine-local.
- **MCP project arg gate-keeping** — every MCP tool accepts a `path` arg pointing at any project dir on disk. Real reach surface; review for security gates pre-MVP.
- **TUI expansion** (post-dogfood, post-CLI-refinement, post-huh-removal) — `-t` / `--tui` flag for browse/search/edit, glamour-rendered preview panes, vim-style multi-select, line numbers in record blocks. Locked direction; out of pre-MVP scope.
- **Magefile uses `gofmt`, not `gofumpt`** — memory rule says gofumpt routed through mage; magefile contradicts. Update magefile or revisit memory. Out of F38 scope.

**MVP-feature-completion gate**: every item above is either closed or explicitly punted to post-`v0.1.0` with a tracking issue. No `// TODO` / `// HACK` / `// XXX` comments left in source.

## Cascade isolation — agents test ONLY their slice

A builder or QA agent operating below strict package level MUST run only the tests their slice owns — not the whole module. Sibling agents racing on the same checkout produce a polluted working tree; running `mage Test` (full module) gives a verdict muddied by other agents' WIP.

- **Below-package scope**: `MAGEFILE_JSON=1 mage testFunc TestMyThing` (single test) or `MAGEFILE_JSON=1 mage testFuncs TestA TestB TestC` (multiple) or with package narrowing via `TA_TEST_PKG=./internal/ops mage testFunc TestMyThing`. Routes through `go test -run <pattern>`.
- **Package-level scope**: `MAGEFILE_JSON=1 mage testPkg ./internal/ops`. One package end-to-end; verdict reflects exactly what the slice owns.
- **Module-level scope**: `MAGEFILE_JSON=1 mage Test` (or `mage Check`). Run by orchestrator-level QA + commit gate, not by sub-package agents mid-build.

The agent runner reports its verdict against the scope it owns. Higher-level QA (segment, confluence, drop) escalates to wider scopes. Orchestrator runs the whole. This mirrors the cascade methodology's "QA at the level integration actually happens" rule (`docs/cascade-methodology.md` §4).

Memory rule still applies: NEVER invoke raw `go test` / `go vet` / `go build` / `gofmt` / `gofumpt`. Always route through mage. The `--project` flag, `MAGEFILE_JSON=1` env var, and the testFunc / testFuncs / testPkg targets are how an agent narrows scope without bypassing the gate.

## Cascade methodology — canonical reference

The agent cascade methodology that ta dogfoods (and the future article / blog post seeds from) lives at [`docs/cascade-methodology.md`](docs/cascade-methodology.md). It's the **app-agnostic** version: thesis, droplet shape, role and model bindings, QA placement, nesting model, failure handling, audit trail, reference implementations. The older Tillsyn-flavored draft (`AGENT_CASCADE_DESIGN.md`) was retired — `docs/cascade-methodology.md` is the canonical source for any cascade questions, planning conversations, and the eventual article.

When orchestrating a cascade in this project — point at `docs/cascade-methodology.md` first, then `docs/PLAN.md` for ta-specific plan/drop sequencing.

## Ta CLI usage

- All `ta <read-command>` invocations from agents MUST pass `--json`. ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun.
- Read commands that accept `--json`: `ta get`, `ta list-sections`, `ta schema` (action=get, the default), `ta search`.
- Mutating commands (`ta create`, `ta update`, `ta delete`, `ta schema --action=create|update|delete`) return a concise laslig success notice on both surfaces; their MCP counterparts already return JSON. Use `--verbose` on the CLI when you want the post-mutation record echoed back.
- All `mage <target>` invocations from agents MUST set `MAGEFILE_JSON=1`. This routes `mage test` / `mage check` / `mage cover` through `go test -json` for agent-parseable output. Fmt, Vet, and Tidy emit plain text either way — only the test-runner step changes.
- Bare `ta` without a TTY is the MCP server — no explicit subcommand needed when registering in `.mcp.json` / `.codex/config.toml`.

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

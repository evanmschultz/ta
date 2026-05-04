# AGENTS.md — project guidance for agent runners

Project-local guidance for agent runners (Codex, etc.) when working inside the `ta` tree. Mirrors `CLAUDE.md` — the two files MUST stay in lockstep.

## Subagent Spawn Defaults — Background-First

Spawn agents with background-mode by default (Codex's equivalent of Claude Code's `run_in_background: true`). Foreground mode lets agents bypass their declared `tools:` allowlist; background mode enforces it. For ta-specific build → QA → commit flows, this is critical — agents have repeatedly tried `node`, `python3`, raw `gofmt`, raw `go test`, all of which are NOT in any agent's allowlist but were reachable via foreground inheritance.

Use foreground only when the agent's result is required to decide the immediate next step AND the task is short enough that the safety trade-off doesn't pay back. For ta's build / QA / planning agents this is rare.

## Pre-QA LSP Refresh Discipline

ta is a Go project. The active LSP is gopls. Before spawning any `go-qa-proof-agent` or `go-qa-falsification-agent`, the gopls daemon's workspace index must reflect the build agent's edits — otherwise QA reads stale diagnostics that don't match disk truth (recurring failure mode: agent reports "undefined: X" for symbols that mage check confirms exist).

- **Hook**: `~/.claude/hooks/pre_agent_lsp_refresh.sh` (or Codex equivalent under `~/.codex/hooks/`) fires before agent spawn and recycles gopls when the spawned agent is a QA variant. Machine-local today; will be relocated into ta's project-local hook tree (`<project>/.claude/hooks/`, `<project>/.codex/hooks/`) once the dogfood phase ships project-local hook management. At that point, every ta dev gets the hook automatically via `ta init`.
- **Manual fallback**: invoke `gopls-sync` skill or restart the agent runner from `/Users/evanschultz/Documents/Code/hylla/ta/main` (the active checkout) if the hook doesn't help.
- **Authoritative verification stays `MAGEFILE_JSON=1 mage check`** — LSP refresh ensures QA's evidence layer matches build truth, but mage check is the gate. Never trust LSP diagnostics over a passing mage check.

## TUI stack — bubbletea/bubbles/lipgloss/glamour/laslig (huh is being removed)

Target stack: `charm.land/bubbletea/v2`, `charm.land/bubbles/v2`, `charm.land/lipgloss/v2`, `charm.land/glamour/v2`, `github.com/evanmschultz/laslig`. NO huh in the long run. Existing huh usage (`ta init` multi-category picker) stays pre-dogfood; replace slice-by-slice. New TUI surface MUST go bubbletea-direct.

## TUI verification — teatest + goldens + VHS, never self-report

NEVER claim TUI behavior works without a captured artifact.

- **Golden snapshots** via `internal/render/schema_flow_test.go::assertSchemaFlowGolden` pattern.
- **`charm.land/x/exp/teatest`** for headless drive of bubbletea models — captures View() at key transitions, snapshots to `.golden`.
- **VHS** (`charm.land/vhs`) for visual artifacts (animated `.gif` / `.txt`) when structural goldens don't catch cursor / color / timing drift. Artifacts committed under `testdata/vhs/`.
- The agent runner MUST run these tools and inspect the artifacts. Self-narration is not evidence.

## Pre-MVP cleanup tracker

Mirrors `CLAUDE.md` § "Pre-MVP cleanup tracker". Phasing: dogfood → full CLI → full TUI overhaul (100% huh-free). MVP launches with zero tech debt. Track open items there.

## Cascade methodology — canonical reference

The agent cascade methodology that ta dogfoods (and the future article / blog post seeds from) lives at [`docs/cascade-methodology.md`](docs/cascade-methodology.md). It's the **app-agnostic** version: thesis, droplet shape, role and model bindings, QA placement, nesting model, failure handling, audit trail, reference implementations.

When orchestrating a cascade in this project — point at `docs/cascade-methodology.md` first, then `docs/PLAN.md` for ta-specific plan/drop sequencing.

## Ta CLI usage

- All `ta <read-command>` invocations from agents MUST pass `--json`. ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun.
- Read commands that accept `--json`: `ta get`, `ta list-sections`, `ta schema` (action=get, the default), `ta search`.
- Mutating commands (`ta create`, `ta update`, `ta delete`, `ta schema --action=create|update|delete`) return a concise laslig success notice on both surfaces; their MCP counterparts already return JSON. Use `--verbose` on the CLI when you want the post-mutation record echoed back.
- All `mage <target>` invocations from agents MUST set `MAGEFILE_JSON=1`. This routes `mage test` / `mage check` / `mage cover` through `go test -json` for agent-parseable output. Fmt, Vet, and Tidy emit plain text either way — only the test-runner step changes.
- Bare `ta` without a TTY is the MCP server — no explicit subcommand needed when registering in `.mcp.json` / `.codex/config.toml`.

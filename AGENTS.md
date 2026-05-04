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

## TUI / huh form discipline — verify with goldens before claiming PASS

ta uses `huh` forms for the `ta init` multi-category picker today and will grow more TUI surface during dogfood. NEVER claim TUI behavior works without verifying against a golden snapshot or equivalent capture. Self-reported "the picker looks right" is not evidence.

- **Pattern**: `internal/render/schema_flow_test.go::assertSchemaFlowGolden` is the canonical example. Materializes a `testdata/*.golden` on first run, byte-compares on subsequent runs.
- **Pre-dogfood scope**: cover at minimum the huh picker output for `ta init` multi-category (`cmd/ta/init_multi.go`).
- **Post-dogfood scope**: when the bubbletea TUI lands, use `charm.land/x/exp/teatest` for snapshot capture.
- **Before claiming "the TUI looks right"**: run the relevant golden test. If no golden exists, write one. Don't waste the dev's time on false-positive PASS claims.

## Cascade methodology — canonical reference

The agent cascade methodology that ta dogfoods (and the future article / blog post seeds from) lives at [`docs/cascade-methodology.md`](docs/cascade-methodology.md). It's the **app-agnostic** version: thesis, droplet shape, role and model bindings, QA placement, nesting model, failure handling, audit trail, reference implementations.

When orchestrating a cascade in this project — point at `docs/cascade-methodology.md` first, then `docs/PLAN.md` for ta-specific plan/drop sequencing.

## Ta CLI usage

- All `ta <read-command>` invocations from agents MUST pass `--json`. ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun.
- Read commands that accept `--json`: `ta get`, `ta list-sections`, `ta schema` (action=get, the default), `ta search`.
- Mutating commands (`ta create`, `ta update`, `ta delete`, `ta schema --action=create|update|delete`) return a concise laslig success notice on both surfaces; their MCP counterparts already return JSON. Use `--verbose` on the CLI when you want the post-mutation record echoed back.
- All `mage <target>` invocations from agents MUST set `MAGEFILE_JSON=1`. This routes `mage test` / `mage check` / `mage cover` through `go test -json` for agent-parseable output. Fmt, Vet, and Tidy emit plain text either way — only the test-runner step changes.
- Bare `ta` without a TTY is the MCP server — no explicit subcommand needed when registering in `.mcp.json` / `.codex/config.toml`.

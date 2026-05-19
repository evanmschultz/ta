# ta

A tiny MCP server that lets LLM coding agents read and write TOML and Markdown files as if they were a structured database — with schemas to keep agents honest.

`ta` exposes these tools over MCP stdio:

- **`get`** — read one record by id (raw bytes by default, structured fields with `fields=[...]`), or every record under an id prefix.
- **`list_sections`** — enumerate record ids under a scope, in file-parse order.
- **`create`** — create a new record; fails if the id already exists. `type` is required and db-qualified (`<db>.<type>`).
- **`update`** — PATCH-style update of an existing record; partial overlays, atomic re-validation.
- **`delete`** — remove a record by id, or a whole file by id prefix.
- **`search`** — structured + regex search across records under a scope.
- **`schema`** — inspect or mutate the resolved schema (get, create, update, delete on db / type / field levels).

Build plan: [`docs/PLAN.md`](docs/PLAN.md).

## Install

From a clone of this repo:

```sh
mage install
```

This builds `ta` and drops the binary at `$HOME/.local/bin/ta`. That directory is on the default `$PATH` on modern Unix, so no Go toolchain is needed to *run* `ta` — only to build it.

Requires Go 1.26 or newer at build time. The binary is pure Go and statically linkable.

## MCP client config

For Claude Code, register `ta` with the `claude mcp add` CLI — not by hand-editing a config file. From inside your project (or the bare root of a bare-repo-plus-worktree layout), run:

```sh
claude mcp add --transport stdio ta -- ta
```

Breakdown:

- `--transport stdio` — how `ta` speaks MCP (over child-process stdin/stdout).
- First `ta` — the **name** the server is registered under (tools appear as `mcp__ta__*`).
- `--` — separator; everything after is the spawn command, not a Claude flag.
- Second `ta` — the **command** to spawn (shell-resolved via `$PATH`).

No `--scope` flag → defaults to **local scope**, which writes to `~/.claude.json` under the current project's cwd and keeps the registration private to your machine. Pass `--scope project` if you want the registration committed to the repo (lands in `.mcp.json` at the project root, managed by the CLI — don't hand-edit it).

Verify the registration landed with:

```sh
claude mcp list
```

`ta` reads no runtime arguments; all tool arguments arrive over MCP. Use `ta --help` for a summary of CLI flags (`--version`, `--log-startup`, `--project`).

### Pinning the project directory

The MCP server resolves its schema from one project per process. By default that's the spawn cwd — which works whenever the MCP client launches `ta` with cwd set to the project root (e.g. starting Claude Code from inside the project checkout).

For launchers that cannot control the spawn cwd, pass `--project <abs-path>` in the registration command. With `claude mcp add`:

```sh
claude mcp add --transport stdio ta -- ta --project /abs/path/to/project
```

Or hand-rolled in a `.mcp.json` your launcher accepts:

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

The flag must be absolute, must exist, and must contain `.ta/schema.toml`. Empty / unset → cwd fallback. The flag wins over cwd when both are present.

## Schemas

Each project carries one schema at `<project>/.ta/schema.toml`. The runtime reads exactly that one file — no home-layer cascade, no ancestor walk. If the project has no schema, `ta` errors with a clear message.

A schema declares one or more **dbs**. Each db lists the file paths it owns (TOML or Markdown — format inferred from the path extension) and the record types those files may contain.

Example `.ta/schema.toml`:

```toml
[plans]
paths = ["plans.toml"]
description = "Planning records."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "blocked", "done"]

[plans.task.fields.body]
type = "string"
```

With that schema in place, an agent can create a task:

```json
{
  "name": "create",
  "arguments": {
    "path": "/abs/path/to/project",
    "id": "plans.task-001",
    "type": "plans.task",
    "data": {
      "id": "task-001",
      "status": "doing",
      "body": "## Approach\n\nStart by..."
    }
  }
}
```

The on-disk bracket header IS the id — `[plans.task-001]` in `plans.toml`. The record's type lives in `.ta/index.toml`, never in the id. Validation failures come back as structured JSON — the agent sees exactly which field failed which rule.

## Building from source

```sh
mage check   # fmtcheck, vet, test, tidy — full-module commit gate
mage build   # produces ./bin/ta
mage install # builds and drops the binary at $HOME/.local/bin/ta
mage fmt     # run gofumpt (latest, auto-installed) in-place
```

Run `mage -l` for the full target list.

### Scope-narrowed test runs (cascade-friendly)

When multiple agents work on the same checkout in a cascade, full-module `mage test` gives a verdict polluted by sibling agents' WIP. Each agent runs ONLY the tests their slice owns:

```sh
mage testFunc TestMyThing                       # one test, whole module
mage testFunc 'TestA|TestB|TestC'               # several tests, pipe-joined regex
TA_TEST_PKG=./internal/ops mage testFunc TestX  # narrow scope further
mage testPkg ./internal/ops                     # full package, end-to-end
mage check                                      # full module — orchestrator-level
```

Test output auto-detects TTY status via `laslig/gotestout` — agents and CI pipes get plain text, humans on a terminal get a styled summary. No env-var prefix needed; the targets above just work in either context. Cascade methodology § "QA Placement" (`docs/cascade-methodology.md`) covers the level-by-level discipline; the rule of thumb is **agents test only what their slice owns; QA escalates one level up**.

## Cascade-agent workflow hooks

If you're running ta's build / QA agents in a cascade (planner → builder → QA-proof + QA-falsification), the LSP daemon (gopls for ta) caches workspace state that lags behind the build agent's writes. A QA agent spawned next reads from a stale LSP and reports diagnostics that don't reflect disk truth. Mage check is authoritative, but the QA agent reads LSP, not mage.

**Pattern**: a `PreToolUse` hook on the `Agent` tool that recycles the LSP daemon when the spawned agent is a QA variant. Machine-local example shipped with ta:

```bash
# ~/.claude/hooks/pre_agent_lsp_refresh.sh
# Fires on Agent spawn. When subagent_type matches qa-proof or
# qa-falsification, kills gopls so the next LSP call gets a fresh index.
INPUT=$(cat)
if printf '%s' "$INPUT" | grep -qE '"subagent_type"[[:space:]]*:[[:space:]]*"[^"]*qa-(proof|falsification)[^"]*"'; then
    pkill -f 'gopls' 2>/dev/null || true
fi
exit 0
```

Register in `~/.claude/settings.json`:

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Agent",
        "hooks": [{ "type": "command", "command": "~/.claude/hooks/pre_agent_lsp_refresh.sh" }]
      }
    ]
  }
}
```

The concept is **universal** across LSP-based languages — extend the script with `tsserver`, `pylsp`, `rust-analyzer` etc. when needed. Documented in [`docs/cascade-methodology.md`](docs/cascade-methodology.md) §4.7 "Pre-QA LSP Refresh — Universal Discipline".

**Dogfood plan**: the long-term goal is for ta itself to manage these hooks (alongside agents, instructions docs, skills, rules) via shipped schemas. Once the dogfood phase lands `claude_hooks` schema support, `ta init` will install the hook into `<project>/.claude/hooks/` automatically and every ta dev gets it without manual setup. Until then, install machine-local from this section.

## TUI demos

Animated VHS recordings of every interactive bubbletea surface live under [`cmd/ta/testdata/vhs/`](cmd/ta/testdata/vhs/). Three high-traffic flows inline below; the full per-tape index follows.

### Root subcommand menu

![bare `ta` root menu showing the top-level subcommand picker rendered in bubbletea](cmd/ta/testdata/vhs/menu.gif)

Bare `ta` (no args) drops into the root subcommand menu — the entry point most devs hit first.

### Multi-category picker `space` select-all

![`ta init` multi-category picker with `space` toggling every visible leaf in a group on and off](cmd/ta/testdata/vhs/picker_select_all.gif)

`space` on a group header toggles every visible leaf at once — the UX the previous huh-based form could not express.

### Interactive `ta create` form

![`ta create` interactive bubbletea form walking through required fields with inline validation](cmd/ta/testdata/vhs/form_create.gif)

`ta create` without all required fields drops into an interactive form covering required + optional fields with inline validation.

### Full per-tape index

- [`smoke.gif`](cmd/ta/testdata/vhs/smoke.gif) / [`.txt`](cmd/ta/testdata/vhs/smoke.txt) — minimum-viable smoke recording proving the VHS pipeline + golden contract are wired.
- [`menu.gif`](cmd/ta/testdata/vhs/menu.gif) / [`.txt`](cmd/ta/testdata/vhs/menu.txt) — bare `ta` root subcommand menu.
- [`picker_project.gif`](cmd/ta/testdata/vhs/picker_project.gif) / [`.txt`](cmd/ta/testdata/vhs/picker_project.txt) — multi-category picker initial render.
- [`picker_filter.gif`](cmd/ta/testdata/vhs/picker_filter.gif) / [`.txt`](cmd/ta/testdata/vhs/picker_filter.txt) — filter mode narrowing leaves.
- [`picker_select_all.gif`](cmd/ta/testdata/vhs/picker_select_all.gif) / [`.txt`](cmd/ta/testdata/vhs/picker_select_all.txt) — `space` toggling all visible leaves in a group.
- [`picker_bootstrap_home.gif`](cmd/ta/testdata/vhs/picker_bootstrap_home.gif) / [`.txt`](cmd/ta/testdata/vhs/picker_bootstrap_home.txt) — `ta init --bootstrap-home` bootstrap picker.
- [`confirm_overwrite.gif`](cmd/ta/testdata/vhs/confirm_overwrite.gif) / [`.txt`](cmd/ta/testdata/vhs/confirm_overwrite.txt) — confirm prompt for overwrite.
- [`form_create.gif`](cmd/ta/testdata/vhs/form_create.gif) / [`.txt`](cmd/ta/testdata/vhs/form_create.txt) — `ta create` interactive form.

Re-record with `mage Vhs` (requires the `vhs` binary on `$PATH`).

## License

Apache-2.0 — see [`LICENSE`](LICENSE).

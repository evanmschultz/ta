# ta

A tiny MCP server that lets LLM coding agents read and write TOML files as if they were a structured database — with schemas to keep agents honest.

`ta` exposes four tools over MCP stdio:

- **`get`** — read a section by bracket path, returning the raw TOML bytes (leading comment block included, so human-written docstrings come along for the ride).
- **`list_sections`** — enumerate every section in a file, in file order.
- **`schema`** — return the resolved schema for a file; with an optional `section` argument, return just the type matched by its first segment.
- **`upsert`** — create or update a section, validated against a schema; untouched bytes (comments, blank lines, other sections) are preserved byte-for-byte.

Design notes: [`docs/ta.md`](docs/ta.md). Build plan: [`docs/PLAN.md`](docs/PLAN.md).

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

`ta` reads no runtime arguments; all tool arguments arrive over MCP. Use `ta --help` for a summary of CLI flags (`--version`, `--log-startup`).

## Schemas

`ta` resolves schemas by cascade-merging from `~/.ta/schema.toml` (the base) down through every `.ta/schema.toml` in the target file's directory chain. Schemas defined closer to the target file supersede same-named schemas from further out; schemas unique to any level are additive. If neither home nor any ancestor has a `.ta/schema.toml`, the call fails with a clear error.

Example `.ta/schema.toml`:

```toml
[schema.task]
description = "A unit of work"

[schema.task.fields.id]
type = "string"
required = true

[schema.task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "blocked", "done"]

[schema.task.fields.body]
type = "string"
```

With that schema in place, an agent can upsert a task:

```json
{
  "name": "upsert",
  "arguments": {
    "path": "/abs/path/to/tasks.toml",
    "section": "task.task_001",
    "data": {
      "id": "TASK-001",
      "status": "doing",
      "body": "## Approach\n\nStart by..."
    }
  }
}
```

Validation failures come back as structured JSON — the agent sees exactly which field failed which rule.

## Building from source

```sh
mage check   # fmtcheck, vet, test, tidy
mage build   # produces ./bin/ta
mage install # builds and drops the binary at $HOME/.local/bin/ta
```

Run `mage -l` for the full target list.

## License

Apache-2.0 — see [`LICENSE`](LICENSE).

# ta

A tiny MCP server that lets LLM coding agents read and write TOML files as if they were a structured database — with schemas to keep agents honest.

`ta` exposes three tools over MCP stdio:

- **`get`** — read a section by bracket path, returning the raw TOML bytes.
- **`list_sections`** — enumerate every section in a file, in file order.
- **`upsert`** — create or update a section, validated against a schema; untouched bytes (comments, blank lines, other sections) are preserved byte-for-byte.

Design notes: [`docs/ta.md`](docs/ta.md). Build plan: [`docs/PLAN.md`](docs/PLAN.md).

## Install

```sh
go install github.com/evanmschultz/ta/cmd/ta@latest
```

Requires Go 1.26 or newer. The binary is pure Go and statically linkable.

## MCP client config

For Claude Code, add this to `.mcp.json` (or the equivalent in your client):

```json
{
  "mcpServers": {
    "ta": {
      "command": "ta"
    }
  }
}
```

`ta` reads no runtime arguments; all tool arguments arrive over MCP. Use `ta --help` for a summary of CLI flags (`--version`, `--log-startup`).

## Schemas

`ta` looks up the schema for a given TOML data file by walking up from the file's directory for a `.ta/config.toml`, then falling back to `~/.ta/config.toml`. The closest match wins.

Example `.ta/config.toml`:

```toml
[schema.task]
description = "A unit of work"

[schema.task.fields.id]
type = "string"
required = true

[schema.task.fields.status]
type = "string"
required = true
allowed = ["todo", "doing", "blocked", "done"]

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
mage install # go install into $GOBIN
```

Run `mage -l` for the full target list.

## License

MIT — see [`LICENSE`](LICENSE).

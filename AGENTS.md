# AGENTS.md — project guidance for agent runners

Project-local guidance for agent runners (Codex, etc.) when working inside the `ta` tree. Mirrors `CLAUDE.md` — the two files MUST stay in lockstep.

## Ta CLI usage

- All `ta <read-command>` invocations from agents MUST pass `--json`. ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun.
- Read commands that accept `--json`: `ta get`, `ta list-sections`, `ta schema` (action=get, the default), `ta search`.
- Mutating commands (`ta create`, `ta update`, `ta delete`, `ta schema --action=create|update|delete`) return a concise laslig success notice on both surfaces; their MCP counterparts already return JSON. Use `--verbose` on the CLI when you want the post-mutation record echoed back.
- All `mage <target>` invocations from agents MUST set `MAGEFILE_JSON=1`. This routes `mage test` / `mage check` / `mage cover` through `go test -json` for agent-parseable output. Fmt, Vet, and Tidy emit plain text either way — only the test-runner step changes.
- Bare `ta` without a TTY is the MCP server — no explicit subcommand needed when registering in `.mcp.json` / `.codex/config.toml`.

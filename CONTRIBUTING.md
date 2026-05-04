# Contributing to ta

ta is a tiny MCP server that lets LLM coding agents read and write structured TOML / Markdown files. Contributors run a multi-agent cascade workflow against the codebase, so the contribution rules cover both the code itself and the agent discipline that produces it.

## Build + test

- **mage-first**. All build / test / lint / format goes through mage targets; never invoke raw `go build`, `go test`, `go vet`, `gofmt`, or `gofumpt`.
- **For agent-parseable output**: prefix any `mage` invocation with `MAGEFILE_JSON=1` so the test-runner step emits `go test -json`. Fmt / Vet / Tidy emit plain text either way.
- **The authoritative gate**: `MAGEFILE_JSON=1 mage check`. Vet + FmtCheck + Test (with race) + Tidy. Must pass before any commit.
- **Coverage gate**: ≥ 70% line coverage on touched packages.

## Cascade-agent workflow rules

If you're using Claude Code (or another LLM client that supports subagents) to drive contributions, follow these rules so cascade nodes don't trip over each other:

### Pre-QA LSP refresh (universal)

Build agents edit code on disk. The next QA agent spawned reads from disk via the language server (gopls for ta; the principle is universal across `tsserver` / `pylsp` / `rust-analyzer` / etc.). The LSP daemon's workspace cache may lag behind the build agent's writes — the QA agent then reads stale diagnostics and ships false counterexamples grounded in evidence that doesn't match disk truth.

**Refresh the LSP daemon BEFORE spawning a QA agent.** The build runner (mage check) stays the authoritative gate; the LSP refresh ensures the QA agent's evidence-gathering layer matches that truth.

The recommended pattern is a Claude Code `PreToolUse` hook on the `Agent` tool that recycles the active LSP daemon when the spawned `subagent_type` matches `qa-proof` or `qa-falsification`. See [`README.md`](README.md) § "Cascade-agent workflow hooks" for a concrete `~/.claude/hooks/pre_agent_lsp_refresh.sh` reference + `~/.claude/settings.json` registration snippet. Concept documented in [`docs/cascade-methodology.md`](docs/cascade-methodology.md) §4.6 "Pre-QA LSP Refresh — Universal Discipline".

**Authoritative verification stays the build runner.** Never trust LSP diagnostics over a passing `mage check`. The hook is UX polish on the QA agent's evidence layer, not a substitute for the build gate.

### Subagent spawn discipline

- **Spawn agents with `run_in_background: true` by default.** Foreground mode inherits the orchestrator's full tool list — agents reach for `node`, `python3`, raw `gofmt`, etc. that aren't in their declared `tools:` allowlist. Background mode enforces the allowlist strictly.
- **Foreground is the exception**, used only when the agent's result is required to decide the immediate next step AND the task is short enough that blocking is cheaper than the parallel-context overhead. For build / QA / planning agents this is rare.
- **Parallel QA pairs** (qa-proof + qa-falsification) MUST go background — they're independent and concurrent dispatch is the whole point.

## Commit style

Conventional-commit subject only. Lowercase. ≤ ~72 chars.

- Types: `feat`, `fix`, `refactor`, `chore`, `docs`, `test`, `ci`, `style`, `perf`.
- Subject describes WHAT changed, not HOW.
- No body, no bullet lists, no co-authored-by trailers, no trailing period.
- Per-file enumeration belongs in the PR description / changelog, not the subject.

Examples:

- `feat(md): file-as-record backend with yaml frontmatter (f31)`
- `fix(ops): type-aware resolver + md section shape (f29 f30)`
- `docs(claude): pre-qa lsp refresh + background-agents discipline`

## Code rules

- **Idiomatic Go**: standard naming, package structure, consumer-side interfaces, `context.Context` first param, errors wrapped with `%w`, table-driven tests, race-safe concurrency.
- **YAGNI**: no abstractions for hypothetical future variation. Two concrete uses before extracting an interface.
- **Comments**: explain WHY, not WHAT. Skip them when the code is self-documenting.
- **Schema discipline**: changes to `examples/schemas/*.toml` must round-trip through `internal/schema::TestExamplesLoadCleanly` (and any per-schema regression test).

## Reference docs

- [`README.md`](README.md) — install, MCP setup, schema overview, cascade-agent workflow hooks.
- [`docs/cascade-methodology.md`](docs/cascade-methodology.md) — app-agnostic agent cascade methodology (thesis, droplet shape, QA placement, etc.).
- [`docs/PLAN.md`](docs/PLAN.md) — ta-specific plan / drop sequencing.
- [`docs/ta.md`](docs/ta.md) — long-form ta design notes.
- [`CLAUDE.md`](CLAUDE.md) — Claude Code project rules (CLI usage, agent rules, cascade pointer).

## License

Apache-2.0 — see [`LICENSE`](LICENSE).

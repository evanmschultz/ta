# Contributing to ta

ta is a tiny MCP server that lets LLM coding agents read and write structured TOML / Markdown files. Contributors run a multi-agent cascade workflow against the codebase, so the contribution rules cover both the code itself and the agent discipline that produces it.

## Build + test

- **mage-first**. All build / test / lint / format goes through mage targets; never invoke raw `go build`, `go test`, `go vet`, `gofmt`, or `gofumpt`.
- **Test output is TTY-aware**: `mage test` / `mage check` / `mage cover` route through `laslig/gotestout` which auto-renders styled summaries on terminals and plain text for agents / CI pipes. No env-var gymnastics — just call the target.
- **Format**: `mage fmt` runs `gofumpt` (latest, auto-installed via `go install mvdan.cc/gofumpt@latest` if missing). gofumpt is a strict superset of `gofmt -s`.
- **The authoritative gate**: `mage check`. FmtCheck + Vet + Test (with race) + Tidy + TemplatesBuildEmbed (skip-on-pnpm-absent). Must pass before any commit.
- **Coverage gate**: ≥ 70% line coverage on touched packages.

### Track B Astro templates — pnpm workflow

Track B of the HTML-template substrate (`web/templates_embed/` → `internal/templates_html_embed/dist/`) is an Astro build that feeds the Go `//go:embed` package at compile time. It is driven by `pnpm`. The discipline:

- **pnpm install once per checkout**: `pnpm install -C web/templates_embed`. This materializes `node_modules/` (gitignored) so the Astro build has its toolchain.
- **`mage templatesBuildEmbed`** — runs `pnpm run build` inside `web/templates_embed/`. Fails loud if pnpm is not on PATH OR if the Astro build returns non-zero. This is the prerequisite of the full embedded build for Track B; anything that depends on `internal/templates_html_embed/dist/` must run this first.
- **`mage templatesBuildVerifyStable`** — rebuilds the Astro dist tree twice (then once more on a flake) and sha256-compares the directory contents. Retry-once-then-fail flake policy: tolerates a single non-deterministic build, surfaces persistent drift as a hard failure with divergent-hash summary.
- **`mage check` skip semantics**: if `pnpm` is NOT on PATH, `mage check` prints a verbose WARN line ("pnpm not on PATH; skipping TemplatesBuildEmbed for Track B; mage Check passes WITHOUT Track B build verification") and continues. The Go-only contributor path stays green without a Node toolchain. **If pnpm IS on PATH but `pnpm install` has not been run**, the Astro build will fail and `mage check` will fail too — this is intentional. The skip is for pnpm-absence only, not for incomplete-pnpm-environments.
- **Why Astro / pnpm**: the embed-side dist tree is generated, not hand-authored, so ta ships statically-rendered HTML produced by stil-themed Astro templates without dragging Node into the runtime. The Go `//go:embed` line pulls the generated artifact at compile time; end users never need pnpm.

### Scope-narrowed test runs (cascade discipline)

Agents working on the same checkout in a cascade share one working tree. Running the full module's tests at any sub-module node produces a verdict polluted by sibling agents' in-flight work. Each agent runs ONLY the tests its slice owns:

- **Below-package** (single-file or tight-cluster builds): `mage testFunc <pattern>` (single test) or `mage testFunc '<Test1>|<Test2>|...'` (several joined via `|` regex). Optional `TA_TEST_PKG=./internal/ops` env scopes to one package.
- **Package-level** (segment / confluence work that owns one package): `mage testPkg ./internal/ops`. End-to-end verdict for the package.
- **Module-level** (orchestrator / drop close): `mage check`. Full integration verdict — the ONLY level that runs the full suite.

The discipline mirrors `docs/cascade-methodology.md` §4.5 "Test-scope Isolation". The principle is universal across languages — wherever the build runner exposes a name-pattern test target (Cargo, vitest, pytest -k, etc.), the same level-by-level escalation applies.

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

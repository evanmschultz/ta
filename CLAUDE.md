# CLAUDE.md — project guidance for ta

Project-local guidance for working inside the `ta` tree. Global rules (Tillsyn coordination, Section 0 reasoning, evidence sources, worktree hygiene, output style) live at `~/.claude/CLAUDE.md` and are NOT duplicated here.

## Agent Routing — Backend Dispatch (Chain Mode)

`ta-*` subagents route to different LLM backends based on role. Each role has a fallback chain (primary + ordered fallbacks). The orchestrator MUST honor this routing.

Chain definitions: [`.claude/agent-chains.sh`](.claude/agent-chains.sh). Dispatcher: [`bin/agent-dispatch.sh`](bin/agent-dispatch.sh). Full explainer: [`docs/agent-backend-routing.md`](docs/agent-backend-routing.md).

**Cascade methodology constraint** ([`docs/cascade-methodology.md`](docs/cascade-methodology.md)): builder droplets touch **1-2 small blocks of code INCLUDING their tests**. The 7B coder handles atomic edits fine. **Only ONE local model is in the chains** — `qwen2.5-coder:7b`. Larger local models melt the machine (a 30B carries ~24 GB KV cache) and aren't in any chain.

**Planner + plan-QA enforcement rule (load-bearing)**: every planner output MUST be reviewed by plan-QA to confirm each terminal builder droplet is 1-2 small blocks (with tests included in that count). If a droplet would be larger, the planner MUST decompose further before plan-QA passes. The 7B builder backend will FAIL LOUDLY on under-decomposed droplets — that is the desired feedback signal. Do not "fix" by routing to a bigger model; fix by re-decomposing.

**Role-appropriate tool allowlists (the actual sandbox)**: every dispatched agent — regardless of backend — runs inside its host runtime (Claude Code via Ollama endpoint, Claude Code via Anthropic OAuth, or codex exec). Each role gets a tool allowlist scoped to what THAT role needs:

- **Planners**: ta create/update/get (to write plans) + read tools (Read, Grep, hylla) + context7. NO Edit/Write/Bash — planners write plans into ta records, not code.
- **Plan-QA + Build-QA**: read-only — Read, Grep, Glob, hylla, ta get/search, `Bash(git diff *)` + `Bash(git log *)` for diff inspection. NO Edit/Write — QA verifies, doesn't change.
- **Builders**: Edit, Write, Read, Grep, Glob, Bash, LSP for the code they're editing. ta get/search (read context) but NOT ta create/update (cascade records are planner/closeout territory). Builders DO the actual file edits — that's the entire point of having builders.
- **Closeout**: Read + `Bash(git *)` + `Bash(mage check)` (verify) + ta update (mark cascade nodes complete). No Edit/Write — closeout coordinates, doesn't author.

The dispatcher passes the persona's frontmatter `tools:` line as `--allowedTools` to Claude Code (or maps it equivalently for codex). **The persona file IS the sandbox spec for that role.** Each project's `.claude/agents/<role>.md` controls what each role can do — edit those files (via `mcp__ta__update` since they're ta records) to tighten role permissions.

**Ollama-routed dispatches** use `--bare --mcp-config` to load only the project's MCP servers (skips CLAUDE.md auto-load, hooks, keychain — saves ~30K input tokens per call) AND `--allowedTools` from the persona for role-appropriate restriction. They are NOT hermetic — the builder running on Ollama has Edit/Write/Read like any builder, just restricted to the persona's allowlist.

### Persona Bash Scoping Discipline — Mage-Only, Never Raw Language Tooling

**Agents NEVER run raw language tooling.** No `go test`, no `go vet`, no `go build`, no `gofmt`, no `gofumpt`, no `pnpm`, no `npm`, no `node`, no `npx`. All test/build/check commands route through the project's build runner — for ta, that's mage. **Orchestrators are the exception** (they're trusted to run any command); agents are scoped.

Per-role Bash allowlist (verified empirically — `--allowedTools` filters tools from the model's visible toolset, so disallowed Bash patterns are simply absent from the model's tool descriptions):

```
role             scoped Bash (allowed)                                          NOT allowed
----             ----                                                            ----
builder          Bash(mage testFunc *), Bash(mage testPkg *),                    go *, gofmt, gofumpt,
                 Bash(git diff *), Bash(git log *), Bash(git status)             pnpm *, npm *, node *,
                                                                                  npx *, git commit/push/reset
planner          (NO Bash — planners author plans, don't run commands)            all
qa-proof         Bash(mage testFunc *), Bash(mage testPkg *), Bash(mage check),  raw lang tooling,
qa-falsif        Bash(git diff *), Bash(git log *), Bash(git status)             git commit/push/reset
closeout         Bash(mage check), Bash(git diff *), Bash(git log *),            raw lang tooling,
                 Bash(git status)                                                 git commit/push/reset
```

**If a capability is missing, add a mage target — do NOT broaden the persona's Bash scope.** Example: ta-fe-* agents currently rely on mage targets for FE testing. If FE tests today only route through `pnpm run test`, the right fix is adding `mage testFe` (and similar) to magefile.go, NOT adding `Bash(pnpm *)` to fe persona allowlists. Other projects adopting this pattern follow the same rule — mage (or your project's equivalent build runner) is the only tool/test entrypoint for agents.

**Verified at runtime**: Claude Code's `--allowedTools` enforcement is strong. Tools NOT in the allowlist (including unscoped `Bash` when only `Bash(mage testFunc *)` is allowed) are FILTERED FROM THE MODEL'S VISIBLE TOOLSET. The model doesn't see them in its tool descriptions and so doesn't attempt them. Smoke verified: an agent asked to call a tool outside its allowlist self-reports "I cannot use that tool because it is not part of my allowlist" — no `permission_denials` event, no `tool_use` attempt.

```
role-primaries{role,backend,model,dispatch}:
ta-go-builder,ollama-local,qwen2.5-coder:7b,bash-dispatcher
ta-fe-builder,ollama-local,qwen2.5-coder:7b,bash-dispatcher
ta-go-planning,codex-exec,gpt-5.4+medium,bash-dispatcher
ta-fe-planning,codex-exec,gpt-5.4+medium,bash-dispatcher
ta-go-qa-falsification,codex-exec,gpt-5.4+xhigh,bash-dispatcher
ta-fe-qa-falsification,codex-exec,gpt-5.4+xhigh,bash-dispatcher
ta-go-qa-proof,claude-native,opus,agent-tool
ta-fe-qa-proof,claude-native,opus,agent-tool
ta-closeout,claude-native,opus,agent-tool
```

Builder fallback chain: 7B → codex gpt-5.4 (effort=low) → claude haiku. NOT a larger local model. Planning + QA + closeout chains never use local Ollama — they fall over cloud APIs only.

**Dispatch**:

- `bash-dispatcher` rows → `echo "<prompt>" | ./bin/agent-dispatch.sh --role <role> --cwd "$(pwd)"`. Walks the chain in `.claude/agent-chains.sh` until one tier succeeds. The dispatched agent runs in its backend (Claude Code with Ollama endpoint / codex exec / Claude Code with Anthropic OAuth) and **performs the work itself** using its role-appropriate tools. The orchestrator's job is to construct the dispatch prompt, monitor the result, and route the next cascade step — NOT to do the file edits. Parse stdout JSON for the response (`result` field for Claude-Code-based backends; codex stream events for codex). Read stderr for `served_by=...` line — note FALLBACK if a non-primary tier served.
- `agent-tool` rows → native Claude `Agent` tool with `subagent_type = <role>`. Reads `.claude/agents/<role>.md`. Anthropic Opus.
- **Dry-run before live dispatch**: `--dry-run` flag prints the tier-1 command without acquiring a slot or dispatching. Useful for verifying wiring.
- **Atomic-builder override**: for proper-cascade 1-2 block droplets, the default 7B is correct. For an unusual case needing a larger model, pass `--model qwen3-coder:30b`.
- **LSP staleness**: `pre_agent_lsp_refresh.sh` fires on Claude's `Agent` tool only. Before any Bash-dispatched call after recent file edits, invoke `/gopls-sync` manually.

**Lock policy**: only Ollama tiers (local + cloud) use file locks (atomic `mkdir` at `/tmp/agent-dispatch/<backend>.<N>.lock`). Codex and Claude tiers don't lock — they dispatch directly and the external API's 429/401 response triggers chain advance. The API itself is the authority on its own concurrency.

**Why**: Ollama v0.14.0+ speaks the Anthropic Messages API. The dispatcher spawns `claude -p` with `ANTHROPIC_BASE_URL=http://localhost:11434` — subagent runs as a full Claude Code session (Read/Edit/Bash/MCP all work) with zero Anthropic token cost for ollama-routed calls. Codex stays separate via `codex exec --ephemeral`. Anti-recursion: dispatcher appends a "you ARE the role" suffix so spawned sessions don't recurse.

**Fallback consumes tokens**: when ollama AND codex are unavailable, the chain falls through to `claude-native` which uses your Anthropic subscription. Deliberate trade-off — better than failing the dispatch entirely. Watch your usage if claude-native fires frequently.

**Orchestrator dispatch pattern** (concrete `Bash()` invocation for `bash-dispatcher` rows):

For short prompts (single-line):
```
Bash(
  command='echo "<task prompt>" | ./bin/agent-dispatch.sh --role ta-go-builder --cwd "$(pwd)"',
  run_in_background=true,
  description='ta-go-builder: <short>',
)
```

For long prompts (multi-line with Section 0 directive, file paths, acceptance gates), write the prompt to a temp file first and use `--prompt-file`:
```
# 1. Write prompt
Write(file_path="/tmp/dispatch-<role>-<id>.md", content="<full multi-line prompt>")

# 2. Dispatch
Bash(
  command='./bin/agent-dispatch.sh --role ta-go-builder --cwd "$(pwd)" --prompt-file /tmp/dispatch-<role>-<id>.md',
  run_in_background=true,
  description='ta-go-builder: <short>',
)
```

**Parsing the response** (orchestrator-side after dispatch returns):
- ollama-local / claude-native served → stdout is Claude Code JSON envelope. The agent's text reply is `.result`. Parse via `jq -r .result` or read the JSON object directly.
- codex-exec served → stdout is raw codex stream output (header lines + body + `tokens used N` footer). The agent's reply is the contiguous non-marker block before the footer.
- Read **stderr** `[disp] served_by=<backend>:<model>` line to know which parser to use. If line contains `FALLBACK`, the primary tier failed and a fallback served.
- Cascade record state (via `mcp__ta__get` on the droplet record) is the authoritative completion signal. The agent updates its state via the persona's `mcp__ta__update` allowance (planners + closeout) OR the orchestrator transitions state after parsing the dispatch response (builders + QA roles whose personas are read-only on cascade records by design).

## Editing role personas

`.claude/agents/ta-*.md` files are ta records under the `claude_agents.agent` schema. Both the native `Agent` tool AND the Bash dispatcher read these. NEVER edit them directly with Edit/Write. Workflow:

1. `mcp__ta__update` on the agent record id (e.g. `ta-go-builder`) with the desired field overlay.
2. `ta template save --kind=agent --path=./.claude/agents/<file>.md --group=ta --overwrite` — pushes the updated persona into `~/.ta/agents/ta/<file>.md`.
3. Verify both files match (project + HOME) before commit.

Direct edits of `~/.claude/agents/*-agent.md` or `~/.ta/agents/*/*.md` bypass ta's substrate tracking and create drift.

## Hylla discipline — Go-only, primary evidence source

Evidence order for Go work: (1) Hylla (`mcp__hylla__*`) for committed symbols/refs/graphs; (2) `git diff` for uncommitted; (3) Read/Grep/Glob for non-Go and post-edit pre-push Go; (4) Context7 + `go doc` + LSP for external semantics.

**Hylla is Go-only.** Never query for `.toml`, `.json`, `.md`, `.yml`, scripts.

**Push-often + ingest-after-push**: after every commit batch push to origin, then trigger `mcp__hylla__hylla_ingest`. The `/commit-and-reingest` skill bundles both. Between push and ingest, fall back to `git log` / `Read`.

Spawn prompts for dispatched ta-go-* roles MUST include the Hylla artifact ref (e.g. `github.com/evanmschultz/ta@main`).

## ta CLI usage

- All `ta <read-command>` invocations from dispatched roles MUST pass `--json`. ANSI laslig output is for humans.
- `--json` accepted on: `ta get`, `ta list-sections`, `ta schema`, `ta search`.
- Mutating commands (`create` / `update` / `delete` / `schema --action=...`) return a concise success notice; use `--verbose` for the post-mutation record.
- `mage test` / `mage check` / `mage cover` route through `laslig/gotestout` (auto-detects TTY).
- Bare `ta` without a TTY is the MCP server.
- **NEVER invoke raw `go test` / `go vet` / `go build` / `gofmt` / `gofumpt`.** Always route through mage.

## Cascade-managed development — use ta to manage ta

For any non-trivial work (multi-droplet slice, planner/builder/QA roles, QA twins), track via ta cascade records — not in-session task lists or markdown plans.

Workflow per [`docs/cascade-methodology.md`](docs/cascade-methodology.md) §3 + §4:

1. **Drop record** — `mcp__ta__create` a `cascade.drop`. id = `drop_NNN.drop.<slug>`. Required: `drop_number`, `structural_type='drop'`, `role`, `state`, `title`, `created_at`, `updated_at`.
2. **Planner record** — `mcp__ta__create` a `cascade.planner` child. Dispatch a `ta-go-planning` (or `ta-fe-planning`) role via the dispatcher; the dispatched role updates the planner record via `mcp__ta__update`.
3. **Plan-QA twins** — when the planner is created, immediately create `cascade.qa_proof` + `cascade.qa_falsification` children (`target_id = <planner-id>`, `state = 'todo'`). These BLOCK descent. Dispatch as parallel background calls.
4. **Recursive decomposition** — if a planner would emit more than 4 children OR cross more than 1 domain concern OR cross more than 1 package, it MUST decompose into child planners. Cascade recurses through planner levels — each terminal level emits 3-6 atomic droplets per child planner touching disjoint paths. Plan-QA twins fire at EVERY planner node.
5. **Builder droplets** — terminal leaves only. One `cascade.droplet` per atomic build slice, each touching ≤4 distinct code-block edits. Builders dispatch in parallel when `paths` are disjoint. No LLM QA at droplet level; build+test is the only droplet-level gate.
6. **Package-level build+test** — after all droplets for a package report `complete`, run `mage testPkg <path>`. Failures cycle: enclosing planner ingests failure → fix directive → droplet re-runs.
7. **Build-QA twins at EVERY planner level** — once all direct children of a planner are `complete` AND package gates green, create two QA children targeting the sub-tree. Both must complete+success before the planner reports complete to its parent.
8. **Closeout + commit** — after L1 drop's build-QA passes, run `mage check`, then commit per segment-close (MCP server bounces after every commit + `mage install` + Claude Code restart; batch reduces friction).

**State machine**: `todo` → `in_progress` → `complete | failed`. `outcome = success | failure | blocked`. Always `mcp__ta__update` to record transitions.

**Dogfood discipline (MCP-first)**: cascade record CRUD goes through MCP. If `mcp__ta__*` fails, REPORT and PAUSE — don't silently fall back to `./bin/ta` CLI.

## Cascade isolation — test only your slice

A dispatched role operating below strict package level MUST run only its slice's tests — not the whole module. Sibling dispatches racing produce polluted working trees; `mage Test` (full module) gives a verdict muddied by other WIP.

- **Below-package**: `mage testFunc TestMyThing` or `mage testFunc 'TestA|TestB|TestC'`. Package narrowing: `TA_TEST_PKG=./internal/ops mage testFunc TestMyThing`.
- **Package-level**: `mage testPkg ./internal/ops`.
- **Module-level**: `mage Test` (or `mage Check`). Orchestrator-level QA + commit gate only.

## MCP server — pinning the project directory

ta's MCP-server invariant is one project per process. Two ways to pin cwd:

- **Launch Claude Code FROM the active project checkout.** Inherits cwd to spawned MCP servers — starting Claude in `/abs/path/to/project` gives `ta` the right project automatically.
- **`--project <abs-path>`** in the MCP server invocation when the launcher cannot control spawn cwd:

  ```json
  {"mcpServers":{"ta":{"command":"ta","args":["--project","/abs/path/to/project"]}}}
  ```

  Path must be absolute, must exist, must contain `.ta/schema.toml`. Empty / unset → cwd fallback. The flag wins over cwd when both are present.

## Project-specific docs

- [`docs/agent-backend-routing.md`](docs/agent-backend-routing.md) — full explainer for the routing pattern above; copy-into-other-project guide.
- [`docs/cascade-methodology.md`](docs/cascade-methodology.md) — the canonical cascade contract; ta dogfoods it.
- [`docs/pre-mvp-tracker.md`](docs/pre-mvp-tracker.md) — open pre-`v0.1.0` items, what's punted, what's closed.
- [`docs/tui-guidance.md`](docs/tui-guidance.md) — TUI stack (bubbletea/bubbles/lipgloss/glamour/laslig), teatest + goldens + VHS verification rules.
- [`docs/PLAN.md`](docs/PLAN.md) — ta-specific drop sequencing.

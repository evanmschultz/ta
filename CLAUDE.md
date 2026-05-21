# CLAUDE.md — project guidance for ta

Project-local guidance for working inside the `ta` tree. Global rules (Tillsyn coordination, Section 0 reasoning, evidence sources, worktree hygiene, output style) live at `~/.claude/CLAUDE.md` and are NOT duplicated here.

## Agent Routing — Backend Dispatch (Chain Mode)

`ta-*` subagents route to different LLM backends based on role. Each role has a fallback chain (primary + ordered fallbacks). The orchestrator MUST honor this routing.

Chain definitions: [`.claude/agent-chains.sh`](.claude/agent-chains.sh). Dispatcher: [`bin/agent-dispatch.sh`](bin/agent-dispatch.sh). Full explainer: [`docs/agent-backend-routing.md`](docs/agent-backend-routing.md).

**Cascade methodology constraint** ([`docs/cascade-methodology.md`](docs/cascade-methodology.md)): builder droplets touch **1-2 small blocks of code INCLUDING their tests**. Builder primary is `claude-native|haiku` via the `Agent` tool (3x cheaper per-token than sonnet per Anthropic pricing); sonnet fallback if haiku fails. Local Ollama was dropped 2026-05-21 — Q7B silent-failed on tool calls (text without Edit/Write); 30B pressured VRAM/thermal under sustained concurrent dispatch and slowed iteration. Haiku trades local-free compute for cloud-speed + predictable concurrency.

**Planner + plan-QA enforcement rule (load-bearing)**: every planner output MUST be reviewed by plan-QA to confirm each terminal builder droplet is 1-2 small blocks (with tests included in that count). If a droplet would be larger, the planner MUST decompose further before plan-QA passes. The 7B builder backend will FAIL LOUDLY on under-decomposed droplets — that is the desired feedback signal. Do not "fix" by routing to a bigger model; fix by re-decomposing.

**Role-appropriate tool allowlists (the actual sandbox)**: every dispatched agent — regardless of backend — runs inside its host runtime (Claude Code via Ollama endpoint, Claude Code via Anthropic OAuth, or codex exec). Each role gets a tool allowlist scoped to what THAT role needs:

- **Planners**: ta create/update/get (to write plans) + read tools (Read, Grep, hylla **for Go personas only**) + context7. NO Edit/Write/Bash — planners write plans into ta records, not code.
- **Plan-QA + Build-QA**: read-only — Read, Grep, Glob, hylla **(Go personas only)**, ta get/search, `Bash(git diff *)` + `Bash(git log *)` for diff inspection. NO Edit/Write — QA verifies, doesn't change.
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
ta-go-builder,claude-native,haiku,agent-tool
ta-fe-builder,claude-native,haiku,agent-tool
ta-go-planning,codex-exec,gpt-5.4+high,bash-dispatcher
ta-fe-planning,codex-exec,gpt-5.4+high,bash-dispatcher
ta-go-qa-falsification,codex-exec,gpt-5.4+high,bash-dispatcher
ta-fe-qa-falsification,codex-exec,gpt-5.4+high,bash-dispatcher
ta-go-qa-proof,claude-native,opus,agent-tool
ta-fe-qa-proof,claude-native,opus,agent-tool
ta-closeout,claude-native,opus,agent-tool
```

Builder fallback chain: claude haiku → claude sonnet. No Ollama, no Codex tier for builders — Anthropic-only via Agent tool. Planning + QA-falsification chains route through bash dispatcher (**codex-only, no `claude -p` fallback subprocess**); when codex exhausts, the dispatcher exits with `CODEX_EXHAUSTED` on stderr and the orchestrator re-dispatches via the native Agent tool (subscription). QA-proof + closeout are agent-tool primary (Opus).

**Dispatch**:

- `bash-dispatcher` rows → `./bin/agent-dispatch.sh --role <role> --cwd "$(pwd)" --prompt "<short pointer>"` (stdin pipe also works). Walks the chain in `.claude/agent-chains.sh` until one tier succeeds. The dispatched agent runs in its backend (Claude Code with Ollama endpoint / codex exec / Claude Code with Anthropic OAuth) and **performs the work itself** using its role-appropriate tools. The orchestrator's job is to construct a SHORT dispatch prompt (pointer to the ta cascade record + Section 0 directive + Hylla ref if Go — see §Orchestrator dispatch pattern below for shape), monitor the result, and route the next cascade step — NOT to do the file edits. Parse stdout JSON for the response (`result` field for Claude-Code-based backends; codex stream events for codex). Read stderr for `served_by=...` line — note FALLBACK if a non-primary tier served.
- `agent-tool` rows → native Claude `Agent` tool with `subagent_type = <role>`. Reads `.claude/agents/<role>.md`. Anthropic Opus.
- **Dry-run before live dispatch**: `--dry-run` flag prints the tier-1 command without acquiring a slot or dispatching. Useful for verifying wiring.
- **Builder model override**: builder primary is `claude-native|haiku` via Agent tool — fast + cheap (3x cheaper than sonnet per Anthropic pricing). Local Ollama (qwen3-coder:30b) was dropped 2026-05-21 — running multiple 30B agents concurrently pressured VRAM/thermal budget. `--model` override at dispatch time only applies to bash-dispatcher tiers (planning, qa-falsification) which run codex-exec.
- **LSP staleness**: `pre_agent_lsp_refresh.sh` fires on Claude's `Agent` tool only. Before any Bash-dispatched call after recent file edits, invoke `/gopls-sync` manually.

**Lock policy**: Codex and Claude tiers dispatch directly — the external API's 429/401 response triggers chain advance. The API itself is the authority on its own concurrency. Dispatcher still supports Ollama tier file locks (atomic `mkdir` at `/tmp/agent-dispatch/<backend>.<N>.lock`) but no role currently routes through Ollama as of 2026-05-21.

**Anti-recursion**: the dispatcher appends a "you ARE the role" suffix to spawned sessions so they don't recurse into another dispatch.

**Codex exhaustion → orchestrator-managed fallback (no `claude -p` subprocess)**: when both codex tiers fail (rate-limit, account, transport), the dispatcher exits non-zero with `[disp] CODEX_EXHAUSTED role=<role>` on stderr instead of subprocessing `claude -p`. The orchestrator parses that signal and re-dispatches the role via the native `Agent` tool with `subagent_type=<role>` + `model=sonnet` (or `opus` for high-stakes QA-falsif). Rationale: `claude -p` as a subprocess can pick up `ANTHROPIC_API_KEY` from the environment (API-key billing), bypassing the Claude Code subscription; the Agent tool always uses the subscription. The `dispatch_claude_native` function in `bin/agent-dispatch.sh` survives only as a safety-net for `chain_qa_proof` / `chain_closeout` manual overrides, and even there it unsets `ANTHROPIC_API_KEY` + `ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN` in its subshell so an API-key bill can never appear.

**Orchestrator dispatch pattern**: spawn prompts are SHORT pointers, not embedded specs. Task content lives in the ta cascade record (read by the agent via `mcp__ta__get <record-id>`); persona framing lives in `.claude/agents/<role>.md` (injected automatically by the dispatcher via `--append-system-prompt`). The orchestrator hands the agent: (a) the record id(s) to read, (b) Section 0 directive verbatim, (c) Hylla artifact ref for Go roles, (d) anything genuinely task-specific that's NOT in the record yet. Nothing else — duplicating acceptance_criteria, definition_of_complete, file paths, etc. into the spawn prompt is anti-pattern; the agent reads the record.

Default form — inline `--prompt`:
```
Bash(
  command='./bin/agent-dispatch.sh --role ta-fe-planning --cwd "$(pwd)" --prompt "Task record: drop_005.drop.planner_l2_a_templates_a11y_d0 (read it + parent drop_005.drop.path_a_thariq_visual_fixes + L1 decomposition drop_005.drop.planner_decompose for context). Section 0 directive: <verbatim>. Hylla: github.com/evanmschultz/ta@main (Go only). Begin."',
  run_in_background=true,
  description='ta-fe-planning: L2-A decompose',
)
```

Stdin pipe (`echo "<prompt>" | ./bin/agent-dispatch.sh ...`) also works; pick whichever is cleanest at the call site.

**Never use `--prompt-file`.** The dispatcher takes the prompt directly via `--prompt` or stdin — temp files are unnecessary and obscure the call site. The `--prompt-file` flag exists only as a last-resort fallback for pathologically nested shell quoting; in practice it should not appear.

Precedence (dispatcher line 151): `--prompt` > `--prompt-file` > stdin.

**Parsing the response** (orchestrator-side after dispatch returns):
- ollama-local / claude-native served → stdout is Claude Code JSON envelope. The agent's text reply is `.result`. Parse via `jq -r .result` or read the JSON object directly.
- codex-exec served → stdout is raw codex stream output (header lines + body + `tokens used N` footer). The agent's reply is the contiguous non-marker block before the footer.
- Read **stderr** `[disp] served_by=<backend>:<model>` line to know which parser to use. If line contains `FALLBACK`, the primary tier failed and a fallback served.
- Cascade record state (via `mcp__ta__get` on the droplet record) is the authoritative completion signal. The agent updates its state via the persona's `mcp__ta__update` allowance (planners + closeout) OR the orchestrator transitions state after parsing the dispatch response (builders + QA roles whose personas are read-only on cascade records by design).

**Tool-call audit (orchestrator MUST do this after every dispatch)**: never trust an agent's self-reported "verdict: pass" or "tool X returned Y". Open the dispatch output file and inspect the actual stream:
- codex stream lines: each tool invocation surfaces as `mcp: <server>/<tool> (started)` then `(completed)` or `(failed)`. A `(failed)` followed by `user cancelled MCP tool call` is a cancellation, not a tool error.
- ollama / claude-native JSON envelope: `tool_use` events in the message iteration list; absence of expected tool events with `num_turns: 1` means the agent answered without calling tools.
- Cross-check every required-work claim against stream evidence: record updates must show `mcp: <ta>/update (completed)`; file edits must show `tool_use: Edit`; tests must show `tool_use: Bash` with the mage command. If the stream doesn't show it, the work didn't happen — re-dispatch or finish it orchestrator-direct.
- Also flag out-of-scope tool calls (anything outside the persona's `tools:` allowlist) — they shouldn't be possible because Claude Code filters tools by allowlist, but codex's MCP exposure is broader and the orchestrator is the only enforcement.

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

Spawn prompts for dispatched ta-go-* roles MUST include the Hylla artifact ref `github.com/evanmschultz/ta@main`.

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

- [`docs/HANDOFF-pre-mvp-feature-complete.md`](docs/HANDOFF-pre-mvp-feature-complete.md) — current source-of-truth for the post-drop_004 reality, the in-flight drop_005 Path A + Path B plan, and the no-semver-phrasing rule.
- [`docs/agent-backend-routing.md`](docs/agent-backend-routing.md) — full explainer for the routing pattern above; copy-into-other-project guide.
- [`docs/cascade-methodology.md`](docs/cascade-methodology.md) — the canonical cascade contract; ta dogfoods it.
- [`docs/pre-mvp-tracker.md`](docs/pre-mvp-tracker.md) — open pre-mvp-feature-complete items, what's punted, what's closed.
- [`docs/tui-guidance.md`](docs/tui-guidance.md) — TUI stack (bubbletea/bubbles/lipgloss/glamour/laslig), teatest + goldens + VHS verification rules.
- [`docs/PLAN.md`](docs/PLAN.md) — ta-specific drop sequencing.

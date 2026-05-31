# CLAUDE.md — project guidance for ta

Project-local guidance for the `ta` tree. Global rules (Tillsyn-style coordination, Section 0 reasoning, evidence sources, output style) live at `~/.claude/CLAUDE.md` — not duplicated here.

## Architecture & Cascade Tracking

ta is the **source-of-truth sibling** for the cross-project agent-dispatch + persona architecture (per-persona `settings.json` + `bin/agent-dispatch.sh` + `bin/agent-audit-toon.py` + `.claude/hooks/ta_action_gate.py` + `.claude/hooks/post_tooluse_agent_audit.py` + canonical magefile + `CASCADE_METHODOLOGY.md`). Other siblings (sand, valv, lagom, bage, polyglot-foundation) cp FROM ta. Sync record + dev verify/commit/push/ingest checklist: `R_SHIP_HANDOFF.md` at repo root.

- **Cascade tracking on ta uses the `ta` MCP** (`mcp__ta__*` on `.ta/`-managed records), **NEVER `tillsyn` MCP** — only the `tillsyn` repo has Tillsyn MCP wired. Persona bodies do NOT make `mcp__tillsyn__*` calls; any leftover textual `mcp__tillsyn__*` ref in a persona body is INERT here.
- **`ta` records are the work-tracking source of truth on this repo.** Built-in `TaskCreate`/`TaskUpdate` are fine for a subagent's granular sub-steps (TDD micro-cycles) or tiny orch reminders — anything durable goes in a `ta` cascade record, not a chat-only list.

## Subagent Discipline

Full spec: `CASCADE_METHODOLOGY.md § Subagent Discipline (2026-05-27)` (byte-identical to tillsyn's canon). Load-bearing rules, terse:

- **Per-persona test surface (minimum only).** Planners: NO test execution (specify commands for builders). Plan-QA: `mage testPkg <import-path>` read-only — never `mage ci`/`testFunc`/raw `go *`. Builders: `mage testFunc <import-path> <Func>` ONLY for funcs they wrote (list each by full name in `## Tools Used`) — never `testPkg`/`ci`/`build`/raw `go test|build|vet`/`gofmt`/`gofumpt`/`go list`; `mage format` allowed ONCE at end. Build-QA: `mage testFunc <path> <Func>` for the funcs they verify/attack. Closeout: `mage check`/`ci` ONCE (unique role privilege; cascade-end gate; no concurrent builders).
- **Hylla MANDATORY-PRIMARY** for committed Go (planners + plan-QA): `mcp__hylla__*` BEFORE Read/LSP. Zero Hylla calls in `## Hylla Feedback` = automatic FAIL. (Go personas only — Hylla indexes Go.)
- **No self-rescoping.** Work exceeding 1-2 small code blocks (>80 prod LOC, >3 prod files, or ≥3 distinct top-level production symbols) → STOP + report BLOCKED for re-split. NEVER partial-ship + grade BUILD COMPLETE.
- **Plan-QA-falsification Rule 3.5:** for EVERY seam the plan wires, `mcp__hylla__hylla_node_full` the surrounding code (~30 lines each side). Inline `// TODO` / `// DEFERRED` / `// follow-up` / "blocked on" = plan **FAIL**. PLUS family-existence checks (query sibling/caller/called-by — partial families are a planning trap).
- **Failure-attribution (sibling-WIP coexistence):** a `mage test-*` error in a file OUTSIDE your declared `paths` → report `BLOCKED-by-sibling-WIP` (file:line) + STOP, never edit it; inside your `paths` → yours, attack it.
- **Closing-comment veracity:** `## Hylla Feedback` + `## Tools Used` (every call by full name + `wc -l` LOC) MANDATORY in every closing comment.
- **Orchestrator audits EVERY agent EVERY dispatch** via jq on the JSONL transcript: raw `go *`, `mage ci`/`testPkg` from builders, zero `mcp__hylla__*` from planners/plan-QA, Edit/Write outside declared `paths`, git mutations, mid-run `auth_request create`, cross-droplet snooping, `grep`/`sed`/`awk` via Bash (use native Grep/Read), missing closing-comment sections. Self-reported "PASS" is not authoritative — if the transcript doesn't show the call, the work didn't happen.

## Agent Routing — Backend Dispatch (Chain Mode)

`ta-*` subagents route to different LLM backends by role, each with a fallback chain the orchestrator MUST honor. Full explainer: `docs/agent-backend-routing.md`. Chains: `.claude/agent-chains.sh`. Dispatcher: `bin/agent-dispatch.sh`. The atomicity contract planners/builders obey is in the Cascade Methodology section below + `CASCADE_METHODOLOGY.md`.

```
role-primaries{role,backend,model,dispatch}:
ta-go-builder,claude-native,haiku,agent-tool
ta-fe-builder,claude-native,haiku,agent-tool
ta-go-planning,codex-exec,gpt-5.5+low,bash-dispatcher
ta-fe-planning,codex-exec,gpt-5.5+low,bash-dispatcher
ta-go-plan-qa-falsification,codex-exec,gpt-5.5+high,bash-dispatcher
ta-fe-plan-qa-falsification,codex-exec,gpt-5.5+high,bash-dispatcher
ta-go-build-qa-falsification,codex-exec,gpt-5.5+low,bash-dispatcher
ta-fe-build-qa-falsification,codex-exec,gpt-5.5+low,bash-dispatcher
ta-go-plan-qa-proof,claude-native,opus,agent-tool
ta-fe-plan-qa-proof,claude-native,opus,agent-tool
ta-go-build-qa-proof,claude-native,sonnet,agent-tool
ta-fe-build-qa-proof,claude-native,sonnet,agent-tool
ta-closeout,claude-native,opus,agent-tool
```

- **Builder** = haiku primary → sonnet fallback (Agent tool, Anthropic-only; no Ollama, no codex tier). Local Ollama dropped 2026-05-21 (silent tool-call failures + VRAM/thermal under concurrent dispatch).
- **Planning + QA-falsification** = codex-exec via `bin/agent-dispatch.sh` (codex-only, no `claude -p` subprocess fallback). On exhaustion the dispatcher exits non-zero with `[disp] CODEX_EXHAUSTED role=<role>` on stderr → orch re-dispatches via the native `Agent` tool (`subagent_type=<role>`, `model=sonnet`, or `opus` for high-stakes QA-falsif). Rationale: a `claude -p` subprocess can pick up `ANTHROPIC_API_KEY` (API-key billing); the Agent tool always uses the subscription. The `dispatch_claude_native` safety-net unsets `ANTHROPIC_API_KEY`/`_BASE_URL`/`_AUTH_TOKEN` in its subshell so an API-key bill can never appear.
- **QA-proof + closeout** = Agent-tool primary (plan-QA-proof + closeout = Opus; build-QA-proof = Sonnet, the cost-aware floor).

**Dispatch invariants:**

- `bash-dispatcher` rows → `./bin/agent-dispatch.sh --role <role> --cwd "$(pwd)" --prompt "<SHORT pointer>"` (stdin pipe also works). Spawn prompts are SHORT pointers, not embedded specs: (a) the ta record id(s) to read, (b) Section 0 directive verbatim, (c) Hylla artifact ref `github.com/evanmschultz/ta@main` for Go roles, (d) anything genuinely task-specific not yet in the record. Task content lives in the ta record (read via `mcp__ta__get`); persona framing is injected by the dispatcher (`--append-system-prompt`). Duplicating acceptance_criteria / paths into the prompt is anti-pattern. `agent-tool` rows → native `Agent` tool, `subagent_type=<role>`. Never `--prompt-file` (last-resort only). `--dry-run` prints the tier-1 command without dispatching.
- **Persona `tools:` lines declare PLAIN `Bash`, not scoped `Bash(...)`** — codex IGNORES the `tools:` line, and scoping over-restricts the built-in channel (blocks `go doc` + `mage` variants the role needs). The git-mutation block is enforced MECHANICALLY: Agent-tool roles via `.claude/hooks/ta_action_gate.py` (hardcoded git-verb deny baseline, independent of the passed `bash_deny`); codex roles via `--sandbox read-only` + hermetic execpolicy `prefix_rule(forbidden)`. Read-only git (`diff`/`status`/`log`/`show`) + `go doc` + `mage` stay allowed.
- **Agents NEVER run raw language tooling** (`go test/vet/build`, `gofmt`, `gofumpt`, `pnpm`, `npm`, `node`, `npx`) — all through mage. Persona-prose discipline; orchestrator is the exception. **Orchestrator is the sole committer/pusher** (all roles, both channels). Missing capability → ADD a mage target; never raw language tooling, never re-narrow `tools:` for git.
- **Tool-call audit (orch, after EVERY dispatch):** never trust a self-reported verdict. codex stream → `mcp: <server>/<tool> (started/completed/failed)` + `tool_use` lines; claude-native JSON envelope → `tool_use` events (absence with `num_turns:1` = answered without tools). Cross-check every required-work claim against stream evidence; if the stream doesn't show it, re-dispatch or finish orch-direct. `[disp] served_by=<backend>:<model>` on stderr names the parser + flags FALLBACK. Cascade-record state (`mcp__ta__get`) is the authoritative completion signal.

## Editing role personas

`.claude/agents/ta-*.md` files are ta records under the `claude_agents.agent` schema. Both the native `Agent` tool AND the Bash dispatcher read these. NEVER edit them directly with Edit/Write. Workflow:

1. `mcp__ta__update` on the agent record id (e.g. `ta-go-builder`) with the field overlay.
2. `ta template save --kind=agent --path=./.claude/agents/<file>.md --group=ta --overwrite` — pushes the updated persona into `~/.ta/agents/ta/<file>.md`.
3. Verify both files match (project + HOME) before commit.

Direct edits of `~/.claude/agents/*.md` or `~/.ta/agents/*/*.md` bypass ta's substrate tracking and create drift.

## Hylla discipline — Go-only, primary evidence source

Evidence order for Go work: (1) Hylla (`mcp__hylla__*`) for committed symbols/refs/graphs; (2) `git diff` for uncommitted; (3) Read/Grep/Glob for non-Go and post-edit pre-push Go; (4) Context7 + `go doc` + LSP for external semantics.

- **Hylla is Go-only.** Never query for `.toml`, `.json`, `.md`, `.yml`, scripts.
- **Push-often + ingest-after-push:** after every commit batch, push to origin, then trigger `mcp__hylla__hylla_ingest`. The `/commit-and-reingest` skill bundles both. Between push and ingest, fall back to `git log` / `Read`.
- Spawn prompts for dispatched `ta-go-*` roles MUST include the Hylla artifact ref `github.com/evanmschultz/ta@main`.

## ta CLI usage

- All `ta <read-command>` invocations from dispatched roles MUST pass `--json` (ANSI laslig output is for humans). `--json` accepted on `ta get`/`list-sections`/`schema`/`search`.
- Mutating commands (`create`/`update`/`delete`/`schema --action=...`) return a concise success notice; `--verbose` for the post-mutation record.
- `mage test`/`check`/`cover` route through `laslig/gotestout` (auto-detects TTY). Bare `ta` without a TTY is the MCP server.
- **NEVER raw `go test`/`go vet`/`go build`/`gofmt`/`gofumpt`** — always through mage.

## Cascade Methodology — Plan Down, Build Up

Canonical contract: `CASCADE_METHODOLOGY.md` (the methodology SOURCE — identical to tillsyn's; reconcile toward it, never overwrite) + `HYLLA_BIN.md §5`. ta-specific reference addenda (substrate comparison, canonical node-shape field spec, benchmarking): `docs/cascade-reference.md`. The orchestrator drives the cascade to completion AUTONOMOUSLY.

The recursive flow — captured here so it's never missed:

1. **PLAN DOWN, BUILD UP.** Plan top-down (a plan node decomposes into child plans + atomic build droplets); build bottom-up (atoms land first, integration nodes follow once inputs are green). Every plan node auto-gets a plan-QA pair (proof ∥ falsification); every build auto-gets a build-QA pair.
2. **RECURSE ON ATOMICITY — NO CHILD CAP.** The ONLY cap is atomic-droplet sizing: **1-2 small code blocks, ≤80 LOC incl. tests, ≤3 files**. A *code block* is one new/changed top-level production symbol (type, function, method) OR one cohesive same-purpose edit cluster — a new type, a new helper, and a rewrite of a *different* function are SEPARATE blocks, never folded under one label. A droplet naming **≥3 distinct production symbols** (tests excluded), projecting **>80 LOC**, or touching **>3 production files** is OVER BUDGET → emit a `kind=plan` sub-planner (it decomposes again), NOT an oversize build. **Plan-QA-falsification MEASURES this per droplet and FAILS the plan on any over-budget droplet — it never accepts the planner's "1-2 blocks" label on faith; on ANY plan amendment it re-measures EVERY droplet, not just the changed one.** "One coherent concern" / "a single non-separable unit" is NOT a budget exception. Depth is multi-level + **ASYMMETRIC** — branches nest as deep as each needs.
3. **PER-BRANCH PARALLELISM.** Every unblocked node of every kind moves at once — sibling sub-planners, plan-QA pairs, builders, build-QA pairs that are code-independent all run concurrently. QA twins are ALWAYS a parallel pair. The only serialization is `blocked_by` naming a real shared file/package or must-exist-first symbol; a shared interface/type sits as a SHALLOW leaf with `blocked_by` edges from its deeper consumers. A spurious `blocked_by` is an anti-pattern (plan-QA-falsification flags it).
4. **DESCENT GATE (per branch, not per tree).** A plan node's plan-QA pair must BOTH PASS before that node launches its child planners OR its build droplets — serializes only that branch's depth; siblings descend/build/QA in parallel. Plan-QA FAIL → wipe-and-replan that subtree. `blocked_by` on a plan node gates its BUILDS, not its decomposition (decomposition is read-only design against a dependency's spec'd shape; sub-planners launch as soon as the parent's plan-QA is green).
5. **DROPLET-LEVEL QA = the automated `mage ci` gate (NOT LLM).** Per droplet: builder builds → `mage ci` green → orch closes the build-QA twins against that gate + commits (no push). LLM proof/falsification QA runs at the planner/integration level, where integration risk lives — not per trivial droplet.
6. **ORCH AUTO-ADVANCE.** Drive the cascade to completion; do NOT ask permission per tick. plan-QA green → immediately launch children; subtree planning green → launch builders → mage-gate-close → commit → advance descendants/ancestors. STOP and ask the dev ONLY for: (a) a genuine fork the spec/methodology/memory can't resolve, (b) a hard blocker, (c) a QA-FAIL needing a design ruling, (d) a destructive/outward action (push/PR/ingest). Per-tick "say go" check-ins are an anti-pattern.

**Track non-trivial work via `ta` cascade records** (not in-session lists / markdown plans):

- **Drop** — `mcp__ta__create` a `cascade.drop`, id `drop_NNN.drop.<slug>` (`drop_number`, `structural_type='drop'`, `role`, `state`, `title`, timestamps).
- **Planner** — `cascade.planner` child; dispatch `ta-go-planning`/`ta-fe-planning`; the dispatched role updates its record via `mcp__ta__update`.
- **Plan-QA twins** — on planner create, immediately create `cascade.qa_proof` + `cascade.qa_falsification` (`target_id=<planner-id>`, `state='todo'`); they BLOCK descent; dispatch as parallel background calls.
- **Builder droplets** — terminal leaves only, one atomic slice each; dispatch in parallel when `paths` are disjoint.
- **Build-QA twins at EVERY planner level** — once all direct children are `complete` AND package gates green, two QA children target the subtree; both complete+success before the planner reports complete.
- **Closeout + commit** — after the L1 drop's build-QA passes, run `mage check`, then commit per segment-close.
- State machine: `todo → in_progress → complete | failed`; `outcome = success | failure | blocked`. Always `mcp__ta__update` transitions. Dogfood discipline: cascade-record CRUD goes through MCP; if `mcp__ta__*` fails, REPORT and PAUSE — don't silently fall back to `./bin/ta`.

## Cascade isolation — test only your slice

A dispatched role below strict package level runs ONLY its slice's tests — not the whole module (sibling dispatches racing produce polluted working trees; full-module `mage Test` gives a verdict muddied by other WIP).

- **Below-package:** `mage testFunc TestMyThing` or `mage testFunc 'TestA|TestB|TestC'`; package-narrow via `TA_TEST_PKG=./internal/ops mage testFunc TestMyThing`.
- **Package-level:** `mage testPkg ./internal/ops`.
- **Module-level:** `mage Test` / `mage check` — orchestrator-level QA + commit gate only.

## MCP server — pinning the project directory

ta's MCP-server invariant is one project per process. Two ways to pin cwd:

- **Launch Claude Code FROM the active project checkout** — inherits cwd to spawned MCP servers.
- **`--project <abs-path>`** in the MCP invocation when the launcher can't control spawn cwd:

  ```json
  {"mcpServers":{"ta":{"command":"ta","args":["--project","/abs/path/to/project"]}}}
  ```

  Path must be absolute, exist, and contain `.ta/schema.toml`. Empty/unset → cwd fallback. The flag wins over cwd when both are present.

## Project-specific docs

- `CASCADE_METHODOLOGY.md` — cascade methodology canon (identical to tillsyn's; the SOURCE).
- [`docs/cascade-reference.md`](docs/cascade-reference.md) — ta cascade reference addenda (test-scope isolation, pre-QA LSP refresh, substrates, node-shape field spec, benchmarking).
- [`docs/agent-backend-routing.md`](docs/agent-backend-routing.md) — full backend-routing explainer; copy-into-other-project guide.
- [`docs/HANDOFF-pre-mvp-feature-complete.md`](docs/HANDOFF-pre-mvp-feature-complete.md) — post-drop_004 reality + in-flight drop_005 Path A/B plan + no-semver-phrasing rule.
- [`docs/pre-mvp-tracker.md`](docs/pre-mvp-tracker.md) — open pre-mvp items: punted / closed.
- [`docs/tui-guidance.md`](docs/tui-guidance.md) — TUI stack (bubbletea/bubbles/lipgloss/glamour/laslig), teatest + goldens + VHS rules.
- [`docs/PLAN.md`](docs/PLAN.md) — ta-specific drop sequencing.

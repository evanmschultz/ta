# Cascade Reference — ta Addenda

> **Methodology canon:** [`CASCADE_METHODOLOGY.md`](../CASCADE_METHODOLOGY.md) (repo root) is the single source of truth for the cascade methodology — plan-down / build-up, the closed-12-kind enum, the three orthogonal axes (`kind` × `role` × `structural_type`), role/model bindings, QA placement, blocker-failure re-QA, failure handling, and the worked example. This document is **ta-specific reference addenda**: two operational disciplines the shared canon does not carry (test-scope isolation, pre-QA LSP refresh) plus the substrate comparison, the canonical node-shape field spec, and the benchmarking/metrics plan that seed ta's article/blog treatment.

## 1. Test-scope Isolation — Agents Verify Only Their Slice

Cascade nodes share one checkout on disk. While siblings run concurrently, the working tree carries WIP from every active builder. Running the entire module's test suite at any sub-module node gives a verdict muddied by other agents' edits. Each agent's verification MUST scope down to what its slice owns:

- **Below-package agents** (single-file or tight-cluster droplet builds) run a name-pattern test invocation — the build runner exposes a "run only these test functions" target. In ta's reference implementation that's `mage testFunc <pattern>` (one or several joined into a `|`-regex like `mage testFunc 'TestA|TestB|TestC'`), optionally narrowed by package via `TA_TEST_PKG=<path>`. The runner translates to `go test -run <pattern> <pkg>`. The agent's verdict reports only its own functions; sibling WIP outside that scope is invisible.
- **Package-level agents** (segment / confluence work that owns one package) run the full package: `mage testPkg <path>` / `go test <pkg>`. Verdict reflects the package end-to-end.
- **Orchestrator / drop-close** runs the full module: `mage check` / `go test ./...`. This is the integration verdict — the only level where cross-package interactions are tested.

The same discipline applies to the build runner equivalents in other languages (Cargo workspaces with `cargo test -p <crate> <name>`, npm scripts with `vitest run -t <name>`, etc.). The principle is universal: **tests scope to the slice; QA escalates one level up; integration is the orchestrator's responsibility.**

This pairs with §2's pre-QA LSP refresh — a fresh LSP plus a slice-scoped test run gives a clean evidence layer for the QA agent to reason against, without other agents' in-flight work corrupting either side.

## 2. Pre-QA LSP Refresh — Universal Discipline

Build agents edit code on disk. The next QA agent spawned reads from disk via the language server (gopls for Go, tsserver for TypeScript, pylsp for Python, rust-analyzer for Rust, and so on). The LSP daemon's workspace cache may lag behind the build agent's writes — leading the QA agent to flag "undefined: X" or "import not found" for symbols the build runner (mage / cargo / npm / etc.) confirms exist. The QA agent then ships false counterexamples grounded in stale state.

**Rule**: refresh the active LSP daemon BEFORE spawning a QA agent. The build runner stays the authoritative gate for code correctness; the LSP refresh ensures the QA agent's evidence-gathering layer matches that truth instead of trailing it.

**Implementation pattern (universal)**: a host-level pre-spawn hook that recycles the active LSP daemon when the spawned agent is a QA variant (typically matched by `subagent_type` containing `qa-proof` or `qa-falsification`). The daemon respawns on the next LSP request with a fresh workspace index. The hook is language-agnostic in *concept* — implementation differs per LSP server.

**Authoritative gate stays the build runner**, never the LSP. Never trust LSP diagnostics over a passing build runner. The LSP refresh is a UX polish on the QA agent's evidence layer, not a substitute for the build gate.

## 3. Reference Implementations

The methodology described in `CASCADE_METHODOLOGY.md` is implementation-agnostic. The same canonical node shape (id, role, structural_type, state, blockers, paths, packages, completion_contract, comments — see §4 for the field specification) can be stored and orchestrated through different substrates. A team can begin with the lightest substrate and graduate upward as their needs warrant.

### 3.1 ta — File-Based Substrate

`ta` (https://github.com/evanmschultz/ta — pre-release at time of writing) provides structured access to TOML and Markdown files via CLI + MCP. The cascade default schema (`examples/schemas/cascade.toml`) declares record types for drops / planners / droplets / QA / failures / discussions / plans / project. Records live in TOML files under `<project>/.ta/cascade/`. CRUD via `ta` CLI or `mcp__ta__*` MCP tools.

`ta init` selects defaults à la carte from categorized libraries (binary-embedded `examples/{schemas,agents,configs,docs-templates}/` plus the user's `~/.ta/` parallel structure). Selections land at canonical project destinations — schemas merge into `<project>/.ta/schema.toml`, agents land at `<project>/.claude/agents/`, etc. No nested template subdirs.

LLM clients (Claude Code, Codex CLI) read the cascade nodes through ta's MCP, spawn subagents using their built-in agent-spawn tools, and write back results through ta. ta does not dispatch agents itself — it is the data layer; the client is the orchestrator.

Suited to single-user / small-team workflows where the working tree is the source of truth and dispatch is human/LLM-driven.

ta's interactive TUI (bubbletea-based picker / form / confirm / menu) ships with VHS recordings under `cmd/ta/testdata/vhs/`. Two flows most relevant to a cascade-managed dev loop:

![`ta create` interactive form walking through required fields when creating a cascade record](../cmd/ta/testdata/vhs/form_create.gif)

`ta create` (without all required fields) drops into the interactive form — the surface a human operator uses to materialize cascade.drop / cascade.planner / cascade.droplet / cascade.qa\_\* records when the LLM client is not driving record creation through MCP.

![`ta init` multi-category picker with `space` toggling every visible leaf in a category group](../cmd/ta/testdata/vhs/picker_select_all.gif)

The multi-category picker (`ta init` and friends) is how a project bootstraps the cascade defaults — agents, instructions, skills, and schema fragments — into a target checkout in one pass. `space` on a group header toggles every visible leaf, which keeps the bootstrap flow fast even when the category tree gets large.

See the ta repo README "TUI demos" section for the full per-tape index.

### 3.2 Tillsyn — Runtime Substrate With Headless Dispatch

Tillsyn (https://github.com/evanmschultz/tillsyn — in development) provides a runtime substrate with a headless dispatcher. Cascade nodes are SQLite rows accessed via MCP. The dispatcher spawns subagents non-interactively per the blocker graph + descent gates, enforces state-transition rules at the runtime layer, and runs auto-spawn on template-defined `child_rules`.

Suited to teams + multi-orchestrator setups where the runtime needs to enforce gates the LLM clients alone cannot, and where the cost of manual orchestration outweighs the cost of running a dispatcher.

### 3.3 Plain Markdown + Claude Code / Codex Subagents

Pre-tooling fallback. Cascade nodes as Markdown files under a project's workflow directory. Orchestration manual: the LLM client spawns subagents via its built-in Agent tool, reads/writes files directly. No type validation, no automatic blocker enforcement — the discipline is human/LLM-enforced.

Useful for prototyping the methodology before adopting `ta` or Tillsyn, and for very small teams where the structured-record overhead isn't worth it.

### 3.4 Choosing a Substrate

| Need                                                | Recommended substrate         |
|-----------------------------------------------------|-------------------------------|
| Quick prototype of the methodology                  | Plain Markdown + LLM client   |
| Structured records, surgical edits, MCP-callable    | ta                            |
| Headless dispatch, runtime gate enforcement         | Tillsyn (when ready)          |
| Migration path                                      | Markdown → ta → Tillsyn       |

All three substrates store the same canonical shape. Migration is possible because the methodology defines the shape — substrates implement storage and dispatch.

## 4. Canonical Node Shape

Implementation-agnostic field specification. Every cascade node carries some subset of these fields; the substrate stores them in its native form (TOML records in ta; SQLite rows in Tillsyn; Markdown frontmatter in plain-MD setups).

### 4.1 NodeBase (every node)

- `id` — substrate-assigned record identifier.
- `parent_id` — parent node id, or empty for roots.
- `title` — short headline.
- `description` — Markdown prose.
- `state` — `todo` | `in_progress` | `complete` | `failed` | `archived`.
- `outcome` — `success` | `failure` | `blocked` | empty.
- `priority` — `low` | `medium` | `high`.
- `labels` — free-form tag strings.
- `blockers` — node ids that must reach `complete` before this node starts.
- `depends_on` — soft dependencies (referenced, not strictly blocking).
- `related_items` — cross-reference ids.
- `created_at`, `updated_at`, `started_at`, `completed_at`, `archived_at` — timestamps.
- `created_by_actor`, `updated_by_actor` — actor identities.
- `created_by_type`, `updated_by_type` — `user` | `agent` | `system`.
- `comments` — embedded thread (id, author, role, timestamp, Markdown body).

### 4.2 ActionItem (extends NodeBase)

- `role` — closed enum: `builder` | `qa-proof` | `qa-falsification` | `qa-a11y` | `qa-visual` | `design` | `commit` | `planner` | `research`.
- `structural_type` — `drop` | `segment` | `confluence` | `droplet` | empty (for non-cascade containers).
- `persistent` — bool, retained anchor items.
- `dev_gated` — bool, requires human reviewer approval to transition state.
- `owner` — actor identity gating state transitions.
- `due_at` — optional deadline.
- `paths`, `packages`, `files` — edit-scope and reference-scope file globs.
- `start_commit`, `end_commit` — git diff anchors.
- `objective`, `acceptance_criteria`, `definition_of_complete`, `validation_plan` — Markdown specs.
- `implementation_notes_user`, `implementation_notes_agent`, `transition_notes`, `blocked_reason`, `risk_notes` — Markdown.
- `command_snippets`, `expected_outputs`, `decision_log` — string arrays.
- `context_blocks` — array of `{kind, ref, label, body, importance}` carrying code locations / URLs / doc refs / Context7 references — whatever context the agent needs.
- `resource_refs` — array of external resources.
- `attempt_count`, `blocked_retry_count`, `last_failure_context` — retry tracking.
- `completion_contract` — structured contract: `start_criteria`, `completion_criteria`, `completion_checklist` (each is an array of checklist items), `completion_evidence`, `completion_notes`, `require_children_complete` policy flag.

### 4.3 Concrete Types

- **drop** — L1 cascade root, `structural_type = "drop"`, has a `drop_number`.
- **planner** — interior, `structural_type ∈ {"segment", "confluence"}`.
- **droplet** — leaf, `structural_type = "droplet"`, `irreducible` flag.
- **qa_proof / qa_falsification** — QA records with `target_id` pointing at the action item being verified/attacked.
- **failure** — captured failure artifact with `failure_kind`, `diagnostic`, `fix_directive`.
- **plan / discussion** — pre-cascade nodes with empty `structural_type`. Lifecycle reuses the same state enum.
- **project** — single-record project metadata with onboarding fields (mission, vocabulary, language, build_tool, standards_markdown_id etc.).

## 5. Benchmarking

The eventual blog/article treatment includes empirical comparisons. Planned benchmarks:

- **Baseline**: single-agent, single-prompt, end-to-end coding — the "monolithic agent" control.
- **Baseline-plus**: single-agent with semi-formal-reasoning certificate (the Section 0 loop). This is the *current* setup without the cascade.
- **Cascade**: multi-level planner-tree + droplets + auto-QA gates + parallel dispatch.

Evaluation framework: arxiv 2603.01896 (*Ugare & Chandra, "Agentic Code Reasoning,"* Meta, 4 Mar 2026) provides the patch-equivalence benchmark shape and reasoning-certificate baseline metrics. We adopt its primary measure (patch-equivalence rate vs. ground truth on standard coding benchmarks — SWE-bench-class) and extend with cascade-specific metrics (§6).

## 6. Metrics & Instrumentation

### 6.1 Per-Droplet

- Build-green rate — percentage that pass build+test on first builder attempt.
- Builder-retry count.
- Planner-edit count between attempts.
- Actual LOC delta vs. ~80 LOC target.
- Actual file count vs. ≤3 file ceiling.
- Builder model + time-to-completion + token cost.

### 6.2 Per-Planner-Node

- Plan-QA pass rate on first shot.
- Plan-QA round count if revised.
- Build-QA pass rate.
- Droplet count per planner — over- vs. under-decomposition signal.

### 6.3 Per-Drop

- Total cost by model tier.
- Total time-to-completion.
- Re-QA frequency — how often does ancestor re-QA fire? Signals plan-quality at the top.
- Parallelism extraction rate — actual parallel spawns ÷ theoretical maximum the blocker graph permits.
- Blocker-cycle detection count.
- Path/package conflict count — missing `blockers` between siblings that share scope.

### 6.4 Comparative

- Cascade vs. baseline-plus: patch-equivalence rate and cost-per-drop on matched workloads.
- Cascade vs. monolithic: same.
- Model-tier ablations: builder sonnet vs. haiku once haiku becomes a candidate.

Instrumentation location: each node's completion comment plus a ledger-style summary record per drop. Substrate-specific: ta surfaces this through structured records; Tillsyn through metric tables.

## 7. Open Questions

- **Q1.** When does a team graduate from plain Markdown to ta to Tillsyn? Heuristic: graduate to ta when search across plans starts consuming meaningful time; graduate to Tillsyn when manual dispatch errors cost more than running a dispatcher.
- **Q2.** Metrics retention format. Substrate-defined; ta uses structured ledger records, Tillsyn uses runtime metric tables.

## 8. References

- arxiv 2603.01896 — Ugare & Chandra, *"Agentic Code Reasoning,"* Meta, 2026-03-04. Source for the reasoning-certificate baseline and the patch-equivalence benchmark shape.
- The cascade methodology described in `CASCADE_METHODOLOGY.md` is one practical realization of the certificate-driven reasoning approach across multiple agents and levels.

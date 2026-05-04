# Agent Cascade — Methodology

A multi-level approach to planning, building, gating, and auditing work
across nested LLM agents. App-agnostic: describes the methodology, not
its implementation. Substrates that can host the methodology — `ta`,
Tillsyn, plain Markdown + Claude Code subagents, Codex CLI, etc. — are
named in §10 Reference Implementations.

This document is the seed for an article describing how to run a
multi-level coding cascade with off-the-shelf LLM clients before
dedicated headless dispatchers are widely available.

---

## 1. Thesis

The cascade is only a *cascade* if every node decomposes into
progressively smaller nodes until the work is an **atomic droplet** —
the smallest unit a builder agent can finish in one shot, touching one
file (or a tight cluster of one file + its tests), landing a few blocks
of real change.

A flat workflow — one planner laying out N units, each unit 1-4 files,
one builder per unit — is not a cascade. It is a more-expensive,
better-structured semi-formal-reasoning loop. The cascade earns its
name only when:

1. **Planners call planners.** A level-1 planner writes directives for
   level-2 planners, which spawn level-3 planners, and so on until the
   leaves are droplets.
2. **Descent gates on plan-QA.** A level never descends until its own
   plan-QA twins pass. A bad plan at level 1 is caught before level 2
   spawns.
3. **Leaves are focused.** Builder work is droplet-sized — one file +
   its tests, a few blocks of change. Cheap enough to retry on failure,
   small enough that a failure doesn't invalidate a whole drop's
   context.
4. **Failure is local.** A broken droplet invalidates one droplet's
   worth of context, not a whole drop. The planner above it patches the
   specific droplet and retries.
5. **Audit is immortal.** Every edit a planner makes to a failed or
   in-flight droplet is retained. The final droplet at completion may
   not match its first draft — both must be inspectable.

Granularity is not the goal. **Bounded blast radius + parallel
extraction + retry-cheap leaves + reviewable audit trail** are the
goals. Granularity is the enabler.

---

## 2. Droplet — The Atomic Unit

A **droplet** is the cascade's atomic build action item. The smallest
unit a dispatcher (or LLM client orchestrating manually) will assign to
a builder agent.

### 2.1 Shape

- **Target**: one source file + its co-located test file. A few blocks
  of real change. Typically ≤ ~80 LOC net, excluding imports and test
  setup.
- **Soft ceiling**: ~200 LOC net change and ~3 files. Not a hard wall.
  The planner uses these as guidance; if a droplet would blow past, the
  planner first tries to decompose, and if that genuinely doesn't work,
  marks the droplet with a justification and asks the orchestrator for
  permission to keep it oversized.
- **Per-action-item-type configuration**: project-specific schemas can
  override droplet ceilings per role/kind (a SQL migration droplet vs.
  a TUI component droplet vs. a unit test droplet may each warrant
  different defaults).
- **Required metadata** on every droplet:
  - `paths []string` — exact file globs the droplet writes to.
  - `packages []string` — packages the droplet compiles under.
  - `blockers []string` — sibling/ancestor node ids this one waits on.
  - `acceptance_criteria` — behavior-level assertions, not
    implementation sketch.
  - `role` — `builder` for the standard case; `qa-proof` /
    `qa-falsification` etc. for QA-lane droplets.

### 2.2 Droplets Are Sub-Package

**Droplets are smaller than a single compile/test unit.** A package
that holds 25 files holds many droplets. Two droplets targeting
different files in the same package share one compile + one test run.

- **Droplets**: sub-package, file-scoped or tight-file-cluster-scoped.
- **Planner nodes**: at package level and above (one planner per
  package as the baseline).
- **LLM QA (plan-QA + build-QA)**: at package level and above. There
  is no LLM QA below the package.
- **Automated QA (build+test gate)**: at package level — one pass
  covers every droplet that targeted that package.
- **Droplets sharing a package**: serialize with explicit `blockers`
  between them. A package is one compile unit; parallel builders on the
  same package would trip over each other's test runs.

### 2.3 Irreducible Droplets

Some droplets cannot be split: a single function-signature change
rippling through one file, a single SQL migration, a single template
edit. The planner marks these `irreducible: true` and plan-QA validates
the claim. Planners default to decompose; irreducibility is the
exception, not an escape hatch.

---

## 3. Roles & Models

Recommended starting bindings during the hardcoded phase. Configurable
per-path and per-action-item type once the team has data.

| Role                     | Model   | Edits Code? | Scope                                          |
|--------------------------|---------|-------------|------------------------------------------------|
| Planner                  | sonnet  | No          | Writes directives for children, authors plans  |
| Plan-QA (proof)          | opus    | No          | Verifies evidence completeness of plan         |
| Plan-QA (falsification)  | opus    | No          | Attacks plan for missed cases / bad blockers   |
| Builder (droplet)        | sonnet  | **Yes**     | Implements one droplet                         |
| Build-QA (proof)         | opus    | No          | Verifies completed sub-tree against acceptance |
| Build-QA (falsification) | opus    | No          | Attacks completed sub-tree for integration gaps|
| Commit agent             | haiku   | No          | Generates commit messages after gates pass     |

**Rationale**:

- Planning + QA is where judgment concentrates — opus for all QA,
  sonnet for planners. Cost flows to where judgment matters.
- Builders run sonnet during early dogfood. Track build-green rate per
  droplet and per builder model (§13 metrics); promote/demote based on
  data.
- Haiku is the cheapest tier; reserved for scripted-templated work like
  commit messages where the surface is narrow.

---

## 4. QA Placement

Three QA surfaces. **None run at the droplet level.**

### 4.1 Package-Level Build+Test (Automated, Not LLM)

Every package that received droplet edits runs one build+test pass
after **all droplets targeting that package** have reported complete.
No LLM. No judgment. Pass/fail is deterministic.

- **Pass**: package is green. The enclosing planner's build-QA (§4.2)
  runs next.
- **Fail**: enclosing planner ingests the failure output, identifies
  which droplet(s) caused it, writes fix directives, sets them back to
  in_progress. Siblings already green do not re-build. Repeat until
  green.

### 4.2 Planner-Level Build-QA (LLM, Proof + Falsification)

Once all direct children of a planner are complete AND their package
build+test gates are green (§4.1), the planner's **build-QA twins** run:

- `qa-proof` — verifies the claimed behavior of the completed sub-tree
  is supported by the actual diff + tests.
- `qa-falsification` — attacks the completed sub-tree for integration
  gaps, contract drift, missing edge-case coverage.

Twins run in parallel. Both must pass before the planner reports
complete to its parent.

### 4.3 Plan-QA (On Every Planner Node)

When any planner node is created (at any level), two plan-QA children
auto-spawn: `qa-proof` and `qa-falsification`. Both blocked-by the
planner's output (the plan).

- **Before descent.** The planner cannot spawn its child planners (or
  child droplets) until both plan-QA twins pass.
- **Parallel within a node.** Proof and falsification run concurrently
  against the same plan output.

### 4.4 Second Plan-QA Sweep — Global L1 Re-Check

When the plan-building pass reaches the leaves AND the total tree depth
under any L1 drop is ≥ 3, a **second plan-QA pass** runs with full
visibility into the constructed tree rooted at that L1. It checks:

- Blocker graph is acyclic.
- No two sibling droplets share a `paths` or `packages` entry without
  an explicit `blockers` linkage.
- Acceptance criteria at the leaves actually compose into the L1 drop's
  stated outcome.
- No orphan droplets — every droplet leads to the drop's outcome.

The depth threshold is a project-tunable starting value.

### 4.5 Test-scope Isolation — Agents Verify Only Their Slice

Cascade nodes share one checkout on disk. While siblings run concurrently, the working tree carries WIP from every active builder. Running the entire module's test suite at any sub-module node gives a verdict muddied by other agents' edits. Each agent's verification MUST scope down to what its slice owns:

- **Below-package agents** (single-file or tight-cluster droplet builds) run a name-pattern test invocation — the build runner exposes a "run only these test functions" target. In ta's reference implementation that's `mage testFunc <pattern>` (one or several joined into a `|`-regex like `mage testFunc 'TestA|TestB|TestC'`), optionally narrowed by package via `TA_TEST_PKG=<path>`. The runner translates to `go test -run <pattern> <pkg>`. The agent's verdict reports only its own functions; sibling WIP outside that scope is invisible.
- **Package-level agents** (segment / confluence work that owns one package) run the full package: `mage testPkg <path>` / `go test <pkg>`. Verdict reflects the package end-to-end.
- **Orchestrator / drop-close** runs the full module: `mage check` / `go test ./...`. This is the integration verdict — the only level where cross-package interactions are tested.

The same discipline applies to the build runner equivalents in other languages (Cargo workspaces with `cargo test -p <crate> <name>`, npm scripts with `vitest run -t <name>`, etc.). The principle is universal: **tests scope to the slice; QA escalates one level up; integration is the orchestrator's responsibility.**

This pairs with §4.4's pre-QA LSP refresh — a fresh LSP plus a slice-scoped test run gives a clean evidence layer for the QA agent to reason against, without other agents' in-flight work corrupting either side.

### 4.6 Why No Droplet-Level LLM QA

Droplets are too small to QA meaningfully in isolation. Correctness at
the droplet level is either trivially satisfied against the acceptance
criteria or obviously wrong — automated build+test catches the second
case. LLM QA at this level pays full cost for near-zero signal. QA
moves up to where integration actually happens.

### 4.7 Pre-QA LSP Refresh — Universal Discipline

Build agents edit code on disk. The next QA agent spawned reads from
disk via the language server (gopls for Go, tsserver for TypeScript,
pylsp for Python, rust-analyzer for Rust, and so on). The LSP daemon's
workspace cache may lag behind the build agent's writes — leading the
QA agent to flag "undefined: X" or "import not found" for symbols the
build runner (mage / cargo / npm / etc.) confirms exist. The QA agent
then ships false counterexamples grounded in stale state.

**Rule**: refresh the active LSP daemon BEFORE spawning a QA agent.
The build runner stays the authoritative gate for code correctness;
the LSP refresh ensures the QA agent's evidence-gathering layer
matches that truth instead of trailing it.

**Implementation pattern (universal)**: a host-level pre-spawn hook
that recycles the active LSP daemon when the spawned agent is a QA
variant (typically matched by `subagent_type` containing `qa-proof`
or `qa-falsification`). The daemon respawns on the next LSP request
with a fresh workspace index. The hook is language-agnostic in
*concept* — implementation differs per LSP server.

**Authoritative gate stays the build runner**, never the LSP. Never
trust LSP diagnostics over a passing build runner. The LSP refresh
is a UX polish on the QA agent's evidence layer, not a substitute
for the build gate.

---

## 5. Nesting Model

### 5.1 Template-Enforced Recursion

Nesting is **required by the template**, not left to planner judgment.
The template encodes:

- Every planner node at depth `d < max_depth` writes directives for
  child planners OR for droplets, never both in the same node.
- Every droplet node is a leaf. A droplet never has children.
- Descent at any level gates on that level's plan-QA twins (§4.3).

### 5.2 Cycle — Down Then Up

The cascade constructs top-down and completes bottom-up.

**Down**: L1 planner → plan-QA pass → spawn L2 planners → each L2
plan-QA pass → spawn L3 planners or droplets → ... until every branch
terminates in droplets.

**Up**: droplets complete → package-level build+test green (§4.1) →
parent planner's build-QA twins pass (§4.2) → parent planner reports
complete to its parent → that parent's build-QA twins pass → ... until
the L1 drop's own build-QA passes and the drop reports complete.

### 5.3 Depth Is Driven By Domain, Not A Constant

There is no "cascade must be N levels deep" rule. A trivial drop may
stay at depth 2 (drop → droplets, if only one package is touched). A
complex feature release may reach depth 5. The template rule is: **if a
planner's output contains more than one package OR more than one
distinct domain concern OR more than ~10 droplets, it must decompose
into child planners instead of emitting droplets directly**.

---

## 6. Failure Handling

### 6.1 `failed` Is A State, Not A Column

The cascade state model carries: `todo`, `in_progress`, `complete`,
`failed`, `archived`. Failed nodes remain in their original tree
position; they are not moved into a separate "failed" lane.

### 6.2 Visualization Rules

- Failed items remain in their original position in the tree.
- Rendered with a distinct visual marker (e.g. red, error glyph).
- Trigger a warning notification in any UI surface.
- Parent nodes render a "has failed descendant" indicator so the
  operator can jump to the failure without expanding every branch.

### 6.3 Planner Edits In Place

When a droplet fails and its package build+test goes red:

1. The enclosing planner node moves to `in_progress` if it had
   advanced.
2. The planner ingests failure output.
3. The planner edits the failed droplet's acceptance, directives,
   `paths`, or splits it into two droplets.
4. The failed droplet transitions `failed` → `in_progress`; the builder
   re-runs.
5. Siblings that succeeded stay complete. They do not rerun unless
   their `paths` or `packages` intersect with the edited droplet — in
   which case the plan-time `blockers` should already have serialized
   them (§7).

---

## 7. Blocker Failure — Re-QA Invariant

When a node `A` fails and gets edits from its planner, the edits may
change assumptions `A`'s ancestors relied on. After `A` finally
completes, the cascade runs mandatory re-QA sweeps.

### 7.1 Ancestor Re-QA (Primary)

Every ancestor planner from `A`'s parent up to the L1 drop re-runs its
**build-QA twins** (both proof + falsification). The twins verify the
ancestor's claimed outcome still holds given `A`'s revised behavior.

**Scope**: all the way up to L1. Not pruned at package boundaries —
even ancestors that share no `paths`/`packages` with the edited droplet
run build-QA re-check, because ancestor planners' **plans** may have
been written against `A`'s original output.

### 7.2 Dependent Re-QA (Edge Case)

Nodes `D` whose `blockers` include `A` and have already completed
re-run their build-QA twins once `A` finally passes.

**This case should be rare.** Under correct blocker semantics, `D`
should not have reached completion while `A` was in `failed` or
post-failure-edit states — `D` would have been blocked from starting.
The case opens only when:

- `A` initially completed successfully.
- `D` started and ran against `A`'s output.
- `A`'s ancestor re-QA (§7.1) later found `A` incorrect, reopening
  `A`.
- `A` got edited, completed again with different behavior.

In that narrow window, `D` already used `A`'s old output; `D`'s
build-QA twins must re-verify against `A`'s new output.

### 7.3 Parallel Sibling Non-Invalidation

Siblings `B` with no blockers linkage to `A` — parallel by construction
— do not re-run QA when `A` is edited. They share neither paths nor
packages with `A` (enforced at plan-QA time, §4.4), so their
correctness is independent of `A`'s revised behavior.

### 7.4 Cost Acceptance

Re-QA cost is real. It is the price of in-place planner edits with
audit retention instead of throwing away and rebuilding. The cascade
accepts the cost because the alternative — full-subtree rebuild on
every failure — is strictly more expensive.

---

## 8. Audit Trail

Every edit a planner makes to an in-flight or failed action item must
be retained. The history is inspectable — operators look back at "what
was the first draft of this droplet versus what actually shipped."

### 8.1 What Must Be Retained

- Every write to `description`, `acceptance_criteria`, `paths`,
  `packages`, `blockers`, `directives`, or any planner-editable field.
- Every state transition with timestamp and transitioning actor.
- Every comment (append-only by construction).

### 8.2 Storage Strategy

Implementation-defined. A simple snapshot-per-edit approach works for
most teams: every write stores the full node snapshot. Storage scales
linearly with edit-count × node-size. Optimize to diff-per-edit only
if dogfood data shows the snapshot approach bounds out unacceptably.

---

## 9. Cascade Tree — Side-By-Side Example

Two L1 drops under one project. `DROP_0` blocks `DROP_1`. `DROP_0` is a
domain-parallel scaffold (no shared packages, no shared paths).
`DROP_1` is a sequential feature release (each L3 package consumes the
package below via `blockers`).

Legend:
- `P` = planner (sonnet). Plan-QA twins (opus) implicit on every `P`.
- `BQ` = build-QA twins (opus) at a planner.
- `•` = droplet (sonnet builder). Package build+test gate implicit per
  package.
- `═══▶` = cross-drop or intra-drop `blockers`.

```
════════════════════════════════════════════════════════════════════════════════
DROP 0 — PLATFORM SCAFFOLD            ║  DROP 1 — AUTH FEATURE RELEASE
3 depths, package-parallel            ║  4 depths, package-sequential
                                      ║  blockers: DROP 0  (cross-drop)
════════════════════════════════════════════════════════════════════════════════

L1  DROP 0  (P + plan-QA)             ║  L1  DROP 1  (P + plan-QA)
     │                                 ║       │
     │                                 ║       L2  auth-feature-strategy
     │                                 ║            (P + plan-QA)
     │                                 ║            plans package order
     │                                 ║            │
     ├─ L2  pkg-logger     ──┐         ║            │
     │   (P + plan-QA)       │         ║            ├─ L3  pkg-user-entity
     │   │                   │         ║            │   (P + plan-QA)
     │   └─ L3 droplets:     │ parall  ║            │   │
     │      • pkg-scaffold   │ no pkg  ║            │   └─ L4 droplets:
     │      • config-bind    │ overlap ║            │      • user-struct
     │      • unit-tests     │ no path ║            │      • password-hash
     │   BQ twins            │ overlap ║            │   BQ twins
     │                       │         ║            │
     ├─ L2  pkg-config     ──┤         ║            ├─ L3  pkg-auth-service
     │   (P + plan-QA)       │         ║            │   (P + plan-QA)
     │   │                   │         ║            │   [blockers: pkg-user-entity]
     │   └─ L3 droplets:     │         ║            │   │
     │      • TOML-parser    │         ║            │   └─ L4 droplets:
     │      • validator      │         ║            │      • verify-password
     │      • defaults       │         ║            │      • token-issuance
     │   BQ twins            │         ║            │      • ratelimit-hook
     │                       │         ║            │   BQ twins
     └─ L2  pkg-storage    ──┘         ║            │
         (P + plan-QA)                 ║            └─ L3  pkg-http-adapter
         │                             ║                (P + plan-QA)
         └─ L3 droplets:               ║                [blockers: pkg-auth-service]
            • schema-ddl               ║                │
            • migrations               ║                └─ L4 droplets:
            • conn-pool                ║                   • POST-/login
         BQ twins                      ║                   • POST-/logout
                                       ║                BQ twins
  (L1 BQ twins roll up L2s)            ║
                                       ║       (L2 BQ twins roll up L3s)
                                       ║  (L1 BQ twins roll up L2)

             │
             │ ═══[blocks]═══▶  DROP_1
             │
```

### 9.1 Parallelism Read

- **DROP_0** L2 children (`logger`, `config`, `storage`) have no shared
  `packages` and no shared `paths`. The dispatcher (or LLM client
  orchestrating manually) can spawn their planners in parallel, their
  droplets in parallel within each package, and QA twins in parallel.
- **DROP_1** L3 chain (`user-entity → auth-service → http-adapter`) is
  strict `blockers`. The dispatcher must serialize. Within each L3,
  droplets targeting that single package serialize via implicit
  intra-package `blockers` — they share compile (§2.2).

### 9.2 Plan-Time Blocker Rules (Validated by Plan-QA §4.4)

- Every sibling pair with overlapping `paths` has an explicit
  `blockers` linkage.
- Every sibling pair with overlapping `packages` has an explicit
  `blockers` linkage.
- No blocker cycles anywhere in the tree.

---

## 10. Reference Implementations

The methodology described above is implementation-agnostic. The same
canonical node shape (id, role, structural_type, state, blockers,
paths, packages, completion_contract, comments — see §11 for the field
specification) can be stored and orchestrated through different
substrates. A team can begin with the lightest substrate and graduate
upward as their needs warrant.

### 10.1 ta — File-Based Substrate

`ta` (https://github.com/evanmschultz/ta — pre-release at time of
writing) provides structured access to TOML and Markdown files via
CLI + MCP. The cascade default schema (`examples/schemas/cascade.toml`)
declares record types for drops / planners / droplets / QA / failures
/ discussions / plans / project. Records live in TOML files under
`<project>/.ta/cascade/`. CRUD via `ta` CLI or `mcp__ta__*` MCP tools.

`ta init` selects defaults à la carte from categorized libraries
(binary-embedded `examples/{schemas,agents,configs,docs-templates}/`
plus the user's `~/.ta/` parallel structure). Selections land at
canonical project destinations — schemas merge into
`<project>/.ta/schema.toml`, agents land at `<project>/.claude/agents/`,
etc. No nested template subdirs.

LLM clients (Claude Code, Codex CLI) read the cascade nodes through
ta's MCP, spawn subagents using their built-in agent-spawn tools, and
write back results through ta. ta does not dispatch agents itself — it
is the data layer; the client is the orchestrator.

Suited to single-user / small-team workflows where the working tree is
the source of truth and dispatch is human/LLM-driven.

### 10.2 Tillsyn — Runtime Substrate With Headless Dispatch

Tillsyn (https://github.com/evanmschultz/tillsyn — in development)
provides a runtime substrate with a headless dispatcher. Cascade nodes
are SQLite rows accessed via MCP. The dispatcher spawns subagents
non-interactively per the blocker graph + descent gates, enforces
state-transition rules at the runtime layer, and runs auto-spawn on
template-defined `child_rules`.

Suited to teams + multi-orchestrator setups where the runtime needs to
enforce gates the LLM clients alone cannot, and where the cost of
manual orchestration outweighs the cost of running a dispatcher.

### 10.3 Plain Markdown + Claude Code / Codex Subagents

Pre-tooling fallback. Cascade nodes as Markdown files under a
project's workflow directory. Orchestration manual: the LLM client
spawns subagents via its built-in Agent tool, reads/writes files
directly. No type validation, no automatic blocker enforcement — the
discipline is human/LLM-enforced.

Useful for prototyping the methodology before adopting `ta` or
Tillsyn, and for very small teams where the structured-record overhead
isn't worth it.

### 10.4 Choosing a Substrate

| Need                                                | Recommended substrate         |
|-----------------------------------------------------|-------------------------------|
| Quick prototype of the methodology                  | Plain Markdown + LLM client   |
| Structured records, surgical edits, MCP-callable    | ta                            |
| Headless dispatch, runtime gate enforcement         | Tillsyn (when ready)          |
| Migration path                                      | Markdown → ta → Tillsyn       |

All three substrates store the same canonical shape. Migration is
possible because the methodology defines the shape — substrates
implement storage and dispatch.

---

## 11. Canonical Node Shape

Implementation-agnostic field specification. Every cascade node carries
some subset of these fields; the substrate stores them in its native
form (TOML records in ta; SQLite rows in Tillsyn; Markdown frontmatter
in plain-MD setups).

### 11.1 NodeBase (every node)

- `id` — substrate-assigned record identifier.
- `parent_id` — parent node id, or empty for roots.
- `title` — short headline.
- `description` — Markdown prose.
- `state` — `todo` | `in_progress` | `complete` | `failed` | `archived`.
- `outcome` — `success` | `failure` | `blocked` | empty.
- `priority` — `low` | `medium` | `high`.
- `labels` — free-form tag strings.
- `blockers` — node ids that must reach `complete` before this node
  starts.
- `depends_on` — soft dependencies (referenced, not strictly blocking).
- `related_items` — cross-reference ids.
- `created_at`, `updated_at`, `started_at`, `completed_at`,
  `archived_at` — timestamps.
- `created_by_actor`, `updated_by_actor` — actor identities.
- `created_by_type`, `updated_by_type` — `user` | `agent` | `system`.
- `comments` — embedded thread (id, author, role, timestamp,
  Markdown body).

### 11.2 ActionItem (extends NodeBase)

- `role` — closed enum: `builder` | `qa-proof` | `qa-falsification` |
  `qa-a11y` | `qa-visual` | `design` | `commit` | `planner` |
  `research`.
- `structural_type` — `drop` | `segment` | `confluence` | `droplet` |
  empty (for non-cascade containers).
- `persistent` — bool, retained anchor items.
- `dev_gated` — bool, requires human reviewer approval to transition
  state.
- `owner` — actor identity gating state transitions.
- `due_at` — optional deadline.
- `paths`, `packages`, `files` — edit-scope and reference-scope file
  globs.
- `start_commit`, `end_commit` — git diff anchors.
- `objective`, `acceptance_criteria`, `definition_of_complete`,
  `validation_plan` — Markdown specs.
- `implementation_notes_user`, `implementation_notes_agent`,
  `transition_notes`, `blocked_reason`, `risk_notes` — Markdown.
- `command_snippets`, `expected_outputs`, `decision_log` — string arrays.
- `context_blocks` — array of `{kind, ref, label, body, importance}`
  carrying code locations / URLs / doc refs / Context7 references —
  whatever context the agent needs.
- `resource_refs` — array of external resources.
- `attempt_count`, `blocked_retry_count`, `last_failure_context` —
  retry tracking.
- `completion_contract` — structured contract: `start_criteria`,
  `completion_criteria`, `completion_checklist` (each is an array of
  checklist items), `completion_evidence`, `completion_notes`,
  `require_children_complete` policy flag.

### 11.3 Concrete Types

- **drop** — L1 cascade root, `structural_type = "drop"`, has a
  `drop_number`.
- **planner** — interior, `structural_type ∈ {"segment", "confluence"}`.
- **droplet** — leaf, `structural_type = "droplet"`, `irreducible`
  flag.
- **qa_proof / qa_falsification** — QA records with `target_id`
  pointing at the action item being verified/attacked.
- **failure** — captured failure artifact with `failure_kind`,
  `diagnostic`, `fix_directive`.
- **plan / discussion** — pre-cascade nodes with empty
  `structural_type`. Lifecycle reuses the same state enum.
- **project** — single-record project metadata with onboarding fields
  (mission, vocabulary, language, build_tool, standards_markdown_id
  etc.).

---

## 12. Benchmarking

The eventual blog/article treatment includes empirical comparisons.
Planned benchmarks:

- **Baseline**: single-agent, single-prompt, end-to-end coding — the
  "monolithic agent" control.
- **Baseline-plus**: single-agent with semi-formal-reasoning
  certificate (the Section 0 loop). This is the *current* setup
  without the cascade.
- **Cascade**: multi-level planner-tree + droplets + auto-QA gates +
  parallel dispatch.

Evaluation framework: arxiv 2603.01896 (*Ugare & Chandra, "Agentic Code
Reasoning,"* Meta, 4 Mar 2026) provides the patch-equivalence benchmark
shape and reasoning-certificate baseline metrics. We adopt its primary
measure (patch-equivalence rate vs. ground truth on standard coding
benchmarks — SWE-bench-class) and extend with cascade-specific metrics
(§13).

---

## 13. Metrics & Instrumentation

### 13.1 Per-Droplet

- Build-green rate — percentage that pass build+test on first builder
  attempt.
- Builder-retry count.
- Planner-edit count between attempts.
- Actual LOC delta vs. ~80 LOC target.
- Actual file count vs. ≤3 file ceiling.
- Builder model + time-to-completion + token cost.

### 13.2 Per-Planner-Node

- Plan-QA pass rate on first shot.
- Plan-QA round count if revised.
- Build-QA pass rate.
- Droplet count per planner — over- vs. under-decomposition signal.

### 13.3 Per-Drop

- Total cost by model tier.
- Total time-to-completion.
- Re-QA frequency — how often does §7 ancestor re-QA fire? Signals
  plan-quality at the top.
- Parallelism extraction rate — actual parallel spawns ÷ theoretical
  maximum the blocker graph permits.
- Blocker-cycle detection count.
- Path/package conflict count — missing `blockers` between siblings
  that share scope.

### 13.4 Comparative

- Cascade vs. baseline-plus: patch-equivalence rate and cost-per-drop
  on matched workloads.
- Cascade vs. monolithic: same.
- Model-tier ablations: builder sonnet vs. haiku once haiku becomes a
  candidate.

Instrumentation location: each node's completion comment plus a
ledger-style summary record per drop. Substrate-specific: ta surfaces
this through structured records; Tillsyn through metric tables.

---

## 14. Open Questions

- **Q1.** When does a team graduate from plain Markdown to ta to
  Tillsyn? Heuristic: graduate to ta when search across plans starts
  consuming meaningful time; graduate to Tillsyn when manual dispatch
  errors cost more than running a dispatcher.
- **Q2.** Metrics retention format. Substrate-defined; ta uses
  structured ledger records, Tillsyn uses runtime metric tables.

---

## 15. References

- arxiv 2603.01896 — Ugare & Chandra, *"Agentic Code Reasoning,"* Meta,
  2026-03-04. Source for the reasoning-certificate baseline and the
  patch-equivalence benchmark shape.
- The cascade methodology described here is one practical realization
  of the certificate-driven reasoning approach across multiple agents
  and levels.

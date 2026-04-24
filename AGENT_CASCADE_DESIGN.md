# Agent Cascade — Design

Dated: 2026-04-18. Author: STEWARD, under dev direction in DROP_1.75 chat window.
Scope: the Tillsyn cascade — its granularity rules, role bindings, QA placement,
nesting model, failure handling, and the metrics we will gather to tune every
dial above.

**Status:** Draft design doc. This file is the seed for Tillsyn's public docs +
an eventual blog post / article describing how to run a multi-level coding
cascade with Markdown files and off-the-shelf subagents, before headless
agents are available. Benchmarking plan (§12) targets the framework used in
*Ugare & Chandra, "Agentic Code Reasoning,"* arxiv 2603.01896, extended with
cascade-specific metrics.

**Unified-for-now — split before MVP.** This single doc currently serves three
audiences: (a) internal pre-dogfood MD-only operations (what the dev + STEWARD
reference day-to-day during Drops 1.75 → 4 before the cascade dispatcher is
live), (b) the concept explanation for people outside Tillsyn who want to run
a similar cascade with plain Markdown + off-the-shelf subagents, and (c) the
source material for the future blog post / article. Keeping it as one file
during active design prevents drift between the conceptual and operational
halves — edits to one land alongside edits to the other. Before MVP public
release, split into `docs/cascade-concept.md` (public-facing, audiences b + c)
and `docs/cascade-operations.md` (internal, audience a). The split is tracked
as a refinement bullet in `PLAN.md` §19.10.

All concrete configuration values documented below are **starting points for
dogfood**. Every one of them is tagged for refinement in `PLAN.md` §19.10
(drop 10 refinement cleanup) so we tune with data, not guesses.

---

## 1. Thesis

The cascade is only a *cascade* if every drop decomposes into progressively
smaller drops until the work is an **atomic minion** — a droplet a builder
model can finish in one shot, touching one file (or a tight cluster of one
file + its tests), landing a few blocks of real change.

Today's dogfood drops are flat — one planner lays out ~6 units, each unit
is 1-4 files, one builder-per-unit. That is not a cascade. It is a
more-expensive, better-structured semi-formal-reasoning loop. The cascade
earns its name only when:

1. **Planners call planners.** A level-1 planner writes directives for
   level-2 planners, which spawn level-3 planners, and so on until the
   leaves are droplets.
2. **Descent gates on plan-QA.** A level never descends until its own
   plan-QA twins pass. A bad plan at level 1 is caught before level 2
   spawns.
3. **Leaves are focused.** Builder work is droplet-sized — one file + its
   tests, a few blocks of change. Cheap enough to retry on failure,
   small enough that a failure doesn't invalidate a whole drop's context.
4. **Failure is local.** A broken droplet invalidates one droplet's
   worth of context, not a whole drop. The planner above it patches the
   specific droplet and retries.
5. **Audit is immortal.** Every edit a planner makes to a failed or
   in-flight droplet is retained. The final droplet at completion may
   not match its first draft — both must be inspectable.

Granularity is not the goal. **Bounded blast radius + parallel extraction
+ retry-cheap leaves + reviewable audit trail** are the goals.
Granularity is the enabler.

---

## 2. Droplet — The Atomic Unit

A **droplet** is the cascade's atomic build actionItem. It is the smallest
unit the dispatcher will assign to a builder agent.

### 2.1 Shape (Soft Targets, Refine With Data)

- **Target**: one source file + its co-located test file. A few blocks
  of real change. Typically ≤ ~80 LOC net, excluding imports and test
  setup.
- **Soft ceiling**: ~200 LOC net change and ~3 files. Not a hard wall.
  The planner uses these as guidance; if a droplet would blow past,
  the planner should first try to decompose, and if that genuinely
  doesn't work, the planner marks the droplet with a justification
  and asks the orchestrator for permission to keep it oversized.
- **Per-actionItem-type config (refinement, `PLAN.md` §19.10)**:
  eventually these targets become template fields so dev can set
  ceilings per actionItem type (e.g., SQL migration droplet vs. TUI
  component droplet vs. unit test droplet may each want different
  defaults). Pre-dogfood defaults are the numbers above, tuned from
  real metrics as we go.
- **Metadata required on every droplet** (first-class Tillsyn fields
  post-Drop-1; worklog entries today):
  - `paths []string` — exact files the droplet writes to
  - `packages []string` — Go packages the droplet compiles under
  - `blocked_by []string` — sibling/ancestor droplets this one waits on
  - `acceptance` — behavior-level assertions, not implementation sketch
  - `role = builder`, `model = sonnet` (see §3; configurable later)

### 2.2 Droplets Are Sub-Package

**Droplets are smaller than a Go package.** A single package (e.g.,
`internal/domain`, which may hold 25 files) holds many droplets. Two
droplets targeting different files in the same package share one
compile and one `go test ./internal/domain/...` run.

Today's rule:

- **Droplets**: sub-package, file-scoped or tight-file-cluster-scoped.
- **Planner nodes**: at package level and above (one planner per
  package is the current baseline).
- **LLM QA (plan-QA + build-QA)**: at package level and above. There
  is no LLM QA below the package.
- **Automated QA (`mage ci`-class build+test)**: at package level —
  one pass covers every droplet that targeted that package.
- **Droplets sharing a package**: serialize with explicit `blocked_by`
  between them. A package is a single compile unit; parallel builders
  on the same package would trip over each other's `go test` runs.

**Future refinement** (`PLAN.md` §19.10): figure out how to nest
planners + QA *inside* a package — sub-package planners keyed on
file-clusters or feature-slices within one package. This would
unblock finer parallelism and tighter-scoped LLM QA for big packages.
Out of scope for initial dogfood; data from dogfood will tell us
whether it's worth building.

### 2.3 Irreducible Droplets

Some droplets cannot be split: a single function signature change
rippling through one file, a single SQL migration, a single template
edit. The planner marks these with `irreducible: true` and plan-QA
validates the claim. Planners default to decompose; irreducibility is
the exception, not the escape hatch.

(Addition flagged to dev; kept per approval.)

---

## 3. Role & Model Bindings

Fixed bindings during the hardcoded phase. Every binding below is
configurable by path + actionItem type in a refinement drop
(`PLAN.md` §19.10, §20.10). These are starting values for dogfood,
not permanent law.

| Role                     | Model   | Edits Code? | Scope                                          |
|--------------------------|---------|-------------|------------------------------------------------|
| Planner                  | sonnet  | No          | Writes directives for children, authors PLAN   |
| Plan-QA (proof)          | opus    | No          | Verifies evidence completeness of plan         |
| Plan-QA (falsification)  | opus    | No          | Attacks plan for missed cases / bad blockers   |
| Builder (droplet)        | sonnet  | **Yes**     | Implements one droplet                         |
| Build-QA (proof)         | opus    | No          | Verifies completed sub-tree against acceptance |
| Build-QA (falsification) | opus    | No          | Attacks completed sub-tree for integration gaps |
| Commit agent (Drop 11+)  | haiku   | No          | Generates commit messages after gates pass     |

**Rationale**:

- Planning + QA is where judgment concentrates — opus for all QA, sonnet
  for planners. Cost flows to where judgment matters.
- Builders run sonnet during initial dogfood. We track build-green rate
  per droplet and per builder model (§13 metrics); if sonnet over-performs
  and haiku becomes a candidate, we test and promote via refinement.
- Haiku's only binding today is the commit agent (arrives post-dogfood
  per `PLAN.md` §19.11 Drop 11). It does not run builder work in the
  initial cascade.

**Configuration open question** (filed as `PLAN.md` §20.10): how are
model bindings configured per path and per actionItem type? Template
field? Project-level settings? Per-drop override? Design deferred to
drop 10 refinement.

---

## 4. QA Placement

Three QA surfaces. None run at the droplet level.

### 4.1 Package-Level Build+Test (Automated, Not LLM)

Every Go package that received droplet edits runs one `mage ci`-equivalent
pass after **all droplets targeting that package** have reported
complete. No LLM. No judgment. Pass/fail is deterministic.

- **Pass**: package is green. The enclosing planner node's build-QA
  (§4.2) runs next.
- **Fail**: enclosing planner node ingests the failure output,
  identifies which droplet(s) caused it, writes fix directives to
  those specific droplets, sets them back to `in_progress`. Siblings
  already green do not re-build. Repeat until the package is green.

### 4.2 Planner-Level Build-QA (LLM, Proof + Falsification)

Once all direct children of a planner node are complete AND their
package build+test gates are green (§4.1), the planner node's
**build-QA twins** run:

- `build-qa-proof` (opus) — verifies the claimed behavior of the
  completed sub-tree is supported by the actual diff + tests.
- `build-qa-falsif` (opus) — attacks the completed sub-tree for
  integration gaps, contract drift, missing edge-case coverage.

Twins run in parallel. Both must pass before the planner node itself
reports complete up to *its* parent.

### 4.3 Plan-QA (On Every Planner Node)

When any planner node is created (at any level), the dispatcher
auto-creates two plan-QA children: `plan-qa-proof` and `plan-qa-falsif`,
both opus. Both are `blocked_by` the planner's output (the plan).

- **Before descent.** The planner node cannot spawn its child planners
  (or child droplets) until both plan-QA twins pass.
- **Parallel within a node.** Proof and falsification run concurrently
  against the same plan output.

### 4.4 Second Plan-QA Sweep — Global L1 Re-Check

When the plan-building pass reaches the leaves (final droplets written
into the tree) AND the total tree depth under any level-1 drop is
**≥ 3**, a **second plan-QA pass** runs with full visibility into the
constructed tree rooted at that level-1. It checks:

- Blocker graph is acyclic.
- No two sibling droplets share a `paths` or `packages` entry without
  an explicit `blocked_by`.
- Acceptance criteria at the leaves actually compose into the
  level-1 drop's stated outcome.
- No orphan droplets (every droplet leads to the drop's outcome).

**Threshold is hardcoded to depth ≥ 3 for now. Configurable in
`PLAN.md` §19.10 refinement** (starting value, adjusted once we
have dogfood data).

### 4.5 Why No Droplet-Level LLM QA

Droplets are too small to QA meaningfully in isolation. Correctness
at the droplet level is either trivially satisfied against the
acceptance criteria or obviously wrong — `mage ci` catches the second
case. LLM QA at this level pays full cost for near-zero signal. QA
moves up to where integration actually happens.

---

## 5. Nesting Model

### 5.1 Template-Enforced Recursion

Nesting is **required by the template**, not left to planner judgment.
The template encodes:

- Every planner node at depth `d < max_depth` writes directives for
  child planners OR for droplets, never both in the same node.
- Every droplet node is a leaf. A droplet never has children.
- Descent at any level gates on that level's plan-QA twins (§4.3).

Today this is a workflow-MD convention — `drops/WORKFLOW.md` and
`drops/_TEMPLATE/PLAN.md` encode it. Drop 3 (template system) promotes
it to Tillsyn `child_rules`. Drop 4 (dispatcher) enforces it at state
transitions.

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
complex feature release may reach depth 5. The template rule is: **if
a planner node's output contains more than one Go package OR more
than one distinct domain concern OR more than ~10 droplets, it must
decompose into child planners instead of emitting droplets directly**.

---

## 6. Failure Handling

### 6.1 `failed` Is A State, Not A Column

The cascade state model has `todo` / `in_progress` / `done`; Drop 1
adds `failed`. The earlier concern — "hidden in a 5th column" — was
a rendering worry, not a model worry. The state model is fine.

### 6.2 TUI Rendering Rules

- Failed items remain in their original position in the tree. Not moved.
- Rendered in **red**.
- Trigger a **warning notification** in the TUI notification surface.
- Parent nodes render a "has failed descendant" glyph so the operator
  can jump to the failure without expanding every branch.

Confirm TUI scope: this renderer work belongs to Drop 1.5 (TUI) or a
subsequent TUI refinement drop. See §11.

### 6.3 Planner Edits In Place

When a droplet fails and its package build+test goes red:

1. The enclosing planner node (the one whose children contain the
   failed droplet) moves to `in_progress` if it had advanced.
2. The planner ingests failure output.
3. The planner edits the failed droplet's acceptance, directives,
   `paths`, or splits it into two droplets.
4. The failed droplet transitions `failed` → `in_progress`; the builder
   re-runs.
5. Siblings that succeeded stay complete. They do not rerun unless
   their `paths` or `packages` intersect with the edited droplet — in
   which case the plan-time `blocked_by` should already have
   serialized them (§7 edge-case discussion).

---

## 7. Blocker Failure — Re-QA Invariant

When a node `A` fails and gets edits from its planner, the edits may
change the assumptions `A`'s ancestors relied on. After `A` finally
completes, the cascade runs mandatory re-QA sweeps.

### 7.1 Ancestor Re-QA (Primary)

Every ancestor planner node from `A`'s parent up to the L1 drop
re-runs its **build-QA twins** (both proof + falsification, opus).
The twins verify the ancestor's claimed outcome still holds given
`A`'s revised behavior.

**Scope**: all the way up to L1. Not pruned at package boundaries —
even ancestors that share no `paths`/`packages` with the edited droplet
run build-QA re-check, because ancestor planners' **plans** may have
been written against `A`'s original output, not just its code.

### 7.2 Dependent Re-QA (Edge Case)

Nodes `D` with `D.blocked_by` including `A` that have already completed
re-run their build-QA twins once `A` finally passes.

**This case should be rare.** Under correct `blocked_by` semantics,
`D` should not have reached completion while `A` was in `failed` or
post-failure-edit states — `D` would have been blocked from starting.
The case opens only when:

- `A` initially completed successfully.
- `D` started and ran against `A`'s output.
- `A`'s ancestor re-QA (§7.1) later found `A` incorrect, reopening `A`.
- `A` got edited, completed again with different behavior.

In that narrow window, `D` already used `A`'s old output; `D`'s
build-QA twins must re-verify against `A`'s new output.

### 7.3 Parallel Sibling Non-Invalidation

Siblings `B` with no `blocked_by` linkage to `A` — parallel by
construction — do not re-run QA when `A` is edited. They share neither
paths nor packages with `A` (enforced at plan-QA time, §4.4), so their
correctness is independent of `A`'s revised behavior.

### 7.4 Cost Acceptance

Re-QA cost is real. It is the price of in-place planner edits with
audit retention instead of throwing away and rebuilding. The cascade
accepts the cost because the alternative — full-subtree rebuild on
every failure — is strictly more expensive.

---

## 8. Audit Trail

Every edit a planner makes to an in-flight or failed actionItem must
be retained. The history is inspectable — operators look back at
"what was the first draft of this droplet versus what actually shipped."

### 8.1 What Must Be Retained

- Every write to `description`, `acceptance`, `paths`, `packages`,
  `blocked_by`, `directives`, or any planner-editable field.
- Every state transition with timestamp + transitioning principal.
- Every comment (already append-only today).

### 8.2 Storage — Option X (Full Snapshot Per Change)

Every write stores the full node JSON. Simple, robust, no
reconstruction logic. Storage cost scales linearly with
edit-count × node-size. Dogfood measures whether this bounds out
acceptably.

**YAGNI for now** on Options Y (diff-per-change) and Z (hybrid
snapshot-plus-diff). Defer until dogfood data says Option X doesn't
fit. Dev confirmed: "don't optimize too soon."

Filed as `PLAN.md` §19.10 refinement: evaluate diff-based storage or
hybrid if snapshot sizes become unwieldy.

### 8.3 Per-Drop Workflow Artifacts — On-Disk Audit Trail

The Tillsyn audit trail above captures planner-editable field history
and state transitions. The **on-disk** audit trail for per-drop work
lives under `main/workflow/drop_N/`, git-tracked on the drop branch,
flowing to `main` via the drop's PR merge.

**Rendering contract.** `main/workflow/drop_N/` mirrors
`workflow/example/` + `workflow/example/drops/_TEMPLATE/` shape
(atomic-small-things discipline — many small MDs, not monoliths).
Only edit workflow-process flow in this doc + the template itself;
`workflow/drop_N/` for any specific N is a rendering of that template
shape for drop N's work. See `PLAN.md` §15.9 for the per-drop flow.

**`failures/` subdir at each branched level of `drop_N/`.** Never
delete QA / plan / build artifacts. Failed QA, plan, or build content
moves into `failures/` at its parent level so the next iteration's
plan / QA files can learn from + count prior failures. Retention =
forever. **Forward-only** — no retroactive backfill for pre-2026-04-19
drops (dev directive).

**`_BLOCKERS.toml` at each branched planner dir.** Every dir with
more than one immediate child (sub-drop or droplet) carries a
`_BLOCKERS.toml` file declaring `blocked_by` between those immediate
children. Scope is **immediate children only** — cross-level blockers
ride on the parent-close-waits-for-child rule, not on
`_BLOCKERS.toml`. TOML chosen because tillsyn already stacks on
`pelletier/go-toml/v2`, the strict parser catches typos before they
rot, and `[[blockers]]` array-of-tables reads cleanly at a glance.
**Today (pre-runtime-dispatcher) this file is a coordination hint —
the orchestrator and planner consult it; discipline is
human/LLM-enforced.** **Tillsyn Drop 1 promotes `paths` /
`packages` to first-class `ActionItem` domain fields, enabling
structured blocker semantics (`blocked_by` is already a coordination
primitive pre-Drop-1).** **Tillsyn Drop 4 ships the runtime
dispatcher, which hard-refuses `in_progress` transitions while any
`blocked_by` entry is still unresolved — preventing work from
starting on items whose blockers have not cleared.** At that point
`_BLOCKERS.toml` becomes the one-shot migration source: each
`[[blockers]]` row loads directly into the target
`ActionItem.blocked_by`. **Migration is two-step:** (1) regenerate
the TOML from `PLAN.md` inline bullets so the two are guaranteed in
sync; (2) then load the TOML into the runtime. Never import the
TOML directly without the sync step — stale TOML could install a
regression the moment runtime enforcement turns on. The file
mirrors the inline `Blocked by:` bullets in each droplet's
`PLAN.md` row — `PLAN.md` is truth; `_BLOCKERS.toml` is the
aggregate view. If they disagree, regenerate the TOML from
`PLAN.md`.

**Refinements-gate ownership.** Every numbered level_1 drop carries a
STEWARD-owned refinements-gate item inside its own tree,
`DROP_N_REFINEMENTS_GATE_BEFORE_DROP_N+1` (see `PLAN.md` §15.8).
Drop-orch creates the gate at drop spin-up; STEWARD works it
post-merge; closing the gate unblocks drop N's level_1 close. The
gate's `workflow/drop_N/` mirror captures the dev ↔ STEWARD
conversation that produced the decisions, so future drops can read
the rationale trail.

**MD ownership split.** Drop-orch owns `workflow/drop_N/` content
writes on the drop branch + architecture-MD edits when scope touches
process. STEWARD post-merge on `main/` reads `workflow/drop_N/` and
splices into the six top-level MDs. See `PLAN.md` §15.7 + §15.9 and
`STEWARD_ORCH_PROMPT.md` §1.3 for the canonical split.

---

## 9. Cascade Tree — Side-By-Side Example

Two level-1 drops under one project. `DROP_0` blocks `DROP_1`.
`DROP_0` is a domain-parallel scaffold (no shared packages, no shared
paths). `DROP_1` is a sequential feature release (each L3 package
consumes the package below via `blocked_by`).

Legend:
- `P` = planner node (sonnet). Plan-QA twins (opus) implicit on every `P`.
- `BQ` = build-QA twins (opus) at a planner node.
- `•` = droplet (sonnet builder). Package build+test gate implicit per package.
- `═══▶` = cross-drop or intra-drop `blocked_by`.

```
════════════════════════════════════════════════════════════════════════════════
DROP 0 — PLATFORM SCAFFOLD            ║  DROP 1 — AUTH FEATURE RELEASE
3 depths, package-parallel            ║  4 depths, package-sequential
                                      ║  blocked_by: DROP 0  (cross-drop, below)
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
     │   │                   │         ║            │   [blocked_by: pkg-user-entity]
     │   └─ L3 droplets:     │         ║            │   │
     │      • TOML-parser    │         ║            │   └─ L4 droplets:
     │      • validator      │         ║            │      • verify-password
     │      • defaults       │         ║            │      • token-issuance
     │   BQ twins            │         ║            │      • ratelimit-hook
     │                       │         ║            │   BQ twins
     └─ L2  pkg-storage    ──┘         ║            │
         (P + plan-QA)                 ║            └─ L3  pkg-http-adapter
         │                             ║                (P + plan-QA)
         └─ L3 droplets:               ║                [blocked_by: pkg-auth-service]
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

- **DROP_0** L2 children (`logger`, `config`, `storage`) have **no
  shared `packages`** and **no shared `paths`**. The dispatcher can
  spawn their planners in parallel, their droplets in parallel
  (within package boundaries — one package build gate per L2), and
  QA twins in parallel.
- **DROP_1** L3 chain (`user-entity → auth-service → http-adapter`)
  is strict `blocked_by`. The dispatcher must serialize. Within
  each L3, droplets targeting that single package serialize via
  implicit intra-package `blocked_by` — they share compile (§2.2).

### 9.2 Plan-Time Blocker Rules (validated by plan-QA §4.4)

- Every sibling pair with overlapping `paths` has an explicit
  `blocked_by` between them.
- Every sibling pair with overlapping `packages` has an explicit
  `blocked_by` between them.
- No blocker cycles anywhere in the tree.

---

## 10. Dogfood Plan (Workflow-MD → Tillsyn-Native)

### 10.1 Pre-Drop-3 (Workflow-MD, Parallel Subagent Spawns Today)

- Hand-apply §2-§7 rules via `drops/WORKFLOW.md` + `drops/_TEMPLATE/`.
- Track droplet metadata in `drops/DROP_N_.../PLAN.md` unit blocks.
- Plan-QA + build-QA twins are manually-spawned subagents per the
  drop orchestrator's spin-up checklist.
- **Parallel spawn today**: the drop orchestrator spawns non-blocked
  sibling subagents concurrently via multi-tool-call `Agent`
  invocations. This is the manual pre-Drop-4 stand-in for the future
  dispatcher. Blocker graph is authored at plan time and honored at
  spawn time.
- Failed-droplet audit: drop orchestrator records every planner edit
  in the drop's `BUILDER_WORKLOG.md` under a `## Droplet Edits`
  subsection.

### 10.2 Drop 3 (Template System)

- Encode `child_rules` so any planner node automatically spawns its
  plan-QA twins on creation.
- Encode the droplet template (paths/packages/acceptance/blocked_by
  as required fields).
- Encode the two-pass plan-QA (per-node + global L1 sweep §4.4).

### 10.3 Drop 4 (Dispatcher)

- Enforce descent-gates-on-plan-QA (§4.3).
- Enforce bottom-up completion (§5.2).
- Enforce blocker-failure re-QA invariant (§7).
- Parallel spawn honoring `blocked_by` + `paths` + `packages`.

### 10.4 Refinement Drop (Post-Drop-4)

- Make role→model bindings configurable per path + actionItem type.
- Make §4.4 global-sweep depth threshold configurable.
- Make droplet ceilings configurable per actionItem type, with
  planner "ask for permission" flow on breach.
- Make audit-storage strategy configurable if Option X no longer
  fits.

(All four itemized in `PLAN.md` §19.10 + §20.10.)

---

## 11. Affected Cascade Drops (Verify Before Promoting)

Per dev direction, "make sure that it wasn't a drop 1 thing."
Self-assessed; planner-agent review deferred until Tillsyn MCP
restores.

| Drop     | Affected    | Why                                                   |
|----------|-------------|-------------------------------------------------------|
| Drop 1   | No (direct) | Uses `paths`/`packages` + `failed` from Drop 1 as dependencies. Drop 1 scope unchanged. |
| Drop 1.5 | **Yes**     | TUI renders `failed` items red + warning notifications (§6.2). Confirm with DROP_1.5_ORCH. |
| Drop 1.75| No          | Kind-collapse + template_libraries excision. Independent. |
| Drop 2   | No          | `metadata.role` is a field migration. Role names here match. |
| Drop 3   | **Yes**     | Templates encode planner-spawns-plan-QA-twins, droplet shape, two-pass plan-QA. |
| Drop 4   | **Yes**     | Dispatcher enforces descent gate, bottom-up completion, blocker-failure re-QA, parallel spawn. |
| Drop 10  | **Yes**     | All refinement toggles in §10.4 are filed here — see `PLAN.md` §19.10. |
| Drop 11  | No (direct) | Commit-agent (haiku) is already scoped here; this doc formalizes its role. |

**Failed-state model stays Drop 1.** This design adds TUI rendering
(Drop 1.5) and audit-trail storage (new, deferred per §8); does not
expand Drop 1 scope.

---

## 12. Benchmarking Plan

The eventual docs/article treatment will include empirical
comparisons. Planned benchmarks:

- **Baseline**: single-agent, single-prompt, end-to-end coding — the
  "monolithic agent" control.
- **Baseline-plus**: single-agent with semi-formal-reasoning
  certificate (the Section 0 loop described in
  `SEMI-FORMAL-REASONING.md`). This is our *current* setup without
  the cascade.
- **Cascade (this design)**: multi-level planner-tree + droplets +
  auto-QA gates + dispatcher-driven parallelism.

Evaluation framework: arxiv 2603.01896 (*Ugare & Chandra, "Agentic
Code Reasoning,"* Meta, 4 Mar 2026) provides the patch-equivalence
benchmark shape + reasoning-certificate baseline metrics. We adopt
its primary measure (patch-equivalence rate vs. ground truth on
standard coding benchmarks — SWE-bench-class) and extend with
cascade-specific metrics (§13).

---

## 13. Metrics & Instrumentation

Per dev direction — "record build/green rate by task, model type,
all sorts of metrics we can track!" Initial metric catalog:

### 13.1 Per-Droplet

- **Build-green rate** — percentage of droplets that pass
  `mage ci`-class package gate on first builder attempt.
- **Builder-retry count** — how many times a droplet was re-dispatched
  after a failed gate.
- **Planner-edit count** — how many times the planner edited the
  droplet's description/acceptance/paths between attempts.
- **Actual LOC delta** — vs. the soft ~80 LOC target.
- **Actual file count** — vs. the soft ≤3 file ceiling.
- **Builder model + time-to-completion + token cost**.

### 13.2 Per-Planner-Node

- **Plan-QA pass rate** — does the plan survive plan-QA on first
  shot, or does it need revision?
- **Plan-QA round count** — if plan fails, how many revision cycles
  until pass?
- **Build-QA pass rate** — does the completed sub-tree survive
  build-QA?
- **Droplet count per planner** — are planners over-decomposing
  (too many trivial droplets) or under-decomposing (bloated
  droplets)?

### 13.3 Per-Drop

- **Total cost** — by model tier (sonnet planners + opus QAs +
  sonnet builders).
- **Total time-to-completion**.
- **Re-QA frequency** — how often does §7 ancestor re-QA fire?
  Signals plan-quality at the top of the tree.
- **Parallelism extraction rate** — actual parallel spawns divided
  by the theoretical maximum the blocker graph permits.
- **Blocker-cycle detection count** — how many cycles did plan-QA
  catch before they shipped?
- **Path/package conflict count** — missing `blocked_by` between
  siblings that share paths or packages.

### 13.4 Comparative

- **Cascade vs. baseline-plus**: patch-equivalence rate (arxiv
  2603.01896) and cost-per-drop on matched workloads.
- **Cascade vs. monolithic**: same.
- **Model-tier ablations**: builder sonnet vs. haiku (once haiku
  becomes a candidate) on matched droplet workloads.

Instrumentation location: each droplet's completion comment + the
`DROP <N> END — LEDGER UPDATE` description. Ledger extracts
aggregates per drop. This becomes the source data for the blog post /
article.

---

## 14. Open Questions

Most opens from the earlier draft resolved. Remaining:

- **Q1. Workflow-MD exit criteria.** When do we retire `drops/` MDs
  in favor of direct Tillsyn writes? Recommend: after Drop 4
  dispatcher lands AND at least 3 workflow-MD drops have completed
  (data for tuning). Confirm before closing this doc.
- **Q2. Metrics storage format.** Per-droplet + per-planner-node
  metrics need structured retention. Today's ledger is prose.
  Defer specifics to the refinement drop; starter shape is a JSON
  block in each `DROP_N_LEDGER_ENTRY` description.

All other prior opens are tracked as refinements in `PLAN.md` §19.10
and configuration questions in `PLAN.md` §20.10.

---

## 15. Cross-References

- `main/PLAN.md` §2.2 — existing infinite-nesting design. This thesis
  operationalizes it.
- `main/PLAN.md` §5 — `paths`/`packages` first-class. Dependency.
- `main/PLAN.md` §17.1 — file/package blocker requirement. This
  thesis extends to package-level build gates.
- `main/PLAN.md` §19.10 — cascade-granularity refinements list.
- `main/PLAN.md` §20.10 — cascade-configuration open questions.
- `main/CLAUDE.md` §"Cascade Tree Structure" — current kind-hierarchy
  + agent bindings. §3 + §4 of this doc refine the bindings.
- `main/CLAUDE.md` §"Blocker Semantics" — `blocked_by` rules. §7
  adds the re-QA-on-blocker-edit invariant.
- `main/SEMI-FORMAL-REASONING.md` — reasoning certificate spec.
  Plan-QA and build-QA twins produce certificates per that spec.
- `workflow/example/drops/WORKFLOW.md` — current 7-phase
  workflow-MD lifecycle. §10.1 of this doc lists the extensions.
- `workflow/example/drops/_TEMPLATE/PLAN.md` — current
  template. §10.1 requires adding a droplet-shape preamble.
- Benchmark reference: arxiv 2603.01896 (Ugare & Chandra, 2026-03-04).

// Snapshot of cascade.* fields as of 2026-05-18 .ta/schema.toml; refresh on schema change
//
// Hand-curated frozen fixtures for the Thariq demo pages
// (web/templates_embed/src/pages/thariq/*.astro). All timestamps are
// hardcoded RFC3339 strings — NO `new Date()`, NO `Math.random()`. Determinism
// is required by D5b VHS / golden-snapshot capture downstream.
//
// Shapes mirror ActionItem (extends NodeBase) per .ta/schema.toml. Only the
// fields the demo pages actually render are populated; unused optional fields
// are omitted to keep the fixture readable.

export type CascadeState = "todo" | "in_progress" | "complete" | "failed" | "archived";
export type CascadeOutcome = "" | "success" | "failure" | "blocked";
export type CascadeRole =
  | "builder"
  | "qa-proof"
  | "qa-falsification"
  | "qa-a11y"
  | "qa-visual"
  | "design"
  | "commit"
  | "planner"
  | "research";

interface CascadeNodeBase {
  id: string;
  title: string;
  state: CascadeState;
  created_at: string;
  updated_at: string;
  outcome?: CascadeOutcome;
  description?: string;
  parent_id?: string;
  priority?: "low" | "medium" | "high";
  started_at?: string;
  completed_at?: string;
  labels?: ReadonlyArray<string>;
}

interface CascadeActionItem extends CascadeNodeBase {
  role: CascadeRole;
  structural_type?: "" | "drop" | "segment" | "confluence" | "droplet";
  objective?: string;
  paths?: ReadonlyArray<string>;
  packages?: ReadonlyArray<string>;
  acceptance_criteria?: string;
  decision_log?: ReadonlyArray<string>;
  transition_notes?: string;
  attempt_count?: number;
  implementation_notes_user?: string;
  implementation_notes_agent?: string;
}

export interface CascadeDrop extends CascadeActionItem {
  structural_type: "drop";
  drop_number: number;
}

export interface CascadePlanner extends CascadeActionItem {
  role: "planner";
}

export interface CascadeDroplet extends CascadeActionItem {
  structural_type: "droplet";
  irreducible?: boolean;
}

export interface CascadeQaProof extends CascadeActionItem {
  role: "qa-proof" | "qa-a11y" | "qa-visual";
  target_id: string;
}

export interface CascadeQaFalsification extends CascadeActionItem {
  role: "qa-falsification";
  target_id: string;
}

// ---------------------------------------------------------------------------
// MOCK_DROP — single L1 drop record.
// ---------------------------------------------------------------------------

export const MOCK_DROP: CascadeDrop = {
  id: "drop_042.drop.thariq_demo_root",
  structural_type: "drop",
  drop_number: 42,
  role: "planner",
  title: "Drop 042 — Thariq demo cascade root",
  state: "in_progress",
  outcome: "",
  priority: "high",
  created_at: "2026-05-10T09:00:00Z",
  updated_at: "2026-05-18T12:30:00Z",
  started_at: "2026-05-10T09:15:00Z",
  objective:
    "Demonstrate the cascade-record substrate against a 4-page stil component gallery. Showcase drop → planner → droplet → qa-twin descent with frozen fixture data.",
  description:
    "Top-level fixture used by the Thariq demo pages. Exercises the full cascade.{drop,planner,droplet,qa_proof,qa_falsification} record shape so the demo gallery can mount each type against real stil components without touching live ta records.",
  acceptance_criteria:
    "- 4 demo pages render frozen fixtures\n- ZERO new inline script across dist/thariq/*.html\n- VHS goldens captured for each demo",
  paths: [
    "web/templates_embed/src/pages/thariq/",
    "web/templates_embed/src/mock/thariq_records.ts",
  ],
  labels: ["demo", "fixture", "thariq"],
  decision_log: [
    "2026-05-10: chose frozen TS fixtures over live ta records (determinism for VHS)",
    "2026-05-12: locked stil component subset to Sidebar / Topbar / MailRow / Playground for v1",
  ],
};

// ---------------------------------------------------------------------------
// MOCK_PLANNERS — interior planner records under MOCK_DROP.
// ---------------------------------------------------------------------------

export const MOCK_PLANNERS: ReadonlyArray<CascadePlanner> = [
  {
    id: "drop_042.drop.l2_a_thariq_planner_shell",
    role: "planner",
    structural_type: "segment",
    parent_id: MOCK_DROP.id,
    title: "L2-A planner — demo shell + fixtures",
    state: "complete",
    outcome: "success",
    priority: "high",
    created_at: "2026-05-10T10:00:00Z",
    updated_at: "2026-05-14T18:20:00Z",
    started_at: "2026-05-10T10:05:00Z",
    completed_at: "2026-05-14T18:20:00Z",
    objective:
      "Stand up the demo shell: index gallery, frozen TS fixtures, testdata directory scaffolding. Output: 5 child droplets.",
    description:
      "Foundation planner. Owns the import-graph choices for the demo cluster and the fixture shape contract. Decision authority for which stil components ship in v1 of the gallery.",
    decision_log: [
      "Lock @hylla/stil imports to deep paths (no exports field in stil v0.0.1)",
      "Mock data lives in src/mock/, not src/data/, to signal non-production fixture",
      "VHS goldens captured orchestrator-direct, not via builder droplet",
    ],
    paths: [
      "web/templates_embed/src/pages/thariq/index.astro",
      "web/templates_embed/src/mock/thariq_records.ts",
    ],
  },
  {
    id: "drop_042.drop.l2_b_thariq_planner_pages",
    role: "planner",
    structural_type: "segment",
    parent_id: MOCK_DROP.id,
    title: "L2-B planner — demo pages",
    state: "in_progress",
    outcome: "",
    priority: "medium",
    created_at: "2026-05-12T09:00:00Z",
    updated_at: "2026-05-18T11:45:00Z",
    started_at: "2026-05-15T08:30:00Z",
    objective:
      "Decompose the 4 demo pages (drop-dashboard, planner-detail, droplet-kanban, qa-twins) into atomic builder droplets. Coordinate sibling-parallel waves.",
    description:
      "Page-level planner. Owns the wave plan: D1 foundation → (D2 ∥ D3 ∥ D4) → D5 close-gate. Routes the qa-twin requirement at every page-completion boundary.",
    decision_log: [
      "Dispatch D2/D3/D4 in parallel (disjoint paths)",
      "Defer D5b VHS capture to orchestrator-direct work (sandbox limitation)",
    ],
    paths: [
      "web/templates_embed/src/pages/thariq/drop-dashboard.astro",
      "web/templates_embed/src/pages/thariq/planner-detail.astro",
      "web/templates_embed/src/pages/thariq/droplet-kanban.astro",
      "web/templates_embed/src/pages/thariq/qa-twins.astro",
    ],
  },
  {
    id: "drop_042.drop.l2_c_thariq_planner_verification",
    role: "planner",
    structural_type: "segment",
    parent_id: MOCK_DROP.id,
    title: "L2-C planner — VHS + a11y verification",
    state: "todo",
    outcome: "",
    priority: "medium",
    created_at: "2026-05-13T11:00:00Z",
    updated_at: "2026-05-13T11:00:00Z",
    objective:
      "Capture VHS goldens and run a11y axe-core scan against dist/thariq/*.html. Records failure artifacts under web/templates_embed/testdata/thariq/.",
    description:
      "Verification planner. Fires after L2-B close-gate. Owns the VHS .tape + .gif + .txt artifact set and the a11y baseline.",
    decision_log: [],
    paths: ["web/templates_embed/testdata/thariq/"],
  },
];

// ---------------------------------------------------------------------------
// MOCK_DROPLETS — 8 atomic build leaves spanning all 4 lifecycle states.
// ---------------------------------------------------------------------------

export const MOCK_DROPLETS: ReadonlyArray<CascadeDroplet> = [
  {
    id: "drop_042.drop.l3_d1_mock_data_and_index",
    role: "builder",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_a_thariq_planner_shell",
    title: "D1 builder — mock data + index gallery",
    state: "complete",
    outcome: "success",
    priority: "high",
    created_at: "2026-05-14T09:00:00Z",
    updated_at: "2026-05-14T17:55:00Z",
    started_at: "2026-05-14T09:30:00Z",
    completed_at: "2026-05-14T17:55:00Z",
    objective: "Foundation droplet. NEW index.astro + thariq_records.ts + testdata stub.",
    paths: [
      "web/templates_embed/src/pages/thariq/index.astro",
      "web/templates_embed/src/mock/thariq_records.ts",
      "web/templates_embed/testdata/thariq/.gitkeep",
      "web/templates_embed/testdata/thariq/README.md",
      "web/templates_embed/tsconfig.json",
    ],
    attempt_count: 1,
    transition_notes:
      "All 5 files landed; mage TemplatesBuildEmbed + TemplatesBuildVerifyStable green on first run.",
  },
  {
    id: "drop_042.drop.l3_d2_drop_dashboard",
    role: "builder",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_b_thariq_planner_pages",
    title: "D2 builder — drop dashboard page",
    state: "complete",
    outcome: "success",
    priority: "high",
    created_at: "2026-05-15T09:00:00Z",
    updated_at: "2026-05-16T14:20:00Z",
    started_at: "2026-05-15T09:30:00Z",
    completed_at: "2026-05-16T14:20:00Z",
    objective:
      "Render MOCK_DROP with descendants as a stil-shaped app-shell dashboard (Sidebar + NavRail + Topbar + MailRow + BottomTabs).",
    paths: ["web/templates_embed/src/pages/thariq/drop-dashboard.astro"],
    attempt_count: 1,
  },
  {
    id: "drop_042.drop.l3_d3_planner_detail",
    role: "builder",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_b_thariq_planner_pages",
    title: "D3 builder — planner detail page",
    state: "in_progress",
    outcome: "",
    priority: "medium",
    created_at: "2026-05-15T09:00:00Z",
    updated_at: "2026-05-18T10:30:00Z",
    started_at: "2026-05-17T11:00:00Z",
    objective:
      "ComponentCard detail of MOCK_PLANNERS[0] (objective + description + decision_log). State pill, back-link.",
    paths: ["web/templates_embed/src/pages/thariq/planner-detail.astro"],
    attempt_count: 1,
  },
  {
    id: "drop_042.drop.l3_d4_droplet_kanban",
    role: "builder",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_b_thariq_planner_pages",
    title: "D4 builder — droplet kanban page",
    state: "in_progress",
    outcome: "",
    priority: "medium",
    created_at: "2026-05-15T09:00:00Z",
    updated_at: "2026-05-18T09:15:00Z",
    started_at: "2026-05-17T13:45:00Z",
    objective:
      "4-column kanban (todo / in_progress / complete / failed) with @container collapse to 1 col below 768px.",
    paths: ["web/templates_embed/src/pages/thariq/droplet-kanban.astro"],
    attempt_count: 1,
  },
  {
    id: "drop_042.drop.l3_d5a_qa_twins_page",
    role: "builder",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_b_thariq_planner_pages",
    title: "D5a builder — qa-twins page + stub markers",
    state: "todo",
    outcome: "",
    priority: "medium",
    created_at: "2026-05-15T09:00:00Z",
    updated_at: "2026-05-15T09:00:00Z",
    objective:
      "qa_proof + qa_falsification side-by-side (2-col grid → 1-col below 768px). Stub .txt markers for VHS slots.",
    paths: [
      "web/templates_embed/src/pages/thariq/qa-twins.astro",
      "web/templates_embed/testdata/thariq/index.txt",
    ],
    irreducible: true,
  },
  {
    id: "drop_042.drop.l3_d6_a11y_axe_scan",
    role: "qa-a11y",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_c_thariq_planner_verification",
    title: "D6 a11y — axe-core scan",
    state: "todo",
    outcome: "",
    priority: "medium",
    created_at: "2026-05-16T09:00:00Z",
    updated_at: "2026-05-16T09:00:00Z",
    objective:
      "Run @axe-core/cli against dist/thariq/*.html. Capture violations as cascade.failure records if any.",
    paths: ["web/templates_embed/testdata/thariq/axe.json"],
  },
  {
    id: "drop_042.drop.l3_d7_vhs_initial_attempt",
    role: "builder",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_c_thariq_planner_verification",
    title: "D7 builder — VHS golden capture (initial attempt)",
    state: "failed",
    outcome: "failure",
    priority: "medium",
    created_at: "2026-05-13T09:00:00Z",
    updated_at: "2026-05-13T16:40:00Z",
    started_at: "2026-05-13T13:00:00Z",
    completed_at: "2026-05-13T16:40:00Z",
    objective:
      "Capture VHS .tape + .gif for each demo page. Blocked by sandbox TTY restriction; rolled out to orchestrator-direct work.",
    paths: ["web/templates_embed/testdata/thariq/"],
    attempt_count: 2,
    transition_notes:
      "vhs requires a TTY the sandbox doesn't provide. Re-routed to orchestrator-direct attention_item.",
    last_failure_context:
      "vhs ./testdata/thariq/index.tape → error: cannot allocate pty (sandbox).",
  },
  {
    id: "drop_042.drop.l3_d8_link_audit",
    role: "qa-visual",
    structural_type: "droplet",
    parent_id: "drop_042.drop.l2_c_thariq_planner_verification",
    title: "D8 qa-visual — inter-page link audit",
    state: "todo",
    outcome: "",
    priority: "low",
    created_at: "2026-05-16T09:00:00Z",
    updated_at: "2026-05-16T09:00:00Z",
    objective:
      "Walk dist/thariq/*.html, assert every <a href> resolves to an existing demo page or external URL with the documented prefix.",
    paths: ["web/templates_embed/testdata/thariq/link-audit.json"],
  },
];

// ---------------------------------------------------------------------------
// QA twin records targeting MOCK_PLANNERS[0].
// ---------------------------------------------------------------------------

export const MOCK_QA_PROOF: CascadeQaProof = {
  id: "drop_042.drop.l2_a_thariq_planner_shell-plan-qa-proof",
  role: "qa-proof",
  parent_id: "drop_042.drop.l2_a_thariq_planner_shell",
  target_id: "drop_042.drop.l2_a_thariq_planner_shell",
  title: "Plan-QA proof of L2-A planner — demo shell + fixtures",
  state: "complete",
  outcome: "success",
  priority: "high",
  created_at: "2026-05-10T10:00:00Z",
  updated_at: "2026-05-12T15:30:00Z",
  started_at: "2026-05-12T14:00:00Z",
  completed_at: "2026-05-12T15:30:00Z",
  objective:
    "Verify the L2-A plan: fixture shape mirrors current schema, import graph is acyclic, demo page count matches the 4-page contract.",
  description:
    "Evidence pass — confirms each plan claim is backed by .ta/schema.toml fields, stil component exports, and the drop description.",
  decision_log: [
    "Fixture shape verified against .ta/schema.toml @ 2026-05-12 — all required fields present",
    "Import graph audit: index.astro → @hylla/stil (Playground + Sidebar + Topbar), no cycles",
    "Demo page count: 4 child pages confirmed (drop-dashboard, planner-detail, droplet-kanban, qa-twins)",
  ],
};

export const MOCK_QA_FALSIFICATION: CascadeQaFalsification = {
  id: "drop_042.drop.l2_a_thariq_planner_shell-plan-qa-falsification",
  role: "qa-falsification",
  parent_id: "drop_042.drop.l2_a_thariq_planner_shell",
  target_id: "drop_042.drop.l2_a_thariq_planner_shell",
  title: "Plan-QA falsif of L2-A planner — demo shell + fixtures",
  state: "complete",
  outcome: "success",
  priority: "high",
  created_at: "2026-05-10T10:00:00Z",
  updated_at: "2026-05-12T16:45:00Z",
  started_at: "2026-05-12T15:30:00Z",
  completed_at: "2026-05-12T16:45:00Z",
  objective:
    "Attack the L2-A plan. Found 2 confirmed counter-examples (snapshot-date comment missing; tsconfig ownership ambiguous) — folded as plan amendments before descent.",
  description:
    "Adversarial pass — tried to break the L2-A plan via hidden dependencies, contract drift, and YAGNI pressure.",
  decision_log: [
    "CE1: thariq_records.ts had no schema-snapshot marker — without it, schema drift is silent. Folded: snapshot-date header comment required.",
    "CE2: tsconfig.json ownership unclear (L3-E3 D1 also touches it). Folded: whichever droplet lands first owns; the other no-ops with a transition note.",
    "Attempted: argue YAGNI on MOCK_DROPLETS size of 8 — REJECTED. D4 kanban requires all 4 states represented; 8 is the smallest set that covers + spreads parents.",
  ],
};

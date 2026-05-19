// Snapshot of cascade.* + roadmap.* + schema.* fields as of 2026-05-18 .ta/schema.toml; refresh on schema change. FROZEN data — NO new Date(), NO Math.random(). All timestamps are hardcoded RFC3339 literals.
//
// L3-E3-D1 foundation fixtures for the stil view pages
// (web/templates_embed/src/pages/stil/*.astro). Each MOCK_* constant maps
// one-to-one onto one of the 7 view pages the L3-E3 lane builds:
//
//   MOCK_DROP             → src/pages/stil/cascade_drop.astro      (D2)
//   MOCK_PLANNER          → src/pages/stil/cascade_planner.astro   (D2)
//   MOCK_DROPLET          → src/pages/stil/cascade_droplet.astro   (D2)
//   MOCK_QA_PROOF         → src/pages/stil/cascade_qa_proof.astro  (D3)
//   MOCK_QA_FALSIFICATION → src/pages/stil/cascade_qa_falsification.astro (D3)
//   MOCK_ROADMAP_VERSION  → src/pages/stil/roadmap_version.astro   (D4)
//   MOCK_SCHEMA_BROWSER   → src/pages/stil/schema_browser.astro    (D4)
//
// Shapes mirror ActionItem (extends NodeBase) per .ta/schema.toml lines
// 14-163 (cascade.*) and 734-755 (roadmap.version). Only fields the demo
// pages actually render are populated; unused optionals are omitted.
//
// AMEND-3 (U1 bindings.json source-path pin): if any consumer of this
// module ever loads stil bindings.json (none does at D1; recorded here so
// downstream droplets honour the pin without re-deriving it), the source-
// of-truth path is:
//
//   /Users/evanschultz/Documents/Code/hylla/stil/main/src/bindings/baseline.json
//
// Fallback to a built `dist/bindings.json` is allowed when the source path
// is unavailable (e.g. release build inside the embed pipeline). The pin
// matches the path documented in the L3-E3 parent record (AMEND-3).

// ---------------------------------------------------------------------------
// Shared cascade enums + base types (mirrors thariq_records.ts conventions
// so downstream pages can swap fixtures without reshaping their props).
// ---------------------------------------------------------------------------

export type CascadeState =
  | "todo"
  | "in_progress"
  | "complete"
  | "failed"
  | "archived";

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

export type RoadmapStatus = "planning" | "in_progress" | "landed" | "punted";

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

export interface RoadmapVersion {
  id: string;
  title: string;
  state: CascadeState;
  status: RoadmapStatus;
  created_at: string;
  updated_at: string;
  vision_summary?: string;
  target_date_band?: string;
  description?: string;
  started_at?: string;
  completed_at?: string;
  priority?: "low" | "medium" | "high";
  labels?: ReadonlyArray<string>;
}

// ---------------------------------------------------------------------------
// Schema browser fixture — hand-curated subset of .ta/schema.toml shaped for
// the schema_browser view. Mirrors the on-disk record structure: a `scopes`
// array of {name, description, types[]} where each type carries an
// `extends` base and a list of `fields`.
// ---------------------------------------------------------------------------

export interface SchemaFieldFixture {
  name: string;
  type: string;
  required?: boolean;
  description?: string;
  enum?: ReadonlyArray<string>;
}

export interface SchemaTypeFixture {
  name: string;
  extends?: string;
  description?: string;
  fields: ReadonlyArray<SchemaFieldFixture>;
}

export interface SchemaScopeFixture {
  name: string;
  description?: string;
  types: ReadonlyArray<SchemaTypeFixture>;
}

export interface SchemaBrowserFixture {
  scopes: ReadonlyArray<SchemaScopeFixture>;
}

// ---------------------------------------------------------------------------
// MOCK_DROP — single cascade.drop record. drop_004-themed so the demo
// pages echo the lane that built them.
// ---------------------------------------------------------------------------

export const MOCK_DROP: CascadeDrop = {
  id: "drop_004.drop.stil_demo_root",
  structural_type: "drop",
  drop_number: 4,
  role: "planner",
  title: "Drop 004 — stil view substrate",
  state: "in_progress",
  outcome: "",
  priority: "high",
  created_at: "2026-05-04T08:00:00Z",
  updated_at: "2026-05-18T22:50:00Z",
  started_at: "2026-05-04T08:15:00Z",
  objective:
    "Ship the stil-rendered view set for cascade.* + roadmap.version + schema browser. 7 pages, frozen fixtures, ZERO authored scripts.",
  description:
    "Top-level demo drop for the stil view substrate. Exercises each cascade + roadmap + schema record type against the stil component library, all driven from this hand-curated fixture file.",
  acceptance_criteria:
    "- 7 pages render frozen fixtures\n- ZERO new authored <script> across dist/stil/*.html\n- mage TemplatesBuildEmbed + TemplatesBuildVerifyStable green",
  paths: [
    "web/templates_embed/src/layouts/StilLayout.astro",
    "web/templates_embed/src/mock/stil_records.ts",
    "web/templates_embed/src/pages/stil/",
  ],
  labels: ["demo", "fixture", "stil"],
  decision_log: [
    "2026-05-04: import stil CSS via deep paths (no exports field in stil v0.0.1)",
    "2026-05-12: lock stil component set to {Sidebar, Topbar, NavRail, BottomTabs, ComponentCard, MailRow, Button, Swatch} for v1",
    "2026-05-18: split cascade_qa into 2 separate .astro files per AMEND-2",
  ],
};

// ---------------------------------------------------------------------------
// MOCK_PLANNER — single cascade.planner record. Child of MOCK_DROP.
// ---------------------------------------------------------------------------

export const MOCK_PLANNER: CascadePlanner = {
  id: "drop_004.drop.l3_e3_stil_views",
  role: "planner",
  structural_type: "segment",
  parent_id: MOCK_DROP.id,
  title: "L3-E3 planner — stil view set",
  state: "in_progress",
  outcome: "",
  priority: "high",
  created_at: "2026-05-15T09:00:00Z",
  updated_at: "2026-05-18T22:30:00Z",
  started_at: "2026-05-15T09:30:00Z",
  objective:
    "Decompose the 7 stil view pages into 4 builder droplets (D1 foundation, D2 cascade records, D3 qa twins, D4 roadmap + schema). Coordinate sibling-parallel waves.",
  description:
    "Page-level planner for the stil view substrate. Owns the wave plan: D1 foundation → (D2 ∥ D3 ∥ D4) → close-gate. Routes the qa-twin requirement at every page-completion boundary.",
  decision_log: [
    "Dispatch D2/D3/D4 in parallel — paths disjoint",
    "Fold all 4 plan-QA amends into D1 (component allowlist, qa-twin split, bindings pin, mock-freeze header)",
    "Defer VHS capture to orchestrator-direct work",
  ],
  paths: [
    "web/templates_embed/src/pages/stil/cascade_drop.astro",
    "web/templates_embed/src/pages/stil/cascade_planner.astro",
    "web/templates_embed/src/pages/stil/cascade_droplet.astro",
    "web/templates_embed/src/pages/stil/cascade_qa_proof.astro",
    "web/templates_embed/src/pages/stil/cascade_qa_falsification.astro",
    "web/templates_embed/src/pages/stil/roadmap_version.astro",
    "web/templates_embed/src/pages/stil/schema_browser.astro",
  ],
};

// ---------------------------------------------------------------------------
// MOCK_DROPLET — single cascade.droplet record. irreducible:true, with a
// paths array. Child of MOCK_PLANNER.
// ---------------------------------------------------------------------------

export const MOCK_DROPLET: CascadeDroplet = {
  id: "drop_004.drop.builder_l3_e3_d1_stil_layout_mock",
  role: "builder",
  structural_type: "droplet",
  parent_id: MOCK_PLANNER.id,
  title: "L3-E3-D1 builder — stil layout + mock + index foundation",
  state: "in_progress",
  outcome: "",
  priority: "high",
  irreducible: true,
  created_at: "2026-05-18T17:30:00Z",
  updated_at: "2026-05-18T22:50:00Z",
  started_at: "2026-05-18T22:50:00Z",
  objective:
    "Foundation droplet. NEW StilLayout.astro + stil_records.ts + stil/index.astro. Unblocks D2/D3/D4.",
  paths: [
    "web/templates_embed/src/layouts/StilLayout.astro",
    "web/templates_embed/src/mock/stil_records.ts",
    "web/templates_embed/src/pages/stil/index.astro",
  ],
  attempt_count: 1,
  transition_notes:
    "All 4 plan-QA amends folded here: component allowlist, cascade_qa split, bindings.json pin, mock-freeze header.",
};

// ---------------------------------------------------------------------------
// MOCK_QA_PROOF — single cascade.qa_proof record. Targets MOCK_PLANNER.
// ---------------------------------------------------------------------------

export const MOCK_QA_PROOF: CascadeQaProof = {
  id: "drop_004.drop.l3_e3_stil_views-plan-qa-proof",
  role: "qa-proof",
  parent_id: MOCK_PLANNER.id,
  target_id: MOCK_PLANNER.id,
  title: "Plan-QA proof of L3-E3 planner — stil view set",
  state: "complete",
  outcome: "success",
  priority: "high",
  created_at: "2026-05-15T09:00:00Z",
  updated_at: "2026-05-18T11:20:00Z",
  started_at: "2026-05-18T10:00:00Z",
  completed_at: "2026-05-18T11:20:00Z",
  objective:
    "Verify the L3-E3 plan: 7 view pages map 1:1 onto fixture exports; import graph is acyclic; component allowlist matches stil v0.0.1 exports.",
  description:
    "Evidence pass — confirms each plan claim is backed by .ta/schema.toml fields, stil component file existence, and the parent drop description.",
  decision_log: [
    "Fixture shape verified against .ta/schema.toml @ 2026-05-18 — all required fields populated",
    "Import graph audit: StilLayout → 3 stil CSS files + Sidebar + Topbar (allowlisted)",
    "View-page count: 7 confirmed (cascade_drop, cascade_planner, cascade_droplet, cascade_qa_proof, cascade_qa_falsification, roadmap_version, schema_browser)",
  ],
};

// ---------------------------------------------------------------------------
// MOCK_QA_FALSIFICATION — single cascade.qa_falsification record. Targets
// MOCK_PLANNER. Reports the 4 confirmed counter-examples folded as plan
// amendments before descent.
// ---------------------------------------------------------------------------

export const MOCK_QA_FALSIFICATION: CascadeQaFalsification = {
  id: "drop_004.drop.l3_e3_stil_views-plan-qa-falsification",
  role: "qa-falsification",
  parent_id: MOCK_PLANNER.id,
  target_id: MOCK_PLANNER.id,
  title: "Plan-QA falsif of L3-E3 planner — stil view set",
  state: "complete",
  outcome: "success",
  priority: "high",
  created_at: "2026-05-15T09:00:00Z",
  updated_at: "2026-05-18T14:50:00Z",
  started_at: "2026-05-18T11:20:00Z",
  completed_at: "2026-05-18T14:50:00Z",
  objective:
    "Attack the L3-E3 plan. Found 4 confirmed counter-examples — all folded as plan amendments before descent.",
  description:
    "Adversarial pass — tried to break the L3-E3 plan via hidden dependencies, YAGNI pressure, and contract drift.",
  decision_log: [
    "CE1: stil component allowlist was implicit. Folded AMEND-1: explicit allowlist {Sidebar, Topbar, NavRail, BottomTabs, ComponentCard, MailRow, Button, Swatch}.",
    "CE2: cascade_qa was a single page mixing proof + falsification roles. Folded AMEND-2: split into cascade_qa_proof.astro + cascade_qa_falsification.astro.",
    "CE3: bindings.json source path was ambiguous. Folded AMEND-3: pin to stil/main/src/bindings/baseline.json (source) with dist/bindings.json fallback.",
    "CE4: stil_records.ts had no schema-snapshot freeze marker. Folded AMEND-4: verbatim header comment with 2026-05-18 snapshot date and explicit FROZEN-data guard.",
  ],
};

// ---------------------------------------------------------------------------
// MOCK_ROADMAP_VERSION — single roadmap.version record. Pre-MVP-feature-
// complete band that the drop_004 work targets.
// ---------------------------------------------------------------------------

export const MOCK_ROADMAP_VERSION: RoadmapVersion = {
  id: "roadmap.version.v0_1_0_pre_mvp",
  title: "v0.1.0 — MVP feature completion",
  state: "in_progress",
  status: "in_progress",
  priority: "high",
  created_at: "2026-04-01T09:00:00Z",
  updated_at: "2026-05-18T22:50:00Z",
  started_at: "2026-04-01T09:15:00Z",
  target_date_band: "pre-MVP",
  vision_summary:
    "Every MVP feature works without known issues. Launch baseline for the dogfood phase: MCP + basic CLI green, TUI surface bubbletea-only (zero huh), HTML view substrate live with stil-rendered views for every record type, cascade methodology dogfooded against itself.",
  description:
    "First tagged release. Phasing: dogfood → CLI refinement → TUI overhaul. MVP-feature-completion MUST launch with zero tech debt — every pre-MVP-feature-completion tracker item is either closed or explicitly punted to post-v0.1.0.",
  labels: ["mvp", "pre-release", "dogfood"],
};

// ---------------------------------------------------------------------------
// MOCK_SCHEMA_BROWSER — hand-curated schema browser fixture mirroring a
// subset of .ta/schema.toml. 1 scope (`cascade`) with 2 types (`drop`
// extending ActionItem, `planner` extending ActionItem). Used by the
// schema_browser view page.
// ---------------------------------------------------------------------------

export const MOCK_SCHEMA_BROWSER: SchemaBrowserFixture = {
  scopes: [
    {
      name: "cascade",
      description:
        "Cascade trees — drops + their segments, confluences, droplets, planners, QA records, failures.",
      types: [
        {
          name: "drop",
          extends: "ActionItem",
          description: "L1 cascade root — one self-contained unit of work.",
          fields: [
            {
              name: "drop_number",
              type: "integer",
              required: true,
              description:
                "Sequential drop number (matches the drop_N directory).",
            },
            {
              name: "structural_type",
              type: "string",
              required: true,
              enum: ["drop"],
              description: "Locked to 'drop'.",
            },
          ],
        },
        {
          name: "planner",
          extends: "ActionItem",
          description:
            "Generic planner action item — interior cascade work that doesn't fit segment or confluence (e.g. plan-revision, retro).",
          fields: [
            {
              name: "role",
              type: "string",
              description:
                "Role of the planner — inherited from ActionItem; cascade.planner records typically set this to 'planner'.",
            },
            {
              name: "structural_type",
              type: "string",
              description:
                "Optional structural_type — segment / confluence / droplet. Inherited from ActionItem.",
            },
          ],
        },
      ],
    },
  ],
};

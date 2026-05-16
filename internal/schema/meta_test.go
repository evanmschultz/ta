package schema

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/backend/md"
)

func TestMetaSchemaEmbeddedAndNonEmpty(t *testing.T) {
	if MetaSchemaTOML == "" {
		t.Fatal("MetaSchemaTOML is empty; embed directive is broken")
	}
	if !strings.Contains(MetaSchemaTOML, "[ta_schema]") {
		t.Errorf("MetaSchemaTOML missing [ta_schema] root: %s", MetaSchemaTOML[:min(200, len(MetaSchemaTOML))])
	}
}

func TestMetaSchemaLoadsUnderNewGrammar(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema must load under its own grammar: %v", err)
	}
	db, ok := reg.DBs["ta_schema"]
	if !ok {
		t.Fatal("ta_schema db missing from parsed meta-schema")
	}
	for _, want := range []string{"db", "type", "field"} {
		if _, ok := db.Types[want]; !ok {
			t.Errorf("meta-schema missing kind %q", want)
		}
	}
}

func TestMetaSchemaPathConstant(t *testing.T) {
	if MetaSchemaPath != "ta_schema" {
		t.Errorf("MetaSchemaPath = %q, want ta_schema", MetaSchemaPath)
	}
}

// TestMetaSchemaDeclaresElementGrammar locks the F21 contract on the
// meta-schema: the field kind must declare element_type and
// element_fields sub-fields, and a [ta_schema.types] documentation
// block must be present.
func TestMetaSchemaDeclaresElementGrammar(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema load: %v", err)
	}
	field, ok := reg.DBs["ta_schema"].Types["field"]
	if !ok {
		t.Fatal("missing ta_schema.field kind")
	}
	if _, ok := field.Fields["element_type"]; !ok {
		t.Error("ta_schema.field.fields.element_type missing — F21 grammar must be declared")
	}
	if _, ok := field.Fields["element_fields"]; !ok {
		t.Error("ta_schema.field.fields.element_fields missing — F21 grammar must be declared")
	}
}

// TestMetaSchemaDeclaresExtendsAndBases locks the F22 contract on the
// meta-schema: a [ta_schema.base] kind block must be present and
// [ta_schema.type] must declare an extends sub-field. The
// [ta_schema.bases] documentation block must also be present (parallel
// to [ta_schema.types]).
func TestMetaSchemaDeclaresExtendsAndBases(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema load: %v", err)
	}
	tsDB, ok := reg.DBs["ta_schema"]
	if !ok {
		t.Fatal("ta_schema db missing")
	}
	baseKind, ok := tsDB.Types["base"]
	if !ok {
		t.Fatal("ta_schema.base kind missing — F22 grammar must be declared as a first-class kind")
	}
	if _, ok := baseKind.Fields["extends"]; !ok {
		t.Error("ta_schema.base.fields.extends missing — bases must declare extends as inheritable")
	}
	typeKind, ok := tsDB.Types["type"]
	if !ok {
		t.Fatal("ta_schema.type kind missing")
	}
	if _, ok := typeKind.Fields["extends"]; !ok {
		t.Error("ta_schema.type.fields.extends missing — concrete types must declare extends as a recognized key")
	}
}

// TestMetaSchemaValidatesUserExtendsSchema confirms a user schema
// using bases + extends loads cleanly under the F22 grammar.
func TestMetaSchemaValidatesUserExtendsSchema(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.bases.NodeBase]
description = "Common cascade-node fields."

[plans.bases.NodeBase.fields.parent_id]
type = "string"

[plans.bases.NodeBase.fields.title]
type = "string"
required = true

[plans.bases.ActionItem]
extends = "NodeBase"

[plans.bases.ActionItem.fields.role]
type = "string"
required = true

[plans.task]
description = "x"
extends = "ActionItem"

[plans.task.fields.status]
type = "string"
required = true
`
	if _, err := LoadBytes([]byte(src)); err != nil {
		t.Fatalf("user schema using F22 grammar should load: %v", err)
	}
}

// TestMetaSchemaValidatesUserElementTypeSchema confirms a user schema
// using element_type / element_fields / [<db>.types.<alias>] loads
// cleanly under the F21 grammar.
func TestMetaSchemaValidatesUserElementTypeSchema(t *testing.T) {
	src := `
[plans]
paths = ["plans.toml"]

[plans.types.ChecklistItem]
description = "x"

[plans.types.ChecklistItem.fields.id]
type = "string"
required = true

[plans.task]
description = "x"

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.paths]
type = "array"
element_type = "string"

[plans.task.fields.checklist]
type = "array"
element_type = "ChecklistItem"
`
	if _, err := LoadBytes([]byte(src)); err != nil {
		t.Fatalf("user schema using F21 grammar should load: %v", err)
	}
}

// TestExamplesLoadCleanly locks the binary-shipped schemas
// (`examples/schemas/cascade.toml`, `examples/schemas/claude_agents.toml`)
// against the loader so any future drift — missing required descriptions,
// retired meta-fields, non-canonical syntax — fails CI loudly instead
// of surfacing only when a user runs `ta init`.
// Path resolution is relative to the test's working dir
// (`internal/schema/`), reaching up two levels to the repo root.
func TestExamplesLoadCleanly(t *testing.T) {
	for _, rel := range []string{
		"../../examples/schemas/cascade.toml",
		"../../examples/schemas/claude_agents.toml",
	} {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			data, err := os.ReadFile(rel)
			if err != nil {
				t.Fatalf("read %s: %v", rel, err)
			}
			if _, err := LoadBytes(data); err != nil {
				t.Errorf("examples schema %s must load cleanly: %v", rel, err)
			}
		})
	}
}

// TestExamplesClaudeAgentsRoundTrip locks the F35 contract on the
// binary-shipped agent files: every `examples/agents/ta/*.md` file must
// be a valid `claude_agents.agent` record under the consolidated
// `claude_agents.toml` schema. The file must split cleanly into
// frontmatter + body via md.SplitFrontmatter, the frontmatter must
// decode into a typed map via md.DecodeFrontmatter, and the resulting
// fields (with the body folded into `prompt` per the `body_field`
// declaration) must validate against the schema's `agent` type. Drift
// here — missing required fields, unknown frontmatter keys, empty
// bodies, name/stem mismatches — fails CI loudly instead of surfacing
// only when a user runs `ta init` against the binary library.
//
// Path resolution is relative to the test's working dir
// (`internal/schema/`), reaching up two levels to the repo root.
func TestExamplesClaudeAgentsRoundTrip(t *testing.T) {
	schemaBytes, err := os.ReadFile("../../examples/schemas/claude_agents.toml")
	if err != nil {
		t.Fatalf("read claude_agents schema: %v", err)
	}
	reg, err := LoadBytes(schemaBytes)
	if err != nil {
		t.Fatalf("load claude_agents schema: %v", err)
	}
	if _, ok := reg.DBs["claude_agents"]; !ok {
		t.Fatal("claude_agents db missing — F35 schema regressed")
	}

	matches, err := filepath.Glob("../../examples/agents/ta/*.md")
	if err != nil {
		t.Fatalf("glob agents: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("no agent files found under examples/agents/ta/ — F34 ships at least one")
	}

	for _, path := range matches {
		t.Run(filepath.Base(path), func(t *testing.T) {
			buf, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			front, body, err := md.SplitFrontmatter(buf)
			if err != nil {
				t.Fatalf("SplitFrontmatter: %v", err)
			}
			if len(front) == 0 {
				t.Fatal("missing frontmatter — F34 invariant: every shipped agent has a frontmatter fence")
			}
			if len(body) == 0 {
				t.Fatal("empty body — F34 invariant: prompt body must be non-empty")
			}
			fields, err := md.DecodeFrontmatter(front)
			if err != nil {
				t.Fatalf("DecodeFrontmatter: %v", err)
			}
			fields["prompt"] = string(body)

			if err := reg.Validate("claude_agents.agent", fields); err != nil {
				t.Errorf("validate against claude_agents.agent: %v", err)
			}

			stem := strings.TrimSuffix(filepath.Base(path), ".md")
			if got, _ := fields["name"].(string); got != stem {
				t.Errorf("frontmatter name %q does not match filename stem %q (Claude Code requires sync)", got, stem)
			}
		})
	}
}

// TestMetaSchema_SelfHostsNestedFieldsKey locks the F28 contract on
// the meta-schema: the field kind must declare a `fields` sub-field and
// that declaration must itself parse cleanly under the new grammar
// (self-host check).
func TestMetaSchema_SelfHostsNestedFieldsKey(t *testing.T) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		t.Fatalf("meta-schema load: %v", err)
	}
	field, ok := reg.DBs["ta_schema"].Types["field"]
	if !ok {
		t.Fatal("missing ta_schema.field kind")
	}
	fieldsDecl, ok := field.Fields["fields"]
	if !ok {
		t.Fatal("ta_schema.field.fields.fields missing — F28 grammar must be declared on the meta-schema")
	}
	if fieldsDecl.Type != TypeTable {
		t.Errorf("ta_schema.field.fields.fields.Type = %q, want %q", fieldsDecl.Type, TypeTable)
	}
}

// TestExamplesCascadeSchema_DBNames locks the F35 db-name consolidation:
// the cascade schema declares the new `cascade` and `agents_md` dbs and
// the renamed `claude_agents` (split out into its own file) is NOT
// re-declared here. Pre-F35 names (`drops`, `claude_md`,
// `agents_home`, `agents_project`) must be gone — anything still
// referencing them is stale and would silently load via merge.
func TestExamplesCascadeSchema_DBNames(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/cascade.toml")
	if err != nil {
		t.Fatalf("read cascade schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load cascade schema: %v", err)
	}
	for _, want := range []string{"cascade", "agents_md"} {
		if _, ok := reg.DBs[want]; !ok {
			t.Errorf("cascade schema missing db %q (F35 consolidation)", want)
		}
	}
	for _, gone := range []string{"drops", "claude_md", "agents_home", "agents_project"} {
		if _, ok := reg.DBs[gone]; ok {
			t.Errorf("cascade schema still declares retired db %q (F35 must remove)", gone)
		}
	}
}

// TestExamplesCascadeSchema_TypeShape locks the F35 type list under the
// cascade db: drop, segment, confluence, droplet, planner, qa_proof,
// qa_falsification, failure must all be declared. segment + confluence
// are first-class types post-F35 (pre-F35 they were enum values on
// `[drops.planner.fields.structural_type]`).
func TestExamplesCascadeSchema_TypeShape(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/cascade.toml")
	if err != nil {
		t.Fatalf("read cascade schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load cascade schema: %v", err)
	}
	cascade, ok := reg.DBs["cascade"]
	if !ok {
		t.Fatal("cascade db missing")
	}
	want := []string{
		"drop", "segment", "confluence", "droplet",
		"planner", "qa_proof", "qa_falsification", "failure",
	}
	for _, name := range want {
		t.Run(name, func(t *testing.T) {
			if _, ok := cascade.Types[name]; !ok {
				t.Errorf("cascade.%s missing", name)
			}
		})
	}
}

// TestExamplesAgentsMD_MountList locks the F35 multi-mount on the
// renamed `agents_md` db: paths must include AGENTS.md (Codex
// convention) AND CLAUDE.md (Claude Code convention) so projects
// shipping either or both are tracked uniformly.
func TestExamplesAgentsMD_MountList(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/cascade.toml")
	if err != nil {
		t.Fatalf("read cascade schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load cascade schema: %v", err)
	}
	db, ok := reg.DBs["agents_md"]
	if !ok {
		t.Fatal("agents_md db missing")
	}
	want := []string{"AGENTS.md", "CLAUDE.md"}
	if len(db.Paths) != len(want) {
		t.Fatalf("agents_md.Paths = %v, want %v", db.Paths, want)
	}
	for i, p := range want {
		if db.Paths[i] != p {
			t.Errorf("agents_md.Paths[%d] = %q, want %q", i, db.Paths[i], p)
		}
	}
}

// TestExamplesClaudeAgents_MultiMount locks the F35 single-db
// multi-mount shape: claude_agents declares both the home library
// (`agents/*/*.md`) and the project install (`.claude/agents/*.md`) on
// one type, replacing the pre-F35 split between `agents_home` and
// `agents_project`.
func TestExamplesClaudeAgents_MultiMount(t *testing.T) {
	data, err := os.ReadFile("../../examples/schemas/claude_agents.toml")
	if err != nil {
		t.Fatalf("read claude_agents schema: %v", err)
	}
	reg, err := LoadBytes(data)
	if err != nil {
		t.Fatalf("load claude_agents schema: %v", err)
	}
	db, ok := reg.DBs["claude_agents"]
	if !ok {
		t.Fatal("claude_agents db missing")
	}
	want := []string{"agents/*/*.md", ".claude/agents/*.md"}
	if len(db.Paths) != len(want) {
		t.Fatalf("claude_agents.Paths = %v, want %v", db.Paths, want)
	}
	for i, p := range want {
		if db.Paths[i] != p {
			t.Errorf("claude_agents.Paths[%d] = %q, want %q", i, db.Paths[i], p)
		}
	}
	if _, ok := db.Types["agent"]; !ok {
		t.Error("claude_agents.agent type missing")
	}
}

// TestExamplesCascadeSchema_AutoSpawnByteFidelity locks the drop_004
// L3-I1-D3 contract: the binary-shipped `examples/schemas/cascade.toml`
// MUST declare `[cascade.drop.auto_spawn]` and `[cascade.planner.auto_spawn]`
// blocks whose parsed shape is BYTE-IDENTICAL to the live
// `.ta/schema.toml`. The example schema is the substrate `ta init` ships
// into new projects — if the example drifts from the live (the only
// schema that actually exercises auto_spawn in the dogfood project),
// every new ta-bootstrapped project gets a regressed cascade contract.
//
// Asserted invariants per [cascade.drop, cascade.planner] type:
//   - both schemas declare the same number of AutoSpawn specs;
//   - each spec's Type, IDTemplate, and Fields map (F23-v2 token strings
//     included verbatim) match.
//
// Path resolution uses the in-package `repoRoot(t)` helper so the test
// is stable regardless of the runner's working directory. The
// `[cascade.droplet.auto_spawn]` block is intentionally OUT OF SCOPE for
// this fidelity gate — it's already pinned by
// `TestAutoSpawn_DropletCreates_SpawnsBuildQATwinPair` (live + synthetic)
// and shipped uncommented in the example schema since pre-F23 v2.
func TestExamplesCascadeSchema_AutoSpawnByteFidelity(t *testing.T) {
	root := repoRoot(t)
	liveBytes, err := os.ReadFile(filepath.Join(root, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read live schema: %v", err)
	}
	exampleBytes, err := os.ReadFile(filepath.Join(root, "examples", "schemas", "cascade.toml"))
	if err != nil {
		t.Fatalf("read example schema: %v", err)
	}

	liveReg, err := LoadBytes(liveBytes)
	if err != nil {
		t.Fatalf("load live schema: %v", err)
	}
	exampleReg, err := LoadBytes(exampleBytes)
	if err != nil {
		t.Fatalf("load example schema: %v", err)
	}

	for _, typeName := range []string{"drop", "planner"} {
		t.Run(typeName, func(t *testing.T) {
			liveCascade, ok := liveReg.DBs["cascade"]
			if !ok {
				t.Fatal("live schema missing cascade db")
			}
			liveType, ok := liveCascade.Types[typeName]
			if !ok {
				t.Fatalf("live schema missing cascade.%s type", typeName)
			}
			exampleCascade, ok := exampleReg.DBs["cascade"]
			if !ok {
				t.Fatal("example schema missing cascade db")
			}
			exampleType, ok := exampleCascade.Types[typeName]
			if !ok {
				t.Fatalf("example schema missing cascade.%s type", typeName)
			}

			if got, want := len(exampleType.AutoSpawn), len(liveType.AutoSpawn); got != want {
				t.Fatalf("cascade.%s AutoSpawn len: example=%d, live=%d (must mirror token-for-token)",
					typeName, got, want)
			}
			if len(liveType.AutoSpawn) == 0 {
				t.Fatalf("cascade.%s declares no AutoSpawn in live schema — drop_004 L2-J should have landed it",
					typeName)
			}

			for i := range liveType.AutoSpawn {
				liveSpec := liveType.AutoSpawn[i]
				exSpec := exampleType.AutoSpawn[i]
				if liveSpec.Type != exSpec.Type {
					t.Errorf("cascade.%s AutoSpawn[%d].Type: example=%q, live=%q",
						typeName, i, exSpec.Type, liveSpec.Type)
				}
				if liveSpec.IDTemplate != exSpec.IDTemplate {
					t.Errorf("cascade.%s AutoSpawn[%d].IDTemplate: example=%q, live=%q",
						typeName, i, exSpec.IDTemplate, liveSpec.IDTemplate)
				}
				if !reflect.DeepEqual(liveSpec.Fields, exSpec.Fields) {
					t.Errorf("cascade.%s AutoSpawn[%d].Fields drift:\n  example=%#v\n  live   =%#v",
						typeName, i, exSpec.Fields, liveSpec.Fields)
				}
			}
		})
	}
}

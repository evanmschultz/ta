package initapply_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/evanmschultz/ta/internal/initapply"
	"github.com/evanmschultz/ta/internal/templates"
)

const plansSchema = `
[plans]
paths = ["plans.toml"]
description = "Planning db."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

func setupBinary(t *testing.T) {
	t.Helper()
	templates.SetBinarySource(fstest.MapFS{
		"examples/schemas/plans.toml":       &fstest.MapFile{Data: []byte(plansSchema)},
		"examples/agents/.keep":             &fstest.MapFile{Data: []byte("")},
		"examples/agents/go/builder.md":     &fstest.MapFile{Data: []byte("# go-builder\nbody\n")},
		"examples/configs/.keep":            &fstest.MapFile{Data: []byte("")},
		"examples/configs/mcp.json":         &fstest.MapFile{Data: []byte(`{"mcpServers":{"ta":{"command":"ta"}}}`)},
		"examples/configs/gitignore":        &fstest.MapFile{Data: []byte("dist/\n")},
		"examples/docs-templates/.keep":     &fstest.MapFile{Data: []byte("")},
		"examples/docs-templates/CLAUDE.md": &fstest.MapFile{Data: []byte("# CLAUDE\n")},
	})
	t.Cleanup(func() { templates.SetBinarySource(nil) })
}

func setupHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	restore := templates.SetRootForTest(home)
	t.Cleanup(restore)
	return home
}

func TestPreview_BinaryItemsListed(t *testing.T) {
	setupBinary(t)
	setupHome(t)
	target := t.TempDir()
	report, err := initapply.Preview(target)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if report.Available == nil {
		t.Fatal("Available nil")
	}
	if len(report.Available.Schemas) == 0 {
		t.Errorf("expected schemas")
	}
	if len(report.Available.Agents) == 0 {
		t.Errorf("expected agents")
	}
	if len(report.Available.Configs) == 0 {
		t.Errorf("expected configs")
	}
	if len(report.Available.DocsTemplates) == 0 {
		t.Errorf("expected docs templates")
	}
}

func TestApply_SchemaWritesToProjectTaSchema(t *testing.T) {
	// F32: empty-provenance + project target = home only. Pre-seed
	// home with the plans db so the strict-provenance resolver finds it.
	setupBinary(t)
	homeRoot := setupHome(t)
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Schemas.Written) != 1 || report.Schemas.Written[0] != "plans" {
		t.Errorf("Written = %v, want [plans]", report.Schemas.Written)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("schema body not written: %s", got)
	}
}

func TestApply_SchemaConflictPolicyError(t *testing.T) {
	// F32: home must hold the schema for empty-provenance + project target.
	// F38d-2.6 content-aware: pre-seed dest with a DIFFERENT plans body
	// than the home source so the comparison registers as drift (real
	// conflict). Identical bodies are now content-equivalent and land
	// in Unchanged, not Conflicts.
	setupBinary(t)
	homeRoot := setupHome(t)
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	target := t.TempDir()

	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// plansHomeOverride uses `paths = ["home-plans.toml"]` — distinct
	// from plansSchema's `["plans.toml"]` — so content-aware sees real
	// drift.
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(plansHomeOverride), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Schemas.Conflicts) != 1 {
		t.Errorf("Conflicts = %v, want [plans]", report.Schemas.Conflicts)
	}
	if len(report.Schemas.Written) != 0 {
		t.Errorf("error policy should leave Written empty: %v", report.Schemas.Written)
	}
}

func TestApply_SchemaConflictPolicyOverwrite(t *testing.T) {
	// F32: pre-seed home so empty-provenance resolves under strict policy.
	setupBinary(t)
	homeRoot := setupHome(t)
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	target := t.TempDir()

	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	old := "[plans]\npaths = [\"old.toml\"]\n[plans.task]\ndescription = \"old\"\n[plans.task.fields.id]\ntype = \"string\"\nrequired = true\n"
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(old), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyOverwrite)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Schemas.Written) != 1 {
		t.Errorf("expected overwrite to write")
	}
	got, _ := os.ReadFile(filepath.Join(taDir, "schema.toml"))
	if !strings.Contains(string(got), "plans.toml") {
		t.Errorf("overwrite did not pick up new content: %s", got)
	}
}

func TestApply_AgentsLandInClaudeAgents(t *testing.T) {
	// F33: project install flattens <group>/<name>.md → <group>-<name>.md
	// at the destination. Home library stays nested. Frontmatter `name`
	// is rewritten to match the flattened destination stem.
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Agents.Written) != 1 {
		t.Errorf("Agents.Written = %v", report.Agents.Written)
	}
	// F33: nested home → flat project leaf.
	if _, err := os.Stat(filepath.Join(target, ".claude", "agents", "go-builder.md")); err != nil {
		t.Errorf("agent not at flattened path: %v", err)
	}
	// F33: nested project layout MUST NOT exist post-flatten.
	if _, err := os.Stat(filepath.Join(target, ".claude", "agents", "go", "builder.md")); err == nil {
		t.Errorf("nested project agent path leaked despite F33 flatten")
	}
}

// agentWithName builds a minimal frontmatter+body agent fixture whose
// `name` field matches the supplied stem. Used by the F33 nested→flat
// tests to verify name-rewrite behavior end-to-end.
func agentWithName(name string) string {
	return "---\nname: " + name + "\ndescription: test agent\n---\nbody\n"
}

// TestApplyAgents_NestedToFlat_RewritesPath locks the F33 path-rewrite
// rule for project installs: nested home `agents/<group>/<name>.md` lands
// flat at `.claude/agents/<group>-<name>.md`.
func TestApplyAgents_NestedToFlat_RewritesPath(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyError); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".claude", "agents", "go-builder.md")); err != nil {
		t.Errorf("flat dest missing: %v", err)
	}
}

// TestApplyAgents_NestedToFlat_RewritesFrontmatterName locks the
// frontmatter `name`-field rewrite. The on-disk filename and the
// frontmatter `name` MUST stay in sync after flattening — Claude Code's
// agent loader keys off the frontmatter name, not the filename.
func TestApplyAgents_NestedToFlat_RewritesFrontmatterName(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyError); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, ".claude", "agents", "go-builder.md"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "name: go-builder") {
		t.Errorf("frontmatter name not rewritten to go-builder: %s", got)
	}
	if strings.Contains(string(got), "name: builder\n") {
		t.Errorf("original name survived rewrite: %s", got)
	}
	// Body must survive the round-trip.
	if !strings.Contains(string(got), "body\n") {
		t.Errorf("body lost during frontmatter rewrite: %s", got)
	}
}

// TestApplyAgents_FlattenCollision_Errors locks the F33 collision-detection
// rule. Two source agents whose flattened destinations resolve to the
// same on-disk leaf must surface ErrFlattenCollision before any write.
// Auto-renaming would silently shadow one source.
func TestApplyAgents_FlattenCollision_Errors(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	// Two distinct sources that BOTH flatten to `go-builder.md`:
	//   agents/go/builder.md          → go-builder.md
	//   agents/go-builder/.md (n/a — group cannot end in hyphen here);
	// instead use a synthetic ungrouped agent literally named
	// "go-builder" + a grouped agent {go, builder}. Both flatten to the
	// same dest leaf because the ungrouped path is `<name>.md` and the
	// grouped path is `<group>-<name>.md`.
	groupedSrc := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(groupedSrc), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(groupedSrc, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed grouped: %v", err)
	}
	flatSrc := filepath.Join(homeRoot, "agents", "go-builder.md")
	if err := os.WriteFile(flatSrc, []byte(agentWithName("go-builder")), 0o644); err != nil {
		t.Fatalf("seed flat: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{
			{Group: "go", Name: "builder"},
			{Name: "go-builder"},
		},
	}
	_, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err == nil {
		t.Fatal("expected ErrFlattenCollision, got nil")
	}
	if !errors.Is(err, initapply.ErrFlattenCollision) {
		t.Errorf("err = %v, want ErrFlattenCollision", err)
	}
	// Source-path identity in the message: the locked design says
	// conflict logs name the source path, not the flattened dest.
	if !strings.Contains(err.Error(), "go/builder") || !strings.Contains(err.Error(), "go-builder") {
		t.Errorf("collision error should name both source paths: %v", err)
	}
}

// TestApplyAgents_HomeTarget_StaysNested locks the `--bootstrap-home`
// path: home target keeps the nested layout because home IS the nested
// shape. No flatten, no frontmatter rewrite.
func TestApplyAgents_HomeTarget_StaysNested(t *testing.T) {
	setupBinary(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".ta")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	restore := templates.SetRootForTest(target)
	t.Cleanup(restore)

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyOverwrite); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Home target → nested.
	if _, err := os.Stat(filepath.Join(target, "agents", "go", "builder.md")); err != nil {
		t.Errorf("home agent not at nested path: %v", err)
	}
	// Home target must NOT flatten.
	if _, err := os.Stat(filepath.Join(target, "agents", "go-builder.md")); err == nil {
		t.Errorf("home target accidentally flattened")
	}
	// Frontmatter must NOT be rewritten on home target — body should
	// match the binary fragment seeded by setupBinary verbatim.
	got, err := os.ReadFile(filepath.Join(target, "agents", "go", "builder.md"))
	if err != nil {
		t.Fatalf("read home agent: %v", err)
	}
	if string(got) != "# go-builder\nbody\n" {
		t.Errorf("home agent rewritten unexpectedly: %q", got)
	}
}

// TestApplyAgents_UngroupedAgent_NoRewrite locks the empty-group case.
// An ungrouped (top-level) agent has no group prefix to flatten in, so
// path stays `<name>.md` and frontmatter `name` stays unchanged.
func TestApplyAgents_UngroupedAgent_NoRewrite(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "solo.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("solo")), 0o644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Name: "solo"}},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyError); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	dest := filepath.Join(target, ".claude", "agents", "solo.md")
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// Path stays `<name>.md`, frontmatter `name` stays `solo`.
	if !strings.Contains(string(got), "name: solo\n") {
		t.Errorf("ungrouped agent name unexpectedly rewritten: %s", got)
	}
}

// TestApplyAgents_FrontmatterMissingNameField_LoudFails locks the QA-
// falsifier follow-up: when source frontmatter exists but lacks a
// required `name:` field, F33 must error rather than silently
// synthesize one. Claude Code's subagent contract requires `name`;
// silently synthesizing would mask schema-authoring bugs.
func TestApplyAgents_FrontmatterMissingNameField_LoudFails(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Frontmatter present but no `name:` field.
	body := "---\ndescription: missing name\n---\nbody\n"
	if err := os.WriteFile(homeAgent, []byte(body), 0o644); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	_, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err == nil {
		t.Fatal("expected error for missing name field, got nil")
	}
	if !strings.Contains(err.Error(), "missing required `name:` field") {
		t.Errorf("error message should name the missing field, got: %v", err)
	}
}

func TestApply_HomeRootReroutes(t *testing.T) {
	// IsHomeRoot returns true when target == $HOME/.ta.
	setupBinary(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".ta")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	restore := templates.SetRootForTest(target)
	t.Cleanup(restore)

	if !initapply.IsHomeRoot(target) {
		t.Fatal("IsHomeRoot should be true for $HOME/.ta")
	}

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	if _, err := initapply.Apply(target, sel, initapply.PolicyOverwrite); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Home target writes directly to $HOME/.ta/schema.toml, not .ta/.ta/.
	if _, err := os.Stat(filepath.Join(target, "schema.toml")); err != nil {
		t.Errorf("home schema not written at root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, ".ta", "schema.toml")); err == nil {
		t.Errorf("schema accidentally written under nested .ta/")
	}
}

func TestApply_ConfigStructuredMerge(t *testing.T) {
	// F32: empty-provenance + project target = home only. Pre-seed
	// home/configs/mcp.json so the resolver finds it.
	setupBinary(t)
	homeRoot := setupHome(t)
	homeMCP := filepath.Join(homeRoot, "configs", "mcp.json")
	if err := os.MkdirAll(filepath.Dir(homeMCP), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeMCP, []byte(`{"mcpServers":{"ta":{"command":"ta"}}}`), 0o644); err != nil {
		t.Fatalf("seed home config: %v", err)
	}
	target := t.TempDir()

	// Pre-seed .mcp.json with a different mcpServers entry — merge
	// must add `ta` from the home template and preserve `other`.
	existing := `{"mcpServers":{"other":{"command":"other"}}}`
	if err := os.WriteFile(filepath.Join(target, ".mcp.json"), []byte(existing), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := initapply.Selections{Configs: []initapply.ConfigSelection{{Name: "mcp.json"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Configs.Written) != 1 {
		t.Errorf("merge should produce a write: %+v", report.Configs)
	}
	got, _ := os.ReadFile(filepath.Join(target, ".mcp.json"))
	if !strings.Contains(string(got), "other") || !strings.Contains(string(got), `"ta"`) {
		t.Errorf("merge did not preserve+add: %s", got)
	}
}

func TestApply_DocsTemplateError(t *testing.T) {
	// F32: empty-provenance + project target = home only. Pre-seed
	// home/docs-templates/CLAUDE.md so the resolver finds it.
	setupBinary(t)
	homeRoot := setupHome(t)
	homeDocs := filepath.Join(homeRoot, "docs-templates", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(homeDocs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeDocs, []byte("# CLAUDE\n"), 0o644); err != nil {
		t.Fatalf("seed home docs: %v", err)
	}
	target := t.TempDir()
	// Pre-seed CLAUDE.md at destination.
	if err := os.WriteFile(filepath.Join(target, "CLAUDE.md"), []byte("# old\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := initapply.Selections{DocsTemplates: []initapply.DocsSelection{{Name: "CLAUDE"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.DocsTemplates.Conflicts) != 1 {
		t.Errorf("expected conflict, got %+v", report.DocsTemplates)
	}
	got, _ := os.ReadFile(filepath.Join(target, "CLAUDE.md"))
	if string(got) != "# old\n" {
		t.Errorf("error policy should leave file untouched: %s", got)
	}
}

func TestApply_DocsTemplateSkip(t *testing.T) {
	// F32: empty-provenance + project target = home only.
	setupBinary(t)
	homeRoot := setupHome(t)
	homeDocs := filepath.Join(homeRoot, "docs-templates", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(homeDocs), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeDocs, []byte("# CLAUDE\n"), 0o644); err != nil {
		t.Fatalf("seed home docs: %v", err)
	}
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "CLAUDE.md"), []byte("# old\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	sel := initapply.Selections{DocsTemplates: []initapply.DocsSelection{{Name: "CLAUDE"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicySkip)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.DocsTemplates.Skipped) != 1 || report.DocsTemplates.Skipped[0] != "CLAUDE" {
		t.Errorf("Skipped = %v", report.DocsTemplates.Skipped)
	}
	got, _ := os.ReadFile(filepath.Join(target, "CLAUDE.md"))
	if string(got) != "# old\n" {
		t.Errorf("skip policy should leave file untouched: %s", got)
	}
}

func TestApply_AggregateConflictsSorted(t *testing.T) {
	// F38d-2.3 enrichment: when Target is set, AggregateConflicts
	// appends the resolved destination path per conflict in parens.
	// Synthetic Target keeps the sort order deterministic across hosts.
	target := "/tmp/sorted-target"
	report := initapply.Report{
		Target:        target,
		Schemas:       initapply.Result{Conflicts: []string{"plans"}},
		Agents:        initapply.Result{Conflicts: []string{"go/builder"}},
		Configs:       initapply.Result{Conflicts: []string{"mcp.json"}},
		DocsTemplates: initapply.Result{Conflicts: []string{"CLAUDE"}},
	}
	got := initapply.AggregateConflicts(report)
	want := []string{
		"agent:go/builder (" + filepath.Join(target, ".claude", "agents", "go-builder.md") + ")",
		"config:mcp.json (" + filepath.Join(target, ".mcp.json") + ")",
		"docs:CLAUDE (" + filepath.Join(target, "CLAUDE.md") + ")",
		"schema:plans (" + filepath.Join(target, ".ta", "schema.toml") + ")",
	}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
	}
}

// TestAggregateConflicts_EnrichesWithDestPath locks the F38d-2.3
// dest-path enrichment contract: each Conflict entry surfaces both its
// category-key AND its on-disk destination path so the user sees the
// concrete file that conflicted, not just the category. Asserts both
// names and both paths appear in the aggregated output for a Report
// carrying two conflicts (schema + agent).
func TestAggregateConflicts_EnrichesWithDestPath(t *testing.T) {
	target := "/tmp/enriched-target"
	report := initapply.Report{
		Target:  target,
		Schemas: initapply.Result{Conflicts: []string{"plans"}},
		Agents:  initapply.Result{Conflicts: []string{"go/builder"}},
	}
	got := initapply.AggregateConflicts(report)
	joined := strings.Join(got, " ")

	wantSchemaPath := filepath.Join(target, ".ta", "schema.toml")
	wantAgentPath := filepath.Join(target, ".claude", "agents", "go-builder.md")

	if !strings.Contains(joined, "schema:plans") {
		t.Errorf("aggregated output missing schema category-key: %v", got)
	}
	if !strings.Contains(joined, "agent:go/builder") {
		t.Errorf("aggregated output missing agent category-key: %v", got)
	}
	if !strings.Contains(joined, wantSchemaPath) {
		t.Errorf("aggregated output missing schema dest path %q: %v", wantSchemaPath, got)
	}
	if !strings.Contains(joined, wantAgentPath) {
		t.Errorf("aggregated output missing agent dest path %q: %v", wantAgentPath, got)
	}
	// Spot-check the paren shape so the wrapper format stays parseable.
	for _, entry := range got {
		if !strings.Contains(entry, " (") || !strings.HasSuffix(entry, ")") {
			t.Errorf("entry %q missing paren-enclosed dest path", entry)
		}
	}
}

// TestAggregateConflicts_EmptyTargetFallsBackToBareKey pins the
// fallback contract: when Target is empty (synthetic Report unused by
// production callers but legal for tests), AggregateConflicts emits the
// bare `<category>:<key>` form instead of a malformed enriched entry
// with a relative path leak.
func TestAggregateConflicts_EmptyTargetFallsBackToBareKey(t *testing.T) {
	report := initapply.Report{
		Schemas: initapply.Result{Conflicts: []string{"plans"}},
	}
	got := initapply.AggregateConflicts(report)
	if len(got) != 1 || got[0] != "schema:plans" {
		t.Errorf("got %v, want [schema:plans]", got)
	}
}

func TestParsePolicy_Defaults(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want initapply.Policy
		bad  bool
	}{
		{"", initapply.PolicyError, false},
		{"error", initapply.PolicyError, false},
		{"skip", initapply.PolicySkip, false},
		{"overwrite", initapply.PolicyOverwrite, false},
		{"force", initapply.PolicyForce, false},
		{"banana", "", true},
	} {
		got, err := initapply.ParsePolicy(tc.in)
		if tc.bad {
			if err == nil {
				t.Errorf("ParsePolicy(%q) want err", tc.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePolicy(%q) err = %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParsePolicy(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestSelectionsFromJSON_RoundTrip(t *testing.T) {
	// Legacy string form for every list (back-compat with pre-F24-P1.A
	// callers and selections-file consumers).
	in := `{"schemas":["plans"],"agents":[{"group":"go","name":"b"}],"configs":["mcp.json"],"docs-templates":["CLAUDE"],"on_conflict":"skip"}`
	sel, err := initapply.SelectionsFromJSON([]byte(in))
	if err != nil {
		t.Fatalf("SelectionsFromJSON: %v", err)
	}
	if len(sel.Schemas) != 1 || sel.Schemas[0].Name != "plans" || sel.Schemas[0].Provenance != "" {
		t.Errorf("schemas: %+v", sel.Schemas)
	}
	if len(sel.Configs) != 1 || sel.Configs[0].Name != "mcp.json" || sel.Configs[0].Provenance != "" {
		t.Errorf("configs: %+v", sel.Configs)
	}
	if len(sel.DocsTemplates) != 1 || sel.DocsTemplates[0].Name != "CLAUDE" || sel.DocsTemplates[0].Provenance != "" {
		t.Errorf("docs-templates: %+v", sel.DocsTemplates)
	}
	if sel.Agents[0].Group != "go" || sel.Agents[0].Name != "b" || sel.Agents[0].Provenance != "" {
		t.Errorf("agents: %+v", sel.Agents)
	}
	if sel.OnConflict != "skip" {
		t.Errorf("on_conflict: %q", sel.OnConflict)
	}
}

// plansHomeOverride differs from plansSchema in the path so the
// P1.A test can distinguish "binary fragment landed" from "home copy
// shadowed it". Same db name, same field shape, different `paths`.
const plansHomeOverride = `
[plans]
paths = ["home-plans.toml"]
description = "Home-shadowed plans."

[plans.task]
description = "A unit of work."

[plans.task.fields.id]
type = "string"
required = true

[plans.task.fields.status]
type = "string"
required = true
`

// TestApply_SchemaProvenancePinSelectsBinary is the P1.A counterexample.
// Without provenance threading, the resolver always tries home first and
// the binary fragment is silently shadowed when the same Name exists in
// both sources. With provenance="ta" the binary copy must land verbatim.
func TestApply_SchemaProvenancePinSelectsBinary(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	// Pre-seed home with a plans db that differs from the binary
	// fragment (different paths). Without explicit provenance the home
	// copy wins via fallback; with provenance="ta" we want the binary.
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansHomeOverride), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Schemas: []initapply.SchemaSelection{
			{Name: "plans", Provenance: string(templates.ProvenanceBinary)},
		},
	}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Schemas.Written) != 1 {
		t.Errorf("Written = %v, want [plans]", report.Schemas.Written)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	// Binary fragment uses `plans.toml`; home shadow uses
	// `home-plans.toml`. Provenance pin must pick the binary. The
	// merged output goes through go-toml/v2 which may emit literal
	// strings with single quotes — check the substring without
	// committing to a specific quote style.
	if !strings.Contains(string(got), "plans.toml") {
		t.Errorf("expected binary fragment paths to land, got: %s", got)
	}
	if strings.Contains(string(got), "home-plans.toml") {
		t.Errorf("home shadow leaked despite provenance=ta: %s", got)
	}
}

// TestApply_SchemaProvenancePinSelectsHome confirms the symmetric pin
// for home — useful when an admin wants to assert the home copy
// authoritatively even on a fresh tree.
func TestApply_SchemaProvenancePinSelectsHome(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansHomeOverride), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{
		Schemas: []initapply.SchemaSelection{
			{Name: "plans", Provenance: string(templates.ProvenanceHome)},
		},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyError); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "home-plans.toml") {
		t.Errorf("expected home shadow paths, got: %s", got)
	}
}

// TestApply_UnknownConfigOverwriteWritesVerbatim is the P1.B
// counterexample. An "unknown" config name has no merger registered.
// Pre-fix, a destination conflict on an unknown config + policy=overwrite
// silently no-op'd: Conflicts was populated, Written stayed empty. Post-fix
// the user's explicit overwrite policy must result in a verbatim raw
// write at the destination.
func TestApply_UnknownConfigOverwriteWritesVerbatim(t *testing.T) {
	// Inject a known-but-unmerge-able config name into the binary
	// library so resolveConfigBytes can find it.
	templates.SetBinarySource(fstest.MapFS{
		"examples/schemas/plans.toml":   &fstest.MapFile{Data: []byte(plansSchema)},
		"examples/agents/.keep":         &fstest.MapFile{Data: []byte("")},
		"examples/configs/.keep":        &fstest.MapFile{Data: []byte("")},
		"examples/configs/foo.toml":     &fstest.MapFile{Data: []byte("[foo]\nnew = true\n")},
		"examples/docs-templates/.keep": &fstest.MapFile{Data: []byte("")},
	})
	t.Cleanup(func() { templates.SetBinarySource(nil) })
	setupHome(t)

	target := t.TempDir()
	// Pre-seed the destination — unknown configs route to
	// `<target>/<name>` per configDestPath default.
	dest := filepath.Join(target, "foo.toml")
	if err := os.WriteFile(dest, []byte("[foo]\nold = true\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// F32: pin to binary explicitly — empty-provenance + project target
	// is now home-only and would error since this unknown config only
	// lives on the binary side.
	sel := initapply.Selections{Configs: []initapply.ConfigSelection{{Name: "foo.toml", Provenance: string(templates.ProvenanceBinary)}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyOverwrite)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Configs.Conflicts) != 1 || report.Configs.Conflicts[0] != "foo.toml" {
		t.Errorf("Conflicts = %v, want [foo.toml]", report.Configs.Conflicts)
	}
	if len(report.Configs.Written) != 1 || report.Configs.Written[0] != "foo.toml" {
		t.Errorf("Written = %v, want [foo.toml] (overwrite must surface a write)", report.Configs.Written)
	}
	got, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(got), "new = true") {
		t.Errorf("dest not overwritten: %s", got)
	}
	if strings.Contains(string(got), "old = true") {
		t.Errorf("old content survived: %s", got)
	}
}

// TestApply_UnknownConfigForceWritesVerbatim mirrors the overwrite test
// for the `force` policy — same code path, separate enum value. Locking
// both prevents a one-policy regression.
func TestApply_UnknownConfigForceWritesVerbatim(t *testing.T) {
	templates.SetBinarySource(fstest.MapFS{
		"examples/schemas/plans.toml":   &fstest.MapFile{Data: []byte(plansSchema)},
		"examples/agents/.keep":         &fstest.MapFile{Data: []byte("")},
		"examples/configs/.keep":        &fstest.MapFile{Data: []byte("")},
		"examples/configs/foo.toml":     &fstest.MapFile{Data: []byte("forced\n")},
		"examples/docs-templates/.keep": &fstest.MapFile{Data: []byte("")},
	})
	t.Cleanup(func() { templates.SetBinarySource(nil) })
	setupHome(t)

	target := t.TempDir()
	dest := filepath.Join(target, "foo.toml")
	if err := os.WriteFile(dest, []byte("preexisting\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// F32: pin to binary explicitly under strict-provenance.
	sel := initapply.Selections{Configs: []initapply.ConfigSelection{{Name: "foo.toml", Provenance: string(templates.ProvenanceBinary)}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyForce)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Configs.Written) != 1 {
		t.Errorf("force should write: %+v", report.Configs)
	}
	got, _ := os.ReadFile(dest)
	if string(got) != "forced\n" {
		t.Errorf("dest = %q, want %q", got, "forced\n")
	}
}

// TestSelectionsFromJSON_ProvenanceObjectForm exercises the new
// object-shape per item, including a mix with bare-string entries.
// This locks the P1.A wire-format extension for round-trip.
func TestSelectionsFromJSON_ProvenanceObjectForm(t *testing.T) {
	in := `{
        "schemas":[{"name":"plans","provenance":"ta"},"notes"],
        "agents":[{"group":"go","name":"b","provenance":"home"}],
        "configs":[{"name":"mcp.json","provenance":"ta"}],
        "docs-templates":[{"name":"CLAUDE","provenance":"home"}]
    }`
	sel, err := initapply.SelectionsFromJSON([]byte(in))
	if err != nil {
		t.Fatalf("SelectionsFromJSON: %v", err)
	}
	if len(sel.Schemas) != 2 {
		t.Fatalf("schemas len = %d, want 2", len(sel.Schemas))
	}
	if sel.Schemas[0].Name != "plans" || sel.Schemas[0].Provenance != "ta" {
		t.Errorf("schemas[0]: %+v", sel.Schemas[0])
	}
	if sel.Schemas[1].Name != "notes" || sel.Schemas[1].Provenance != "" {
		t.Errorf("schemas[1]: %+v", sel.Schemas[1])
	}
	if sel.Agents[0].Provenance != "home" {
		t.Errorf("agents[0].Provenance = %q, want home", sel.Agents[0].Provenance)
	}
	if sel.Configs[0].Provenance != "ta" {
		t.Errorf("configs[0].Provenance = %q, want ta", sel.Configs[0].Provenance)
	}
	if sel.DocsTemplates[0].Provenance != "home" {
		t.Errorf("docs-templates[0].Provenance = %q, want home", sel.DocsTemplates[0].Provenance)
	}
}

// ---- F32 strict-provenance tests ----------------------------------

// TestApply_EmptyHome_ProjectInit_Errors locks the F32 strict-provenance
// rule: empty-provenance + project target requires a populated home.
// Empty home + project target must fail-fast with a friendly error
// pointing at `ta init --bootstrap-home`, never silently fall back to
// the binary library.
func TestApply_EmptyHome_ProjectInit_Errors(t *testing.T) {
	setupBinary(t)
	setupHome(t) // home root exists but has no schema.toml / agents/ / etc.
	target := t.TempDir()

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	_, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err == nil {
		t.Fatal("expected error when home is empty and target is project")
	}
	msg := err.Error()
	if !strings.Contains(msg, "home library is empty") {
		t.Errorf("error should name the empty-home condition: %v", err)
	}
	if !strings.Contains(msg, "ta init --bootstrap-home") {
		t.Errorf("error should point at `ta init --bootstrap-home`: %v", err)
	}
}

// TestApply_EmptyHome_HomeInit_PopulatesFromBinary covers the canonical
// `ta init --bootstrap-home` flow: target IS $HOME/.ta, empty-provenance
// resolves to binary-only so the home library can be bootstrapped from
// shipped defaults.
func TestApply_EmptyHome_HomeInit_PopulatesFromBinary(t *testing.T) {
	setupBinary(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".ta")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	restore := templates.SetRootForTest(target)
	t.Cleanup(restore)

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyOverwrite)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Schemas.Written) != 1 || report.Schemas.Written[0] != "plans" {
		t.Errorf("Written = %v, want [plans]", report.Schemas.Written)
	}
	// Home target writes directly to $HOME/.ta/schema.toml.
	got, err := os.ReadFile(filepath.Join(target, "schema.toml"))
	if err != nil {
		t.Fatalf("read home schema: %v", err)
	}
	if !strings.Contains(string(got), "[plans]") {
		t.Errorf("binary fragment did not land in home: %s", got)
	}
}

// TestApply_PopulatedHome_ProjectInit_UsesHomeOnly: when home is
// populated and target is a project, empty-provenance resolves to home
// only — never to binary. A binary fragment with the same name must not
// shadow the home copy under the new strict policy.
func TestApply_PopulatedHome_ProjectInit_UsesHomeOnly(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansHomeOverride), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	target := t.TempDir()

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(report.Schemas.Written) != 1 {
		t.Errorf("Written = %v, want [plans]", report.Schemas.Written)
	}
	got, err := os.ReadFile(filepath.Join(target, ".ta", "schema.toml"))
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	// home plansHomeOverride uses `home-plans.toml` — the binary fragment
	// uses `plans.toml`. Strict-provenance must keep the home copy.
	if !strings.Contains(string(got), "home-plans.toml") {
		t.Errorf("expected home-only fragment, got: %s", got)
	}
	if strings.Contains(string(got), `paths = ["plans.toml"]`) {
		t.Errorf("binary fragment leaked into project despite populated home: %s", got)
	}
}

// TestApply_PopulatedHome_TargetingHome_OverwritesPolicyApplies
// confirms `--bootstrap-home` against an already-populated home respects
// `--on-conflict`. Default error policy must surface a conflict; an
// explicit overwrite refreshes the home copy from binary.
func TestApply_PopulatedHome_TargetingHome_OverwritesPolicyApplies(t *testing.T) {
	setupBinary(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	target := filepath.Join(home, ".ta")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(target, "schema.toml"), []byte(plansHomeOverride), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(target)
	t.Cleanup(restore)

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}

	// Default error policy must surface a conflict.
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply (error policy): %v", err)
	}
	if len(report.Schemas.Conflicts) != 1 {
		t.Errorf("error policy must surface conflict, got %+v", report.Schemas)
	}
	if len(report.Schemas.Written) != 0 {
		t.Errorf("error policy must not write, got %+v", report.Schemas)
	}

	// Overwrite policy must refresh from binary.
	report, err = initapply.Apply(target, sel, initapply.PolicyOverwrite)
	if err != nil {
		t.Fatalf("Apply (overwrite): %v", err)
	}
	if len(report.Schemas.Written) != 1 {
		t.Errorf("overwrite policy must write, got %+v", report.Schemas)
	}
	got, err := os.ReadFile(filepath.Join(target, "schema.toml"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	// After overwrite the binary fragment (`plans.toml`) must replace
	// the prior home shadow (`home-plans.toml`).
	if !strings.Contains(string(got), "plans.toml") {
		t.Errorf("expected refreshed binary fragment, got: %s", got)
	}
	if strings.Contains(string(got), "home-plans.toml") {
		t.Errorf("home shadow survived overwrite: %s", got)
	}
}

// ---- F38d-2.5 atomic preflight tests -------------------------------

// TestInitApply_AtomicityOnConflict_NoPartialWrite locks the F38d-2.5
// pre-scan contract: when ANY destination conflicts under PolicyError,
// NO write touches disk anywhere — schema, agent, config, or docs. The
// pre-fix failure had .ta/schema.toml landing successfully before the
// agent write conflict surfaced; this test pins the new atomic behavior.
func TestInitApply_AtomicityOnConflict_NoPartialWrite(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	// Seed home schema so empty-provenance + project target resolves.
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	// Seed home agent so the agent selection has a resolvable source.
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir home agent: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed home agent: %v", err)
	}

	target := t.TempDir()
	// Pre-populate ONLY the agent dest path. Schema dest is untouched —
	// under the pre-fix sequential apply, the schema would land first.
	agentDest := filepath.Join(target, ".claude", "agents", "go-builder.md")
	if err := os.MkdirAll(filepath.Dir(agentDest), 0o755); err != nil {
		t.Fatalf("mkdir agent dest: %v", err)
	}
	if err := os.WriteFile(agentDest, []byte("preexisting agent\n"), 0o644); err != nil {
		t.Fatalf("seed agent dest: %v", err)
	}

	sel := initapply.Selections{
		Schemas: []initapply.SchemaSelection{{Name: "plans"}},
		Agents:  []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	// Option (b) contract: Apply returns nil error; conflicts ride on
	// the Report so the cmd/ta wrapper inspects Report.<Cat>.Conflicts.
	if err != nil {
		t.Fatalf("Apply: expected nil error under option (b), got %v", err)
	}
	// Agent conflict must be reported.
	if len(report.Agents.Conflicts) != 1 || report.Agents.Conflicts[0] != "go/builder" {
		t.Errorf("Agents.Conflicts = %v, want [go/builder]", report.Agents.Conflicts)
	}
	// Atomicity: schema dest must NOT have been written despite
	// preceding the agent in the apply order.
	if _, statErr := os.Stat(filepath.Join(target, ".ta", "schema.toml")); statErr == nil {
		t.Errorf("schema.toml landed despite agent conflict — atomic preflight regressed")
	}
	// Atomicity: no per-category Written entries anywhere.
	if len(report.Schemas.Written) != 0 {
		t.Errorf("Schemas.Written = %v, want empty under atomic preflight", report.Schemas.Written)
	}
	if len(report.Agents.Written) != 0 {
		t.Errorf("Agents.Written = %v, want empty", report.Agents.Written)
	}
	// Pre-existing agent file must survive untouched.
	got, readErr := os.ReadFile(agentDest)
	if readErr != nil {
		t.Fatalf("read agent dest: %v", readErr)
	}
	if string(got) != "preexisting agent\n" {
		t.Errorf("pre-existing agent mutated: %q", got)
	}
}

// TestInitApply_ApplyReturnsNilOnPolicyErrorWithConflicts pins the
// option (b) error-contract end-to-end: Apply returns nil; the populated
// Report.<Cat>.Conflicts flow through AggregateConflicts to produce the
// exact user-facing error string that cmd/ta/init_multi.go formats.
// Locking the format here keeps the CLI wrapper firing unchanged.
func TestInitApply_ApplyReturnsNilOnPolicyErrorWithConflicts(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	target := t.TempDir()
	taDir := filepath.Join(target, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// F38d-2.6 content-aware: seed dest with a DIFFERENT plans body
	// (plansHomeOverride uses distinct `paths`) so the comparator
	// surfaces a real conflict — identical bytes would now land in
	// Unchanged, not Conflicts.
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(plansHomeOverride), 0o644); err != nil {
		t.Fatalf("seed schema dest: %v", err)
	}

	sel := initapply.Selections{Schemas: []initapply.SchemaSelection{{Name: "plans"}}}
	report, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("Apply: expected nil error under option (b), got %v", err)
	}

	// Mirror cmd/ta/init_multi.go:43-49 verbatim so any drift in that
	// wrapper format surfaces here.
	conflicts := initapply.AggregateConflicts(report)
	if len(conflicts) == 0 {
		t.Fatal("AggregateConflicts: expected at least one conflict, got 0")
	}
	wrapperErr := fmt.Errorf("init: %d conflict(s); re-run with --on-conflict=skip|overwrite|force: %s",
		len(conflicts), strings.Join(conflicts, ", "))

	// F38d-2.3: enriched output includes the resolved dest path. The
	// schema path is target-relative so we build the expectation from
	// the real tmp target.
	wantPath := filepath.Join(target, ".ta", "schema.toml")
	want := "init: 1 conflict(s); re-run with --on-conflict=skip|overwrite|force: schema:plans (" + wantPath + ")"
	if wrapperErr.Error() != want {
		t.Errorf("wrapper error = %q, want %q", wrapperErr.Error(), want)
	}
}

// ---- F38d-2.6 content-aware conflict tests -------------------------

// TestInitApply_ContentAware_IdenticalRerunNoConflict locks the
// F38d-2.6 idempotent re-run contract: when every destination is
// byte-equivalent (modulo canonicalized YAML key order) to the
// would-be-written content, Apply records Unchanged per item and
// emits zero Conflicts. The pre-fix failure was treating any existing
// destination as a conflict under PolicyError, falsely flagging a
// clean re-run as needing --on-conflict=.
func TestInitApply_ContentAware_IdenticalRerunNoConflict(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	// Seed home with one schema + one agent + one docs template so the
	// re-run exercises all three Result categories that flow through
	// the content-aware branches (Schemas via applySchemas,
	// Agents/DocsTemplates via recordOutcome).
	if err := os.WriteFile(filepath.Join(homeRoot, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home schema: %v", err)
	}
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir home agent: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed home agent: %v", err)
	}
	homeDocs := filepath.Join(homeRoot, "docs-templates", "CLAUDE.md")
	if err := os.MkdirAll(filepath.Dir(homeDocs), 0o755); err != nil {
		t.Fatalf("mkdir home docs: %v", err)
	}
	if err := os.WriteFile(homeDocs, []byte("# CLAUDE\nbody\n"), 0o644); err != nil {
		t.Fatalf("seed home docs: %v", err)
	}

	target := t.TempDir()
	sel := initapply.Selections{
		Schemas:       []initapply.SchemaSelection{{Name: "plans"}},
		Agents:        []initapply.AgentSelection{{Group: "go", Name: "builder"}},
		DocsTemplates: []initapply.DocsSelection{{Name: "CLAUDE"}},
	}

	// First Apply lands every destination.
	first, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	if len(first.Schemas.Written) != 1 || len(first.Agents.Written) != 1 || len(first.DocsTemplates.Written) != 1 {
		t.Fatalf("first Apply: expected 1 written per category, got schemas=%v agents=%v docs=%v",
			first.Schemas.Written, first.Agents.Written, first.DocsTemplates.Written)
	}
	// Snapshot dest bytes so we can prove the second Apply did not
	// rewrite them.
	schemaDest := filepath.Join(target, ".ta", "schema.toml")
	agentDest := filepath.Join(target, ".claude", "agents", "go-builder.md")
	docsDest := filepath.Join(target, "CLAUDE.md")
	schemaBefore, err := os.ReadFile(schemaDest)
	if err != nil {
		t.Fatalf("read schema after first apply: %v", err)
	}
	agentBefore, err := os.ReadFile(agentDest)
	if err != nil {
		t.Fatalf("read agent after first apply: %v", err)
	}
	docsBefore, err := os.ReadFile(docsDest)
	if err != nil {
		t.Fatalf("read docs after first apply: %v", err)
	}

	// Second Apply: identical selections + PolicyError. With the
	// content-aware fix, every dest is Unchanged and zero Conflicts.
	second, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(second.Schemas.Conflicts) != 0 {
		t.Errorf("Schemas.Conflicts = %v, want empty (Unchanged)", second.Schemas.Conflicts)
	}
	if len(second.Agents.Conflicts) != 0 {
		t.Errorf("Agents.Conflicts = %v, want empty (Unchanged)", second.Agents.Conflicts)
	}
	if len(second.DocsTemplates.Conflicts) != 0 {
		t.Errorf("DocsTemplates.Conflicts = %v, want empty (Unchanged)", second.DocsTemplates.Conflicts)
	}
	if len(second.Schemas.Unchanged) != 1 || second.Schemas.Unchanged[0] != "plans" {
		t.Errorf("Schemas.Unchanged = %v, want [plans]", second.Schemas.Unchanged)
	}
	if len(second.Agents.Unchanged) != 1 || second.Agents.Unchanged[0] != "go/builder" {
		t.Errorf("Agents.Unchanged = %v, want [go/builder]", second.Agents.Unchanged)
	}
	if len(second.DocsTemplates.Unchanged) != 1 || second.DocsTemplates.Unchanged[0] != "CLAUDE" {
		t.Errorf("DocsTemplates.Unchanged = %v, want [CLAUDE]", second.DocsTemplates.Unchanged)
	}
	// No category should have Written entries on a fully-Unchanged
	// re-run.
	if len(second.Schemas.Written) != 0 || len(second.Agents.Written) != 0 || len(second.DocsTemplates.Written) != 0 {
		t.Errorf("Unchanged re-run produced Written entries: schemas=%v agents=%v docs=%v",
			second.Schemas.Written, second.Agents.Written, second.DocsTemplates.Written)
	}
	// Disk bytes must be untouched.
	schemaAfter, err := os.ReadFile(schemaDest)
	if err != nil {
		t.Fatalf("read schema after second apply: %v", err)
	}
	agentAfter, err := os.ReadFile(agentDest)
	if err != nil {
		t.Fatalf("read agent after second apply: %v", err)
	}
	docsAfter, err := os.ReadFile(docsDest)
	if err != nil {
		t.Fatalf("read docs after second apply: %v", err)
	}
	if !bytes.Equal(schemaBefore, schemaAfter) {
		t.Errorf("schema dest rewritten by Unchanged re-run\nbefore: %s\nafter:  %s", schemaBefore, schemaAfter)
	}
	if !bytes.Equal(agentBefore, agentAfter) {
		t.Errorf("agent dest rewritten by Unchanged re-run\nbefore: %s\nafter:  %s", agentBefore, agentAfter)
	}
	if !bytes.Equal(docsBefore, docsAfter) {
		t.Errorf("docs dest rewritten by Unchanged re-run\nbefore: %s\nafter:  %s", docsBefore, docsAfter)
	}
}

// TestInitApply_ContentAware_ModifiedFileStillConflicts pins the
// safety side of F38d-2.6: real drift between dest bytes and the
// would-write body MUST still surface as a conflict (not Unchanged).
// Without this anchor, a content-aware comparator that erroneously
// reported equivalence on drift would silently overwrite operator
// edits.
func TestInitApply_ContentAware_ModifiedFileStillConflicts(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir home agent: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed home agent: %v", err)
	}

	target := t.TempDir()
	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyError); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Mutate the dest agent's body — frontmatter intact but the post-
	// frontmatter body now diverges from what the install would emit.
	agentDest := filepath.Join(target, ".claude", "agents", "go-builder.md")
	modified := "---\nname: go-builder\ndescription: test agent\n---\nLOCAL EDITS — do not clobber\n"
	if err := os.WriteFile(agentDest, []byte(modified), 0o644); err != nil {
		t.Fatalf("mutate agent dest: %v", err)
	}

	// Second Apply: PolicyError must surface a real Conflict, not
	// Unchanged. The mutated body MUST survive untouched.
	second, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(second.Agents.Conflicts) != 1 || second.Agents.Conflicts[0] != "go/builder" {
		t.Errorf("Agents.Conflicts = %v, want [go/builder] (drift must register)", second.Agents.Conflicts)
	}
	if len(second.Agents.Unchanged) != 0 {
		t.Errorf("Agents.Unchanged = %v, want empty (drift must not register as Unchanged)", second.Agents.Unchanged)
	}
	if len(second.Agents.Written) != 0 {
		t.Errorf("Agents.Written = %v, want empty under PolicyError", second.Agents.Written)
	}
	got, err := os.ReadFile(agentDest)
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if string(got) != modified {
		t.Errorf("local edits clobbered\ngot:  %s\nwant: %s", got, modified)
	}
}

// TestInitApply_ContentAware_FrontmatterKeyOrderIgnored locks the
// canonicalization contract: two `.md` files differing only in the
// alphabetical-vs-authored order of YAML frontmatter keys must be
// treated as content-equivalent. yaml.v3 round-trip through
// md.DecodeFrontmatter + md.EncodeFrontmatter neutralizes key-order
// drift so re-runs that the install transform would produce in
// alphabetical order do not falsely conflict with on-disk files
// authored in a different key order.
func TestInitApply_ContentAware_FrontmatterKeyOrderIgnored(t *testing.T) {
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir home agent: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte(agentWithName("builder")), 0o644); err != nil {
		t.Fatalf("seed home agent: %v", err)
	}

	target := t.TempDir()
	sel := initapply.Selections{
		Agents: []initapply.AgentSelection{{Group: "go", Name: "builder"}},
	}
	if _, err := initapply.Apply(target, sel, initapply.PolicyError); err != nil {
		t.Fatalf("first Apply: %v", err)
	}

	// Rewrite dest with frontmatter keys in a different order
	// (description first, then name). Body identical. Content-aware
	// comparator must canonicalize both sides before sha256 and treat
	// the file as Unchanged.
	agentDest := filepath.Join(target, ".claude", "agents", "go-builder.md")
	reordered := "---\ndescription: test agent\nname: go-builder\n---\nbody\n"
	if err := os.WriteFile(agentDest, []byte(reordered), 0o644); err != nil {
		t.Fatalf("reorder frontmatter at dest: %v", err)
	}

	second, err := initapply.Apply(target, sel, initapply.PolicyError)
	if err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if len(second.Agents.Conflicts) != 0 {
		t.Errorf("Agents.Conflicts = %v, want empty (key-order drift must canonicalize)", second.Agents.Conflicts)
	}
	if len(second.Agents.Unchanged) != 1 || second.Agents.Unchanged[0] != "go/builder" {
		t.Errorf("Agents.Unchanged = %v, want [go/builder]", second.Agents.Unchanged)
	}
	// Reordered file must survive untouched — Unchanged path does no
	// rewrite.
	got, err := os.ReadFile(agentDest)
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if string(got) != reordered {
		t.Errorf("dest rewritten by Unchanged re-run\ngot:  %s\nwant: %s", got, reordered)
	}
}

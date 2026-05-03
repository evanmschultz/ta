package initapply_test

import (
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
	if err := os.WriteFile(filepath.Join(taDir, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
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
	// F32: empty-provenance + project target = home only. Pre-seed an
	// agent under home/agents/go/builder.md so the resolver finds it.
	setupBinary(t)
	homeRoot := setupHome(t)
	homeAgent := filepath.Join(homeRoot, "agents", "go", "builder.md")
	if err := os.MkdirAll(filepath.Dir(homeAgent), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(homeAgent, []byte("# go-builder\nbody\n"), 0o644); err != nil {
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
	if _, err := os.Stat(filepath.Join(target, ".claude", "agents", "go", "builder.md")); err != nil {
		t.Errorf("agent not at expected path: %v", err)
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
	report := initapply.Report{
		Schemas:       initapply.Result{Conflicts: []string{"plans"}},
		Agents:        initapply.Result{Conflicts: []string{"go/builder"}},
		Configs:       initapply.Result{Conflicts: []string{"mcp.json"}},
		DocsTemplates: initapply.Result{Conflicts: []string{"CLAUDE"}},
	}
	got := initapply.AggregateConflicts(report)
	want := []string{"agent:go/builder", "config:mcp.json", "docs:CLAUDE", "schema:plans"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d (got %v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("idx %d: got %q, want %q", i, got[i], want[i])
		}
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
// pointing at `ta init --target-system`, never silently fall back to
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
	if !strings.Contains(msg, "ta init --target-system") {
		t.Errorf("error should point at `ta init --target-system`: %v", err)
	}
}

// TestApply_EmptyHome_HomeInit_PopulatesFromBinary covers the canonical
// `ta init --target-system` flow: target IS $HOME/.ta, empty-provenance
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
// confirms `--target-system` against an already-populated home respects
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

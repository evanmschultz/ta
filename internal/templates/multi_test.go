package templates_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/evanmschultz/ta/internal/templates"
)

// fixtureFS returns a synthetic embed-style FS with two schemas, a
// couple of grouped agents, one flat agent, two configs, two docs
// templates, and the `.keep` sentinel files. Designed to exercise
// every multi-category branch.
func fixtureFS() fstest.MapFS {
	return fstest.MapFS{
		"examples/schemas/cascade.toml": &fstest.MapFile{
			Data: []byte(plansSchema),
		},
		"examples/schemas/agents.toml": &fstest.MapFile{
			Data: []byte(agentsSchema),
		},
		"examples/agents/.keep":             &fstest.MapFile{Data: []byte("# placeholder\n")},
		"examples/agents/go/builder.md":     &fstest.MapFile{Data: []byte("# go-builder\nbody\n")},
		"examples/agents/go/qa.md":          &fstest.MapFile{Data: []byte("# go-qa\nbody\n")},
		"examples/agents/fe/builder.md":     &fstest.MapFile{Data: []byte("# fe-builder\nbody\n")},
		"examples/agents/orphan.md":         &fstest.MapFile{Data: []byte("# orphan\nbody\n")},
		"examples/configs/.keep":            &fstest.MapFile{Data: []byte("")},
		"examples/configs/mcp.json":         &fstest.MapFile{Data: []byte(`{"x":1}`)},
		"examples/configs/gitignore":        &fstest.MapFile{Data: []byte("dist/\nbin/\n")},
		"examples/docs-templates/.keep":     &fstest.MapFile{Data: []byte("")},
		"examples/docs-templates/CLAUDE.md": &fstest.MapFile{Data: []byte("# CLAUDE.md\n")},
		"examples/docs-templates/README.md": &fstest.MapFile{Data: []byte("# README.md\n")},
	}
}

const agentsSchema = `
[agents]
paths = ["agents/*/*.md"]
description = "Agents library."

[agents.agent]
description = "One agent."
heading = 1

[agents.agent.fields.name]
type = "string"
required = true
description = "Agent name."

[agents.agent.fields.body]
type = "string"
format = "markdown"
description = "Agent body."
`

func TestSetBinarySource_NilResetsBinary(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	got, err := templates.ListItems(templates.KindSchema)
	if err != nil {
		t.Fatalf("ListItems with binary: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected items from binary fixture")
	}
	templates.SetBinarySource(nil)
	got, err = templates.ListItems(templates.KindSchema)
	if err != nil {
		t.Fatalf("ListItems without binary: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil binary source should yield zero items, got %v", got)
	}
}

func TestListItems_SchemaBinaryAndHome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "schema.toml"), []byte(plansSchema), 0o644); err != nil {
		t.Fatalf("seed home: %v", err)
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	got, err := templates.ListItems(templates.KindSchema)
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	// Expect: binary-plans, binary-agents, home-plans
	names := map[string]map[string]bool{} // name → provenance set
	for _, it := range got {
		if names[it.Name] == nil {
			names[it.Name] = map[string]bool{}
		}
		names[it.Name][string(it.Provenance)] = true
	}
	if !names["plans"]["ta"] {
		t.Errorf("missing plans@ta in %v", got)
	}
	if !names["plans"]["home"] {
		t.Errorf("missing plans@home in %v", got)
	}
	if !names["agents"]["ta"] {
		t.Errorf("missing agents@ta in %v", got)
	}
}

func TestListItems_AgentsGroupBucket(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	got, err := templates.ListItems(templates.KindAgent)
	if err != nil {
		t.Fatalf("ListItems agents: %v", err)
	}
	groups := map[string]int{}
	for _, it := range got {
		groups[it.Group]++
	}
	if groups["go"] != 2 {
		t.Errorf("want 2 in 'go', got %d in %v", groups["go"], got)
	}
	if groups["fe"] != 1 {
		t.Errorf("want 1 in 'fe', got %d", groups["fe"])
	}
	if groups[""] != 1 {
		t.Errorf("want 1 ungrouped (orphan.md), got %d", groups[""])
	}
}

func TestListItems_ConfigsKeepFiltered(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	got, err := templates.ListItems(templates.KindConfig)
	if err != nil {
		t.Fatalf("ListItems configs: %v", err)
	}
	for _, it := range got {
		if it.Name == ".keep" {
			t.Errorf(".keep sentinel leaked into config items")
		}
	}
	names := []string{}
	for _, it := range got {
		names = append(names, it.Name)
	}
	wantNames := map[string]bool{"mcp.json": true, "gitignore": true}
	for n := range wantNames {
		if !slices.Contains(names, n) {
			t.Errorf("missing config %q in %v", n, names)
		}
	}
}

func TestListItems_DocsTemplatesTrimsExtension(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	got, err := templates.ListItems(templates.KindDocsTemplate)
	if err != nil {
		t.Fatalf("ListItems docs: %v", err)
	}
	for _, it := range got {
		if strings.HasSuffix(it.Name, ".md") {
			t.Errorf("docs template name should be trimmed of .md: %q", it.Name)
		}
	}
}

func TestListAll_SortedDeterministic(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	first, err := templates.ListAll()
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	second, err := templates.ListAll()
	if err != nil {
		t.Fatalf("ListAll repeat: %v", err)
	}
	if len(first) != len(second) {
		t.Fatalf("len drift: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("idx %d drift: %+v vs %+v", i, first[i], second[i])
		}
	}
}

func TestShowItem_BinarySchema(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	data, err := templates.ShowItem(templates.Item{
		Kind: templates.KindSchema, Name: "plans", Provenance: templates.ProvenanceBinary,
	})
	if err != nil {
		t.Fatalf("ShowItem binary plans: %v", err)
	}
	if !strings.Contains(string(data), "[plans]") {
		t.Errorf("missing [plans]: %s", data)
	}
}

func TestShowItem_BinaryAgent(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	data, err := templates.ShowItem(templates.Item{
		Kind: templates.KindAgent, Name: "builder", Group: "go",
		Provenance: templates.ProvenanceBinary,
	})
	if err != nil {
		t.Fatalf("ShowItem binary builder: %v", err)
	}
	if !strings.Contains(string(data), "go-builder") {
		t.Errorf("missing body: %s", data)
	}
}

func TestShowItem_NotFound(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	templates.SetBinarySource(fixtureFS())
	t.Cleanup(func() { templates.SetBinarySource(nil) })

	_, err := templates.ShowItem(templates.Item{
		Kind: templates.KindAgent, Name: "ghost", Group: "go",
		Provenance: templates.ProvenanceBinary,
	})
	if !errors.Is(err, templates.ErrItemNotFound) {
		t.Errorf("err = %v, want ErrItemNotFound", err)
	}
}

func TestSaveAgent_HomePersists(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	body := []byte("# my-agent\nbody\n")
	if err := templates.SaveAgent("my-agent", "go", body, false); err != nil {
		t.Fatalf("SaveAgent: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "agents", "go", "my-agent.md"))
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if string(got) != string(body) {
		t.Errorf("body mismatch")
	}

	// Conflict without force errors loudly.
	if err := templates.SaveAgent("my-agent", "go", body, false); err == nil {
		t.Errorf("expected conflict error without force")
	}
	// Force overwrites.
	if err := templates.SaveAgent("my-agent", "go", []byte("# v2\n"), true); err != nil {
		t.Errorf("force save: %v", err)
	}
	got, _ = os.ReadFile(filepath.Join(root, "agents", "go", "my-agent.md"))
	if string(got) != "# v2\n" {
		t.Errorf("force did not overwrite: %s", got)
	}
}

func TestSaveAgent_FlatGroup(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	if err := templates.SaveAgent("orphan", "", []byte("# x\n"), false); err != nil {
		t.Fatalf("SaveAgent flat: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "agents", "orphan.md")); err != nil {
		t.Errorf("flat agent not at expected path: %v", err)
	}
}

func TestSaveConfig_DefaultsAndConflict(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	if err := templates.SaveConfig("mcp.json", []byte(`{}`), false); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "configs", "mcp.json")); err != nil {
		t.Errorf("mcp.json missing: %v", err)
	}
	if err := templates.SaveConfig("mcp.json", []byte(`{}`), false); err == nil {
		t.Errorf("expected conflict")
	}
	if err := templates.SaveConfig("mcp.json", []byte(`{"x":2}`), true); err != nil {
		t.Errorf("force save: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "configs", "mcp.json"))
	if string(got) != `{"x":2}` {
		t.Errorf("force did not overwrite: %s", got)
	}
}

func TestSaveDocsTemplate_AppendsExtension(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	if err := templates.SaveDocsTemplate("CLAUDE", []byte("# CLAUDE\n"), false); err != nil {
		t.Fatalf("SaveDocsTemplate: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "docs-templates", "CLAUDE.md")); err != nil {
		t.Errorf("CLAUDE.md missing: %v", err)
	}
}

func TestDeleteAgent_HappyAndMissing(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	body := []byte("# x\n")
	if err := templates.SaveAgent("a", "g", body, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := templates.DeleteAgent("a", "g"); err != nil {
		t.Errorf("DeleteAgent: %v", err)
	}
	// Idempotent miss is an explicit ErrItemNotFound.
	if err := templates.DeleteAgent("a", "g"); !errors.Is(err, templates.ErrItemNotFound) {
		t.Errorf("DeleteAgent missing err = %v, want ErrItemNotFound", err)
	}
}

func TestDeleteConfig_NotFound(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	err := templates.DeleteConfig("missing.json")
	if !errors.Is(err, templates.ErrItemNotFound) {
		t.Errorf("err = %v, want ErrItemNotFound", err)
	}
}

func TestSaveAgent_RejectsBadName(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	for _, bad := range []string{"", "..", "a/b", ".hidden"} {
		err := templates.SaveAgent(bad, "g", []byte("x"), false)
		if !errors.Is(err, templates.ErrInvalidName) {
			t.Errorf("SaveAgent(%q) err = %v, want ErrInvalidName", bad, err)
		}
	}
}

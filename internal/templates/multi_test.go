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

// TestSaveSubstrateFile_FileShapedSuccess verifies that SaveSubstrateFile
// successfully saves a file for a supported file-shaped substrate (e.g.,
// claude_agents with group, or claude_md_fragments without group).
func TestSaveSubstrateFile_FileShapedSuccess(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	// Create a temporary source file to copy.
	srcFile := filepath.Join(t.TempDir(), "test.md")
	srcData := []byte("# Test Agent\nbody\n")
	if err := os.WriteFile(srcFile, srcData, 0o644); err != nil {
		t.Fatalf("seed source file: %v", err)
	}

	// Test 1: grouped substrate (claude_agents).
	if err := templates.SaveSubstrateFile("claude_agents", srcFile, "go", "test-agent.md", false); err != nil {
		t.Fatalf("SaveSubstrateFile claude_agents: %v", err)
	}
	// Expect: ~/.ta/agents/go/test-agent.md
	dstPath := filepath.Join(root, "agents", "go", "test-agent.md")
	got, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("read destination: %v", err)
	}
	if string(got) != string(srcData) {
		t.Errorf("file content mismatch: got %q, want %q", string(got), string(srcData))
	}

	// Test 2: non-grouped substrate (claude_md_fragments).
	srcFile2 := filepath.Join(t.TempDir(), "fragment.md")
	fragData := []byte("## Fragment\nmore text\n")
	if err := os.WriteFile(srcFile2, fragData, 0o644); err != nil {
		t.Fatalf("seed fragment file: %v", err)
	}
	if err := templates.SaveSubstrateFile("claude_md_fragments", srcFile2, "", "fragment.md", false); err != nil {
		t.Fatalf("SaveSubstrateFile claude_md_fragments: %v", err)
	}
	// Expect: ~/.ta/claude-md/fragment.md
	fragPath := filepath.Join(root, "claude-md", "fragment.md")
	gotFrag, err := os.ReadFile(fragPath)
	if err != nil {
		t.Fatalf("read fragment: %v", err)
	}
	if string(gotFrag) != string(fragData) {
		t.Errorf("fragment content mismatch: got %q, want %q", string(gotFrag), string(fragData))
	}
}

// TestSaveSubstrateFile_RejectsBundleSubstrates verifies that the 4 bundle
// substrates (claude_skills, claude_plugins, example_thariq, example_stil)
// are explicitly rejected as unsupported in this drop.
func TestSaveSubstrateFile_RejectsBundleSubstrates(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	srcFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bundleNames := []string{"claude_skills", "claude_plugins", "example_thariq", "example_stil"}
	for _, name := range bundleNames {
		err := templates.SaveSubstrateFile(name, srcFile, "", "", false)
		if err == nil {
			t.Errorf("SaveSubstrateFile(%q) expected error but got nil", name)
		}
		if !strings.Contains(err.Error(), "directory bundle") || !strings.Contains(err.Error(), "unsupported") {
			t.Errorf("SaveSubstrateFile(%q) error message not helpful: %v", name, err)
		}
	}
}

// TestSaveSubstrateFile_RejectsUnknownSubstrate verifies that an unknown
// substrate name is rejected with an error naming the 10 supported defaults.
func TestSaveSubstrateFile_RejectsUnknownSubstrate(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	srcFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(srcFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	err := templates.SaveSubstrateFile("not_a_substrate", srcFile, "", "", false)
	if err == nil {
		t.Errorf("SaveSubstrateFile unknown substrate expected error but got nil")
	}
	if !strings.Contains(err.Error(), "unknown substrate") {
		t.Errorf("error message should mention unknown substrate: %v", err)
	}
	// Error should list the 10 supported defaults.
	supportedSubstrates := []string{
		"claude_agents", "claude_hooks", "claude_output_styles",
		"claude_md_fragments", "claude_settings_fragments", "claude_mcp_servers",
		"codex_agents", "codex_config_fragments", "codex_mcp_servers", "agents_md",
	}
	for _, sub := range supportedSubstrates {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error message should list supported substrate %q: %v", sub, err)
		}
	}
}

// TestSaveSubstrateFile_RejectsMissingSourceFile verifies that a non-existent
// source file is rejected with a helpful error.
func TestSaveSubstrateFile_RejectsMissingSourceFile(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	err := templates.SaveSubstrateFile("claude_agents", "/nonexistent/file.txt", "go", "file.md", false)
	if err == nil {
		t.Errorf("SaveSubstrateFile missing source expected error but got nil")
	}
	if !strings.Contains(err.Error(), "read source") {
		t.Errorf("error message should mention read source: %v", err)
	}
}

// TestSaveSubstrateFile_ConflictWithoutOverwrite verifies that saving to an
// existing destination without overwrite=true errors.
func TestSaveSubstrateFile_ConflictWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	srcFile := filepath.Join(t.TempDir(), "test.md")
	if err := os.WriteFile(srcFile, []byte("body"), 0o644); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	// Save successfully the first time.
	if err := templates.SaveSubstrateFile("claude_agents", srcFile, "go", "test.md", false); err != nil {
		t.Fatalf("first save: %v", err)
	}

	// Try to save again without overwrite — should error.
	err := templates.SaveSubstrateFile("claude_agents", srcFile, "go", "test.md", false)
	if err == nil {
		t.Errorf("expected conflict error on second save without overwrite")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error message should mention conflict: %v", err)
	}

	// With overwrite=true, should succeed.
	srcFile2 := filepath.Join(t.TempDir(), "test2.md")
	if err := os.WriteFile(srcFile2, []byte("new body"), 0o644); err != nil {
		t.Fatalf("seed source 2: %v", err)
	}
	if err := templates.SaveSubstrateFile("claude_agents", srcFile2, "go", "test.md", true); err != nil {
		t.Errorf("overwrite save: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(root, "agents", "go", "test.md"))
	if string(got) != "new body" {
		t.Errorf("overwrite did not work: %s", got)
	}
}

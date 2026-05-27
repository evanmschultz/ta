package install_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/dotta"
	"github.com/evanmschultz/ta/internal/install"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// writeSourceFile materializes a fake substrate-source file at
// filepath.Join(root, relPath) and returns its absolute path. Test
// fixtures build a dotta.Tree manually (no need to invoke dotta.Walk)
// pointing at these on-disk files.
func writeSourceFile(t *testing.T, root, relPath, data string) string {
	t.Helper()
	abs := filepath.Join(root, relPath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir parent of %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
	return abs
}

// makeSubtree builds a dotta.Subtree whose AbsPath lives at
// filepath.Join(dottaRoot, name) and whose Files list pairs RelPath
// (subtree-relative) with the source-file abspaths previously
// materialized by writeSourceFile.
func makeSubtree(name, dottaRoot, onConflict string, files []dotta.FileMeta) dotta.Subtree {
	return dotta.Subtree{
		Name:    name,
		AbsPath: filepath.Join(dottaRoot, name),
		RelPath: name,
		Mapping: dotta.Mapping{OnConflict: onConflict},
		Files:   files,
	}
}

// TestApply_PlainCopySubstrateLandsFilesUnderDestination pins the
// canonical L3-I3 happy path: a substrate whose source basename matches
// a dotta subtree.Name installs every enumerated file under
// projectRoot/Destination via the CopyFile primitive.
func TestApply_PlainCopySubstrateLandsFilesUnderDestination(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	// Source file at <dottaRoot>/agents/builder.md
	srcAbs := writeSourceFile(t, dottaRoot, "agents/builder.md", "agent body\n")

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_agents": {
				Source:      "~/.ta/agents",
				Destination: ".claude/agents",
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("agents", dottaRoot, "", []dotta.FileMeta{{
				Name:    "builder.md",
				AbsPath: srcAbs,
				RelPath: "builder.md",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wantDst := filepath.Join(projectRoot, ".claude", "agents", "builder.md")
	got, readErr := os.ReadFile(wantDst)
	if readErr != nil {
		t.Fatalf("dst not written at %s: %v", wantDst, readErr)
	}
	if string(got) != "agent body\n" {
		t.Errorf("dst content mismatch: got %q want %q", got, "agent body\n")
	}

	if len(rep.Written) != 1 || !strings.HasPrefix(rep.Written[0], "claude_agents:") {
		t.Errorf("Report.Written = %v, want one claude_agents: entry", rep.Written)
	}
	if len(rep.Errors) != 0 {
		t.Errorf("Report.Errors = %v, want none", rep.Errors)
	}
}

// TestApply_FlattenByBasenameDropsGroupDir verifies the flatten_strategy
// pass-through: a substrate-source layout like agents/go/builder.md
// lands at .claude/agents/builder.md (no go/ middle segment) when the
// substrate declares flatten_strategy=by_basename.
func TestApply_FlattenByBasenameDropsGroupDir(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	srcAbs := writeSourceFile(t, dottaRoot, "agents/go/builder.md", "flattened agent\n")

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_agents": {
				Source:          "~/.ta/agents",
				Destination:     ".claude/agents",
				FlattenStrategy: "by_basename",
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("agents", dottaRoot, "", []dotta.FileMeta{{
				Name:    "builder.md",
				AbsPath: srcAbs,
				RelPath: "go/builder.md",
			}}),
		},
	}

	if _, err := install.Apply(cfg, tree, projectRoot); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	wantFlat := filepath.Join(projectRoot, ".claude", "agents", "builder.md")
	if _, err := os.Stat(wantFlat); err != nil {
		t.Fatalf("expected flat dst at %s: %v", wantFlat, err)
	}

	notWantNested := filepath.Join(projectRoot, ".claude", "agents", "go", "builder.md")
	if _, err := os.Stat(notWantNested); err == nil {
		t.Errorf("nested path %s should not exist after flatten", notWantNested)
	}
}

// TestApply_MissingSubtreeSilentlySkips verifies that a substrate whose
// source has no matching subtree under dottaTree is skipped without
// error and without contributing to Report.Written or Report.Errors.
func TestApply_MissingSubtreeSilentlySkips(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_agents": {
				Source:      "~/.ta/agents",
				Destination: ".claude/agents",
			},
		},
	}

	// Empty dotta tree — no subtrees at all.
	tree := dotta.Tree{Root: dottaRoot}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: unexpected error: %v", err)
	}
	if len(rep.Written) != 0 || len(rep.Skipped) != 0 || len(rep.Errors) != 0 {
		t.Errorf("expected empty report, got %+v", rep)
	}
}

// TestApply_OnConflictSkipPreservesExisting pins Fold B: when
// subtree.Mapping.OnConflict=skip and the destination already exists,
// Apply records the file in Report.Skipped and leaves the destination
// untouched.
func TestApply_OnConflictSkipPreservesExisting(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	srcAbs := writeSourceFile(t, dottaRoot, "agents/builder.md", "new body\n")

	// Pre-seed destination with sentinel content.
	dstPath := filepath.Join(projectRoot, ".claude", "agents", "builder.md")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		t.Fatalf("mkdir dst parent: %v", err)
	}
	if err := os.WriteFile(dstPath, []byte("EXISTING"), 0o644); err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_agents": {
				Source:      "~/.ta/agents",
				Destination: ".claude/agents",
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("agents", dottaRoot, dotta.OnConflictSkip, []dotta.FileMeta{{
				Name:    "builder.md",
				AbsPath: srcAbs,
				RelPath: "builder.md",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, readErr := os.ReadFile(dstPath)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	if string(got) != "EXISTING" {
		t.Errorf("skip-policy must not overwrite: got %q want %q", got, "EXISTING")
	}

	if len(rep.Skipped) != 1 || !strings.HasPrefix(rep.Skipped[0], "claude_agents:") {
		t.Errorf("Report.Skipped = %v, want one claude_agents: entry", rep.Skipped)
	}
	if len(rep.Written) != 0 {
		t.Errorf("Report.Written should be empty when policy=skip: got %v", rep.Written)
	}
}

// TestApply_ReplaceStrategyRoutesToCopyFile verifies Fold C: when
// merge_strategy=replace and the destination already exists, Apply
// routes through CopyFile (overwrite) — either directly via the
// shouldMerge=false fast path or via the MergeFile sentinel fallback.
// Either way the destination ends up bearing the source bytes.
func TestApply_ReplaceStrategyRoutesToCopyFile(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	srcAbs := writeSourceFile(t, dottaRoot, "agents/builder.md", "REPLACED\n")

	dstPath := filepath.Join(projectRoot, ".claude", "agents", "builder.md")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		t.Fatalf("mkdir dst parent: %v", err)
	}
	if err := os.WriteFile(dstPath, []byte("OLD"), 0o644); err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_agents": {
				Source:        "~/.ta/agents",
				Destination:   ".claude/agents",
				MergeStrategy: "replace",
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("agents", dottaRoot, "", []dotta.FileMeta{{
				Name:    "builder.md",
				AbsPath: srcAbs,
				RelPath: "builder.md",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, readErr := os.ReadFile(dstPath)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	if string(got) != "REPLACED\n" {
		t.Errorf("replace strategy must overwrite via CopyFile: got %q", got)
	}

	if errors.Is(asErrors(rep.Errors), install.ErrReplaceStrategyDelegate) {
		t.Errorf("replace sentinel must not leak into Report.Errors: %v", rep.Errors)
	}
	if len(rep.Written) != 1 {
		t.Errorf("Report.Written = %v, want one entry", rep.Written)
	}
}

// TestApply_MergePathDeepDedupesHooks pins the
// claude_settings_fragments registry path: when the substrate matches
// the registry entry and the destination is pre-seeded with a hooks
// payload, Apply routes through MergeFile with the registry's
// arrayDedupeKeys ({"PreToolUse": "matcher,command"}) and the destination
// ends up with deduped (matcher, command) entries plus the appended
// SessionStart entry.
func TestApply_MergePathDeepDedupesHooks(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	// Source: incoming settings fragment with Agent + SessionStart entries.
	// Note: top-level event arrays (not wrapped under "hooks").
	incoming := `{
  "PreToolUse": [
    {"matcher": "Agent", "command": "new-cmd"},
    {"matcher": "SessionStart", "command": "fresh"}
  ]
}`
	srcAbs := writeSourceFile(t, dottaRoot, "claude-settings/settings.json", incoming)

	// Pre-seed destination with an existing Agent matcher + command entry.
	existing := `{
  "PreToolUse": [
    {"matcher": "Agent", "command": "old-cmd"}
  ]
}`
	dstPath := filepath.Join(projectRoot, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		t.Fatalf("mkdir dst parent: %v", err)
	}
	if err := os.WriteFile(dstPath, []byte(existing), 0o644); err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_settings_fragments": {
				Source:        "~/.ta/claude-settings",
				Destination:   ".claude/settings.json",
				MergeStrategy: "merge",
			},
		},
	}

	// The substrate's destination is the literal settings.json file (not
	// a directory). ResolveDestination treats Destination as relative to
	// projectRoot and joins file.RelPath onto it; to make the final path
	// equal projectRoot/.claude/settings.json (file path), file.RelPath
	// is empty? No — ResolveDestination errors on empty RelPath. The
	// claude_settings_fragments substrate's source dir holds exactly one
	// file ("settings.json") and Destination is ".claude/settings.json";
	// the resolved path becomes projectRoot/.claude/settings.json/settings.json
	// under the default resolver. To match the canonical install layout,
	// the substrate uses flatten_strategy=by_basename so RelPath collapses
	// to file basename. But Destination still has /settings.json suffix.
	//
	// For this test we set FlattenStrategy="" and a Destination that is
	// the parent directory of the canonical .claude/settings.json file —
	// the file basename then lands at .claude/settings.json. This mirrors
	// what L3-I5 will refine; for L3-I3 we pin the merger dispatch
	// happening at all.
	cfg.Substrates["claude_settings_fragments"] = installconfig.Substrate{
		Source:        "~/.ta/claude-settings",
		Destination:   ".claude",
		MergeStrategy: "merge",
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("claude-settings", dottaRoot, "", []dotta.FileMeta{{
				Name:    "settings.json",
				AbsPath: srcAbs,
				RelPath: "settings.json",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(rep.Errors) != 0 {
		t.Errorf("Report.Errors = %v, want none", rep.Errors)
	}

	out, readErr := os.ReadFile(dstPath)
	if readErr != nil {
		t.Fatalf("read merged dst: %v", readErr)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse merged dst: %v\n%s", err, out)
	}
	pretool, ok := got["PreToolUse"].([]any)
	if !ok {
		t.Fatalf("PreToolUse missing or not an array at top level: %+v", got)
	}
	if len(pretool) != 2 {
		t.Errorf("expected 2 entries (Agent + SessionStart) via registry; got %d: %+v", len(pretool), pretool)
	}
	// Build a map of (matcher, command) -> true to verify dedupe.
	entries := map[string]string{} // "matcher:command" -> command
	for _, e := range pretool {
		ent, _ := e.(map[string]any)
		m, _ := ent["matcher"].(string)
		c, _ := ent["command"].(string)
		entries[m] = c
	}
	// Existing (Agent, old-cmd) wins on dedupe; incoming SessionStart appended.
	if entries["Agent"] != "old-cmd" {
		t.Errorf("existing (Agent, old-cmd) should win on dedupe, got %q (full=%+v)", entries["Agent"], entries)
	}
	if entries["SessionStart"] != "fresh" {
		t.Errorf("incoming SessionStart not appended: %+v", entries)
	}
}

// asErrors collapses a Report.Errors string slice into a single error
// for errors.Is assertions in the test helpers. The Report.Errors API
// surfaces error strings, not typed errors; this helper exists only so
// the replace-strategy test can sanity-check that ErrReplaceStrategyDelegate
// never leaks into the report payload.
func asErrors(errs []string) error {
	if len(errs) == 0 {
		return nil
	}
	return errors.New(strings.Join(errs, "; "))
}

// TestApply_RegistrationDirectivesWriteSettingsFileAndKeepDirectiveReport
// verifies that Apply invokes the real D3 writer (ApplyRegistrations), which
// mutates the declared settings files, while preserving Report.Registrations
// as the directive echo (required by existing callers).
func TestApply_RegistrationDirectivesWriteSettingsFileAndKeepDirectiveReport(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	srcAbs := writeSourceFile(t, dottaRoot, "hooks/pre.sh", "#!/usr/bin/env bash\n")

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_hooks": {
				Source:      "~/.ta/hooks",
				Destination: ".claude/hooks",
				Chmod:       "0755",
				Register: []installconfig.Registration{
					{
						Event:        "PreToolUse",
						Matcher:      "Bash",
						SettingsFile: ".claude/settings.local.json",
						SourceFile:   "pre.sh",
					},
				},
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("hooks", dottaRoot, "", []dotta.FileMeta{{
				Name:    "pre.sh",
				AbsPath: srcAbs,
				RelPath: "pre.sh",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify Report.Registrations is preserved (directive echo).
	if len(rep.Registrations) != 1 {
		t.Fatalf("Report.Registrations len = %d, want 1: %+v", len(rep.Registrations), rep.Registrations)
	}
	if rep.Registrations[0].Event != "PreToolUse" || rep.Registrations[0].Matcher != "Bash" {
		t.Errorf("registration directive = %+v, want PreToolUse/Bash", rep.Registrations[0])
	}

	// Verify settings file was actually written (D3 writer invoked).
	settingsPath := filepath.Join(projectRoot, ".claude", "settings.local.json")
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings file not written: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	pretool, ok := got["PreToolUse"].([]any)
	if !ok || len(pretool) == 0 {
		t.Fatalf("PreToolUse array missing or empty: %+v", got)
	}

	entry := pretool[0].(map[string]any)
	if matcher, ok := entry["matcher"].(string); !ok || matcher != "Bash" {
		t.Errorf("PreToolUse entry matcher = %q, want Bash", matcher)
	}
	if command, ok := entry["command"].(string); !ok || !strings.Contains(command, "pre.sh") {
		t.Errorf("PreToolUse entry command = %q, want .../pre.sh", command)
	}

	// Verify RegistrationOutcomes is populated.
	if len(rep.RegistrationOutcomes) != 1 {
		t.Fatalf("Report.RegistrationOutcomes len = %d, want 1: %+v", len(rep.RegistrationOutcomes), rep.RegistrationOutcomes)
	}
	outcome := rep.RegistrationOutcomes[0]
	if outcome.Substrate != "claude_hooks" {
		t.Errorf("outcome.Substrate = %q, want claude_hooks", outcome.Substrate)
	}
	if outcome.Status != "added" {
		t.Errorf("outcome.Status = %q, want added", outcome.Status)
	}
}

// TestApply_RegistrationOutcomeContract_AddedAndDeduped verifies that
// RegistrationOutcomes correctly tracks added and deduped statuses.
func TestApply_RegistrationOutcomeContract_AddedAndDeduped(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	srcAbs := writeSourceFile(t, dottaRoot, "hooks/pre.sh", "#!/usr/bin/env bash\n")

	settingsPath := filepath.Join(projectRoot, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir settings parent: %v", err)
	}

	// Pre-seed settings file with one entry.
	existingSettings := `{
  "PreToolUse": [
    {"matcher": "Bash", "command": ".claude/hooks/pre.sh"}
  ]
}
`
	if err := os.WriteFile(settingsPath, []byte(existingSettings), 0o644); err != nil {
		t.Fatalf("seed settings: %v", err)
	}

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_hooks": {
				Source:      "~/.ta/hooks",
				Destination: ".claude/hooks",
				Chmod:       "0755",
				Register: []installconfig.Registration{
					{
						Event:        "PreToolUse",
						Matcher:      "Bash",
						SettingsFile: ".claude/settings.local.json",
						SourceFile:   "pre.sh",
					},
					{
						Event:        "PreToolUse",
						Matcher:      "Agent",
						SettingsFile: ".claude/settings.local.json",
						SourceFile:   "pre.sh",
					},
				},
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("hooks", dottaRoot, "", []dotta.FileMeta{{
				Name:    "pre.sh",
				AbsPath: srcAbs,
				RelPath: "pre.sh",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify outcomes: first is deduped (already exists), second is added.
	if len(rep.RegistrationOutcomes) != 2 {
		t.Fatalf("Report.RegistrationOutcomes len = %d, want 2: %+v", len(rep.RegistrationOutcomes), rep.RegistrationOutcomes)
	}

	// First outcome: Bash matcher — should be deduped (already in settings).
	if rep.RegistrationOutcomes[0].Matcher != "Bash" {
		t.Errorf("outcome[0].Matcher = %q, want Bash", rep.RegistrationOutcomes[0].Matcher)
	}
	if rep.RegistrationOutcomes[0].Status != "deduped" {
		t.Errorf("outcome[0].Status = %q, want deduped", rep.RegistrationOutcomes[0].Status)
	}

	// Second outcome: Agent matcher — should be added (new entry).
	if rep.RegistrationOutcomes[1].Matcher != "Agent" {
		t.Errorf("outcome[1].Matcher = %q, want Agent", rep.RegistrationOutcomes[1].Matcher)
	}
	if rep.RegistrationOutcomes[1].Status != "added" {
		t.Errorf("outcome[1].Status = %q, want added", rep.RegistrationOutcomes[1].Status)
	}
}

// TestApply_RegistrationOutcomeContract_Error verifies that when
// ApplyRegistrations encounters an error, RegistrationOutcome.Status is
// set to "error" and RegistrationOutcome.Error is populated.
func TestApply_RegistrationOutcomeContract_Error(t *testing.T) {
	dottaRoot := t.TempDir()
	projectRoot := t.TempDir()

	srcAbs := writeSourceFile(t, dottaRoot, "hooks/pre.sh", "#!/usr/bin/env bash\n")

	// Create a settings path in a directory that does not exist and cannot be created.
	badSettingsPath := "/root/nonexistent/settings.json"
	if os.Geteuid() == 0 {
		t.Skip("skipping error test when running as root")
	}

	cfg := installconfig.Config{
		Substrates: map[string]installconfig.Substrate{
			"claude_hooks": {
				Source:      "~/.ta/hooks",
				Destination: ".claude/hooks",
				Chmod:       "0755",
				Register: []installconfig.Registration{
					{
						Event:        "PreToolUse",
						Matcher:      "Bash",
						SettingsFile: badSettingsPath,
						SourceFile:   "pre.sh",
					},
				},
			},
		},
	}

	tree := dotta.Tree{
		Root: dottaRoot,
		Subtrees: []dotta.Subtree{
			makeSubtree("hooks", dottaRoot, "", []dotta.FileMeta{{
				Name:    "pre.sh",
				AbsPath: srcAbs,
				RelPath: "pre.sh",
			}}),
		},
	}

	rep, err := install.Apply(cfg, tree, projectRoot)
	// Apply may or may not return an error (depends on whether directory creation fails).
	// The important thing is that RegistrationOutcomes captures the error.

	// Verify at least one outcome has error status.
	if len(rep.RegistrationOutcomes) == 0 {
		t.Fatalf("Report.RegistrationOutcomes should have error entry, got empty")
	}

	foundError := false
	for _, outcome := range rep.RegistrationOutcomes {
		if outcome.Status == "error" && outcome.Error != "" {
			foundError = true
			break
		}
	}

	if !foundError {
		t.Errorf("expected at least one error outcome, got %+v", rep.RegistrationOutcomes)
	}

	_ = err // Suppress unused error variable warning.
}

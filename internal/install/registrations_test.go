package install_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/install"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// TestApplyRegistrations_SourceFileResolvesToDestinationCommand verifies that
// the command field is resolved from the join of sub.Destination and
// reg.SourceFile, with slash normalization.
func TestApplyRegistrations_SourceFileResolvesToDestinationCommand(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	sub := installconfig.Substrate{
		Destination: ".claude/hooks",
	}
	regs := []installconfig.Registration{
		{
			Event:        "PreToolUse",
			Matcher:      "Bash",
			SettingsFile: settingsPath,
			SourceFile:   "git_commit_guard.sh",
		},
	}

	if err := install.ApplyRegistrations(settingsPath, sub, regs); err != nil {
		t.Fatalf("ApplyRegistrations: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse settings: %v\n%s", err, data)
	}

	pretool, ok := got["PreToolUse"].([]any)
	if !ok || len(pretool) == 0 {
		t.Fatalf("PreToolUse array missing or empty: %+v", got)
	}

	entry := pretool[0].(map[string]any)
	command := entry["command"].(string)

	// Expect command to be ".claude/hooks/git_commit_guard.sh" (forward slashes).
	expected := ".claude/hooks/git_commit_guard.sh"
	if command != expected {
		t.Errorf("command mismatch: got %q, want %q", command, expected)
	}
}

// TestApplyRegistrations_WritesTopLevelEventHooks verifies that hook entries
// are written under top-level event-key arrays (e.g., "PreToolUse" at the root
// level), not nested under a "hooks" wrapper.
func TestApplyRegistrations_WritesTopLevelEventHooks(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	sub := installconfig.Substrate{
		Destination: ".claude/hooks",
	}
	regs := []installconfig.Registration{
		{
			Event:        "PreToolUse",
			Matcher:      "Bash",
			SettingsFile: settingsPath,
			SourceFile:   "hook1.sh",
		},
		{
			Event:        "SessionStart",
			Matcher:      "",
			SettingsFile: settingsPath,
			SourceFile:   "hook2.sh",
		},
	}

	if err := install.ApplyRegistrations(settingsPath, sub, regs); err != nil {
		t.Fatalf("ApplyRegistrations: %v", err)
	}

	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	// Verify PreToolUse and SessionStart are at top level, not nested.
	if _, ok := got["PreToolUse"]; !ok {
		t.Errorf("PreToolUse missing at top level: %+v", got)
	}
	if _, ok := got["SessionStart"]; !ok {
		t.Errorf("SessionStart missing at top level: %+v", got)
	}

	// Verify there is no "hooks" wrapper.
	if _, ok := got["hooks"]; ok {
		t.Errorf("hooks wrapper should not exist; got structure: %+v", got)
	}
}

// TestApplyRegistrations_ReapplyDedupesSameMatcherAndCommand verifies that
// re-running the same registration (same matcher and command) does not duplicate
// the entry.
func TestApplyRegistrations_ReapplyDedupesSameMatcherAndCommand(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "settings.json")

	sub := installconfig.Substrate{
		Destination: ".claude/hooks",
	}
	regs := []installconfig.Registration{
		{
			Event:        "PreToolUse",
			Matcher:      "Bash",
			SettingsFile: settingsPath,
			SourceFile:   "hook1.sh",
		},
	}

	// First apply.
	if err := install.ApplyRegistrations(settingsPath, sub, regs); err != nil {
		t.Fatalf("ApplyRegistrations (first): %v", err)
	}

	// Verify initial state: one entry.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after first apply: %v", err)
	}
	var after1 map[string]any
	if err := json.Unmarshal(data, &after1); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	pretool1, ok := after1["PreToolUse"].([]any)
	if !ok || len(pretool1) != 1 {
		t.Errorf("expected 1 entry after first apply, got %d", len(pretool1))
	}

	// Second apply with same registration.
	if err := install.ApplyRegistrations(settingsPath, sub, regs); err != nil {
		t.Fatalf("ApplyRegistrations (second): %v", err)
	}

	// Verify final state: still one entry (deduped).
	data, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings after second apply: %v", err)
	}
	var after2 map[string]any
	if err := json.Unmarshal(data, &after2); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	pretool2, ok := after2["PreToolUse"].([]any)
	if !ok || len(pretool2) != 1 {
		t.Errorf("expected 1 entry after re-apply (deduped), got %d: %+v", len(pretool2), pretool2)
	}
}

// TestApplyRegistrations_CreatesMissingSettingsFileRootObject verifies that
// a missing settings file is created as a minimal root object when ApplyRegistrations
// is called.
func TestApplyRegistrations_CreatesMissingSettingsFileRootObject(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "subdir", "settings.json")

	// Ensure parent directory exists (registrations writer does not create it).
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatalf("mkdir parent: %v", err)
	}

	sub := installconfig.Substrate{
		Destination: ".claude/hooks",
	}
	regs := []installconfig.Registration{
		{
			Event:        "PreToolUse",
			Matcher:      "Bash",
			SettingsFile: settingsPath,
			SourceFile:   "hook1.sh",
		},
	}

	// Apply to non-existent settings file.
	if err := install.ApplyRegistrations(settingsPath, sub, regs); err != nil {
		t.Fatalf("ApplyRegistrations: %v", err)
	}

	// Verify file was created.
	if _, err := os.Stat(settingsPath); err != nil {
		t.Fatalf("settings file not created: %v", err)
	}

	// Verify content is valid JSON with minimal root object.
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse settings: %v", err)
	}

	// Verify it contains the PreToolUse entry.
	if _, ok := got["PreToolUse"]; !ok {
		t.Errorf("PreToolUse missing in created settings: %+v", got)
	}
}

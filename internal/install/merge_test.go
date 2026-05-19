package install_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/install"
	"github.com/evanmschultz/ta/internal/installconfig"
)

// writeFile is a tiny test helper that writes data to a temp file and
// fails the test on any os error. It keeps the table-driven tests
// below short and uniform.
func writeFile(t *testing.T, dir, name, data string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

func TestMergeFile_DispatchesJSONByExtension(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.json", `{"b": 2}`)
	dst := writeFile(t, dir, "dst.json", `{"a": 1}`)

	err := install.MergeFile(src, dst, installconfig.Substrate{}, nil)
	if err != nil {
		t.Fatalf("MergeFile: %v", err)
	}

	out, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	var got map[string]any
	if jsonErr := json.Unmarshal(out, &got); jsonErr != nil {
		t.Fatalf("reparse dst: %v\n%s", jsonErr, out)
	}
	if got["a"] != float64(1) || got["b"] != float64(2) {
		t.Errorf("merged JSON missing keys: %+v", got)
	}
}

func TestMergeFile_DispatchesTOMLByExtension(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.toml", `b = 2`)
	dst := writeFile(t, dir, "dst.toml", `a = 1`)

	err := install.MergeFile(src, dst, installconfig.Substrate{}, nil)
	if err != nil {
		t.Fatalf("MergeFile: %v", err)
	}

	out, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	body := string(out)
	if !strings.Contains(body, "a = 1") || !strings.Contains(body, "b = 2") {
		t.Errorf("merged TOML missing keys:\n%s", body)
	}
}

func TestMergeFile_AppendStrategyDedupesLines(t *testing.T) {
	dir := t.TempDir()
	// .txt extension proves the strategy overrides extension dispatch.
	src := writeFile(t, dir, "src.txt", "dist/\ncoverage.out\n")
	dst := writeFile(t, dir, "dst.txt", "dist/\nbin/\n")

	sub := installconfig.Substrate{MergeStrategy: "append"}
	if err := install.MergeFile(src, dst, sub, nil); err != nil {
		t.Fatalf("MergeFile: %v", err)
	}

	out, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	body := string(out)
	if strings.Count(body, "dist/") != 1 {
		t.Errorf("append-strategy should dedupe dist/, got:\n%s", body)
	}
	for _, want := range []string{"dist/", "bin/", "coverage.out"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in merged output:\n%s", want, body)
		}
	}
}

func TestMergeFile_DestinationMissingErrors(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.json", `{"a": 1}`)
	missingDst := filepath.Join(dir, "does-not-exist.json")

	err := install.MergeFile(src, missingDst, installconfig.Substrate{}, nil)
	if err == nil {
		t.Fatal("expected error for missing destination, got nil")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Errorf("expected wrapped os.ErrNotExist, got %v", err)
	}
}

func TestMergeFile_DeclinesReplaceStrategy(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.json", `{"a": 1}`)
	dst := writeFile(t, dir, "dst.json", `{"a": 2}`)

	sub := installconfig.Substrate{MergeStrategy: "replace"}
	err := install.MergeFile(src, dst, sub, nil)
	if !errors.Is(err, install.ErrReplaceStrategyDelegate) {
		t.Fatalf("expected ErrReplaceStrategyDelegate, got %v", err)
	}

	// Confirm dst was NOT touched — replace path must be a no-op here;
	// the caller is responsible for routing to CopyFile.
	out, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	if string(out) != `{"a": 2}` {
		t.Errorf("dst should be untouched on replace-decline, got %q", out)
	}
}

func TestMergeFile_DeclinesUnknownStrategy(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.json", `{"a": 1}`)
	dst := writeFile(t, dir, "dst.json", `{"a": 1}`)

	sub := installconfig.Substrate{MergeStrategy: "weird-unknown"}
	err := install.MergeFile(src, dst, sub, nil)
	if err == nil {
		t.Fatal("expected error for unknown strategy, got nil")
	}
	if errors.Is(err, install.ErrReplaceStrategyDelegate) {
		t.Errorf("unknown strategy should NOT return the replace sentinel")
	}
	if !strings.Contains(err.Error(), "unknown merge_strategy") {
		t.Errorf("error should mention unknown merge_strategy, got %v", err)
	}
}

func TestMergeFile_SurfacesConflictAsLoudError(t *testing.T) {
	dir := t.TempDir()
	src := writeFile(t, dir, "src.json", `{"a": "replace"}`)
	dst := writeFile(t, dir, "dst.json", `{"a": "keep"}`)

	err := install.MergeFile(src, dst, installconfig.Substrate{}, nil)
	if err == nil {
		t.Fatal("expected loud error on conflict, got nil")
	}
	if !strings.Contains(err.Error(), "conflict") {
		t.Errorf("conflict error should mention 'conflict', got %v", err)
	}
	if !strings.Contains(err.Error(), `"a"`) {
		t.Errorf("conflict error should include conflicting path 'a', got %v", err)
	}

	// Confirm dst was NOT overwritten on conflict — the merged document
	// is only persisted when zero conflicts arise.
	out, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	if string(out) != `{"a": "keep"}` {
		t.Errorf("dst should be untouched on conflict, got %q", out)
	}
}

func TestMergeFile_MergePathDeepDedupesNamedArray(t *testing.T) {
	dir := t.TempDir()
	// Canonical claude-settings.json shape: hooks live under
	// .hooks.PreToolUse[] and identify themselves by their "matcher"
	// field. arrayDedupeKeys passes through verbatim to NewJSONMerger,
	// so the dedupe key "hooks.PreToolUse" → "matcher" must be honored
	// when both sides declare an entry with matcher="Agent".
	existingJSON := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Agent", "command": "old-cmd"}
    ]
  }
}`
	incomingJSON := `{
  "hooks": {
    "PreToolUse": [
      {"matcher": "Agent", "command": "new-cmd"},
      {"matcher": "SessionStart", "command": "fresh"}
    ]
  }
}`

	src := writeFile(t, dir, "src.json", incomingJSON)
	dst := writeFile(t, dir, "dst.json", existingJSON)

	dedupe := map[string]string{"hooks.PreToolUse": "matcher"}
	if err := install.MergeFile(src, dst, installconfig.Substrate{}, dedupe); err != nil {
		t.Fatalf("MergeFile: %v", err)
	}

	out, readErr := os.ReadFile(dst)
	if readErr != nil {
		t.Fatalf("read dst: %v", readErr)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse dst: %v\n%s", err, out)
	}
	hooks, ok := got["hooks"].(map[string]any)
	if !ok {
		t.Fatalf("hooks shape lost: %+v", got)
	}
	pretool, ok := hooks["PreToolUse"].([]any)
	if !ok {
		t.Fatalf("PreToolUse shape lost: %+v", hooks)
	}
	// Dedupe key honored → matcher="Agent" appears exactly once and
	// existing wins (old-cmd), and matcher="SessionStart" is appended.
	if len(pretool) != 2 {
		t.Errorf("expected 2 deduped entries (Agent + SessionStart), got %d: %+v", len(pretool), pretool)
	}
	matchers := map[string]string{}
	for _, e := range pretool {
		ent, _ := e.(map[string]any)
		m, _ := ent["matcher"].(string)
		c, _ := ent["command"].(string)
		matchers[m] = c
	}
	if matchers["Agent"] != "old-cmd" {
		t.Errorf("existing Agent.command should win, got %q (full=%+v)", matchers["Agent"], matchers)
	}
	if matchers["SessionStart"] != "fresh" {
		t.Errorf("incoming SessionStart not appended: %+v", matchers)
	}
}

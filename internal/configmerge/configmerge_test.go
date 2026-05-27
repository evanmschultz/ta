package configmerge_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/configmerge"
)

// ---- JSON ----------------------------------------------------------

func TestJSONMerger_NewKeysAdded(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	existing := []byte(`{"a": 1}`)
	incoming := []byte(`{"b": 2}`)
	out, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("unexpected conflicts: %+v", conflicts)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if got["a"] != float64(1) || got["b"] != float64(2) {
		t.Errorf("merged map = %+v", got)
	}
}

func TestJSONMerger_ExistingPreservedOnConflict(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	existing := []byte(`{"a": "keep"}`)
	incoming := []byte(`{"a": "replace"}`)
	out, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d: %+v", len(conflicts), conflicts)
	}
	if conflicts[0].Path != "a" {
		t.Errorf("conflict path = %q, want a", conflicts[0].Path)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got["a"] != "keep" {
		t.Errorf("existing not preserved: %+v", got)
	}
}

func TestJSONMerger_DeepMerge(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	existing := []byte(`{"mcp":{"servers":{"a":{"cmd":"ax"}}}}`)
	incoming := []byte(`{"mcp":{"servers":{"b":{"cmd":"bx"}}}}`)
	out, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("unexpected conflicts: %+v", conflicts)
	}
	if !strings.Contains(string(out), `"a"`) || !strings.Contains(string(out), `"b"`) {
		t.Errorf("deep-merge missing keys: %s", out)
	}
}

func TestJSONMerger_ArrayDedupeByCommand(t *testing.T) {
	merger := configmerge.NewJSONMerger(map[string]string{"hooks": "command"})
	existing := []byte(`{"hooks":[{"command":"a","args":["1"]}]}`)
	incoming := []byte(`{"hooks":[{"command":"a","args":["2"]}, {"command":"b","args":["3"]}]}`)
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	hooks, _ := got["hooks"].([]any)
	if len(hooks) != 2 {
		t.Errorf("dedupe by command should keep 2 entries, got %d (%+v)", len(hooks), hooks)
	}
}

func TestJSONMerger_EmptyExisting(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	out, _, err := merger.Merge(nil, []byte(`{"x":1}`))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !strings.Contains(string(out), `"x"`) {
		t.Errorf("empty-existing path lost incoming: %s", out)
	}
}

func TestJSONMerger_BothEmpty(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	out, conflicts, err := merger.Merge(nil, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("unexpected conflicts: %+v", conflicts)
	}
	if !strings.Contains(string(out), "{") {
		t.Errorf("expected empty object output, got %q", out)
	}
}

func TestJSONMerger_TypeMismatchRecorded(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	existing := []byte(`{"a": "scalar"}`)
	incoming := []byte(`{"a": {"nested": true}}`)
	_, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 1 || conflicts[0].Reason != "type-mismatch" {
		t.Errorf("expected type-mismatch conflict, got %+v", conflicts)
	}
}

// ---- TOML ----------------------------------------------------------

func TestTOMLMerger_NewKeysAdded(t *testing.T) {
	merger := configmerge.NewTOMLMerger(nil)
	existing := []byte(`a = 1`)
	incoming := []byte(`b = 2`)
	out, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 0 {
		t.Errorf("unexpected conflicts: %+v", conflicts)
	}
	var got map[string]any
	if err := toml.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v\n%s", err, out)
	}
	if got["a"] != int64(1) || got["b"] != int64(2) {
		t.Errorf("merged map = %+v", got)
	}
}

func TestTOMLMerger_ExistingPreservedOnConflict(t *testing.T) {
	merger := configmerge.NewTOMLMerger(nil)
	existing := []byte(`a = "keep"`)
	incoming := []byte(`a = "replace"`)
	out, conflicts, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(conflicts) != 1 {
		t.Errorf("expected 1 conflict, got %+v", conflicts)
	}
	var got map[string]any
	if err := toml.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if got["a"] != "keep" {
		t.Errorf("existing not preserved: %+v", got)
	}
}

func TestTOMLMerger_DeepMerge(t *testing.T) {
	merger := configmerge.NewTOMLMerger(nil)
	existing := []byte(`[mcp_servers.a]
command = "ax"`)
	incoming := []byte(`[mcp_servers.b]
command = "bx"`)
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !strings.Contains(string(out), "a") || !strings.Contains(string(out), "b") {
		t.Errorf("deep-merge missing entries: %s", out)
	}
}

// ---- Line ---------------------------------------------------------

func TestLineMerger_AppendsNewLines(t *testing.T) {
	merger := configmerge.NewLineMerger()
	existing := []byte("dist/\nbin/\n")
	incoming := []byte("coverage.out\nnode_modules/\n")
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	for _, want := range []string{"dist/", "bin/", "coverage.out", "node_modules/"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("missing %q in merged: %s", want, out)
		}
	}
}

func TestLineMerger_DedupeExact(t *testing.T) {
	merger := configmerge.NewLineMerger()
	existing := []byte("dist/\nbin/\n")
	incoming := []byte("dist/\ncoverage.out\n")
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if strings.Count(string(out), "dist/") != 1 {
		t.Errorf("dedupe failed: %s", out)
	}
}

func TestLineMerger_TrailingWhitespaceTrimForDedupe(t *testing.T) {
	merger := configmerge.NewLineMerger()
	existing := []byte("dist/  \n")
	incoming := []byte("dist/\n")
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if strings.Count(string(out), "dist/") != 1 {
		t.Errorf("trailing-whitespace trim failed: %q", out)
	}
}

func TestLineMerger_BothEmpty(t *testing.T) {
	merger := configmerge.NewLineMerger()
	out, _, err := merger.Merge(nil, nil)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("expected empty output, got %q", out)
	}
}

func TestLineMerger_NoExisting(t *testing.T) {
	merger := configmerge.NewLineMerger()
	out, _, err := merger.Merge(nil, []byte("a\nb\n"))
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	if !strings.Contains(string(out), "a") || !strings.Contains(string(out), "b") {
		t.Errorf("incoming lost: %q", out)
	}
}

// ---- Round-trip stability -----------------------------------------

func TestJSONMerger_ArrayDedupeByMatcherAndCommandTuple(t *testing.T) {
	merger := configmerge.NewJSONMerger(map[string]string{"hooks": "matcher,command"})
	existing := []byte(`{"hooks":[{"matcher":"bash","command":"bash_guard","args":["1"]}]}`)
	incoming := []byte(`{"hooks":[{"matcher":"bash","command":"bash_guard","args":["2"]}, {"matcher":"bash","command":"other_guard","args":["3"]}, {"matcher":"git","command":"bash_guard","args":["4"]}]}`)
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	hooks, _ := got["hooks"].([]any)
	if len(hooks) != 3 {
		t.Errorf("dedupe by (matcher,command) tuple should keep 3 entries (1 existing + 2 incoming unique), got %d (%+v)", len(hooks), hooks)
	}
}

func TestJSONMerger_ArrayDedupeSameMatcherDifferentCommandSurvives(t *testing.T) {
	merger := configmerge.NewJSONMerger(map[string]string{"hooks": "matcher,command"})
	existing := []byte(`{}`)
	incoming := []byte(`{"hooks":[{"matcher":"bash","command":"guard_a"}, {"matcher":"bash","command":"guard_b"}]}`)
	out, _, err := merger.Merge(existing, incoming)
	if err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("reparse: %v", err)
	}
	hooks, _ := got["hooks"].([]any)
	if len(hooks) != 2 {
		t.Errorf("same matcher with different commands should produce 2 distinct entries, got %d", len(hooks))
	}
}

func TestJSONMerger_IdempotentReapply(t *testing.T) {
	merger := configmerge.NewJSONMerger(nil)
	a := []byte(`{"a":1,"b":{"c":2}}`)
	b := []byte(`{"d":3,"b":{"e":4}}`)
	first, _, err := merger.Merge(a, b)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, _, err := merger.Merge(first, b)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if string(first) != string(second) {
		t.Errorf("merge(merge(a,b),b) != merge(a,b)\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}

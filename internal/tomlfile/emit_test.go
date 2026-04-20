package tomlfile

import (
	"bytes"
	"math"
	"strings"
	"testing"
	"time"
)

func TestEmitSectionBasic(t *testing.T) {
	got, err := EmitSection("task.task_001", map[string]any{
		"id":     "TASK-001",
		"status": "todo",
	})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	want := "[task.task_001]\n" +
		"id = \"TASK-001\"\n" +
		"status = \"todo\"\n"
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmitSectionSortsKeys(t *testing.T) {
	got, err := EmitSection("x", map[string]any{
		"z": 1,
		"a": 2,
		"m": 3,
	})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	want := "[x]\na = 2\nm = 3\nz = 1\n"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestEmitSectionRejectsEmptyPath(t *testing.T) {
	if _, err := EmitSection("", nil); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestEmitSectionRejectsNonBareKey(t *testing.T) {
	if _, err := EmitSection("x", map[string]any{"has space": 1}); err == nil {
		t.Fatal("expected error on non-bare key")
	}
}

func TestEmitValueTypes(t *testing.T) {
	ts := time.Date(2026, 4, 20, 12, 30, 45, 0, time.UTC)
	got, err := EmitSection("x", map[string]any{
		"s":        "hello",
		"b_true":   true,
		"b_false":  false,
		"i":        int(42),
		"i64":      int64(-7),
		"u":        uint(5),
		"f":        3.14,
		"f_whole":  float64(2),
		"t":        ts,
		"arr":      []any{"a", "b"},
		"inline_t": map[string]any{"k": 1},
	})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	wantContains := []string{
		`s = "hello"`,
		`b_true = true`,
		`b_false = false`,
		`i = 42`,
		`i64 = -7`,
		`u = 5`,
		`f = 3.14`,
		`f_whole = 2.0`,
		`t = 2026-04-20T12:30:45Z`,
		`arr = ["a", "b"]`,
		`inline_t = {k = 1}`,
	}
	s := string(got)
	for _, want := range wantContains {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q:\n%s", want, s)
		}
	}
}

func TestEmitStringEscapes(t *testing.T) {
	got, err := EmitSection("x", map[string]any{
		"q": `a "quoted" line with \ backslash`,
	})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	want := "[x]\nq = \"a \\\"quoted\\\" line with \\\\ backslash\"\n"
	if string(got) != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestEmitStringControlCharEscape(t *testing.T) {
	got, err := EmitSection("x", map[string]any{"c": "\x01"})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	if !bytes.Contains(got, []byte(`\u0001`)) {
		t.Errorf("control char not escaped as \\u0001: %s", got)
	}
}

func TestEmitMultilineString(t *testing.T) {
	body := "## Approach\n\nWe did a thing.\n"
	got, err := EmitSection("task.t", map[string]any{"body": body})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	s := string(got)
	if !strings.Contains(s, `body = """`) {
		t.Errorf("missing multi-line opener: %s", s)
	}
	if !strings.Contains(s, "## Approach") {
		t.Errorf("missing body content: %s", s)
	}
	if !strings.HasSuffix(strings.TrimRight(s, "\n"), `"""`) {
		t.Errorf("missing multi-line closer: %s", s)
	}
}

func TestEmitMultilineTripleQuoteEscape(t *testing.T) {
	body := `contains """triple quote""" sequence
second line`
	got, err := EmitSection("x", map[string]any{"body": body})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	if !bytes.Contains(got, []byte(`\"`)) {
		t.Errorf("triple-quote sequence should be broken up with \\\"; got:\n%s", got)
	}
}

func TestEmitFloatSpecials(t *testing.T) {
	cases := []struct {
		v    float64
		want string
	}{
		{math.NaN(), "nan"},
		{math.Inf(1), "inf"},
		{math.Inf(-1), "-inf"},
	}
	for _, tc := range cases {
		got, err := EmitSection("x", map[string]any{"f": tc.v})
		if err != nil {
			t.Fatalf("EmitSection(%v): %v", tc.v, err)
		}
		if !bytes.Contains(got, []byte(tc.want)) {
			t.Errorf("float %v: missing %q in %s", tc.v, tc.want, got)
		}
	}
}

func TestEmitArrayHeterogeneous(t *testing.T) {
	got, err := EmitSection("x", map[string]any{
		"a": []any{1, 2, 3},
	})
	if err != nil {
		t.Fatalf("EmitSection: %v", err)
	}
	if !bytes.Contains(got, []byte(`a = [1, 2, 3]`)) {
		t.Errorf("array emission wrong: %s", got)
	}
}

func TestEmitUnsupportedType(t *testing.T) {
	type custom struct{ X int }
	if _, err := EmitSection("x", map[string]any{"v": custom{X: 1}}); err == nil {
		t.Fatal("expected error on unsupported type")
	}
}

func TestEmitNilValueRejected(t *testing.T) {
	if _, err := EmitSection("x", map[string]any{"v": nil}); err == nil {
		t.Fatal("expected error on nil value")
	}
}

func TestIsBareKey(t *testing.T) {
	cases := []struct {
		k    string
		want bool
	}{
		{"id", true},
		{"task_001", true},
		{"TASK-001", true},
		{"abc123", true},
		{"", false},
		{"has space", false},
		{"dotted.key", false},
		{`"quoted"`, false},
	}
	for _, tc := range cases {
		if got := isBareKey(tc.k); got != tc.want {
			t.Errorf("isBareKey(%q) = %v, want %v", tc.k, got, tc.want)
		}
	}
}

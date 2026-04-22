package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/evanmschultz/ta/internal/templates"
)

func newTemplateLibraryFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, name := range []string{"schema", "dogfood"} {
		path := filepath.Join(root, name+".toml")
		if err := os.WriteFile(path, []byte(cliTaskSchema), 0o644); err != nil {
			t.Fatalf("seed %s: %v", name, err)
		}
	}
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)
	return root
}

func TestTemplateListCmdDefault(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	for _, want := range []string{"dogfood", "schema"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q: %s", want, s)
		}
	}
}

func TestTemplateListCmdJSON(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	want := []string{"dogfood", "schema"}
	if len(payload.Templates) != len(want) {
		t.Fatalf("got %v, want %v", payload.Templates, want)
	}
	for i, n := range want {
		if payload.Templates[i] != n {
			t.Errorf("idx %d: got %q, want %q", i, payload.Templates[i], n)
		}
	}
}

func TestTemplateListCmdEmpty(t *testing.T) {
	root := t.TempDir()
	restore := templates.SetRootForTest(root)
	t.Cleanup(restore)

	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"list", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Templates []string `json:"templates"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if len(payload.Templates) != 0 {
		t.Errorf("want empty list, got %v", payload.Templates)
	}
}

func TestTemplateShowCmdDefault(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"show", "schema"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	s := out.String()
	// Glamour-rendered: assert the load-bearing schema fragments survive
	// through ANSI styling.
	for _, want := range []string{"plans", "task"} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q: %s", want, s)
		}
	}
}

func TestTemplateShowCmdJSON(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"show", "schema", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("execute: %v stderr=%s", err, errOut.String())
	}
	var payload struct {
		Template string `json:"template"`
		Bytes    string `json:"bytes"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if payload.Template != "schema" {
		t.Errorf("template = %q, want schema", payload.Template)
	}
	if !strings.Contains(payload.Bytes, "[plans.task]") {
		t.Errorf("bytes missing schema body: %q", payload.Bytes)
	}
}

func TestTemplateShowCmdMissingErrors(t *testing.T) {
	newTemplateLibraryFixture(t)
	cmd := newTemplateCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"show", "ghost"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("expected error showing missing template")
	}
}

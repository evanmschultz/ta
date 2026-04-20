package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunVersionPrintsToStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--version"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, errOut.String())
	}
	if !strings.HasPrefix(out.String(), "ta ") {
		t.Errorf("stdout should start with 'ta <version>': %q", out.String())
	}
	if !strings.Contains(out.String(), "go ") {
		t.Errorf("stdout missing go version line: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Errorf("stderr should be empty on --version: %q", errOut.String())
	}
}

func TestRunHelpPrintsUsageToStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--help"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr=%q", code, errOut.String())
	}
	for _, want := range []string{"ta", "list_sections", "upsert", "--version", "--log-startup"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("help output missing %q:\n%s", want, out.String())
		}
	}
}

func TestRunUnknownFlagReturnsTwo(t *testing.T) {
	var out, errOut bytes.Buffer
	code := run([]string{"--not-a-real-flag"}, &out, &errOut)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if errOut.Len() == 0 {
		t.Errorf("expected error output on stderr")
	}
}

func TestVersionFallsBackToDevel(t *testing.T) {
	v := version()
	if v == "" {
		t.Fatal("version empty")
	}
}

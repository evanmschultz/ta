//go:build mage

// Mage build automation for ta.
//
// Run "mage -l" to list targets. The top-level gate is "mage check" which
// runs fmtcheck, vet, test, and tidy.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const binDir = "bin"

// localBuildVCSFlag disables VCS stamping so `go build` stays quiet in
// bare-worktree checkouts that confuse Go's VCS auto-detection.
const localBuildVCSFlag = "-buildvcs=false"

// Build compiles the ta binary to ./bin/ta for local dev.
func Build() error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	return run("go", "build", localBuildVCSFlag, "-o", binDir+"/ta", "./cmd/ta")
}

// Install builds ta from the current working tree and drops the binary at
// $HOME/.local/bin/ta so MCP clients can invoke it by bare name without
// requiring a Go toolchain on the end user's machine. Also seeds
// $HOME/.ta/schema.toml from examples/schema.toml on first install;
// existing user schemas are never overwritten.
//
// Dev-only dogfood target. Orchestrator and subagents MUST NOT invoke it.
func Install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	installDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return fmt.Errorf("create install dir %q: %w", installDir, err)
	}
	installedPath := filepath.Join(installDir, "ta")
	if err := run("go", "build", localBuildVCSFlag, "-o", installedPath, "./cmd/ta"); err != nil {
		return err
	}
	return seedHomeSchema(home)
}

// seedHomeSchema creates $HOME/.ta/ if missing and copies
// examples/schema.toml to $HOME/.ta/schema.toml when no schema file is
// already present. An existing schema is left untouched so repeated
// `mage install` runs never clobber user edits.
func seedHomeSchema(home string) error {
	taDir := filepath.Join(home, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		return fmt.Errorf("create %q: %w", taDir, err)
	}
	dst := filepath.Join(taDir, "schema.toml")
	if _, err := os.Stat(dst); err == nil {
		fmt.Printf("ta: leaving existing %s untouched\n", dst)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %q: %w", dst, err)
	}
	src := filepath.Join("examples", "schema.toml")
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %q: %w", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("write %q: %w", dst, err)
	}
	fmt.Printf("ta: seeded %s\n", dst)
	return nil
}

// Test runs the full test suite with the race detector.
func Test() error {
	return run("go", "test", "-race", "-count=1", "./...")
}

// Cover produces a function-level coverage report.
func Cover() error {
	if err := run("go", "test", "-race", "-coverprofile=coverage.out", "./..."); err != nil {
		return err
	}
	return run("go", "tool", "cover", "-func=coverage.out")
}

// Vet runs go vet across the module.
func Vet() error {
	return run("go", "vet", "./...")
}

// Fmt formats sources in place (gofmt -s).
func Fmt() error {
	return run("gofmt", "-s", "-w", ".")
}

// FmtCheck fails if any file is not gofmt -s clean.
func FmtCheck() error {
	out, err := exec.Command("gofmt", "-s", "-l", ".").Output()
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Fprint(os.Stderr, string(out))
		return fmt.Errorf("files are not gofmt -s clean")
	}
	return nil
}

// Tidy runs go mod tidy and fails if go.mod or go.sum changed.
func Tidy() error {
	before, err := snapshot("go.mod", "go.sum")
	if err != nil {
		return err
	}
	if err := run("go", "mod", "tidy"); err != nil {
		return err
	}
	after, err := snapshot("go.mod", "go.sum")
	if err != nil {
		return err
	}
	if before != after {
		return fmt.Errorf("go.mod or go.sum changed; commit the tidy result")
	}
	return nil
}

// Check is the composite gate: fmtcheck, vet, test, tidy.
func Check() error {
	for _, step := range []func() error{FmtCheck, Vet, Test, Tidy} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// Clean removes build artifacts.
func Clean() error {
	return os.RemoveAll(binDir)
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func snapshot(paths ...string) (string, error) {
	var b strings.Builder
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		b.WriteString(p)
		b.WriteByte('\n')
		b.Write(data)
		b.WriteByte('\n')
	}
	return b.String(), nil
}

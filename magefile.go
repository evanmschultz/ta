//go:build mage

// Mage build automation for ta.
//
// Run "mage -l" to list targets. The top-level gate is "mage check" which
// runs fmtcheck, vet, test, and tidy.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const binDir = "bin"

// Build compiles the ta binary to ./bin/ta.
func Build() error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	return run("go", "build", "-o", binDir+"/ta", "./cmd/ta")
}

// Install installs the ta binary into $GOBIN.
func Install() error {
	return run("go", "install", "./cmd/ta")
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

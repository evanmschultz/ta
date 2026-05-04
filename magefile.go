//go:build mage

// Mage build automation for ta.
//
// Run "mage -l" to list targets. The top-level gate is "mage check" which
// runs fmtcheck, vet, test, and tidy.
//
// Agent-facing JSON output (V2-PLAN §12.12): set MAGEFILE_JSON=1 to
// route "go test" through -json on Test / Check / Cover. Fmt, Vet,
// and Tidy emit plain text either way — the JSON switch only affects
// the test-runner step, which is the surface agents parse.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/render"
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
// requiring a Go toolchain on the end user's machine. Also creates
// $HOME/.ta/schema.toml as an EMPTY file on first install (per 2026-04-24
// amendment — no embedded default; user populates it from `examples/`,
// from CLI `ta schema --action=create ...`, or by promoting a project's
// `.ta/schema.toml` via `ta template save`). Existing user schemas are
// never overwritten.
//
// User-facing progress and completion output routes through laslig (via
// internal/render) so `mage install` looks visually consistent with the
// rest of the ta CLI surface per V2-PLAN §12.17.5 [A3]. Notices and
// facts go to stderr; the underlying `go build` output stays on the
// subprocess's inherited stdout/stderr.
func Install() error {
	rr := render.New(os.Stderr)

	home, err := os.UserHomeDir()
	if err != nil {
		return installError(rr, "resolve home", err)
	}
	installDir := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		return installError(rr, fmt.Sprintf("create install dir %q", installDir), err)
	}
	installedPath := filepath.Join(installDir, "ta")
	if err := run("go", "build", localBuildVCSFlag, "-o", installedPath, "./cmd/ta"); err != nil {
		return installError(rr, "build ta", err)
	}
	schemaPath, schemaOutcome, err := seedHomeSchema(rr, home)
	if err != nil {
		return err
	}
	if err := rr.Success("install complete", "ta built from working tree", nil); err != nil {
		return err
	}
	return rr.Facts([]laslig.Field{
		{Label: "binary", Value: installedPath},
		{Label: "schema", Value: schemaPath},
		{Label: "outcome", Value: schemaOutcome},
	})
}

// seedHomeSchema creates $HOME/.ta/ if missing and writes an EMPTY
// $HOME/.ta/schema.toml on first install. Per 2026-04-24 amendment, no
// default schema is embedded in the binary or copied from examples/.
// The user populates the file via one of:
//
//   - copy a sample from `examples/` in the ta repo (e.g.
//     `cp examples/schema.toml ~/.ta/schema.toml`),
//   - build a schema in a project (`ta schema --action=create ...`)
//     and promote it via `ta template save`, or
//   - hand-edit the file directly.
//
// An existing schema is left untouched so repeated `mage install` runs
// never clobber user edits. Returns the destination path and a short
// outcome label ("created" or "untouched") for the Install summary.
func seedHomeSchema(rr *render.Renderer, home string) (string, string, error) {
	taDir := filepath.Join(home, ".ta")
	if err := os.MkdirAll(taDir, 0o755); err != nil {
		return "", "", installError(rr, fmt.Sprintf("create %q", taDir), err)
	}
	dst := filepath.Join(taDir, "schema.toml")
	if _, err := os.Stat(dst); err == nil {
		if nerr := rr.Notice(
			laslig.NoticeInfoLevel,
			"schema untouched",
			fmt.Sprintf("%s already present; leaving user edits in place", dst),
			nil,
		); nerr != nil {
			return "", "", nerr
		}
		return dst, "untouched", nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", "", installError(rr, fmt.Sprintf("stat %q", dst), err)
	}
	if err := os.WriteFile(dst, nil, 0o644); err != nil {
		return "", "", installError(rr, fmt.Sprintf("write %q", dst), err)
	}
	if nerr := rr.Notice(
		laslig.NoticeInfoLevel,
		"empty schema created",
		fmt.Sprintf("wrote empty %s — populate it before running `ta init` in projects", dst),
		[]string{
			"Copy a sample: cp examples/schema.toml ~/.ta/schema.toml (see the ta repo)",
			"Or build via CLI: ta schema --action=create --kind=db --name=<name> --data='{...}'",
			"Or promote from a project: ta template save (after building schema in a project)",
			"Sample schemas live in the ta repo under examples/",
		},
	); nerr != nil {
		return "", "", nerr
	}
	return dst, "created", nil
}

// installError renders a laslig error notice for a user-facing Install
// failure and returns a wrapped Go error so mage still reports a
// non-zero exit. The wrapped error keeps the stage label and original
// cause for post-mortem grep; the notice is the pretty surface.
func installError(rr *render.Renderer, stage string, cause error) error {
	_ = rr.Notice(
		laslig.NoticeErrorLevel,
		"install failed",
		fmt.Sprintf("%s: %s", stage, cause.Error()),
		nil,
	)
	return fmt.Errorf("%s: %w", stage, cause)
}

// Test runs the full test suite with the race detector. Set
// MAGEFILE_JSON=1 to route "go test" through -json for agent-parseable
// output (V2-PLAN §12.12).
func Test() error {
	args := []string{"test", "-race", "-count=1"}
	if jsonMode() {
		args = append(args, "-json")
	}
	args = append(args, "./...")
	return run("go", args...)
}

// TestFunc runs ONLY the test functions matching the given regex
// pattern (`go test -run <pattern>`) within the given package path
// (default `./...` — pass TA_TEST_PKG=./internal/ops to scope tighter).
// Cascade discipline: a builder or QA agent operating below strict
// package level invokes `MAGEFILE_JSON=1 mage testFunc TestMyThing`
// (or `mage testFunc 'TestPickerFilter|TestPickerBucket'` for multiple)
// so their result is unmuddied by sibling agents' WIP. Higher levels
// run `mage Test` to verify the integrated whole.
//
// Args: first positional = `-run` regex (one func or `|`-joined alts).
// TA_TEST_PKG env var = package path scope (default `./...`).
func TestFunc(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("testFunc: pattern required (e.g. mage testFunc TestMyThing)")
	}
	pkg := os.Getenv("TA_TEST_PKG")
	if pkg == "" {
		pkg = "./..."
	}
	args := []string{"test", "-race", "-count=1", "-run", pattern}
	if jsonMode() {
		args = append(args, "-json")
	}
	args = append(args, pkg)
	return run("go", args...)
}

// TestFuncs is the multi-pattern shorthand: joins the given function
// names with `|` and delegates to `go test -run`. Equivalent to
// `mage testFunc 'TestA|TestB|TestC'` but takes them as separate args
// so callers don't have to quote the pipe.
func TestFuncs(names ...string) error {
	if len(names) == 0 {
		return fmt.Errorf("testFuncs: at least one test name required")
	}
	return TestFunc(strings.Join(names, "|"))
}

// TestPkg runs every test in ONE package path (default `./...`).
// Cascade discipline: package-level QA invokes `mage testPkg
// ./internal/ops` so the verdict reflects exactly what their slice
// owns. Set MAGEFILE_JSON=1 for agent-parseable output.
func TestPkg(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("testPkg: package path required (e.g. mage testPkg ./internal/ops)")
	}
	args := []string{"test", "-race", "-count=1"}
	if jsonMode() {
		args = append(args, "-json")
	}
	args = append(args, pkg)
	return run("go", args...)
}

// Cover produces a function-level coverage report. Set MAGEFILE_JSON=1
// to emit the test-runner step as -json (the coverage-tool step
// remains text — it is a digest, not a parse target).
func Cover() error {
	testArgs := []string{"test", "-race", "-coverprofile=coverage.out"}
	if jsonMode() {
		testArgs = append(testArgs, "-json")
	}
	testArgs = append(testArgs, "./...")
	if err := run("go", testArgs...); err != nil {
		return err
	}
	return run("go", "tool", "cover", "-func=coverage.out")
}

// jsonMode reports whether MAGEFILE_JSON is set to a truthy value.
func jsonMode() bool {
	v := os.Getenv("MAGEFILE_JSON")
	return v != "" && v != "0" && v != "false"
}

// Vet runs go vet across the module.
func Vet() error {
	return run("go", "vet", "./...")
}

// Fmt formats sources in place via gofumpt (latest). Auto-installs
// gofumpt to GOBIN if missing so contributors don't have to manage it
// out of band. gofumpt is a strict superset of gofmt -s: every gofmt -s
// fix plus tighter standards (no else-if-then-else without separation,
// no `,` before `... )`, etc.). The memory rule + CONTRIBUTING.md
// formalize this — never invoke gofmt or raw gofumpt directly; always
// route through `mage Fmt` / `mage FmtCheck`.
func Fmt() error {
	if err := ensureGofumpt(); err != nil {
		return err
	}
	return run("gofumpt", "-w", ".")
}

// FmtCheck fails if any file is not gofumpt-clean. Listing produced by
// `gofumpt -l`; non-empty = drift; emits the offending paths to stderr.
func FmtCheck() error {
	if err := ensureGofumpt(); err != nil {
		return err
	}
	out, err := exec.Command("gofumpt", "-l", ".").Output()
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Fprint(os.Stderr, string(out))
		return fmt.Errorf("files are not gofumpt-clean (run `mage fmt`)")
	}
	return nil
}

// ensureGofumpt makes `gofumpt` resolvable on PATH by installing the
// latest from upstream when missing. Idempotent — `go install` against
// an already-current binary is a no-op.
func ensureGofumpt() error {
	if _, err := exec.LookPath("gofumpt"); err == nil {
		return nil
	}
	return run("go", "install", "mvdan.cc/gofumpt@latest")
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

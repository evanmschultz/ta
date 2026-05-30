//go:build mage

// Mage build automation for ta.
//
// Run "mage -l" to list targets. The top-level gate is "mage ci" which runs
// FormatCheck, Vet, Coverage (race+cover combined), Tidy. ta-specific:
// TemplatesA11y is a separate gate (CI workflow .github/workflows/ci.yml::a11y)
// to keep the Go-only `ci` job free of Node/pnpm setup.
//
// Canonical 12-target shape (per tillsyn P6 — see R-SHIP-TA refinement
// b8ee83b2-1e5c-4002-bdef-60dfb191ba30):
//
//	TestFunc(pkg, fn)  builder + build-QA       go test -run "^<Func>$" -count=1 -race <pkg>
//	TestPkg(pkg)       plan-QA read-only        go test -count=1 <pkg>
//	Test               closeout/orch            go test ./...
//	RacePkg(pkg)       build-QA                 go test -race -count=1 <pkg>
//	Race               closeout/orch            go test -race ./...
//	FormatFile(file)   builder + build-QA       gofumpt -w <file>
//	Format             closeout/orch            gofumpt -w .
//	FormatCheck        ci                       gofumpt -l . && fail if non-empty
//	VetPkg(pkg)        builder + build-QA       go vet <pkg>
//	Vet                closeout/orch            go vet ./...
//	Tidy               orch-only                go mod tidy + diff-exit-code
//	CI                 closeout/orch            FormatCheck + Vet + Coverage + Tidy
//
// ta-specific extras (preserved): Build/Install/Cover/Vhs/Serve/TemplatesA11y/Clean.
//
// Test output is rendered through laslig/gotestout, which auto-detects TTY:
// humans get a styled summary, agents/CI pipes get plain text.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/evanmschultz/laslig"
	"github.com/evanmschultz/laslig/gotestout"

	"github.com/evanmschultz/ta/internal/install_hygiene"
)

const binDir = "bin"

// localBuildVCSFlag disables VCS stamping so `go build` stays quiet in
// bare-worktree checkouts that confuse Go's VCS auto-detection.
const localBuildVCSFlag = "-buildvcs=false"

// Aliases preserves the familiar hyphenated task names while keeping the visible target list small.
var Aliases = map[string]interface{}{
	"check":          CI,
	"fmt":            Format,
	"fmt-check":      FormatCheck,
	"format-check":   FormatCheck,
	"format-file":    FormatFile,
	"test-func":      TestFunc,
	"test-pkg":       TestPkg,
	"race-pkg":       RacePkg,
	"vet-pkg":        VetPkg,
	"templates-a11y": TemplatesA11y,
}

// Build compiles the ta binary to ./bin/ta for local dev.
func Build() error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	return run("go", "build", localBuildVCSFlag, "-o", binDir+"/ta", "./cmd/ta")
}

// Install builds ta from the current working tree and drops the binary
// at $HOME/.local/bin/ta so MCP clients can invoke it by bare name
// without requiring a Go toolchain on the end user's machine. It also
// seeds $HOME/.ta/schema.toml on first install (empty placeholder per
// the 2026-04-24 amendment — never overwritten if already present).
//
// Per drop_004 L3-G2-D1 the install hygiene logic (schema seed +
// validation + Facts row + error formatting) lives in
// internal/install_hygiene/, a regular Go package that is tested under
// `mage testPkg ./internal/install_hygiene`. This mage target is the
// thin wrapper: it supplies a BuildFunc that shells out to `go build`
// (mage's domain) and delegates everything else to install_hygiene.Run.
func Install() error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home: %w", err)
	}
	_, err = install_hygiene.Run(context.Background(), install_hygiene.Options{
		Home: home,
		Errf: os.Stderr,
		BuildFunc: func(dst string) error {
			return run("go", "build", localBuildVCSFlag, "-o", dst, "./cmd/ta")
		},
	})
	return err
}

// Test runs the full test suite without race detection or coverage.
// Closeout/orchestrator surface — fastest all-package gate. Use TestPkg
// for one package, TestFunc for one function, Race for race detection.
func Test() error {
	return runGoTest("./...")
}

// TestPkg runs every test in ONE package path without race detection.
// Plan-QA read-only surface — verifies a code claim against a single
// package without race overhead.
func TestPkg(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("testPkg: package path required (e.g. mage testPkg ./internal/ops)")
	}
	return runGoTest(pkg, "-count=1")
}

// TestFunc runs ONE named test function in ONE package path with race
// detection and no result caching. Builder + build-QA surface — verifies
// a builder's just-edited test func without sibling pollution.
//
// Per cascade methodology (CASCADE_METHODOLOGY.md): a builder operating
// below strict package level invokes `mage testFunc <pkg> <TestFuncName>`
// so its verdict is unmuddied by sibling agents' WIP. The signature is
// (pkg, testName) — the prior 1-arg (pattern) form is retired.
func TestFunc(pkg, testName string) error {
	pkg = strings.TrimSpace(pkg)
	testName = strings.TrimSpace(testName)
	if pkg == "" {
		return fmt.Errorf("testFunc: package path required (e.g. mage testFunc ./internal/ops TestMyThing)")
	}
	if testName == "" {
		return fmt.Errorf("testFunc: test function name required (e.g. mage testFunc ./internal/ops TestMyThing)")
	}
	return runGoTest(pkg, "-run", "^"+testName+"$", "-race", "-count=1")
}

// Race runs the full test suite over every package with the race detector
// enabled. Closeout/orchestrator surface. Use RacePkg for one package.
func Race() error {
	return runGoTest("./...", "-race")
}

// RacePkg runs tests with the race detector for ONE package path.
// Build-QA surface — verifies a builder's just-shipped package is
// race-clean.
func RacePkg(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("racePkg: package path required (e.g. mage racePkg ./internal/ops)")
	}
	return runGoTest(pkg, "-race", "-count=1")
}

// Cover produces a function-level coverage report (race + cover combined
// for the CI gate). The test-runner step routes through laslig/gotestout
// (TTY-aware); the coverage-tool step emits plain text either way.
func Cover() error {
	if err := runGoTest("./...", "-race", "-coverprofile=coverage.out"); err != nil {
		return err
	}
	return run("go", "tool", "cover", "-func=coverage.out")
}

// Vhs renders every `cmd/ta/testdata/vhs/*.tape` through the `vhs`
// CLI and asserts a per-tape positive-content marker landed in the
// produced `.txt` artifact. Each tape MUST carry a `# expect: <text>`
// header — the marker is a deterministic post-action string the
// model's View() emits in response to the tape's input. After vhs
// runs, this target greps the matching .txt for the marker; an empty
// or vacuous tape (one that produces no terminal output) fails loud
// instead of silently passing. `vhs` is not gated by mage ci;
// missing vhs binary fails the target with an install hint but does
// not block other gates.
func Vhs() error {
	if _, err := exec.LookPath("vhs"); err != nil {
		return fmt.Errorf("vhs binary not on PATH; install with `go install charm.land/vhs/v2@latest` or `brew install vhs`: %w", err)
	}
	tapes, err := filepath.Glob("cmd/ta/testdata/vhs/*.tape")
	if err != nil {
		return fmt.Errorf("vhs: glob tapes: %w", err)
	}
	if len(tapes) == 0 {
		return fmt.Errorf("vhs: no tapes found under cmd/ta/testdata/vhs/")
	}
	for _, tape := range tapes {
		marker, err := readTapeExpectMarker(tape)
		if err != nil {
			return err
		}
		if err := run("vhs", tape); err != nil {
			return fmt.Errorf("vhs: render %s: %w", tape, err)
		}
		txtPath := strings.TrimSuffix(tape, ".tape") + ".txt"
		out, err := os.ReadFile(txtPath)
		if err != nil {
			return fmt.Errorf("vhs: read produced .txt %s: %w", txtPath, err)
		}
		if !strings.Contains(string(out), marker) {
			return fmt.Errorf("vhs: marker %q absent in %s (vacuous tape)", marker, txtPath)
		}
	}
	return nil
}

// readTapeExpectMarker scans a .tape file for the first line of the
// form `# expect: <marker>` and returns the trimmed marker. Tapes
// without the header fail the Vhs target loud — the marker is the
// only thing distinguishing a real-rendering tape from a no-op.
func readTapeExpectMarker(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("vhs: read tape %s: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(trimmed, "# expect:"); ok {
			marker := strings.TrimSpace(rest)
			if marker == "" {
				return "", fmt.Errorf("vhs: tape %s has empty `# expect:` marker", path)
			}
			return marker, nil
		}
	}
	return "", fmt.Errorf("vhs: tape %s missing `# expect: <marker>` header", path)
}

// runGoTest invokes `go test -json [extraArgs] [$TA_GO_TEST_FLAGS] <pkg>`
// and pipes the event stream through laslig/gotestout for TTY-aware
// rendering. NO race detection or coverage in the baseline — callers add
// `-race`, `-cover`, etc. via extraArgs as needed. This change (vs the
// pre-2026-05-29 baked-in `-race -count=1`) aligns ta with the canonical
// 12-target shape, where Test/TestPkg = no race and Race/RacePkg/TestFunc
// = explicit race.
//
// The TA_GO_TEST_FLAGS envvar is whitespace-tokenized via strings.Fields
// and each non-empty token appended after the `-run <pattern>` hand-off
// so callers can flip `-update`, `-race=false`, `-timeout=30s`, or any
// combination without forcing a magefile edit. Empty / unset is a no-op.
func runGoTest(pkg string, extraArgs ...string) error {
	args := append([]string{"test", "-json"}, extraArgs...)
	for _, tok := range strings.Fields(os.Getenv("TA_GO_TEST_FLAGS")) {
		args = append(args, tok)
	}
	args = append(args, pkg)
	cmd := exec.Command("go", args...)
	cmd.Stderr = os.Stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	summary, renderErr := gotestout.Render(os.Stderr, stdout, gotestout.Options{
		Policy: laslig.Policy{Format: laslig.FormatAuto},
		View:   gotestout.ViewCompact,
	})
	// Drain any remaining bytes if Render returned early so cmd.Wait
	// doesn't deadlock on a broken pipe.
	_, _ = io.Copy(io.Discard, stdout)
	waitErr := cmd.Wait()
	if waitErr != nil {
		return fmt.Errorf("go test failed (tests-failed=%d, build-errors=%d): %w",
			summary.TestsFailed, summary.BuildErrors, waitErr)
	}
	if renderErr != nil {
		return fmt.Errorf("gotestout render: %w", renderErr)
	}
	return nil
}

// Vet runs go vet across the module.
func Vet() error {
	return run("go", "vet", "./...")
}

// VetPkg runs go vet over ONE package path. Builder + build-QA surface.
func VetPkg(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("vetPkg: package path required (e.g. mage vetPkg ./internal/ops)")
	}
	return run("go", "vet", pkg)
}

// Format formats sources in place via gofumpt (latest). Auto-installs
// gofumpt to GOBIN if missing so contributors don't have to manage it
// out of band. gofumpt is a strict superset of gofmt -s: every gofmt -s
// fix plus tighter standards (no else-if-then-else without separation,
// no `,` before `... )`, etc.). The memory rule + CONTRIBUTING.md
// formalize this — never invoke gofmt or raw gofumpt directly; always
// route through `mage format` / `mage formatCheck`.
//
// Note: renamed from `Fmt` to `Format` per the canonical 12-target shape
// (2026-05-29). Hyphenated alias `fmt` preserved in the Aliases map.
func Format() error {
	if err := ensureGofumpt(); err != nil {
		return err
	}
	return run("gofumpt", "-w", ".")
}

// FormatFile rewrites ONE file (or directory) with gofumpt. Builder +
// build-QA surface — formats only the file(s) just edited.
func FormatFile(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("formatFile: path required (e.g. mage formatFile internal/ops/foo.go)")
	}
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("formatFile: %w", err)
	}
	if err := ensureGofumpt(); err != nil {
		return err
	}
	return run("gofumpt", "-w", path)
}

// FormatCheck fails if any file is not gofumpt-clean. Listing produced by
// `gofumpt -l`; non-empty = drift; emits the offending paths to stderr.
//
// Note: renamed from `FmtCheck` to `FormatCheck` per the canonical
// 12-target shape (2026-05-29). Hyphenated alias `fmt-check` preserved.
func FormatCheck() error {
	if err := ensureGofumpt(); err != nil {
		return err
	}
	out, err := exec.Command("gofumpt", "-l", ".").Output()
	if err != nil {
		return err
	}
	if len(strings.TrimSpace(string(out))) > 0 {
		fmt.Fprint(os.Stderr, string(out))
		return fmt.Errorf("files are not gofumpt-clean (run `mage format`)")
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

// CI is the composite Go gate: FormatCheck, Vet, Cover (race+cover combined),
// Tidy. The a11y suite is a separate gate (TemplatesA11y) run by the
// dedicated CI job .github/workflows/ci.yml::a11y. Keeping them separate
// means the `ci` CI job stays Go-only (no Node/pnpm setup) and the `a11y`
// job is the single enforcement point for accessibility. Local devs run
// both targets independently: `mage ci` for Go, `mage templatesA11y` for
// a11y (warn-skipped without a11y/node_modules).
//
// Note: renamed from `Check` to `CI` per the canonical 12-target shape
// (2026-05-29). Hyphenated alias `check` preserved.
func CI() error {
	for _, step := range []func() error{FormatCheck, Vet, Cover, Tidy} {
		if err := step(); err != nil {
			return err
		}
	}
	return nil
}

// Serve builds ta and runs `ta serve` with its documented defaults
// (--bind=127.0.0.1 --port=4321 per cmd/ta/serve_cmd.go). Thin wrapper
// for local-dev iteration on the HTTP cascade browser; production
// invocation goes through the installed `ta serve` binary instead.
func Serve() error {
	if err := Build(); err != nil {
		return err
	}
	return run(binDir+"/ta", "serve")
}

// TemplatesA11y runs the Playwright + axe-core a11y suite under a11y/
// against the live `ta serve` HTTP cascade browser. Environment-split
// gate semantics:
//
//   - CI (CI=true): always runs. Missing pnpm, missing deps, or any axe
//     violation fails CI. CI workflow installs the toolchain before
//     this gate fires.
//   - Local: warn-skip when a11y/node_modules is absent so a Go-only
//     dev doesn't get blocked by every mage ci. Install with
//     `cd a11y && pnpm install && pnpm exec playwright install chromium`
//     to opt in locally.
//
// Per drop_009 L2-A D4: the a11y gate is a HARD requirement in CI
// (.github/workflows/ci.yml job `a11y`). Local skipping is intentional
// dev-friction relief — CI is the enforcement point.
func TemplatesA11y() error {
	inCI := os.Getenv("CI") == "true"
	if !inCI {
		if _, err := os.Stat("a11y/node_modules"); err != nil {
			fmt.Fprintln(os.Stderr,
				"WARN: a11y/node_modules absent; skipping TemplatesA11y locally. "+
					"CI enforces a11y on every PR. Install with "+
					"`cd a11y && pnpm install && pnpm exec playwright install chromium` "+
					"to enable locally.")
			return nil
		}
	}
	pnpm, err := exec.LookPath("pnpm")
	if err != nil {
		return fmt.Errorf("templatesA11y: pnpm not on PATH: %w", err)
	}
	if err := Build(); err != nil {
		return fmt.Errorf("templatesA11y: build ta binary: %w", err)
	}
	cmd := exec.Command(pnpm, "test")
	cmd.Dir = "a11y"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("templatesA11y: pnpm test in a11y/: %w", err)
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

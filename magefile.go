//go:build mage

// Mage build automation for ta.
//
// Run "mage -l" to list targets. The top-level gate is "mage check" which
// runs fmtcheck, vet, test, and tidy.
//
// Test output is rendered through laslig/gotestout, which auto-detects
// the writer's TTY status: humans get a styled summary, agents (and any
// other non-TTY consumer such as a CI pipe) get plain text. No env-var
// gymnastics. `go test -json` runs unconditionally; gotestout handles
// human-vs-plain selection per laslig's FormatAuto policy.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanmschultz/laslig"
	"github.com/evanmschultz/laslig/gotestout"

	"github.com/evanmschultz/ta/internal/install_hygiene"
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

// Test runs the full test suite with the race detector. Output is
// rendered through laslig/gotestout — TTY callers get a styled
// summary, non-TTY consumers (agents, CI pipes) get plain text.
func Test() error {
	return runGoTest("./...")
}

// TestFunc runs ONLY the test functions matching the given regex
// pattern (`go test -run <pattern>`) within the given package path
// (default `./...` — pass TA_TEST_PKG=./internal/ops to scope tighter).
// Cascade discipline: a builder or QA agent operating below strict
// package level invokes `mage testFunc TestMyThing` (or
// `mage testFunc 'TestA|TestB'` for multiple) so its verdict is
// unmuddied by sibling agents' WIP. Higher levels run `mage Test` to
// verify the integrated whole.
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
	return runGoTest(pkg, "-run", pattern)
}

// TestPkg runs every test in ONE package path. Cascade discipline:
// package-level QA invokes `mage testPkg ./internal/ops` so the
// verdict reflects exactly what their slice owns.
func TestPkg(pkg string) error {
	if pkg == "" {
		return fmt.Errorf("testPkg: package path required (e.g. mage testPkg ./internal/ops)")
	}
	return runGoTest(pkg)
}

// Cover produces a function-level coverage report. The test-runner
// step routes through laslig/gotestout (TTY-aware, like Test); the
// coverage-tool step emits plain text either way (it's a digest, not
// a parse target).
func Cover() error {
	if err := runGoTest("./...", "-coverprofile=coverage.out"); err != nil {
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
// instead of silently passing. `vhs` is not gated by mage check;
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

// runGoTest invokes `go test -json -race -count=1 [extraArgs]
// [$TA_GO_TEST_FLAGS] <pkg>` and pipes the event stream through
// laslig/gotestout for TTY-aware rendering. gotestout's FormatAuto
// policy emits a styled summary on terminals and plain text
// otherwise; agents and CI pipes get the raw plain output without
// env-var gymnastics. A non-zero `go test` exit translates to a
// wrapped error including the failed-test count. The
// TA_GO_TEST_FLAGS envvar is whitespace-tokenized via strings.Fields
// and each non-empty token appended after the `-run <pattern>` hand-
// off so callers can flip `-update`, `-race=false`, `-timeout=30s`,
// or any combination without forcing a magefile edit. Empty / unset
// is a no-op.
func runGoTest(pkg string, extraArgs ...string) error {
	args := append([]string{"test", "-json", "-race", "-count=1"}, extraArgs...)
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

// Check is the composite gate: fmtcheck, vet, test, tidy, templates-embed.
// The TemplatesBuildEmbed step is skip-on-pnpm-absent: if pnpm is not on
// PATH, Check emits a verbose WARN line and proceeds without Track B
// build verification. Any other failure from TemplatesBuildEmbed (e.g.
// pnpm present but `pnpm run build` non-zero) bubbles up and fails Check.
func Check() error {
	for _, step := range []func() error{FmtCheck, Vet, Test, Tidy} {
		if err := step(); err != nil {
			return err
		}
	}
	if err := TemplatesBuildEmbed(); err != nil {
		if errors.Is(err, errPnpmAbsent) {
			fmt.Fprintln(os.Stderr,
				"WARN: pnpm not on PATH; skipping TemplatesBuildEmbed for Track B; "+
					"mage Check passes WITHOUT Track B build verification")
			return nil
		}
		return err
	}
	return nil
}

// errPnpmAbsent is the sentinel returned by ensurePnpm when the `pnpm`
// binary is not resolvable on PATH. Check uses errors.Is against this
// sentinel to distinguish "skip Track B silently" from "real build
// failure that must fail the gate".
var errPnpmAbsent = errors.New("pnpm not on PATH")

// ensurePnpm resolves the `pnpm` binary on PATH. Parallel in shape to
// ensureGofumpt — but pnpm CANNOT be auto-installed reliably across
// platforms (npm, corepack, brew, asdf all reasonable; no canonical
// `go install`-equivalent), so this helper only locates an existing
// pnpm and returns errPnpmAbsent when missing. Direct callers
// (TemplatesBuildEmbed) propagate the error; Check downgrades it to a
// WARN-and-skip via errors.Is.
func ensurePnpm() (string, error) {
	path, err := exec.LookPath("pnpm")
	if err != nil {
		return "", errPnpmAbsent
	}
	return path, nil
}

// TemplatesBuildEmbed builds the Astro project under web/templates_embed/
// that feeds internal/templates_html_embed/dist/ for go:embed (Track B).
// Returns errPnpmAbsent (wrapped) if pnpm is not on PATH so callers can
// distinguish "skip" from "build failed". Any other non-zero pnpm exit
// bubbles up as a wrapped error — a present-pnpm build failure is
// always a hard failure (CONTRIBUTING.md documents that devs with pnpm
// must run `pnpm install -C web/templates_embed` once before `mage
// check`).
func TemplatesBuildEmbed() error {
	pnpm, err := ensurePnpm()
	if err != nil {
		return fmt.Errorf("templates build embed: %w", err)
	}
	cmd := exec.Command(pnpm, "run", "build")
	cmd.Dir = "web/templates_embed"
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("templates build embed: pnpm run build: %w", err)
	}
	// Restore the dist/.keep placeholder that Astro's build step wipes when
	// clearing dist/ before writing output. Without this restore, the embed
	// package's TestEmbeddedEmbed_DistKeepPresent fails after any TemplatesBuildEmbed
	// run, and a fresh clone (with no prior build) can lose the placeholder
	// the moment a dev runs the build target. The //go:embed all:dist
	// directive requires at least one file at the embed root at compile time;
	// the .keep sentinel guarantees that round-trip across dev workflows.
	const keep = "internal/templates_html_embed/dist/.keep"
	keepBody := []byte("# placeholder; do not delete. //go:embed all:dist requires this file at fresh-clone state.\n")
	if err := os.WriteFile(keep, keepBody, 0o644); err != nil {
		return fmt.Errorf("templates build embed: restore %s: %w", keep, err)
	}
	return nil
}

// TemplatesBuildVerifyStable rebuilds the Astro dist tree twice and
// compares the sha256 of the dist directory between runs. If the two
// hashes diverge on the first try (Astro flake, timestamp drift, etc.),
// the target rebuilds ONCE MORE and compares the second run's hash to
// the third. If THAT compare also fails, the target returns an error
// summarizing the divergent hashes — three builds total max. This is
// the "retry-once-then-fail" flake policy: tolerate a single
// non-deterministic build, but persistent drift is a real bug in the
// Astro pipeline and fails loud.
func TemplatesBuildVerifyStable() error {
	const distDir = "internal/templates_html_embed/dist"
	if err := TemplatesBuildEmbed(); err != nil {
		return err
	}
	hashA, err := hashDistTree(distDir)
	if err != nil {
		return fmt.Errorf("templates verify stable: hash first build: %w", err)
	}
	if err := TemplatesBuildEmbed(); err != nil {
		return err
	}
	hashB, err := hashDistTree(distDir)
	if err != nil {
		return fmt.Errorf("templates verify stable: hash second build: %w", err)
	}
	if hashA == hashB {
		return nil
	}
	// First pair diverged; retry once.
	if err := TemplatesBuildEmbed(); err != nil {
		return err
	}
	hashC, err := hashDistTree(distDir)
	if err != nil {
		return fmt.Errorf("templates verify stable: hash retry build: %w", err)
	}
	if hashB == hashC {
		return nil
	}
	return fmt.Errorf(
		"templates verify stable: dist tree non-deterministic across three builds "+
			"(hashA=%s hashB=%s hashC=%s)",
		hashA, hashB, hashC,
	)
}

// hashDistTree walks dir and returns a hex sha256 over the sorted list
// of (relative path, file content sha256) tuples. Sorting the entries
// before hashing makes the result independent of filesystem walk order.
// Used by TemplatesBuildVerifyStable to detect non-deterministic Astro
// builds.
func hashDistTree(dir string) (string, error) {
	type entry struct {
		path string
		sum  string
	}
	var entries []entry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		sum := sha256.Sum256(data)
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			return relErr
		}
		entries = append(entries, entry{path: rel, sum: hex.EncodeToString(sum[:])})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("walk %q: %w", dir, err)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	roll := sha256.New()
	for _, e := range entries {
		fmt.Fprintf(roll, "%s\x00%s\n", e.path, e.sum)
	}
	return hex.EncodeToString(roll.Sum(nil)), nil
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

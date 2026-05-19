// Package install_hygiene holds the schema-seeding and Facts-row logic
// previously inlined in magefile.go::Install. Lifting it out of the
// magefile (which carries the `//go:build mage` constraint and is
// therefore invisible to `go test`) into a regular Go package lets us
// gate the behavior under `mage testPkg ./internal/install_hygiene`.
//
// Scope per drop_004 L3-G2-D1 (F1+F2+F3):
//
//   - F1: validate a pre-existing `$HOME/.ta/schema.toml` against the
//     meta-schema via internal/schema.LoadBytes BEFORE declaring it
//     "untouched". On parse failure, emit a WARN notice and leave the
//     file in place — never silently overwrite a broken user schema.
//   - F2: the Facts-row outcome value (and matching INFO/WARN notice
//     titles) speak human language. `untouched` is replaced with
//     "schema preserved (your edits are safe)" etc.
//   - F3: when home is missing the seeder keeps writing an empty
//     placeholder `schema.toml` (option (a) from the walkthrough). The
//     choice + rationale is appended to Options.DecisionLog so the
//     decision is auditable from the install run.
//
// magefile.go::Install becomes a thin wrapper: it constructs Options,
// supplies a BuildFunc that shells `go build` (mage's domain stays in
// mage), and returns Run's error. All schema-aware logic lives here.
package install_hygiene

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/render"
	"github.com/evanmschultz/ta/internal/schema"
)

// F3 decision marker. Recorded in Options.DecisionLog / Report.DecisionLog
// so the install run can be audited later. Per L3-G2-D1 the default is
// option (a): keep the empty placeholder so `ta init`'s empty-home guard
// has a stable anchor and `mage install` produces visible artifacts on
// first run.
const F3DecisionLog = "F3: kept empty placeholder default — populate via examples/, " +
	"ta schema --action=create, or ta template save"

// Raw outcome labels are the internal state of the seeder. The Facts row
// and notices use the human-language form (see humanLabel below) per F2.
const (
	OutcomeUntouchedValid = "untouched-valid"
	OutcomeCreated        = "created"
	OutcomeInvalid        = "invalid"
	OutcomePlaceholder    = "placeholder"
)

// humanLabel maps a raw outcome label to user-facing copy per F2
// (E2E_FIXES.md:29-33 + the new F1 "invalid" branch).
func humanLabel(raw string) string {
	switch raw {
	case OutcomeUntouchedValid:
		return "schema preserved (your edits are safe)"
	case OutcomeCreated:
		return "placeholder created (empty — populate it next)"
	case OutcomePlaceholder:
		return "placeholder reset (D2 cold-home guard)"
	case OutcomeInvalid:
		return "schema invalid (left in place — see warning above)"
	default:
		return raw
	}
}

// Options configures a Run invocation. The zero value is not usable;
// Home must be set. Renderer is optional — if nil, Run constructs one
// against Errf (or os.Stderr if Errf is also nil).
type Options struct {
	// Home is the absolute path treated as $HOME. Required.
	Home string

	// InstallDir is the directory the binary is written to. Defaults to
	// filepath.Join(Home, ".local", "bin") when empty.
	InstallDir string

	// BinaryName is the file name written under InstallDir. Defaults to
	// "ta" when empty.
	BinaryName string

	// BuildFunc is called to build the binary into `dst`. When nil the
	// build step is skipped — tests use this seam to exercise the
	// seeding logic without shelling out to `go build`.
	BuildFunc func(dst string) error

	// DryRun, when true, suppresses every filesystem mutation. The
	// helpers still compute their would-be paths and outcomes so callers
	// can preview a run.
	DryRun bool

	// Errf is the writer the renderer writes to when Renderer is nil.
	// Defaults to os.Stderr.
	Errf io.Writer

	// Renderer is the optional pre-constructed laslig renderer. When
	// nil, Run constructs one against Errf.
	Renderer *render.Renderer

	// DecisionLog, when non-nil, is appended to as Run records audit
	// entries (e.g. the F3 placeholder default). Report.DecisionLog
	// always carries the same entries regardless of this pointer.
	DecisionLog *[]string
}

// Report captures the outcome of one Run invocation.
type Report struct {
	// BinaryPath is the absolute path the binary was (or would be)
	// written to. Always populated.
	BinaryPath string

	// SchemaPath is the absolute path of $HOME/.ta/schema.toml.
	SchemaPath string

	// OutcomeRaw is the internal outcome label (one of OutcomeCreated,
	// OutcomeUntouchedValid, OutcomeInvalid, OutcomePlaceholder).
	OutcomeRaw string

	// OutcomeHuman is the F2 human-language form of OutcomeRaw — this
	// is what callers should display.
	OutcomeHuman string

	// DecisionLog captures audit entries recorded during the run (e.g.
	// the F3 placeholder default choice).
	DecisionLog []string

	// Notices carries user-facing hints surfaced by Run after the
	// install completes. Today (F8) the only entry is the running-MCP
	// hint emitted when an existing `ta` process is detected on the
	// host; future hints (e.g. PATH-membership warnings) will land
	// here too. The slice is ordered: index 0 is the first hint
	// pushed.
	Notices []string
}

// Run executes the install hygiene pipeline:
//
//  1. Resolve install dir + binary path.
//  2. Optionally build the binary via opts.BuildFunc.
//  3. Seed $HOME/.ta/schema.toml per F1 (validate) + F2 (human labels)
//     + F3 (keep placeholder default).
//  4. Emit the SUCCESS notice + Facts row through the renderer.
//
// The context is accepted for API symmetry with the rest of the ta
// codebase; today the helper does no blocking I/O of its own beyond
// what BuildFunc may do.
func Run(ctx context.Context, opts Options) (Report, error) {
	if opts.Home == "" {
		return Report{}, fmt.Errorf("install_hygiene: Options.Home is required")
	}

	rr := opts.Renderer
	if rr == nil {
		w := opts.Errf
		if w == nil {
			w = os.Stderr
		}
		rr = render.New(w)
	}

	installDir := opts.InstallDir
	if installDir == "" {
		installDir = filepath.Join(opts.Home, ".local", "bin")
	}
	binaryName := opts.BinaryName
	if binaryName == "" {
		binaryName = "ta"
	}
	binaryPath := filepath.Join(installDir, binaryName)

	if !opts.DryRun {
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			return Report{BinaryPath: binaryPath}, Error(rr, fmt.Sprintf("create install dir %q", installDir), err)
		}
	}

	if !opts.DryRun && opts.BuildFunc != nil {
		if err := opts.BuildFunc(binaryPath); err != nil {
			return Report{BinaryPath: binaryPath}, Error(rr, "build ta", err)
		}
	}

	seedRep, err := SeedHomeSchema(ctx, opts)
	if err != nil {
		return seedRep, err
	}
	seedRep.BinaryPath = binaryPath

	// F8 — detect a stale running ta MCP server and surface the
	// restart hint. Detection is best-effort: any error from pgrep
	// (other than "absent" / "no match") is swallowed because failing
	// the install over a missing detection helper would be worse than
	// the rare case where the hint is silently skipped.
	if detected, derr := detectRunningTaMCP(ctx); derr == nil && detected {
		seedRep.Notices = append([]string{F8RunningMCPNotice}, seedRep.Notices...)
		if err := rr.Notice(
			laslig.NoticeInfoLevel,
			"running MCP detected",
			"An existing ta MCP server is still loaded against the previous binary; restart Claude Code or Codex to pick up the new install.",
			[]string{
				"Claude Code: quit + relaunch from the project checkout.",
				"Codex: end the session and start a new one.",
			},
		); err != nil {
			return seedRep, err
		}
	}

	if err := rr.Success("install complete", "ta built from working tree", nil); err != nil {
		return seedRep, err
	}
	if err := rr.Facts([]laslig.Field{
		{Label: "binary", Value: binaryPath},
		{Label: "schema", Value: seedRep.SchemaPath},
		{Label: "outcome", Value: seedRep.OutcomeHuman},
	}); err != nil {
		return seedRep, err
	}
	return seedRep, nil
}

// SeedHomeSchema creates $HOME/.ta/ if missing and seeds
// $HOME/.ta/schema.toml per F1 / F2 / F3.
//
// Branches:
//
//   - schema.toml present + parses cleanly via schema.LoadBytes →
//     OutcomeUntouchedValid, no notice (silent).
//   - schema.toml present + LoadBytes returns an error → OutcomeInvalid,
//     WARN notice naming the file + the parse error + the three repair
//     paths. The file is NOT overwritten.
//   - schema.toml missing → write empty placeholder, OutcomeCreated,
//     INFO notice + F3DecisionLog entry appended.
//
// SeedHomeSchema is exported so callers (tests, future tools) can
// exercise the seeder without invoking the binary-build phase of Run.
func SeedHomeSchema(_ context.Context, opts Options) (Report, error) {
	if opts.Home == "" {
		return Report{}, fmt.Errorf("install_hygiene: SeedHomeSchema requires Options.Home")
	}

	rr := opts.Renderer
	if rr == nil {
		w := opts.Errf
		if w == nil {
			w = os.Stderr
		}
		rr = render.New(w)
	}

	taDir := filepath.Join(opts.Home, ".ta")
	dst := filepath.Join(taDir, "schema.toml")

	rep := Report{SchemaPath: dst}
	appendDecision := func(entry string) {
		rep.DecisionLog = append(rep.DecisionLog, entry)
		if opts.DecisionLog != nil {
			*opts.DecisionLog = append(*opts.DecisionLog, entry)
		}
	}

	if !opts.DryRun {
		if err := os.MkdirAll(taDir, 0o755); err != nil {
			return rep, Error(rr, fmt.Sprintf("create %q", taDir), err)
		}
	}

	info, statErr := os.Stat(dst)
	switch {
	case statErr == nil && !info.IsDir():
		// F1 — validate before declaring untouched.
		buf, readErr := os.ReadFile(dst)
		if readErr != nil {
			return rep, Error(rr, fmt.Sprintf("read %q", dst), readErr)
		}
		if _, parseErr := schema.LoadBytes(buf); parseErr != nil {
			rep.OutcomeRaw = OutcomeInvalid
			rep.OutcomeHuman = humanLabel(OutcomeInvalid)
			if nerr := rr.Notice(
				laslig.NoticeWarningLevel,
				"schema invalid",
				fmt.Sprintf("%s did not parse against the meta-schema: %s", dst, parseErr.Error()),
				[]string{
					"Hand-edit the file to fix the error reported above.",
					"Replace from examples/ in the ta repo (e.g. cp examples/schema.toml ~/.ta/schema.toml).",
					"Rebuild via CLI: ta schema --action=create --kind=db --name=<name> --data='{...}'",
				},
			); nerr != nil {
				return rep, nerr
			}
			return rep, nil
		}
		rep.OutcomeRaw = OutcomeUntouchedValid
		rep.OutcomeHuman = humanLabel(OutcomeUntouchedValid)
		return rep, nil

	case statErr == nil && info.IsDir():
		return rep, Error(rr, fmt.Sprintf("stat %q", dst), fmt.Errorf("path exists but is a directory"))

	case errors.Is(statErr, fs.ErrNotExist):
		// F3 — keep the empty-placeholder default (option (a)).
		appendDecision(F3DecisionLog)
		if !opts.DryRun {
			if err := os.WriteFile(dst, nil, 0o644); err != nil {
				return rep, Error(rr, fmt.Sprintf("write %q", dst), err)
			}
		}
		rep.OutcomeRaw = OutcomeCreated
		rep.OutcomeHuman = humanLabel(OutcomeCreated)
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
			return rep, nerr
		}
		return rep, nil

	default:
		return rep, Error(rr, fmt.Sprintf("stat %q", dst), statErr)
	}
}

// Error renders a laslig error notice for a user-facing Install failure
// and returns a wrapped Go error so callers (mage targets, future CLI
// front ends) still report a non-zero exit. The wrapped error keeps the
// stage label + original cause for post-mortem grep; the notice is the
// pretty surface.
func Error(rr *render.Renderer, stage string, cause error) error {
	_ = rr.Notice(
		laslig.NoticeErrorLevel,
		"install failed",
		fmt.Sprintf("%s: %s", stage, cause.Error()),
		nil,
	)
	return fmt.Errorf("%s: %w", stage, cause)
}

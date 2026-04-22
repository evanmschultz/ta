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

	"github.com/evanmschultz/ta/internal/mcpsrv"
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

// Dogfood materializes the ta-v2 drop's build+QA lineage into
// workflow/ta-v2/db.toml by routing through mcpsrv.Create — the same
// code path the MCP tool uses. Per V2-PLAN §2.7 ("we eat the output")
// and §12.10 (dogfood migration). Idempotent: if the db file already
// exists we assume the migration has run and skip to avoid collision
// errors on the ErrRecordExists guard.
//
// Post-V2-PLAN §12.11 the runtime reads only <project>/.ta/schema.toml
// with no home-layer fallback, so the HOME-staging workaround this
// target used to carry is gone — we invoke mcpsrv.Create directly on
// the project root.
func Dogfood() error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("resolve cwd: %w", err)
	}
	dbFile := filepath.Join(root, "workflow", "ta-v2", "db.toml")
	if _, err := os.Stat(dbFile); err == nil {
		fmt.Printf("ta: %s already exists; dogfood migration already materialized. Skipping.\n", dbFile)
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", dbFile, err)
	}

	records := dogfoodRecords()
	for _, rec := range records {
		if _, _, err := mcpsrv.Create(root, rec.Section, "", rec.Data); err != nil {
			return fmt.Errorf("create %s: %w", rec.Section, err)
		}
	}
	fmt.Printf("ta: wrote %d records to %s\n", len(records), dbFile)
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

// dogfoodRecord is one row to materialize under plan_db.ta-v2.
// Section is the full dotted address the MCP tool expects; Data is
// the field map validated against .ta/schema.toml's plan_db types.
type dogfoodRecord struct {
	Section string
	Data    map[string]any
}

// dogfoodRecords returns every record the ta-v2 drop needs to
// materialize per V2-PLAN §12.10: 8 completed build_tasks + 16 QA
// twins + 2 in-flight build_tasks = 26 rows. Bodies are 2–4 sentence
// structured summaries citing commit SHAs + design decisions; the
// verbose narrative stays in workflow/ta-v2/WORKLOG.md until §12.11
// README collapse. Ordering matches the chronological drop arc so
// ta get + ta search returns records in a sensible default sequence.
func dogfoodRecords() []dogfoodRecord {
	const owner = "evanmschultz"
	// Build tasks keyed by drop step. Each entry carries the step's
	// commit-SHA anchor and a compact structured summary.
	builds := []struct {
		id, status, title, body string
	}{
		{
			id:     "task_12_1",
			status: "done",
			title:  "Backend interface extraction",
			body: "Defined `internal/record/Backend` and moved `internal/tomlfile/` behind it as `internal/backend/toml/` per V2-PLAN §5.1 / §12.1. " +
				"Commit `1e636d9` (followed by the Option A schema-rename chain `e689007`..`14b22d2`) preserved byte-identity of the moved files modulo the `package tomlfile` → `package toml` header and kept all MVP tests green with `-race`. " +
				"Interface-only `internal/record` package has no tests at this slice; downstream consumption begins in §12.2.",
		},
		{
			id:     "task_12_2",
			status: "done",
			title:  "Schema language update",
			body: "Reshaped `.ta/schema.toml` from `[schema.<type>]` to db-scoped `[<db>.<type>]` and added the meta-schema validator per V2-PLAN §4.1 / §4.7 / §12.2. " +
				"Commit `ca0b63e` landed the loader rewrite (shape exclusivity, format/extension check, MD heading rules, path uniqueness), the embedded meta-schema (`internal/schema/meta_schema.toml` + `//go:embed`), and the `ta_schema` scope short-circuit in `handleSchema`. " +
				"Option A follow-up `95f1d48` closed the Lookup→LookupDB fallback regression that silently accepted dotted type typos.",
		},
		{
			id:     "task_12_3",
			status: "done",
			title:  "Address resolution package",
			body: "Shipped `internal/db/` with uniform `<db>.<type>.<id-path>` / `<db>.<instance>.<type>.<id-path>` address parsing, dir-per-instance + collection scans, prefix-glob, and `filepath.IsLocal` guard on `path_hint` per V2-PLAN §2.9 / §3.4 / §11.D. " +
				"Combined with §12.4 per dev directive; final state at `693ff63` after three reworks (`7b8cb70` → `4dfd480` → `7d2f99d` → `693ff63`) and three spec companions (`8ba89b8`, `dea7bca`, `bd10688`). " +
				"Strict orphan semantics landed via Option B: legacy orphans readable, new orphan-level writes require materializing the missing declared ancestor first.",
		},
		{
			id:     "task_12_4",
			status: "done",
			title:  "MD backend",
			body: "Built `internal/backend/md/` as a schema-driven ATX scanner with hierarchical ancestor-chain addressing, same-or-shallower byte-range rule, nested `Splice` with `ErrParentMissing`, and malformed-address guard symmetric across `Emit` and `Splice` per V2-PLAN §5.3 / §2.10 / §2.11. " +
				"Combined with §12.3 per dev directive; final state at `693ff63`. " +
				"Coverage at `mage cover` time: 91.1% — above the ≥85% backend target from §10.4.",
		},
		{
			id:     "task_12_5",
			status: "done",
			title:  "Data tool surface",
			body: "Hard-cut `upsert` and added `get(fields)`, `create(section, data, path_hint)`, `update(section, data)`, `delete(section)` on both MCP and CLI surfaces per V2-PLAN §3.1 / §3.4 / §3.5 / §3.6 / §12.5. " +
				"Combined with §12.6; final state at `aa7f1a6` after `5f607ab` (combined build) and `e99ff94` (spec amendment adding `fields` to `kind=type` payloads). " +
				"Option A follow-up in `aa7f1a6` closed three fail-loudly gaps: MD non-body field rejection, dir-per-instance rollback on write failure, and record-level delete file-not-found wrapping.",
		},
		{
			id:     "task_12_6",
			status: "done",
			title:  "Schema tool CRUD",
			body: "Extended the `schema` tool with `action={get, create, update, delete}` + atomic-rollback via `schema.LoadBytes` pre-write gate per V2-PLAN §3.3 / §4.5 / §4.7 / §12.6. " +
				"Combined with §12.5 in commits `5f607ab` / `e99ff94` / `aa7f1a6`. " +
				"Reserved-name guard on `ta_schema` closed the §12.2 Proof-routed unknown; mutations always target the project `.ta/schema.toml` write layer, never home.",
		},
		{
			id:     "task_12_7",
			status: "done",
			title:  "Laslig CLI rendering",
			body: "Added `internal/render/` consolidating every CLI surface behind a single `Renderer` (`Notice` / `Success` / `Error` / `List` / `Markdown` / `Record`); moved `humanPolicy` from `cmd/ta/main.go` to `HumanPolicy` in the render package per V2-PLAN §13 / §12.7. " +
				"Combined with §12.8 in commits `a482cd0` / `85fe917`. " +
				"§13.3 MCP firewall enforced by dependency direction: `internal/mcpsrv/` imports no `internal/render`.",
		},
		{
			id:     "task_12_8",
			status: "done",
			title:  "Search",
			body: "Shipped `internal/search/` with `Query{Path, Scope, Match, Query, Field}` + `Result{Section, Bytes, Fields}`, regex via `regexp`, AND-ordering (Match first, Query second), cross-instance union per V2-PLAN §3.7 / §7 / §12.8. " +
				"Combined with §12.7 in `a482cd0` / `85fe917`. " +
				"Option A follow-up `85fe917` added shared `internal/backend/md/layout.go` `CheckBackableFields` helper so `mcpsrv/fields.go` and `internal/search/search.go` share one MD body-only rejection contract, plus `--verbose` on mutating CLI commands and unconstrained-scope unknown-field tightening.",
		},
		{
			id:     "task_12_9",
			status: "doing",
			title:  "MCP caching",
			body: "In-progress: in-memory schema-cascade cache at `internal/mcpsrv/cache.go` keyed on project path with `os.Stat`-mtime + deletion invalidation; atomic swap on `MutateSchema` success; startup meta-validation via `Config.ProjectPath` pre-warm that refuses to boot on a malformed cascade. " +
				"Implements V2-PLAN §4.6 + §12.9. " +
				"Thread-safe RWMutex + double-checked locking; per-project entries are independent.",
		},
		{
			id:     "task_12_10",
			status: "doing",
			title:  "Dogfood migration",
			body: "In-progress: this record is itself materialized by the `mage dogfood` target introduced in this slice, which routes through `mcpsrv.Create` to eat the §2.7 dogfood principle end-to-end. " +
				"Writes `workflow/ta-v2/db.toml` carrying 8 done build_tasks + 16 QA twins + 2 in-flight build_tasks per V2-PLAN §12.10. " +
				"Idempotent on re-run via existence check; orchestrator retains write ownership of `workflow/ta-v2/WORKLOG.md` which stays in place until §12.11 README collapse.",
		},
	}

	// QA twin lineage. Every completed build_task gets one proof + one
	// falsification twin; §12.9 + §12.10 have no twins yet (orchestrator
	// adds them once QA completes). Final verdicts per WORKLOG.md:
	// every Falsification PASSED as of post-Option-resolution state
	// (§12.2 Option A; §12.4 Option B; §12.6 + §12.8 Option A).
	qaTwins := []struct {
		id, parent, kind, status, body string
	}{
		// §12.1 twins
		{
			id: "qa_12_1_proof", parent: "task_12_1", kind: "proof", status: "passed",
			body: "PASS (fresh-context re-run after Option A schema-rename resolution) over commits `1e636d9`..`14b22d2`. " +
				"V2-PLAN §5.1 interface shape matched exactly; 9 moved files byte-identical modulo package header; `mage check` green with `-race`; zero remaining scope creep after the follow-up chain. " +
				"Retrospective: first Proof pass missed the `doc.go` scope-creep leak that Falsification caught; re-run post-fix confirmed clean closure.",
		},
		{
			id: "qa_12_1_falsification", parent: "task_12_1", kind: "falsification", status: "passed",
			body: "PASS (post-Option-A). Original pass at `1e636d9` FAILED on one confirmed counterexample: out-of-scope, code-contradicting prose edit in `internal/config/doc.go` that referenced schema-rename variables the code itself hadn't renamed yet. " +
				"Dev chose Option A: land the matching `config.go` exports rename (commit `e689007`) plus supporting follow-ups (`b436017`, `ee9efa8`, `1575041`, `14b22d2`). " +
				"Final state clean; `mage check` green; other 11 attack vectors all REFUTED.",
		},
		// §12.2 twins
		{
			id: "qa_12_2_proof", parent: "task_12_2", kind: "proof", status: "passed",
			body: "PASS against `ca0b63e` diff + HEAD tree. " +
				"Grammar migration complete (zero `[schema.` survivors in live code), all shape/format/heading/type/field rules tested with negative cases, meta-schema self-describing via `TestMetaSchemaLoadsUnderNewGrammar`, `ta_schema` scope short-circuit bypasses `config.Resolve`. " +
				"Three non-blocking unknowns routed: reserved-name collision on user `ta_schema` (routed to §12.6), legacy `[schema.<type>]` home schemas will fail pre-stable (release-notes item for §12.12), cascade wholesale-replace under-tested (route to §12.6).",
		},
		{
			id: "qa_12_2_falsification", parent: "task_12_2", kind: "falsification", status: "passed",
			body: "PASS (post-Option-A). Original pass at `ca0b63e` FAILED on one CONFIRMED: Lookup→LookupDB fallback in `cmd/ta/commands.go:107-117` and `internal/mcpsrv/tools.go:238-260` silently accepted dotted type typos (`plans.ghost` → rendered whole `plans` db), violating §1.1 / §3 \"path typos fail loudly\". " +
				"Option A follow-up `95f1d48` added `!strings.Contains(section, \".\")` guard + two negative tests. " +
				"15 other attacks all REFUTED; three routed unknowns (path traversal, APFS case-insensitivity, trailing-slash normalization) deferred to downstream slices.",
		},
		// §12.3+§12.4 twins — one pair per build_task
		{
			id: "qa_12_3_proof", parent: "task_12_3", kind: "proof", status: "passed",
			body: "PASS for the §12.3+§12.4 combined cycle against `7d2f99d` (pre-orphan-fix HEAD). " +
				"Uniform address grammar at `internal/db/address.go:79-102`, schema-driven sectioning in both backends, hierarchical body ranges, MD ancestor-chain addressing, `filepath.IsLocal` guard, interface freeze preserved from §12.1, all 11 required new tests present. " +
				"Coverage: `internal/backend/md` 91.1%, `internal/backend/toml` 86.6% — both above the ≥85% target.",
		},
		{
			id: "qa_12_3_falsification", parent: "task_12_3", kind: "falsification", status: "passed",
			body: "PASS (post-Option-B). Same review arc as §12.4 — one combined Falsification pass covered both tasks. " +
				"Original pass on `7d2f99d` FAILED on two defects (2.1 `parentAddress` contract mismatch on orphan chains; 2.2 `Splice` missing malformed-address guard). " +
				"Dev chose Option B strict-orphan semantics (spec commit `bd10688`, code commit `693ff63`); 22 other attacks REFUTED.",
		},
		{
			id: "qa_12_4_proof", parent: "task_12_4", kind: "proof", status: "passed",
			body: "PASS for the combined §12.3+§12.4 cycle — same evidence set as `qa_12_3_proof` because the Proof agent covered both tasks in one pass. " +
				"MD ATX scanner correctness (declared-heading-only sectioning, fenced-code state, splice invariant), per-parent-per-declared-level slug uniqueness, orphan-read-works invariant all verified with file:line + test citations.",
		},
		{
			id: "qa_12_4_falsification", parent: "task_12_4", kind: "falsification", status: "passed",
			body: "PASS (post-Option-B) — same two defects as `qa_12_3_falsification` since the §12.3+§12.4 cycle was reviewed as one combined pass. " +
				"The `parentAddress` contract mismatch and `Splice` guard gap were both local to `internal/backend/md/backend.go`; Option B resolved both with a docstring rewrite + one-line guard + three negative tests in commit `693ff63`.",
		},
		// §12.5+§12.6 twins
		{
			id: "qa_12_5_proof", parent: "task_12_5", kind: "proof", status: "passed",
			body: "PASS for §12.5+§12.6 combined cycle against `e99ff94`. " +
				"Every V2-PLAN §3.1 / §3.3 / §3.4 / §3.5 / §3.6 / §4.5 / §4.6 / §4.7 / §11.D clause reflected in committed code + tests with file:line + test citations. " +
				"Atomic rollback confirmed pre-write-gated via `schema.LoadBytes`; `filepath.IsLocal` preserved from §12.3; upsert hard-cut in both surfaces; `ta_schema` reserved-name guard closes §12.2 Proof unknown.",
		},
		{
			id: "qa_12_5_falsification", parent: "task_12_5", kind: "falsification", status: "passed",
			body: "PASS with three advisory findings (no blockers) against `e99ff94`, then fully resolved by Option A `aa7f1a6`. " +
				"2.1 (MD non-body silent drop in fields extractor, moderate), 2.2 (Create on dir-per-instance left orphan dir on WriteAtomic failure, low), 2.3 (record-level Delete missing ErrFileNotFound wrap, low). " +
				"All three closed with one-line fixes + three negative tests; 36 other attacks REFUTED.",
		},
		{
			id: "qa_12_6_proof", parent: "task_12_6", kind: "proof", status: "passed",
			body: "PASS for the combined §12.5+§12.6 cycle — same evidence set as `qa_12_5_proof`. " +
				"Atomic-rollback via `schema.LoadBytes` pre-write gate verified; mutations target project `.ta/schema.toml` not home; `ta_schema` reserved-name rejection end-to-end; post-mutation cascade re-resolve returns fresh sources.",
		},
		{
			id: "qa_12_6_falsification", parent: "task_12_6", kind: "falsification", status: "passed",
			body: "PASS with advisory findings shared across §12.5+§12.6 cycle — see `qa_12_5_falsification` for the three findings and their Option A resolution in `aa7f1a6`. " +
				"Schema-specific attacks (LoadBytes skip, reserved-name bypass, mutation-without-cascade-reresolve) all REFUTED.",
		},
		// §12.7+§12.8 twins
		{
			id: "qa_12_7_proof", parent: "task_12_7", kind: "proof", status: "passed",
			body: "PASS for §12.7+§12.8 combined cycle against `a482cd0`. " +
				"Every §3.7 / §7 / §13 / §12.7 / §12.8 contract verified with file:line + test citations. " +
				"§13.3 MCP firewall confirmed clean (`rg \"internal/render\" internal/mcpsrv/` returns zero); scope grammar (5 forms), Match-then-Query ordering, cross-instance union, hierarchical CLI routing, string-field glamour dispatch all backed by tests.",
		},
		{
			id: "qa_12_7_falsification", parent: "task_12_7", kind: "falsification", status: "passed",
			body: "PASS (post-Option-A) — original pass on `a482cd0` FAILED with one moderate blocker (#30 — MD non-body silent drop in `search.decodeFields`) + two observations (#17 verbose flag missing, #2/#12 unconstrained-scope unknown-field silent-skip). " +
				"Option A `85fe917` closed all three: shared `internal/backend/md/layout.go` + `CheckBackableFields` helper, `--verbose` on mutators, `validateScopeNames` at Run entry. " +
				"28 other attacks REFUTED.",
		},
		{
			id: "qa_12_8_proof", parent: "task_12_8", kind: "proof", status: "passed",
			body: "PASS for the combined §12.7+§12.8 cycle — same evidence set as `qa_12_7_proof`. " +
				"Search engine correctness (Match + Query semantics, scope grammar, cross-instance union), prefix-glob `*` / `-*` support, and Match-first / Query-second ordering all verified against `internal/search/search_test.go`.",
		},
		{
			id: "qa_12_8_falsification", parent: "task_12_8", kind: "falsification", status: "passed",
			body: "PASS (post-Option-A). Same findings as `qa_12_7_falsification` since the §12.7+§12.8 cycle was reviewed as one combined pass. " +
				"The MD non-body silent drop in `decodeFields` was a search-specific failure mode of the same class the data-tool surface had previously closed; `85fe917` unified both entry points behind one contract.",
		},
	}

	out := make([]dogfoodRecord, 0, len(builds)+len(qaTwins))
	for _, b := range builds {
		out = append(out, dogfoodRecord{
			Section: "plan_db.ta-v2.build_task." + b.id,
			Data: map[string]any{
				"id":     b.id,
				"status": b.status,
				"title":  b.title,
				"owner":  owner,
				"body":   b.body,
			},
		})
	}
	for _, q := range qaTwins {
		out = append(out, dogfoodRecord{
			Section: "plan_db.ta-v2.qa_task." + q.id,
			Data: map[string]any{
				"id":                q.id,
				"parent_build_task": q.parent,
				"kind":              q.kind,
				"status":            q.status,
				"body":              q.body,
			},
		})
	}
	return out
}

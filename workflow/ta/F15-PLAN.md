# F15 — `ta template save` merges into `~/.ta/schema.toml`

## Spec recap

Single-file home library. `~/.ta/schema.toml` is the only file `ta` reads or writes in `~/.ta/`. `ta template save [<name-csv>]` merges project dbs into the home file. `ta init` picker reads only `~/.ta/schema.toml`.

## 1. Slice shape — atomic single commit

F15 lands as ONE atomic commit. The `templates` package API and every caller (`save`, `apply`, `delete`, `list`, `show`, `init`) all flip together — splitting them produces a half-broken intermediate state where the picker walks files that `save` no longer creates. Memory rule "no half-finished implementations" applies. The commit replaces:

- `internal/templates/templates.go` — full rewrite around a single-file model.
- `internal/templates/templates_test.go` — full rewrite.
- `cmd/ta/template_cmd.go` — `save`/`list`/`show`/`delete` rewired; `apply` unchanged in spirit (still copies one db's body into a project schema, but sources the body from the merged home).
- `cmd/ta/template_cmd_test.go` — rewrite the affected test functions.
- `cmd/ta/init_cmd.go` — `chooseSchema` simplified: drop multi-template walk, drop `collectHomeDBs` cross-template merge, load one file, build picker from its dbs.
- `cmd/ta/init_cmd_test.go` — rewrite the picker tests.

## 2. Templates package API post-F15

Rename loudly so callers break at compile rather than silently get new semantics on old names.

```go
package templates

func Root() (string, error)                                     // unchanged: ~/.ta
func SchemaPath() (string, error)                               // NEW: ~/.ta/schema.toml
func SetRootForTest(dir string) (restore func())                // unchanged

// Read API — schema-shaped, not file-shaped.
func LoadHome() (Registry, []byte, error)                       // (registry, raw bytes, err); missing file → (zero, nil, nil)
func ListDBs() ([]string, error)                                // sorted db names from LoadHome
func ShowDB(name string) ([]byte, error)                        // raw TOML body for one db; ErrDBNotFound when absent

// Write API — db-set merges, never file writes.
func SaveDBs(projectSchemaBytes []byte, names []string, opts SaveOptions) (SaveResult, error)
func DeleteDB(name string) error                                // re-emits remaining dbs

type SaveOptions struct{ Overwrite bool }
type SaveResult struct {
    Written   []string  // db names whose body was written/replaced
    Skipped   []string  // db names skipped because target already has them and Overwrite=false
    Conflicts []string  // db names where home and project both declare it (subset reported up to caller for prompt)
}

var ErrDBNotFound = errors.New("templates: db not found in ~/.ta/schema.toml")
```

The old `List/Load/Save/Delete` names are dropped. Calling `templates.Save(root, "foo", bytes)` post-F15 is a compile error — exactly the loud break we want, because pre-F15 callers carry pre-F15 semantics.

`validateName` survives but becomes db-name validation (still rejects `..`, separators, dot-prefix, empty).

## 3. Conflict semantics

Db-level replace-or-skip. Type-level merging into one db is OUT OF SCOPE for F15: if home `[plans]` and project `[plans]` both exist, F15 treats them as opaque competing bodies. Confirmed by the spec's "same-name db conflicts on merge prompt confirm-or-skip" wording — db-granular, not type-granular.

Skip semantics: "leave home unchanged for THAT db." Other selected dbs in the same `save` invocation still apply. Aborting the whole save on one conflict surprises the user when they ran `ta template save plans,notes` and only `plans` collides. Per-db decisions match the user's per-db invocation grammar.

UX shape:

- **Off-TTY, no `--overwrite`**: every conflict is auto-skipped, listed in the report, exit 0 if any non-conflict dbs were written, exit 1 if zero were written (so CI catches a no-op).
- **Off-TTY, `--overwrite`**: every conflict is replaced.
- **TTY, conflicts present**: ONE huh.Confirm sized to the conflict count: "Overwrite N existing db(s) in `~/.ta/schema.toml`? [<csv>]" (yes = all, no = skip all). Per-db prompts read worse for >2 conflicts — the user already filtered via the positional arg. If they want surgical control they pass an explicit subset.

`SaveOptions.Overwrite` carries the resolved decision; `runTemplateSave` does the prompting and never asks `templates.SaveDBs` to prompt. Keeps the package off the huh import list (firewall §14.2 still has it on stdlib + schema + fsatomic).

## 4. Init picker integration

Current `chooseSchema` (init_cmd.go:213) does this dance: `templates.List(root)` → loop `templates.Load` per name → `collectHomeDBs` to merge across templates → `pickDBs` → `subsetSchema`. Post-F15:

```go
func chooseSchema(...) (string, []byte, error) {
    if f.template != "" {
        // --template path is now obsolete in single-file world. Either:
        //   (a) drop the flag entirely (loud break), or
        //   (b) reinterpret as a db-name shorthand selecting that single db.
        // See §8 open question — recommend (b) for back-compat in scripted bootstrap.
    }
    reg, raw, err := templates.LoadHome()
    if err != nil { return "", nil, err }
    if reg.DBs == nil || len(reg.DBs) == 0 {
        return "", nil, emptyHomeError(errOut, schemaPath)
    }
    // Build picker rows from reg.DBs (one row per db, displayName from db.Description).
    // ...
}
```

Picker rows are dbs, one per row — same behavior the user sees today, just sourced from one file. `collectHomeDBs` deletes entirely. `subsetSchema` survives; `buildProjectSchemaBytes` survives. The "malformed template skipped" warning path collapses into a single LoadHome error since there is one file: a parse failure aborts with a path-pointing error (no per-template skip-and-continue).

`emptyHomeError` text updates: instead of pointing at "examples/ + `mage install`," it points at "edit `~/.ta/schema.toml` directly or run `ta template save` from a project."

## 5. Re-validation after merge

`SaveDBs` MUST re-run `schema.LoadBytes` on the merged result before atomic write. Two distinct dbs with overlapping `paths` is the canonical failure mode — `[plans]` from project A declares `paths = ["plans.toml"]`, `[notes]` already in home declares `paths = ["plans.toml"]`, the merge crosses the `ErrOverlappingPaths` invariant. Without post-merge re-validation, `~/.ta/schema.toml` would land in a state the picker can't load.

Gate location: inside `SaveDBs` after the merged `map[string]any` is marshalled and before `fsatomic.Write`. On validation failure, return an error wrapping the offending sentinel (`ErrOverlappingPaths`, `ErrIDCollisionAcrossTypes`) and write nothing. The `runTemplateSave` caller surfaces the error via `fang`.

## 6. List / Show / Delete post-F15

- **`ta template list`**: enumerate dbs from `templates.ListDBs()`. Output rows are db names (sorted). `--json` shape changes: `{"dbs": [...]}` (was `{"templates": [...]}`). Loud key rename signals the semantic flip.
- **`ta template show <db>`**: emit the raw TOML body for one db, sliced from the home file. Implementation: re-marshal `map[string]any{<db>: bodies[<db>]}` rather than AST splice — pelletier/go-toml/v2 has no whitespace-preserving AST and the simpler round-trip matches what `init`'s `subsetSchema` already does. Comment-loss across save/show is acceptable; users authoring comment-heavy templates edit `~/.ta/schema.toml` directly.
- **`ta template delete <db>`**: remove one db block by re-emitting the rest. Same round-trip path as show. Confirms via huh on TTY, `--force` off-TTY (existing pattern). Last db deletion writes a comment-only `~/.ta/schema.toml` (parallels init's empty-schema branch); deleting from an already-empty file errors `ErrDBNotFound`.

Round-trip vs splice rationale: AST splice would require reaching into pelletier's internal AST or a third-party TOML editor; the round-trip cost is minor reformatting (already accepted for project schema generation in init). Keeping one TOML emit path across the codebase is more valuable than preserving user-authored whitespace.

## 7. Test rewrite scope

**Retire entirely** (multi-file semantics gone):
- `templates_test.go::TestListSortsAndFiltersNonToml` — multiple files in `~/.ta/` is an error post-F15.
- `templates_test.go::TestSaveCreatesRootAndWrites` — `Save(name, bytes)` no longer exists.
- `templates_test.go::TestDeleteHappyPath` — `Delete(name)` semantics changed from file-removal to db-removal.
- All `cmd/ta/template_cmd_test.go` tests asserting per-name file paths under `~/.ta/`.
- `init_cmd_test.go` tests asserting `~/.ta/<other>.toml` participates in the picker.

**Rewrite (same name, new body)**:
- `TestLoadHappyPath` → `TestLoadHomeReturnsRegistry`.
- `TestSaveValidatesBeforeWrite` → `TestSaveDBsValidatesMergeBeforeWrite` — assert that an `[invalid]` db with no `paths` blocks the merge AND leaves any existing `~/.ta/schema.toml` byte-identical.
- `TestValidateNameRejectsPathTraversal` — survive as db-name validation; same cases.

**New test functions**:
- `TestSaveDBsMergesAllProjectDBsBare` — bare invocation merges everything.
- `TestSaveDBsFiltersByCSV` — `["plans"]` and `["plans","notes"]` filter shapes.
- `TestSaveDBsConflictSkipDefault` — same-name db, no overwrite, home unchanged for that db.
- `TestSaveDBsConflictOverwrite` — same-name db, `Overwrite=true`, home replaced.
- `TestSaveDBsRejectsOverlappingPaths` — post-merge validation gate.
- `TestDeleteDBLastEntryEmptiesFile` — comment-only schema after final delete.
- `TestInitPickerSourceIsHomeSchemaOnly` — extra `~/.ta/legacy.toml` is ignored, single file is canonical.

Total test churn: ~15 functions retired or rewritten, ~7 new.

## 8. Open questions for dev confirmation

1. **`ta init --template <name>` flag fate**. In a single-file world the flag lost its primary meaning. Two paths: (a) drop the flag, loud break for any scripted callers; (b) reinterpret as "select exactly this one db from `~/.ta/schema.toml` non-interactively." Recommend (b) — preserves the off-TTY shortcut and `bootstrap.default_template` config key — but wants explicit dev call.
2. **`apply` subcommand semantics**. `ta template apply <name>` currently copies one full template file into a project. Post-F15 is `<name>` a db-name (slice the home file, write a project schema containing just that db)? Or is `apply` redundant now that `init`'s db-multi-select covers the same use case? Recommend: keep `apply <db>` as the non-interactive single-db shortcut; cross-reference `init --template` from question 1.
3. **CSV vs repeated flag**. `ta template save plans,notes` matches the spec but loses if a db name ever contains a comma (validateName already forbids separators, but CSV is still a semantic narrowing). Alternative: `ta template save plans notes` (variadic positionals, cobra `MaximumNArgs` → `ArgsArbitrary`). Recommend variadic — matches `git add foo bar`, no escape rules. Spec wording "`ta template save plans,notes`" was illustrative, not prescriptive — confirm with dev.
4. **Migration of existing `~/.ta/<name>.toml` files**. F15 makes them dead bytes on disk. Should `ta init` / `ta template list` print a one-time "found N legacy template files; migrate via …" warning? Or silently ignore them? Recommend: warning on first invocation that finds them, no auto-migration (auto-merge could clobber the user's hand-edited home schema). Needs dev call.
5. **Comment preservation on save**. The marshal-round-trip path drops user-authored comments in the home schema when ANY db is written. Acceptable per memory rule "one schema per dir" (template store is machine-managed), but worth flagging. If comment preservation is required, the implementation cost balloons: pelletier has no whitespace-preserving AST, would need a third-party editor or hand-rolled splice.

## TL;DR (for orchestrator)

F15 is a single-commit rewrite collapsing `~/.ta/*.toml` walking into a single `~/.ta/schema.toml`; new templates API surface is `LoadHome / ListDBs / ShowDB / SaveDBs / DeleteDB` with loud renames so old callers break at compile; conflicts resolve at db-granularity (per-call confirm on TTY, `--overwrite` flag off-TTY, default skip); post-merge re-validates via `schema.LoadBytes` to catch overlapping paths; init picker simplifies to one-file load + db-multi-select; ~22 test functions churn; 5 open questions block implementation, primarily `--template` flag fate, `apply` semantics, and legacy-file migration policy.

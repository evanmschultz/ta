# F10 — ID Cleanup (Strict Pre-MVP)

**Status:** locked spec, ready for opus builder.
**Discipline:** strict pre-MVP. No back-compat. No transition code. No half-migrated state. No tolerance flags. All tests rewrite same slice. Memory rules honored everywhere.
**Memory rules in force:**
- `feedback_ta_id.md` — records have ids; on-disk bracket IS the id; type NEVER in id (lives in index only).
- `feedback_ta_one_schema_file_per_dir.md` — one schema.toml per .ta/ directory.
- `feedback_ta_index_rebuild_recovery_only.md` — rebuild recovery-only (legitimate use during a documented format migration).

---

## 1. The Locked Architecture (After This Slice)

### 1.1 The ID

Records have an **id**. The id is one dotted path. Examples:

- `plans.demo-1`
- `notes.note-001`
- `workflow.drop_3.db.task-001`

The id is what users / agents pass. The id is what ta returns in responses. The id is what `cat <file>.toml` shows as the bracket header. ONE word, ONE shape, end-to-end.

### 1.2 Type Lives In The Index, Never In The ID

`.ta/index.toml` records `{type, created, updated}` per id. Read paths resolve type from the index. Writes pass type via `--type plans.task` (CLI) or `typeName` (MCP), required only on Create.

### 1.3 On-Disk Bracket = The ID

`plans.toml` contains `[plans.demo-1]`. NOT `[plans.task.demo-1]`. The TOML bracket header is the id verbatim. No type segment ever appears in any data file's bracket path.

The schema's `[<db>.<type>]` declaration in `schema.toml` is METADATA describing the shape of records of that type. It is NOT a bracket prefix that propagates into data files.

### 1.4 ID Uniqueness Per File

Within a single-file db, ids are unique across types. If db `plans` declares both `task` and `note` types, no `t1` id may belong to both — `[plans.t1]` is one record, not two. Schema-load enforces this invariant via `ErrIDCollisionAcrossTypes`. CRUD operations re-check at write time.

For multi-file glob mounts (`paths = ["workflow/*/db.toml"]`), each instance file is its own scope.

### 1.5 Format Inferred From Path Extension

`paths = ["plans.toml"]` → TOML backend. `paths = ["docs/*.md"]` → MD backend. Schema-load validates: every path has a recognized extension; all paths in one db share the same extension. No `format` field declared in schema.

New errors: `ErrInconsistentPathFormats`, `ErrAmbiguousPathFormat`.

`Field.Format` (markdown render hint at `[<db>.<type>.fields.<name>] format = "markdown"`) is PRESERVED — different semantic from db-level format. NON-TARGET of this slice.

### 1.6 Collection Mounts Rejected

`paths = ["docs/"]` (trailing-slash) rejects at schema-load with `ErrCollectionMountUnsupported`. Schemas requiring directory expansion use globs (`paths = ["docs/*.md"]`). All collection-mount runtime code DELETED — no half-migrated state.

### 1.7 `--type` / `typeName`

REQUIRED on Create, OPTIONAL on read paths (resolved from index when absent; cross-checked when passed). MUST be db-qualified (`plans.task`). Bare-slug `task` rejected with `ErrTypeNotQualified`.

### 1.8 Index Format v2

`format_version = 2`. Mismatch fails at load with `ErrUnknownFormatVersion` and a one-shot `ta index rebuild` remediation. Old-format detection covered by `format_version` mismatch + per-file bracket form mismatch (see §1.9).

### 1.9 Error Sentinels (Three, Distinct)

- `ErrRecordNotFound` — no such record on disk and no index entry. Remediation: check the id.
- `ErrIndexMissing` — `.ta/index.toml` file absent. Remediation: `ta index rebuild` to create it.
- `ErrTypeUnresolved` — record exists on disk but no index entry; type can't be resolved. Remediation: `ta index rebuild`.

### 1.10 No Tolerance Flags

`verifyTypeAgainstId`'s missing-from-index tolerance retires. `deleteIndexEntry`'s `ErrUnknownFormatVersion` tolerance retires. Hard failure + rebuild hint replaces both.

### 1.11 MCP `dbView.Format`

KEEP on read (derived field, inspectability). REJECT `format=` on input (`schema(action=create|update, kind=db, data=...)`) with loud error.

### 1.12 Code-Surface Vocabulary

- Struct: `Resolved` (was `Address`) — the resolved form of an id; contains derived fields like `FilePath`, `BracketKey` for internal use.
- Function: `ResolveID(id string) (Resolved, error)` (was `ParseAddress`).
- Function arguments: take `id string` where they took `section string` or `addr db.Address`.
- MCP parameter: `id` (was `section`).
- CLI flag: `<id>` positional (was `<section>` / `<address>`).
- Errors: `ErrIDDoesNotMatchAnyDB`, `ErrBadID`, plus the rest of §1.9.

---

## 2. `resolveTypeForID` Branch Table

```
resolveTypeForID(resolved Resolved, typeName string, requireType bool, projectRoot string) (resolvedType string, err error)
```

| typeName    | requireType | Index hit? | Behavior                                                                  |
|-------------|-------------|------------|---------------------------------------------------------------------------|
| ""          | true        | n/a        | `ErrTypeMismatch` — Create requires `--type`. Loud, fail at top of call.  |
| ""          | false       | yes        | Return index entry's type.                                                |
| ""          | false       | no         | `ErrTypeUnresolved` — record not indexed; remediation: `ta index rebuild`.|
| "<db>.<type>" | true      | n/a        | Validate db.type combo exists in schema; return typeName. (Create.)       |
| "<db>.<type>" | false     | yes (match)  | Return resolved (matching) type.                                        |
| "<db>.<type>" | false     | yes (mismatch) | `ErrTypeMismatch` with index's type vs caller's type.                 |
| "<db>.<type>" | false     | no         | Return typeName as authoritative; index will pick up on next rebuild.    |
| "task" (bare) | any       | n/a        | `ErrTypeNotQualified` — "use db-qualified form, e.g. `plans.task`".      |

`Resolved` carries no type field — type is returned as `(string, error)`, never stored alongside id-resolution data.

---

## 3. Files In Scope (Exhaustive)

### 3.1 Production Source

- `internal/db/address.go` → conceptually rename to `internal/db/id.go`. Drop `Address.Type` field. Rename `Address` struct to `Resolved`. Rewrite `Canonical()` to return id (drop type composition). Rewrite `ParseAddress` → `ResolveID`. Drop `firstDeclaredTypeIndex`. Drop collection-mount runtime branches. Doc comments rewritten in id vocabulary.
- `internal/db/resolver.go:105-108,165-209` — delete `walkCollection`, delete collection-check branch in `expandMount`.
- `internal/ops/backend.go:85-115` — `tomlBracketPath(id string)` (no type parameter, no composition; the id IS the bracket path). `backendSectionPath(id string)`. `tomlRelPathForFields(id string)`. Backend lookup is by id.
- `internal/ops/helpers.go` — `validationPath` keeps; signatures take id; `verifyTypeAgainstAddress` RETIRES; `verifyTypeAgainstIndex` RETIRES; new `resolveTypeForID` per §2; `writeIndexEntry` uses id as key; `deleteIndexEntry` retires `ErrUnknownFormatVersion` tolerance.
- `internal/ops/ops.go` — Get / GetAllFields / Create / Update / Delete / Search call `resolveTypeForID`. Sentinel checks at lines 482, 657 rewrite to `resolved.BracketKey == ""` predicate (or whatever signals scope vs single-record post-rename). Search post-walk filter loads index once into a map, lookup-per-result.
- `internal/ops/schema_mutate.go:115,252` — drop `"format"` from emitted map; drop `"format"` from metaKey iteration; reject incoming `data["format"]` with new `ErrFormatKeyForbidden`.
- `internal/index/index.go` — bump `format_version` to 2. Comment lines 53-54 update. `flattenInto`/`nestEntry` agnostic to key shape (no change).
- `internal/index/rebuild.go:209-265` — `canonicalForBracket` returns the bracket name verbatim (which IS the id). No type stripping. No db-prefix stripping in single-file case (the file's bracket is `[plans.demo-1]`, the id is `plans.demo-1`, identity transform). For multi-file glob mounts, the bracket is `[id-segment-after-file-relpath]`; canonical key prepends file-relpath to produce full id.
- `internal/schema/load.go` — derive db format from first path's extension; validate all paths share extension; reject collection mounts; reject extensionless paths; new id-uniqueness-across-types invariant. Robust dotfile-aware extension parsing.
- `internal/schema/meta.go` / `internal/schema/meta_schema.toml` — drop self-declared `format = "toml"` on `[ta_schema]` root; drop `[ta_schema.db.fields.format]` block; rewrite description prose; rewrite Phase-9.1 history paragraph.
- `internal/search/search.go:215-235,226,294-316,300,366-376,394` — drop `firstDeclaredTypeIndexHere`; rewrite `parseScope` for id-prefix shape; drop `matchCollectionScope`; switch `searchPlan.typeName` from scope-parse to caller-supplied.
- `cmd/ta/commands.go` — `--type` flag descriptions updated (db-qualified). All cobra Long/Example strings rewrite. `lookupDBAndType` reads enriched type from `resolveTypeForID`. Positional argument named `id` everywhere (was `section`/`address`). Lines: 42-58, 111, 267-278, 378-388, 417, 438-452, 481, 493-502, 519, 697.
- `cmd/ta/init_cmd.go` — audit for id-vocabulary leakage in cobra example or default content.
- `internal/mcpsrv/tools.go` — tool descriptions rewrite. `id` parameter (renamed from `section`). `dbView.Format` stays as derived read-only output; `format=` on input rejected. `type` parameter description specifies db-qualified form. Lines 21-25, 31-32, 49-54, 71-78, 96-105, 117-120, 129-151, 168-177, 720-728.
- `internal/mcpsrv/server.go` — no source-code grammar knowledge, no change.
- `magefile.go:515,528` — literal id composition `"ta.db.build_task." + b.id` and `"ta.db.qa_task." + q.id` rewrite to drop type segment: `"ta.db." + b.id` and similar (or whatever the new id form is for those records under their schema).

### 3.2 Tests (ALL Rewrite, Same Slice)

ALL on-disk TOML bracket fixtures rewrite to drop type segment. ALL id-string literals migrate. Collection-mount fixtures convert to globs. Extensionless paths get extensions. `format = "toml"`/`format = "md"` lines REMOVED. `Address` → `Resolved` references in test code rename.

- `internal/db/address_test.go` (8 hits) — renames to `id_test.go` conceptually; parser semantics drives this; case-shape changes, not just literals.
- `internal/db/instance_test.go` (3 hits).
- `internal/index/index_test.go`.
- `internal/index/rebuild_test.go` (23 hits, fixture lines 25/44/58 + bracket-form rewrites).
- `internal/ops/ops_test.go` (39 hits, including IsScopeAddress test cases at 262-264).
- `internal/ops/dogfood_test.go` (22 hits).
- `internal/ops/cache_test.go`.
- `internal/ops/schema_mutate_test.go`.
- `internal/search/search_test.go` (71 hits).
- `internal/render/renderer_test.go`.
- `internal/render/schema_flow_test.go`.
- `internal/mcpsrv/server_test.go` (39 hits).
- `cmd/ta/commands_test.go` (71 hits).
- `cmd/ta/init_cmd_test.go`.
- `internal/templates/templates_test.go`.
- `internal/schema/meta_test.go`.

### 3.3 Goldens

- `cmd/ta/testdata/get_single.golden:2` — id rewrite (drop type segment).
- `cmd/ta/testdata/get_single_json.golden:2-3` — bytes line rewrites (on-disk bracket NOW MATCHES id); `section` field renamed to `id`, value rewritten.
- `cmd/ta/testdata/schema_flow.golden:11-12` — preserve format render decision per §1.11.

### 3.4 Examples + Meta-Schema

- `examples/schema.toml:25` — `paths = ["plans"]` → `paths = ["plans.toml"]`. Drop `format = "toml"`.
- `examples/schemas/cascade.toml` — drop every `format = "..."` line. Verify all paths have extensions. Verify any sample-record references use id form.
- `examples/README.md` — remove `format = ` references.
- `internal/schema/meta_schema.toml:14-17` — drop `format = "toml"` self-declaration on `[ta_schema]` root.
- `internal/schema/meta_schema.toml:28-32` — drop `[ta_schema.db.fields.format]` block.
- `internal/schema/meta_schema.toml:37` — rewrite description prose (drop `'docs/'` and extensionless examples).
- `internal/schema/meta_schema.toml` — Phase-9.1 history paragraph: rewrite for current state.

### 3.5 Docs

- `docs/PLAN.md` — wholesale-restructured version (in-flight). All id-vocabulary fixes from QA review (id form everywhere, no `<file-relpath>.<id-tail>` framing in user-facing prose, no chronology lexicon, F3 + F16 included or explicitly subsumed, three sentinels named, MCP `dbView.Format` asymmetry stated, F12 ordering reconciled, etc.).
- `docs/cascade-methodology.md` — audit for id-vocabulary leakage; verify §11 Canonical Node Shape uses id form.
- `E2E_FIXES.md` — close-out F10 entry with corrected diagnosis paragraph; F11 entry retires (its reason-to-exist dissolves once bracket-form is uniform).

---

## 4. Build Tasks (Sequential, Each QA-Gated)

### T0 — `docs/PLAN.md` Restructured

(Already drafted; fixes from QA review applied in T0a addendum.)

### T0a — `docs/PLAN.md` QA-Driven Fixes

Apply the 12 consolidated QA findings (chronology lexicon, retired-form leakage, migration framing, F3 + F16 inclusion, three sentinels, MCP `dbView.Format`, F21.3 aliases, F12/F24/v0.1.0 reconciliation, line 32 fence, line 154 cascade tense, F11 framing, line 178 forward-ref).

In light of the id-vocabulary lock, also: rewrite `<file-relpath>.<id-tail>` framing throughout to the id-only formulation; rename "address" → "id" in user-facing prose; drop the line-32 bracket-vs-address discussion entirely (bracket IS id, no fence needed).

Single commit. `MAGEFILE_JSON=1 mage check` green (sanity, no Go change).

### T1 — ID Grammar + Resolved Struct + Backend Signatures (ATOMIC)

The atomic compile-or-broken slice. Tree must compile after this commit.

- `internal/db/address.go` rename to `internal/db/id.go`. Drop `Address.Type`. Rename `Address` struct → `Resolved`. Rewrite `Canonical()`. Rewrite `ParseAddress` → `ResolveID`. Retire `firstDeclaredTypeIndex`. Retire collection-mount runtime branches.
- `internal/db/resolver.go`: delete `walkCollection`, delete collection-check in `expandMount`.
- `internal/ops/errors.go`: add `ErrTypeUnresolved`, `ErrIndexMissing`, `ErrRecordNotFound`, `ErrTypeNotQualified`, `ErrFormatKeyForbidden`, `ErrCollectionMountUnsupported`, `ErrInconsistentPathFormats`, `ErrAmbiguousPathFormat`, `ErrIDCollisionAcrossTypes`. Rename existing: `ErrUnknownDB` → `ErrIDDoesNotMatchAnyDB`, `ErrBadAddress` → `ErrBadID`.
- `internal/ops/helpers.go`: retire `verifyTypeAgainstAddress`; retire `verifyTypeAgainstIndex` tolerance; new `resolveTypeForID`; retire `deleteIndexEntry` `ErrUnknownFormatVersion` tolerance.
- `internal/ops/backend.go`: `tomlBracketPath(id string) string` — id IS bracket. `backendSectionPath(id string)`, `tomlRelPathForFields(id string)`.
- `internal/ops/ops.go`: every `addr.Type` rewrites; every `addr.X` becomes `resolved.X`; Create/Update/Get/Delete/Search call `resolveTypeForID`; `IsScopeAddress` predicate rewrites; Search type filter loads index map once.
- `internal/ops/schema_mutate.go`: drop `"format"` emit + metaKey; reject incoming `data["format"]`.
- `magefile.go:515,528`: id literal rewrite (drop type segment).
- All test files in §3.2 rewrite simultaneously — including bracket-form fixtures (data files inside tests).
- Verification: `MAGEFILE_JSON=1 mage check` green.
- Single commit.

### T2 — Schema Loader Format Inference + ID-Uniqueness Invariant

- `internal/schema/load.go`: derive format from first path extension; validate; reject collection mounts; reject extensionless paths; enforce `ErrIDCollisionAcrossTypes` per file scope.
- `internal/schema/meta.go` / `meta_schema.toml`: drop self-declared `format`; drop meta-field declaration; rewrite description prose + history paragraph.
- `examples/schema.toml`: rewrite paths + drop format.
- `examples/schemas/cascade.toml`: drop every `format = "..."` line.
- `examples/README.md`: remove `format = ` references.
- All extensionless-paths fixtures rewrite.
- Verification: `MAGEFILE_JSON=1 mage check` green.
- Single commit.

### T3 — Index Format Bump + Canonical-Key Identity

- `internal/index/index.go`: `format_version = 2`. Comment update.
- `internal/index/rebuild.go`: `canonicalForBracket` returns bracket-as-id (identity transform for single-file; file-relpath-prefixed for multi-file glob mounts).
- Index test fixtures rewrite to v2 + new key shape.
- Migration test: load v1 index → `ErrUnknownFormatVersion` → user runs `ta index rebuild` → v2 index produced → reads succeed.
- Idempotent rebuild test: rebuilding v2 index produces byte-identical output.
- Verification: `MAGEFILE_JSON=1 mage check` green.
- Single commit.

### T4 — TOML Bracket Migration On Data Files

The big new piece. Existing `plans.toml`-style files have `[plans.task.demo-1]`-form brackets. Post-T1+T2+T3 these still parse via the resolver because resolver is fixed, but the on-disk shape is wrong.

- Rebuild walks each declared db's paths; opens each file; for every existing bracket whose form is `[<file-relpath>.<type>.<id>]`, rewrites to `[<file-relpath>.<id>]`. Atomic write per file.
- New error: `ErrFileFormatTooOld` — fired when a read encounters a bracket that contains a type segment AND the index is at v2. Single-action remediation: `ta index rebuild`.
- Migration semantics: rebuild also re-keys the index entries to match the new bracket form (already covered in T3 via canonical-key identity).
- Test: project with mixed v1-shape data files → first read fires `ErrFileFormatTooOld` → `ta index rebuild` migrates files + index → reads succeed.
- Verification: `MAGEFILE_JSON=1 mage check` green.
- Single commit.

### T5 — Search + List-Sections Walker Cleanup (Subsumes Old F11)

Once bracket form is uniform `<file-relpath>.<id-tail>`-shape (i.e. the id), the per-mount-shape decision in the walker collapses. F11's reason-to-exist (bracket-form misalignment) DISSOLVES. Walker just iterates each db's `paths`, opens each file, scans bracket = id.

- `internal/search/search.go`: drop `firstDeclaredTypeIndexHere`; rewrite `parseScope` for id-prefix shape; drop `matchCollectionScope`; rewire `searchPlan.typeName` from scope-parse to caller-supplied.
- Walker tolerates missing literal paths (already correct in current code; add explicit regression tests).
- F11 entry in `E2E_FIXES.md` retires.
- Verification: `MAGEFILE_JSON=1 mage check` green.
- Single commit.

### T6 — CLI + MCP Surface

- `cmd/ta/commands.go`: positional `<id>` everywhere; `--type` db-qualified flag descriptions; cobra Long/Example strings rewrite.
- `cmd/ta/commands_test.go` (71 hits): rewrite ids + `--type` values + bracket-form fixtures.
- `cmd/ta/init_cmd.go` + `init_cmd_test.go`: audit + rewrite.
- `internal/mcpsrv/tools.go`: `id` parameter (renamed from `section`); `dbView.Format` stays on read; `format=` rejected on input; `type` description db-qualified.
- `internal/mcpsrv/server_test.go` (39 hits): rewrite.
- Goldens: regen per §3.3.
- Verification: `MAGEFILE_JSON=1 mage check` green.
- Single commit.

### T7 — Docs Closeout

- `docs/cascade-methodology.md`: audit id-vocabulary; verify §11 Canonical Node Shape.
- `examples/README.md`: final pass.
- `E2E_FIXES.md`: F10 close-out paragraph; F11 retirement note (subsumed by T5).
- Verification: visual review.
- Single commit.

---

## 5. Why F11 Retires

F11 was "walker bracket-form misalignment between write (per-mount-entry) and read (per-db)." That misalignment EXISTED BECAUSE the bracket form differed: single-file dbs got `[<db>.<type>.<id>]`, multi-file dbs got `[<type>.<id>]`. Read paths chose the wrong form when `len(Paths) != 1`.

Once the bracket form is uniform `<file-relpath>.<id-tail>`-shape (T4), there is no per-mount-shape decision. Walker reads bracket = id. F11's whole architectural problem dissolves.

The remaining piece of F11 — "walker tolerates missing literal paths" — was already correctly handled in current code (per F11 QA finding: walker already swallows `fs.ErrNotExist`). T5 adds explicit regression tests to lock the existing tolerance.

`E2E_FIXES.md` F11 entry retires in T7.

---

## 6. Late-Stage Dogfood-Refinement File Format Additions

(Carried forward from prior plan; adds candidate formats once F10 lands.)

- TXT — paragraph-or-section addressable; lowest existing structured tooling; high journal/log/brain-dump value.
- JSON / JSONC — wide adoption; uniform CRUD across config files.
- YAML — structured-data peer to TOML; common in CI/CD.
- `.env` — flat key=value; trivial parser.
- Justfile — recipe DSL; useful for monorepo recipe rewrites.
- Dockerfile — directive blocks; useful for "rewrite base images across many projects."

Lower priority: Makefile (grammar complexity), INI (existing tools sufficient), per-daemon configs. NOT IN SCOPE: source code formats (Go / Python / TS) — language servers cover that.

The format-from-extension inference architecture introduced in T2 is the foundation that makes adding any of these a localized change.

---

## 7. Sequencing Summary

T0 (PLAN.md restructure — DONE) → T0a (PLAN.md QA fixes) → T1 (id grammar + Resolved struct + backend signatures + tests) → T2 (schema format inference + id-uniqueness) → T3 (index format v2) → T4 (TOML data file bracket migration) → T5 (walker cleanup; F11 retires) → T6 (CLI + MCP surface) → T7 (docs closeout)

Each T-task its own commit. `MAGEFILE_JSON=1 mage check` green before every commit. opus QA-twin pair (proof + falsification) review before every commit. No commit ships if either QA finds unmitigated counterexample.

`mage install` is dev-only — never a verification target during this slice.

No raw `gofmt` / `gofumpt` — mage targets only.

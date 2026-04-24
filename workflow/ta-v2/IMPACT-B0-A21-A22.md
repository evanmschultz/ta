# IMPACT — §12.17.5 [B0] / [A2.1] / [A2.2]

Code-impact analysis for the three decoupling items. Every claim is cited
to `path:line`. Read in order — [B0] establishes the package layout the
other two build against.

All absolute paths are rooted at
`/Users/evanschultz/Documents/Code/hylla/ta/main`. For brevity, `$ROOT`
denotes that prefix in bulleted lists; fully-qualified citations in prose
use the absolute form.

---

## 1. [B0] impact — split `internal/mcpsrv/` into `internal/ops/` + `internal/mcpsrv/`

### 1.1 Files to move / create / modify / delete

**Move verbatim from `$ROOT/internal/mcpsrv/` to `$ROOT/internal/ops/`**
(change `package mcpsrv` → `package ops` at the top of each; no other
edits in this pass):

- `ops.go` (563 lines — `$ROOT/internal/mcpsrv/ops.go`)
- `backend.go` (110 lines — `$ROOT/internal/mcpsrv/backend.go`)
- `cache.go` (215 lines — `$ROOT/internal/mcpsrv/cache.go`)
- `errors.go` (69 lines — `$ROOT/internal/mcpsrv/errors.go`)
- `fields.go` (155 lines — `$ROOT/internal/mcpsrv/fields.go`)
- `schema_mutate.go` (412 lines — `$ROOT/internal/mcpsrv/schema_mutate.go`)

**Split** `$ROOT/internal/mcpsrv/tools.go` (670 lines). Four helpers
currently live here but are called exclusively from domain code, so they
move with the domain:

- `spliceOut` (`tools.go:366`) — called from `ops.go:431` (Delete). Move
  to `$ROOT/internal/ops/ops.go` (or a new `$ROOT/internal/ops/splice.go`).
- `readFileIfExists` (`tools.go:570`) — called from `ops.go:173`
  (Create). Move to `ops` package.
- `validationPath` (`tools.go:585`) — called from `ops.go:157`
  (Create) and `ops.go:303` (Update). Move to `ops` package.
- `tomlRelPathForFields` (`tools.go:611`) — called from `ops.go:87, 140,
  333`. Move to `ops` package.

The remainder of `tools.go` — tool decls (`getTool` … `schemaTool` at
`18-144`), handlers (`handleGet`, `handleListSections`, `handleCreate`,
`handleUpdate`, `handleDelete`, `handleSearch`, `handleSchema`,
`handleSchemaGet`, `handleSchemaMutate` at `209-508`), result structs
(`listResult`, `mutationSuccess`, `fieldsResult`, `schemaResult`,
`dbView`, `typeView`, `fieldView`, `searchHit`, `searchResult` at
`148-327`), MCP req-parsing helpers (`requirePathAndSection`,
`requireDataObject`, `optionalStringArray`, `mustJSON`,
`validationOrPlainError` at `377-566`), and schema-view helpers
(`toDBsView`, `toDBView`, `toTypesView`, `toTypeView` at `624-669`) —
stays in `$ROOT/internal/mcpsrv/tools.go`, rewired to call `ops.Get`,
`ops.ListSections`, `ops.Create`, `ops.Update`, `ops.Delete`,
`ops.Search`, `ops.MutateSchema`, `ops.ResolveProject`, and the `ops.Err*`
sentinels.

**Modify in place (stays in `$ROOT/internal/mcpsrv/`):**

- `server.go` (82 lines) — `New` (`server.go:46`) currently calls
  `defaultCache.Resolve(cfg.ProjectPath)` on `server.go:56` to pre-warm
  the cache. After [B0] the cache lives in `ops`; swap to
  `ops.ResolveProject(cfg.ProjectPath)` which already goes through
  `ops.defaultCache` under the hood. `registerTools` (`server.go:73`)
  unchanged. Package stays `mcpsrv`.
- `tools.go` — rewire every call; see §1.1 above and §1.2–§1.3 below.
- `testing.go` (14 lines) — `ResetDefaultCacheForTest` at line 12
  currently wipes `mcpsrv`'s cache. Move the function to
  `$ROOT/internal/ops/testing.go` (package `ops`) so external tests that
  reset the cache (cmd/ta commands_test.go:40-41, dogfood_test.go:82-83)
  call `ops.ResetDefaultCacheForTest`. Keep a thin delegating wrapper
  in `$ROOT/internal/mcpsrv/testing.go` ONLY if some existing
  `mcpsrv_test` file still needs the old symbol by the old name. For
  B0's mechanical-only contract, rename callers rather than keep a
  wrapper.
- `doc.go` (4 lines) — docstring says `mcpsrv hosts the MCP server and
  its four tool handlers: get, list_sections, schema, and upsert`. Stale
  anyway (seven tools now, no upsert). Tighten to the post-[B0] scope:
  "MCP protocol glue over `internal/ops`" per §6a.3.

**Delete:**

- `$ROOT/internal/mcpsrv/export_test.go` (39 lines) — every exported
  test hook references the package-local `defaultCache` /
  `resolveFromProjectDirUncached`. Move the file to
  `$ROOT/internal/ops/export_test.go` (package `ops`). The three
  exported hooks (`DefaultCacheLoadCountForTest`,
  `SwapDefaultCacheLoaderForTest`, `DefaultResolveUncachedForTest` at
  `export_test.go:19-39`) all move; one hook `(s *Server).MCPServer()`
  at `export_test.go:10` stays on `mcpsrv` (`server.go` Server type
  does not move).
- `$ROOT/internal/mcpsrv/cache_test.go` (302 lines) — pure cache tests,
  package `mcpsrv_test`. Move to
  `$ROOT/internal/ops/cache_test.go` as package `ops_test`; rewrite
  imports: `internal/mcpsrv` → `internal/ops`.
- `$ROOT/internal/mcpsrv/dogfood_test.go` (232 lines) — exercises
  `mcpsrv.Create` / `mcpsrv.Get` / `mcpsrv.Search`, zero MCP protocol
  surface. Move to `$ROOT/internal/ops/dogfood_test.go`. **Name
  collision**: `$ROOT/internal/search/dogfood_test.go` already exists;
  renaming would be cosmetic and Go allows same-filename-different-dir,
  so no conflict.
- `$ROOT/internal/mcpsrv/server_test.go` (1328 lines, 43 Test funcs at
  `$ROOT/internal/mcpsrv/server_test.go:1475, 1533, 1541` etc.) — MCP
  in-process-client tests. STAYS in `mcpsrv_test` (it tests the MCP
  protocol surface end-to-end). No edits in the mechanical [B0] pass.

**Create:**

- `$ROOT/internal/ops/` directory.
- `$ROOT/internal/ops/doc.go` — one-line package doc per the [B0]
  charter: "Package ops is the Go-level endpoint layer shared by CLI
  and MCP adapters. No protocol dependencies; plain Go in, plain Go
  out. See docs/PLAN.md §6a."

### 1.2 Exports that change name or package

Every move flips the package qualifier. Callers outside the old
`mcpsrv` rewire; callers inside move with the symbol. Exhaustive
list:

| Symbol | Old | New | Kind |
|---|---|---|---|
| `ResolveProject` | `mcpsrv.ResolveProject` | `ops.ResolveProject` | func — `ops.go:37` |
| `GetResult` | `mcpsrv.GetResult` | `ops.GetResult` | struct — `ops.go:44` |
| `Get` | `mcpsrv.Get` | `ops.Get` | func — `ops.go:53` |
| `GetAllFields` | `mcpsrv.GetAllFields` | `ops.GetAllFields` | func — `ops.go:105` |
| `Create` | `mcpsrv.Create` | `ops.Create` | func — `ops.go:152` |
| `Update` | `mcpsrv.Update` | `ops.Update` | func — `ops.go:247` |
| `Delete` | `mcpsrv.Delete` | `ops.Delete` | func — `ops.go:390` |
| `SearchHit` | `mcpsrv.SearchHit` | `ops.SearchHit` | struct — `ops.go:440` |
| `ListSections` | `mcpsrv.ListSections` | `ops.ListSections` | func — `ops.go:458` |
| `Search` | `mcpsrv.Search` | `ops.Search` | func — `ops.go:473` |
| `MutateSchema` | `mcpsrv.MutateSchema` | `ops.MutateSchema` | func — `schema_mutate.go:35` |
| `ErrRecordExists` | `mcpsrv.ErrRecordExists` | `ops.ErrRecordExists` | sentinel — `errors.go:15` |
| `ErrRecordNotFound` | `mcpsrv.ErrRecordNotFound` | `ops.ErrRecordNotFound` | sentinel — `errors.go:20` |
| `ErrFileNotFound` | `mcpsrv.ErrFileNotFound` | `ops.ErrFileNotFound` | sentinel — `errors.go:24` |
| `ErrAmbiguousDelete` | `mcpsrv.ErrAmbiguousDelete` | `ops.ErrAmbiguousDelete` | sentinel — `errors.go:28` |
| `ErrReservedName` | `mcpsrv.ErrReservedName` | `ops.ErrReservedName` | sentinel — `errors.go:33` |
| `ErrMetaSchemaViolation` | `mcpsrv.ErrMetaSchemaViolation` | `ops.ErrMetaSchemaViolation` | sentinel — `errors.go:38` |
| `ErrTypeHasRecords` | `mcpsrv.ErrTypeHasRecords` | `ops.ErrTypeHasRecords` | sentinel — `errors.go:43` |
| `ErrDBHasData` | `mcpsrv.ErrDBHasData` | `ops.ErrDBHasData` | sentinel — `errors.go:47` |
| `ErrUnknownSchemaTarget` | `mcpsrv.ErrUnknownSchemaTarget` | `ops.ErrUnknownSchemaTarget` | sentinel — `errors.go:51` |
| `ErrUnknownField` | `mcpsrv.ErrUnknownField` | `ops.ErrUnknownField` | sentinel — `errors.go:55` |
| `ErrUnsupportedFormat` | `mcpsrv.ErrUnsupportedFormat` | `ops.ErrUnsupportedFormat` | sentinel — `errors.go:60` |
| `ErrCannotClearRequired` | `mcpsrv.ErrCannotClearRequired` | `ops.ErrCannotClearRequired` | sentinel — `errors.go:67` |
| `ResetDefaultCacheForTest` | `mcpsrv.ResetDefaultCacheForTest` | `ops.ResetDefaultCacheForTest` | func — `testing.go:12` |
| `DefaultCacheLoadCountForTest` | `mcpsrv.DefaultCacheLoadCountForTest` | `ops.DefaultCacheLoadCountForTest` | func — `export_test.go:19` |
| `SwapDefaultCacheLoaderForTest` | `mcpsrv.SwapDefaultCacheLoaderForTest` | `ops.SwapDefaultCacheLoaderForTest` | func — `export_test.go:28` |
| `DefaultResolveUncachedForTest` | `mcpsrv.DefaultResolveUncachedForTest` | `ops.DefaultResolveUncachedForTest` | func — `export_test.go:37` |

**Stays on `mcpsrv`** (no rename):

- `mcpsrv.Config` (`server.go:18`) and `mcpsrv.Config.Name/Version/ProjectPath` fields.
- `mcpsrv.Server` (`server.go:31`).
- `mcpsrv.New(cfg Config) (*Server, error)` (`server.go:46`).
- `(*Server).Run(ctx) error` (`server.go:68`).
- `(*Server).MCPServer() *server.MCPServer` (test-only — `export_test.go:10`).

Nothing else in `mcpsrv`'s current surface is exported. Every unexported
helper either moves with the domain files (`cloneMap`, `splitTwo`,
`splitThree`, `applyDBMutation`, `applyTypeMutation`, `applyFieldMutation`,
`ensureFieldsTable`, `dbHasDataOnDisk`, `typeHasRecordsOnDisk`,
`registryFromRoot`, `loadSchemaMap`, `applyMutation`,
`resolveFromProjectDir`, `resolveFromProjectDirUncached`,
`defaultCache`, `schemaCache`, `cacheEntry`, `newSchemaCache`,
`newSchemaCacheWithLoader`, `resolutionSource`, `snapshotMTime`,
`buildBackend`, `tomlDeclaredName`, `backendSectionPath`,
`stripTOMLPrefix`, `extractFields`, `extractTOMLFields`,
`extractMDFields`, `extractAllDeclaredFields`, `stripHeadingLine`,
`loadExistingFields`, `overlayPatch`, `deleteAtLevel`) or stays in
`mcpsrv/tools.go` (the MCP-protocol-bound unexported helpers listed
above). The four helpers flagged under §1.1 (`spliceOut`,
`readFileIfExists`, `validationPath`, `tomlRelPathForFields`) move
from `tools.go` to `ops/` because they are domain-side callees whose
current location in `tools.go` is historical drift, not architectural
intent.

### 1.3 Callers that must rewire

**All three files in `cmd/ta/` that import mcpsrv:**

- `$ROOT/cmd/ta/main.go` — imports `mcpsrv` at `main.go:25`; calls
  `mcpsrv.New`/`mcpsrv.Config` at `main.go:217`. After [B0]: no rewire
  here because `New`/`Config` stay on `mcpsrv`. Import stays.
- `$ROOT/cmd/ta/commands.go` — imports `mcpsrv` at `commands.go:15`.
  Every symbol reference rewires. Enumerated from `rg -n "mcpsrv\."
  cmd/ta/commands.go`:
  - `commands.go:55` — `mcpsrv.Get(path, section, fields)` →
    `ops.Get(path, section, fields)`.
  - `commands.go:63` — `mcpsrv.GetAllFields(path, section)` →
    `ops.GetAllFields(path, section)`.
  - `commands.go:69` — `mcpsrv.Get(path, section, fields)` → `ops.Get`.
  - `commands.go:111` — `mcpsrv.Get(path, section, nil)` → `ops.Get`.
  - `commands.go:141` — `mcpsrv.ResolveProject(path)` →
    `ops.ResolveProject`.
  - `commands.go:157` — `mcpsrv.ResolveProject(path)` → `ops.ResolveProject`.
  - `commands.go:249` — `mcpsrv.ListSections(path, resolvedScope)` →
    `ops.ListSections`.
  - `commands.go:562` — `mcpsrv.Search(path, scope, match, query, field)` →
    `ops.Search`.
  - `commands.go:583` — `[]mcpsrv.SearchHit` → `[]ops.SearchHit`.
  - `commands.go:597` — `[]mcpsrv.SearchHit` → `[]ops.SearchHit`.
  - `commands.go:602` — `mcpsrv.ResolveProject(path)` → `ops.ResolveProject`.
  - `commands.go:655, 692` — `mcpsrv.ResolveProject(path)` →
    `ops.ResolveProject`.
  - `commands.go:872` — `mcpsrv.Create(path, section, pathHint, data)` →
    `ops.Create`.
  - `commands.go:876` — `mcpsrv.Update(path, section, data)` →
    `ops.Update`.
  - `commands.go:880` — `mcpsrv.Delete(path, section)` → `ops.Delete`.
  - `commands.go:884` — `mcpsrv.MutateSchema(path, action, kind, name,
    data)` → `ops.MutateSchema`.
- `$ROOT/cmd/ta/commands_test.go` — imports `mcpsrv` at `commands_test.go:11`.
  - `commands_test.go:40` — `t.Cleanup(mcpsrv.ResetDefaultCacheForTest)` →
    `t.Cleanup(ops.ResetDefaultCacheForTest)`.
  - `commands_test.go:41` — `mcpsrv.ResetDefaultCacheForTest()` →
    `ops.ResetDefaultCacheForTest()`.

Add `"github.com/evanmschultz/ta/internal/ops"` to all three cmd/ta
files' import blocks. The existing `mcpsrv` import in `main.go` stays
(for `mcpsrv.New`/`mcpsrv.Config`); commands.go and commands_test.go
drop the `mcpsrv` import entirely once all rewires are applied.

**Magefile:**

- `$ROOT/magefile.go:25` — imports `mcpsrv`. Only reference is
  `magefile.go:169` `mcpsrv.Create(root, rec.Section, "", rec.Data)` →
  `ops.Create`. Swap import.

**mcpsrv's own files after the move:**

- `$ROOT/internal/mcpsrv/tools.go` — needs new import of
  `github.com/evanmschultz/ta/internal/ops`. Every unqualified symbol
  reference becomes `ops.<Symbol>`:
  - `tools.go:219` — `Get(path, section, fields)` → `ops.Get`.
  - `tools.go:236` — `ListSections(path, scope)` → `ops.ListSections`.
  - `tools.go:257` — `Create(path, section, pathHint, data)` →
    `ops.Create`.
  - `tools.go:280` — `Update(path, section, data)` → `ops.Update`.
  - `tools.go:299` — `Delete(path, section)` → `ops.Delete`.
  - `tools.go:349` — `Search(path, scope, match, queryStr, field)` →
    `ops.Search`.
  - `tools.go:414` — `schema.MetaSchemaPath` (unchanged, stays on
    schema).
  - `tools.go:424` — `resolveFromProjectDir(path)` →
    `ops.ResolveProject` (the lower-case helper `resolveFromProjectDir`
    vanishes during the move; it was always just a wrapper over the
    cache). `tools.go:424`'s direct call becomes
    `ops.ResolveProject(path)`.
  - `tools.go:498` — `MutateSchema(...)` → `ops.MutateSchema`.
  - `tools.go:378-384` (`validationOrPlainError` body) uses
    `errors.As(err, &vErr)` on `*schema.ValidationError` — unaffected
    (no mcpsrv-local error dependency).
  - Unqualified refs to `ErrRecordExists`, `ErrRecordNotFound`,
    `ErrFileNotFound`, `ErrAmbiguousDelete`, `ErrCannotClearRequired`
    etc. — if any, become `ops.<Sentinel>`. Current
    `tools.go` does not reference these sentinels directly (handlers
    just surface `err.Error()` text — see `tools.go:221, 238, 258,
    282, 300, 351, 385`). **Zero** sentinel rewires in tools.go.
  - Unqualified refs to `validationPath` (the tools.go local helper at
    `tools.go:585`) become `ops.ValidationPath` (if exported) or stay
    private within ops; tools.go callers must route through the new
    export. **However**: the only callers of `validationPath` are
    `ops.go:157` and `ops.go:303` — both inside the move. So
    `validationPath` stays unexported in `ops`, and tools.go stops
    seeing it. No tools.go change required for this helper.
  - Same reasoning for `tomlRelPathForFields`, `spliceOut`,
    `readFileIfExists`: all four helpers' callers are inside `ops.go`,
    so they become unexported ops-internal helpers.
- `$ROOT/internal/mcpsrv/server.go:56` — currently calls
  `defaultCache.Resolve(cfg.ProjectPath)` on a package-local cache.
  After [B0], `defaultCache` lives in `ops`; the idiomatic swap is
  `ops.ResolveProject(cfg.ProjectPath)` (which funnels through
  `ops.defaultCache.Resolve` internally, same pre-warm semantics).
  Add import of `internal/ops`.

**Within `mcpsrv_test` (server_test.go):**

- `server_test.go:116, 118, 151, 152` — `mcpsrv.ResetDefaultCacheForTest`
  → `ops.ResetDefaultCacheForTest`. The cache-reset hook is now owned
  by ops.
- `server_test.go:1170` — `mcpsrv.ResolveProject(fx.projectRoot)` →
  `ops.ResolveProject`.
- Add import of `internal/ops`. `mcpsrv` import stays for `New`,
  `Config`, `MCPServer()`.

### 1.4 Ordering constraints within [B0]

Per §12.17.5.1 Round 3 the plan calls for a single atomic commit. Trace
the dependency graph of the move to confirm one commit is feasible:

- `ops.go`, `backend.go`, `cache.go`, `errors.go`, `fields.go`,
  `schema_mutate.go` form an internally-coherent package cluster; they
  already share a set of unexported helpers and the `defaultCache`
  singleton. Move them together, flip the package decl, done.
- The four tools.go helpers (`spliceOut`, `readFileIfExists`,
  `validationPath`, `tomlRelPathForFields`) must land in ops before
  the move compiles cleanly, because ops.go already calls them (today
  it works because tools.go is in the same package). Move them in the
  same commit.
- server.go + tools.go in the remaining `mcpsrv` package need to
  import `internal/ops` to compile after the move. Must land in the
  same commit.
- Every cmd/ta caller (commands.go, commands_test.go) needs the new
  import; magefile.go same.
- mcpsrv_test/server_test.go + cache_test.go + dogfood_test.go are
  tests — if any of the three stays in mcpsrv_test referencing ops
  symbols, it needs the ops import. cache_test.go and
  dogfood_test.go MOVE to internal/ops/, flipping their test-package
  decl. server_test.go stays but gains `ops` import.

**Single atomic commit is the right target.** Incremental sub-steps
("move ops.go only, keep tools.go talking to the old ops") do not
compile: `ops.go` calls `tools.go`-resident helpers
(`validationPath` etc.) and vice versa. Any partial move leaves
dangling refs.

**Exception — pre-move preparatory commit (optional, not required):**
move `validationPath`, `tomlRelPathForFields`, `spliceOut`,
`readFileIfExists` from `tools.go` into `ops.go` *within the current
mcpsrv package* first, so `tools.go` stops owning domain helpers. That
prep commit is semantic-neutral and makes the big move cleaner
(tools.go becomes pure MCP glue in one pass). Optional — flag in the
plan, don't require it.

### 1.5 Risks

- **Circular imports.** Post-[B0] the dep chain is `cmd/ta → ops;
  mcpsrv → ops; cmd/ta → mcpsrv`. No cycles. `ops` depends on
  `config, db, schema, backend/toml, backend/md, record, search`
  (same as today's `mcpsrv`). `mcpsrv` depends on `ops` + `mcp-go`.
  `ops` never imports `mcp-go`. Verify with `go list -deps` post-move.
- **Unexported helpers that cross the boundary.** Four helpers
  (§1.1) flagged and routed. No others detected in the enumeration
  above.
- **Test-helper re-export.** The export_test.go hooks use internal
  access (lowercase `defaultCache`). When export_test.go moves to ops,
  the hooks see ops-internal state directly — semantic parity
  preserved. If any `mcpsrv_test` file currently relies on
  `mcpsrv.SwapDefaultCacheLoaderForTest` or
  `mcpsrv.DefaultResolveUncachedForTest`, they need to reimport ops.
  Enumeration:
  `$ROOT/internal/mcpsrv/cache_test.go:59, 67` — moves with the file
  to `ops_test`.
  `$ROOT/internal/mcpsrv/server_test.go` — no refs to those hooks
  (verified via `rg "SwapDefaultCacheLoaderForTest|DefaultResolveUncachedForTest"
  internal/mcpsrv/server_test.go`; zero hits).
- **Schema import cycle risk on `validationPath`.** `validationPath`
  uses `schema.Registry`; `schema` does not depend on mcpsrv. Safe.
- **Dogfood test name collision.** `internal/search/dogfood_test.go`
  exists; adding `internal/ops/dogfood_test.go` is fine (different
  dirs). No collision.
- **Stale comments.** Multiple comments in the search package and
  others (`internal/search/search.go:111, 237, 347, 490, 547`,
  `internal/search/errors.go:24`, `internal/search/doc.go:10`,
  `internal/backend/md/layout.go:11, 23`, `internal/render/doc.go:9`,
  `internal/templates/templates.go:8`, `cmd/ta/commands.go:163, 725`)
  reference `mcpsrv`. These are **documentation / comment drift**, not
  blocking compile issues. Out of scope for the mechanical [B0] pass;
  track as follow-up or allow organic cleanup. Flagging here so plan
  QA falsification doesn't treat them as planning blind spots.
- **`doc.go` staleness.** `internal/mcpsrv/doc.go:1-4` already
  mis-describes the package (mentions `upsert`, claims four tools).
  Refresh opportunistically; not a correctness risk.
- **`resolveFromProjectDir` disappearing.** The old internal helper at
  `ops.go:28` exists purely as a wrapper over `defaultCache.Resolve`.
  After [B0], move makes it redundant with `ResolveProject`; the
  cleanest shape is to delete the lower-case wrapper and have the
  (formerly mcpsrv, now ops) internal callers call `defaultCache.Resolve`
  directly where they had called `resolveFromProjectDir`. Semantic-
  neutral, avoids two identical names in the package. Flag for the
  builder — 1-commit decision.

### 1.6 Effort estimate

- **File moves**: ~1525 LoC net moved (sum of the six full-file moves
  plus ~120 LoC pulled from tools.go). Package-decl flip on every file.
- **Import rewires**: 18 call sites in `cmd/ta/commands.go`, 2 in
  `cmd/ta/commands_test.go`, 1 in `magefile.go`, ~12 in
  `internal/mcpsrv/tools.go`, 1 in `internal/mcpsrv/server.go`, 5 in
  `internal/mcpsrv/server_test.go`. Total ≈ 40 line-edits for
  rewiring.
- **Test moves**: 2 files (cache_test, dogfood_test) — `package
  mcpsrv_test` → `package ops_test`, flip import path.
- **Single builder, one commit**. No parallelism — the changes are
  tightly coupled.
- **QA pair** must verify: (a) `mage check` green, (b) every MCP tool
  still handles round-trip via `internal/mcpsrv/server_test.go` (MCP
  protocol surface unchanged), (c) no new cycles via
  `go list -deps ./internal/...`, (d) no orphan refs (`rg "mcpsrv\."
  internal/ops/`).

---

## 2. [A2.1] impact — move `list-sections` `limit`/`all` into the endpoint

Depends on [B0] landing first. All references below use post-[B0] paths.

### 2.1 Files to modify

- `$ROOT/internal/ops/ops.go` — `ListSections` signature change.
- `$ROOT/internal/search/search.go` — `Query` gains limit/all fields +
  early-exit in `Run`'s outer loop.
- `$ROOT/internal/mcpsrv/tools.go` — `listSectionsTool` + `handleListSections`.
- `$ROOT/cmd/ta/commands.go` — `newListSectionsCmd` — drop post-fetch
  `sections[:limit]` slice, pass flag values through to `ops.ListSections`.
- Test files: see §2.6.

### 2.2 New signatures

**Today** (`internal/mcpsrv/ops.go:458`):

```go
func ListSections(path, scope string) ([]string, error)
```

**Post-[A2.1]**:

```go
func ListSections(path, scope string, limit int, all bool) ([]string, error)
```

Contract:

- `all == true` → return every address in scope (no cap). `limit` is
  ignored.
- `all == false && limit > 0` → return at most `limit` addresses;
  early-exit the scan once `limit` is reached.
- `all == false && limit <= 0` → today's CLI default is `limit == 10`
  applied CLI-side; the endpoint is now authoritative. Decision for
  the endpoint: `limit <= 0 && all == false` is treated as "apply
  default". Spec §3.2 and §3.7 both say "default 10". Apply it at the
  endpoint. CLI and MCP adapters pass their incoming values through
  verbatim; if `limit == 0 && all == false` the endpoint substitutes
  10. Document in the endpoint docstring.
- `all == true && limit > 0` — mutex at the adapter level (CLI
  `MarkFlagsMutuallyExclusive` at `commands.go:275`, MCP JSON-schema
  hint); endpoint-level behavior: `all` wins, `limit` ignored. Log
  nothing — adapters are the gate.

### 2.3 Internal plumbing — `internal/search/search.go`

`ListSections` is implemented as a zero-filter search (see
`internal/mcpsrv/ops.go:458` comment block). It routes through
`search.Run`, which today has no cap. To preserve that routing and
get endpoint-level early-exit, extend `search.Query` + `Run`:

**`$ROOT/internal/search/search.go:37-43` — add fields**:

```go
type Query struct {
    Path  string
    Scope string
    Match map[string]any
    Query *regexp.Regexp
    Field string
    Limit int  // new — 0 means no cap
    All   bool // new — true means "return every hit, ignore Limit"
}
```

**`Run` body** (`search.go:57-108`) — add early-exit in the outer and
inner loop bodies. Reference points:

- `search.go:85` `for _, dbName := range plan.dbOrder`
- `search.go:91` `for _, inst := range instances`
- `search.go:101` `results, err := searchFile(dbDecl, inst, plan, q)`
- `search.go:105` `out = append(out, results...)`

After `out = append(out, results...)` at `search.go:105`, add:

```go
if !q.All && q.Limit > 0 && len(out) >= q.Limit {
    return out[:q.Limit], nil
}
```

Pushing the cap INTO `searchFile` is a second-order optimization (stop
walking one file mid-iteration); for [A2.1] the outer-loop check is
sufficient because the perf regression the plan cites ("walks every
record in scope") manifests at the record-walk level — an early-exit
after each file boundary already turns O(total records) into O(until
first cap-cross). The planner can flag "per-record early-exit inside
searchFile" as a follow-up if a post-ship benchmark shows the
file-boundary cap is insufficient.

### 2.4 MCP tool declaration change

`$ROOT/internal/mcpsrv/tools.go:34-46` — `listSectionsTool()`. Add two
new params matching §3.2's spec:

```go
func listSectionsTool() mcp.Tool {
    return mcp.NewTool(
        "list_sections",
        mcp.WithDescription(
            "Enumerate record addresses under a scope. Returns full "+
            "project-level dotted addresses in file-parse order, ready to "+
            "pass back to get/update/delete. Defaults to 10 addresses; "+
            "pass all=true or a larger limit to widen.",
        ),
        mcp.WithString("path", mcp.Required(), mcp.Description("Project directory (absolute).")),
        mcp.WithString(
            "scope",
            mcp.Description("Optional: '<db>' | '<db>.<type>' | '<db>.<instance>' | '<db>.<type>.<id-prefix>' | '<db>.<instance>.<type>(.<id-prefix>)?'. Default = whole project."),
        ),
        mcp.WithNumber("limit", mcp.Description("Optional cap. Default 10. Mutually exclusive with all=true.")),
        mcp.WithBoolean("all", mcp.Description("Optional. When true, return every address in scope; ignores limit.")),
    )
}
```

`handleListSections` (`tools.go:229`) parses the two new params:

```go
func handleListSections(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
    _ = ctx
    path, err := req.RequireString("path")
    if err != nil {
        return mcp.NewToolResultError(fmt.Sprintf("invalid path arg: %v", err)), nil
    }
    scope := req.GetString("scope", "")
    // limit/all per §3.2. Default limit 10 at the endpoint; adapters pass through.
    limit := int(req.GetFloat("limit", 0))
    all := req.GetBool("all", false)
    if limit > 0 && all {
        return mcp.NewToolResultError("pass either limit or all, not both"), nil
    }
    sections, err := ops.ListSections(path, scope, limit, all)
    if err != nil {
        return mcp.NewToolResultError(err.Error()), nil
    }
    // ... rest unchanged
}
```

Verify `req.GetFloat` / `req.GetBool` API via mark3labs/mcp-go docs —
the equivalent helpers are already used on `tools.go:335-337` for
search's `query`/`field`/`scope`, but those are all strings; numeric
and boolean accessors need confirmation against the SDK. Builder's
gate.

### 2.5 CLI rewire

`$ROOT/cmd/ta/commands.go:217-278` — `newListSectionsCmd`. The flags
already exist (`commands.go:273-275`). Current code slices post-fetch
at `commands.go:253-255`:

```go
sections, err := mcpsrv.ListSections(path, resolvedScope)
if err != nil {
    return err
}
if !all && limit > 0 && len(sections) > limit {
    sections = sections[:limit]
}
```

**Post-[A2.1]**:

```go
sections, err := ops.ListSections(path, resolvedScope, limit, all)
if err != nil {
    return err
}
// post-fetch slice removed — endpoint owns the cap
```

Drop the `[:limit]` block. The `cobra.MarkFlagsMutuallyExclusive("limit",
"all")` at `commands.go:275` stays (adapter-level guard is correct;
see §2.2).

### 2.6 Test impact

Every test that exercises `mcpsrv.ListSections` (now `ops.ListSections`)
changes to the new signature:

- `$ROOT/internal/ops/ops.go` — no direct ListSections tests today
  (the only coverage is through the MCP tool in server_test.go).
  Post-[A2.1] add a unit test in
  `$ROOT/internal/ops/ops_test.go` (or a new
  `$ROOT/internal/ops/list_sections_test.go`) that proves:
  - `limit = 0, all = false` applies the default cap (10).
  - `limit = 5, all = false` returns 5.
  - `limit = 0, all = true` returns all.
  - `limit = 3, all = true` is accepted by the endpoint, returns all
    (adapter-level mutex covers the UX; endpoint doesn't reject so
    library callers see permissive behavior).
  - Early-exit: construct a scope with >>cap records; assert the
    backing file read count is bounded by the cap-cross (or at minimum
    `len(result) == cap`).

- `$ROOT/internal/mcpsrv/server_test.go` — extend
  `TestListSectionsProjectDirAndScope` (`server_test.go:1333-1360`)
  and `TestListSectionsMultiInstanceAddresses` (`server_test.go:1365-1419`)
  with limit/all cases. New tests:
  - `TestListSectionsDefaultLimitOfTen` — seed 15 records, call
    without `limit`/`all`, expect 10.
  - `TestListSectionsAllReturnsEveryRecord` — seed 15, call with
    `all: true`, expect 15.
  - `TestListSectionsLimitMutexWithAll` — call with both
    `{"limit": 5, "all": true}`, expect tool-level error.

- `$ROOT/cmd/ta/commands_test.go` — there are existing list-sections
  CLI tests (verify via `rg "list-sections\|ListSections\|newListSectionsCmd"
  cmd/ta/commands_test.go` before writing). Add regression for the
  removed post-fetch slice: if today a CLI test checks behavior when
  records > limit, that test still passes post-change (the endpoint
  now does the slicing) — but add one test that proves early-exit by
  seeding >>limit records and asserting the cap is honored.

### 2.7 Risks

- **Test churn from added function params.** Every caller gets two
  new args; mechanical (enumerated above).
- **Default-cap inversion.** Today the CLI sets `limit = 10` via
  `cmd/ta/commands.go:273` `IntVarP` default; the MCP path has
  **no cap today** (`tools.go:236` `ListSections(path, scope)` —
  uncapped). Post-[A2.1] both paths get the cap. This is a behavior
  change for MCP callers — the MCP agent that today relies on
  `list_sections` returning everything in scope will now get only 10
  unless it passes `all=true`. **This is the F1 MCP asymmetry fix the
  plan explicitly calls out**; it is intentional, not a regression.
  Release-note material.
- **search.Query shape breakage.** `Limit`/`All` fields on
  `search.Query` are additive (zero values preserve current
  behavior). Existing `search.Run` callers in `internal/search/search_test.go`
  (21k file) and `internal/search/dogfood_test.go` need no change if
  they don't set the new fields. Verify via `rg "search\.Query\{"
  internal/ /cmd/` — any literal constructs that will need `Limit`/`All`
  get zero values automatically.
- **Endpoint-level limit <= 0 substitution.** If a library caller
  truly wants "0 hits, no cap substitution", they have no escape
  today. Three options: (a) accept this as intentional (10-default
  matches spec), (b) use `all == false && limit == -1` sentinel for
  "no cap", (c) require callers to always pass either `limit >= 1`
  or `all == true`. Recommend (a) per plan spec.

### 2.8 Effort estimate

- ~20 LoC in `internal/search/search.go` (Query fields + early-exit).
- ~10 LoC in `internal/ops/ops.go` (signature + plumbing).
- ~15 LoC in `internal/mcpsrv/tools.go` (tool decl + handler params).
- ~3 LoC removed in `cmd/ta/commands.go`.
- ~80-120 LoC new test coverage (3-4 new tests at ~30 LoC each).
- Total: ~150 LoC, mostly tests.

---

## 3. [A2.2] impact — add `limit`/`all` to `search` endpoint + MCP tool + CLI

Depends on [B0]. Same shape as [A2.1] but on the `search` endpoint;
§3.7 already specs the signature.

### 3.1 Files to modify

- `$ROOT/internal/ops/ops.go` — `Search` signature change.
- `$ROOT/internal/search/search.go` — same `Query` fields (shared with
  A2.1) + same early-exit. A2.1 and A2.2 SHARE the `search.Query`
  additions and the `search.Run` early-exit. If A2.1 lands first, A2.2
  reuses the plumbing; if they land in parallel, they touch the same
  file (`internal/search/search.go`) and must be **blocked_by** each
  other. Recommend: land A2.1 first (gets the search.go change in),
  then A2.2 inherits. Alternative: one builder owns BOTH and lands
  them as sibling commits.
- `$ROOT/internal/mcpsrv/tools.go` — `searchTool` + `handleSearch`.
- `$ROOT/cmd/ta/commands.go` — `newSearchCmd` gets `--limit`/`--all`
  flags.
- Test files: see §3.5.

### 3.2 New signatures

**Today** (`internal/mcpsrv/ops.go:473`):

```go
func Search(path, scope string, match map[string]any, queryRegex, field string) ([]SearchHit, error)
```

**Post-[A2.2]**:

```go
func Search(path, scope string, match map[string]any, queryRegex, field string, limit int, all bool) ([]SearchHit, error)
```

Same contract as `ops.ListSections` (§2.2): `limit` default 10 at the
endpoint; `all == true` wins over `limit`; early-exit required.

### 3.3 MCP tool declaration change

`$ROOT/internal/mcpsrv/tools.go:97-122` — `searchTool()`. Add two new
params and update the description to cite §3.7's cap semantics:

```go
mcp.WithNumber("limit", mcp.Description("Optional cap on returned hits. Default 10. Mutex with all.")),
mcp.WithBoolean("all", mcp.Description("Optional. When true, return every hit in scope; ignores limit.")),
```

Update `handleSearch` (`tools.go:329`) to parse the two params and
forward:

```go
limit := int(req.GetFloat("limit", 0))
all := req.GetBool("all", false)
if limit > 0 && all {
    return mcp.NewToolResultError("pass either limit or all, not both"), nil
}
hits, err := ops.Search(path, scope, match, queryStr, field, limit, all)
```

### 3.4 CLI rewire

`$ROOT/cmd/ta/commands.go:530-579` — `newSearchCmd`. Add flags
analogous to list-sections (`commands.go:273-275`):

```go
var limit int
var all bool
// ...
cmd.Flags().IntVarP(&limit, "limit", "n", 10, "cap the hit count at N (default 10)")
cmd.Flags().BoolVar(&all, "all", false, "return every match (disables --limit)")
cmd.MarkFlagsMutuallyExclusive("limit", "all")
```

And thread them through the `ops.Search` call at `commands.go:562`:

```go
hits, err := ops.Search(path, scope, match, query, field, limit, all)
```

Update the cmd's Long description + Example strings to document
`--limit`/`--all`.

### 3.5 Test impact

- `$ROOT/internal/ops/ops_test.go` — add unit tests for
  `ops.Search` with limit/all:
  - default-10 behavior with >>10 hits.
  - `all=true` returns every hit.
  - early-exit path (file-boundary cap).
  - mutex enforced at caller layer (endpoint accepts both, adapter
    rejects).
- `$ROOT/internal/mcpsrv/server_test.go` — add MCP-surface tests:
  - `TestSearchDefaultLimit` — seed > 10 matching records, call
    without limit/all, expect 10.
  - `TestSearchAllReturnsEveryHit` — seed > 10, pass `all: true`,
    expect all.
  - `TestSearchLimitMutexWithAll` — pass both, expect error.
  - Extend existing `TestSearchReturnsHits` (`server_test.go:1475`),
    `TestSearchCrossInstanceUnion` (`server_test.go:1541`) to be
    aware of the default cap (they currently assert ≤ 2 hits so they
    are unaffected by a 10-default — verify).
- `$ROOT/internal/search/search_test.go` — if it has unit tests on
  `search.Run` directly, add cases with `Limit > 0` and `All = true`.
- `$ROOT/cmd/ta/commands_test.go` — add
  `TestSearchCmdLimitCapsResults`, `TestSearchCmdAllReturnsEverything`,
  `TestSearchCmdMutexFlags`.
- `$ROOT/internal/search/dogfood_test.go` — no action (no limit
  assertions; the dogfood hits are below the default cap).

### 3.6 Risks

- **Shared `search.Run` change.** If A2.1 and A2.2 land in parallel,
  they edit the same search.go hunk. The plan (§12.17.5.1 Round 4)
  says "parallel-safe if the three builders each own distinct
  functions." The distinct-functions gate holds at the `ops.*` level
  (ListSections vs Search) but fails at `search.Query` — both add
  fields to the same struct and both expect the same early-exit in
  `Run`. **Resolution**: declare A2.1 as blocked_by the Query change
  and land the Query shape change atomically with A2.1 (or extract a
  prep commit that adds `Limit`/`All` + early-exit to `search.Run`
  before A2.1 and A2.2 fan out). The plan's "parallel-safe" claim
  needs this caveat; flag in planning.
- **Default cap is a user-facing behavior change.** MCP `search` today
  is uncapped; post-[A2.2] it caps at 10 by default. Same release-
  note caveat as [A2.1].
- **Empty regex vs empty limit.** Today `handleSearch` at
  `tools.go:336` reads `queryStr` as empty when omitted; the new
  `limit`/`all` params follow the same pattern. No conflict.
- **Existing `search_test.go` fixtures.** 21k LoC file. Most tests
  don't care about cap; verify none inadvertently trip the default-10
  gate. If any test seeds > 10 records without passing `all=true`,
  it becomes a regression. Audit required.

### 3.7 Effort estimate

- ~15 LoC in `internal/ops/ops.go` (signature + plumbing).
- ~15 LoC in `internal/mcpsrv/tools.go`.
- ~10 LoC in `cmd/ta/commands.go` (flag vars + pass-through).
- ~0 LoC in `internal/search/search.go` IF A2.1 landed first.
  Otherwise ~20 LoC (Query fields + early-exit).
- ~100-150 LoC new test coverage.
- Total: ~140-200 LoC.

---

## 4. Parity audit and order constraints

### 4.1 §14.2.1 CLI-only template management — consistency audit

§14.2.1 (`docs/PLAN.md:1300-1309`) justifies CLI-only `ta template
list|show|save|apply|delete` via four boundaries:

- **Scope boundary.** MCP = project-scoped; templates = `~/.ta/` =
  user-global. Self-consistent — an MCP session cannot resolve
  `~/.ta/` cleanly without crossing the session's handshake-fixed cwd
  (`cmd/ta/main.go:213-220` pins `cwd = project root`).
- **Agency boundary.** Agents operate per-project; templates are
  user-ergonomics. Internally consistent with the "agents shouldn't
  edit `~/.bashrc`" analogy.
- **Temporal boundary.** Templates are pre-project; MCP server exists
  post-project. Consistent with the §14.2 "firewall":
  `internal/templates/templates.go:8` comment block notes
  "depends on stdlib + `internal/schema/` only. It does not import
  `internal/config/Resolve`. Runtime consumers never import
  `internal/templates/`." — the four-boundary justification aligns
  with the import-level firewall.
- **Trust boundary.** Cross-project side effects. Internally
  consistent.

**Read-only `list`/`show` caveat** (§14.2.1 last paragraph) weakens
the boundary but explicitly defers re-evaluation. Self-consistent.

**Audit verdict**: internally consistent. No code-work implied. Flag
only if [B0] or [A2.*] inadvertently implicate templates (they don't —
`internal/templates/` is not in the [B0]/[A2.*] blast radius).

### 4.2 Cross-item ordering

**[B0] is a hard prerequisite** for [A2.1] and [A2.2] — the plan
(§12.17.5 [B0] bullet, `docs/PLAN.md:1182`) and
§12.17.5.1 Round 3 both call this out.

**[A2.1] and [A2.2] parallelism caveat (new finding this pass):**
Round 4 (`docs/PLAN.md:1215`) says "Parallel-safe if the three
builders each own distinct functions." The A2.1/A2.2 pair share an
edit to `internal/search/search.go`:

- Both add `Limit int` and `All bool` to `search.Query`
  (`internal/search/search.go:37-43`).
- Both add the early-exit block in `search.Run`
  (`internal/search/search.go:85-107` cluster).

Three mitigation paths (flagged for plan):

1. **Serialize A2.1 and A2.2.** A2.1 lands first and drops the
   `search.Query` / `search.Run` changes; A2.2 inherits the plumbing
   and only rewires the MCP tool + CLI. Simplest; extends Round 4 to
   two mini-rounds.
2. **Pre-commit the shared search.go change.** Land a prep commit
   that adds `Limit`/`All` + early-exit to `search.Run`; then fan out
   A2.1 / A2.2 / B2 in parallel. Cleanest for parallelism; adds one
   commit.
3. **Single builder owns both.** A2.1+A2.2 one builder, B2 another
   builder in parallel. Matches the plan's 3-builder claim if you
   squint: effectively two distinct edit scopes
   (list-sections+search ops vs get-scope-expansion).

Recommend path 2 or 3 — both preserve the parallel Round 4 shape
without lying about the edit scope. Plan text should acknowledge the
`search.Query` shared edit explicitly.

### 4.3 `search.Query` `All` vs existing `all`-style flag names

Go convention — `search.Query.All bool` reads fine. No naming
collision in `internal/search/search.go` today (`rg "\\.All\\b"
internal/search/search.go` returns zero).

### 4.4 Adapter-level mutex enforcement

CLI uses `cobra.MarkFlagsMutuallyExclusive("limit", "all")` at
`commands.go:275` (list-sections) — proven pattern. A2.2 uses the
same. MCP has no native mutex primitive; enforcement is
handler-level (explicit error string, see §2.4 / §3.3 example). The
endpoint layer in `internal/ops/` is permissive: `all == true`
shadows `limit`. This is the right factoring per §6a.2 — adapters
own UX-level invariants, endpoints own semantics.

---

## TL;DR

- **T1**: [B0] is a six-file wholesale move (`ops.go`, `backend.go`,
  `cache.go`, `errors.go`, `fields.go`, `schema_mutate.go`) from
  `internal/mcpsrv/` to a new `internal/ops/` package, plus four
  orphan helpers pulled from `tools.go` (`spliceOut`, `readFileIfExists`,
  `validationPath`, `tomlRelPathForFields`) that currently live with
  the MCP handlers but only serve domain code. `server.go` and a
  trimmed `tools.go` stay in `mcpsrv`. 28 exported symbols rename from
  `mcpsrv.*` → `ops.*` (three functions, three types, thirteen error
  sentinels, four test hooks, `ResolveProject`, `MutateSchema`, plus
  the eight CRUD/search/list entrypoints). Nineteen call sites rewire
  across `cmd/ta/commands.go` (16 refs), `cmd/ta/commands_test.go` (2),
  `magefile.go` (1); `cmd/ta/main.go` unchanged (imports `mcpsrv.New`
  which stays). Test files `cache_test.go` and `dogfood_test.go` move
  (package `mcpsrv_test` → `ops_test`); `server_test.go` stays and
  gains an `ops` import for 5 symbol refs. Single atomic commit; no
  ordering split is clean. ~1525 LoC moved, ~40 rewire edits.

- **T2**: [A2.1] narrows to `ops.ListSections(path, scope string,
  limit int, all bool) ([]string, error)`. `search.Query` gains `Limit
  int` and `All bool` fields; `search.Run` gains an early-exit check
  after each file's results are appended. MCP tool `list_sections`
  gains `limit` (number) and `all` (boolean) params; handler parses,
  enforces mutex, forwards. CLI drops the post-fetch slice at
  `commands.go:253-255` and passes its existing flag values through.
  User-visible: MCP's `list_sections` gains a default-10 cap (it is
  uncapped today). ~150 LoC including tests.

- **T3**: [A2.2] is structurally identical to [A2.1] but on
  `ops.Search(…, limit int, all bool)`. The `search.Query` +
  `search.Run` changes are SHARED with [A2.1] — if landed in parallel
  they touch the same hunk of `internal/search/search.go:37-107`. Plan
  should serialize the two (A2.1 → A2.2) or extract a prep commit that
  lands the shared search.go shape first. MCP tool gains `limit`/`all`;
  CLI gains `--limit`/`-n`/`--all` with cobra mutex. Same default-10
  cap is a user-visible MCP behavior change. ~140-200 LoC including
  tests.

- **T4**: §14.2.1 template-CLI-only justification is internally
  consistent. Scope/agency/temporal/trust boundaries line up with the
  import-level firewall in `internal/templates/templates.go`. No code
  implications in the [B0]/[A2.*] blast radius.

- **T5**: Biggest risks: (a) stale mcpsrv-mentioning comments in
  `internal/search/`, `internal/backend/md/`, `internal/render/`,
  `internal/templates/`, `cmd/ta/commands.go` — documentation drift,
  not compile risk, but QA falsification will call it out if we don't
  flag as out-of-scope for mechanical [B0]; (b) the default-10 cap on
  MCP `list_sections` / `search` is a real behavior change for agents
  that today depend on uncapped results — release-note material per
  the plan's F1-asymmetry-fix framing; (c) the A2.1/A2.2 shared edit
  in `search.Query` / `search.Run` breaks the Round 4 "three builders
  in parallel" claim unless we pre-commit the shared shape or
  serialize the pair.

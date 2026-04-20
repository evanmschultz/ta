# `ta` MVP Plan

Target: MCP server that exposes `get` / `list_sections` / `upsert` over a directory of schema-validated TOML files, per `ta.md`.

Everything in this plan is sized for a single worktree (`main/`) on a bare repo. No sibling worktrees, no parallel lanes. MVP = the three tools work, schemas validate, tree-sitter splices preserve human content.

---

## 1. Goal and scope

**In scope for MVP (single `main/` worktree):**

- MCP server over stdio, three tools: `get`, `list_sections`, `upsert`.
- Schema resolution by walking up from the file path arg → project `.ta/config.toml` → `~/.ta/config.toml`.
- Tree-sitter-based parse + surgical byte-splice for upsert (comments outside touched section preserved verbatim).
- Canonical emission for the upserted section only.
- Structured validation errors returned to the agent (required / type / enum).
- Atomic writes (temp-file + rename).
- `mage`-driven build/test/lint gates.
- `go doc`-driven integration of `laslig` for CLI-surface output (`--version`, `--help`, startup-stderr banner, pre-transport error reporting).

**Out of scope (deferred, per ta.md YAGNI list):**

- Multi-section transactions, file watching, diff/merge, taplo-style formatting, optional schemas, in-section comment preservation, `[[array_of_tables]]` semantics (noted as open question).

---

## 2. Dependency pins (newest as of 2026-04-19)

Verified via `gh api` today. All pinned to concrete versions in `go.mod`.

| Module | Version | Role |
|---|---|---|
| Go toolchain | `1.26.2` | verified via `go version` on this machine |
| `github.com/magefile/mage` | `v1.17.1` | build automation (installed as a dev tool, not a module dep) |
| `github.com/odvcencio/gotreesitter` | `v0.14.0` | pure-Go tree-sitter runtime; provides `grammars` subpkg with TOML grammar (`grammars/toml_register.go`, `toml_lexer.go`) |
| `github.com/mark3labs/mcp-go` | `v0.48.0` | MCP SDK (stdio transport, tool registration, structured errors) |
| `github.com/pelletier/go-toml/v2` | `v2.3.0` | schema config parser only; user TOML never touches this |
| `github.com/evanmschultz/laslig` | `v0.2.4` | human-facing CLI output (sections, notices, printer) — used on `--help` / `--version` / startup errors, NOT on MCP responses |
| stdlib | — | `os`, `path/filepath`, `context`, `fmt`, `errors`, `io`, `encoding/json` |

**`go doc` gate:** before wiring `laslig`, `mcp-go`, or `gotreesitter`, run `go doc <pkg>` and `go doc <pkg>.<Symbol>` to confirm exact API surfaces (constructors, handler signatures, error shapes). Do not code against remembered APIs.

---

## 3. Phase 0 — Repo and worktree bootstrap

Preconditions: this directory currently contains `ta.pdf`, `ta.md`, `PLAN.md` and is **not** a git repo.

Steps (confirm each with user before destructive moves):

1. **Park existing docs.** Move `ta.pdf`, `ta.md`, `PLAN.md` to `/Users/evanschultz/Documents/Code/hylla/ta-staging/` (outside the target).
2. **Create the remote.**
   ```
   gh api user --jq .login               # confirm owner
   gh repo create evanmschultz/ta \
     --public \
     --description "MCP server exposing TOML files as a schema-validated, agent-accessible database" \
     --disable-wiki
   ```
3. **Clone as bare into this directory.** The current path becomes the bare repo.
   ```
   rmdir /Users/evanschultz/Documents/Code/hylla/ta
   git clone --bare git@github.com:evanmschultz/ta.git /Users/evanschultz/Documents/Code/hylla/ta
   ```
4. **Add the `main` worktree.**
   ```
   cd /Users/evanschultz/Documents/Code/hylla/ta
   git worktree add -b main main
   ```
5. **Re-home docs into the worktree.** Move `ta.pdf`, `ta.md`, `PLAN.md` from staging into `main/docs/` (design record) and delete the staging dir.
6. **First commit in `main/`:** `.gitignore`, `LICENSE` (MIT), `README.md` stub, `docs/ta.md`, `docs/ta.pdf`, `docs/PLAN.md`. Push `main`.

Gate: `git -C main status` clean, `git push` succeeds, `gh repo view evanmschultz/ta` shows description.

---

## 4. Phase 1 — Module scaffold

Working directory: `/Users/evanschultz/Documents/Code/hylla/ta/main/`.

1. `go mod init github.com/evanmschultz/ta`
2. Pin toolchain in `go.mod`: `go 1.26.2` (exact, no `toolchain` directive unless a dep forces it).
3. `go get` the pinned versions from §2. Verify with `go list -m all`.
4. Install mage as a dev tool, not a module dep: `go install github.com/magefile/mage@v1.17.1`.
5. Write `magefile.go` with `//go:build mage` guard and targets listed in §7.
6. `go doc` each third-party dep and save one-line summaries of the symbols we actually plan to use into `docs/api-notes.md`. This is the primary guard against hallucinated APIs.

Gate: `mage check` (see §7) passes against an empty package set.

---

## 5. Architecture

### 5.1 Package layout

```
main/
├── cmd/
│   └── ta/
│       └── main.go              # entrypoint — flag parsing, laslig for CLI surfaces, hand off to mcpsrv
├── internal/
│   ├── config/
│   │   ├── config.go            # walk-up resolution; load ~/.ta/config.toml + project override via go-toml
│   │   └── config_test.go
│   ├── schema/
│   │   ├── schema.go            # Schema, Field, Type types
│   │   ├── validate.go          # Validate(sectionPath string, data map[string]any) error
│   │   ├── error.go             # ValidationError — Unwrap/Errors accessor, structured to JSON for MCP
│   │   └── *_test.go
│   ├── tomlfile/
│   │   ├── parse.go             # tree-sitter parser wrapper; Section spans with byte ranges
│   │   ├── splice.go            # Upsert: byte-surgical replace of target section
│   │   ├── emit.go              # canonical TOML section emission for the upserted block only
│   │   ├── atomic.go            # WriteAtomic(path string, data []byte) — temp + fsync + rename
│   │   └── *_test.go
│   └── mcpsrv/
│       ├── server.go            # mcp-go server construction, tool registration
│       ├── tools.go             # Get / ListSections / Upsert handlers
│       └── tools_test.go
├── docs/
│   ├── ta.md                    # design doc
│   ├── ta.pdf
│   ├── PLAN.md                  # this file
│   └── api-notes.md             # go-doc-sourced API cheatsheet
├── magefile.go
├── go.mod
├── go.sum
├── .gitignore
├── LICENSE
└── README.md
```

Module path: `github.com/evanmschultz/ta`. Binary path: `./cmd/ta`.

### 5.2 Data flow (upsert, the load-bearing path)

```
MCP client
   │  {tool: "upsert", args: {path, section, data}}
   ▼
mcpsrv.Upsert(ctx, req)
   │
   ├──► config.Resolve(path) ─────► *schema.Registry   (walk-up from path)
   │
   ├──► schema.Validate(req.Section, req.Data) ──► ValidationError or nil
   │                                                 │
   │                                            fail ▼
   │                          mcp.NewToolResultError(structured JSON)
   │
   ├──► tomlfile.Parse(path) ──► *tomlfile.File       (tree-sitter CST + byte buffer)
   ├──► file.Splice(req.Section, emitCanonical(req.Data)) ──► []byte (new full buffer)
   ├──► tomlfile.WriteAtomic(path, newBuf)
   │
   └──► mcp.NewToolResultText(summary)
```

Everything outside the section's byte range passes through unchanged. That is the single invariant that `tomlfile.splice` must enforce and test.

### 5.3 Dependencies between packages

`cmd/ta` → `mcpsrv` → {`config`, `schema`, `tomlfile`}. No cycles. `schema` does not import `tomlfile`; validation operates on `map[string]any`, the format-neutral decoded form. Keeps the validator unit-testable without tree-sitter.

---

## 6. Go-idiomatic design notes

Decisions locked in before any code lands. If an implementation choice contradicts one of these, the plan is wrong — stop and revisit.

- **6.1 Package names.** Single lowercase words, no underscores, no stutter. `schema.Validate` not `validator.SchemaValidator.Validate`. `tomlfile` over `tomlio` (the `io` suffix mimics stdlib naming and is misleading for a CST-based package).
- **6.2 Errors.** `errors.New` for sentinels, `fmt.Errorf("...: %w", err)` for wrapping. `schema.ValidationError` is a concrete type exposing `Unwrap() []error` (Go 1.20+ joinable errors) and a `MarshalJSON` method so `mcpsrv` can hand the agent a structured payload — not an opaque string.
- **6.3 Context first.** Every exported function that does I/O or can be cancelled takes `ctx context.Context` as its first parameter. MCP handlers already receive one.
- **6.4 Interfaces defined by consumer.** `mcpsrv` declares any small interface it needs (e.g., `schemaResolver`) locally. `schema` exports concrete types. This keeps dependency direction clean and lets tests inject fakes without touching producer packages.
- **6.5 Zero values useful.** `schema.Schema{}` and `tomlfile.File{}` should be safe to zero-value where possible; constructors (`schema.Load`, `tomlfile.Parse`) return populated values.
- **6.6 No globals.** The MCP server is constructed in `cmd/ta/main.go`, passed its deps explicitly. No package-level state in `schema`, `tomlfile`, or `mcpsrv`. Parsers that should be reused (tree-sitter `Parser`) live on a struct field.
- **6.7 Functional options only where warranted.** `mcpsrv.New(cfg, opts ...Option)` is fine; nothing else needs options for MVP.
- **6.8 Small files, cohesive packages.** Every file in one package should plausibly need to know about every other file's types. Split when that stops being true.
- **6.9 Test package choice.** Use external test package (`package schema_test`) when testing public API; use internal (`package schema`) only when the test genuinely needs unexported access. Prefer table tests with `t.Run` subtests.
- **6.10 Atomic writes.** `os.CreateTemp(destDir, pattern)` → write → `f.Sync()` → `f.Close()` → `os.Rename`. `destDir` same filesystem as target; fall back to copy-and-truncate only if rename fails cross-device (not in MVP).
- **6.11 No CLI framework.** `flag` from stdlib is enough for `--help` / `--version`. laslig renders the output. `cobra`/`urfave-cli` would be YAGNI.
- **6.12 Logging.** Startup banner and pre-transport errors go to stderr via laslig's `Printer`. Once MCP transport is running, the protocol owns stdout — do not log anywhere the client might parse as RPC. Prefer structured error returns over logs.
- **6.13 Concurrency.** MCP tool handlers may be called concurrently (verify via `go doc mcp-go`). `schema.Schema` is read-only after load → safe. `tomlfile` operations take the file path and do full re-read → no shared mutable state. No mutex in MVP.
- **6.14 Race detector in CI.** `mage test` invokes `go test -race ./...`. Non-negotiable.
- **6.15 `errcheck` discipline.** No ignored errors, no `_ = f.Close()` without a reason comment. `defer f.Close()` in write paths pairs with an explicit `f.Sync()` before it.

---

## 7. Mage targets (build gates)

Authoritative via `mage -l`. Raw `go build`/`go test` is not used in this repo — always mage.

- `mage build` — `go build -o bin/ta ./cmd/ta`
- `mage install` — `go install ./cmd/ta`
- `mage test` — `go test -race -count=1 ./...`
- `mage cover` — `go test -race -coverprofile=coverage.out ./... && go tool cover -func=coverage.out`
- `mage vet` — `go vet ./...`
- `mage fmt` — `gofmt -s -w .` + `goimports -w .` (goimports installed if missing)
- `mage fmtcheck` — `gofmt -s -l .` must emit zero lines
- `mage tidy` — `go mod tidy` then fail if `go.mod`/`go.sum` changed
- `mage check` — composite gate: `fmtcheck` → `vet` → `test` → `tidy`. This is the pre-commit / pre-push gate.
- `mage clean` — remove `bin/` and `coverage.out`

---

## 8. Implementation phases (TDD order)

Each phase lands as its own commit (or small commit series). No phase claims done until `mage check` passes.

### 8.1 Phase 1 — Scaffold + doctests

Covered in §4. Exit criterion: `mage check` green on empty packages.

### 8.2 Phase 2 — `schema` package (no tree-sitter yet)

1. `schema.Schema`, `schema.Field`, `schema.Type` types.
2. `schema.Load(r io.Reader) (*Schema, error)` decoding schema config via `go-toml/v2` into the typed model.
3. `schema.Validate(sectionPath string, data map[string]any) error` — required / type / enum checks; returns `*ValidationError`.
4. `ValidationError.MarshalJSON` → the structured error shape from `ta.md` §Validation.

Tests: table-driven; cover every failure mode from the example error in `ta.md`. This is the most testable, most deterministic package — lean on it.

### 8.3 Phase 3 — `config` package

1. `config.Resolve(filePath string) (*schema.Schema, error)` walking from `filePath`'s directory upward until a `.ta/config.toml` is found, else `~/.ta/config.toml`, else a typed `ErrNoConfig`.
2. Caching is YAGNI for MVP — re-resolve per call, cheap.

Tests: use `t.TempDir()` for filesystem layouts; do not mock `os`.

### 8.4 Phase 4 — `tomlfile.parse`

1. Construct tree-sitter parser for TOML via `gotreesitter` + `grammars.TomlLanguage` (confirm exact symbol with `go doc`).
2. Walk the CST, record every `[section]` and `[[array_of_tables]]` header's byte range plus the byte range of the full section body (header + trailing body until next header or EOF).
3. `Parse(path) (*File, error)` returns `{buf []byte, sections []Section}` where `Section{Path string, HeaderRange [2]int, BodyRange [2]int}`.

Tests: golden fixtures in `testdata/` covering comments, multiline strings, nested tables, `[[array_of_tables]]` (discovery only — upsert semantics deferred).

### 8.5 Phase 5 — `tomlfile.emit` + `tomlfile.splice`

1. `emitSection(path string, data map[string]any) []byte` — canonical output: sorted keys, correctly quoted strings, ints/floats/bools/datetimes per TOML spec. Lean on an internal writer; do **not** pull in taplo.
2. `(*File).Splice(sectionPath string, replacement []byte) ([]byte, error)` — replace the byte range with `replacement`, leaving everything outside it byte-identical.
3. `WriteAtomic(path string, data []byte) error` per §6.10.

Tests: for every splice test, diff the pre-splice buffer outside the target range against the post-splice buffer — must be byte-identical. This is the only test that proves the core invariant.

### 8.6 Phase 6 — `mcpsrv`

1. `go doc github.com/mark3labs/mcp-go` to lock handler signature, tool-registration API, and structured error constructor.
2. `New(cfg Config) (*Server, error)` wires `schema` + `tomlfile` + mcp-go server.
3. Register three tools. Each handler:
   - parses args (path + section + data for upsert; path + section for get; path for list).
   - resolves schema via `config.Resolve`.
   - delegates to domain packages.
   - wraps `*schema.ValidationError` into a structured MCP error result.
4. `Run(ctx) error` starts stdio transport.

Tests: in-process round-trip tests. Construct the server, hand it a test TOML file in `t.TempDir()`, exercise each tool via the mcp-go client surface (confirm it exposes one; if not, call handlers directly — still high-value).

### 8.7 Phase 7 — `cmd/ta`

1. `flag` for `--help`, `--version`; everything else goes straight to `mcpsrv.Run`.
2. laslig renders `--help` and `--version` to stdout when invoked directly by a human (detect via `isatty` on stderr — or always render; laslig's `Policy` handles non-TTY fallbacks).
3. Startup banner (module version + git SHA via `debug.ReadBuildInfo()`) to stderr via laslig, only when `--log-startup` is set (off by default to avoid polluting MCP transport stderr).
4. `os.Exit(1)` on startup error with laslig notice to stderr.

### 8.8 Phase 8 — Readme + release polish

1. `README.md` with install via `go install github.com/evanmschultz/ta/cmd/ta@latest`, MCP client config snippet, schema example.
2. `gh release create v0.1.0` once `mage check` green and smoke-tested with a real Claude Code client.

---

## 9. Testing strategy

- **Unit tests** for every package — TDD order above. Coverage target: ≥85% on `schema` and `tomlfile` (they carry the invariants); ≥70% elsewhere.
- **Invariant test** for `tomlfile.Splice`: for any parseable input and any section, bytes outside the target range are preserved exactly. One table-driven test + one property-style fuzz (`f.Fuzz` with a corpus) — the fuzz target is small, real TOML fragments.
- **Round-trip test** at `mcpsrv` level: create a file via `upsert`, read it back via `get`, assert equality of the returned section data.
- **Race detector** always on (`mage test` uses `-race`).
- **No mocks of stdlib or third-party deps.** Use `t.TempDir()` and real files; use mcp-go's own client surface if it exposes one.

---

## 10. MVP done-ness checklist

- [ ] `gh repo view evanmschultz/ta` shows the description.
- [ ] `/Users/evanschultz/Documents/Code/hylla/ta` is a bare repo; `main/` is the single worktree.
- [ ] `mage check` green in `main/`.
- [ ] All three MCP tools callable end-to-end against a real TOML file + schema.
- [ ] Splice invariant test passes (bytes outside the target range preserved).
- [ ] Validation error shape matches `ta.md`'s example (structured JSON, not opaque string).
- [ ] Atomic writes verified via `strace`/`dtruss` or a crash-injection test (nice-to-have; skip if not cheap).
- [ ] `ta --help` and `ta --version` render via laslig.
- [ ] Tagged release `v0.1.0` pushed.

---

## 11. Open items (resolve during build, not before)

Carried from `ta.md` §"Open questions." Each will get an ADR-style comment in code at the point of decision.

- **`gotreesitter` TOML grammar coverage.** Phase 4's golden fixtures are where this gets proven. If a fixture fails, file upstream and pick a fallback (manual bracket-matching fallback is ~50 LOC).
- **mcp-go structured error shape.** Resolved in Phase 6 via `go doc`. If errors flatten to strings, wrap our JSON into the string and accept the ergonomic tax.
- **`[[array_of_tables]]` upsert semantics.** MVP: `list_sections` surfaces them; `get` reads them by index; `upsert` errors with `ErrArrayOfTablesNotSupported` and a message saying "use index syntax in section path" — full semantics deferred.
- **Upsert on a missing file.** Default: create it with just the new section. Verified in Phase 6 tests.
- **Atomic write cross-device rename failure.** Out of MVP; document and defer.

---

## 12. What I am explicitly not doing in MVP

- Not splitting into multiple worktrees — `main/` only.
- Not creating Tillsyn project tracking yet (this MD plan is the artifact the dev asked for; Tillsyn can be added later if lane work multiplies).
- Not adding a second package for YAML/JSON support — the architecture accommodates it later, but no scaffolding today.
- Not writing custom observability — `debug.ReadBuildInfo` + stderr laslig notices is the whole logging surface.

# F24 — Multi-Category `ta init` + Symmetric Template Surface + `embed.FS`

Date: 2026-05-02
Status: Pre-build planning. Open questions flagged for dev confirmation BEFORE implementation.

Ground truth read on 2026-05-02:
- `cmd/ta/init_cmd.go` (812 lines) — current single-category init.
- `cmd/ta/template_cmd.go` (565 lines) — current schema-only template surface.
- `internal/templates/templates.go` (506 lines) — schema-only home library.
- `examples/` — `README.md` + `schemas/cascade.toml` populated; `agents/{go,fe,generic}`, `configs/`, `docs-templates/` exist as empty dirs. Legacy `examples/schema.toml` still present.

## 1. Slice shape — atomic vs sequenced (recommendation)

**Recommendation: ONE atomic commit, F24.4 → F24.3 → F24.1 → F24.2 internal ordering.**

Rationale:
- F15/F19+F20/F21/F22/F23 set the precedent — multi-file slices land atomically under a single QA-proof + QA-falsification gate. Splitting F24 across four commits would 4× the QA cost without buying anything reviewable in isolation.
- The four sub-phases are tightly coupled at the API boundary:
  - F24.1 picker calls into F24.3 enumeration (`ListAll(provenance=true)`).
  - F24.1 source-walking depends on F24.4 `embed.FS` mount.
  - F24.2 save promotion writes into the same `~/.ta/` parallel structure that F24.3 enumerates and F24.1 reads.
- A staged approach forces stub APIs that immediately get rewritten; one atomic commit lets the templates package settle on its final shape in one go.

Counter-argument considered: F24.4 (`embed.FS`) ships *new binary content* (cascade.toml is already there; agents/configs/docs-templates are empty placeholders). A reviewer might want the embed.FS commit isolated for binary-size auditing. Mitigation: scope F24.4 to mounting only what already exists in `examples/` today (schemas/cascade.toml + the empty dir structure for forward compat). Agent .md files and config skeletons land in their own follow-up slice (F25?) when content actually exists. This keeps the F24 commit content-light.

**Build order inside the slice:**
1. F24.4 — wire `embed.FS` mount + a `Source` interface in templates package.
2. F24.3 — extend templates package with multi-category list/show/delete + provenance.
3. F24.1 — multi-category init picker using the new templates surface.
4. F24.2 — multi-category save using the same templates surface (write side).

Each step compiles cleanly without the next, and tests gate each step.

## 2. `embed.FS` layout (F24.4)

**What gets pinned today (atomic with F24):**
- `examples/schemas/cascade.toml` — real content, embedded.
- `examples/schemas/*.toml` — anything else dropped here in the future, picked up via `embed.FS` glob (no code change).
- `examples/agents/{go,fe,generic}/` — directory structure pinned via a `.keep` sentinel per dir; content empty for now.
- `examples/configs/` + `examples/docs-templates/` — same, `.keep` sentinel.

**What does NOT ship in F24:**
- Agent .md files. Per `examples/README.md` line 144-152, these come from `~/.claude/agents/` and need generalization first. Defer to a later slice. Picker shows `[ta]` source as empty; user falls through to `[home]` items only.
- Config skeletons (claude-settings.json, mcp.json, gitignore, codex-config.toml). The current `init` already synthesizes `.mcp.json` + `.codex/config.toml` programmatically; F24's config category becomes additive to that later.

**Embed mechanics:**
```go
// internal/templates/embed.go
//go:embed all:examples
var embeddedFS embed.FS

// Source interface lets the picker iterate either binary or home
// without caring which.
type Source interface {
    ListSchemas() ([]string, error)
    ShowSchema(name string) ([]byte, error)
    ListAgents(lang string) ([]string, error)
    // ... per category ...
    Provenance() string  // "ta" or "home"
}
```

The `embed.FS` lives at `internal/templates/embed.go`. The directive `//go:embed all:examples` mounts the directory tree at the binary build root — but `examples/` is at repo root, NOT under `internal/templates/`. **Decision needed:** either (a) mirror `examples/` into `internal/templates/embedded/` at build time via mage, or (b) move the canonical `examples/` *inside* `internal/templates/`, or (c) use `cmd/ta/embed.go` and pass the FS into templates via dependency injection.

**Recommend (c):** keep `examples/` at repo root for repo-level discoverability (people grep for "example schemas" and find it without `internal/` spelunking). Mount the embed.FS in `cmd/ta/embed.go`, inject into templates via `templates.SetBinarySource(fsys fs.FS)` from `cmd/ta/main.go` init. Tests that need binary-source coverage inject a synthetic `fstest.MapFS`.

Open question A: dev preference between (a/b/c)?

## 3. Templates package extension (F24.3 API surface)

Current package is schema-only. Multi-category needs new types and methods. Proposal:

```go
// internal/templates/templates.go (extended)

type Kind string
const (
    KindSchema       Kind = "schema"
    KindAgent        Kind = "agent"
    KindConfig       Kind = "config"
    KindDocsTemplate Kind = "docs-template"
)

type Provenance string
const (
    ProvenanceBinary Provenance = "ta"
    ProvenanceHome   Provenance = "home"
)

type Item struct {
    Kind        Kind
    Name        string      // db name | filename | canonical-config-name
    Lang        string      // agents only; "" for others
    Provenance  Provenance
    DisplayName string      // "<name> — <description>"
}

// ListAll returns every item from binary + home, sorted by (Kind, Lang,
// Name, Provenance). When provenance==false collapses duplicates with
// home winning over binary (per init merge semantics).
func ListAll(provenance bool) ([]Item, error)

// Per-kind helpers — the picker uses ListAll, but the command surface
// (`ta template list --kind=X`) wants kind-scoped enumerations.
func ListSchemas(provenance bool) ([]Item, error)
func ListAgents(lang string, provenance bool) ([]Item, error)
func ListConfigs(provenance bool) ([]Item, error)
func ListDocsTemplates(provenance bool) ([]Item, error)

// Show returns raw bytes for one item from a specific provenance. The
// caller specifies provenance because both [ta] and [home] may declare
// the same name; the picker / `ta template show` lets the user pick.
func Show(kind Kind, name, lang string, provenance Provenance) ([]byte, error)

// Save / Delete are home-only — binary is read-only.
func SaveAgent(srcPath, lang string, opts SaveOptions) (SaveResult, error)
func SaveConfig(srcPath, canonical string, opts SaveOptions) (SaveResult, error)
func SaveDocsTemplate(srcPath, canonical string, opts SaveOptions) (SaveResult, error)
func DeleteAgent(name, lang string) error
func DeleteConfig(canonical string) error
func DeleteDocsTemplate(canonical string) error

// Existing schema API stays intact (LoadHome / ListDBs / ShowDB /
// SaveDBs / DeleteDB). The new ListSchemas wraps ListDBs and stamps
// provenance.
```

Backward compatibility: keep `LoadHome / ListDBs / ShowDB / SaveDBs / DeleteDB / LegacyTemplateFiles` as-is. Internal callers may switch to the new API later, but F24 does not churn callers that don't need it.

## 4. Config structured-merge implementation

Three formats × one contract:

```go
// internal/configmerge/configmerge.go (new package)
package configmerge

type Conflict struct {
    Path     string  // e.g. "mcpServers.ta" or "settings.hooks[2]"
    Existing any
    Incoming any
    Reason   string  // "value-mismatch" | "type-mismatch"
}

type Merger interface {
    Merge(existing, incoming []byte) ([]byte, []Conflict, error)
}

func NewJSONMerger(arrayDedupeKeys map[string]string) Merger // .claude/settings.json, .mcp.json
func NewTOMLMerger(arrayDedupeKeys map[string]string) Merger // .codex/config.toml
func NewLineMerger() Merger                                   // .gitignore
```

Semantics:
- **JSON / TOML:** deep object merge. New keys added. Existing keys with equal values: no-op. Existing keys with different values: emit `Conflict`, leave existing in place (caller decides). Arrays: append-with-dedupe; `arrayDedupeKeys` says which sub-key identifies "same item" (e.g. `"mcpServers": "command"` means dedupe by command).
- **Line:** split on `\n`, append lines absent from existing, dedupe by exact match (case-sensitive, trim trailing whitespace).

Reuse: the existing `mergeClaudeMCP` / `mergeCodexMCP` in `init_cmd.go` already do bespoke merging with the "leave existing untouched" rule. The new `configmerge` package generalizes that, and the existing functions become thin callers of `NewJSONMerger / NewTOMLMerger`. Net delta: less code in init_cmd.go, more reusable code in configmerge.

Open question B: should `configmerge` live at `internal/configmerge/` or under `internal/templates/merge/`? Recommend `internal/configmerge/` because schema merge already lives in `internal/templates/`; mixing config + schema mergers under one package muddies the firewall.

## 5. Picker UI shape (F24.1 huh form)

huh v2 supports multi-group forms. Layout:

```
Group 1: "Schemas" — MultiSelect over ListSchemas(provenance=true).
         Each option: "[ta] cascade — full cascade methodology"
                      "[home] custom-plans — my project plans variant"
Group 2: "Agents (Go)" — MultiSelect over ListAgents("go", true).
Group 3: "Agents (FE)" — MultiSelect, only shown if any items exist.
Group 4: "Agents (generic)" — MultiSelect, only shown if items exist.
Group 5: "Configs" — MultiSelect over ListConfigs(true).
Group 6: "Docs templates" — MultiSelect over ListDocsTemplates(true).
Group 7: "MCP targets" — existing two-toggle (Claude / Codex).
```

Empty groups are skipped (current binary has empty agent/config/docs dirs). Submit handler walks per-category selections and routes each to its destination.

Lang inference (agents): the project's primary language. Source: read `<target>/.ta/config.toml` `bootstrap.language` (new key) or huh-prompt at start ("Project language: [go] [fe] [generic]"). Defaults to "go" because the dogfooding project is Go.

Open question C: do we add a top-level `bootstrap.language` to `bootstrapConfig` in F24, or defer to a later slice and always huh-prompt for now? Recommend prompt for F24 — additive, can move into config.toml later without breaking anything.

## 6. MCP wire shape

**Recommend: new tool `init`** with two operations.

```jsonc
// preview — discover what's available
{
  "tool": "init",
  "action": "preview",
  "path": "/abs/path",
  "lang": "go"
}
// returns: {
//   "schemas":        [{"name":"cascade","provenance":"ta","display":"..."}, ...],
//   "agents":         [...],
//   "configs":        [...],
//   "docs_templates": [...],
//   "conflicts":      []   // populated only by apply
// }

// apply — execute selections
{
  "tool": "init",
  "action": "apply",
  "path": "/abs/path",
  "lang": "go",
  "selections": {
    "schemas":        [{"name":"cascade","provenance":"ta"}],
    "agents":         [{"name":"go-builder-agent.md","provenance":"home"}],
    "configs":        [{"name":"claude-settings","provenance":"ta"}],
    "docs_templates": [{"name":"CLAUDE.md","provenance":"home"}]
  },
  "on_conflict": "error" | "skip" | "overwrite" | "merge-only" | "force",
  "per_conflict": {                               // optional, overrides on_conflict per item
    ".claude/settings.json": "merge-only"
  }
}
// returns: written / skipped / conflicted lists per category;
// errors structured as ConflictResponse when on_conflict=="error"
// and any conflicts existed.
```

Rationale for separate tool (vs extending `template`): `template` is library-management surface (`list/show/save/delete`); `init` is project-bootstrap surface. Different verbs, different mental model. Conflating them would force users to memorize that `template apply` does both library listing AND project bootstrap.

## 7. CLI-JSON shape

```sh
# Preview — non-interactive enumeration of available items.
ta init --json --preview --path /abs

# Apply — selections-file JSON in the same shape as the MCP apply payload.
ta init --json --selections-file selections.json --on-conflict error --path /abs

# Convenience: --all-binary / --all-home flags pre-populate the
# selections set without requiring a JSON file.
ta init --json --all-binary --on-conflict error
```

Conflict flags (mirror locked spec): `--overwrite`, `--skip-conflicts`, `--merge-only`, `--force`. Default: error on first conflict, list every conflict in the error payload, suggest re-run flag.

Open question D: do we need both `--selections-file` and per-flag selections (e.g. `--with-schema=cascade --with-agent=go-builder@home`)? Per-flag would be ergonomic for shell users but explodes the flag set. Recommend selections-file only for F24; per-flag deferred unless dev objects.

## 8. Test coverage breakdown

Group 1 — `internal/templates` (~12 new tests):
- `TestListAll_Provenance` — binary + home interleave, sorted, dedupe rules.
- `TestListAll_EmptyHome / EmptyBinary` — both edge cases.
- `TestListAgents_LangFilter` — go vs fe vs generic isolation.
- `TestSaveAgent_Conflict` / `TestSaveConfig_Conflict` / `TestSaveDocsTemplate_Conflict` — overwrite/skip semantics per kind.
- `TestDeleteAgent_BinaryReadonly` — binary-source delete returns ErrReadOnly.
- `TestShow_BothProvenances` — same name in both, disambiguation.
- `TestEmbedFSMount` — synthetic fstest.MapFS injected, binary side enumerated correctly.

Group 2 — `internal/configmerge` (~10 new tests):
- `TestJSONMerger_NewKeys / Conflicts / ArrayDedupe`.
- `TestTOMLMerger_*` (parallel set).
- `TestLineMerger_DedupeExact / TrimWhitespace / Empty`.
- `TestMerger_RoundTrip` — merge(merge(a,b),b) == merge(a,b).

Group 3 — `cmd/ta/init_cmd.go` (~8 new tests, replacing/extending current init tests):
- `TestRunInit_MultiCategory_TUI` — synthetic huh form with MapFS binary + tmpdir home.
- `TestRunInit_JSON_Preview` — `--json --preview` payload shape.
- `TestRunInit_JSON_ApplySelections` — `--selections-file` end-to-end.
- `TestRunInit_OnConflict_Error / Skip / Overwrite / MergeOnly / Force` — all five.
- `TestRunInit_PerConflict_Override` — per-path override beats global flag.

Group 4 — `cmd/ta/template_cmd.go` (~6 new tests):
- `TestTemplateList_KindFilter` — `--kind=agent` returns only agents.
- `TestTemplateShow_KindLangProvenance` — disambiguation flags.
- `TestTemplateSave_Agent / Config / DocsTemplate` — promote each kind.
- `TestTemplateDelete_BinaryReadonly` — error message phrasing.

Group 5 — `internal/mcpsrv` (~4 new tests):
- `TestMCPInit_Preview` — JSON-RPC payload shape.
- `TestMCPInit_Apply_Conflict_Error` — structured conflict response.
- `TestMCPInit_Apply_Force` — silent overwrite path.

Total: ~40 new tests. Mage targets: `mage test` (full), `mage test ./internal/templates/...` (focus), `mage check` (CI gate).

## 9. Open questions and unknowns (BEFORE build start)

- **A. embed.FS mount strategy.** (a) mirror `examples/` into `internal/templates/embedded/` at build time, (b) move `examples/` under `internal/templates/`, or (c) mount in `cmd/ta/embed.go` and inject. Plan recommends (c).
- **B. configmerge package location.** `internal/configmerge/` or `internal/templates/merge/`. Plan recommends `internal/configmerge/`.
- **C. lang detection for agents.** New `bootstrap.language` key in `~/.ta/config.toml` vs always huh-prompt. Plan recommends huh-prompt only for F24.
- **D. CLI selections shape.** `--selections-file` only vs also `--with-schema=` style flags. Plan recommends file-only.
- **E. F24 commit scope vs binary content.** This plan caps F24 at structure + cascade.toml. Confirm: agent .md content + config skeletons land in F25, not F24?
- **F. legacy `examples/schema.toml` retirement.** examples/README.md line 36-37 says "will retire once F4 + embed-via-embed.FS strategy lands." F24 IS that landing. Do we delete it in F24 or leave for F25?
- **G. `ta template apply` future.** Current `apply` is schema-only single-db extract. After F24, does it grow `--kind=` to apply ANY single item? Defer to F25 unless dev wants it now.

## 10. Memory rules respected

- `feedback_ta_one_schema_file_per_dir.md` — `~/.ta/schema.toml` remains the SOLE schema file in `~/.ta/`. Agents, configs, docs-templates live in *subdirectories* (`~/.ta/agents/`, etc.), NOT as additional `.toml` schema fragments. Confirmed compatible.
- `feedback_install_local_bin.md` — F24 verification gates are `mage test` / `mage check` only. Never `mage install`.
- `feedback_qa_before_every_commit.md` — atomic-commit recommendation is contingent on full QA-proof + QA-falsification pair clearing the slice.
- `feedback_ta_id.md` — no record-id semantics changes; templates package stays out of the runtime read path.

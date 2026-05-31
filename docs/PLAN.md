# ta — Plan

This document describes ta's locked architecture and the remaining forward-looking work. Three sections: **A. What ta is**, **B. Why (rationale)**, **C. Remaining action items**.

Cross-references: per-fix detail lives in `E2E_FIXES.md`; cascade methodology lives in `CASCADE_METHODOLOGY.md` (+ ta addenda in `docs/cascade-reference.md`); per-category default-bundle layout lives in `examples/README.md`; F10 implementation breakdown lives in `workflow/ta/F10-PLAN.md`.

---

## A. What ta is

### A.1 Project scope

ta is a structured-data CLI and MCP server for inherently semi-structured text files. Backends: TOML (configuration, planning, structured records) and Markdown (prose with declared heading sections). The same surface — get / list-sections / search / create / update / delete / schema / index / template / init — is available three ways: **CLI** (cobra + fang + laslig + huh), **MCP** (mark3labs/mcp-go over stdio for agent harnesses), and **a shared core** (`internal/ops`) consumed by both adapters. The primary value is uniform CRUD across files of declared shapes: agents and humans reach into a TOML or MD file by an id that hides the on-disk path layout, and ta resolves the id against a project-level schema.

### A.2 Core concepts

- **Schema** — declarative description of databases, types, and fields, stored in `.ta/schema.toml`. The schema is the API; field `description` strings are the docstring layer, surfaced through `schema(action="get")`.
- **Database (`db`)** — top-level table in `schema.toml`. Each db declares a `paths = [...]` slice of literal-or-glob entries pointing at one or more on-disk files of one shared format (inferred from extension).
- **Type** — sub-table under a db. Each type declares fields; for MD-backed dbs, a type also declares a `heading` level (1..6).
- **Field** — sub-table under a type's `fields`. Carries `type` (one of `string | integer | float | boolean | datetime | array | table`), optional `required`, `description`, `enum`, `default`, `format` (markdown render hint).
- **ID** — the canonical identifier of a record. One dotted path. Examples: `plans.demo-1`, `notes.note-001`, `workflow.drop_3.db.task-001`. The id is what users / agents pass, what ta returns, what `cat <file>.toml` shows as the bracket header. ONE thing.
- **Record** — one bracket in a TOML file or one section under a declared heading in an MD file. Body bytes include descendant brackets / deeper headings as content.
- **Runtime index** — `.ta/index.toml`. Per-project map of every record's id → type, created/updated timestamps. ta is the sole writer; reads consult it for type resolution; rebuild is recovery-only.

### A.3 ID

The id is one dotted path. It identifies one record in one file. ta resolves the id internally to a file-on-disk and a bracket-key inside that file; that resolution is implementation. Users and agents see ONE word: id.

- Examples: `plans.demo-1`, `notes.note-001`, `docs.installation`, `workflow.drop_3.db.task-001`.
- The id NEVER carries type. Type is metadata held in the runtime index, not in any user-visible string.
- The on-disk TOML bracket header IS the id verbatim. `plans.toml` contains `[plans.demo-1]`. The schema's `[<db>.<type>]` declaration is metadata describing record shape; it is NOT a bracket prefix that propagates into data files.
- Within a single-file db, ids are unique across types. If db `plans` declares both `task` and `note` types, no `t1` may belong to both — `[plans.t1]` is one record. Schema-load and CRUD enforce via `ErrIDCollisionAcrossTypes`.

A scope-prefix id (a strict prefix of a full id) names many records: `plans` resolves to every record in `plans.toml`; `workflow.drop_3.db` resolves to every record in that file. `get` / `list-sections` / `search` accept scope-prefixes; `create` / `update` / `delete` of a single record require a full id.

Type is supplied separately via `--type <db>.<type>` (CLI) or `typeName` (MCP). REQUIRED on Create. OPTIONAL on read paths (resolved from the runtime index when absent; cross-checked when passed). MUST be db-qualified (`plans.task`, never bare `task`). Bare-slug form rejects with `ErrTypeNotQualified`.

### A.4 Schema model — `paths` slice

Every db declares its on-disk extent as a `paths = [...]` slice. Three entry shapes:

- **Literal path** — `"plans.toml"`, `"README.md"`, `"workflow/drop_3/db.toml"`. Names exactly one file relative to the project root.
- **Glob** — `"workflow/*/db.toml"`, `"docs/*.md"`. Expands to zero or more matching files at resolve time.
- **Home-rooted** — `"~/.ta/projects/myproj/workflow/*/db.toml"`. Expands `~` to the user home; the rest is glob or literal.

Format is **inferred from the path extension** at schema-load time. All paths in one db share the same recognized extension (`.toml`, `.md`; further extensions — see Phase 15). Mixed-extension paths-slices fail at load with `ErrInconsistentPathFormats`. Extensionless paths fail with `ErrAmbiguousPathFormat`. Trailing-slash collection mounts fail with `ErrCollectionMountUnsupported` — declare a glob instead (`paths = ["docs/*.md"]`).

Schema example:

```toml
[plans]
paths = ["plans.toml", "archive/plans.toml"]
description = "Project plans across active and archive."

[plans.task]
description = "One unit of work."

[plans.task.fields.id]
type = "string"
required = true
description = "Stable identifier."

[plans.task.fields.body]
type = "string"
format = "markdown"
description = "Approach + evidence + traces."

[docs]
paths = ["docs/*.md"]
description = "Per-file documentation pages."

[docs.section]
heading = 2
description = "One H2 section. Body is markdown."

[docs.section.fields.body]
type = "string"
format = "markdown"
description = "Prose under this H2."
```

### A.5 One schema.toml per `.ta/`

Every `.ta/` directory holds **exactly one** schema file named `schema.toml`. The `[db.type.field]` nesting language is the composition surface; multiple files would duplicate that capability and create merge ambiguity. The rule applies uniformly to home (`~/.ta/schema.toml`) and to every project (`<proj>/.ta/schema.toml`). Schema cascade resolves home first, then every `.ta/schema.toml` on the ancestor chain of the target path, root-to-file. Same-named dbs override; unique dbs are additive.

### A.6 Runtime index

`.ta/index.toml` is the type-resolution lookup. ta writes it atomically on every successful create / update / delete; reads consult it to resolve a record's id to its owning type. The index is **trust-and-fail-loud**:

- ta is the only writer. Manual edits are not detected; corruption is loud at first read.
- On `format_version` mismatch, the loader fails with `ErrUnknownFormatVersion` and points the user at `ta index rebuild`.
- On a record present on disk but absent from the index, reads fail with `ErrTypeUnresolved` (with the same rebuild remediation hint).
- On the index file being absent entirely, reads fail with `ErrIndexMissing` (rebuild remediation).
- On a request id that has no on-disk record AND no index entry, reads fail with `ErrRecordNotFound`.
- Index reads do not tolerate missing-from-index entries; every drift fires loud with one of the sentinels above.
- Rebuild is **recovery-only** — never a routine diagnostic. To verify index state, read `.ta/index.toml` directly between operations.

The index format is a flat map keyed by id, pelletier-marshaled into nested TOML brackets for human readability. The format carries `format_version = 2` at the root; a `format_version` mismatch triggers a hard-stop loud failure with one-shot `ta index rebuild` remediation.

### A.7 Validation — meta-schema

Schemas validate against an internal **meta-schema** that lives in the binary (and is itself readable via `schema(action="get", scope="ta_schema")`). The meta-schema enforces:

- `paths = [...]` is required on every db; entries are well-formed literal / glob / home-rooted paths; all share one recognized extension.
- Every type has `description`. MD types have `heading` (1..6). TOML types have no `heading`.
- Every field has `type` from the supported set. Optional `required`, `description`, `enum`, `default`, `format`.
- `extends` (per F22) chains a type or db onto a base; cycle detection is at load.
- `element_type` and `element_fields` (per F21.1 / F21.2) describe array element shape.
- Reusable type aliases (per F21.3) declared once at `[ta_schema.types.<alias>]` and referenced by name from `element_type`.
- No two dbs at one cascade layer may share a path or have a path that is a prefix of another's (no nested dbs).
- Record types within one MD-backed db may not share `heading` values.
- Within a single-file db, ids are unique across record types (`ErrIDCollisionAcrossTypes`).
- The db-level `format` key is FORBIDDEN on user-supplied schema input. Schema mutation rejects an incoming `data["format"]` payload at the db level with `ErrFormatKeyForbidden`. Db format is inferred from path extension (A.4).
- The field-level `format` hint (`[<db>.<type>.fields.<name>] format = "markdown"`) is preserved as a renderer signal — semantically distinct from the db-level format and unaffected by the rejection above.

Violations fail the `schema` mutation atomically with a structured error; the schema on disk is never partially written.

### A.8 Surfaces — CLI, MCP, shared core

```
cmd/ta/                     CLI: cobra + fang + laslig + huh
  main.go                     bare-on-TTY → huh menu; bare-on-stdio → MCP server
  commands.go                 one cobra subcommand per endpoint
internal/
  ops/                       shared endpoint layer; plain Go funcs; no protocol dep
  mcpsrv/                    MCP protocol glue; tool decls + handlers; calls into ops
  schema/                    schema.toml load + meta-schema validation
  db/                        id parsing + path resolution
  index/                     runtime index load + rebuild
  backend/
    toml/                    TOML backend (pelletier-go-toml)
    md/                      MD backend (hand-rolled ATX scanner; no CGO)
  search/                    structured + regex search across backends
  fsatomic/                  atomic writes
  templates/                 ~/.ta/ default-bundle save / list / show / delete
```

The CLI and MCP are both **adapters** over `internal/ops`. Endpoints own all semantics: path resolution, scope walking, filters, limits, validation, splice, write, index update. Adapters add nothing beyond I/O marshaling. Asymmetries are limited to: TTY-only UX (huh forms; bare-on-TTY menu), render polish (laslig + glamour vs raw structured data), template-library management (CLI-only).

### A.9 Tool surface

Endpoint set, identical on CLI and MCP except where noted:

- `get(path, id, [fields])` — read one record (or scope-prefix match) by id.
- `list-sections(path, [scope], [limit], [all])` — enumerate ids.
- `search(path, [scope], [match], [query], [field], [limit], [all])` — full-text + structured filter.
- `create(path, id, --type=<db>.<type>, data)` — create new record.
- `update(path, id, [--type=<db>.<type>], data)` — patch existing record (PATCH semantics).
- `delete(path, id, [--type=<db>.<type>], [--force], [--verbose])` — remove record / file (per F19's id levels).
- `schema(path, action, [scope|kind|name|data], [--paths-append], [--paths-remove])` — inspect or mutate schema.
- `index(path, action)` — rebuild (`action=rebuild`) or get (`action=get`) the runtime index.
- `init(path, [--template], [--db], [--agent], [--config], [--docs-template])` — bootstrap a new project from binary defaults + `~/.ta/` defaults.
- `template(action, [--kind=schema|agent|config|docs-template], [--name], [--path], [--canonical])` — manage `~/.ta/` defaults.

Read commands accept `--json` for agent-parseable output. Mutating commands return a concise laslig success notice on the CLI; pass `--verbose` to echo the post-mutation record. MCP counterparts always return JSON.

The `schema(action=get)` MCP response includes a derived `format` field on each db's view (read-only inspectability — agents see the inferred format). The reverse direction is forbidden: `schema(action=create|update, kind=db, data=...)` rejects incoming `format=` payloads with `ErrFormatKeyForbidden` (format derives from path extension, not from caller input).

Bare `ta` on a TTY opens the huh subcommand menu. Bare `ta` on stdio launches the MCP server. No explicit `mcp` subcommand needed when registered in `.mcp.json` / `.codex/config.toml`.

### A.10 Cascade methodology compatibility

Cascade nodes (action items, drops, droplets, planners, qa records) are storable as ta records once F21 + F22 + F23 land — F21 gives typed array elements and shape validation, F22 gives schema inheritance for the NodeBase / ActionItem common fields, F23 gives auto-spawn for the QA-twin pattern. The orchestration layer that walks the cascade tree (LLM client OR Tillsyn dispatcher) sits **above** ta — ta provides the storage substrate and the structured-CRUD surface; cascade dispatching is not coupled into ta itself. Detailed methodology lives in `CASCADE_METHODOLOGY.md`. Cascade schema fragment lives at `examples/schemas/cascade.toml` (planned).

### A.11 Build tooling

All build / test / lint / verification routes through mage. `mage check` is the canonical green-bar command. Test output routes through `laslig/gotestout` which auto-detects TTY status — humans get a styled summary, agents and CI pipes get plain text without env-var gymnastics. Raw `go test`, `go build`, `go vet`, `gofmt`, `gofumpt` are out of scope for agent invocations — always go through the mage target. `mage install` builds the binary into `$HOME/.local/bin/ta` (NOT `$GOBIN`); this is dev-only and never a verification target.

`mage install` writes an empty `~/.ta/schema.toml` placeholder when none exists; never overwrites existing user content. The empty placeholder lets first-run `ta init` produce a clean empty-home guard pointing at `examples/` rather than crashing on a missing file.

---

## B. Why — design rationale

### B.1 Path obscured from agents

Agents address records by id. The id is on-disk-aware (the file lives at `plans.toml`, identity is `plans`), but agents never construct directory traversal logic, never join with project roots, never reason about platform separators. ta resolves the id to disk via the path-slice declared in the schema. This decouples the agent's semantic mental model ("a record in plans, named demo-1") from any specific layout choice.

### B.2 Type orthogonal to id

Embedding type in the id produces two failure modes: records of multiple types in one file lack unambiguous addressing without the type segment, and every read has to carry the type even when ta could look it up. The locked design keeps type out of the id entirely; the runtime index is the lookup table. Type is required orthogonally on Create (`--type <db>.<type>`) because Create is the moment the disambiguation is established; on read paths, type is optional and resolves from the index. The db-qualified form (`plans.task`, not bare `task`) is the only valid form — without type-in-id, bare-slug type would be ambiguous when two dbs declare the same type slug.

### B.3 Format inferred from extension

A `format = "toml"` declaration on a db that already declares `paths = ["plans.toml"]` is redundant data at best and a footgun at worst (declaring `format = "md"` with `paths = ["plans.toml"]` gives the loader contradictory truth). The locked design treats the path's extension as the single source of truth: TOML paths give the TOML backend; MD paths give the MD backend; mismatched extensions in one db fail loud. This shrinks every schema, removes a class of inconsistency bugs, and simplifies adding new formats — register an extension in the loader's map and implement the backend, no schema-language change required.

### B.4 One schema.toml per .ta/

The schema-composition language already supports arbitrary `[db.type.field]` nesting; multiple `.toml` files in one `.ta/` would duplicate that capability and add merge-ambiguity (which file wins on a conflict; what does ordering mean). Forcing a single canonical `schema.toml` per directory makes the cascade well-defined: outer layer fully merges into inner, db-by-db, with same-name override. The architectural rule is non-negotiable.

### B.5 Trust-and-fail-loud index

Auto-rebuilding the index on any drift would mask schema or write-path bugs and create silent recovery-from-corruption that papers over real failures. The locked design treats ta as the sole writer and treats every detected drift as a loud failure with an explicit `ta index rebuild` remediation. Rebuild is recovery-only: routine commands never trigger it; suggesting it as the first response to any error is wrong. To verify index state, read `.ta/index.toml` directly between operations. A `format_version` bump is the one documented escape hatch — when the index format itself changes, the loader fires `ErrUnknownFormatVersion` with one-shot rebuild remediation rather than silently re-interpreting older shapes.

### B.6 Append-aware merge on init

`ta init` and `ta template save` never silently clobber user state. Every category (schema, agents, configs, docs-templates) merges with append-aware semantics: structured merge for configs (new keys added, existing kept; arrays append-with-dedupe); db-by-db merge for schemas (same-name conflict prompts confirm-or-skip); filename-additive for agents and docs-templates. Confirmation surfaces appropriately per surface — huh prompt on TTY, `ErrInitConflict` + `--overwrite`/`--skip-conflicts`/`--merge-only`/`--force` flags on CLI, structured conflict response on MCP. Sourcing from `~/.ta/` does not bypass the confirm flow — a user's home agent overwriting a project agent still triggers the prompt.

### B.7 Embedded defaults via embed.FS

Binary-embedded defaults under `examples/` ship inside the ta binary via Go's `embed.FS`. First-time `ta init` in a fresh checkout has zero network dependency and zero "where do I get the defaults" UX. Users customize by promoting items into `~/.ta/` (parallel structure: `~/.ta/schema.toml`, `~/.ta/agents/<lang>/`, `~/.ta/configs/`, `~/.ta/docs-templates/`); subsequent inits show binary + home defaults side-by-side, provenance-tagged. Binary defaults are immutable through ta's surface — `ta template delete` on a binary item errors with "copy to home first to customize."

### B.8 Cascade methodology compatibility (layered, not coupled)

The cascade methodology — drops, droplets, action items, qa twins, role-based dispatch — is a coordination pattern that consumes ta as substrate. ta provides the storage shape (categorized records), the id grammar, and the structured-CRUD surface; the orchestrator that walks the cascade tree is a separate concern, run by either an LLM client driving ta directly or a Tillsyn-style dispatcher. ta does not bake cascade-specific semantics into its core. The cascade schema fragment is one of the bundled defaults `ta init` offers, not the only point of the tool.

---

## C. Remaining action items

Forward-looking work to v0.1.0. Each item is a one-line summary plus reference to the detailed entry in `E2E_FIXES.md` where applicable. Order roughly matches sequencing in `workflow/ta/F10-PLAN.md` and the F-line punch list.

### Phase 1 — F10: id grammar + format inference

Drops type from the id; all reads/writes use the canonical id form. Type lives on `--type <db>.<type>` (CLI) or `typeName` (MCP). Format inferred from path extension (replaces the db-level `format` field). On-disk TOML brackets are the id (no type segment). Index `format_version = 2`. Detailed task breakdown in `workflow/ta/F10-PLAN.md`. Sub-items:

- **T1** — drop the type field from the address struct, rename the struct to `Resolved`, rewrite parser, rewrite Canonical, remove collection-mount runtime, update all backends + ops + magefile.go literals + tests in one atomic commit. Backend signatures take id as a single string (the bracket header is the id; no composition).
- **T2** — schema loader: derive db format from first path's extension; reject mixed-extension and extensionless paths-slices; reject collection mounts; enforce `ErrIDCollisionAcrossTypes` per file scope; update meta-schema; update `examples/`.
- **T3** — index `format_version = 2`; `canonicalForBracket` returns bracket-as-id (identity transform for single-file mounts; file-relpath-prefixed for multi-file glob mounts); rebuild produces `format_version = 2` from disk truth; idempotent-rebuild test.
- **T4** — TOML data file bracket alignment: `ta index rebuild` walks each declared db's data files and ensures every bracket header equals its record id. Reads of files whose brackets do not match their id fire `ErrFileFormatTooOld` and direct the user to `ta index rebuild`.
- **T5** — search + list-sections rewires (remove `firstDeclaredTypeIndex`; rewrite `parseScope` for id-prefix shape; index-once-loaded type filter). F11's read-path bug dissolves once the bracket form is uniform with the id (no per-mount-shape decision exists to misalign).
- **T6** — CLI + MCP surface: db-qualified `--type`; the MCP record-targeting parameter is `id`; tool descriptions and goldens regen.
- **T7** — docs closeout (cascade-reference.md audit; E2E_FIXES.md F10 close-out).

### Phase 2 — F15: template save merges into ~/.ta/schema.toml

`ta template save` writes to `~/.ta/schema.toml` (merge), never to a per-name file. The `<name>` argument becomes a filter for which dbs to merge:
- bare → merge ALL project dbs;
- `ta template save plans` → merge only `[plans]`;
- `ta template save plans,notes` → merge specifically those;
- TTY: huh.MultiSelect over project dbs.

`ta init` picker reads ONLY `~/.ta/schema.toml`; the legacy `~/.ta/*.toml` walk is removed. Same-name db conflicts on merge prompt confirm-or-skip (huh on TTY; CLI flag `--overwrite` for non-interactive). `~/.ta/schema.toml` is the only file ta reads or writes in `~/.ta/`. (See `E2E_FIXES.md` F15.)

### Phase 3 — F19 + F20: delete shape + verbose flag

F19 defines delete with three id levels:

- **Record** — full id — removes one bracket from one file.
- **File** — bare file-relpath portion of an id — removes one concrete file when the relpath uniquely identifies it (single literal-path resolution OR uniquely-globbed match).
- **Glob-rooted** — bare file-relpath resolving via glob to multiple files — refuses with `ErrUnscopedGlobDelete` ("id matches multiple files: pick one").

The id parser accepts bare file-relpath for delete. File-level delete prompts confirmation (`--force` for non-interactive). All delete error messages use paths-slice vocabulary throughout.

F20 wires `--verbose` on the delete subcommand. On success with `--verbose`: echo id removed, file path it lived in, count of remaining records under the same parent scope. (See `E2E_FIXES.md` F19, F20.)

### Phase 4 — F21: typed array elements + element shapes + type aliases

Required for cascade dogfood. The meta-schema accepts any slice for `type = "array"` and any map for `type = "table"`; cascade nodes need stronger validation. Three sub-phases:

- **F21.1 — `element_type` for arrays.** New meta-schema field on `[<db>.<type>.fields.<name>]` — declares the element shape for `type = "array"`. Validates each element; failure cites `field = "paths[2]"` with expected/actual.
- **F21.2 — `element_fields` for arrays of tables.** Recursive shape declaration for arrays whose elements are tables (`completion_checklist`, `context_blocks`, `resource_refs`, `comments`).
- **F21.3 — Type aliases.** Declare a reusable type once at `[ta_schema.types.<alias>]`; reference by name from `element_type` declarations. Eliminates redundant inline shape blocks across cascade types.

Build all three before release. Detailed schema syntax in `E2E_FIXES.md` F21.

### Phase 5 — F22: schema inheritance via `extends`

`extends = "<base>"` pulls every field from a base type into a child. Single-extends only (no multi-inheritance); re-declared fields override; cycle detection at load. Required to avoid 6× redundant declarations of NodeBase / ActionItem fields across `[drops.drop]`, `[drops.planner]`, `[drops.droplet]`, `[drops.qa_proof]`, `[drops.qa_falsification]`, `[drops.failure]`. Sequenced after F21 within the same release. Detailed design in `E2E_FIXES.md` F22.

### Phase 6 — F23: auto-spawn rules

`[<db>.<type>.auto_spawn]` declarations describe records that ta auto-creates whenever a record of the parent type is created. Mirrors Tillsyn's `child_rules` pattern. Example: creating a `drops.drop` record triggers automatic creation of one `drops.qa_proof` and one `drops.qa_falsification` placeholder, all in one atomic write. Lights up "drop creates required QA twin records automatically" semantics. Lands after F22 (auto-spawn references concrete types that depend on inheritance resolution). F23a (constraint-expression DSL) is rejected in favor of F23b (declarative auto-spawn). Detailed design in `E2E_FIXES.md` F23.

### Phase 7 — F24: multi-category init + template save + symmetric template surface + embed.FS

`ta init` defaults are categorized + à la carte from binary `examples/{schemas,agents/<lang>,configs,docs-templates}/` (embedded via `embed.FS`) AND from user `~/.ta/` parallel structure. Selections land at canonical project destinations. Four sub-phases:

- **F24.1 — Multi-category `ta init` picker** (huh + MCP + CLI-JSON) with append-aware merge + confirm-before-overwrite per surface. Per-category merge semantics: schemas merge dbs (same-name → confirm-or-skip); configs structured-merge with append-dedupe on arrays; agents and docs-templates additive by filename (existing → confirm-or-skip); `.gitignore` append-with-dedupe by exact line.
- **F24.2 — Multi-category `ta template save`** family with `--kind=schema|agent|config|docs-template` and category-appropriate args. `--all-kinds` bulk-promotes everything from project to home with per-conflict prompts.
- **F24.3 — Symmetric `ta template list / show / delete`** for inspection. Provenance-tagged enumeration; binary defaults are read-only (`delete` errors with "copy to home first to customize").
- **F24.4 — `embed.FS` integration** so the binary ships `examples/` contents; ta init walks the embed for binary-side defaults.

Lands after F15 + F22 + F23. Together these complete the "user shares defaults across projects, ta init mixes binary + home defaults, never silently overwrites" model. Layout details in `examples/README.md`. Detailed design in `E2E_FIXES.md` F24.

### Phase 8 — F18 + F16: picker UI fix (subsumes queued-stdin auto-submit)

`ta init` picker has a frame-ownership conflict between laslig (warnings + title block + description) and huh (option-list + help bar): laslig pre-output occupies terminal lines before huh starts; huh's full-screen rendering then competes for the same area; result is that selectable rows are not visible to the user, `x` and arrow keys appear inert, and only Enter exits — sometimes with a non-deterministic selection. Architectural fix (pick one): (a) buffer laslig output until after the form completes; (b) flush + clear before opening huh; (c) render warnings inside huh as a `huh.Note` group above the MultiSelect (recommended). Then apply Dracula theme across every huh form, add `[x]/[ ]` markers, cursor indicator, padding, and after-submit echo. The after-submit echo also catches the queued-stdin auto-submit bug (F16): when stdin has bytes queued from a multi-line paste and huh consumes them as form input, the echo prompt forces an explicit confirm step that reveals the unintended selection before write. Subsumes F17 (wrong toggle key in prompt text — the help bar reflects the actual binding once the rendering is fixed). Workaround for the picker is `ta init --template <name>`, which bypasses the picker entirely. Detailed design in `E2E_FIXES.md` F18, F16, F17.

### Phase 9 — F13: huh form upgrade

`ta create` / `ta update` huh form is single-line `huh.Input` for every field with no markdown preview and no theme. Honor [D1] type/format dispatch already specced: `string` + `format = "markdown"` → `huh.Text` (multi-line); bare `string` → `huh.Input`; `string` + enum → `huh.Select`; etc. Add live glamour preview for markdown fields (verify huh API support via Context7 — may require a custom bubbletea wrapper). Apply Dracula theme. Improve label/field separation. Detailed design in `E2E_FIXES.md` F13.

### Phase 10 — F1 + F2 + F2a + F4 + F5: install / init / help UX polish

- **F1** — `mage install` validates an existing `~/.ta/schema.toml` against the meta-schema before declaring `untouched`; emits a laslig WARN block on invalid existing schema with repair guidance.
- **F2** — `outcome: untouched` → human-readable copy: `schema preserved (your edits are safe)` / `placeholder created (empty — populate it next)` / `placeholder reset (D2 cold-home guard)`. Apply to both INFO and SUCCESS blocks.
- **F2a** — Walkthrough docs / instructional copy: prefer `glow` over `cat` for TOML / MD inspection. ta itself uses laslig + glamour internally; this is purely a human-facing ergonomics note.
- **F3** — `mage install` empty-placeholder behavior (locked in A.11): writes an empty `~/.ta/schema.toml` placeholder when none exists; never overwrites existing content. No additional implementation work for F3 beyond the A.11 lock.
- **F4** — `ta init` empty-home guard message: replace local `examples/` reference with a remote URL (`https://github.com/<org>/ta/tree/main/examples`) + one-shot fetch idiom (`gh repo clone` + `cp` example).
- **F5** — `ta schema` no-args error: render the same laslig empty-home-style guidance block listing the three populate paths (`--action=create`, `examples/` cp, `template save`) instead of bare error text.

Detailed designs in `E2E_FIXES.md` F1, F2, F2a, F4, F5.

### Phase 11 — F8 + F9: MCP dev-loop polish

- **F8** — `mage install` post-install hint when an MCP `ta` process is detected (`pgrep -f '^ta$'`): "Restart Claude Code / Codex to pick up the new binary." Cheap detection, clear message.
- **F9** — MCP cache single-project-per-process is hostile in bare-root + worktree setups. Auto-rebind on first failure: when the requested project differs from the bound project AND the bound project has no schema, silently rebind to the new project. Documents the bare-root + worktree pattern by accommodating it.

Detailed designs in `E2E_FIXES.md` F8, F9.

### Phase 12 — F14: rebuild preserves created timestamps

`ta index rebuild` overwrites `created` timestamps for entries already in the (corrupted-but-readable) index. Fix: on rebuild, if the existing index is parseable, READ each entry's `created` first; keep that value when re-emitting; only re-stamp `updated` to "now". For entries truly new on disk (or when the index file is absent / unreadable), fall back to "now" for both. Adjacent improvement: rebuild output reports "preserved created on N records, stamped fresh on M records" so users can tell when history was lost. Detailed design in `E2E_FIXES.md` F14.

### Phase 13 — F6: schema-mutation huh TUI

`ta schema --action=create|update|delete` is JSON-only; humans on a TTY get a JSON wall when they came from the empty-home guard expecting an interactive build path. Add huh forms. Two reasonable shapes — pick one in design:

- **(a) On-demand** — `ta schema --action=create --kind=db` (no `--data`) drops into a huh form that walks the db's required fields (`paths`, `description`), then types, then fields. Mirrors the [D1] pattern on `ta create` / `ta update` for records.
- **(b) Top-level** — `ta schema build` (new subcommand) opens a guided multi-step huh form that emits the equivalent action=create chain end-to-end.

Either way, `--data` / `--data-file` remain non-interactive escapes for agents and scripts. Detailed design in `E2E_FIXES.md` F6.

This phase also resolves F7 (incremental type build is architecturally impossible — `--kind=type` rolls back because empty-fields is invalid at load time). Recommended fix (a in F7): relax the load rule so types with zero fields are valid during construction; defer the "≥1 field" check to record-validation time. The huh TUI from F6 can wrap whichever F7 resolution lands.

### Phase 14 — agent MD copy from `~/.claude/agents/` to `examples/agents/<lang>/`

Promote stable agent-rules files from `~/.claude/agents/` into ta's bundled `examples/agents/<lang>/` so `ta init` can offer them via the F24.1 picker. Slice opens with a dev review of the source agents in `~/.claude/agents/` to lock the canonical shape, then generalizes from `go-builder-agent.md`, `go-qa-proof-agent.md`, `go-qa-falsification-agent.md`, `go-planning-agent.md` into both go-flavored and language-neutral templates. Sequenced after F24 so the embed.FS pipeline is the delivery mechanism.

### Phase 15 — Late-stage dogfood-refinement file format additions

ta supports TOML and Markdown. Format inferred from path extension at schema-load. Adding a format requires:

1. Recognized-extension entry in the schema loader's extension-map.
2. Backend implementation under `internal/backend/<format>/`.
3. Id-resolution semantics for the format's "section" concept.

Candidate formats, in rough priority order based on tooling-gap and dogfood utility:

- **TXT** — paragraph-or-section addressable; lowest existing structured tooling; high journal/log/brain-dump value.
- **JSON / JSONC** — wide adoption; uniform CRUD across config files where existing JSON tooling is fragmented per-app.
- **YAML** — structured-data peer to TOML; common in CI/CD workflows, k8s manifests, ansible playbooks.
- **`.env`** — flat key=value; very common; trivial parser.
- **Justfile** — recipe DSL; no existing structured-CRUD tool; useful for monorepo recipe rewrites.
- **Dockerfile** — directive blocks; useful for "rewrite base images across many projects" workflows.

Lower priority: Makefile (grammar complexity), INI (existing tools sufficient), per-daemon configs (one grammar each, high cost). Source code formats (Go / Python / TS) NOT in scope — language servers cover that ground.

The format-from-extension inference architecture (introduced in F10) is the foundation that makes adding any of these a localized change rather than a schema-language redesign.

---

## Sequencing summary

```
F10 (T1..T7)
  └─ F15
      └─ F19 + F20
          └─ F21 (.1 .2 .3) + F22 (single release window; F21 lands first within it)
              └─ F23
                  └─ F24 (.1 .2 .3 .4)
                      └─ F18 (subsumes F16, F17)
                          └─ F13
                              └─ F1 + F2 + F2a + F3 + F4 + F5
                                  └─ F8 + F9
                                      └─ F14
                                          └─ F6 (subsumes F7)
                                              └─ agent-MD promotion

Post-v0.1.0 tracks:
  ├─ F12 — temporal / hot-load schema for arbitrary md/toml (its own slice)
  └─ Phase 15 — additional file format support (TXT first, then JSON / YAML / .env / Justfile / Dockerfile per priority)
```

Each F-line is its own slice — single coherent commit (or short commit chain), `mage check` green before every commit, opus QA-twin pair (proof + falsification) review before every commit. Memory rule `feedback_qa_before_every_commit.md` honored throughout. `mage install` is dev-only; never a verification target during any slice. Raw `gofmt` / `gofumpt` never invoked — mage targets only.

v0.1.0 release ships F10 + F15 + F19 + F20 + F21 + F22 + F23 + F24 + F18/F16/F17 + F13 + F1/F2/F2a/F3/F4/F5 + F8/F9 + F14 + F6/F7 + agent-MD. Phase 15 (additional file format support) lands incrementally on its own cadence — TXT first per priority order, others as use cases warrant. F12 ships in a subsequent version on its own track. F11 has no separate slice; its read-path bug dissolves once the bracket form is uniform with the id (T5 in F10).

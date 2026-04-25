# ta — Redesign Plan

> **Status:** design-locked through §13; §11 open questions resolved in
> the 2026-04-21 round. Amendments landed 2026-04-23:
> §3.5 gains PATCH semantics on `update`;
> §12.17.5 reframes the pre-§12.18 rollup as "dogfooding readiness";
> §14.3 softens the absolute-path requirement on CLI `ta init` /
> `ta template apply` (CLI accepts relative; MCP still absolute-only);
> §10.3, §12.10, §12.18 gain rename-historical notes.
> §12.17.5 is the live rollup of dogfooding-readiness items surfacing
> during §12.17 E2E — fluid; dev owns the authoritative list
> out-of-band. This document is a temporary working artifact; when
> implementation is complete it collapses into the project-root
> `README.md` at §12.18 and `docs/` is deleted.
>
> **Naming note.** An earlier iteration of this file was split between
> `docs/PLAN.md` (the MVP plan) and `docs/V2-PLAN.md` (the redesign).
> The MVP is shipped; the redesign is in flight. The MVP plan was
> deleted on 2026-04-23 and V2-PLAN renamed to PLAN, making this the
> single plan source. "v2" appears in schema/code comments as the
> internal delta identifier, and still leaks into a few fang-help
> examples (e.g. `ta template save schema-v2`) — user-facing surfaces
> drift toward "v2-free" wording but are not mechanically purged; fix
> opportunistically.

---

## 1. Motivation

The MVP shipped a working MCP server with `get` / `list_sections` / `schema` / `upsert` over schema-validated TOML. The redesign fixes five issues the MVP exposed:

- **1.1** `upsert` hides intent. "Create a new file" and "mutate an existing one" should be different operations so path typos fail loudly.
- **1.2** File-extension-driven dispatch lets mismatched content and extension mix silently. Schema should own format; agents and humans should not see extensions at all.
- **1.3** Single-format (TOML-only) design rules out MD-backed content. Real projects mix: `CLAUDE.md` / `AGENTS.md` / `README.md` alongside planning and worklog databases.
- **1.4** No search primitive. MCP has no piping, so `ta get | rg` is impossible for agents. Humans can pipe via CLI but agents can't.
- **1.5** Schema is a separate concern from data but currently lives in the same mental model. Treating schema as the single source of docstrings (field `description` + TOML comments) eliminates the need for any external API docs.

---

## 2. Design principles

- **2.1 Schema owns format.** `[plan_db] format = "toml"` binds one db to one format. No extension-based dispatch. No format argument on any tool call.
- **2.2 Agents never see filenames or extensions.** They address records by `<db>.<type>.<id-path>` (§2.9). The tool resolves to disk.
- **2.3 Format is not a user concern after schema creation.** Once the schema says `format = "md"`, everything routes to the MD backend automatically.
- **2.4 DRY backend interface.** Lang-agnostic logic (schema resolution, validation, search, MCP routing, atomic writes) is one package layer above a thin `record.Backend` interface. Each format is a small implementation.
- **2.5 Pure Go, no CGO.** Tree-sitter-markdown is CGO; we stay on a hand-rolled ATX scanner. Cross-compilation and single-binary distribution matter more than handling arbitrary CommonMark edge cases.
- **2.6 One drop, not phased.** This is a pre-1.0 rewrite of the tool surface. Phasing adds coordination cost without meaningful safety — the MVP's scope is small enough to rewrite coherently.
- **2.7 Dogfood.** The schema at `.ta/schema.toml` governs this project's own `README.md`, `CLAUDE.md`, planning records, and worklog. We eat the output.
- **2.8 No doc files after implementation.** Single `README.md`. Every other doc collapses. The schema is the API reference.
- **2.9 Uniform address grammar.** Every record, TOML or MD, single- or multi-instance, is addressed by the same shape: `<db>.<type>.<id-path>` (single-instance) or `<db>.<instance>.<type>.<id-path>` (multi-instance). `<id-path>` is 1+ dot-separated segments. Format does not bleed into address shape. Agents learn one grammar.
- **2.10 Schema-driven sectioning.** The scanner parses between **id-paths matching declared types**, not between raw syntactic markers. A heading or TOML bracket that doesn't match any declared type is body content of the enclosing declared section. This is what lets a TOML record's body carry a TOML code block without the code block's inner `[brackets]` becoming sibling sections, and lets an MD record's body carry subheadings without every subheading having to be a schema-declared type.
- **2.11 Body = bytes to next non-descendant boundary.** A declared record's byte range runs from its start (heading line or bracket line) to the start of the next record at the **same or shallower** level (for MD) or the next **non-descendant bracket** (for TOML), or EOF. Descendants are part of the parent's body — an H3 under an H2 is included in the H2's body bytes; `[plans.task.t1.subtask]` is included in `[plans.task.t1]`'s body bytes. If the deeper path is itself an addressable record (because another type is declared at that level, or because TOML bracket paths allow any depth), it has its **own** byte range, narrower and nested inside the parent's. Ranges can overlap in the native hierarchy; the splice invariant holds per address — each address denotes one exact byte range at that depth.

---

## 3. Tool surface

Seven tools. Hard cut from MVP — no `upsert`, no compat alias. All tool calls take `path` (directory, absolute for MCP, relative-or-absolute for CLI) as their first argument.

Tool split reflects a deliberate asymmetry:

- **Data CRUD** is four separate tools (`get`, `create`, `update`, `delete`) — these run dozens of times per session, so each one having a narrow contract makes path typos fail loudly and instantly.
- **Schema CRUD** is one tool (`schema`) with an `action` parameter — schema ops are rare bootstrap-and-evolve operations, and consolidating keeps the surface smaller without sacrificing clarity.
- **Search** and **list** are read-only navigation primitives, each their own tool.

### 3.1 `get`

Read one record. Default = raw bytes of that record's on-disk section (comments, formatting, exact whitespace preserved, including any descendant records as body).

```
get(path, section, [fields])
  path     — project directory
  section  — "<db>.<type>.<id-path>" — any depth (e.g.
             "plan_db.build_task.task_001", or
             "plans.task.t1.subtask" for a TOML record at bracket
             path [plans.task.t1.subtask], or
             "readme.section.install.prereqs" for an H3 under an H2)
  fields   — optional: array of field names to extract; default = all
             (returns raw bytes). When provided, returns a structured
             subset: {fields: {name: value, ...}}. Filtering happens
             after the backend locates the section; MCP response shape
             switches from raw bytes to JSON object.
```

Behavior: resolve schema cascade from `path` → find the db → dispatch to backend → backend locates the record at the requested id-path → return that record's byte range (including any descendants nested inside it). `<id-path>` is 1+ dot-separated segments; depth is format-natural (TOML: any valid bracket path under the type anchor; MD: ancestor-chain of slugs from the type's anchor heading level down to the target heading). Errors if no record at that id-path exists or the db isn't declared.

If `fields` is supplied, the returned object carries only those named fields; unknown field names error. For MD body-only record types (§5.3.3), `fields = ["body"]` is equivalent to the default (whole body). For TOML records with typed fields, the backend parses and extracts the named fields from the located byte range.

**Scope expansion (§12.17.5 [B2] decision).** `section` may also be a PREFIX address that resolves to multiple records — `<db>`, `<db>.<instance>`, `<db>.<type>` across instances, or `<db>.<instance>.<type>` within one instance. In that case `get` returns every matching record (ordered by file-parse order). CLI renders each record as its own Section block; JSON returns `{"records": [{section, fields}, ...]}`. `--limit <N>` (default 10, `-n` shorthand) and `--all` (boolean, mutex with `--limit`) control the cap. Single-record addresses ignore both flags. Code-side implementation pending.

### 3.2 `list_sections`

Enumerate records. Requires a db path (per §7.5's decision). Scope can narrow to a type, a record prefix, or a single instance of a multi-instance db.

```
list_sections(path, [scope], [limit], [all])
  path   — project directory
  scope  — optional: "<db>" | "<db>.<instance>" | "<db>.<type>" | "<db>.<type>.<id-prefix>"
           (wildcard prefix also accepted: "<db>.reference-*"); default = whole project
  limit  — optional: int, default 10. Caps the returned address count. Endpoint-enforced
           (per §6a.1 parity rule); early-exits the scan once the cap is reached.
  all    — optional: bool, default false. When true, returns every matching address;
           mutually exclusive with `limit` (passing both is rejected by the endpoint).
```

Returns the ordered list of full section addresses under that scope. Multi-instance dbs return instance-qualified addresses (`<db>.<instance>.<type>.<id-path>`).

**§12.17.5 [A2.1] amendment.** `limit` / `all` are endpoint params (not CLI-only post-slice) per §6a.1. Both CLI (`--limit N` / `-n N` / `--all`) and MCP tool accept them; both pass through to the endpoint. Early-exit implementation required — do not materialize the full address set then slice.

### 3.3 `schema`

Inspect or mutate the resolved schema. Single tool with an `action` parameter — schema ops are rare and specialized enough to consolidate behind one tool rather than split into a parallel CRUD surface.

```
schema(path, action, [scope], [kind], [name], [data])
  path    — project directory
  action  — "get" | "create" | "update" | "delete"

  action = "get":
    scope  — optional: "<db>" | "<db>.<type>" | "ta_schema"
    Returns the resolved schema (or scoped subset). Glamour-rendered
    markdown on CLI; structured JSON on MCP.

  action = "create" | "update":
    kind   — "db" | "type" | "field"
    name   — dotted address:
               kind=db    → "<db>"                (e.g. "plan_db")
               kind=type  → "<db>.<type>"         (e.g. "plan_db.build_task")
               kind=field → "<db>.<type>.<field>" (e.g. "plan_db.build_task.id")
    data   — JSON object matching the meta-schema for this kind

  action = "delete":
    kind   — "db" | "type" | "field"
    name   — dotted as above
```

**`data` shape per `kind`** (enforced by the meta-schema in §4.7):

- `kind = "db"`: `{format, description?}` **plus exactly one of** `{file}` (single-instance) **or** `{directory}` (multi-instance). See §4.1 / §5.5.
- `kind = "type"`: `{description, heading?, fields?}` — `heading` required when the owning db has `format = "md"`; `fields` is an optional sub-table of `{<field-name>: {type, required?, description, enum?, default?, format?}, ...}` that seeds the type's initial fields in one atomic mutation. Because §4.7 meta-schema validation requires every type to carry ≥1 field, creating a type without also declaring fields (or having a follow-up `schema(action="create", kind="field", ...)` inside the same atomic window) would fail the post-mutation re-validation and roll back. The `fields` key lets agents land a type + its initial fields in one call; subsequent fields are still added via `schema(action="create", kind="field", ...)`.
- `kind = "field"`: `{type, required?, description, enum?, default?, format?}`.

**`action = "delete"` behavior:**

- `kind = "db"`: errors if any data files still exist for this db on disk. Caller must `delete` (data tool) the on-disk records / files / instance dirs first. Then the schema entry is removed.
- `kind = "type"`: errors if any records of this type still exist. Then the type entry is removed.
- `kind = "field"`: always allowed; field is dropped from the schema. Existing records retain the field in their on-disk bytes but subsequent `get` will not surface it; subsequent `update` will reject a `data` payload containing it.

**`action = "get"` returns** each field entry with its `description`, `type`, `required`, `enum`, and `default` — everything an agent needs to construct a valid `create` or `update` call. The meta-schema itself is readable via `scope = "ta_schema"`.

### 3.4 `create`

Create a new record. Fails if the record already exists. Creates the backing file (and intermediate directories) if missing — this is the only tool that creates files.

```
create(path, section, data, [path_hint])
  path       — project directory
  section    — "<db>.<type>.<id-path>" | "<db>.<instance>.<type>.<id-path>"
  data       — JSON object matching the type's field schema
  path_hint  — optional (collection dbs only): relative path within the
               collection root for the backing file, e.g. "reference/api.md".
               Must stay inside the collection root (no `..` escape).
               When omitted, the flat form is used (`<slug>.<ext>`).
```

Behavior: resolve schema → validate `data` against the type → resolve backing file path from section + db shape (+ optional `path_hint` for collection dbs) → create missing directories and the file if absent → emit the record in the backend's format → splice in → atomic write. Errors if the record already exists in the file, if validation fails, if the db isn't declared, or if the resulting path would collide with an existing instance slug.

### 3.5 `update`

Update an existing record. Fails if the file doesn't exist. Creates the record within the file if it doesn't exist yet (record-level upsert within an existing file).

```
update(path, section, data)
  path     — project directory
  section  — "<db>.<type>.<id-path>" | "<db>.<instance>.<type>.<id-path>"
  data     — JSON object of fields to change (PATCH semantics)
```

**PATCH semantics (§12.17.5 decision).** `data` is a partial overlay, not a full replacement. Provided fields replace their stored values; unspecified fields retain their existing bytes verbatim. Rationale: agents and humans both routinely change one or two fields on a multi-field record — demanding a full re-send burns MCP context budget and invites copy-paste errors.

- **Empty `data` (`{}`).** No-op success: `update` returns the existing record unchanged, touches no bytes. The caller gets a clean success response they can use to confirm the record exists without mutating.
- **Clearing a NOT-required field.** Pass `{"field": null}`. The field is removed from the on-disk bytes.
- **Clearing a required field (no `default`).** Pass `{"field": null}` → errors with `"cannot clear required field <name>"`. Required fields cannot be unset via `update`; change the schema first or delete + recreate the record.
- **Clearing a required field with a schema `default`.** Pass `{"field": null}` → the stored bytes are replaced with the schema default. The merged record still has a value for the field (so validation passes), but the user-supplied value is dropped and the default fills. Semantically equivalent to "reset this field to the declared default".
- **Validation.** After overlay, the merged record is validated against the type's field schema. Any provided field that fails type/enum/format validation rejects the whole update (atomic; on-disk bytes unchanged).

Behavior: resolve schema → require file exists → locate existing record → overlay `data` onto existing fields → validate merged record → splice the record (replace-or-append within the file) → atomic write.

**MCP parity.** The MCP `update` tool uses the same PATCH semantics as the CLI. `create` (§3.4) remains full-required — no prior state to overlay — though fields with a schema `default` may be omitted.

### 3.6 `delete`

Remove a record, a data file, or a multi-instance dir. Never touches the schema — use `schema(action="delete")` for that.

```
delete(path, section)
  path     — project directory
  section  — address to remove; see levels below
```

**Address levels:**

- **`<db>.<type>.<id-path>`** / **`<db>.<instance>.<type>.<id-path>`** — remove just that record's bytes from the file. Leaves the file on disk even if empty. `<id-path>` may be multi-segment for deep TOML bracket paths.
- **`<db>`** (single-instance db only) — remove the entire data file (`plans.toml`, `README.md`).
- **`<db>.<instance>`** (`directory` db) — remove the entire instance directory (`workflow/drop_3/`).
- **`<db>.<instance>`** (`collection` db) — remove the single backing file (`docs/reference/api.md`). Empty parent dirs are left in place; prune manually if desired.
- **`<db>`** (multi-instance db) — **errors.** Ambiguous; the caller must either delete each `<db>.<instance>` individually first, or (if intent is "drop this db type entirely") route through `schema(action="delete", kind="db")` after zeroing out instances.

All deletes are atomic. The schema entry is untouched in every case — drop the schema separately if you also want the type gone.

### 3.7 `search`

Structured + full-text search across records. No MCP piping, so this is the native search primitive.

```
search(path, [scope], [match], [query], [field], [limit], [all])
  path   — project directory
  scope  — optional: "<db>" | "<db>.<type>" | "<db>.<type>.<id-prefix>"; default = whole project
  match  — optional: { field-name: exact-value, ... } exact-match on typed fields (enum, string, bool, int)
  query  — optional: Go regexp (RE2) matched against string fields (including body)
  field  — optional: restrict `query` to one named field; default = all string fields
  limit  — optional: int, default 10. Caps the returned hit count. Endpoint-enforced
           (per §6a.1 parity rule); early-exits the scan once the cap is reached.
  all    — optional: bool, default false. When true, returns every matching record;
           mutually exclusive with `limit` (passing both is rejected by the endpoint).
```

Returns full matching records. No byte ranges, no snippets — whole sections come back so the caller can read verbatim. (YAGNI: we may add ranges/snippets post-MVP if agents want narrower context.)

For multi-instance dbs, `scope = "<db>"` searches across **all instances** (union); narrow with `scope = "<db>.<instance>"` to restrict to one.

**§12.17.5 [A2.1] amendment.** `limit` / `all` are endpoint params (not CLI-only post-slice) per §6a.1. Both CLI (`--limit N` / `-n N` / `--all`) and MCP tool accept them; both pass through to the endpoint. Early-exit implementation required — do not materialize the full hit set then slice.

Worked example in §7.2.

---

## 4. Schema language

### 4.1 Shape

The schema file lives at `.ta/schema.toml`. Top-level tables are **databases**. Sub-tables under a db are **record types**. Sub-tables under a record type (named `fields`) are **fields**.

A db has one of three shapes — **single-instance**, **dir-per-instance** (drops / logical buckets), or **file-per-instance** (pages / named content) — selected by which root key is set. See §5.5 for multi-instance semantics.

```toml
[<db>]                              # db metadata
format = "toml" | "md"              # required: canonical format
description = "..."                 # recommended

# Exactly one of the following three keys:
file = "..."                        # single-instance: relative path from
                                    # project root (e.g., "README.md",
                                    # "plans.toml"). Extension must match
                                    # `format`.
directory = "..."                   # dir-per-instance: relative path from
                                    # project root (e.g., "workflow").
                                    # Each immediate child dir that holds a
                                    # `db.toml` or `db.md` (matching
                                    # `format`) is one instance. Canonical
                                    # filename `db.<ext>` required; the
                                    # subdir IS the instance identity.
collection = "..."                  # file-per-instance: relative path from
                                    # project root (e.g., "docs"). Every
                                    # file under this dir (recursively)
                                    # whose extension matches `format` is
                                    # one instance. Filename IS the
                                    # identity; nested dirs are
                                    # organizational. Slug = relative path
                                    # stripped of extension, path
                                    # separators joined with hyphens.

[<db>.<type>]                       # record type
description = "..."
# MD-only: heading = 1..6 (declares which heading level this type represents)

[<db>.<type>.fields.<field>]        # one field
type = "string" | "integer" | "float" | "boolean" | "datetime" | "array" | "table"
required = true | false             # default false
description = "..."
enum = [...]                        # optional
default = ...                       # optional
format = "markdown"                 # optional hint; informational only —
                                    # see §13 for CLI rendering semantics
```

### 4.2 Why top-level = db

The MVP used `[schema.<type>]` — the `schema.` prefix was redundant namespace bookkeeping. In v2 the schema file has one job, so top-level tables *are* the db entries. Names no longer collide with a fixed prefix; cascades remain clean because cascade merging keys on the db name directly.

### 4.3 Field docstrings

Every `description = "..."` string becomes the authoritative docstring for that field, surfaced through `schema`. TOML comments above the field are for humans reading the schema file; they do not round-trip to the agent.

### 4.4 Cascade resolution

Unchanged from MVP: `~/.ta/schema.toml` is the base layer; every `.ta/schema.toml` found on the target path's ancestor chain folds on top, root-to-file. Same-named dbs override; unique dbs are additive. Inside a db, the closer cascade layer's entry wholly replaces outer entries.

### 4.5 Schema mutation via the `schema` tool

Schemas are created, updated, and deleted through the single `schema` tool with an `action` parameter — see §3.3 for the full signature.

Worked examples:

- `schema(action="create", kind="db", name="plan_db", data={format: "toml", directory: "workflow", description: "..."})` — declares a new multi-instance db.
- `schema(action="create", kind="type", name="plan_db.build_task", data={description: "..."})` — declares a type under it.
- `schema(action="create", kind="field", name="plan_db.build_task.id", data={type: "string", required: true, description: "..."})` — declares a field.
- `schema(action="update", kind="field", name="plan_db.build_task.status", data={enum: ["todo","doing","review","blocked","done"], ...})` — changes a field.
- `schema(action="delete", kind="field" | "type" | "db", name="...")` — removes an entry (with the cascading rules in §3.3).

Consolidating schema ops behind one tool (vs splitting into `schema_create` / `schema_update` / `schema_delete`) reflects the asymmetry noted at the top of §3: data CRUD is called per-record dozens of times per session; schema ops are rare bootstrap-and-evolve operations. One tool with an action switch keeps the surface smaller without sacrificing clarity.

The schema format is itself governed by a **meta-schema** that lives in the tool binary (not on disk). The meta-schema describes what a valid `data` payload looks like for each `kind`, and is itself readable via `schema(action="get", scope="ta_schema")` — uniform API, no help-text-vs-data split.

### 4.6 In-memory cache (MCP lifecycle)

- MCP server loads and resolves the schema cascade **once at startup** for the project it's configured in.
- On every tool call the server `os.Stat`s each cascade file and compares mtime to the cached mtime. If any file's mtime changed, it reloads the cascade before serving. Zero new deps (stdlib `os.Stat`); handles `git checkout` switching branches with different schemas.
- Non-mutating tools (`get`, `list_sections`, `schema` with `action="get"`, `search`) use the cached schema after the mtime check.
- Mutating data tools (`create`, `update`, `delete`): use the current cached schema for validation, then perform the atomic write.
- Mutating schema tool (`schema` with `action="create" | "update" | "delete"`): on success, invalidate → re-resolve cascade → re-validate via the meta-schema → if the new schema is malformed, the mutation is rolled back atomically (pre-mutation bytes kept in memory during the transaction).

### 4.7 Meta-schema validation

The tool enforces:

- Every top-level table has `format` ("toml" | "md") and **exactly one** of `file` (single-instance), `directory` (dir-per-instance), or `collection` (file-per-instance). `description` recommended.
- `file`: canonical extension must match `format` (`file = "plans.toml"` requires `format = "toml"`).
- `directory`: relative to project root; the dir itself need not exist at schema-declare time (created on first `create` into one of its instances).
- `collection`: relative to project root; the dir itself need not exist at schema-declare time (created on first `create` with a section under it). Scanning is recursive. Dotfiles and files whose extension does not match `format` are ignored.
- Every sub-table under a db that is not `fields` is a record type. Types have `description`; MD types have `heading` (int 1..6).
- Every `fields.<name>` sub-table has `type` (one of the seven supported types). `description`, `required`, `enum`, `default`, `format` optional.
- No two dbs at one cascade layer may point to the same path via `file`, `directory`, or `collection`, nor may one db's path be a prefix of another's (no nested dbs).
- Record types within one db may not share the same `heading` value (MD only).

Violations fail the `schema` tool mutation with a structured error and the schema on disk is never touched.

---

## 5. Backends

### 5.1 The `record.Backend` interface

All format-specific work sits behind one interface. Everything above it is lang-agnostic.

Backends are **schema-aware at construction** (per §2.10): the factory takes the list of declared types for the db so the scanner can recognize which headings/brackets are record boundaries and which are content. Non-declared markers between two declared records belong to the first record's body.

```go
package record

// Record is the validated, format-neutral representation of a single
// record's fields: JSON-shaped, keyed by field name.
type Record map[string]any

// Section is a backend's view of one on-disk record.
type Section struct {
    Path   string     // full address "<db>.<type>.<id-path>"
    Range  [2]int     // byte range in the file buffer — from this
                      // declared record's start to the next declared
                      // record's start (or EOF)
    Record Record     // parsed fields (nil until Load)
}

// DeclaredType is the minimum schema info a backend needs to section a
// buffer. Each backend interprets the fields per its format:
//   TOML: Name = "<db>.<type>" bracket-path prefix. Heading is ignored.
//   MD:   Heading = 1..6 heading level. Name is the type name used when
//         composing addresses for records at that level.
type DeclaredType struct {
    Name    string
    Heading int
}

type Backend interface {
    // List returns every declared-record address under scope (or all
    // declared records if scope == ""). Non-declared markers in the
    // buffer are ignored (content, not structure).
    List(buf []byte, scope string) ([]string, error)

    // Find locates one declared record by full address.
    Find(buf []byte, section string) (Section, bool, error)

    // Emit serializes a validated record to this format's canonical bytes
    // for the given address. Includes the heading/header line.
    Emit(section string, rec Record) ([]byte, error)

    // Splice replaces (or appends) a declared record's bytes in buf,
    // preserving every byte outside the touched range verbatim.
    Splice(buf []byte, section string, emitted []byte) ([]byte, error)
}

// Each backend package provides:
//   func NewBackend(types []DeclaredType) Backend
// The types slice is the full list of declared types in the owning db.
// Callers rebuild the backend when the schema cascade reloads.
```

### 5.2 TOML backend

The current `internal/tomlfile/` package moves behind this interface. Named `internal/backend/toml/`.

Under §2.10 the TOML backend is schema-driven at the TYPE ANCHOR: after pelletier parses the file, every bracket whose path starts with a declared-type prefix (`<db>.<type>.…`) is addressable. Brackets outside any declared-type prefix are ignored (not addressable, not sections).

Under §2.11 the BYTE RANGE of an addressable bracket runs from its header line to the start of the **next non-descendant bracket** (or EOF). Descendant brackets — those whose path is a strict prefix-extension of this bracket's path — are part of this bracket's body bytes. This is what lets `get` on a parent return the whole subtree, and `get` on a child return just the child's range:

```toml
[plans.task.t1]
title = "parent"
body = "some body"

[plans.task.t1.notes]
note1 = "..."
note2 = "..."

[plans.task.t2]
title = "next sibling"
```

With schema declaring `[plans.task]` as a type:

- `get(section="plans.task.t1")` returns the bytes from `[plans.task.t1]` header to the start of `[plans.task.t2]` — the `[plans.task.t1.notes]` bracket and its key-values are INCLUDED as body (it's a descendant of `plans.task.t1`).
- `get(section="plans.task.t1.notes")` returns just the bytes of that bracket, nested inside `t1`'s range.
- `get(section="plans.task.t2")` returns bytes from `[plans.task.t2]` to EOF.

Both `t1` and `t1.notes` are addressable — calling `get` on either returns the bytes at that depth. Ranges nest in the native hierarchy. Splice on any address modifies exactly that address's byte range, leaving everything outside (including sibling subtrees and ancestor surroundings) untouched.

**`update(section="plans.task.t1", …)`** replaces the whole subtree of `t1` including `t1.notes`. **`update(section="plans.task.t1.notes", …)`** replaces just the notes subtree, preserving the parent's `title` / `body` keys and any sibling brackets. TOML's native bracket-path uniqueness (enforced by pelletier) guarantees each address maps to exactly one byte range.

**Non-descendant brackets between declared records.** A bracket like `[unrelated.thing]` sitting between two declared-type brackets belongs to the first declared record's body (it's non-descendant, but the body range extends to the next non-descendant bracket at the SAME OR SHALLOWER anchor depth — see the fenced-code-in-TOML use case). The practical upshot: authors can write bookkeeping brackets inside a record's body without each becoming a phantom sibling.

### 5.3 MD backend — pure-Go ATX scanner

New package `internal/backend/md/`.

#### 5.3.1 Why ATX scanner, not tree-sitter

- Pure Go. No CGO. Cross-compiles to any Go target with `GOOS=... GOARCH=... go build`.
- No grammar dependency. tree-sitter-markdown has drifted across forks; the scanner cannot drift.
- Small code (~200–300 lines). Tree-sitter-markdown pulls in a grammar binary plus bindings.
- Full parity for what we need: section boundaries by ATX heading, fenced-code-block awareness, byte ranges. We do not care about emphasis, links, tables, etc. — those are all inside the body string.
- Constrained input: since agents write all content through `create` / `update`, the tool controls what ends up on disk. Edge cases (setext, HTML blocks, nested blockquotes containing headings) are tool-emitted never, so the scanner can be strict.

#### 5.3.2 Section model — schema-declared headings are sections; body includes descendants

Per §2.10 / §2.11, the scanner is schema-driven and the body-range rule is hierarchical:

- A heading `# Text` through `###### Text` is an **addressable record** only when its level matches a declared type's `heading` value. Headings at non-declared levels are body content of the enclosing declared record.
- Each declared type maps exactly one heading level to a type name (`[readme.section] heading = 2` says "every H2 in this db is a `section` record").
- **Addressing**. A declared record is addressed by its ancestor-chain of declared-level slugs starting at the type's anchor heading level:
  - A bare H2 record with schema `[readme.section] heading = 2` → `readme.section.<h2-slug>`.
  - If the schema also declares `[readme.subsection] heading = 3`, an H3 under an H2 → `readme.subsection.<h2-slug>.<h3-slug>`. The address is `<type>.<id-path>` where `<id-path>` is the chain of ancestor slugs through declared levels. Scope for uniqueness is per-parent: two H3s under the same H2 cannot share a slug; two H3s under different H2s are fine.
- **Byte range**. A declared record's byte range runs from its heading line to the start of the next heading at the **same or shallower declared level** (or EOF). Deeper headings — declared or not — are part of this record's body bytes.
  - An H2's range ends at the next H2 (or H1 if declared, or EOF). Any H3/H4/H5/H6 between them is inside the H2's body.
  - If H3 is also a declared type, an H3's range ends at the next H3 or H2 (or H1 if declared) — deeper H4-H6 under the H3 are part of the H3's body.
- **`get` on a parent returns the full subtree.** `get(readme.section.install)` returns the whole H2 block including any nested H3s. `get(readme.section.install.prereqs)` (assuming subsection is declared) returns just the H3 block, nested inside the H2's range. Ranges overlap in the native hierarchy; splice on any address modifies exactly that address's byte range.
- **Slug uniqueness**: per parent scope, per declared level. Two H2s with slug `install` → collision (refused at read + write). An H2 `install` and an H3 `install` → no collision (different levels). Two H3s `prereqs` under different H2s → no collision.
- **Non-declared subheadings are opaque content bytes.** Authors can use H3–H6 freely inside a record body without declaring each heading as a schema type. Author discipline (don't write confusing duplicate subheadings) is a human-readability concern, not scanner-enforced.

**Example.** Given schema `[readme.title] heading = 1` and `[readme.section] heading = 2` (no deeper types declared) and the file:

```md
# ta

Tiny MCP server for schema-validated TOML and Markdown.

## Installation

Install from source:

    mage install

### Prerequisites

A Go toolchain.

### Troubleshooting

If `mage install` fails, ...

## MCP client config

...
```

- `get(readme.title.ta)` → `"# ta\n\nTiny MCP server for schema-validated TOML and Markdown.\n\n"` (ends at next H1 or shallower; since H2 Installation is a different level, H1 ta's range only contains the prose between `# ta` and `## Installation`).
- `get(readme.section.installation)` → `"## Installation\n\nInstall from source:\n\n    mage install\n\n### Prerequisites\n\nA Go toolchain.\n\n### Troubleshooting\n\nIf `mage install` fails, ...\n\n"` — both H3s are body content (no H3 type declared). Range ends at `## MCP client config` (next H2).
- `get(readme.section.mcp-client-config)` → `"## MCP client config\n\n...\n"`.

If the schema later adds `[readme.subsection] heading = 3`:

- `readme.subsection.installation.prerequisites` and `readme.subsection.installation.troubleshooting` become addressable records (scoped under the `installation` H2 parent).
- `get(readme.section.installation)` STILL returns the whole H2 block including the two H3s — the parent's range doesn't shrink; the H3s now just have their own narrower nested ranges too.
- `get(readme.subsection.installation.prerequisites)` returns just the H3 block.

**Orphan records — existing-only, strict on write.** An "orphan" is a declared-level heading whose declared ancestor chain in the buffer is incomplete — e.g. an H3 sitting directly under an H1 when the schema declares `[<db>.title] heading = 1`, `[<db>.section] heading = 2`, AND `[<db>.subsection] heading = 3`, but the document has no H2 between the H1 and the H3. Orphans arise from hand-edited legacy files or mid-write transient states; schema-authored output rarely produces them.

- **Read.** The scanner emits an orphan with a chain composed from the declared ancestor slugs that ARE present in the buffer, skipping the empty slot for the missing level. An H3 `prereqs` under H1 `ta` (no H2) resolves to `<db>.subsection.ta.prereqs`. `get` / `update` / `delete` on an existing orphan address succeed via exact-address match — these paths do not consult ancestor lookup.
- **Write.** `create` (and `Splice` of a NEW record) at an orphan address fails with `ErrParentMissing`. The resolver looks for a heading at the next-shallower declared level (regardless of whether that slug is present in the orphan chain) and refuses to insert when it's absent. To add a second orphan sibling, the caller must first materialize the missing declared ancestor (e.g. `create` the H2 at `<db>.section.ta`), then retry the subsection `create`.
- **Rationale.** Tool-authored output stays schema-consistent. Legacy hand-edited orphans remain readable so this pre-v0.1.0 policy does not break existing files; extensions to those files route through the declared hierarchy. "Fail loudly on write" aligns with §1.1 and §2.10 — the write path is the surface where typo or missing-ancestor intent can still be caught and corrected.

#### 5.3.3 MVP field layout — body only

MD record types have **one field**: `body`. The heading text serves as the record id (via slug). There is no `title` field because the heading text already is the address.

On `create` / `update`, the tool:

1. Takes `section = "readme.section.installation"` and `data = {body: "Install from source:\n\n    mage install\n"}`.
2. Looks up `readme.section`'s `heading` in schema (e.g., 2) and unslugifies `installation` → `"Installation"`.
3. Emits `"## Installation\n\nInstall from source:\n\n    mage install\n"`.
4. Splices it in.

`get` reverses this: returns the raw heading-plus-body bytes. The caller parses the `body` field by stripping the heading line if they want just the body.

#### 5.3.4 Post-MVP: typed fields via fenced TOML frontmatter

If record types ever need typed fields beyond `body`, the **fenced TOML frontmatter block** approach applies:

```md
## Installation

```toml ta-fields
order = 2
tags = ["setup", "build"]
```

Install from source:

    mage install
```

The ` ```toml ta-fields ` opener identifies a schema-field block; fields inside are decoded as TOML and mapped to typed fields in the record's JSON. Body is everything outside the block.

**Rejected alternative — subheading encoding:**

```md
## Installation

### order

2

### intro

Install from source:

### example

    mage install
```

Rejected because fenced code blocks inside subheading-delimited fields collide with the "fenced code = raw body content" expectation, and nested headings are ambiguous when body content legitimately contains its own subheadings.

**MVP rule:** ship with body-only. Add fenced-TOML frontmatter only when a real schema requires typed MD fields.

#### 5.3.5 Edge cases the scanner handles

- **Fenced code blocks** (```` ```lang ```` or `~~~lang`): `#` lines inside them are content, not headings. Scanner tracks fence open/close state.
- **Indented code blocks** (4+ leading spaces): `#` at column 4+ is not a heading. Scanner requires heading start at column 0.
- **Setext headings** (`Heading\n====`): on read, scanner ignores them. On write (tool-emitted), we never produce them. If a human hand-edits and introduces one, `get` won't see it as a section — human error, documented as a limitation.
- **HTML blocks**: out of scope for MVP. Tool-emitted content never contains raw HTML blocks.
- **Non-declared heading levels**: syntactically valid ATX headings at levels that no declared type claims are **content**, not section boundaries (per §5.3.2 / §2.10). They don't terminate the enclosing declared section's body.

### 5.4 Extension path for new backends

Adding a third format (YAML, JSONL, plain text) means one new package implementing `record.Backend`, one new entry in a format → backend map, and nothing else. No change to schema resolution, validation, search, or MCP routing.

### 5.5 Multi-instance addressing and file placement

Two multi-instance modes exist because two semantically distinct use cases exist:

- **Dir-per-instance** (`directory = "..."`) — logical bucket. The subdir IS the identity; the file inside is bookkeeping. Good for opaque drops / projects / workflows where an "instance" is a conceptual grouping.
- **File-per-instance** (`collection = "..."`) — named content. The filename IS the identity; dirs are organizational. Good for docs pages, blog posts, notes — anything whose file name is meaningful.

**Address grammar by db shape:**

| Db shape                       | Address                                  |
|--------------------------------|------------------------------------------|
| Single-instance                | `<db>.<type>.<id-path>`                  |
| Dir-per-instance               | `<db>.<instance>.<type>.<id-path>`       |
| File-per-instance              | `<db>.<instance>.<type>.<id-path>`       |

Tools resolve which form applies by looking up the db's declaration in the cascade. `<id-path>` is **1+ dot-separated segments**, uniform across both formats, and may be any depth matching the file's native hierarchy:

- **TOML:** `<id-path>` is the bracket tail after `<type>`. Any bracket path in the file under the type anchor is addressable at any depth (`t1`, `t1.subtask`, `a.b.c.d`). `get` on a parent path returns the whole subtree (descendants included in the body); `get` on a child path returns just the child's range (nested inside the parent's).
- **MD:** `<id-path>` is the ancestor-chain of slugs through declared heading levels. A bare H2 with type `section` → `<db>.section.<h2-slug>`. If schema also declares H3 as type `subsection`, an H3 under an H2 → `<db>.subsection.<h2-slug>.<h3-slug>`. Non-declared deeper headings are body content of the enclosing declared record.
- **Ranges nest.** `get(<db>.<type>.parent)` and `get(<db>.<type2>.parent.child)` both work; the second returns a narrower byte range nested inside the first. Splice on any address modifies exactly that address's byte range.
- **Typo detection.** An id-path that does not correspond to any record in the resolved file errors loudly with "no record at `<address>`". The schema-driven scanner makes this check unambiguous: a deeper path that doesn't match an actual bracket/heading in the file fails loud, instead of silently promoting to a deeper content slice.

#### 5.5.1 Dir-per-instance (`directory`)

Each immediate subdir of the declared directory containing a canonical `db.toml` (or `db.md` per `format`) is one instance. The subdir name is the instance slug (kebab-case).

**Auto-creation on `create`.** `create(section="plan_db.drop_3.build_task.task_001", ...)` where `drop_3` does not yet exist atomically:

1. Creates `workflow/drop_3/` if missing.
2. Creates `workflow/drop_3/db.toml` (canonical filename) if missing.
3. Splices the record in.

**Canonical filename required; no per-db filename configuration.** If a project wants two distinct dbs in one dir, declare them as two separate single-instance dbs at those paths — don't overload one instance dir with multiple db files.

**Deletion.** `delete(section="plan_db.drop_3")` removes the entire `workflow/drop_3/` directory. `delete(section="plan_db.drop_3.build_task.task_001")` removes just that record's bytes from the file.

#### 5.5.2 File-per-instance (`collection`)

Every file under the declared directory (recursively) whose extension matches `format` is one instance. Dotfiles and mismatched extensions are skipped.

**Slug derivation** — path from the collection root with extension stripped, path separators joined with hyphens, each segment kebab-cased:

| On-disk path                        | Instance slug          |
|-------------------------------------|------------------------|
| `docs/installation.md`              | `installation`         |
| `docs/getting-started.md`           | `getting-started`      |
| `docs/reference/api.md`             | `reference-api`        |
| `docs/tutorial/first-steps.md`      | `tutorial-first-steps` |
| `docs/a/b/c/d.md`                   | `a-b-c-d`              |

**Auto-creation on `create` — flat vs nested.** The slug alone is ambiguous (`reference-api` could map to `docs/reference-api.md` OR `docs/reference/api.md`). Disambiguation via the optional `path_hint` parameter:

- `create(section="docs.reference-api.section.endpoints", data={...})` — no hint → creates `docs/reference-api.md` (flat; default).
- `create(section="docs.reference-api.section.endpoints", path_hint="reference/api.md", data={...})` — hint → creates `docs/reference/api.md` (nested; explicit).

Intermediate directories are created as needed. On subsequent calls to an existing instance, `path_hint` must be omitted or match the existing path — changing a path is a manual rename, not a tool operation.

**Deletion.** `delete(section="docs.reference-api")` removes the single backing file. Empty parent directories are left in place; prune manually if desired. To remove a whole category (`docs/reference/*`), enumerate via `list_sections(scope="docs.reference-*")` and delete each returned instance.

**Collision handling.** Slug collisions fail at both write time (`create` refuses the operation) and read time (`list_sections` / `search` / `get` error with `slug collision: "<slug>" maps to both <path1> and <path2>`). No auto-rename, no silent disambiguation. Consistent with §11.5 MD heading-slug rule.

#### 5.5.3 Listing and search scoping

- `list_sections(scope="<db>")` on any multi-instance db returns instance-qualified addresses across all instances.
- `list_sections(scope="<db>.<instance>")` narrows to one instance.
- `list_sections(scope="<db>.<prefix>-*")` — prefix-glob wildcard on the instance slug (e.g. `docs.reference-*`). Supported for both multi-instance modes.
- `search(scope="<db>")` performs cross-instance union; `search(scope="<db>.<instance>")` narrows to one instance.

---

## 6. Package layout

```
main/
├── cmd/
│   └── ta/
│       ├── main.go                    # fang + laslig wiring; dispatch to mcpsrv or CLI subcommand
│       └── commands.go                # CLI subcommands (one per tool)
├── internal/
│   ├── schema/                        # lang-agnostic: Registry, Type, Field, Validate
│   ├── config/                        # lang-agnostic: cascade resolution of .ta/schema.toml
│   ├── db/                            # lang-agnostic: db + type + section address resolution
│   ├── record/                        # lang-agnostic: Backend interface
│   ├── backend/
│   │   ├── toml/                      # TOML backend (moved from internal/tomlfile)
│   │   └── md/                        # MD backend (new; ATX scanner)
│   ├── search/                        # lang-agnostic: structured + regex search over backends
│   ├── fsatomic/                      # atomic writes, path canonicalization
│   └── mcpsrv/                        # MCP routing; resolves schema → dispatches to backend
├── examples/
│   └── schema.toml                    # sample schema (copy to ~/.ta/schema.toml or use as a template)
├── .ta/
│   └── schema.toml                    # project-level dogfood schema (governs this repo)
├── magefile.go
├── go.mod
├── go.sum
├── LICENSE
└── README.md                          # final single-source doc, post-collapse
```

Dependency direction: `cmd/ta` → `mcpsrv` → `{config, schema, db, search, backend/*, fsatomic}`. Backends depend on nothing except `schema` and `record`. Zero cycles.

**Post-[B0] layout.** §12.17.5 [B0] splits `internal/mcpsrv/` into two packages: `internal/ops/` (domain endpoints — plain Go functions with no MCP dep; the shared surface both CLI and MCP call into) and `internal/mcpsrv/` (trimmed to MCP protocol glue only — `Server` struct, stdio run loop, tool declarations, tool handlers that call `internal/ops.*`). Post-[B0] dependency chain: `cmd/ta` → `internal/ops` and `internal/mcpsrv` → `internal/ops`. Both adapters over one endpoint layer. See §6a for the full decoupling principle.

---

## 6a. CLI/MCP decoupling principle

The CLI (`cmd/ta/`) and MCP tool handlers (`internal/mcpsrv/tools.go`) are both **presentation adapters** over a shared endpoint layer (`internal/ops/` post-[B0]; `internal/mcpsrv/*.go` pre-[B0]). The endpoint layer owns all semantics: path resolution, scope walking, filters, limits, validation, splice, write. The adapters add nothing beyond I/O marshaling.

- **CLI adapter** (`cmd/ta/`) — parses flags via cobra, calls the endpoint, renders the result through laslig (or JSON when `--json` is set).
- **MCP adapter** (`internal/mcpsrv/tools.go`) — parses tool params from the MCP protocol JSON, calls the endpoint, wraps the result in MCP content.

### 6a.1 Parity rule

Any capability on one adapter is presumed to exist on the other. Asymmetries are documented with explicit justification. Acceptable asymmetries today:

- **TTY-only UX** — huh interactive forms on `ta create` / `ta update` / `ta init` picker; `ta` bare command's huh subcommand menu. MCP agents send JSON directly; they don't have a TTY.
- **Render polish** — CLI uses laslig + glamour for markdown fields; MCP returns raw structured data (§13.3). Rendering is strictly a presentation concern.
- **Template library management** — `ta template list | show | save | apply | delete` is CLI-only per §14.2's four-boundary justification (scope / agency / temporal / trust).

### 6a.2 Endpoint package — `internal/ops/`

Plain Go functions. No dependency on cobra, fang, huh, or mark3labs/mcp-go. Takes basic types (`string`, `int`, `bool`, `[]byte`, `map[string]any`, typed struct params). Returns plain data + `error`. Unit-testable without protocol plumbing.

Every data/schema/search/list endpoint lives here. When adding a new parameter (e.g. `limit int, all bool` for [A2.1] / [B2]), add it to the endpoint signature; both adapters pass it through. Never apply filters at the adapter level.

### 6a.3 MCP package — `internal/mcpsrv/`

Strictly the MCP protocol glue after [B0]:

- `server.go` — `Server` struct, `New(Config) (*Server, error)`, `Run(ctx) error` (stdio transport).
- `tools.go` — tool declarations for mark3labs/mcp-go + handlers that parse tool params, call `internal/ops.*`, and marshal the result into MCP content. Handlers are thin: one function call per tool in the ideal case.

No domain logic. If you're writing something that isn't "parse MCP params / call ops / marshal back", it belongs in `internal/ops/`.

---

## 7. Search — full spec

### 7.1 Tool shape

```
search(path, [scope], [match], [query], [field])
```

- `path` — project directory (required).
- `scope` — prefix filter: `<db>`, `<db>.<type>`, or `<db>.<type>.<id-prefix>`. Default = whole project.
- `match` — object of `{field: exact-value}` pairs. Exact-match on typed fields (string, enum, bool, int, datetime). All pairs must match (AND).
- `query` — regex (Go RE2) matched against string-typed fields.
- `field` — optional: restrict `query` to one field. Default = all string fields in the matched record type.

All filters are AND-combined. `match` runs first (cheap, indexable-ish); `query` runs only on records that passed `match`.

Result = full record sections (as from `get`). No byte ranges, no snippets — YAGNI until agents ask for them.

### 7.2 Worked example

Given `plans.toml`:

```toml
[build_task.task_001]
id = "TASK-001"
status = "todo"
owner = "alice"
body = """
## Approach

Rewrite the ATX scanner to handle fenced code blocks.
Look for similar logic in cmd/X.
"""

[build_task.task_002]
id = "TASK-002"
status = "doing"
owner = "bob"
body = """
## Approach

Migrate the mcpsrv tools.
"""

[build_task.task_003]
id = "TASK-003"
status = "todo"
owner = "alice"
body = """
## Approach

Write the search implementation.
"""

[qa_task.qa_001]
id = "QA-001"
parent_build_task = "TASK-001"
kind = "proof"
status = "todo"
body = "Verify scanner handles fenced code blocks."
```

**Tool call:**

```json
{
  "name": "search",
  "arguments": {
    "path": "/abs/project",
    "scope": "plan_db.build_task",
    "match": {"status": "todo", "owner": "alice"},
    "query": "scanner",
    "field": "body"
  }
}
```

**Interpretation:** "find every `build_task` whose status is `todo` AND owner is `alice` AND whose `body` field matches regex `scanner`."

**Result:** `task_001` matches (status + owner + body contains "scanner"). `task_003` fails the regex. `task_002` fails the match. `qa_001` is out of scope.

```json
{
  "hits": [
    {
      "section": "plan_db.build_task.task_001",
      "record": {
        "id": "TASK-001",
        "status": "todo",
        "owner": "alice",
        "body": "## Approach\n\nRewrite the ATX scanner to handle fenced code blocks.\nLook for similar logic in cmd/X.\n"
      }
    }
  ]
}
```

**Variations:**

- No `query`, only `match`: "find all todos owned by alice." Returns two records (task_001, task_003).
- No `match`, only `query`: "find anything mentioning 'scanner' anywhere in the project." Scans all string fields across all types.
- No filters at all: returns every record under `scope` (or the whole project). Effectively `list_sections` but with full records.

---

## 8. MCP server lifecycle

### 8.1 Project-scoped MCP config

Best practice: register the `ta` MCP server per-project, in `.claude/` or `.codex/` (whichever client the user runs). Each project has its own MCP server process with its own in-memory schema cache, rooted at the project directory. This avoids:

- Cross-project schema cache pollution.
- Stale cache when switching between projects.
- The "which schema did that resolve to?" ambiguity a global instance creates.

Registration examples (exact form depends on client — `claude mcp add --scope project ta -- ta` for Claude Code).

### 8.2 Schema cache

On MCP server startup:

1. Resolve `path` (the project directory the server is rooted at).
2. Walk the cascade: `~/.ta/schema.toml` + every `.ta/schema.toml` on the ancestor chain of `path`.
3. Merge into one resolved schema.
4. Run meta-schema validation (§4.7). If the resolved schema is malformed, the server logs a startup error and refuses to start — better to fail at boot than to serve bad data.
5. Hold the result in memory for the process lifetime.

Non-mutating tools read the cached schema directly. Zero disk hits per call.

### 8.3 Invalidation

- On successful `create` / `update` against a `ta_schema.*` section, the server:
  1. Writes the new schema bytes atomically.
  2. Re-reads and re-resolves the cascade.
  3. Re-validates via the meta-schema.
  4. If valid, swaps the cache atomically under a sync.RWMutex.
  5. If invalid, rolls back the write and returns a structured error. (This requires keeping the pre-mutation bytes in memory during the transaction.)
- On out-of-band edits (user runs `$EDITOR .ta/schema.toml` directly), the cache goes stale. Recovery: user restarts the MCP server. Documented limitation.

---

## 9. Dogfood schema — the target shape

This is the schema that governs **this project** once v2 lands. Stored at `/main/.ta/schema.toml`. Replaces the current MVP-era example schema.

```toml
# ============================================================================
# ta project schema — the dogfood schema. Governs README, CLAUDE.md,
# planning records, and the worklog. Agents interact via `create`, `update`,
# `get`, `list_sections`, `search`, never by touching these files directly.
# ============================================================================

# ----------------------------------------------------------------------------
# README — single-source project doc. One H1 title section at top; the rest
# are H2 sections.
# ----------------------------------------------------------------------------
[readme]
file = "README.md"
format = "md"
description = "Single-source project overview. Replaces all docs/*.md files."

[readme.title]
heading = 1
description = "The H1 at the top of README.md. Exactly one per file."

[readme.title.fields.body]
type = "string"
format = "markdown"
description = "Prose directly under the H1, before the first H2. Tagline plus short intro."

[readme.section]
heading = 2
description = "One H2 section. Addressed by its heading slug. Example: 'installation', 'mcp-client-config', 'schema-language'."

[readme.section.fields.body]
type = "string"
format = "markdown"
description = "Prose under this H2, until the next H2 or EOF. Markdown with fenced code, tables, deeper subheadings allowed."

# ----------------------------------------------------------------------------
# agents — CLAUDE.md / AGENTS.md. Same H1-plus-H2 shape. The schema
# describes the agent-rules file structure; the file's identity (CLAUDE
# vs AGENTS) depends on cascade override.
# ----------------------------------------------------------------------------
[agents]
file = "CLAUDE.md"
format = "md"
description = "Agent-facing rules file. Top-level H2 sections are rule groups."

[agents.title]
heading = 1
description = "The H1 preamble of the agents file."

[agents.title.fields.body]
type = "string"
format = "markdown"
description = "Intro prose under the H1, before the first rule group."

[agents.section]
heading = 2
description = "One rule group. Example slugs: 'tillsyn-first-coordination', 'evidence-sources', 'qa-discipline'."

[agents.section.fields.body]
type = "string"
format = "markdown"
description = "The rules under this heading. Bullet lists and subheadings typical."

# ----------------------------------------------------------------------------
# plan_db — TOML-backed planning records. Multi-instance: each drop gets
# its own subdir under workflow/ with a canonical db.toml holding that
# drop's build tasks and QA twins. Address shape:
# `plan_db.<drop-slug>.build_task.<id-path>`.
# ----------------------------------------------------------------------------
[plan_db]
directory = "workflow"
format = "toml"
description = "Planning worklog. Each immediate subdir of workflow/ is one drop; its db.toml holds build tasks and QA twins for that drop."

[plan_db.build_task]
description = "One unit of implementation work. Smallest addressable slice of planning."

[plan_db.build_task.fields.id]
type = "string"
required = true
description = "Stable identifier, e.g. 'TASK-001'. Never reused."

[plan_db.build_task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "review", "blocked", "done"]
description = "Current state. 'review' is the project gate before 'done'."

[plan_db.build_task.fields.title]
type = "string"
required = true
description = "One-line headline. No trailing period."

[plan_db.build_task.fields.owner]
type = "string"
required = true
description = "GitHub handle of the task owner."

[plan_db.build_task.fields.body]
type = "string"
format = "markdown"
description = "Approach, evidence, traces, unknowns. Markdown."

[plan_db.qa_task]
description = "QA verification twin of one build_task. Auto-generated on build_task create; owned by a QA role."

[plan_db.qa_task.fields.id]
type = "string"
required = true
description = "QA identifier matching the build_task: 'QA-001' twins 'TASK-001'."

[plan_db.qa_task.fields.parent_build_task]
type = "string"
required = true
description = "Build task id this QA verifies."

[plan_db.qa_task.fields.kind]
type = "string"
required = true
enum = ["proof", "falsification"]
description = "Which QA pass this twin performs."

[plan_db.qa_task.fields.status]
type = "string"
required = true
enum = ["todo", "doing", "passed", "failed"]
description = "QA outcome state."

[plan_db.qa_task.fields.body]
type = "string"
format = "markdown"
description = "QA notes: evidence, counterexamples, conclusions."

# ----------------------------------------------------------------------------
# worklog — append-only narrative. Single-instance: one worklog.toml at
# project root. Kept separate from plan_db so drop-scoped planning and
# project-wide chronological notes do not collide in one namespace.
# ----------------------------------------------------------------------------
[worklog]
file = "worklog.toml"
format = "toml"
description = "Append-only chronological record of decisions and work done."

[worklog.entry]
description = "One dated worklog entry."

[worklog.entry.fields.id]
type = "string"
required = true
description = "Entry id, convention 'YYYY-MM-DD-NNN'."

[worklog.entry.fields.date]
type = "datetime"
required = true
description = "Entry datetime (RFC 3339)."

[worklog.entry.fields.title]
type = "string"
required = true
description = "One-line summary."

[worklog.entry.fields.body]
type = "string"
format = "markdown"
description = "Full entry narrative: evidence, reasoning, links."

# ----------------------------------------------------------------------------
# docs — file-per-instance MD collection. Example / dogfood: README remains
# the authoritative docs source; `docs/` exists to exercise the collection
# mode end-to-end (nested dirs, path_hint, cross-instance search, delete
# semantics). Content here is example material, not real project docs.
# ----------------------------------------------------------------------------
[docs]
collection = "docs"
format = "md"
description = "File-per-instance MD collection. Each .md file under docs/ (recursively) is one page. Filename IS the page identity; nested dirs are organizational only."

[docs.title]
heading = 1
description = "Page title — H1 at the top of the file, one per page."

[docs.title.fields.body]
type = "string"
format = "markdown"
description = "Intro prose under the H1, before the first H2."

[docs.section]
heading = 2
description = "One H2 section within a page. Addressed by heading slug."

[docs.section.fields.body]
type = "string"
format = "markdown"
description = "Section body. Markdown with fenced code and deeper subheadings allowed (H3+ live inside body prose)."
```

---

## 10. Migration plan — one drop

### 10.1 Renames

- `internal/tomlfile/` → `internal/backend/toml/` (behind `record.Backend`).
- `upsert` tool → removed; replaced by `create` + `update` + `delete`.
- `schema.toml` top-level prefix: `[schema.<type>]` → `[<db>.<type>]` (db-first shape).
- `schema` tool: extended from read-only to `action`-based CRUD (`get` / `create` / `update` / `delete`).

### 10.2 Additions

- `internal/record/` — `Backend` interface.
- `internal/backend/md/` — ATX scanner implementation.
- `internal/db/` — address resolution (parse `<db>.<type>.<id-path>` and `<db>.<instance>.<type>.<id-path>` into backend lookups per §2.9; scan multi-instance dirs for instances; handle `collection` slug derivation, `path_hint` on create with `filepath.IsLocal` safety, prefix-glob scope).
- `internal/search/` — structured + regex search over backends; cross-instance union for multi-instance dbs.
- `internal/render/` — laslig-based CLI rendering layer; glamour for markdown-content string fields.
- New MCP tools: `create`, `update`, `delete`, `search`. Extended `schema` tool with action param.
- CLI subcommands matching tools.
- Meta-schema validator covering single-instance vs multi-instance.
- Updated dogfood schema per §9 (plan_db becomes multi-instance).

### 10.3 Deletions

At implementation time (not yet):

- `docs/ta.md`, `docs/api-notes.md`, `docs/PLAN.md` (this file) — all collapse into a single final `README.md` covering installation, MCP client config, schema language, tool reference, and rationale for why `ta` exists. (The MVP-era `docs/PLAN.md` was deleted and V2-PLAN renamed to PLAN on 2026-04-23.)
- The MVP example `schema.toml` if its contents are already represented in the new dogfood.

### 10.4 Test plan

Per-package coverage targets:

- `internal/schema/` ≥ 85% — load, validate, meta-validate.
- `internal/backend/toml/` ≥ 85% — schema-driven bracket filtering (§2.10), splice invariant across declared-section boundaries with non-declared brackets as body content, canonical emit.
- `internal/backend/md/` ≥ 85% — ATX scanner, fenced-code state, schema-driven section extraction (declared heading levels only), non-declared headings preserved as body content, splice invariant.
- `internal/db/` ≥ 80% — address parsing.
- `internal/search/` ≥ 80% — match + query combinations.
- `internal/mcpsrv/` ≥ 70% — tool routing; use in-process fixtures.

Critical invariant tests:

- **Splice invariant** (both backends): bytes outside a section's range are byte-identical pre/post-splice. Table test + fuzz.
- **Round-trip**: `create` a record, `get` it back, parse result matches `data` passed in.
- **Cascade merge**: home + project schema merges produce expected unions and overrides.
- **Meta-schema rejection**: malformed schema mutations fail atomically — on-disk bytes unchanged, cache unchanged.

### 10.5 Verification steps

1. `mage check` — all tests pass, tidy clean, vet clean, fmt clean.
2. `mage install` — binary at `~/.local/bin/ta`. Per 2026-04-24 amendment: creates an EMPTY `~/.ta/schema.toml` and prints laslig instructions on how to populate it (copy from `examples/`, build via `ta schema --action=create`, or promote from a project via `ta template save`); no embedded default schema.
3. `ta schema get /abs/path/to/main` — dogfood schema resolves, cascade shows home + project sources; output glamour-rendered.
4. `ta create /abs/path/to/main --section readme.title.ta --data-file -` with a test body → creates (or fails if exists). Output rendered via laslig.
5. `ta update /abs/path/to/main --section plan_db.ta.build_task.task_001 --data '{...}'` — mutates an existing record in a multi-instance db (auto-creates `workflow/ta/db.toml` on first call).
6. `ta delete /abs/path/to/main --section plan_db.ta.build_task.task_001` — removes just that record.
7. `ta search /abs/path/to/main --scope plan_db --match '{"status":"todo"}' --query scanner` — cross-instance search over every drop's plan_db.
8. `ta get /abs/path/to/main --section plan_db.ta.build_task.task_001` — verify laslig renders string fields as markdown (code blocks highlighted).
9. MCP client smoke test: register `ta` in `.claude/`, restart Claude Code, exercise each tool once.

---

## 11. Open questions

### 11.A Resolved in the 2026-04-21 round

- **11.1 ~~Schema mutation meta-model.~~** **RESOLVED.** One `schema` tool with an `action` parameter (`get` / `create` / `update` / `delete`). Data CRUD stays as separate tools (`get`, `create`, `update`, `delete`) — asymmetric by frequency. See §3.3, §4.5.
- **11.2 ~~Meta-schema surface.~~** **RESOLVED.** Meta-schema is first-class, readable via `schema(action="get", scope="ta_schema")`. Uniform API, no help-text-vs-data split.
- **11.3 ~~Cache invalidation.~~** **RESOLVED.** Stat-mtime on every MCP call; reload cascade when any schema file's mtime changed. Zero new deps. See §4.6.
- **11.4 ~~Dogfood timing.~~** **RESOLVED.** Migrate during implementation, as soon as `create` works — catches real bugs while building `search`.
- **11.5 ~~Slug collisions in MD.~~** **RESOLVED.** Strict error at write time (`create` / `update` refuse colliding slugs) and at read time (`get` / `list_sections` / `search` refuse files with duplicate slugs).
- **11.6 ~~Delete operation.~~** **RESOLVED.** Added as §3.6. Three address levels: record, whole file (single-instance db), whole instance dir (multi-instance db). Whole-db delete of a multi-instance db is intentionally ambiguous and errors; caller must zero out instances first or route through `schema(action="delete", kind="db")`.
- **11.7 ~~`list_sections` output shape.~~** **RESOLVED.** Flat list of full addresses, stable source-order within each file.

### 11.B Still open

- **11.8 Multi-instance slug derivation.** For `create(section="plan_db.drop_3.build_task.task_001", ...)`, the `drop_3` instance slug is trusted verbatim from the caller — we don't normalize or re-slug it. Question: do we validate it matches `[a-z0-9][a-z0-9-_]*`? Leaning yes (matches filesystem safety + kebab-slug conventions used in MD slugging).
- **11.9 Cross-instance atomicity for `delete <db>` on multi-instance.** §3.6 errors on this. Alternative: accept with an explicit `--force` / `force=true` flag that cascades through every instance. Leaning keep-erroring — forcing the caller through explicit instance enumeration keeps the "deleting 17 directories" decision visible.

### 11.C Decided in the 2026-04-21 Option D round

- **File-per-instance schema mode.** Added third root key `collection = "..."` alongside `file` and `directory`. File-per-instance scans recursively, maps file paths to hyphen-joined kebab slugs (§5.5.2).
- **`path_hint` on `create` for collection dbs.** Optional parameter disambiguates flat (`docs/reference-api.md`) vs nested (`docs/reference/api.md`). Default = flat; omit `path_hint` on subsequent calls to the same instance.
- **Prefix-glob scope.** `list_sections` and `search` accept `<db>.<prefix>-*` as an instance-slug wildcard (§5.5.3).
- **Release tag.** `v0.1.0` (not `v1.0.0`); pre-stable per §2.6.

### 11.D Decided in the 2026-04-21 schema-driven-sectioning round

- **Uniform address grammar.** `<db>.<type>.<id-path>` single-instance / `<db>.<instance>.<type>.<id-path>` multi-instance, where `<id-path>` is 1+ segments. Same shape for TOML and MD — see §2.9. Refactored grammar table at §5.5.
- **Schema-driven sectioning.** Backend scanners parse between declared-type id-paths, not raw syntactic markers. Non-declared markers are content of the enclosing declared section. See §2.10, §2.11, §5.2, §5.3.2. Applies to both backends.
- **Body range model.** A declared record's byte range extends from its start to the next declared record's start (or EOF). No overlapping ranges; splice invariant simple. See §2.11.
- **Backend is schema-aware at construction.** `record.Backend` factory takes a list of declared types for the owning db so the scanner knows which markers to treat as boundaries. Interface signatures unchanged from §12.1; what changes is how the backend is instantiated. See §5.1.
- **`path_hint` safety.** `path_hint` must stay inside the collection root — `..` escape is rejected (implementation note: use `filepath.IsLocal`; Go 1.20+). See §3.4.
- **MD non-declared subheadings = content.** Authors can use H3–H6 (or any non-declared level) as free-form subheadings inside a record body without having to declare each as a schema type. Slug-uniqueness only applies at declared levels. Per-level author discipline for deeper headings is a human-readability concern, not a scanner-enforced one. See §5.3.2.
- **Hierarchical body ranges, not flat.** §2.11 refined: a declared record's byte range includes its descendants in the native hierarchy (TOML: descendant brackets; MD: deeper headings). If a deeper path is itself addressable (because another type is declared at that level, or TOML bracket paths allow any depth), it has its own narrower byte range nested inside the parent's. `get`/`update`/`delete` operate on exactly the requested address's range; ranges overlap in the native tree. See §5.2, §5.3.2.
- **`get` gains optional `fields` parameter.** `get(path, section, [fields])` — default returns raw bytes (current behavior); when `fields` is supplied, returns a structured subset `{fields: {name: value, ...}}`. For MD body-only types, `fields=["body"]` is equivalent to the default. For TOML, the backend parses and extracts the named fields from the located range. Unknown field names error. See §3.1.
- **MD hierarchical addressing.** `<id-path>` for MD is the ancestor-chain of slugs through declared heading levels (not just the leaf slug). Uniqueness is per-parent per declared level. See §5.3.2, §5.5.

---

## 12. Execution plan — ordered work breakdown

One drop. The ordering below is build-order, not commit-boundary — commits may group steps where natural.

1. **12.1 Backend interface extraction.** Define `internal/record/Backend`. Move `internal/tomlfile/` behind it as `internal/backend/toml/`. Zero behavior change yet; all existing tests keep passing.
2. **12.2 Schema language update.** Rename `[schema.<type>]` → `[<db>.<type>]` in the loader. Add `file` / `directory` / `format` / `heading` meta-fields. Write meta-schema validator covering single-instance vs multi-instance. Update dogfood schema at `.ta/schema.toml` to the new shape (§9). Expose the meta-schema as a literal in the binary surfaced via `ta_schema` scope.
3. **12.3 Address resolution package.** `internal/db/` parses `<db>.<type>.<id-path>` and `<db>.<instance>.<type>.<id-path>` into backend lookups. Uniform rule across formats (§2.9): 3+ segments single-instance, 4+ segments multi-instance; tail is joined into `<id-path>`. Dir-per-instance scan (canonical filename per subdir). File-per-instance scan (recursive, slug from path). Prefix-glob matching on instance slug. `path_hint` resolution on `create`, with `filepath.IsLocal` guard against `..` escape (§11.D). Collision detection at read and write time. Lang-agnostic.
4. **12.4 MD backend.** Schema-driven ATX scanner (§2.10) + List / Find / Emit / Splice. Scanner takes declared types at construction; emits sections only for headings matching a declared level; non-declared headings are body content. Body range = from declared heading to next declared heading (at any declared level) or EOF. Body-only field layout. Splice-invariant fuzz test. Slug-collision error at read and write **per declared level**.
5. **12.5 Data tool surface.** Replace `upsert` with `create` / `update` / `delete` in `mcpsrv` and CLI. Wire multi-instance auto-dir-and-file creation into `create`; wire three address levels into `delete` (§3.6). Hard cut, no aliases.
6. **12.6 Schema tool CRUD.** Extend the existing `schema` tool with `action` param. `get` keeps current behavior; `create` / `update` / `delete` mutate the resolved-cascade write layer (project `.ta/schema.toml`). Re-validate via meta-schema on every mutation with atomic rollback.
7. **12.7 Laslig CLI rendering.** Build `internal/render/` on top of laslig. String fields rendered as markdown via glamour (syntax-highlighted code blocks). MCP output unchanged (structured JSON). See §13.
8. **12.8 Search.** `internal/search/` + `search` tool + CLI subcommand. Regex via `regexp`. Cross-instance union for multi-instance dbs.
9. **12.9 MCP caching.** In-memory schema cache with `os.Stat`-mtime check per call; atomic swap on schema mutations; startup meta-validation refuses to boot on a malformed cascade.
10. **12.10 Dogfood migration.** Migrate the redesign plan (then named `docs/V2-PLAN.md`, renamed to `docs/PLAN.md` on 2026-04-23) → `workflow/ta/db.toml` via `ta create` calls (each §12.x step becomes one `build_task` record; each QA pass a `qa_task` twin). Verify `search` and `get` against real records.
11. **12.11 Strip global cascade from runtime.** `internal/config/Resolve` reads only `<project>/.ta/schema.toml`. No home-layer, no ancestor walk. `mcpsrv.Config.ProjectPath` required. Cache collapses to a single entry. All six `config.Resolve` callers updated (mcpsrv cache / ops / schema_mutate / tools, search, cmd/ta). `mage dogfood` loses its HOME-staging workaround. Tests simplify (drop `t.Setenv HOME` staging). See §14 for the full architecture.
12. **12.12 JSON output mode.** All CLI read commands (`get`, `list-sections`, `schema get`, `search`, `template list`, `template show`) grow a `--json` flag that emits structured JSON instead of laslig-rendered markdown. Mage targets `Test` / `Check` / `Cover` grow `--json` output (or respect `MAGEFILE_JSON=1`). Default human path unchanged. Agent-facing guidance in CLAUDE.md / AGENTS.md: use `--json` for every `ta` and `mage` invocation.
13. **12.13 Template library at `~/.ta/`.** New `internal/templates/` package: `List()`, `Load(name)`, `Save(name, bytes)`, `Delete(name)`. Convention: `~/.ta/<name>.toml` is one named template; `~/.ta/schema.toml` is the "default" template. `ta template list` + `ta template show <name>` CLI subcommands (read-only this slice). **Firewall:** `internal/templates/` imports stdlib + `internal/schema/` only — NEVER `internal/config/Resolve`. Runtime consumers never touch `internal/templates/`.
14. **12.14 Project bootstrap — `ta init`.** New CLI subcommand. Takes an optional path arg (defaults to cwd; initially absolute-only, relaxed to relative+absolute via `filepath.Abs` per 2026-04-23 amendment; §12.17.5 [A1] further shifts this to a `--path` flag across all path-taking commands). `mkdir -p` the target. Huh-based template picker from `~/.ta/`. Writes `<path>/.ta/schema.toml` from the chosen template. Per §12.17.5 [D2] 2026-04-24 amendment: `ta init` errors with a laslig-structured notice when `~/.ta/` has no usable schema source (empty schema.toml + no templates), pointing at `examples/` in the ta repo, `ta schema --action=create`, and `ta template save`. No default-embedded schema ships with the binary; `mage install` only creates an empty `~/.ta/schema.toml` placeholder and prints populate instructions. Writes MCP configs per `<path>/.ta/config.toml` opt-in (default both): `<path>/.mcp.json` for Claude Code, `<path>/.codex/config.toml` for Codex. No git-worktree gating — works from any writable directory.
15. **12.14.5 Style cleanup sweep (pre-Pair-C gate).** Mechanical stdlib-modernization pass plus an unused-identifier sweep across every Go file in the repo. No design judgment; orchestrator direct-edit pass (not a builder spawn). **Scope:**
    - **Stdlib modernizations** — apply everywhere the pattern matches, not just at the sites gopls currently flags:
      - `HasSuffix + TrimSuffix` → `strings.CutSuffix` / `bytes.CutSuffix` (Go 1.20+).
      - `strings.Split(s, sep)` ranged in a `for range` → `strings.SplitSeq(s, sep)` iterator (Go 1.24+).
      - Manual `for k, v := range src { dst[k] = v }` → `maps.Copy(dst, src)` (Go 1.21+).
      - `strings.IndexByte` / `bytes.IndexByte` followed by slice split → `strings.Cut` / `bytes.Cut` (Go 1.18+).
      - C-style `for i := 0; i < N; i++` with no other use of `i` → `for i := range N` (Go 1.22+).
      - Manual `wg.Add(1); go func(){ defer wg.Done(); ... }()` → `wg.Go(func(){ ... })` (Go 1.25+).
    - **Unused identifiers** — delete any `const` / `var` / `func` flagged unused by gopls or `go vet`. The orchestrator decides case-by-case whether the identifier names a missing test (write it then) or is genuinely dead code (delete it).
    - **Boundary.** Purely local refactor. No behavior changes. No new imports beyond stdlib. `mage check` green; `mage dogfood` green. One commit: `refactor: apply stdlib modernizations and prune dead identifiers`.
    - **Sequencing.** Runs immediately after §12.14 and before §12.15/§12.16 (Pair C). Does NOT run in parallel with Pair B or Pair C — isolation keeps merge conflicts on `cmd/ta/commands.go` and test files at zero.
    - **Standing QA concern, from §12.14.5 onward.** Every QA spawn prompt (`go-qa-proof-agent` AND `go-qa-falsification-agent`) from this step forward includes the line: *"Also scan the files you touch for new stdlib-modernization opportunities (CutSuffix, SplitSeq, maps.Copy, bytes.Cut, range-over-int, WaitGroup.Go, strings.Cut) and unused identifiers (const/var/func); flag them in your report for the next orchestrator cleanup sweep."* QA reports the hits; it does not fix them. Cleanup remains orchestrator-gated so scope stays crisp and style churn does not compete with real correctness findings.
16. **12.15 Template save — `ta template save`.** Verbatim-copy `<cwd>/.ta/schema.toml` to `~/.ta/<name>.toml`. Huh-prompts for name if not given; huh-confirms overwrite. Enables the "find a better schema in a project, promote it to global template" flow.
17. **12.16 Huh interactive CLI root.** Bare `ta` with TTY detected → huh menu of subcommands. Bare `ta` without TTY (MCP client over stdio) → MCP server, unchanged from today. Existing `.mcp.json` / `claude mcp add` invocations that spawn bare `ta` keep working. Huh pickers also added inside `ta init` (template + claude/codex opt-in) and `ta template save` (name prompt) where a dropdown helps.
18. **12.17 E2E dev+assistant gate.** No code. Dev + assistant walk through: fresh `ta init` on a new absolute path, template save round-trip, CRUD round-trip on the seeded project, MCP registration verified in Claude Code + Codex, `mage dogfood` still works end-to-end with the new cascade-free runtime. Gate before §12.17.5.
19. **12.17.5 Dogfooding readiness.** Not "pre-release" — these are gates that must resolve before §12.17 becomes a real dogfood flow rather than a bootstrap smoke test. Release is later and has its own gate at §12.19. Dev maintains the authoritative checklist out-of-band; this item is the plan-side rollup of what has surfaced so far during E2E.

    **Work items.** Phase labels (A/B/C/D/E) map to the execution schedule in §12.17.5.1.

    - **[A1] `--path` flag pattern across all commands.** Drop the `<path>` positional from every path-taking command. Introduce `--path <value>` as an optional flag accepting relative OR absolute (resolved via `filepath.Abs`); default = cwd. Applies uniformly to `ta get`, `ta list-sections`, `ta create`, `ta update`, `ta delete`, `ta schema`, `ta search`, `ta init`, `ta template apply`. MCP tool handlers keep the absolute-required guard server-side — agents with a drifted cwd would silently write to the wrong project. Release-note caveat: `ta create` / `ta update` / `ta delete` from a typoed cwd with a sibling `.ta/schema.toml` would silently mutate the wrong project; acceptable risk (typing in the right dir is the overwhelming common case). This supersedes the prior "default path to cwd" + "accept relative paths" bullets.

    - **[A2] `ta list-sections` rewrite — match MCP tool shape.** Today the CLI takes a TOML **file** path and lists bracket paths from that one file; it diverges from the MCP tool (`list_sections(path, scope)` per §3.2) which takes a project dir + scope and returns project-level addresses. Rewrite the CLI to match MCP: project dir (via `--path`, default cwd) + optional scope (`--scope` flag AND optional second positional). Output emits full project-level addresses (`plan_db.ta.build_task.task_12_1`, not `build_task.task_12_1`) so copy-paste composes with CRUD addresses. `--limit <N>` (default 10, `-n` shorthand) + `--all` boolean; mutex-exclusive. **Scope boundary with [A1]:** A1 leaves `newListSectionsCmd` alone; A2 owns the rewrite — no parallel edits to the same function.

    - **[A3] `mage install` output styling.** `mage install` currently prints plain text ("current schema.toml untouched" etc.). Route through laslig so install output is visually consistent with the rest of the CLI surface.

    - **[B0] Split `internal/mcpsrv/` into `internal/ops/` + `internal/mcpsrv/` per §6a.** Mechanical refactor, no semantic changes. Move `ops.go` (Get / Update / Create / Delete / ListSections / GetAllFields), `fields.go` (field extraction), `cache.go` (schema cache), `schema_mutate.go` (schema CRUD), `errors.go` (sentinels), `backend.go` (backend dispatch) into a new `internal/ops/` package. `internal/mcpsrv/` shrinks to `server.go` (Server type + stdio run loop) and `tools.go` (tool declarations + handlers calling `internal/ops.*`). Every caller rewires imports: `cmd/ta/*` changes `"…/internal/mcpsrv"` → `"…/internal/ops"` and updates call sites from `mcpsrv.Get` → `ops.Get` etc.; `internal/mcpsrv/tools.go` does the same. [B1] and [B3] already committed against `internal/mcpsrv/`; [B0] rewires them as part of the move. After [B0], [A2.1] / [B2] / [D1] / [D2] land against `internal/ops/` naturally. **Must land before [A2.1] / [B2] / [D1].**

    - **[A2.1] Move `list-sections` `limit`/`all` into the endpoint.** Today `mcpsrv.ListSections(path, scope)` walks every record in scope; the CLI slices `[:limit]` after. Per §6a.1 the endpoint owns filtering: post-[B0] becomes `ops.ListSections(path, scope string, limit int, all bool) ([]string, error)` and early-exits the scan once the cap is reached. MCP tool gains matching `limit` / `all` params. CLI passes its flag values through. Fixes the F1 MCP asymmetry + the post-fetch-slice perf gap. **Endpoint default semantics:** when `limit <= 0 && all == false` the endpoint substitutes default 10 (do NOT error on missing limit). Depends on [B0]. Bundled with [A2.2] because both edit `search.Query` + `search.Run`.

    - **[A2.2] Add `limit` / `all` to `search` endpoint + MCP tool for parity.** §3.7 spec already amended with the new signature. Per §6a.1 the endpoint enforces the cap; MCP tool declaration gains matching params; CLI `ta search` gains `--limit <N>` (default 10, `-n` shorthand) + `--all`. Early-exit implementation required. Same endpoint-default semantics as [A2.1]. Bundled with [A2.1] under one builder — the shared `search.Query` + `search.Run` edits preclude parallel builders. Depends on [B0].

    - **[A2.3] Release-note items surfaced by [A2.1]/[A2.2].** MCP `list_sections` + `search` gain a default-10 cap where they were previously uncapped. Document in §12.19 release notes under "breaking changes": agents currently relying on full uncapped results must pass `all=true` or an explicit `limit`. Not a code item — planning surface only.

    - **[B1] `update` PATCH semantics (MCP + CLI).** Implement the spec already amended into §3.5: provided fields overlay the stored record; unspecified fields retain their values. Null-clears non-required; null on required-no-default errors; null on required-with-default resets to the schema default (literal write at update time; later schema default-value edits don't retroactively update records). **Empty `data` (`{}`)** short-circuits before overlay: no-op success, no re-validation of the existing record, no disk write. If the stored record is already invalid on disk that's surfaced on the next read; `update` is not a validator. After non-empty overlay, merged record is validated against the type schema atomically (reject the whole update on any field failure; on-disk bytes unchanged).

    - **[B2] `ta get` scope-address expansion.** Today `ta get` requires a fully-qualified single-record address. Allow it to accept prefix/scope addresses matching the same grammar `search` / `list_sections` use: `<db>`, `<db>.<instance>`, `<db>.<type>` across instances, `<db>.<instance>.<type>` within instance, `<db>.<instance>.<type>.<id>` = single record (current behavior). When the address resolves to multiple records, return each — human render: one record per laslig Section block, ordered by file-parse order. JSON: `{"records": [{section, fields}, ...]}`. `--limit <N>` (default 10, `-n` shorthand) + `--all` boolean, mutex-exclusive. Single-record gets silently ignore `--limit` / `--all`. `--all` is the self-documenting escape for "no cap"; there is no `--limit 0` semantic. §3.1 amendment note already added 2026-04-23.

    - **[B3] Unified render helper between `get` and `search`.** Currently `get` (no `--fields`) calls `renderRawRecord` (raw TOML/MD bytes through a fence) while `search` calls `render.Renderer.Record(section, fields)` which dispatches per-type through glamour. The latter is the target. Extract the per-type field render loop into a shared helper; `get` synthesizes all declared fields from the located record and calls the same helper; `search` keeps its current path through the same helper. DRY + consistent rendering across CRUD and search surfaces. Multi-record outputs (from B2) reuse the same helper per record with Section boundaries between. **Regression-lock:** capture search's current stdout as a golden-file fixture BEFORE the extraction; post-refactor, byte-identical output (or intentional diff justified in the commit). Prevents silent drift in the existing search UX.

    - **[C1] `ta schema get` flow-per-field render.** Current Table layout wraps cell contents word-by-word under narrow terminal widths, producing unreadable column-broken text. Schema get is inherently per-field prose (type + description + enum + default + required) — a flow layout (one field per block, labels on the left, description as paragraph prose) reads cleanly at any terminal width. Builds on the B3 shared render helper where shape overlaps; new helper (e.g. `SchemaFlow`) built on laslig primitives (Section/Paragraph/KV per field) where it doesn't. Depends on B3 landing first.

    - **[D1] Interactive huh form per field on `ta create` / `ta update`.** JSON is agent-shape; humans shouldn't hand-craft `--data '{...}'` payloads. On TTY default, build a huh.Form from the type's declared fields. Dispatch keys on `(Field.Type, Field.Format)`: `string` + `format="markdown"` → huh.Text (multi-line, triple-quoted on TOML emit); `string` + enum non-empty → huh.Select; `string` + `format="datetime"` OR `Type=datetime` → huh.Input with RFC3339 validator; bare `string` → huh.Input; `integer`/`float` → huh.Input with numeric validator; `boolean` → huh.Confirm; `array`/`table` → JSON-textarea fallback (huh.Text validating `json.Unmarshal`). On `update`, pre-fill existing values; empty submission retains existing. `--data` and `--json` remain as non-interactive escapes for agents and scripts. TOML emit of multi-line strings uses `"""` with embedded `"""` escaped via concatenation or backslash; MD emit is literal.

    - **[D2] Remove `--blank` and add empty-home guard.** **2026-04-24 amendment — REPLACES the prior "default embedded schema" scope; followup amendment same date drops `mage install` schema-seeding too.** Today `ta init --blank` writes a one-comment-header `.ta/schema.toml`. The `--blank` flag + `<blank>` huh picker option + `blankSchemaBody` / `blankTemplateChoice` constants + `SchemaSource="blank"` path are removed entirely from `cmd/ta/init_cmd.go` and its test. In their place: a home-empty guard fires when `~/.ta/` has no usable schema source (no templates AND schema.toml empty/malformed), emitting a laslig-structured notice pointing at `examples/`, `ta schema --action=create`, and `ta template save`. **`mage install` no longer copies `examples/schema.toml` to `~/.ta/schema.toml`** — it creates an empty placeholder file and prints laslig instructions on how to populate it (copy from `examples/`, build via CLI, or promote from a project). No default-embedded schema ships with the binary. Rationale: a one-size-fits-all ship default is the wrong UX; examples showcase real use after the cascade-agents design lands (§12.17.6). Footprint: `cmd/ta/init_cmd.go`, `cmd/ta/init_cmd_test.go`, `magefile.go:seedHomeSchema`, plus §12.14 + §14 bootstrap prose. Users and their orchestrators decide the schema shape they want; we ship copyable examples and docs.

    - **[E1] Dogfood pass.** Dev-driven exercise of the cascade-free runtime against real project work (not just §12.17 walkthrough). Observations feed back into any last polish rounds. No code — sign-off slice.

    - **Additional items.** Dev is compiling the full list; more entries land here as they surface.

    **12.17.5.1 Execution schedule — parallel and sequential phases.** Built from the phase labels above. Each phase spawns one or more `go-builder-agent`s and each build commit gates through a `go-qa-proof-agent` + `go-qa-falsification-agent` pair before advancing.

    **Landed rounds** (Phase A + parts of Phase B already on `main`):

    - **Round 1 — Phase A landed.** A1 (`4b3c46a` --path flag) + A3 (`a307207` mage install laslig) + A2 (`99b5bff` list-sections rewrite). All three + their QA passes on origin.
    - **Round 2 — Phase B partial landed.** B1 (PATCH semantics) + B3 (unified render helper) bundled into `5369aaf`. QA pending in subsequent rounds (superseded by [B0] refactor — B1/B3 code gets rewired during [B0] move, then QA'd in the new shape).

    **Remaining rounds:**

    - **Round 3 — [B0] solo (mechanical package split).** Move `mcpsrv/*` domain files into `internal/ops/`; leave `server.go` + `tools.go` in `internal/mcpsrv/`; rewire every `cmd/ta/*` import + `internal/mcpsrv/tools.go` handlers. Single builder, no parallelism — the move touches nearly every file and must land atomically. `mage check` green after each incremental move if broken into sub-steps; ideally one commit for the full shift. QA pair verifies no semantic drift.

    - **Round 4 — [A2.1+A2.2] bundled solo.** [A2.1] and [A2.2] SHARE edits in `internal/search/search.go` — both add `Limit`/`All` fields to `search.Query` and an early-exit in `search.Run`'s outer loop. That shared surface forbids naive parallelism. Bundle them under one builder: "A2.1+A2.2 limit/all into list-sections and search endpoints." Planning evidence: `workflow/ta/IMPACT-B0-A21-A22.md §4.2`.

    - **Round 5 — [B2] solo.** [B2] (`ta get` scope expansion) runs after Round 4. Rationale: [B2]'s multi-record `ta get` with `--limit`/`--all` routes through the same `search.Run` walker that [A2.1] / [A2.2] edit. Running [B2] in parallel with Round 4 would create a second contender for `search.Query.Limit/All`. Serializing keeps the search.go surface owned by one builder at a time. Falsification evidence: round-2 QA on the decoupling plan.

    - **Round 6 — Phase C (solo).** C1 — `ta schema get` flow-per-field render. Depends on B3's shared helper (already committed in `5369aaf`; survives [B0] as part of the move).

    - **Round 7 — Phase D (sequential per 2026-04-24 amendment).** D1 huh form per field landed solo (`30974e6`); new-D2 `--blank` removal + empty-home guard lands solo afterward. Both touch `cmd/ta/` tests and init-cmd prose so parallel builders would entangle; sequential serialises the `cmd/ta/` test surface.

    - **Round 8 — Phase E.** E1 dogfood pass (no builder — human walkthrough). Closes §12.17.5. E1 surfaced the [F1] schema UX gap below.

    - **Round 9 — §12.17.9 paths-shape + index (multi-phase sequential builder series).** Replaces three current shapes with `paths = [...]` slice; new address grammar without db prefix; adds `.ta/index.toml`; subsumes [F1]. Sub-rounds 9.1–9.8 listed in §12.17.9. Each sub-round is one builder + QA pair + commit. SEQUENTIAL — each builds on previous. Largest pre-release drop.

    - **Round 10 — §12.17.6 cascade-agents design.** Dev + orchestrator collaborative design session. No builder. Output: a new design doc under `docs/` (exact filename TBD). Pre-release; its output shapes §12.17.7 and the dogfood local-schema.

    - **Round 11 — §12.17.7 examples rebuild.** Dev + orchestrator collaborative build. No solo builder (dev-driven design decisions throughout). Replaces `examples/schema.toml` with a richer copy-from-able set based on §12.17.6.

After §12.17.5 closes: §12.17.9 paths-shape + index → §12.17.6 cascade-agents design → §12.17.7 examples rebuild → §12.18 README collapse → §12.19 v0.1.0 release tag.

**12.17.8 `ta init` db-picker redesign — SUPERSEDED by §12.17.9.** This drop's UX intent (multi-select dbs from home schema library on init) is folded into §12.17.9 Phase 5 below. Original spec retained for context but no longer freestanding.

**12.17.9 Paths-shape model + runtime index (pre-release, multi-phase builder task). Locked 2026-04-24 after extensive design discussion through E1 dogfood.** Replaces the three current shapes (`ShapeFile`, `ShapeDirectory`, `ShapeCollection`) with a single `paths = [...]` slice model. Adds `.ta/index.toml` runtime record-type index. Changes address grammar to drop the db-prefix segment. Subsumes the §12.17.8 db-picker redesign. Largest §12.17.5 sibling drop.

**Locked design (no more redesign — these are decisions to build against):**

- **Schema model**: every db declares `paths = [...]` (slice, length 1+, glob `*` allowed for one segment). Replaces `file=` / `directory=` / `collection=`. Project-relative or home-relative (`~/...`). Examples: `["plans"]`, `["workflow/*/db"]`, `["docs/"]`, `["~/.ta/projects/myproj/workflow/*/db"]`. Schema-load REJECTS overlapping `paths` slices across dbs (would make addresses ambiguous).
- **Address grammar**: `<file-relpath>.<id-tail>`. NO db prefix. The schema's `paths` mount determines the db. `<file-relpath>` is the dotted on-disk path under the mount, basename only (no extension). `<id-tail>` is the bracket path inside the file. Examples: `phase_1.db.t1` (under `paths = ["workflow/*/db"]` → `workflow/phase_1/db.toml [t1]`); `README.installation` (under `paths = ["."]` → `README.md [installation]`); `guides.install.prereqs` (under `paths = ["docs/"]` → `docs/guides/install.md [prereqs]`).
- **Type via flag**: `--type` is REQUIRED on `ta create` (defines the type for validation + index). OPTIONAL on `ta get`/`update`/`delete`/`search` — index resolves; if specified must match index else error.
- **`.ta/index.toml`**: top-level `format_version = 1` scalar. One bracket-table per record, keyed by canonical address (no db prefix). Each entry carries `type`, `created`, `updated`. Idiomatic TOML — natural nested form. Example:
  ```toml
  format_version = 1

  [phase_1.db.t1]
  type = "task"
  created = 2026-04-24T15:00:00Z
  updated = 2026-04-24T15:30:00Z

  [README.installation]
  type = "section"
  created = 2026-04-24T14:00:00Z
  updated = 2026-04-24T14:30:00Z
  ```
- **Index is trust-and-fail-loud**: written atomically on every create/update/delete. NO mtime-stat caching, NO auto-rebuild, NO drift-recovery scaffolding. If a read finds a mismatch (index says yes but bracket missing on disk, or bracket on disk that's not in index), error loudly with "run `ta index rebuild`". Manual command only.
- **Id-tail uniqueness**: enforced at create-time. The full canonical address `<file-relpath>.<id-tail>` must not exist in the index. ErrRecordExists fires if it does. TOML bracket parser also enforces uniqueness within each file by parser semantics — two-layer safety.
- **`ta create --file <path> --schema <db>`**: pre-create step that creates the on-disk file (and any missing parent dirs) using the schema's `format` extension. After file exists, `ta create <addr> --type <name> --data '{...}'` writes the first record into it. Auto-mkdir + auto-create.
- **CLI `ta create <addr> --type <name>`** without `--data` opens a huh form with one input per declared field on the type, dispatching by `(Field.Type, Field.Format)` per existing D1 logic.
- **MCP `mcp__ta__create`** always carries `data` JSON; agents never use the form path.
- **CLI+MCP path-list sugar**: `ta schema --action=update --kind=db --name=<db> --paths-append=<glob>` and `--paths-remove=<glob>`. MCP equivalent via optional `paths_append` / `paths_remove` params on `mcp__ta__schema(action="update")`. Server-side fetch-modify-write with atomic-rollback. Full-replace via `data.paths` array also works.
- **`ta template save`** preserves project schema's `paths` verbatim into `~/.ta/<name>.toml`. No path rewriting on promote.
- **`ta init` db-picker**: `huh.MultiSelect` over the home schema library's declared dbs. User picks subset. Selected dbs (with their `paths`, `format`, types, fields) are reconstructed into the project's `.ta/schema.toml`. Replaces template-file picker.
- **`ta index rebuild`**: manual command. Walks every declared db's `paths`, opens each matching file, parses records, regenerates `.ta/index.toml`. Atomic write. Used after hand-edits or on index errors.

**Phase decomposition (sequential builder rounds; each lands as one commit after QA pair PASS):**

- **Phase 9.1 — Schema model migration.** Replace `schema.DB.Path string` + `schema.DB.Shape Shape` with `schema.DB.Paths []string`. Update meta-schema (`internal/schema/meta_schema.toml`) to require `paths` slice + reject `file`/`directory`/`collection`. Schema-load enforces no-overlap-across-dbs invariant. Update `examples/schema.toml`. Migrate `main/.ta/schema.toml` (dogfood) to `paths = ["workflow/*/db"]` + types `build_task`, `qa_task`. Old shape constants (ShapeFile etc.) deleted from `internal/schema/schema.go`. Tests in `internal/schema` updated for new model.

- **Phase 9.2 — Address parser + path resolver.** Replace `internal/db/address.go:ParseAddress` with new grammar `<file-relpath>.<id-tail>` (no db prefix). Replace `internal/db/resolver.go` instance/path logic with paths-glob expansion. New helper: given an address and a `Registry`, find which db's `paths` mount contains the file, return the absolute file path + bracket id-tail. Tests in `internal/db` heavily updated.

- **Phase 9.3 — Index store package.** New `internal/index/` package. Read/write `.ta/index.toml` with `format_version` scalar + per-record bracket tables. `index.Get(addr)`, `index.Put(addr, type, created, updated)`, `index.Delete(addr)`, `index.Walk()`. Atomic write via `internal/fsatomic`. Concurrent-write safety via `.ta/index.toml.lock` sentinel. Tests in `internal/index/`. New `ta index rebuild` CLI command (no MCP equivalent — too rebuild-y for an agent). Empty-index initial-state handled cleanly.

- **Phase 9.4 — CRUD rewires.** `ops.Create` requires `--type`, validates type exists on db, validates fields, writes bracket, updates index. `ops.Update` resolves type from index, validates fields, PATCH overlay, updates index `updated` timestamp. `ops.Delete` removes bracket + index entry. `ops.Get` resolves type from index (or validates `--type` if passed). `ops.ListSections` walks index keyspace under scope. `ops.Search` filters index keys + reads bracket bytes for content match. CLI commands gain `--type` flag. `--type` REQUIRED on create, OPTIONAL on read commands. `--file` + `--schema` on create-the-file path. ErrRecordExists, type-mismatch, index-disk-mismatch errors all spec'd.

- **Phase 9.5 — Init UX redesign (subsumes [F1]).** Replace `pickTemplate` (file-list select) with `pickDBs` (db-list multi-select) in `cmd/ta/init_cmd.go`. Parses home `~/.ta/schema.toml`, extracts top-level db names + their meta, presents `huh.MultiSelect`. Selected dbs reconstructed into project schema via `pelletier/go-toml/v2` marshal of `schema.Registry` subset. `--template <name>` remains as full-file shortcut path. Zero-db selection writes empty schema (per dev decision; user builds via `ta schema --action=create`). Empty home schema → existing `emptyHomeError`.

- **Phase 9.6 — Schema CRUD path-list sugar.** `--paths-append` / `--paths-remove` flags on `ta schema --action=update --kind=db`. Server-side fetch-modify-write through existing atomic-rollback path. MCP `mcp__ta__schema(action="update")` gains `paths_append`/`paths_remove` optional params. Tests in `internal/ops` and `internal/mcpsrv`.

- **Phase 9.7 — Test suite + golden regen.** All affected goldens regenerated. `cmd/ta/testdata/` goldens updated for new address grammar. `dogfood_test.go` rewires to new shape. Cross-package integration tests confirm CLI ↔ MCP parity holds.

- **Phase 9.8 — Migration + dogfood verification.** Final `mage dogfood` run materializes new index from migrated schema. E2E walkthrough in `main/` with the user against real records. Migration notes added to release notes.

**Sequencing rule.** Phases run STRICTLY SEQUENTIALLY — each phase's diff is the foundation for the next. No parallel builders. QA pair on every commit. Stop on any QA design-issue finding for orchestrator + dev discussion before resuming.

**Migration impact.** Breaking change for any downstream consumer of `schema.DB.Shape` / `Path` / `File`-keyword. Release-note text drafted in Phase 9.8. v0.1.0 ships post §12.17.9 with this as the canonical model — no compat layer for old shapes.

**12.17.6 Cascade-agents design (pre-release, collaborative).** Dev + orchestrator work through this together — no solo subagent, no dev-alone writing session. Defines the cascade-agents discipline that orchestrator / builder / QA / research agents obey across Tillsyn-coordinated work: roles and their boundaries, handoff mechanics, auth lifecycle (project-scoped vs global sessions), keymap and UI defaults, and the rule-cascade layering (`~/.claude/CLAUDE.md` ↔ `~/.codex/AGENTS.md` ↔ project `CLAUDE.md` / `AGENTS.md` ↔ `.ta/` schema records). Output: a design doc under `docs/` (filename TBD during the session) capturing the discipline in prose plus example records. Signed off jointly by dev and orchestrator before §12.17.7 starts. Rationale: its output shapes the dogfood local-schema structure and the §12.17.7 examples; cannot skip and still ship a usable v0.1.0.

**12.17.7 Examples dir rebuild (pre-release, collaborative).** Dev + orchestrator build `examples/` together based on the §12.17.6 design. Replaces `examples/schema.toml` with a richer set of copy-from-able examples that bake in the cascade-agents discipline: multi-DB schema sample (plans + notes + agent-rules), matching sample records (`examples/plans.toml`, `examples/CLAUDE.md`, etc.), plus a short `examples/README.md` describing how to copy-in. `mage install` does NOT auto-seed any default — it creates an empty `~/.ta/schema.toml` placeholder and prints instructions pointing at this dir; the user picks which example to copy in (or builds via CLI). Rationale (per dev): users and their orchestrators decide the shape they want; we ship docs and copyable examples rather than baking any single shape into the binary.

20. **12.18 README collapse.** Compose final `README.md` from existing doc content (`docs/ta.md` + consolidated plan spec). Delete `docs/` and the MVP-era `examples/schema.toml` if superseded.
21. **12.19 Release.** `mage check` clean; tag `v0.1.0` (pre-stable per §2.6). Release notes cover all breaking changes from §12.11 + §12.14 + §12.16 (home-layer runtime drop, bare-`ta` TTY dispatch, `Config.ProjectPath` required).

---

## 13. CLI rendering — laslig + glamour

All CLI outputs route through laslig. This is how the tool stays ergonomic for human operators without spending MCP tool-call budget on formatting.

### 13.1 What laslig renders

- **`get`**: address as a laslig header, then each field labelled and value-rendered. String fields are passed through laslig's markdown renderer (glamour) — so a TOML string field containing a fenced ```go block appears with syntax highlighting in the terminal.
- **`list_sections`**: laslig `List` of addresses, one per line, grouped visually by db/type even though the JSON payload is flat (per §11.7).
- **`schema` (action=get)**: glamour-rendered markdown composed from the resolved schema: H1 per db, H2 per type, H3 per field, each field's `description` as body prose, `enum` / `default` shown in a compact metadata line.
- **`search`**: one laslig card per hit — address as header, matching record's fields rendered as in `get`.
- **`create` / `update` / `delete` / `schema` (mutating actions)**: concise success/error `Notice` only. No content echo unless `--verbose` is passed.

### 13.2 Assumption: all string fields are markdown content

The schema-level convention is that **every `type = "string"` field carries markdown content**, even in TOML files. Justification:

- Plain text is valid markdown (renders unchanged; no visible regression).
- Authors and agents who want rich rendering (code blocks, inline emphasis, lists, tables) get it for free.
- The `format = "markdown"` field-level hint in §4.1 becomes informational only — laslig renders all string fields as markdown regardless. The hint is kept so an alternate renderer (JSON export, plain log, terminal without colour) can branch on it if needed.

Example. A `plan_db.ta.build_task.task_001` record with a TOML body:

```toml
[build_task.task_001]
id = "TASK-001"
status = "todo"
body = """
## Approach

Rewrite the ATX scanner.

```go
func scanHeading(line []byte) (level int, text string) {
    // ...
}
```
"""
```

`ta get` rendering in the terminal: `## Approach` as a coloured heading, the `go` code block with syntax highlighting. `ta get | cat` (or stdout not a TTY): raw content passed through, no ANSI.

### 13.3 MCP output is never laslig-rendered

MCP calls return structured JSON (raw field values for `get`, record arrays for `search`, the resolved schema tree for `schema`). Rendering is strictly a CLI concern. An agent asking for a record gets raw bytes/values — it can render them however it wants, or feed them into further tool calls without parsing past ANSI escapes.

### 13.4 Package layout

`internal/render/` sits alongside the backends; `cmd/ta/commands.go` wires each CLI subcommand through it. `internal/render/` depends only on laslig + the record types from `internal/record/`. `internal/mcpsrv/` does **not** import `internal/render/` — that's the firewall keeping MCP output structured.

---

## 14. Project bootstrap, template library, and MCP config generation

Expands §12.11 – §12.16 of the execution plan. Describes the target architecture for schema resolution, template storage, CLI entry points, MCP-config generation, and agent guidance — all of which land before the v0.1.0 tag.

### 14.1 Motivation

Pre-cleanup the cascade model folds `~/.ta/schema.toml` into every project's runtime schema. §12.9 / §12.10 surfaced three coupled problems:

- **Unbounded cache growth.** Every project path the server sees is cached forever because the home layer folds in per-project.
- **Staging workarounds in tooling.** `mage dogfood` has to create a tmpdir and redirect `HOME` to sidestep the dev's legacy `~/.ta/schema.toml`.
- **Stale-cache gaps on new cascade layers.** A `~/.ta/schema.toml` created mid-session is silently ignored by the cache until restart; patched in §12.9 but the class persists structurally.

More fundamentally, the cascade couples every project to per-user home state, which is hostile to MCP's per-project agent model. §14 removes the coupling entirely.

### 14.2 Runtime vs templates separation

- **Runtime.** `<project>/.ta/schema.toml` is the ONLY schema file the MCP server or CLI data tools consult. No home layer, no ancestor walk. Resolvable with one `os.ReadFile`.
- **Templates.** `~/.ta/` becomes a pure template library — a directory of schema files named `~/.ta/<name>.toml` users pick from when bootstrapping a project. The library is NEVER read at runtime. Only `ta init` and `ta template *` touch it.
- **Firewall.** `internal/templates/` depends on stdlib + `internal/schema/` only. It does not import `internal/config/Resolve`. Runtime consumers never import `internal/templates/`.

### 14.2.1 Why template management is CLI-only (parity asymmetry per §6a.1)

`ta template list | show | save | apply | delete` are CLI-only — no matching MCP tools today. Four independent boundaries justify the asymmetry:

- **Scope boundary.** MCP sessions are project-scoped (one `cwd` per stdio handshake). Templates live at `~/.ta/`, which is user-global. Managing global state from inside a project session crosses the session's natural scope.
- **Agency boundary.** Agents operate within a project. Templates are the user's personal starter-schema collection — dev ergonomics, not project artifacts. Agents shouldn't manage the user's global dev config any more than they should edit `~/.bashrc` or `~/.gitconfig`.
- **Temporal boundary.** Templates get consumed during bootstrap (`ta init` pulls from them to seed a new project). The MCP server for that new project doesn't exist yet. Once a server is up, templates are out of scope — the agent is operating on the project, not creating new ones.
- **Trust boundary.** `~/.ta/` is shared across ALL the user's projects. An agent in project X deleting a template that project Y relies on would be a cross-project side effect waiting to happen. CLI has an explicit human running it; MCP doesn't.

Where the boundary is weaker: **read-only** `list` / `show` arguably violate none of the four hard boundaries. Worth revisiting if/when an agent-in-the-loop new-project bootstrap workflow surfaces. Write-side (`save` / `apply` / `delete`) stays CLI-only.

### 14.3 CLI shape after this drop

**§12.17.5 [A1] amendment.** The positional `[path]` shape described below is the pre-§12.17.5 form. Once [A1] lands, every path-taking command drops the positional and takes `--path <value>` as an optional flag (default cwd, accepts relative or absolute via `filepath.Abs`). The prose below preserves the pre-amendment shape as historical context for the §12.11–§12.16 implementations that are already committed.

- **Bare `ta`** — TTY-dispatched.
  - With a TTY (human in a terminal): launches a huh menu listing every subcommand with its short description.
  - Without a TTY (MCP client over stdio): starts the MCP server, unchanged from today. Existing `.mcp.json` / `claude mcp add` invocations that spawn bare `ta` keep working byte-identically.
- **`ta init [path]`** — bootstrap a project. (Pre-[A1] shape. Post-[A1]: `ta init --path <value>` default cwd.)
  - Optional path arg (defaults to cwd). Per the 2026-04-23 §12.17.5 amendment the CLI accepts both relative and absolute forms and resolves via `filepath.Abs`; the MCP tool handler continues to reject relative paths (agents with a drifted cwd would silently write to the wrong project). Pre-amendment spec said "must be absolute"; live code still enforces that until §12.17.5 lands.
  - `mkdir -p` the target.
  - Huh-based template picker; `~/.ta/` scan. Per §12.17.5 [D2] 2026-04-24 amendment: init errors with a laslig-structured notice when `~/.ta/` has no usable schema (empty schema.toml + no templates), pointing at `examples/` in the ta repo, `ta schema --action=create`, and `ta template save`. `mage install` creates an empty `~/.ta/schema.toml` placeholder; it does not seed from `examples/`.
  - Writes `<path>/.ta/schema.toml` from the chosen template.
  - Writes MCP configs per `<path>/.ta/config.toml` (see §14.5) or huh-prompts for claude/codex opt-in; default both.
  - No git-worktree gating. Works from any writable directory.
- **`ta template list | save [name] | apply <name> [path] | show <name> | delete <name>`** — template library management. (Post-[A1]: `ta template apply <name> [--path <value>]`.)
  - `list` prints every `.toml` file under `~/.ta/`.
  - `save [name]` copies `<cwd>/.ta/schema.toml` to `~/.ta/<name>.toml` verbatim (project → global). Huh-prompts for name if omitted; huh-confirms overwrite if the name exists.
  - `apply <name> [path]` copies `~/.ta/<name>.toml` into `<path>/.ta/schema.toml` (global → project). Target path defaults to cwd; CLI accepts relative or absolute per the §12.17.5 amendment (live code still enforces absolute-only until the amendment lands). Huh-confirms overwrite if `<path>/.ta/schema.toml` already exists. Schema-only — does NOT touch `.mcp.json` / `.codex/config.toml`; use `ta init` for a full bootstrap.
  - `show <name>` renders the template via `render.Renderer.Markdown` (or `--json`).
  - `delete <name>` removes a template; huh-confirms.
- **All existing data/schema tools unchanged** on the CLI surface; only their internals update for project-local resolution.
- **`--json` flag on every read command.** Bypasses laslig; emits structured JSON for agent consumption. Mage targets `Test` / `Check` / `Cover` also grow `--json` (or respect `MAGEFILE_JSON=1`). CLAUDE.md / AGENTS.md instructs agents to use `--json` for every invocation.

### 14.4 Generated MCP configs

**Claude Code — `<project>/.mcp.json`** (per current Claude Code docs at `code.claude.com/docs/en/mcp`):

```json
{
  "mcpServers": {
    "ta": {
      "command": "ta",
      "args": [],
      "env": {}
    }
  }
}
```

`args` is `[]` because bare `ta` without a TTY starts the MCP server — the client spawns it with stdio pipes, no explicit subcommand needed. `env` is available for future per-project environment injection.

**Codex — `<project>/.codex/config.toml`** (per current Codex docs at `developers.openai.com/codex/config-basic`):

```toml
[mcp_servers.ta]
command = "ta"
args = []
```

Trusted-project-only per Codex semantics — the user must mark the project as trusted for Codex to load this config layer.

### 14.5 Project-level bootstrap config — `<project>/.ta/config.toml` (optional)

```toml
[bootstrap]
claude = true                  # generate .mcp.json (default true)
codex = true                   # generate .codex/config.toml (default true)
default_template = "schema"    # preferred template for `ta init` on this path; huh picker defaults to it
```

Read by `ta init` only. Never touched by the MCP server, data tools, or `ta template *`.

### 14.6 Schema validation discipline — break loudly, no fsnotify

Validation runs on every schema read and every schema write. No filesystem-watcher layer: stat-mtime-per-call (§12.9) + the loader's own pass are the sole gates, and they are sufficient by construction.

- **Read path.** Every tool call that touches `<project>/.ta/schema.toml` goes through `mcpsrv.ResolveProject` → cache → `config.Resolve` → `schema.LoadBytes`. If the bytes fail to parse or fail the meta-schema check (§4.7), the tool call errors loudly with the line/column pointer from the loader. No stale-serve. Post-§12.11 the cache holds one entry for the single project-local file, so the mtime check is one `os.Stat` per call.
- **Write path.** `MutateSchema` (and the data-tool surface that ends up writing records, for its pre-write schema lookup) is atomic: pre-write `LoadBytes` re-validation gate, no-write on failure, pre-mutation bytes preserved. A mutation that would produce a malformed schema never touches disk (§12.6 Option A already lands this; §14 preserves it).
- **Pre-existing invalid schema.** If a user hand-edits `<project>/.ta/schema.toml` into an invalid state and then attempts `ta create` / `ta update` / `ta schema create` / any tool call, the initial read-and-validate fails BEFORE the mutation logic runs. The user sees the existing file's problem first. Mutations cannot silently layer on top of broken bytes.
- **No fsnotify.** External edits (editor saves, `git checkout` switching schema files between branches) break loudly on the NEXT tool call via mtime-triggered re-resolve + re-validate. Adding a filesystem watcher would duplicate that gate at the cost of cross-platform surface area (kqueue/inotify/FSEvents differences, watcher-leak guards on SIGKILL). Stat-per-call is simpler and sufficient.

### 14.7 Help menus — fang-rendered examples on every command

Every cobra `Command` ships an `Example` field carrying 1–3 realistic invocations. Fang styles the help output (bold headings, coloured usage lines, optional pager for long help). The goal: a user can type `ta init --help` or `ta help init` and see enough example output to proceed without reading external docs.

- **`-h` and `--help` work on every command and subcommand** — cobra default, fang preserves.
- **`ta help [command]` and `ta h [command]`** — both work via `rootCmd.SetHelpCommand(&cobra.Command{Use: "help", Aliases: []string{"h"}, ...})`.
- **`ta <command> h` (positional `h` after a subcommand) is NOT wired.** It would conflict with commands that accept positional args — `ta get h` would try to fetch section `h` rather than print help. Users get help via `-h` / `--help` or `ta help <command>`. Documented in the §12.19 release notes.
- **Example field content.** Every subcommand lists the canonical happy-path invocation, one variant showing the most common flag (e.g. `--fields` on `get`, `--json` on read commands), and for commands that huh-prompt when args are missing, one variant showing the fully-non-interactive form.

### 14.8 Agent guidance — CLAUDE.md / AGENTS.md updates

Land as part of §12.12 and §12.17 closeout:

- All `ta <read-command>` invocations from agents MUST pass `--json`. ANSI-rendered laslig output is for humans only; agents parsing ANSI escape codes is a footgun.
- All `mage <target>` invocations from agents MUST pass `--json` (or set `MAGEFILE_JSON=1`).
- Bootstrap for a new project is `ta init <abs-path>` — humans and agents use the same verb. `.ta/config.toml` at the target path governs which MCP-config targets get written.
- Bare `ta` without a TTY is the MCP server — no explicit subcommand needed when registering in `.mcp.json` / `.codex/config.toml`.

### 14.9 Breaking changes landing in §12.11 – §12.16

Called out in the §12.19 release notes:

- **`~/.ta/schema.toml` is no longer runtime.** Users relying on it as a global base layer must either move the schema into `<project>/.ta/schema.toml` or rename the file (e.g. `~/.ta/default.toml`) to use it as a template via `ta init`.
- **`mcpsrv.Config.ProjectPath` is now required.** Library callers constructing `mcpsrv.New(Config{})` without a path will see an error; `.mcp.json` users are unaffected because the MCP client provides the project path via the stdio handshake (implementation note — verify at §12.11 build time; fallback is to require it as a CLI arg alongside the bare-server mode).
- **Existing `claude mcp add` / `.mcp.json` entries with args other than the above shape.** Users who hand-edited their `.mcp.json` can keep it; `ta init` regenerates the canonical form but respects a pre-existing file if one already has the `ta` entry.

### 14.10 Dependency additions

- `charm.land/huh/v2` v2.0.3 — interactive dropdown pickers. Module self-declares as `charm.land/huh/v2` (not `github.com/charmbracelet/huh/v2` as earlier drafts of this plan suggested); upstream GitHub mirror at `github.com/charmbracelet/huh` hosts the same source. API verified against v2.0.0 tag in Context7; same surface on v2.0.3.

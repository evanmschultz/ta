# ta v2 — Redesign Plan

> **Status:** design-locked through §13; §11 open questions mostly
> resolved in the 2026-04-21 round (two new minor Qs remain). This
> document is a temporary working artifact. When implementation is
> complete, the entire `docs/` directory collapses into a single
> `README.md` at the project root.
>
> **Relationship to `docs/PLAN.md`:** that file was the MVP plan; MVP is
> shipped. This document supersedes it for all new work and is the single
> source of truth for the v2 redesign.

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

### 3.2 `list_sections`

Enumerate records. Requires a db path (per §7.5's decision). Scope can narrow to a type, a record prefix, or a single instance of a multi-instance db.

```
list_sections(path, scope)
  path   — project directory
  scope  — "<db>" | "<db>.<instance>" | "<db>.<type>" | "<db>.<type>.<id-prefix>"
           (wildcard prefix also accepted: "<db>.reference-*")
```

Returns the ordered list of full section addresses under that scope. Multi-instance dbs return instance-qualified addresses (`<db>.<instance>.<type>.<id-path>`).

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
- `kind = "type"`: `{description, heading?}` — `heading` required when the owning db has `format = "md"`.
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
  data     — JSON object matching the type's field schema
```

Behavior: resolve schema → validate `data` → require file exists → splice the record (replace-or-append within the file) → atomic write.

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
search(path, [scope], [match], [query], [field])
  path   — project directory
  scope  — optional: "<db>" | "<db>.<type>" | "<db>.<type>.<id-prefix>"; default = whole project
  match  — optional: { field-name: exact-value, ... } exact-match on typed fields (enum, string, bool, int)
  query  — optional: Go regexp (RE2) matched against string fields (including body)
  field  — optional: restrict `query` to one named field; default = all string fields
```

Returns full matching records. No byte ranges, no snippets — whole sections come back so the caller can read verbatim. (YAGNI: we may add ranges/snippets post-MVP if agents want narrower context.)

For multi-instance dbs, `scope = "<db>"` searches across **all instances** (union); narrow with `scope = "<db>.<instance>"` to restrict to one.

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
│   └── schema.toml                    # base schema seeded to ~/.ta/schema.toml by mage install
├── .ta/
│   └── schema.toml                    # project-level dogfood schema (governs this repo)
├── magefile.go
├── go.mod
├── go.sum
├── LICENSE
└── README.md                          # final single-source doc, post-collapse
```

Dependency direction: `cmd/ta` → `mcpsrv` → `{config, schema, db, search, backend/*, fsatomic}`. Backends depend on nothing except `schema` and `record`. Zero cycles.

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

- `docs/ta.md`, `docs/api-notes.md`, `docs/PLAN.md`, `docs/V2-PLAN.md` (this file) — all collapse into a single final `README.md` covering installation, MCP client config, schema language, tool reference, and rationale for why `ta` exists.
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
2. `mage install` — binary at `~/.local/bin/ta`, `~/.ta/schema.toml` seeded on fresh machine.
3. `ta schema get /abs/path/to/main` — dogfood schema resolves, cascade shows home + project sources; output glamour-rendered.
4. `ta create /abs/path/to/main --section readme.title.ta --data-file -` with a test body → creates (or fails if exists). Output rendered via laslig.
5. `ta update /abs/path/to/main --section plan_db.ta-v2.build_task.task_001 --data '{...}'` — mutates an existing record in a multi-instance db (auto-creates `workflow/ta-v2/db.toml` on first call).
6. `ta delete /abs/path/to/main --section plan_db.ta-v2.build_task.task_001` — removes just that record.
7. `ta search /abs/path/to/main --scope plan_db --match '{"status":"todo"}' --query scanner` — cross-instance search over every drop's plan_db.
8. `ta get /abs/path/to/main --section plan_db.ta-v2.build_task.task_001` — verify laslig renders string fields as markdown (code blocks highlighted).
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
10. **12.10 Dogfood migration.** Migrate `docs/PLAN.md` + `docs/V2-PLAN.md` → `workflow/ta-v2/db.toml` via `ta create` calls (each §12.x step becomes one `build_task` record; each QA pass a `qa_task` twin). Verify `search` and `get` against real records.
11. **12.11 README collapse.** Compose final `README.md` from existing doc content (`docs/ta.md` + consolidated V2 spec). Delete `docs/` and the MVP-era `examples/schema.toml` if superseded.
12. **12.12 Release.** `mage check` clean; tag `v0.1.0` (pre-stable per §2.6).

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

Example. A `plan_db.ta-v2.build_task.task_001` record with a TOML body:

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

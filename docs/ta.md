# ta

A tiny MCP server that lets LLM coding agents read and write TOML files as if they were a structured database — with schemas to keep agents honest.

---

## Why this exists

LLM coding agents (Claude Code, Codex, etc.) currently use markdown files for planning, work logs, and task tracking. Markdown is great for humans but terrible for structured data: there is no schema, no type checking, no way to enforce that a "task" entry has the fields it needs. Agents can silently invent or omit fields, and the human in the loop only finds out later.

`ta` replaces those markdown planning files with TOML files treated like API endpoints. The agent calls a tool to read a section, calls another to upsert one, and a schema enforces what every section type must look like. If the agent tries to write a section missing required fields, the call fails and the agent has to either ask the human or correct itself. **Nothing slips past silently. Human-in-the-loop by construction.**

---

## Why the name `ta`

`ta` is Swedish for "to take" — the imperative form of the verb. The whole tool is built around one verb: take this section out, take this data and put it in.

It also happens to read as **T**OML + **A**ST, or "TOML t**a**ker.", an accidental coincidence.

---

## What it does

Only three tools.

### `get`

Read a section from a TOML file by its bracket path, (supporting nested schemas (`x.y.z`).

### `list_sections`

Enumerate all sections in a TOML file.

### `upsert`

Create or update a section. Validates against the resolved schema. Fails loudly if required fields are missing or types don't match.

> [IMPORTANT!] NOTE
> All three take a file path as an argument. The path is always required (see "CWD problem" below for why).

---

## Why TOML

Considered alternatives:

- **JSON** — no comments, unreadable for humans, trailing-comma hell.
- **YAML** — significant whitespace (LLMs get this wrong constantly), the norway problem (`no` parses as `false`), three competing spec versions.
- **Markdown** — what we're replacing. No structure, no types.
- **JSON5/JSONC** — comments allowed but still bracket-noisy.
- **KDL** — designed for this, but tooling is sparse and tree-sitter support is less mature.

TOML: comments supported, clear `[section]` syntax that maps cleanly to "API endpoints," real types, one stable spec, hard to get syntactically wrong.

### TOML and code blocks

TOML has no native concept of "embedded language with syntax highlighting" the way markdown does with triple backticks. But TOML's triple-quoted strings (`'''...'''`) preserve newlines and don't need escaping, which means **markdown-inside-TOML-strings** works beautifully:

````toml
[task.implementation]
status = "in_progress"
body = '''
## Approach

We'll use byte-surgical splicing for the upsert path:

```go
func upsert(path string, section string, data map[string]any) error {
    // ...
}
```

That preserves the surrounding comments.
'''
````

The TOML structure is the API; the markdown body is the freeform writeup. Agents read/write the structure via `get` / `upsert`; humans (and `glow`) render the strings as markdown. Everybody wins.

**To render code blocks inside TOML files with syntax highlighting**, we recommend [`glow`](https://github.com/charmbracelet/glow) — it renders markdown-in-strings beautifully, including code fences.

---

## Architecture

### Parsing and writing: byte-surgical splicing with a pure-Go scanner

`ta` uses a small purpose-built pure-Go scanner for both reading **and** writing. The scanner walks the file byte-by-byte, tracking string-literal (`"..."`, `'...'`, `"""..."""`, `'''...'''`) and comment (`#...`) state so it can locate section-header boundaries (`[x]`, `[[x]]`) without being fooled by bracket characters that appear inside strings or comments. Full TOML value semantics are not required: we only need the byte ranges of each section so we can splice.

**Why byte splicing rather than a TOML round-tripper:**

Go has no `encoding/toml` in the standard library. The two real third-party options (`pelletier/go-toml/v2` and `BurntSushi/toml`) parse → native Go value → re-emit. That round-trip loses inline comments, reorders keys, and normalizes whitespace.

For our use case — work logs and planning files that humans will write notes into — silently erasing the human's comments or reordering sections is unacceptable. The human's file is not a transient serialization; it's a living document the agent shares with them.

Byte splicing solves this with **surgical edits**:

1. Scan the file and record the byte range of every section header and its body.
2. Find the target section's range.
3. Replace just those bytes with the new section content.
4. Everything outside that range — including all human comments and formatting elsewhere in the file — is preserved verbatim.

The "format" of the upserted section itself is canonical (decided by `ta`'s emission code: double-quoted strings, one key per line, no inline comments). We don't try to remember the human's original formatting of the section being replaced.

We do not use a formatter library like `taplo`. Adding one would mean a separate pass that could mutate human-authored regions elsewhere in the file, which defeats the whole point.

### Why a custom scanner, not tree-sitter

The original design called for [`gotreesitter`](https://github.com/odvcencio/gotreesitter) — pure-Go tree-sitter runtime with a bundled TOML grammar. It parses single-line TOML correctly, but its TOML grammar currently rejects both multi-line string forms (`"""..."""` and `'''...'''`). Multi-line strings are load-bearing for `ta`'s markdown-in-TOML design (see "TOML and code blocks" above), so we cannot ship on a parser that errors on them.

A purpose-built scanner is ~150 lines and tracks exactly the four pieces of state that matter for section-header detection: basic strings, literal strings, multi-line basic strings, multi-line literal strings, and `#` line comments. Full TOML value semantics (types, nested inline tables, arrays, datetimes) live elsewhere — schema-config parsing uses `go-toml/v2`, and the scanner only ever sees the user's data files. No cross-cutting parser is needed.

`gotreesitter` is retained in `go.mod` as an anchored dependency so we can revisit the decision if upstream lands multi-line string support; the open-items list below tracks that.

### MCP framework

[`mark3labs/mcp-go`](https://github.com/mark3labs/mcp-go) — the de facto Go MCP SDK. Handles stdio transport, tool registration, and request/response shape; `ta` only needs the `server.NewMCPServer` + `AddTool` + `ServeStdio` surface.

---

## The CWD problem (and our solution)

Stdio MCP servers do not reliably inherit the client's working directory. This is a known, unresolved issue in the MCP ecosystem. The three common workarounds are:

1. Hardcode `cwd` in the MCP server config (breaks when switching projects).
2. Pass workspace path as a tool argument (the accepted workaround).
3. Use env vars set per-project in MCP config (requires per-project config edits).

`ta` sidesteps the problem entirely by **never needing CWD in the first place**.

Every tool already requires a `path` argument — `get` needs to know which file to read, `upsert` needs to know which file to write. Once we have that path, we have all the anchoring we need: walk up from the file's directory looking for `.ta/config.toml`, and if nothing is found, fall back to `~/.ta/config.toml`.

```
/Users/me/projects/foo/work/tasks.toml  ← path arg
  ↑ walk up looking for .ta/config.toml
  ↑
/Users/me/projects/foo/.ta/config.toml  ← found here? use it
  ↑
~/.ta/config.toml                       ← otherwise fall back to global
```

**Closer configs win.** A project-local `.ta/config.toml` supersedes the root config. This mirrors how most "walk up to find config" tools behave and keeps per-project schema customization trivial.

This approach — walking up from a known file path to find config — is how `git`, `eslint`, `prettier`, and most of the modern tool ecosystem solve the same problem. It's well-understood by both users and agents.

---

## Schema design

Schemas are required. Not optional.

**Reasoning** *(explained here for the design record, but should NOT appear in the MCP tool descriptions the agent sees — agents just follow the contract the schema imposes):* the whole point of `ta` is to give the human authority over what the agent can and cannot write. A schemaless mode would re-open the exact failure the tool exists to prevent: agents inventing or omitting fields and the human only finding out later. One mode, one contract.

### Schema-for-TOML in TOML

Each section type gets a schema entry. Each field within that type gets its own sub-table with metadata:

```toml
# ~/.ta/config.toml or <project>/.ta/config.toml

[schema.task]
description = "A unit of work an agent picks up"

[schema.task.fields.id]
type = "string"
required = true
description = "Stable identifier, e.g. 'TASK-001'"

[schema.task.fields.status]
type = "string"
required = true
enum = ["todo", "in_progress", "done", "blocked"]
description = "Current state of the task"

[schema.task.fields.body]
type = "string"
required = false
format = "markdown"
description = "Freeform writeup. Markdown with code fences supported."

[schema.task.fields.estimate_hours]
type = "integer"
required = false
description = "Rough hour estimate"
```

The structure is always:

```
[schema.<type_name>]                  ← the section type
[schema.<type_name>.fields.<name>]    ← each field declaration
```

Field metadata supports:

- `type` — `string`, `integer`, `float`, `boolean`, `datetime`, `array`, `table`. **Validated.**
- `required` — boolean. If `true` and missing on upsert, the call fails.
- `description` — human/LLM-readable explanation. Appears in error messages.
- `enum` — optional. List of allowed values. Validated.
- `format` — optional. Documentation hint only (e.g., `"markdown"`, `"code"`). **Not validated.** Communicates intent to the agent and to renderers like glow.
- `default` — optional. Used when `required=false` and the field is absent.

### Section-to-schema mapping

**Convention-based:** the first segment of the section path determines the schema.

- `[task.task_001]` → uses `[schema.task]`
- `[task.task_002]` → uses `[schema.task]`
- `[note.standup_2026_04_19]` → uses `[schema.note]`
- `[plan.q2_roadmap]` → uses `[schema.plan]`

No magic `_type` field needed in each section. Predictable, simple, easy for agents to learn.

### Validation

On `upsert`:

1. Resolve schema by walking up from the file path arg.
2. Determine section type from the first segment of the section path.
3. Look up `[schema.<type>]`. If not found, fail with a clear error.
4. For every field marked `required = true`, check the incoming data has it.
5. For every field in the incoming data, check its type matches the schema declaration.
6. If `enum` is set, check the value is in the allowed list.
7. On any failure, return a structured error naming the field, showing the description, and (where applicable) the allowed values.

Example error the agent would receive:

```
upsert failed for [task.task_042]:
  - missing required field 'status'
    description: Current state of the task
    allowed values: ["todo", "in_progress", "done", "blocked"]
  - field 'estimate_hours' has type 'string', expected 'integer'
    description: Rough hour estimate
```

This kind of error gives the agent enough information to either fix the call itself or come back to the human with a specific question.

---

## Explicitly out of scope (YAGNI list)

These came up during design and were deliberately rejected. Documented here so they don't creep back in later.

- **Type validation beyond TOML's native types.** TOML already enforces type syntax. We don't reinvent JSON Schema.
- **Format preservation of the upserted section itself.** Byte splicing preserves everything _outside_ the touched section. Inside it, output is canonical. We don't try to remember the human's original formatting of that one section.
- **Atomic multi-section writes / transactions.** Config files aren't databases.
- **File watching / change notifications.** Consumers can re-read.
- **Diffing or merging.** Out of scope.
- **A separate formatter library (taplo, etc.).** The splicer is the formatter. Adding a separate formatter would mutate human-authored regions, which defeats the point.
- **Optional schemas.** Schemas are required by design — see "Schema design" reasoning above.
- **Comment preservation inside upserted sections.** Comments outside the section are preserved by splicing. Comments _inside_ the section being upserted are replaced along with the section. (Acceptable: the agent owns its own sections; the human's comments belong in untouched parts of the file.)

---

## Open questions / things that need verification before / during build

- **`gotreesitter` TOML grammar multi-line string support — RESOLVED.** Probe during Phase 4 confirmed the grammar rejects both `"""..."""` and `'''...'''`. Multi-line strings are load-bearing for `ta`'s markdown-in-TOML design, so we pivoted to a purpose-built pure-Go scanner. `gotreesitter` remains anchored in `go.mod`; revisit if upstream lands multi-line support.
- **Exact mcp-go API for tool registration with structured errors.** Need to confirm errors flow through to the agent in a useful shape, not just as opaque strings.
- **Handling of `[[array_of_tables]]`.** Convention-based mapping (first segment → schema) still works, but upsert semantics need a decision: do we append, or do we address by index? Lean toward "address by index for upsert, separate `append` tool if we ever need it" — but YAGNI says wait until it's actually needed.
- **What happens when the file doesn't exist on `upsert`.** Probably: create it. Worth confirming this is the desired behavior versus erroring.
- **Atomic writes.** Should use the standard "write to temp file, rename" pattern to avoid corrupting the TOML file if the process dies mid-write. Standard practice but worth calling out.

---

## Dependencies

Minimal. Three direct dependencies plus stdlib.

- `github.com/mark3labs/mcp-go` — MCP server SDK.
- `github.com/pelletier/go-toml/v2` — used **only for parsing the schema config file** (`~/.ta/config.toml` and project overrides). The user's actual TOML data files go through the scanner, not this library. Using a real TOML parser for our own config keeps schema-loading code trivial; we only pay the no-comment-preservation cost on a file we own and control.
- `github.com/odvcencio/gotreesitter` — anchored dependency, not load-bearing. Retained in `go.mod` as a candidate replacement for the scanner if upstream fixes the multi-line-string grammar bug.
- Standard library: `os`, `path/filepath`, `context`, `fmt`, `errors`.

---

## Estimated size

Rough order of magnitude:

- ~50 lines: MCP server setup, stdio transport, tool registration.
- ~150 lines: section scanner — byte-level state machine for section-header detection.
- ~60 lines: TOML emission for upserted sections (canonical output: key = value, escape strings, handle arrays/nested tables).
- ~80 lines: schema loading (walk-up resolution, parse config, build in-memory schema).
- ~60 lines: validation (required check, type check, enum check, build structured errors).
- ~40 lines: file I/O — atomic write, create-if-missing.

Total: ~440 lines of Go. Single-binary, no runtime dependencies.

---

## What success looks like

A directory of TOML files functions as a typed, validated, agent-accessible database for project planning and work logs. The agent can read sections, write sections, list sections — but cannot silently violate the contract the human established in the schema. Comments and formatting humans add to those files survive every agent interaction.

Markdown planning files become unnecessary. The structure is enforced; the freeform writeup lives in markdown-formatted string fields and renders beautifully via glow.

That's it. Single purpose. Take the section out, take the data and put it in. **Ta.**

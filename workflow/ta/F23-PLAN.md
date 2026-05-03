# F23 — auto_spawn rules (Tillsyn `child_rules` equivalent)

Single atomic commit. Builds on F22 (bases + extends, commit `01bb189`). Replaces F23a constraint-DSL with the simpler F23b auto-spawn approach per PLAN.md §C Phase 6.

## Slice shape

One commit, one slice, no follow-ons. Touches:

- `internal/schema/meta_schema.toml` — declare `[ta_schema.type.fields.auto_spawn]` and the spawn-spec sub-shape.
- `internal/schema/load.go` + `internal/schema/schema.go` — parse `auto_spawn` on type bodies, attach to `SectionType`, propagate through `extends` flattening, run cycle detection, run completeness validation.
- `internal/ops/ops.go` (`Create`) — fire spawn rules after parent validation, in one atomic-best-effort pass.
- `internal/ops/schema_mutate.go` — extend `applyTypeMutation` and `applyBaseMutation` to round-trip `auto_spawn` through update without dropping it (mirrors how `fields` is preserved).
- `internal/mcpsrv/tools.go` — extend `create` tool with `no_spawn` boolean; extend `schema` tool description to document `auto_spawn` under `kind=type`.
- `cmd/ta/commands.go` — add `--no-spawn` flag to `newCreateCmd`.
- New test file: `internal/schema/auto_spawn_test.go` — meta-schema + loader + cycle tests.
- New test file: `internal/ops/auto_spawn_test.go` — Create-fire integration tests.

## Schema syntax (locked)

Spawn rule lives at the **type** level, parallel to `fields`:

```toml
[drops.drop]
description = "..."
[drops.drop.fields]
status = { type = "string", required = true }

[drops.drop.auto_spawn]
on_create = [
    { type = "drops.qa_proof",         id_template = "{parent_id}-qa-proof",         fields = { parent_id = "{parent_id}", role = "qa-proof",         state = "todo" } },
    { type = "drops.qa_falsification", id_template = "{parent_id}-qa-falsification", fields = { parent_id = "{parent_id}", role = "qa-falsification", state = "todo" } },
]
```

**Rule fields (each spawn-spec):**

- `type` (string, required) — db-qualified target type (`<db>.<type>`). Must resolve at load time.
- `id_template` (string, required) — id literal with `{parent_id}` and `{index}` interpolation tokens. Tokens are the only escape; literal `{parent_id}` is not supported in v1 (Unknown).
- `fields` (table, optional) — static field values for the spawned record. Strings are interpolated through the same two-token rule. Missing required fields without defaults on the target type fail validation at load time AND at create time.
- `count` is **dropped** from spec — `on_create = [...]` already lets you list N specs; no need for a multiplier. (Counter to prompt §2; rationale in §QA Falsification §10 below.)

**Rule scope:**

- Only `on_create` for v1. Update/delete do not fire spawns.
- A type with no `auto_spawn` table is a no-op — spawn semantics are opt-in.
- A base ([<db>.bases.<name>]) MAY declare `auto_spawn`; the rule flattens onto inheriting concrete types via the existing `expandBases` path. A concrete type's own `auto_spawn` block wholesale-replaces an inherited one (matches the same-named-field rule in §F22).

## ID generation

Two tokens, both required to be supported:

- `{parent_id}` → the full parent id (e.g. `drops.drop-001`).
- `{index}` → the 1-based index of the spec within `on_create = [...]`, useful when the same type is spawned twice.

The bracket of a spawned record IS its full id (memory rule `feedback_ta_id.md`). `id_template = "{parent_id}-qa-proof"` against parent `drops.drop-001` produces id `drops.drop-001-qa-proof`. The id is flat — no hierarchical lookup mechanism is added.

The id_template MUST resolve to a valid id under the target type's db (i.e. start with the target db prefix). The loader rejects templates that resolve to ids inconsistent with the spec's `type` field.

## Loader phase ordering

Insert one new phase between Phase B.0 (`expandBases`) and Phase B (`expandAliases`):

- **Phase B.0.5 — propagate auto_spawn through extends.** When `expandBases` flattens base fields onto a concrete type, the base's `auto_spawn` (if any) is copied onto the type unless the type declares its own `auto_spawn`. Same wholesale-replace semantics as same-named fields.
- **Phase B.0.6 — validate spawn graph.** After all types are populated, build a directed graph: edge from type T1 → T2 for each spec (T1 declares `auto_spawn` spawning T2). Run DFS to detect cycles. Surface `ErrSpawnCycle` with the chain. Bases that contribute spawn rules participate as the inheriting concrete type, not the base name.
- **Phase B.0.7 — validate spawn-spec completeness.** For each spec, confirm:
  - `type` resolves to a concrete record type in the registry (not a base, not an alias).
  - `id_template` is non-empty and contains only the two supported tokens.
  - Every required field on the target type that lacks a `default` is present in the spec's `fields` table OR in the target type's defaulting layer.

New sentinel errors in `internal/schema/load.go`:

- `ErrSpawnCycle` — cycle detected in spawn graph.
- `ErrSpawnUnknownType` — `type` does not resolve to a concrete record type.
- `ErrSpawnInvalidIDTemplate` — id_template empty, malformed, or uses unsupported tokens.
- `ErrSpawnIncompletePayload` — required field missing from spec's `fields` map (with no default on target).

## SectionType field shape

```go
// SectionType (internal/schema/schema.go)
type SectionType struct {
    Name        string
    Description string
    Heading     int
    Fields      map[string]Field
    AutoSpawn   []SpawnSpec   // nil when no [<db>.<type>.auto_spawn] declared
}

type SpawnSpec struct {
    Type       string            // db-qualified, e.g. "drops.qa_proof"
    IDTemplate string            // e.g. "{parent_id}-qa-proof"
    Fields     map[string]any    // static values; string entries interpolate
}
```

`AutoSpawn` is a slice (preserves declaration order; spawn order is significant for index-token resolution and on-disk write order).

## ops.Create extension

After parent validation succeeds and BEFORE writing to disk, the loader-resolved `SectionType.AutoSpawn` is consulted:

1. **Pre-validate every spawn child.** For each spec, render `id_template` and `fields` through interpolation, then run `resolution.Registry.Validate` on the rendered payload against the target type. If any child fails validation, return without touching disk — no parent write, no child write.
2. **Resolve write paths for parent + every child.** Different children may live in different files. Collect (filePath, id, type, payload) tuples.
3. **Sequential writes, parent first, then children in declaration order.** Each write uses the existing `backend.Splice` + `toml.WriteAtomic` path. If a mid-pass write fails, surface the error wrapped with `ErrSpawnPartialWrite` listing which children landed and which didn't. Atomicity is therefore: **all-or-nothing on validation; best-effort sequential on disk writes**.
4. **`no_spawn=true` opt-out** skips steps 1-3 entirely. The parent record is created with no children. Useful for migration scripts and recovery; default is spawn-on.

New sentinel error in `internal/ops/errors.go`:

- `ErrSpawnPartialWrite` — partial spawn-children landed; the wrapped message lists landed and missing ids.

## MCP / CLI wire

**MCP `create` tool:**

```go
mcp.WithBoolean(
    "no_spawn",
    mcp.Description("Optional. When true, suppresses any [<db>.<type>.auto_spawn] rules declared on the target type. Default: false (auto_spawn fires)."),
),
```

**CLI `ta create`:** add `--no-spawn` boolean flag, default false, wired into `runCreate` (signature gains a `noSpawn bool`).

**MCP `schema` tool description (line 177):** append a sentence noting that `kind=type` `data` accepts an `auto_spawn` table with the spawn-spec shape; document the two interpolation tokens; document `no_spawn` opt-out on `create`.

## schema_mutate round-trip

`applyTypeMutation` currently preserves `fields` on update via `ensureFieldsTable`. Apply the same preservation to `auto_spawn`:

- On `action=update kind=type`, the meta-keys cleared are currently `description` + `heading`. Do NOT add `auto_spawn` to that clear-list. The update's `data` payload either supplies a new `auto_spawn` (replaces) or omits it (preserves the existing one — same as `fields`).
- On `action=create kind=type`, `data.auto_spawn` lands verbatim under the new type entry; loader handles the rest.
- `applyBaseMutation` mirrors the same: bases may carry `auto_spawn` and round-trip it through update without loss.

## Test coverage

**`internal/schema/auto_spawn_test.go`** (loader-level):

1. `TestAutoSpawn_BasicTwoQAChildren` — parent type with two QA spawn-specs loads and exposes `SectionType.AutoSpawn` in declaration order.
2. `TestAutoSpawn_UnknownTargetType` — spec.type names a missing type → `ErrSpawnUnknownType`.
3. `TestAutoSpawn_TargetIsBase` — spec.type names a base → `ErrSpawnUnknownType` (bases are not concrete types).
4. `TestAutoSpawn_TargetIsAlias` — spec.type names an alias → `ErrSpawnUnknownType`.
5. `TestAutoSpawn_DirectCycle` — type A spawns A → `ErrSpawnCycle`.
6. `TestAutoSpawn_IndirectCycle` — A spawns B, B spawns A → `ErrSpawnCycle` with full chain in message.
7. `TestAutoSpawn_BadIDTemplate_Empty` — `id_template = ""` → `ErrSpawnInvalidIDTemplate`.
8. `TestAutoSpawn_BadIDTemplate_UnknownToken` — uses `{whatever}` → `ErrSpawnInvalidIDTemplate`.
9. `TestAutoSpawn_IDTemplateDBPrefixMismatch` — template resolves to id outside spec.type's db → `ErrSpawnInvalidIDTemplate`.
10. `TestAutoSpawn_MissingRequiredField` — target type requires field `state` (no default), spec.fields omits it → `ErrSpawnIncompletePayload`.
11. `TestAutoSpawn_RequiredWithDefaultOK` — spec omits a required-but-defaulted field, loads cleanly.
12. `TestAutoSpawn_BaseDeclaresSpawn_ConcreteInherits` — base body has `auto_spawn`; concrete type with `extends` inherits it; same SectionType.AutoSpawn as if declared directly.
13. `TestAutoSpawn_ConcreteOverridesBase` — both base and concrete declare `auto_spawn`; concrete wins wholesale.

**`internal/ops/auto_spawn_test.go`** (Create-level):

14. `TestCreate_FiresSpawn_TwoQAChildren` — create parent → on disk: parent record + both QA records, all valid, all with interpolated fields.
15. `TestCreate_NoSpawnFlag_SuppressesChildren` — `no_spawn=true` → only parent on disk.
16. `TestCreate_SpawnChildValidationFailure_ParentNotWritten` — child renders to invalid payload → no on-disk state at all (atomic on validation).
17. `TestCreate_IndexTokenInIDTemplate` — two specs targeting same type with `id_template = "{parent_id}-child-{index}"` → ids `...-child-1` and `...-child-2`.
18. `TestCreate_ChildIDCollidesWithExisting_Errors` — pre-existing record at one of the spawned ids → `ErrRecordExists` wrapped, parent not written.
19. `TestCreate_ChildrenInDifferentDBs` — spec.type points at a different db (different on-disk file) → both files written.
20. `TestCreate_PartialWriteFailure_SurfacesErr` — fault-injected write failure on second child → parent + first child landed, second missing, error wraps `ErrSpawnPartialWrite` listing both.

**MCP / CLI smoke** (extend existing test files, no new files):

21. `internal/mcpsrv/server_test.go` — `create` tool with `no_spawn=true` works.
22. `cmd/ta/commands_test.go` — `ta create --no-spawn` flag works.

## Out of scope (Unknowns, deferred)

- Literal-brace escaping in id_template / fields. v1 does not allow embedding a literal `{parent_id}` string. If a user needs that, it surfaces as a feature request post-F23.
- Tokens beyond `{parent_id}` and `{index}` (no `{parent_field.X}`, no `{now}`). Adding more tokens later is additive and non-breaking.
- Spawn-on-update / spawn-on-delete. Spec is `on_create` only.
- Cross-process atomicity. ta has no transaction layer across files; F23 inherits ta's per-file write semantics. Documented in `ErrSpawnPartialWrite` wording.
- Concurrent create-collisions on spawn child ids. Two concurrent parents could race to spawn the same child id; whichever loses sees `ErrRecordExists`. Not new — same as today's record-create race.
- `count = N` multiplier on a single spec. Replaced by listing N specs (rationale: makes id_template per-spec, avoids index-only differentiation pitfalls).

## Acceptance

- `mage check` passes (gofumpt, vet, tidy).
- `mage test` passes; `MAGEFILE_JSON=1 mage test` parses.
- All 22 tests above present and green.
- `ta create plans.task-001 --type plans.task --data '{...}'` on a type with `auto_spawn` produces parent + child records on disk; with `--no-spawn`, only parent.
- Schema loaded with a self-spawning type fails with `ErrSpawnCycle`.
- F22 tests still pass (regression guard on `extends` interaction).

## Notes for the orchestrator

- QA pair (proof + falsification) gates the commit per `feedback_qa_before_every_commit.md`. Both reviews fire AFTER the build slice is on disk, BEFORE the commit.
- Slice fits the "1-4 build tasks" planner rule as ONE atomic commit covering the seven file touches above. Do not split — meta-schema, loader, ops, mcp, cli, and tests must land together to avoid partial state in the registry.
- gopls re-sync after slice lands (new `SpawnSpec` type, new sentinel errors, new field on `SectionType`).

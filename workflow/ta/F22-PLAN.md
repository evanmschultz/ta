# F22 — Schema Inheritance via `extends`

Status: planning draft, awaiting dev confirmation on Open Questions before implementation start.
Anchor: PLAN.md §C Phase 5 / E2E_FIXES.md F22. Follows F21 (commit `1b3c6d4`) architecture closely.

## 1. Slice shape

Single atomic commit, single slice. Confirmed.

The implementation is a load-time Phase A.5 (between alias collection in Phase A and alias expansion in Phase B). Validation (`internal/schema/validate.go`) sees only flattened `SectionType.Fields` maps and needs zero changes — this is the same architectural property F21 preserved.

## 2. Bases namespace location

`[<db>.bases.<name>]` — per-db lexical placement, Registry-wide visibility. Mirrors F21's `[<db>.types.<alias>]` exactly.

Reserves a new meta-field key `bases` at the `[<db>]` root, parallel to `types`. Means:
- Add `metaFieldBases = "bases"` to the `load.go` constant block (lines 25-29).
- `buildDB` switch (lines 473-516) gains a `case metaFieldBases:` arm that validates table shape and skips (the actual base bodies are collected up front by `collectBases`, parallel to `collectAliases`).
- Update the unknown-meta-field error message to list `paths/description/types/bases`.

## 3. `extends` field placement

`extends` sits on:
- `[<db>.<typeName>]` — concrete record types extending a base.
- `[<db>.bases.<base>]` — bases extending other bases (chain).

NOT on alias bodies (`[<db>.types.<alias>]`). Aliases already have a composition story via element_type chains; piling `extends` on them would conflate two mechanisms. **Open Question 1** to confirm with dev.

Adds `typeKeyExtends = "extends"` to `load.go` constants (lines 45-49) and a `case typeKeyExtends:` arm in `buildType` (lines 579-621). Update unknown-type-key error message to list `description, heading, fields, extends`.

## 4. What `extends` references

Bases-only. `extends = "Foo"` resolves against the bases registry exclusively. Concrete-type-extends-concrete-type is rejected.

Rationale: the spec example only ever shows `extends` pointing at bases; permitting concrete→concrete inheritance opens questions (does the child also inherit `heading`? `description`?) the spec does not answer. Conservative.

**Open Question 2** to confirm with dev — the alternate (permissive) reading is to allow `extends` to reference any record-type name, with bases simply being "types that have no on-disk records of their own".

## 5. Override semantics

Wholesale replacement. Re-declaring a field name in the child fully replaces the base's field declaration: `type`, `required`, `enum`, `description`, `default`, `format`, `element_type`, `element_fields` all swap. This is NOT a partial-override "merge fields-of-fields".

Implementation: build base fields map first (deep-cloned via `cloneFieldMap` from F21), then iterate child fields map and assign — child key wins on collision regardless of map iteration order.

Confirmed via the F21 deep-copy helpers (`cloneField`, `cloneFieldMap` at `load.go:441-459`) — bases reuse them so a child override never aliases the base's slice/map state.

## 6. Field merge determinism

Deterministic. The merge algorithm is "copy base fields, then assign child fields" — the second pass overwrites by key, not by iteration order. Two runs against the same TOML produce identical Registries even though Go map iteration is randomized. Tests must include a determinism check via repeat-load + `reflect.DeepEqual`.

## 7. Cycle detection

`ErrExtendsCycle` sentinel parallel to `ErrAliasCycle`. The resolver `resolveBase(name, raw, resolved, visiting, chain)` mirrors `resolveAlias` (load.go:336-369) shape:
- `visiting` map detects re-entry on the active recursion stack.
- `chain` slice records the human-readable path for the error message: `"A → B → C → A"`.
- A→A self-reference and A→B→A mutual reference both trip the same code path.

Cycle walks must traverse base→base extends. Concrete types are leaves of the extends graph (per Open Question 2 conservative answer), so concrete→base→base→…→base→A→base is the worst path; cycle detection stops at any base re-entry.

## 8. Bases without `extends`

Allowed. A base that declares its own fields without inheriting is the root of an extends chain. Parallel to a non-extending concrete type.

## 9. Empty body bases

Rejected. `[plans.bases.X]` with neither `extends` nor `fields` has no useful semantics. Error message: `"schema: %s.bases.%s: base must declare at least one field or extends"`.

This matches F21's empty-alias rejection (`load.go:277-281`).

## 10. Implementation footprint

### Files touched

- `internal/schema/load.go` — primary changes:
  - New constants: `metaFieldBases`, `typeKeyExtends`.
  - New sentinels: `ErrExtendsCycle`, `ErrUnknownBase`.
  - New functions: `collectBases`, `buildBaseBody`, `expandBases`, `resolveBase`, `applyExtends`.
  - Edits: `buildRegistry` adds Phase A.0 (collectBases), Phase A.5 (expandBases) before existing phases. `buildDB` adds `metaFieldBases` arm + updated unknown-meta-field message. `buildType` adds `typeKeyExtends` arm + amended "at least one field OR extends" check.
- `internal/schema/schema.go` — likely zero changes. The resolved field set still lives in `SectionType.Fields`. (Confirm: do we need to retain the `extends` name on `SectionType` for diagnostics? Lean no — flatten-and-discard.)
- `internal/schema/meta_schema.toml` — add `[ta_schema.base]` self-description block (parallel to existing `[ta_schema.type]`); add `extends` and `bases` meta-field declarations on `[ta_schema.type]` and `[ta_schema.db]` respectively. Update `[ta_schema.types]` description block to add a sibling `[ta_schema.bases]` documentation block.

### Phase ordering inside `buildRegistry`

```
Phase A.0 — collectBases (NEW, F22)
Phase A   — collectAliases (existing F21)
Phase A.5 — buildDB / buildType / buildField (existing; now also records `extends` per type)
Phase B.0 — expandBases (NEW, F22): walk reg.DBs, for each type with non-empty extends, resolveBase deep-clones the base's resolved fields, then child overrides are assigned wholesale.
Phase B   — expandAliases (existing F21)
Phase C   — checkPathsOverlap (existing)
```

Bases expand before aliases because a base may declare an array field whose `element_type` names an alias; alias inlining must run after extends-flattening so it sees the full field set.

## 11. Test coverage breakdown

### `internal/schema/load_test.go`

New tests (table-driven where natural):

- `TestExtends_HappyPath_SingleLevel` — base with two fields, type extends, assert flattened type has both base + own fields with correct attrs.
- `TestExtends_HappyPath_MultiLevel` — A→B→C chain (C is the concrete type), assert all three field sets present.
- `TestExtends_OverrideField` — base declares `status` with enum {todo, doing, done}; child re-declares `status` with enum {todo, doing}. Assert child's enum wins, full replacement (no enum union).
- `TestExtends_OverridesAreWholesale` — base field has `required = true, format = "markdown"`, child redeclares same field with only `type = "string"`. Assert child field has `required = false`, no format.
- `TestExtends_DeepCloneIndependence` — load registry, mutate a flattened child field's Enum slice, assert base's resolved Enum unchanged. Validates `cloneFieldMap` is used on the extends path.
- `TestExtends_CycleDetection_SelfReference` — `[<db>.bases.A] extends = "A"`. Assert `errors.Is(err, ErrExtendsCycle)`, message contains `"A → A"`.
- `TestExtends_CycleDetection_Mutual` — A extends B, B extends A. Assert `errors.Is(err, ErrExtendsCycle)`.
- `TestExtends_CycleDetection_Long` — A→B→C→D→A. Assert chain in message.
- `TestExtends_UnknownBase` — `extends = "DoesNotExist"`. Assert `errors.Is(err, ErrUnknownBase)`.
- `TestExtends_EmptyBaseRejected` — `[<db>.bases.X]` with no extends, no fields.
- `TestExtends_BasesAcrossDBs` — db1 declares base, db2 type extends it. Assert Registry-wide visibility works (parallel to F21 alias visibility).
- `TestExtends_DuplicateBaseName` — same base name declared in two dbs. Reject like F21 alias-namespace collision.
- `TestExtends_LoadDeterminism` — load same TOML twice, `reflect.DeepEqual(reg1, reg2)`.
- `TestExtends_BaseWithArrayField_ElementTypeAlias` — base declares an array field whose `element_type` is an alias. Type extends that base. Assert the alias is correctly inlined into the inherited field after Phase B (verifies Phase B.0 → B ordering).
- `TestExtends_ConcreteTypeNotExtensible` — `extends = "<existing record type name>"`. Assert error mentioning bases-only (Open Question 2).
- `TestExtends_OnTypeOnly_NotOnFields` — `extends` on a `[<db>.<type>.fields.<f>]` body. Reject as unknown field-key (existing path; needs explicit assertion).

### `internal/schema/validate_test.go`

- `TestValidate_AfterExtends_RequiredFromBase` — type extends a base whose field is `required = true`. Validate a section that omits that field. Assert `FailureMissingRequired`.
- `TestValidate_AfterExtends_OverriddenEnum` — child narrows base's enum. Validate a value that's in base's enum but not child's. Assert `FailureEnumMismatch`.

### `internal/schema/meta_test.go`

- Update existing meta-schema self-validation test if it exists, otherwise add `TestMetaSchema_DeclaresExtendsAndBases` — load `meta_schema.toml`, assert `[ta_schema.type]` declares a `extends` field and `[ta_schema.db]` documents `bases`.

## 12. Memory rules cross-check

- `feedback_ta_id.md` — id is canonical; type lives in index only. F22 doesn't touch ids or the index. No conflict.
- `feedback_ta_one_schema_file_per_dir.md` — single schema file per `.ta/`. Bases live inside that one schema file (under any db). No conflict.
- `feedback_qa_before_every_commit.md` — orchestrator runs go-qa-proof + go-qa-falsification on the F22 commit. This plan is the input; the QA pair targets the resulting code. No conflict.

## 13. Open questions (BLOCKING — confirm before implementation)

1. **Does an alias body support `extends`?** This plan answers no — aliases compose via `element_type`, bases compose via `extends`, do not cross the streams. Confirm.
2. **Can a concrete record type be the target of `extends`?** This plan answers no — bases-only. The permissive reading (any-type-extensible) means dropping the dedicated `bases` namespace entirely and letting any `[<db>.<type>]` be extended. Cleaner type system, but conflicts with the spec example's explicit `[plans.bases.X]` placement.
3. **Should bases live in the same Registry-wide namespace as aliases, or a separate one?** This plan answers separate. A type cannot be both a base and an alias. Confirm.
4. **Is `extends` recorded on `SectionType` after flattening, or fully discarded?** This plan answers discarded — the Registry only carries flattened field sets. Diagnostics that want to say "field `X` came from base `Y`" would need a sidecar map. Confirm we don't need that for v1.
5. **Meta-schema self-description shape.** Should `bases` get its own `kind = "base"` self-description block (parallel to `kind = "type"`) so `schema(action="get", scope="ta_schema")` enumerates it as a first-class kind? Or just document it under `[ta_schema.types]` as a sibling? This plan leans first-class. Confirm.

## 14. Out of scope

- Multi-extends. Single-extends only per spec.
- `extends` on alias bodies (Open Question 1).
- Concrete-type-extends-concrete-type (Open Question 2).
- Diagnostics that report which base contributed a flattened field.
- CRUD-tool surface for declaring bases (`schema(action="create", kind="base", ...)`) — that lands in a follow-up F2x slice once the Open Questions converge.

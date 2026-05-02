# F21 — Meta-schema language extensions

F21.1 typed array elements + F21.2 element shapes for arrays of tables + F21.3 type aliases. Single slice. PLAN says "build all three before release."

## Spec recap

- **F21.1** — `element_type = "<primitive>"` on `type = "array"` fields. Validates each element. Failure cites `field = "paths[2]"`.
- **F21.2** — `element_type = "table"` plus `element_fields.<name>` recursion. Failure cites `field = "completion_checklist[1].complete"`.
- **F21.3** — `[ta_schema.types.<alias>]` declares a reusable shape; `element_type = "<alias>"` references it.

Examples (verbatim from prompt):

```toml
[drops.droplet.fields.paths]
type = "array"
element_type = "string"

[drops.droplet.fields.completion_checklist]
type = "array"
element_type = "table"

[drops.droplet.fields.completion_checklist.element_fields.id]
type = "string"
required = true
```

## 1. Slice shape — atomic single commit

All three features land in ONE commit. Three reasons:

- The element validator is the same code path for F21.1 (primitive element) and F21.2 (table element with recursive shape) — splitting forces a throwaway primitive-only walker that F21.2 immediately rewrites.
- F21.3 aliases inline at load into the same `Field` tree F21.2 produces — implementing aliases without F21.2 means inlining into nothing useful.
- PLAN spec says "build all three before release"; no half-state ships.

Rejected alternative: F21.1 → F21.2 → F21.3 sequence. Each intermediate produces a registry shape that the next slice rewrites; tests churn 3x.

## 2. Files touched

| File | Action |
|---|---|
| `internal/schema/meta_schema.toml` | Add `element_type`, `element_fields` to `[ta_schema.field]`. Add `[ta_schema.types]` block describing the aliases table. Add `element_type` / `element_fields` to themselves so the meta-schema validates user schemas that use them. |
| `internal/schema/schema.go` | Extend `Field` with `ElementType Type` and `ElementFields map[string]Field` (or nested `*Field`); add `Registry.Aliases map[string]Field` (load-time scratch, dropped post-expansion) — keep `Field` recursive via `ElementFields`. |
| `internal/schema/load.go` | New keys `element_type`, `element_fields` on field bodies; new top-level walker for `ta_schema.types` (aliases) only on the meta-schema's own load *and* on user schemas at the same scope (`[<schema>.types.<alias>]`); two-pass load (collect aliases → expand referenced aliases inline); reserved-name check; cycle detection. |
| `internal/schema/validate.go` | `valueMatchesType` recurses into arrays when `field.ElementType != ""`; recurses into tables when `field.ElementFields != nil`; produces bracketed/dotted `field` paths in `FieldFailure`. |
| `internal/schema/error.go` | Document the path syntax on `FieldFailure.Field` (flat name | `name[i]` | `name[i].sub` | `name[i].sub[j].leaf`); JSON contract additive — no breaking change. |
| `internal/schema/load_test.go` | Add cases (see §11). |
| `internal/schema/validate_test.go` | Add cases (see §11). |
| `internal/schema/meta_test.go` | Verify the meta-schema itself validates a user schema that uses element_type / element_fields / types. |

`MetaSchemaTOML` re-embedded automatically via `//go:embed`.

## 3. Meta-schema additions

Add to `[ta_schema.field]` block in `meta_schema.toml`:

```toml
[ta_schema.field.fields.element_type]
type = "string"
description = "When type = \"array\", the type of each element. One of the seven primitives, the literal \"table\" (then element_fields declares the row shape), or a registered alias name from [<schema>.types.<alias>]. Required when element_fields is present."

[ta_schema.field.fields.element_fields]
type = "table"
description = "When element_type = \"table\", the per-element field shape. Each sub-key is itself a [field] (recursive). Forbidden when element_type is anything other than \"table\"."
```

`element_fields` is declared `type = "table"` at the meta-schema level — its keys are themselves field bodies, and the load walker re-enters `buildField` for each. TOML's bracket grammar handles arbitrary depth natively, so no parser work.

Add a new top-level structural block describing aliases (a fourth meta-schema kind alongside `db`, `type`, `field`):

```toml
[ta_schema.types]
description = "Optional table of named field-shape aliases declared at [<schema>.types.<alias>]. Each alias is a single [field] body (same shape as [ta_schema.field]) reusable via element_type = \"<alias>\". Aliases must not shadow primitives (string, integer, float, boolean, datetime, array, table); cycles are rejected at load."
```

The meta-schema is itself a TOML document the loader parses; its bracket structure is unchanged — TOML accepts the additions natively.

## 4. Alias resolution — load-time expansion

**Decision: load-time inlining.** Two phases inside `buildRegistry`:

1. **Phase A — collect.** Walk every top-level `[<schema>.types.<alias>]` (meta-schema gets the same treatment for `[ta_schema.types.<alias>]`). For each, build a `Field` body via the existing `buildField` shape and stash in a scratch `map[string]Field{}` keyed `"<schema>.<alias>"`. No expansion yet.
2. **Phase B — expand.** Walk every field; whenever `element_type` names an alias, deep-copy the alias body into the field's `ElementType` / `ElementFields` chain. Recurse into `element_fields` so nested aliases also expand. Aliases reference aliases via the same lookup.

Validation stays simple: at validate-time, `Field` already carries its fully-resolved element shape — no alias indirection at the hot path. Memory cost: minor duplication when the same alias is used in many fields. Acceptable.

Rejected alternative: lazy lookup at validate-time. Forces every validate call to walk the alias map, threads cycle detection into the hot path, and means a failed alias resolution surfaces as a runtime error on a record save instead of at schema load. Worse on every axis.

## 5. Cycle detection

Phase B keeps a "currently expanding" set. When expanding alias `A`, the set holds `{A}`. If expansion encounters `element_type = "A"` (self-reference) or `element_type = "B"` where `B` is also in the set (mutual cycle), error:

```
schema: type alias cycle detected: A → B → A
```

Sentinel `ErrAliasCycle` for `errors.Is` testing. The check lives in load.go alongside the new alias walker. Linear in number of aliases.

## 6. Reserved primitive names

At Phase A collect time, reject any alias whose name is in the reserved set: `string`, `integer`, `float`, `boolean`, `datetime`, `array`, `table`.

Sentinel `ErrAliasShadowsPrimitive`. Error message:

```
schema: alias %q shadows reserved primitive type
```

Rationale: `element_type = "string"` must always mean the primitive. Shadowing breaks every reader's mental model.

## 7. Error message paths

Today `FieldFailure.Field` is a flat name (e.g. `"status"`). F21 extends additively:

| Shape | Example |
|---|---|
| Flat scalar (unchanged) | `"status"` |
| Array element | `"paths[2]"` |
| Table element field | `"completion_checklist[1].complete"` |
| Nested array element field | `"matrix[3].cells[7]"` |

Encoding rules:

- `[<int>]` — array index, zero-based.
- `.<name>` — table field access. Always after `]` or another `.<name>`.
- No leading dot. Top-level field name is the bare identifier.

Threading: `validate.go` becomes a recursive walker. Each recursion carries a path-prefix string; failures embed `prefix + "[" + i + "]" + ...`. The existing `*FieldFailure` struct gains nothing new — only the contents of its `Field` string evolve. JSON contract additive: clients that parse `Field` as a flat name still get flat names for non-array failures; clients that want bracketed paths get them.

Doc the new grammar on `FieldFailure.Field` and in `error.go`'s package comment.

## 8. F21.2 nesting depth

No declared cap. Practical cap is the meta-schema author's tolerance for `[...element_fields.x.element_fields.y.element_fields...]` brackets — TOML and Go both handle 100+ levels with no issue. The validation walker is recursive; stack overflow is the only theoretical concern, and at 50+ levels of declared schema nesting the schema author has bigger problems.

If we want safety: cap at 16 with `ErrElementFieldsTooDeep` and revisit if anyone hits it. Recommend NO cap for v1; YAGNI.

## 9. Empty arrays

`[]` is valid for `element_type = <anything>` and for `element_type = "table"` with `element_fields`. Element validation is per-element; zero elements means zero failures. Lock that.

This also means `required = true` on the array field is the only way to force a non-empty array — `element_fields` constrains element shape, not collection cardinality. If a future feature wants min-length / max-length, that's a separate field.

## 10. Required inside element_fields

`required = true` inside `[<...>.element_fields.<name>]` means EVERY element must have that field. Per-element check in the validation walker. Failure path: `"checklist[3].id"` if the third element omits `id`.

This matches the natural reading of the spec example — every checklist row needs `id`, `text`, `complete`. Locked.

## 11. Test coverage

### load_test.go — new

- `TestLoadElementTypePrimitive` — `element_type = "string"` parses, `Field.ElementType == TypeString`.
- `TestLoadElementTypeRejectsUnknownPrimitive` — `element_type = "color"` errors.
- `TestLoadElementTypeOnNonArrayRejected` — `element_type` on `type = "string"` errors.
- `TestLoadElementFieldsRequiresTableElement` — `element_fields` without `element_type = "table"` errors.
- `TestLoadElementFieldsNested` — two-level recursion (`element_fields.row.element_fields.cell`) parses round-trip.
- `TestLoadAliasBasic` — `[<schema>.types.checklist_item]` defines, field references via `element_type = "checklist_item"`, expansion produces full element shape.
- `TestLoadAliasCycleSelf` — alias `A` references itself, returns `ErrAliasCycle`.
- `TestLoadAliasCycleMutual` — `A → B → A`, returns `ErrAliasCycle`.
- `TestLoadAliasShadowsPrimitive` — alias named `string`, returns `ErrAliasShadowsPrimitive`.
- `TestLoadAliasUnknownReference` — `element_type = "ghost"` (not a primitive, not an alias), returns sentinel `ErrUnknownElementType`.
- `TestLoadAliasReferencedFromAlias` — `A` references `B`, both expand correctly when used.

### validate_test.go — new

- `TestValidateElementTypeMismatch` — `paths = ["a", 2, "c"]` against `element_type = "string"` produces `field = "paths[1]"`, kind = `type_mismatch`.
- `TestValidateElementTypePassthrough` — homogeneous string array passes.
- `TestValidateElementFieldsMissingRequired` — array of tables, one element missing `complete`, produces `field = "completion_checklist[1].complete"`, kind = `missing_required`.
- `TestValidateElementFieldsTypeMismatch` — array of tables, one element has `complete = "yes"`, produces nested type-mismatch with bracketed+dotted path.
- `TestValidateElementFieldsUnknownField` — element has stray key, produces `field = "checklist[0].mystery"`, kind = `unknown_field`.
- `TestValidateNestedElementFields` — array → table → array → string, full recursion produces `"matrix[2].cells[3]"` on a leaf failure.
- `TestValidateEmptyArrayPasses` — `[]` against `element_type` validates (zero failures).
- `TestValidateAliasResolution` — record validates against an alias-backed field exactly as if the alias body were inlined.
- `TestValidationErrorJSONForBracketedPath` — JSON round-trip preserves the bracketed path string verbatim.

### load_test.go — change

- None retire. `TestLoadRejectsUnsupportedFieldType` keeps existing semantics (unsupported types still rejected; alias names are NOT in `isSupportedType`'s primitive set — they live in the alias map).

### validate_test.go — change

- `TestValidateMultipleFailuresOrdered` keeps its sort; verify the new bracketed-path failures sort lexicographically against flat names. (`"completion_checklist[1].complete"` sorts before `"id"` etc. — confirm the test fixture's assertions still hold or update.)

### meta_test.go — change

- `TestMetaSchemaLoadsUnderNewGrammar` extends to assert the meta-schema's own `field` kind now declares `element_type` and `element_fields` sub-fields.
- New `TestMetaSchemaValidatesUserElementTypeSchema` — a hand-written user schema using element_type / element_fields / types loads cleanly via the meta-schema's own enforcement.

## 12. Open questions for dev

1. **MCP `schema` tool write-side.** Today `schema(action="create", kind="field", data=...)` is meta-schema-validated against `[ta_schema.field]`. Once the meta-schema declares `element_type` and `element_fields`, agents can pass them in `data`. Confirm: do we want F21 to also wire the `cmd/ta/schema_cmd.go` and `internal/mcp/schema_tool.go` paths to round-trip these keys? Or is F21 strictly the loader/validator and the schema tool gets a follow-up?
2. **Alias visibility scope.** `[<schema>.types.<alias>]` — is the alias visible only within its declaring schema file, or across the whole cascade-merged Registry? Lean toward Registry-wide so the home `~/.ta/schema.toml` can ship a standard library of aliases (`completion_item`, `kv_pair`, etc.). Confirm.
3. **`element_type = "array"` recursive.** `paths` of `paths` of strings. Spec doesn't mention. YAGNI: reject for v1 (`element_type` of `array` not allowed) — unblock later if a real shape needs it. Confirm.
4. **`default` on element_fields.** A required element-field with a default — apply or not? Existing `default` is informational only (load.go comment line 90). Stay informational; no behaviour change. Confirm.
5. **Cap on element_fields nesting depth?** §8 above recommends NO cap for v1. Confirm.

Block on these before implementation.

## 13. Memory rules consulted

- `feedback_ta_id.md` — F21 doesn't touch ids; no impact.
- `feedback_ta_one_schema_file_per_dir.md` — `[<schema>.types.<alias>]` keeps everything in the single schema file; no new files. Compatible.
- `feedback_qa_before_every_commit.md` — orchestrator runs go-qa-proof + go-qa-falsification on the implementation commit; this plan is the input.
- `feedback_no_raw_gofmt.md` — formatting via mage target only.
- Project CLAUDE.md — `MAGEFILE_JSON=1` on every test run; verify via `mage test` (not raw `go test`).

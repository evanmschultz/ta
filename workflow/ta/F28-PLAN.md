# F28 — Direct Nested-Table Inner-Shape Validation

## 1. Slice Shape

- **Single atomic commit.** Confirmed. The seven items below ship together, gated by a paired `go-qa-proof` + `go-qa-falsification` review per the QA-before-every-commit rule.
- Scope: extend field-level grammar so `[<db>.<type>.fields.<f>.fields.<sub>]` is a recognized declaration when `<f>` has `type = "table"`. Mirrors F21.2 (`element_fields` for arrays) but for single nested tables.

## 2. Syntax Choice

- **Use `fields`.** Recommendation accepted.
- Rationale: TOML already nests db-type-fields as `<db>.<type>.fields.<x>`. Reusing the literal `fields` makes the inner-shape declaration `<...>.<f>.fields.<sub>` — uniform, no new keyword, agent-discoverable from the meta-schema's existing pattern.
- Reject `table_fields` (asymmetric with `element_fields`'s naming and introduces a third spelling). Reject overloading `element_fields` (the `element_*` prefix is bound to array semantics; conflating breaks readability and the validator's existing branch on `field.Type == TypeArray`).

## 3. Grammar Restriction

- `fields` only valid when `type = "table"`. Reject everywhere else with a load-time error mirroring the existing `element_type` / `element_fields` invariant block (`internal/schema/load.go:1706-1731`).
- `element_fields` stays valid only when `element_type = "table"` (already enforced; no change).
- Concretely, in `buildField` add three checks after the switch:
  - `len(f.Fields) > 0 && f.Type != TypeTable` → reject `fields is only valid on type = "table"`.
  - `f.Type == TypeTable && f.ElementType != ""` → reject `element_type is only valid on type = "array"` (already covered by existing rule at L1711, no new check needed — verify ordering).
  - `f.Type == TypeTable && len(f.ElementFields) > 0` → already covered by existing L1722; no new check.

## 4. Nesting Depth

- **No cap.** Lean accepted. TOML's bracket grammar self-limits practical depth (path lengths blow up before any sane schema would care). Cycle detection covers correctness; depth is a non-issue because inline-only nested tables cannot self-reference (each TOML bracket path is unique by construction).
- Document this choice in the doc-comment on the new `Field.Fields` doc.

## 5. Validator Path Format

- Confirmed: existing grammar already supports both shapes.
- Array-of-tables sub-field: `completion_checklist[1].complete` (existing; `error.go:46`).
- Direct nested-table sub-field: `completion_contract.start_criteria` (new; strict subset — no `[i]` token).
- Implementation: refactor `validateElementTable` (`validate.go:194-255`) to take a `pathPrefix string` and use it directly (the function already builds `elemPath + "." + fname` — that's exactly the dotted concatenation we need). For direct nested-table the prefix is the bare field name; for array elements it is `<name>[<i>]`. Single helper, two call sites.
- Update `FieldFailure.Field` doc (`error.go:40-50`) to add the new shape line: `- Nested table sub-field: "completion_contract.start_criteria"`.

## 6. Aliases Interaction

- **Inline only for now.** Lean accepted.
- Rationale: aliases inline through `element_type = "<alias>"` (`load.go:1248-1278`). For direct nested tables, the syntactic equivalent would be `type = "<alias>"`, but `type` is constrained to the seven primitives (`meta_schema.toml:106`). Adding alias support to `type` is a separate slice (touches the entire type-validation grammar) and would conflict with the `fields = "<alias>"` flat reference used in some other systems. Defer.
- `inlineFieldRecursive` (`load.go:1241-1292`) MUST still recurse into `Fields` — a nested-table sub-field can itself declare `element_type = "<alias>"`, and that inner alias must be inlined. Add a parallel `if len(f.Fields) > 0 { ... }` block after the existing ElementFields recursion at L1280-1290.

## 7. Meta-Schema

- Add `[ta_schema.field.fields.fields]` after the `element_fields` block at `meta_schema.toml:135-137`:

```toml
[ta_schema.field.fields.fields]
type = "table"
description = "When type = \"table\", the per-instance field shape declared inline. Each sub-key is itself a [field] body (recursive; arbitrary nesting depth). Forbidden when type is anything other than \"table\". Required fields inside `fields` apply to the single nested table value (not per-element — element_fields covers arrays of tables). Aliases (element_type = \"<alias>\") are not supported on the outer field; alias references go through element_type chains for arrays only."
```

- Self-host: the meta-schema's own `[ta_schema.field.fields.<x>]` declarations are themselves nested-table-shaped. Loading the meta-schema with the new `fields` key recognized exercises the new path on the very first Load. This is the one-line meta-test.

## 8. Test Coverage

Sixteen tests minimum, listed by file and name:

**`internal/schema/load_test.go` — 11 tests**

1. `TestBuildField_AcceptsFields` — `[db.type.fields.contract] type="table"` plus `[db.type.fields.contract.fields.start] type="string"` loads cleanly; resulting Field has `Type=TypeTable` and `Fields["start"].Type=TypeString`.
2. `TestBuildField_RejectsFieldsOnNonTable` — `type = "string"` with `[..fields.x.fields.y]` → load error containing `fields is only valid on type = "table"`.
3. `TestBuildField_RejectsFieldsOnArray` — `type = "array"` with `fields` → same error (covers the array-vs-table separation explicitly).
4. `TestBuildField_RejectsFieldsAlongsideElementFields` — `type="table"` plus a stray `element_fields` block → load error (existing invariant, regression-guarded).
5. `TestBuildField_RejectsFieldsAlongsideElementType` — `type="table"` plus `element_type` → load error (existing invariant).
6. `TestBuildField_NestedFieldsRecursive` — three levels deep: `contract.fields.checklist.fields.item.fields.text` → all resolve, cloneField produces independent Fields maps at every level.
7. `TestBuildField_NestedFieldsArrayInside` — nested table contains an `array` sub-field with `element_type = "string"` → loader threads element_type into the inner Field.
8. `TestBuildField_NestedFieldsAliasInside` — nested table sub-field has `element_type = "<alias>"`; alias inlining must reach the inner field via `inlineFieldRecursive` recursion through Fields.
9. `TestBuildField_RejectsUnknownKeyMessageMentionsFields` — error message at L1694 is updated to list `fields` in the allowed set; verify literal substring.
10. `TestCloneField_DeepClonesFields` — mutating a cloned Field's `Fields` map does not affect the source (regression for base-inheritance aliasing).
11. `TestExpandBases_NestedTableInBase` — a base declares a `type=table` field with inner `fields`; concrete type extending it inherits a deep-cloned copy.

**`internal/schema/validate_test.go` — 5 tests**

12. `TestValidate_NestedTableHappyPath` — `completion_contract` table with all sub-fields valid → no failures.
13. `TestValidate_NestedTableMissingRequired` — required sub-field absent → failure with `Field = "completion_contract.start_criteria"`, `Kind = FailureMissingRequired`.
14. `TestValidate_NestedTableTypeMismatch` — sub-field with wrong primitive type → `Field = "completion_contract.require_children_complete"`, `Kind = FailureTypeMismatch`.
15. `TestValidate_NestedTableUnknownSubField` — extra sub-key → `Field = "completion_contract.bogus"`, `Kind = FailureUnknownField`.
16. `TestValidate_NestedTableTwoLevelsDeep` — sub-field is itself `type=table` with its own `fields`; failure on a doubly-nested leaf produces `Field = "outer.inner.leaf"`.

**`internal/schema/meta_test.go` — 1 test (or extend existing)**

17. `TestMetaSchema_SelfHostsNestedFieldsKey` — the embedded `meta_schema.toml` parses cleanly with the new `fields` key recognized; the resolved Registry's `ta_schema.field.fields.fields` Field has `Type = TypeTable`. Self-host check.

Mage targets for the build-task acceptance:
- `mage test-pkg ./internal/schema` for the package suite.
- `mage check` for the full pre-commit gate (fmt + vet + test). Discover available targets via `mage -l` (memory rule: `mage install` is dev-only, never a verification target).

## 9. Cascade Restoration

After loader supports `fields`, restore the F27-workaround drops in `examples/schemas/cascade.toml`:

- Re-add `[project.bases.ActionItem.fields.completion_contract]` with `type = "table"` and `description`.
- Re-add the six sub-field declarations under `[project.bases.ActionItem.fields.completion_contract.fields.<sub>]`:
  - `start_criteria` — `type = "array"`, `element_type = "string"`.
  - `completion_criteria` — `type = "array"`, `element_type = "string"`.
  - `completion_checklist` — `type = "array"`, `element_type = "table"`, plus `[..completion_checklist.element_fields.<x>]` items per the F21.2 grammar.
  - `require_children_complete` — `type = "boolean"`.
  - Plus any others the dev pre-F27 had declared; verify against pre-F27 git history before the build-task starts.
- Confirm via an examples-schemas Load test that cascade.toml parses cleanly post-restoration.

## 10. Open Questions

1. **Does the dev want the new `fields` key permitted on `[<db>.bases.<name>.fields.<f>]` and `[<db>.types.<alias>.fields.<f>]` too, or only on concrete-type fields?** The mechanical answer is "all three" because `buildField` is shared. But the alias path may need restriction (see §6 above). Default: allow on all three (bases / aliases / concrete types) since `buildField` is the single recognizer. If dev wants alias bodies to forbid nested `fields`, add a load-time gate.
2. **Should `Fields` be exported on the JSON contract of failures?** Today `FieldFailure` only carries the dotted path (`Field`); no per-failure dump of the schema's nested shape. Lean: no change — keep the JSON contract identical; the dotted path is sufficient for agents to look up the schema themselves.
3. **Pre-F27 cascade.toml diff** — does the dev have a git ref or commit hash for the dropped declarations, or should the build-task reconstruct from `completion_contract` semantics? Builder needs this before the Cascade Restoration step.

## 11. Build Task Summary

One build-task, one commit. Files touched:

- `internal/schema/schema.go` — add `Fields map[string]Field` to `Field` struct; doc-comment.
- `internal/schema/load.go` — new `fieldKeyFields` constant; new switch case in `buildField`; updated unknown-key error string; new invariant check; recursion in `inlineFieldRecursive`; deep-clone in `cloneField`.
- `internal/schema/validate.go` — new branch in `validateField` for `Type == TypeTable && len(Fields) > 0`; refactor `validateElementTable` to a path-prefix-parameterized helper used by both array-element and direct nested-table walks.
- `internal/schema/error.go` — extend `FieldFailure.Field` doc-comment with the nested-table shape example.
- `internal/schema/meta_schema.toml` — add `[ta_schema.field.fields.fields]` block.
- `internal/schema/load_test.go` — 11 new tests.
- `internal/schema/validate_test.go` — 5 new tests.
- `internal/schema/meta_test.go` — 1 new test (or extend existing).
- `examples/schemas/cascade.toml` — restore `completion_contract` sub-field declarations.

Acceptance: `mage check` clean, all 17 new tests pass, cascade.toml round-trips through Load without error.

// L3-G9-D3b: schema-mutation TUI factory. Builds a bubbletea-direct
// form against the embedded meta-schema for one of the four mutation
// kinds (db, type, field, base). Reuses cmd/ta/form.go's existing
// formModel — the factory derives a schema.SectionType from
// schema.MetaSchemaForKind (landed by D3a) and hands it to FormFor.
//
// The factory is consumed by cmd/ta/schema_cmd.go's RunE dispatch:
// when action=create, kind ∈ {db,type,field,base}, --data is empty,
// and stdin is a real TTY, the form fires; on submit, the collected
// payload feeds runSchemaMutate exactly as a non-interactive
// --data='{...}' would. Off-TTY callers and explicit --data callers
// keep the existing non-interactive path unchanged.
//
// F7 rollback note: action=create + kind=type with an empty `fields`
// table rolls back in ops.MutateSchema (meta-schema rule: every type
// must declare at least one own field). The cmd/ta dispatch (NOT this
// factory) catches that error and surfaces a laslig Warn hint
// pointing at `ta schema --action=create --kind=field --name=<type>.<field>`
// so the operator sees the recovery path without re-reading the
// meta-schema docs. This factory itself is pure — no side effects,
// no error surfacing; only meta-schema resolution + form construction.

package main

import (
	"fmt"

	"github.com/evanmschultz/ta/internal/schema"
)

// newSchemaMutateForm builds a bubbletea formModel + collect closure
// for a schema-mutation action. The kind selects which meta-schema
// SectionType drives the field walk (db / type / field / base).
// prefillData, when non-nil, supplies starting values for matching
// field names (used by action=update so existing values render in
// the form for in-place edit).
//
// Returns the form model, the metadata slice (for tests that bypass
// the model and write directly into raw pointers), and the collect
// closure that converts the form's per-field raw state into the
// map[string]any payload ops.MutateSchema consumes.
//
// Errors only on meta-schema resolution failure (build-time
// corruption — never user-input-driven; the four valid kinds are
// hard-coded in MetaSchemaForKind).
func newSchemaMutateForm(kind string, prefillData map[string]any) (*formModel, []FormField, func() (map[string]any, error), error) {
	st, err := schema.MetaSchemaForKind(kind)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("schema mutate form: %w", err)
	}
	// FormFor honors the schema.SectionType verbatim — sorted-name
	// field iteration, dispatch table for widget kinds, prefill
	// stringification per type. isUpdate=false here because schema
	// mutations always submit a full payload; the patch-style omit-on-
	// unchanged logic is a record-update concern, not a schema concern.
	form, meta, collect := FormFor(*st, prefillData, false)
	return form, meta, collect, nil
}

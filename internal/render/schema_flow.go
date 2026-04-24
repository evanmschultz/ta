package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/schema"
)

// SchemaFlow renders the resolved schema registry as per-field flow
// blocks rather than a Markdown table (V2-PLAN §12.17.5 [C1]).
//
// The pre-[C1] `renderSchemaMarkdown` output piped every db+type+field
// triple through a fixed-column Markdown table whose description column
// wrapped word-by-word under narrow terminals — unreadable at 80 cols
// and worse in an editor pane. SchemaFlow replaces that with a flow
// layout built from laslig primitives:
//
//   - Top-level Markdown block for "# Schema for <path>" and the
//     "Resolved from" source list (glamour renders the code-span
//     formatting cleanly; this part was already prose-shaped).
//   - One Section header per db (e.g. "plans"), a KV block of the db
//     meta-fields (shape / path / format), and a Paragraph body for
//     the db description when present.
//   - One Section header per type (e.g. "plans.task"), an optional
//     heading-level KV row, and a Paragraph body for the type
//     description when present.
//   - One KV block per field carrying label/value rows (type / required
//     / default / enum / format) followed by a Paragraph body for the
//     field description. Field descriptions are the payload that table
//     cells mangled; as Paragraph bodies laslig soft-wraps them at full
//     terminal width rather than at column width.
//
// path is the project root the schema was resolved from; scope is the
// dotted sub-selector the caller asked for (empty = whole registry);
// sources is the resolution's source file list (verbatim from
// config.Resolution.Sources); dbs is the db map to render (already
// filtered by the caller when scope names one db or one type).
//
// Iteration order is alphabetical by name at every level (db, type,
// field) so CLI output is deterministic independent of Go map-iteration
// order.
func (r *Renderer) SchemaFlow(path, scope string, sources []string, dbs map[string]schema.DB) error {
	if err := r.p.Markdown(laslig.Markdown{Body: schemaFlowHeader(path, scope, sources)}); err != nil {
		return err
	}
	dbNames := sortedKeys(dbs)
	for _, dbName := range dbNames {
		if err := r.renderDB(dbs[dbName]); err != nil {
			return err
		}
	}
	return nil
}

// renderDB writes one db block: Section header, db-meta KV block, and
// the db-description Paragraph, then each declared type under it.
func (r *Renderer) renderDB(db schema.DB) error {
	if err := r.p.Section(db.Name); err != nil {
		return err
	}
	dbPairs := []laslig.Field{
		{Label: "shape", Value: string(db.Shape)},
		{Label: "path", Value: db.Path},
		{Label: "format", Value: string(db.Format)},
	}
	if err := r.p.KV(laslig.KV{Pairs: dbPairs}); err != nil {
		return err
	}
	if db.Description != "" {
		if err := r.p.Paragraph(laslig.Paragraph{Body: db.Description}); err != nil {
			return err
		}
	}
	typeNames := make([]string, 0, len(db.Types))
	for n := range db.Types {
		typeNames = append(typeNames, n)
	}
	sort.Strings(typeNames)
	for _, tname := range typeNames {
		if err := r.renderType(db.Name, db.Types[tname]); err != nil {
			return err
		}
	}
	return nil
}

// renderType writes one type block: Section header keyed on the full
// "<db>.<type>" address (so the UI shows the address the user would
// pass to a subcommand), optional heading-level KV row, the
// type-description Paragraph, then one labelled KV+Paragraph block per
// declared field.
func (r *Renderer) renderType(dbName string, t schema.SectionType) error {
	if err := r.p.Section(dbName + "." + t.Name); err != nil {
		return err
	}
	if t.Heading != 0 {
		if err := r.p.KV(laslig.KV{
			Pairs: []laslig.Field{{Label: "heading", Value: fmt.Sprintf("%d", t.Heading)}},
		}); err != nil {
			return err
		}
	}
	if t.Description != "" {
		if err := r.p.Paragraph(laslig.Paragraph{Body: t.Description}); err != nil {
			return err
		}
	}
	fieldNames := make([]string, 0, len(t.Fields))
	for fn := range t.Fields {
		fieldNames = append(fieldNames, fn)
	}
	sort.Strings(fieldNames)
	for _, fn := range fieldNames {
		if err := r.renderSchemaField(t.Fields[fn]); err != nil {
			return err
		}
	}
	return nil
}

// renderSchemaField writes one field block: a titled KV of declared
// metadata (type / required / default / enum / format, only the rows
// that carry data), followed by the description as a Paragraph body so
// laslig's own wrapping handles narrow terminals rather than cell
// fragmentation.
func (r *Renderer) renderSchemaField(f schema.Field) error {
	pairs := []laslig.Field{{Label: "type", Value: string(f.Type)}}
	if f.Required {
		pairs = append(pairs, laslig.Field{Label: "required", Value: "yes"})
	} else {
		pairs = append(pairs, laslig.Field{Label: "required", Value: "no"})
	}
	if f.Default != nil {
		pairs = append(pairs, laslig.Field{Label: "default", Value: fmt.Sprintf("%v", f.Default)})
	}
	if len(f.Enum) > 0 {
		pairs = append(pairs, laslig.Field{Label: "enum", Value: formatEnum(f.Enum)})
	}
	if f.Format != "" {
		pairs = append(pairs, laslig.Field{Label: "format", Value: f.Format})
	}
	if err := r.p.KV(laslig.KV{Title: f.Name, Pairs: pairs}); err != nil {
		return err
	}
	if f.Description != "" {
		if err := r.p.Paragraph(laslig.Paragraph{Body: f.Description}); err != nil {
			return err
		}
	}
	return nil
}

// schemaFlowHeader builds the top-of-output Markdown block. The header
// is kept short and declarative; everything else is rendered as
// structured laslig blocks, not inside this markdown body.
func schemaFlowHeader(path, scope string, sources []string) string {
	var sb strings.Builder
	if scope != "" {
		fmt.Fprintf(&sb, "# Schema for `%s` (scope `%s`)\n\n", path, scope)
	} else {
		fmt.Fprintf(&sb, "# Schema for `%s`\n\n", path)
	}
	if len(sources) > 0 {
		sb.WriteString("**Resolved from:**\n\n")
		for _, s := range sources {
			fmt.Fprintf(&sb, "- `%s`\n", s)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// formatEnum renders the enum slice as a bracketed comma-separated list
// for display in the KV "enum" row. Strings are not quoted — the schema
// declares scalar enum values inline and the display mirrors the
// schema-file shape rather than Go's %v verbatim (which would bracket
// values as `[a b c]` without commas).
func formatEnum(values []any) string {
	parts := make([]string, 0, len(values))
	for _, v := range values {
		parts = append(parts, fmt.Sprintf("%v", v))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// sortedKeys returns the keys of a map[string]schema.DB in alphabetical
// order. Small helper local to schema rendering so callers don't have
// to sort at each call site.
func sortedKeys(dbs map[string]schema.DB) []string {
	out := make([]string, 0, len(dbs))
	for k := range dbs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

package ops

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// extractFields returns the named fields of the located record as a
// JSON-shaped map. Field names are validated against the declared type
// so a typo errors loudly (ErrUnknownField) rather than silently
// returning a half-filled map.
//
// TOML records: the backend's Find already returned the byte range for
// this section. We decode the FULL file bytes once with pelletier
// (TOML's native multi-bracket model does not support decoding a
// fragment directly), then walk the address tree to isolate the
// target record's fields.
//
// MD records: body-only per §5.3.3. The backend's section bytes are
// "<heading>\n\n<body>\n". fields=["body"] returns the body without
// the heading line; any other field name errors as not declared.
func extractFields(fileBuf []byte, sec record.Section, db schema.DB, addrType string, relPath string, fields []string) (map[string]any, error) {
	st, ok := db.Types[addrType]
	if !ok {
		return nil, fmt.Errorf("%w: type %q not declared on db %q", ErrUnknownField, addrType, db.Name)
	}
	// Every requested field must be declared, whether or not it is
	// actually present in the record on disk (absent declared fields
	// just omit from the returned map).
	for _, name := range fields {
		if _, ok := st.Fields[name]; !ok {
			return nil, fmt.Errorf("%w: field %q not declared on %q", ErrUnknownField, name, addrType)
		}
	}

	switch db.Format {
	case schema.FormatTOML:
		return extractTOMLFields(fileBuf, relPath, fields)
	case schema.FormatMD:
		// Per F31: file-as-record dbs route through the frontmatter
		// extractor; section-mode dbs keep the body-only path.
		if st.IsFileRecord() {
			return extractFileRecordFields(fileBuf, st, fields)
		}
		return extractMDFields(fileBuf, sec, fields)
	default:
		return nil, fmt.Errorf("%w: db %q format=%q", ErrUnsupportedFormat, db.Name, db.Format)
	}
}

// extractTOMLFields decodes the whole file via pelletier, walks
// relPath's dotted segments, and returns the named leaf values.
// relPath is the backend-relative address (strip already applied).
func extractTOMLFields(fileBuf []byte, relPath string, fields []string) (map[string]any, error) {
	var root map[string]any
	if err := toml.Unmarshal(fileBuf, &root); err != nil {
		return nil, fmt.Errorf("ops: decode file: %w", err)
	}
	segs := strings.Split(relPath, ".")
	cursor := root
	for _, seg := range segs {
		next, ok := cursor[seg]
		if !ok {
			return nil, fmt.Errorf("%w: no record at %q", ErrRecordNotFound, relPath)
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w: %q is not a table", ErrRecordNotFound, relPath)
		}
		cursor = nextMap
	}
	out := make(map[string]any, len(fields))
	for _, name := range fields {
		if v, ok := cursor[name]; ok {
			out[name] = v
		}
	}
	return out, nil
}

// extractMDFields supports the body-only layout of §5.3.3. Under that
// layout the only readable field is "body" (the record's bytes stripped
// of the heading line). A declared field with any other name passes
// the outer schema-declared check but cannot be served by this backend;
// erroring loudly here keeps the extractor contract honest — callers
// should not silently receive an empty field entry. When MD frontmatter
// (§5.3.4) lands post-MVP this branch extends to parse fenced fields.
func extractMDFields(fileBuf []byte, sec record.Section, fields []string) (map[string]any, error) {
	if err := md.CheckBackableFields(fields); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrUnknownField, err.Error())
	}
	out := make(map[string]any, len(fields))
	raw := fileBuf[sec.Range[0]:sec.Range[1]]
	body := stripHeadingLine(raw)
	for _, name := range fields {
		// CheckBackableFields has already rejected anything other than
		// "body"; this loop just populates the result for each
		// requested name.
		_ = name
		out["body"] = string(body)
	}
	return out, nil
}

// extractAllDeclaredFields returns every declared field the record
// actually carries on disk. Missing declared fields are silently
// omitted. Used by GetAllFields in the B3 unified-render flow: `ta get`
// (no --fields) synthesizes all declared fields from schema + decoded
// record, then routes through the shared render helper (V2-PLAN
// §12.17.5 [B3]).
//
// Unlike extractFields this never returns ErrUnknownField — the field
// set is the declared type itself, not user input. For MD body-only
// records this surfaces "body" when the type declares it and silently
// skips any non-body declared fields (the body-only layout cannot
// serve them; the strict path in extractFields still errors on explicit
// user requests so the contract lie is surfaced there, not silently
// masked here).
func extractAllDeclaredFields(fileBuf []byte, sec record.Section, db schema.DB, typeSt schema.SectionType, relPath string) (map[string]any, error) {
	switch db.Format {
	case schema.FormatTOML:
		names := make([]string, 0, len(typeSt.Fields))
		for name := range typeSt.Fields {
			names = append(names, name)
		}
		return extractTOMLFields(fileBuf, relPath, names)
	case schema.FormatMD:
		// Per F31: file-as-record returns frontmatter map + body field.
		if typeSt.IsFileRecord() {
			names := make([]string, 0, len(typeSt.Fields))
			for name := range typeSt.Fields {
				names = append(names, name)
			}
			return extractFileRecordFields(fileBuf, typeSt, names)
		}
		out := map[string]any{}
		if _, ok := typeSt.Fields["body"]; ok {
			raw := fileBuf[sec.Range[0]:sec.Range[1]]
			out["body"] = string(stripHeadingLine(raw))
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: db %q format=%q", ErrUnsupportedFormat, db.Name, db.Format)
	}
}

// extractFileRecordFields parses the frontmatter + body of a
// file-as-record buffer and returns the named subset of declared
// fields. The body field is sourced from the body bytes; every other
// field is sourced from the YAML frontmatter map. Per F31.
//
// An empty buffer (file-as-record records require frontmatter, but a
// brand-new record about to be created produces a 0-byte file before
// the first emit) returns an empty (non-nil) map; loadExistingFields
// is the only caller that can be reached on empty buffers and it
// already tolerates an empty result via overlayPatch.
func extractFileRecordFields(fileBuf []byte, st schema.SectionType, fields []string) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	if len(fileBuf) == 0 {
		return out, nil
	}
	front, body, err := md.SplitFrontmatter(fileBuf)
	if err != nil {
		return nil, fmt.Errorf("file-as-record: split frontmatter: %w", err)
	}
	var fm map[string]any
	if front != nil {
		fm, err = md.DecodeFrontmatter(front)
		if err != nil {
			return nil, fmt.Errorf("file-as-record: decode frontmatter: %w", err)
		}
	}
	for _, name := range fields {
		if name == st.BodyField {
			out[name] = string(body)
			continue
		}
		if v, ok := fm[name]; ok {
			out[name] = v
		}
	}
	return out, nil
}

// stripHeadingLine returns raw with the first line (the heading line)
// and any directly-following blank separator removed. If raw has no
// newline the whole buffer is considered the heading and the return
// is empty. This mirrors the Emit format: "## Heading\n\n<body>\n".
func stripHeadingLine(raw []byte) []byte {
	_, rest, ok := bytes.Cut(raw, []byte{'\n'})
	if !ok {
		return nil
	}
	// Skip at most one blank-line separator.
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	return rest
}

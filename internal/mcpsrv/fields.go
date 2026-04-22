package mcpsrv

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/pelletier/go-toml/v2"

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
		return nil, fmt.Errorf("mcpsrv: decode file: %w", err)
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

// extractMDFields supports the body-only layout of §5.3.3. The only
// declared field is "body"; we return the record's bytes stripped of
// the heading line.
func extractMDFields(fileBuf []byte, sec record.Section, fields []string) (map[string]any, error) {
	out := make(map[string]any, len(fields))
	raw := fileBuf[sec.Range[0]:sec.Range[1]]
	body := stripHeadingLine(raw)
	for _, name := range fields {
		if name == "body" {
			out["body"] = string(body)
		}
		// Any other declared field is legal by schema but not backed
		// by emit/read semantics under the body-only layout. Leave it
		// out of the result; the caller can inspect presence/absence.
	}
	return out, nil
}

// stripHeadingLine returns raw with the first line (the heading line)
// and any directly-following blank separator removed. If raw has no
// newline the whole buffer is considered the heading and the return
// is empty. This mirrors the Emit format: "## Heading\n\n<body>\n".
func stripHeadingLine(raw []byte) []byte {
	nl := bytes.IndexByte(raw, '\n')
	if nl < 0 {
		return nil
	}
	rest := raw[nl+1:]
	// Skip at most one blank-line separator.
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	return rest
}

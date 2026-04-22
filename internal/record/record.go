// Package record defines the format-neutral types that sit above the
// per-format backends. A Record is the validated, JSON-shaped projection
// of one record's fields; a Section wraps that Record with its on-disk
// address and byte range inside the owning file buffer; a Backend is the
// thin interface each format implements so the lang-agnostic layer above
// (schema resolution, validation, search, MCP routing) can stay
// format-agnostic.
//
// Backends are schema-aware at construction (V2-PLAN §2.10 / §5.1): the
// per-backend NewBackend factory accepts a list of DeclaredType values
// describing every record type declared on the owning db. The scanner
// treats only declared markers as section boundaries; non-declared
// markers between two declared records belong to the first record's
// body (V2-PLAN §2.11). Interface method signatures are unchanged from
// §12.1; what changes is how each backend is instantiated.
//
// See docs/V2-PLAN.md §5.1 for the full design rationale.
package record

// Record is the validated, format-neutral representation of a single
// record's fields: JSON-shaped, keyed by field name.
type Record map[string]any

// Section is a backend's view of one on-disk record.
//
// Path is the full address of the record ("<db>.<type>.<id>..."); Range
// is the [start, end) byte range of the record's bytes inside the file
// buffer the backend was given — running from this declared record's
// start (heading line or bracket line) to the start of the next
// declared record (at any type) or EOF, per V2-PLAN §2.11. Record
// carries the parsed fields when the backend has chosen to populate
// them, and may be nil when the backend returns locator-only views.
type Section struct {
	Path   string
	Range  [2]int
	Record Record
}

// DeclaredType is the minimum schema information a backend needs to
// section a buffer. Each backend interprets the fields per its format
// (V2-PLAN §5.1):
//
//   - TOML: Name is the bracket-path prefix the backend treats as a
//     record boundary. A bracket whose path equals Name or starts with
//     Name+"." is a declared record. Heading is unused.
//   - MD: Heading is the ATX heading level (1..6) at which this type's
//     sections live. Name is the type name used when composing
//     addresses for records at that level (e.g. "section" for records
//     addressed as "<db>.section.<slug>").
//
// Types within a single owning db must not share the same Heading on
// the MD side (meta-schema rule per V2-PLAN §4.7).
type DeclaredType struct {
	Name    string
	Heading int
}

// Backend is the per-format seam. All format-specific byte-level work
// (section scanning, canonical emission, surgical splicing) lives behind
// this interface; the lang-agnostic layer above it (schema resolution,
// validation, search, MCP routing) speaks only Section, Record, and raw
// file buffers.
type Backend interface {
	// List returns every section address under scope, or every section
	// in the buffer when scope == "".
	List(buf []byte, scope string) ([]string, error)

	// Find locates one section by full address. The returned bool is
	// false when no section matches; err is non-nil only for parse
	// failures on buf.
	Find(buf []byte, section string) (Section, bool, error)

	// Emit serializes a validated Record to this format's canonical
	// bytes for the given section address. The returned bytes include
	// the heading/header line for the record.
	Emit(section string, rec Record) ([]byte, error)

	// Splice replaces (or appends) a section's bytes in buf, preserving
	// every byte outside the touched range verbatim. emitted is the
	// bytes produced by Emit for the same section address.
	Splice(buf []byte, section string, emitted []byte) ([]byte, error)
}

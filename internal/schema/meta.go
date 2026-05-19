package schema

import (
	_ "embed"
	"fmt"
)

// MetaSchemaTOML is the canonical meta-schema document: a literal
// description of the schema language itself, surfaced via the `schema`
// tool's `scope = "ta_schema"` (V2-PLAN §3.3, §12.2). It is the single
// source of truth an agent reads to construct a valid `schema(action=
// "create"|"update", kind, name, data)` call.
//
// The meta-schema is embedded at compile time from meta_schema.toml and
// never read from disk at runtime; callers can rely on it being present
// in every binary.
//
//go:embed meta_schema.toml
var MetaSchemaTOML string

// MetaSchemaPath is the scope identifier that selects the meta-schema
// via `schema(action="get", scope="ta_schema")`.
const MetaSchemaPath = "ta_schema"

// MetaSchemaForKind returns the SectionType describing one meta-schema
// kind ("db", "type", "field", or "base") so callers — most notably the
// schema-mutation TUI (L3-G9-D3b) — can drive a typed form off the
// embedded meta-schema without re-parsing TOML themselves.
//
// The returned *SectionType is a defensive copy of the registry entry;
// mutating it does not affect the embedded meta-schema. An error is
// returned for any kind not declared in the meta-schema. The function
// also returns an error if the embedded meta-schema fails to load,
// which would indicate a build-time corruption rather than a user
// input problem.
func MetaSchemaForKind(kind string) (*SectionType, error) {
	reg, err := LoadBytes([]byte(MetaSchemaTOML))
	if err != nil {
		return nil, fmt.Errorf("schema: load embedded meta-schema: %w", err)
	}
	db, ok := reg.DBs[MetaSchemaPath]
	if !ok {
		return nil, fmt.Errorf("schema: embedded meta-schema missing %q db", MetaSchemaPath)
	}
	st, ok := db.Types[kind]
	if !ok {
		return nil, fmt.Errorf("schema: unknown meta-schema kind %q (valid kinds: db, type, field, base)", kind)
	}
	// Defensive copy so callers can mutate the returned value without
	// poisoning the registry-held entry (Go map values are not
	// addressable in any case, so we must return the address of a
	// local).
	cp := st
	return &cp, nil
}

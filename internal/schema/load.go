package schema

import (
	"fmt"
	"io"

	"github.com/pelletier/go-toml/v2"
)

// Load reads a schema config document from r and returns the resolved
// Registry. Unknown top-level keys are rejected so typos surface at load
// time instead of silently disabling a schema type.
func Load(r io.Reader) (Registry, error) {
	dec := toml.NewDecoder(r)
	dec.DisallowUnknownFields()

	var cfg schemaConfig
	if err := dec.Decode(&cfg); err != nil {
		return Registry{}, fmt.Errorf("schema: parse config: %w", err)
	}

	reg := Registry{Types: make(map[string]SectionType, len(cfg.Schema))}
	for name, raw := range cfg.Schema {
		st := SectionType{
			Name:        name,
			Description: raw.Description,
			Fields:      make(map[string]Field, len(raw.Fields)),
		}
		for fname, rf := range raw.Fields {
			if !isSupportedType(rf.Type) {
				return Registry{}, fmt.Errorf(
					"schema: schema.%s.fields.%s: unsupported type %q",
					name, fname, rf.Type,
				)
			}
			st.Fields[fname] = Field{
				Name:        fname,
				Type:        rf.Type,
				Required:    rf.Required,
				Description: rf.Description,
				Enum:        rf.Enum,
				Format:      rf.Format,
				Default:     rf.Default,
			}
		}
		reg.Types[name] = st
	}
	return reg, nil
}

type schemaConfig struct {
	Schema map[string]rawSectionType `toml:"schema"`
}

type rawSectionType struct {
	Description string              `toml:"description"`
	Fields      map[string]rawField `toml:"fields"`
}

type rawField struct {
	Type        Type   `toml:"type"`
	Required    bool   `toml:"required"`
	Description string `toml:"description"`
	Enum        []any  `toml:"enum"`
	Format      string `toml:"format"`
	Default     any    `toml:"default"`
}

func isSupportedType(t Type) bool {
	switch t {
	case TypeString, TypeInteger, TypeFloat, TypeBoolean,
		TypeDatetime, TypeArray, TypeTable:
		return true
	}
	return false
}

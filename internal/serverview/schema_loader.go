package serverview

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// SchemaLoaderResult holds the minimal data needed to render the schema browser:
// the structured schema data (scopes → types → fields hierarchy) and the
// committed Track A template name.
//
// The template name is fixed to "schema_browser.html" as the only
// committed Track A schema browsing template. The Scopes are extracted
// from .ta/schema.toml and reshaped for rendering.
type SchemaLoaderResult struct {
	Scopes       []ScopeView // scopes from the schema
	TemplateName string      // committed Track A template name ("schema_browser.html")
}

// ScopeView represents a top-level scope in the schema (e.g. "cascade").
type ScopeView struct {
	Name        string      // scope name (e.g. "cascade")
	Description string      // scope description
	Types       []TypeView  // types within this scope
}

// TypeView represents a type within a scope (e.g. "cascade.drop").
type TypeView struct {
	Name        string       // type name (e.g. "drop")
	Extends     string       // parent type name if extended (e.g. "ActionItem")
	Description string       // type description
	Fields      []FieldView  // fields within this type
}

// FieldView represents a field within a type.
type FieldView struct {
	Name        string   // field name
	Type        string   // field type (string, integer, boolean, array, etc.)
	Required    bool     // whether the field is required
	Default     any      // default value if any
	Enum        []string // enum values if constrained
	Description string   // field description
}

// LoadSchema reads the schema.toml file from the live .ta project at projectPath,
// extracts the scope→type→field hierarchy, and returns it shaped for rendering.
//
// The template name is always "schema_browser.html" — the only committed
// Track A template for schema browsing.
//
// Returns an error if the project cannot be resolved, the schema file is not found,
// or parsing fails.
func LoadSchema(projectPath string) (SchemaLoaderResult, error) {
	// Construct the path to .ta/schema.toml.
	schemaPath := filepath.Join(projectPath, ".ta", "schema.toml")

	// Read the file.
	data, err := os.ReadFile(schemaPath)
	if err != nil {
		return SchemaLoaderResult{}, fmt.Errorf("read schema file %q: %w", schemaPath, err)
	}

	// Parse the TOML into a generic map. The top level of schema.toml
	// is organized as scope names (keys) with nested type/field info.
	var raw map[string]any
	if err := toml.Unmarshal(data, &raw); err != nil {
		return SchemaLoaderResult{}, fmt.Errorf("parse schema.toml: %w", err)
	}

	// Extract scopes from the raw map. Each top-level key that has a map value
	// is a scope.
	scopes, err := extractScopes(raw)
	if err != nil {
		return SchemaLoaderResult{}, fmt.Errorf("extract scopes: %w", err)
	}

	return SchemaLoaderResult{
		Scopes:       scopes,
		TemplateName: "schema_browser.html",
	}, nil
}

// extractScopes parses the raw TOML map into a list of ScopeView structs.
// It handles the nested [scope.type.fields] hierarchy.
//
// The TOML structure when parsed by go-toml/v2 becomes nested maps:
// {
//   "cascade": {
//     "description": "...",
//     "drop": { "description": "...", "extends": "...", "fields": { ... } },
//     "planner": { ... },
//     ...
//   },
//   ...
// }
func extractScopes(raw map[string]any) ([]ScopeView, error) {
	scopeMap := make(map[string]map[string]any) // scope -> (type -> data)
	scopeDesc := make(map[string]string)        // scope -> description

	// Process each top-level key. Top-level keys are scope names.
	for scopeName, scopeVal := range raw {
		scopeData, ok := scopeVal.(map[string]any)
		if !ok {
			// Skip non-map entries.
			continue
		}

		// Initialize this scope's type map.
		scopeMap[scopeName] = make(map[string]any)

		// Extract scope-level description if present.
		if desc, ok := scopeData["description"].(string); ok {
			scopeDesc[scopeName] = desc
		}

		// Now iterate over the scope's contents to find types.
		// Types are nested under the scope (e.g., under "cascade" we have "drop", "planner", etc.)
		for typeKey, typeVal := range scopeData {
			// Skip the special "description" key and pseudo-entries.
			if typeKey == "description" {
				continue
			}

			// Check if this is a type entry (should be a map).
			if typeData, ok := typeVal.(map[string]any); ok {
				scopeMap[scopeName][typeKey] = typeData
			}
		}
	}

	// Second pass: convert scope map to ScopeView list.
	var scopes []ScopeView
	var scopeNames []string
	for scopeName := range scopeMap {
		scopeNames = append(scopeNames, scopeName)
	}
	sort.Strings(scopeNames)

	typeExtends := make(map[string]string) // "scope.type" -> parent

	for _, scopeName := range scopeNames {
		typeMap := scopeMap[scopeName]

		// Collect type names and sort them.
		var typeNames []string
		for typeName := range typeMap {
			// Skip non-type entries.
			if typeName == "auto_spawn" || typeName == "fields" {
				continue
			}
			typeNames = append(typeNames, typeName)
		}
		sort.Strings(typeNames)

		// Collect "extends" info for types in this scope.
		for typeName, typeData := range typeMap {
			if m, ok := typeData.(map[string]any); ok {
				if extendsVal, ok := m["extends"].(string); ok {
					typeExtends[scopeName+"."+typeName] = extendsVal
				}
			}
		}

		var types []TypeView
		for _, typeName := range typeNames {
			typeData := typeMap[typeName]

			// Extract the type view.
			tv, err := extractType(scopeName, typeName, typeData, typeExtends)
			if err != nil {
				return nil, fmt.Errorf("extract type %q.%q: %w", scopeName, typeName, err)
			}

			types = append(types, tv)
		}

		scope := ScopeView{
			Name:        scopeName,
			Description: scopeDesc[scopeName],
			Types:       types,
		}

		scopes = append(scopes, scope)
	}

	return scopes, nil
}

// extractType converts a type entry into a TypeView.
// The typeData map may contain fields nested under a "fields" key,
// or it may be the fields map itself.
func extractType(scopeName, typeName string, typeData any, typeExtends map[string]string) (TypeView, error) {
	m, ok := typeData.(map[string]any)
	if !ok {
		return TypeView{}, fmt.Errorf("expected map for type %q.%q", scopeName, typeName)
	}

	// Extract the extends field.
	extendsKey := scopeName + "." + typeName
	extendsVal := typeExtends[extendsKey]

	// Extract description.
	desc := coerceStringField(m, "description")

	// Extract fields. Fields are nested under a "fields" key.
	fieldsData, ok := m["fields"].(map[string]any)
	if !ok {
		// No fields data available; return empty fields list.
		fieldsData = make(map[string]any)
	}

	var fields []FieldView
	var fieldNames []string
	for fieldName := range fieldsData {
		fieldNames = append(fieldNames, fieldName)
	}
	sort.Strings(fieldNames)

	for _, fieldName := range fieldNames {
		fieldData := fieldsData[fieldName]

		fv, err := extractField(fieldName, fieldData)
		if err != nil {
			return TypeView{}, fmt.Errorf("extract field %q: %w", fieldName, err)
		}

		fields = append(fields, fv)
	}

	return TypeView{
		Name:        typeName,
		Extends:     extendsVal,
		Description: desc,
		Fields:      fields,
	}, nil
}

// extractField converts a field entry into a FieldView.
func extractField(fieldName string, fieldData any) (FieldView, error) {
	m, ok := fieldData.(map[string]any)
	if !ok {
		return FieldView{}, fmt.Errorf("expected map for field %q", fieldName)
	}

	// Extract the type.
	typeVal := coerceStringField(m, "type")

	// Extract required flag (default false).
	required := false
	if reqVal, ok := m["required"].(bool); ok {
		required = reqVal
	}

	// Extract default value.
	var defaultVal any
	if v, ok := m["default"]; ok {
		defaultVal = v
	}

	// Extract enum values (expected to be []any but we'll convert to []string).
	var enumValues []string
	if enumData, ok := m["enum"].([]any); ok {
		for _, e := range enumData {
			enumValues = append(enumValues, fmt.Sprintf("%v", e))
		}
	}

	// Extract description.
	desc := coerceStringField(m, "description")

	return FieldView{
		Name:        fieldName,
		Type:        typeVal,
		Required:    required,
		Default:     defaultVal,
		Enum:        enumValues,
		Description: desc,
	}, nil
}

// findDotIndex returns the index of the first dot in s, or -1 if not found.
func findDotIndex(s string) int {
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}

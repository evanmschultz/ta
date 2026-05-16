package format

import (
	"fmt"
	"os"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

// HtmlManifest is the concrete Manifest impl for the html format. Selectors
// are CSS selectors; the actual selector → *html.Node matching logic lives
// in the html backend package. This type holds only the manifest data and
// provides the dispatch surface for that backend.
type HtmlManifest struct {
	Selectors_     []string
	SelectorToName map[string]string
	NameToSelector map[string]string
	Description    string
	FieldBindings  map[string]any
}

// BlockName resolves an opaque node to a manifest-declared block name.
// The actual node-shape interpretation is the backend's responsibility;
// this minimal impl returns ("", false) — the backend will wrap or replace
// this method when it walks an *html.Node tree.
func (m *HtmlManifest) BlockName(node any) (string, bool) {
	if s, ok := node.(string); ok {
		if name, hit := m.SelectorToName[s]; hit {
			return name, true
		}
	}
	return "", false
}

// Selectors returns the ordered list of CSS selectors declared in the manifest.
func (m *HtmlManifest) Selectors() []string { return m.Selectors_ }

// MdManifest is the concrete Manifest impl for the md format. Selectors are
// heading-path strings ("##", "## > ###", etc.).
type MdManifest struct {
	Selectors_     []string
	SelectorToName map[string]string
	NameToSelector map[string]string
	Description    string
	FieldBindings  map[string]any
}

func (m *MdManifest) BlockName(node any) (string, bool) {
	if s, ok := node.(string); ok {
		if name, hit := m.SelectorToName[s]; hit {
			return name, true
		}
	}
	return "", false
}

func (m *MdManifest) Selectors() []string { return m.Selectors_ }

// TxtManifest is the concrete Manifest impl for the txt format. Selectors
// are regex pattern strings; the txt backend compiles them.
type TxtManifest struct {
	Selectors_     []string
	SelectorToName map[string]string
	NameToSelector map[string]string
	Description    string
	FieldBindings  map[string]any
}

func (m *TxtManifest) BlockName(node any) (string, bool) {
	if s, ok := node.(string); ok {
		if name, hit := m.SelectorToName[s]; hit {
			return name, true
		}
	}
	return "", false
}

func (m *TxtManifest) Selectors() []string { return m.Selectors_ }

// rawManifest is the on-disk TOML shape — a permissive superset of every
// concrete manifest type. Only `format` is required; the loader picks the
// right selectors table based on that value.
type rawManifest struct {
	Format               string         `toml:"format"`
	Description          string         `toml:"description"`
	Selectors            map[string]any `toml:"selectors"`
	HeadingPathSelectors map[string]any `toml:"heading_path_selectors"`
	RegexSelectors       map[string]any `toml:"regex_selectors"`
	FieldTypeBindings    map[string]any `toml:"field_type_bindings"`
}

// LoadManifestFile reads a manifest TOML file from disk and dispatches to
// the concrete Manifest impl indicated by the `format` field. Supported
// values: "html", "md", "txt". Returns an error for unknown formats,
// missing `format`, or malformed TOML.
//
// # Schema-permissive selector tables
//
// The `template_manifest` schema does not declare any selectors table as
// `required = true` (only `format` is required). LoadManifestFile follows
// the schema: a manifest with no selectors table — OR with a mis-named
// table for the declared format (e.g. `format = "html"` paired with a
// `[heading_path_selectors]` table that belongs to the md format) — is
// accepted SILENTLY, producing a Manifest with an empty Selectors() list.
// Downstream callers MUST handle zero-selector Manifests defensively;
// "no block found" failures during Find/Splice are the surface signal.
//
// This permissiveness is INTENTIONAL: it lets manifest authors stage
// incomplete drafts and lets the schema evolve without breaking loaders.
// If/when stricter validation is wanted, the change is documented as a
// follow-up slice rather than an accidental loader break.
//
// Regression tests pin this behavior; see TestManifest_SilentlyAcceptsEmptySelectors
// and TestManifest_SilentlyAcceptsCrossFormatTable in manifest_test.go.
func LoadManifestFile(path string) (Manifest, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("format: read manifest %q: %w", path, err)
	}
	return loadManifestBytes(buf, path)
}

func loadManifestBytes(buf []byte, source string) (Manifest, error) {
	var raw rawManifest
	if err := toml.Unmarshal(buf, &raw); err != nil {
		return nil, fmt.Errorf("format: parse manifest %q: %w", source, err)
	}
	if raw.Format == "" {
		return nil, fmt.Errorf("format: manifest %q: missing required field %q", source, "format")
	}
	switch raw.Format {
	case "html":
		return buildHtmlManifest(raw, source)
	case "md":
		return buildMdManifest(raw, source)
	case "txt":
		return buildTxtManifest(raw, source)
	default:
		return nil, fmt.Errorf("format: manifest %q: unknown format %q (want html, md, or txt)", source, raw.Format)
	}
}

// normalizeSelectors flattens a free-form table into a deterministic
// (selector, name) pair set + an ordered selector list (sorted by block
// name for reproducibility). Returns an error if any value is not a string.
func normalizeSelectors(table map[string]any, source, field string) (selectors []string, selToName, nameToSel map[string]string, err error) {
	selToName = make(map[string]string, len(table))
	nameToSel = make(map[string]string, len(table))
	names := make([]string, 0, len(table))
	for name, v := range table {
		s, ok := v.(string)
		if !ok {
			return nil, nil, nil, fmt.Errorf("format: manifest %q: %s.%s must be a string, got %T", source, field, name, v)
		}
		selToName[s] = name
		nameToSel[name] = s
		names = append(names, name)
	}
	sort.Strings(names)
	selectors = make([]string, 0, len(names))
	for _, n := range names {
		selectors = append(selectors, nameToSel[n])
	}
	return selectors, selToName, nameToSel, nil
}

func buildHtmlManifest(raw rawManifest, source string) (*HtmlManifest, error) {
	sels, selToName, nameToSel, err := normalizeSelectors(raw.Selectors, source, "selectors")
	if err != nil {
		return nil, err
	}
	return &HtmlManifest{
		Selectors_:     sels,
		SelectorToName: selToName,
		NameToSelector: nameToSel,
		Description:    raw.Description,
		FieldBindings:  raw.FieldTypeBindings,
	}, nil
}

func buildMdManifest(raw rawManifest, source string) (*MdManifest, error) {
	sels, selToName, nameToSel, err := normalizeSelectors(raw.HeadingPathSelectors, source, "heading_path_selectors")
	if err != nil {
		return nil, err
	}
	return &MdManifest{
		Selectors_:     sels,
		SelectorToName: selToName,
		NameToSelector: nameToSel,
		Description:    raw.Description,
		FieldBindings:  raw.FieldTypeBindings,
	}, nil
}

func buildTxtManifest(raw rawManifest, source string) (*TxtManifest, error) {
	sels, selToName, nameToSel, err := normalizeSelectors(raw.RegexSelectors, source, "regex_selectors")
	if err != nil {
		return nil, err
	}
	return &TxtManifest{
		Selectors_:     sels,
		SelectorToName: selToName,
		NameToSelector: nameToSel,
		Description:    raw.Description,
		FieldBindings:  raw.FieldTypeBindings,
	}, nil
}

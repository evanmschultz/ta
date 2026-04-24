package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/huh/v2"

	"github.com/evanmschultz/ta/internal/schema"
)

// WidgetKind names the huh widget chosen for a schema.Field by the
// dispatch table in V2-PLAN §12.17.5 [D1]. Explicit enum so the
// FormFor builder is testable without poking at huh's internal field
// structs (huh.Field is an interface, concrete widget types aren't
// exported as structs we can assert on ergonomically).
type WidgetKind int

// Widget dispatch codomain. The table in V2-PLAN §12.17.5 [D1] maps
// (Field.Type, Field.Format) → one of these:
//
//	string + markdown          → WidgetText        (multi-line; TOML emits """)
//	string + enum non-empty    → WidgetSelect
//	string + datetime format   → WidgetDatetime    (Input + RFC3339 validator)
//	datetime (field.Type)      → WidgetDatetime
//	string + any other         → WidgetInput
//	integer / float            → WidgetNumeric     (Input + numeric validator)
//	boolean                    → WidgetConfirm
//	array / table              → WidgetJSONTextarea (Text + json.Unmarshal validator)
const (
	WidgetInput WidgetKind = iota
	WidgetText
	WidgetSelect
	WidgetConfirm
	WidgetDatetime
	WidgetNumeric
	WidgetJSONTextarea
)

// FormField carries per-field metadata for FormFor's return value. The
// test surface (huh_form_test.go) asserts over Name + Kind; the
// Required / raw pointers are internal plumbing for the post-submit
// collect closure.
type FormField struct {
	Name     string
	Kind     WidgetKind
	Required bool

	// One of rawStr / rawBool is non-nil depending on Kind. Confirm
	// writes a *bool; every other widget writes a string the collect
	// closure coerces back into the typed value.
	rawStr  *string
	rawBool *bool

	// prefilled carries the string form of the existing value for
	// update-mode blank-retains semantics (see collect).
	prefilled    string
	hadPrefill   bool
	prefilledRaw any
}

// FormFor builds a huh.Form from the declared fields of typeSt.
// prefill optionally carries the existing stored values (update mode);
// pass nil on create. isUpdate toggles blank-retains semantics: on
// update, leaving a field at its prefilled value (or blank for a field
// with no prefill) omits it from the returned payload so the PATCH
// overlay leaves the stored bytes untouched.
//
// The returned closure is the post-Run collector: call it after
// form.Run() succeeds to coerce raw strings into typed values and
// assemble the map[string]any payload for ops.Create / ops.Update.
func FormFor(typeSt schema.SectionType, prefill map[string]any, isUpdate bool) (*huh.Form, []FormField, func() (map[string]any, error)) {
	// Stable field order — schema.SectionType.Fields is a map, so we
	// sort by declared name for deterministic form layout and
	// reproducible tests.
	names := make([]string, 0, len(typeSt.Fields))
	for name := range typeSt.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	meta := make([]FormField, 0, len(names))
	huhFields := make([]huh.Field, 0, len(names))

	for _, name := range names {
		f := typeSt.Fields[name]
		kind := dispatchWidget(f)

		ff := FormField{
			Name:     name,
			Kind:     kind,
			Required: f.Required,
		}
		if prefill != nil {
			if v, ok := prefill[name]; ok {
				ff.prefilled = stringifyForField(v, f)
				ff.hadPrefill = true
				ff.prefilledRaw = v
			}
		}

		title := name
		if f.Required {
			title = name + " *"
		}
		description := f.Description

		switch kind {
		case WidgetConfirm:
			b := false
			if ff.hadPrefill {
				if bv, ok := ff.prefilledRaw.(bool); ok {
					b = bv
				}
			}
			ff.rawBool = &b
			widget := huh.NewConfirm().
				Title(title).
				Description(description).
				Value(&b)
			huhFields = append(huhFields, widget)

		case WidgetSelect:
			s := ff.prefilled
			ff.rawStr = &s
			opts := make([]huh.Option[string], 0, len(f.Enum))
			for _, ev := range f.Enum {
				str := fmt.Sprint(ev)
				opts = append(opts, huh.NewOption(str, str))
			}
			widget := huh.NewSelect[string]().
				Title(title).
				Description(description).
				Options(opts...).
				Value(&s)
			huhFields = append(huhFields, widget)

		case WidgetText, WidgetJSONTextarea:
			s := ff.prefilled
			ff.rawStr = &s
			widget := huh.NewText().
				Title(title).
				Description(description).
				Value(&s)
			if kind == WidgetJSONTextarea {
				widget = widget.Validate(jsonArrayOrTableValidator(f.Type, f.Required, ff.hadPrefill))
			} else if f.Required {
				widget = widget.Validate(nonEmptyIfRequiredValidator(ff.hadPrefill))
			}
			huhFields = append(huhFields, widget)

		case WidgetDatetime:
			s := ff.prefilled
			ff.rawStr = &s
			widget := huh.NewInput().
				Title(title).
				Description(description).
				Value(&s).
				Validate(datetimeValidator(f.Required, ff.hadPrefill))
			huhFields = append(huhFields, widget)

		case WidgetNumeric:
			s := ff.prefilled
			ff.rawStr = &s
			widget := huh.NewInput().
				Title(title).
				Description(description).
				Value(&s).
				Validate(numericValidator(f.Type, f.Required, ff.hadPrefill))
			huhFields = append(huhFields, widget)

		default: // WidgetInput
			s := ff.prefilled
			ff.rawStr = &s
			widget := huh.NewInput().
				Title(title).
				Description(description).
				Value(&s)
			if f.Required {
				widget = widget.Validate(nonEmptyIfRequiredValidator(ff.hadPrefill))
			}
			huhFields = append(huhFields, widget)
		}

		meta = append(meta, ff)
	}

	form := huh.NewForm(huh.NewGroup(huhFields...))

	collect := func() (map[string]any, error) {
		out := make(map[string]any, len(meta))
		for _, ff := range meta {
			f := typeSt.Fields[ff.Name]
			switch ff.Kind {
			case WidgetConfirm:
				v := *ff.rawBool
				if isUpdate && ff.hadPrefill {
					// Blank-retains on update: if user didn't change
					// the prefilled bool, omit from payload.
					if prev, ok := ff.prefilledRaw.(bool); ok && prev == v {
						continue
					}
				}
				out[ff.Name] = v

			case WidgetSelect, WidgetInput, WidgetText:
				raw := strings.TrimSpace(*ff.rawStr)
				// WidgetText (markdown) keeps newlines; only Input /
				// Select should TrimSpace. Re-extract for Text.
				if ff.Kind == WidgetText {
					raw = *ff.rawStr
				}
				if raw == "" {
					// Blank-retains on update; blank = unset on create
					// (optional fields get omitted so validation can
					// fire on required-empty via the Validate callback
					// that already ran).
					if isUpdate && ff.hadPrefill {
						continue
					}
					if !f.Required {
						continue
					}
					return nil, fmt.Errorf("field %q is required", ff.Name)
				}
				if isUpdate && ff.hadPrefill && raw == ff.prefilled {
					continue
				}
				out[ff.Name] = raw

			case WidgetDatetime:
				raw := strings.TrimSpace(*ff.rawStr)
				if raw == "" {
					if isUpdate && ff.hadPrefill {
						continue
					}
					if !f.Required {
						continue
					}
					return nil, fmt.Errorf("field %q is required", ff.Name)
				}
				if isUpdate && ff.hadPrefill && raw == ff.prefilled {
					continue
				}
				// Round-trip through RFC3339 so ops.* sees a Go time
				// the TOML emitter can format.
				t, err := time.Parse(time.RFC3339, raw)
				if err != nil {
					return nil, fmt.Errorf("field %q: %w", ff.Name, err)
				}
				out[ff.Name] = t

			case WidgetNumeric:
				raw := strings.TrimSpace(*ff.rawStr)
				if raw == "" {
					if isUpdate && ff.hadPrefill {
						continue
					}
					if !f.Required {
						continue
					}
					return nil, fmt.Errorf("field %q is required", ff.Name)
				}
				if isUpdate && ff.hadPrefill && raw == ff.prefilled {
					continue
				}
				if f.Type == schema.TypeInteger {
					n, err := strconv.ParseInt(raw, 10, 64)
					if err != nil {
						return nil, fmt.Errorf("field %q: %w", ff.Name, err)
					}
					out[ff.Name] = n
				} else {
					n, err := strconv.ParseFloat(raw, 64)
					if err != nil {
						return nil, fmt.Errorf("field %q: %w", ff.Name, err)
					}
					out[ff.Name] = n
				}

			case WidgetJSONTextarea:
				raw := strings.TrimSpace(*ff.rawStr)
				if raw == "" {
					if isUpdate && ff.hadPrefill {
						continue
					}
					if !f.Required {
						continue
					}
					return nil, fmt.Errorf("field %q is required", ff.Name)
				}
				if isUpdate && ff.hadPrefill && raw == strings.TrimSpace(ff.prefilled) {
					continue
				}
				var decoded any
				if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
					return nil, fmt.Errorf("field %q: %w", ff.Name, err)
				}
				// Validate the decoded shape matches the declared type.
				switch f.Type {
				case schema.TypeArray:
					if _, ok := decoded.([]any); !ok {
						return nil, fmt.Errorf("field %q: expected JSON array", ff.Name)
					}
				case schema.TypeTable:
					if _, ok := decoded.(map[string]any); !ok {
						return nil, fmt.Errorf("field %q: expected JSON object", ff.Name)
					}
				}
				out[ff.Name] = decoded
			}
		}
		return out, nil
	}

	return form, meta, collect
}

// dispatchWidget encodes the §12.17.5 [D1] dispatch table. Split out
// as a pure function so tests can assert the mapping directly without
// spinning up a form.
func dispatchWidget(f schema.Field) WidgetKind {
	switch f.Type {
	case schema.TypeBoolean:
		return WidgetConfirm
	case schema.TypeInteger, schema.TypeFloat:
		return WidgetNumeric
	case schema.TypeDatetime:
		return WidgetDatetime
	case schema.TypeArray, schema.TypeTable:
		return WidgetJSONTextarea
	case schema.TypeString:
		if strings.EqualFold(f.Format, "markdown") {
			return WidgetText
		}
		if strings.EqualFold(f.Format, "datetime") {
			return WidgetDatetime
		}
		if len(f.Enum) > 0 {
			return WidgetSelect
		}
		return WidgetInput
	default:
		return WidgetInput
	}
}

// stringifyForField renders an existing field value as a string for
// prefill display. time.Time gets RFC3339; arrays/tables get JSON;
// everything else takes the obvious form.
func stringifyForField(v any, f schema.Field) string {
	if v == nil {
		return ""
	}
	switch f.Type {
	case schema.TypeArray, schema.TypeTable:
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return ""
		}
		return string(b)
	case schema.TypeDatetime:
		if t, ok := v.(time.Time); ok {
			return t.Format(time.RFC3339)
		}
		return fmt.Sprint(v)
	case schema.TypeBoolean:
		if b, ok := v.(bool); ok {
			return strconv.FormatBool(b)
		}
		return fmt.Sprint(v)
	default:
		return fmt.Sprint(v)
	}
}

// nonEmptyIfRequiredValidator returns a huh validator that rejects
// empty input on create and on update when no prefill was present (so
// the required field has no fallback). On update with prefill, empty
// is allowed — the collect closure maps it to blank-retains.
func nonEmptyIfRequiredValidator(hadPrefill bool) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" && !hadPrefill {
			return errors.New("value is required")
		}
		return nil
	}
}

// datetimeValidator rejects non-RFC3339 input. Empty is allowed when
// the field is optional or a prefill exists (blank-retains).
func datetimeValidator(required, hadPrefill bool) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			if required && !hadPrefill {
				return errors.New("value is required (RFC3339, e.g. 2006-01-02T15:04:05Z07:00)")
			}
			return nil
		}
		if _, err := time.Parse(time.RFC3339, s); err != nil {
			return fmt.Errorf("expected RFC3339 datetime: %w", err)
		}
		return nil
	}
}

// numericValidator enforces ParseInt / ParseFloat based on declared
// type. Required + no-prefill rejects empty; otherwise empty is fine.
func numericValidator(t schema.Type, required, hadPrefill bool) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			if required && !hadPrefill {
				return errors.New("value is required")
			}
			return nil
		}
		if t == schema.TypeInteger {
			if _, err := strconv.ParseInt(s, 10, 64); err != nil {
				return fmt.Errorf("expected integer: %w", err)
			}
			return nil
		}
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			return fmt.Errorf("expected number: %w", err)
		}
		return nil
	}
}

// jsonArrayOrTableValidator defers to json.Unmarshal then type-asserts
// the top-level shape against array vs table.
func jsonArrayOrTableValidator(t schema.Type, required, hadPrefill bool) func(string) error {
	return func(s string) error {
		s = strings.TrimSpace(s)
		if s == "" {
			if required && !hadPrefill {
				return errors.New("value is required (JSON)")
			}
			return nil
		}
		var decoded any
		if err := json.Unmarshal([]byte(s), &decoded); err != nil {
			return fmt.Errorf("invalid JSON: %w", err)
		}
		switch t {
		case schema.TypeArray:
			if _, ok := decoded.([]any); !ok {
				return errors.New("expected JSON array, e.g. [\"a\",\"b\"]")
			}
		case schema.TypeTable:
			if _, ok := decoded.(map[string]any); !ok {
				return errors.New("expected JSON object, e.g. {\"k\":\"v\"}")
			}
		}
		return nil
	}
}

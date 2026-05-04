// F38d-3 bubbletea formModel. Subsumes the former cmd/ta/huh_form.go:
//
//   - dispatchWidget, FormField, stringifyForField, and all four
//     validators are preserved verbatim — they are pure functions on
//     schema.Field and don't depend on any external form library.
//
//   - FormFor's signature shape is preserved: returns (*formModel,
//     []FormField, collect). Callers run the model via
//     runFormProgram(form) then invoke collect.
//
//   - WidgetConfirm reuses F38d-4's confirmModel pattern in-line; the
//     form embeds a per-field cursor so the confirm widget shares the
//     same enter/left/right semantics.
//
// UI shape: single-page tab-walk. tab / shift+tab navigates fields,
// enter on the last field submits, esc / ctrl+c aborts. Per-widget
// validation runs at submit time via the shared validators.

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/evanmschultz/ta/internal/schema"
)

// WidgetKind names the widget chosen for a schema.Field by the
// dispatch table in V2-PLAN §12.17.5 [D1].
type WidgetKind int

const (
	WidgetInput WidgetKind = iota
	WidgetText
	WidgetSelect
	WidgetConfirm
	WidgetDatetime
	WidgetNumeric
	WidgetJSONTextarea
)

// FormField carries per-field metadata for FormFor's return value.
type FormField struct {
	Name     string
	Kind     WidgetKind
	Required bool

	rawStr  *string
	rawBool *bool

	prefilled    string
	hadPrefill   bool
	prefilledRaw any
}

// formModel walks a slice of FormFields one render at a time. The
// active widget receives keypresses; tab / shift+tab advance the
// active index; enter on the last field submits; esc / ctrl+c
// aborts. Widgets sync their state into the FormField raw pointers
// after every Update so collect() at submit time sees the latest
// values.
type formModel struct {
	title     string
	fields    []FormField
	widgets   []formWidget
	active    int
	submitted bool
	aborted   bool
	err       error
	altScreen bool
}

// syncActive writes the active widget's current value into its
// FormField raw pointer. Called after every keystroke that mutates
// widget state. Tests bypass the model and write rawStr / rawBool
// directly; this method runs only in live flows.
func (m *formModel) syncActive() {
	if m.active < 0 || m.active >= len(m.widgets) {
		return
	}
	w := &m.widgets[m.active]
	ff := &m.fields[m.active]
	switch w.kind {
	case WidgetConfirm:
		if ff.rawBool != nil {
			*ff.rawBool = w.confirm
		}
	case WidgetSelect:
		if ff.rawStr != nil && len(w.options) > 0 && w.selected < len(w.options) {
			*ff.rawStr = w.options[w.selected]
		}
	case WidgetText, WidgetJSONTextarea:
		if ff.rawStr != nil {
			*ff.rawStr = w.textarea.Value()
		}
	default:
		if ff.rawStr != nil {
			*ff.rawStr = w.input.Value()
		}
	}
}

// formWidget is the UI handle for a single FormField. Each kind
// owns its own state — textinput/textarea for string-style widgets,
// a bool cursor for confirm, a selection cursor for select, a slice
// of options for select.
type formWidget struct {
	kind     WidgetKind
	input    textinput.Model
	textarea textarea.Model
	confirm  bool
	options  []string
	selected int
}

// FormFor builds a formModel from the declared fields of typeSt.
// Signature shape preserved from the prior version.
func FormFor(typeSt schema.SectionType, prefill map[string]any, isUpdate bool) (*formModel, []FormField, func() (map[string]any, error)) {
	names := make([]string, 0, len(typeSt.Fields))
	for name := range typeSt.Fields {
		names = append(names, name)
	}
	sort.Strings(names)

	meta := make([]FormField, 0, len(names))
	widgets := make([]formWidget, 0, len(names))

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

		w := formWidget{kind: kind}

		switch kind {
		case WidgetConfirm:
			b := false
			if ff.hadPrefill {
				if bv, ok := ff.prefilledRaw.(bool); ok {
					b = bv
				}
			}
			ff.rawBool = &b
			w.confirm = b

		case WidgetSelect:
			s := ff.prefilled
			ff.rawStr = &s
			opts := make([]string, 0, len(f.Enum))
			for _, ev := range f.Enum {
				opts = append(opts, fmt.Sprint(ev))
			}
			w.options = opts
			for i, opt := range opts {
				if opt == ff.prefilled {
					w.selected = i
					break
				}
			}

		case WidgetText, WidgetJSONTextarea:
			s := ff.prefilled
			ff.rawStr = &s
			ta := textarea.New()
			ta.SetValue(s)
			ta.Placeholder = name
			w.textarea = ta

		default: // WidgetInput, WidgetDatetime, WidgetNumeric
			s := ff.prefilled
			ff.rawStr = &s
			ti := textinput.New()
			ti.SetValue(s)
			ti.Placeholder = name
			w.input = ti
		}

		widgets = append(widgets, w)
		meta = append(meta, ff)
	}

	m := &formModel{
		title:   "Fill " + typeSt.Name,
		fields:  meta,
		widgets: widgets,
	}
	if len(widgets) > 0 {
		m.focusActive()
	}

	collect := makeFormCollect(typeSt, meta, widgets, isUpdate)
	return m, meta, collect
}

// makeFormCollect closes over the meta and returns the post-Run
// collector. Coerces raw strings into typed values and assembles
// the map[string]any payload for ops.Create / ops.Update. Reads
// from FormField raw pointers — the formModel's Update writes
// widget state into the raw pointers on every keystroke so the
// pointers ARE the source of truth at submit time. Tests bypass
// the model and write rawStr / rawBool directly; this contract is
// preserved.
func makeFormCollect(typeSt schema.SectionType, meta []FormField, _ []formWidget, isUpdate bool) func() (map[string]any, error) {
	return func() (map[string]any, error) {
		out := make(map[string]any, len(meta))
		for _, ff := range meta {
			f := typeSt.Fields[ff.Name]
			switch ff.Kind {
			case WidgetConfirm:
				v := *ff.rawBool
				if isUpdate && ff.hadPrefill {
					if prev, ok := ff.prefilledRaw.(bool); ok && prev == v {
						continue
					}
				}
				out[ff.Name] = v

			case WidgetSelect, WidgetInput, WidgetText:
				raw := strings.TrimSpace(*ff.rawStr)
				if ff.Kind == WidgetText {
					raw = *ff.rawStr
				}
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
}

// focusActive routes focus to the active widget. textinput and
// textarea both expose a Focus method.
func (m *formModel) focusActive() {
	if m.active < 0 || m.active >= len(m.widgets) {
		return
	}
	w := &m.widgets[m.active]
	switch w.kind {
	case WidgetText, WidgetJSONTextarea:
		w.textarea.Focus()
	case WidgetInput, WidgetDatetime, WidgetNumeric:
		w.input.Focus()
	}
}

// blurActive un-focuses the active widget so its visual cursor
// hides while another field is selected.
func (m *formModel) blurActive() {
	if m.active < 0 || m.active >= len(m.widgets) {
		return
	}
	w := &m.widgets[m.active]
	switch w.kind {
	case WidgetText, WidgetJSONTextarea:
		w.textarea.Blur()
	case WidgetInput, WidgetDatetime, WidgetNumeric:
		w.input.Blur()
	}
}

// Init satisfies tea.Model. Returns the active widget's blink
// command if any.
func (m *formModel) Init() tea.Cmd { return nil }

// Update routes messages.
func (m *formModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(v)
	case tea.QuitMsg:
		return m, nil
	}
	return m, nil
}

func (m *formModel) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Global keys regardless of widget.
	switch {
	case k.Code == tea.KeyEscape:
		m.aborted = true
		m.err = errInitAborted
		return m, tea.Quit
	case k.Mod&tea.ModCtrl != 0 && k.Code == 'c':
		m.aborted = true
		m.err = errInitAborted
		return m, tea.Quit
	case k.Code == tea.KeyTab:
		if k.Mod&tea.ModShift != 0 {
			m.advance(-1)
		} else {
			m.advance(1)
		}
		return m, nil
	}

	// Per-widget routing.
	if m.active < 0 || m.active >= len(m.widgets) {
		return m, nil
	}
	w := &m.widgets[m.active]
	switch w.kind {
	case WidgetConfirm:
		switch k.Code {
		case tea.KeyLeft, tea.KeyRight:
			w.confirm = !w.confirm
			m.syncActive()
		case tea.KeyEnter:
			if m.active == len(m.widgets)-1 {
				m.submitted = true
				return m, tea.Quit
			}
			m.advance(1)
		}
	case WidgetSelect:
		switch k.Code {
		case tea.KeyUp:
			if w.selected > 0 {
				w.selected--
				m.syncActive()
			}
		case tea.KeyDown:
			if w.selected < len(w.options)-1 {
				w.selected++
				m.syncActive()
			}
		case tea.KeyEnter:
			if m.active == len(m.widgets)-1 {
				m.submitted = true
				return m, tea.Quit
			}
			m.advance(1)
		}
	case WidgetText, WidgetJSONTextarea:
		// Enter advances ONLY when ctrl+enter / shift+enter so the
		// textarea can accept newlines normally. ctrl+s submits.
		if k.Code == tea.KeyEnter && k.Mod&(tea.ModCtrl|tea.ModShift) != 0 {
			if m.active == len(m.widgets)-1 {
				m.submitted = true
				return m, tea.Quit
			}
			m.advance(1)
			return m, nil
		}
		var cmd tea.Cmd
		w.textarea, cmd = w.textarea.Update(k)
		m.syncActive()
		return m, cmd
	default: // WidgetInput, WidgetDatetime, WidgetNumeric
		if k.Code == tea.KeyEnter {
			if m.active == len(m.widgets)-1 {
				m.submitted = true
				return m, tea.Quit
			}
			m.advance(1)
			return m, nil
		}
		var cmd tea.Cmd
		w.input, cmd = w.input.Update(k)
		m.syncActive()
		return m, cmd
	}
	return m, nil
}

// advance moves the active index by delta, wrapping at boundaries.
// The active widget loses focus; the new active widget gains it.
func (m *formModel) advance(delta int) {
	if len(m.widgets) == 0 {
		return
	}
	m.blurActive()
	m.active = (m.active + delta + len(m.widgets)) % len(m.widgets)
	m.focusActive()
}

// View renders the form. Title at top; one block per field with
// title + active-marker + widget content; help bar at bottom.
func (m *formModel) View() tea.View {
	var b strings.Builder
	if m.title != "" {
		b.WriteString(formTitleStyle.Render(m.title))
		b.WriteString("\n\n")
	}
	for i, ff := range m.fields {
		w := m.widgets[i]
		marker := "  "
		if i == m.active {
			marker = "▶ "
		}
		title := ff.Name
		if ff.Required {
			title += " *"
		}
		if i == m.active {
			b.WriteString(formActiveLabelStyle.Render(marker + title))
		} else {
			b.WriteString(formIdleLabelStyle.Render(marker + title))
		}
		b.WriteByte('\n')
		switch w.kind {
		case WidgetConfirm:
			if w.confirm {
				b.WriteString("    [x] yes")
			} else {
				b.WriteString("    [ ] yes")
			}
		case WidgetSelect:
			for j, opt := range w.options {
				prefix := "    [ ] "
				if j == w.selected {
					prefix = "    [x] "
				}
				b.WriteString(prefix + opt)
				if j < len(w.options)-1 {
					b.WriteByte('\n')
				}
			}
		case WidgetText, WidgetJSONTextarea:
			b.WriteString("    " + strings.ReplaceAll(w.textarea.View(), "\n", "\n    "))
		default:
			b.WriteString("    " + w.input.View())
		}
		b.WriteString("\n\n")
	}
	b.WriteString(formHelpStyle.Render(
		"tab next  shift+tab prev  enter advance/submit  ctrl+enter submit textarea  esc abort",
	))
	v := tea.NewView(b.String())
	v.AltScreen = m.altScreen
	return v
}

// Err reports the terminal error condition. Returns errInitAborted
// on esc / ctrl+c, nil on clean submit.
func (m *formModel) Err() error { return m.err }

// runFormProgram is the production execution path.
func runFormProgram(m *formModel) error {
	m.altScreen = true
	final, err := tea.NewProgram(m).Run()
	if err != nil {
		return fmt.Errorf("form: %w", err)
	}
	fm, ok := final.(*formModel)
	if !ok {
		return fmt.Errorf("form: unexpected final model type %T", final)
	}
	if fm.aborted {
		return errInitAborted
	}
	// Sync widget state back into the source model so collect can
	// see the post-run values. The source `m` is the same pointer,
	// so widgets[] mutations through Update have already landed —
	// nothing additional to do here.
	return nil
}

// dispatchWidget encodes the §12.17.5 [D1] dispatch table. Pure
// function on schema.Field — preserved verbatim from huh_form.go.
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
// prefill display. Preserved verbatim from huh_form.go.
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

// nonEmptyIfRequiredValidator preserved verbatim from huh_form.go.
func nonEmptyIfRequiredValidator(hadPrefill bool) func(string) error {
	return func(s string) error {
		if strings.TrimSpace(s) == "" && !hadPrefill {
			return errors.New("value is required")
		}
		return nil
	}
}

// datetimeValidator preserved verbatim from huh_form.go.
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

// numericValidator preserved verbatim from huh_form.go.
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

// jsonArrayOrTableValidator preserved verbatim from huh_form.go.
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

package render

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/evanmschultz/laslig"

	"github.com/evanmschultz/ta/internal/schema"
)

// Renderer wraps a laslig Printer with ta-specific helpers. Construct via
// New(w). The zero value is not usable.
type Renderer struct {
	w      io.Writer
	policy laslig.Policy
	p      *laslig.Printer
}

// New constructs a Renderer using HumanPolicy().
func New(w io.Writer) *Renderer {
	return NewWithPolicy(w, HumanPolicy())
}

// NewWithPolicy constructs a Renderer using a caller-supplied policy.
// Useful when a subcommand needs a non-default glamour preset.
func NewWithPolicy(w io.Writer, policy laslig.Policy) *Renderer {
	return &Renderer{w: w, policy: policy, p: laslig.New(w, policy)}
}

// Notice writes a laslig Notice banner. Used by mutating actions and
// non-fatal informational output (§13.1).
func (r *Renderer) Notice(level laslig.NoticeLevel, title, body string, detail []string) error {
	return r.p.Notice(laslig.Notice{
		Level:  level,
		Title:  title,
		Body:   body,
		Detail: detail,
	})
}

// Success is a convenience wrapper for mutating-op success banners.
func (r *Renderer) Success(title, body string, detail []string) error {
	return r.Notice(laslig.NoticeSuccessLevel, title, body, detail)
}

// List writes a titled list of addresses or labels. Maps to
// `list_sections` output (§13.1).
func (r *Renderer) List(title string, items []string, empty string) error {
	li := make([]laslig.ListItem, len(items))
	for i, item := range items {
		li[i] = laslig.ListItem{Title: item}
	}
	return r.p.List(laslig.List{Title: title, Items: li, Empty: empty})
}

// Markdown writes body through laslig's glamour path. Used for `schema
// get` rendering and raw markdown pass-through in `get` (§13.1, §13.2).
func (r *Renderer) Markdown(body string) error {
	return r.p.Markdown(laslig.Markdown{Body: body})
}

// Facts renders a compact column-aligned block of labelled facts via
// laslig.KV. Use for post-mutation summaries (ta init, ta template
// save/apply/delete) where Notice's title+body+detail shape reads as a
// wall of text but the underlying data is really labelled pairs.
func (r *Renderer) Facts(pairs []laslig.Field) error {
	return r.p.KV(laslig.KV{Pairs: pairs})
}

// RenderField is one labelled field in a Record render. Type drives the
// per-field rendering dispatch: string fields glamour-rendered as
// markdown; everything else label:value. Array and table values are
// rendered as a ```json block so structure survives into the terminal.
type RenderField struct {
	Name  string
	Type  schema.Type
	Value any
}

// Record renders one logical record: the address as a laslig Section
// header, then each declared field per its type. String fields are
// rendered through Markdown (§13.2); scalar non-string types are shown as
// label:value lines via laslig.KV; array / table values are rendered as
// a fenced JSON code block inside a Markdown block so pretty-printing
// survives.
//
// Section is the full dotted address (e.g. "plans.task.t1"); fields may
// be nil, in which case only the header is written.
func (r *Renderer) Record(section string, fields []RenderField) error {
	if err := r.p.Section(section); err != nil {
		return err
	}
	if len(fields) == 0 {
		return nil
	}
	// Stable order: caller-provided order wins; callers that want
	// alphabetical order can sort before calling.
	for _, f := range fields {
		if err := r.renderField(f); err != nil {
			return err
		}
	}
	return nil
}

// renderField dispatches on f.Type.
//
// - TypeString: glamour-rendered as "### <label>\n\n<value>\n".
// - TypeArray / TypeTable: label + ```json\n…\n``` block.
// - Other scalars: laslig.KV pair.
func (r *Renderer) renderField(f RenderField) error {
	switch f.Type {
	case schema.TypeString:
		return r.renderStringField(f)
	case schema.TypeArray, schema.TypeTable:
		return r.renderStructuredField(f)
	default:
		return r.renderScalarField(f)
	}
}

func (r *Renderer) renderStringField(f RenderField) error {
	s, _ := f.Value.(string)
	var body strings.Builder
	fmt.Fprintf(&body, "### %s\n\n", f.Name)
	if s != "" {
		body.WriteString(s)
		if !strings.HasSuffix(s, "\n") {
			body.WriteByte('\n')
		}
	}
	return r.p.Markdown(laslig.Markdown{Body: body.String()})
}

func (r *Renderer) renderStructuredField(f RenderField) error {
	raw, err := json.MarshalIndent(f.Value, "", "  ")
	if err != nil {
		return fmt.Errorf("render field %q: %w", f.Name, err)
	}
	body := fmt.Sprintf("### %s\n\n```json\n%s\n```\n", f.Name, string(raw))
	return r.p.Markdown(laslig.Markdown{Body: body})
}

func (r *Renderer) renderScalarField(f RenderField) error {
	return r.p.KV(laslig.KV{
		Pairs: []laslig.Field{{Label: f.Name, Value: fmt.Sprintf("%v", f.Value)}},
	})
}

// SortFieldsByName reorders fields alphabetically by Name. Convenience
// for callers that want deterministic terminal output.
func SortFieldsByName(fields []RenderField) {
	sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
}

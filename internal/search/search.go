package search

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os"
	"regexp"
	"sort"
	"strings"

	tomlv2 "github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/md"
	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// Query is the input to Run. Only Path is strictly required; the other
// fields narrow the search.
//
// Semantics (V2-PLAN §3.7 / §7):
//   - Scope is either empty (whole project), "<db>", "<db>.<instance>"
//     (multi-instance dbs only), "<db>.<type>", or "<db>.<type>.<id-prefix>".
//     An "-*" suffix on the id-prefix is tolerated as a no-op.
//   - Match pairs AND-combine; every pair must match exactly. String/enum
//     compare via Go ==; numbers compare numerically (int vs float
//     promoted). Array/table match → ErrUnscalarMatch.
//   - Query is applied AFTER Match on records that passed Match. When
//     Field == "" the regex is matched against every string-typed field;
//     when Field is set, only that one declared string field is scanned.
//     A hit is any FindIndex match.
//   - Limit caps the returned hit count; 0 means "no cap". When Limit > 0
//     and All is false, Run early-exits after each file's results are
//     appended once len(out) >= Limit — O(until first cap-cross) rather
//     than O(all records). Ignored when All is true.
//   - All == true returns every hit (ignores Limit). Adapters (CLI cobra
//     mutex, MCP handler guard) enforce the UX-level "pass limit OR all,
//     not both" rule; this type stays permissive at the endpoint layer
//     so library callers see predictable precedence (All wins). See
//     docs/PLAN.md §6a.1 + §3.2 + §3.7 + §12.17.5 [A2.1]+[A2.2].
type Query struct {
	Path  string
	Scope string
	Match map[string]any
	Query *regexp.Regexp
	Field string
	Limit int
	All   bool
}

// Result is one hit. Section is the full dotted address; Bytes is the
// record's on-disk byte range (what `get` would return); Fields is the
// decoded field map for callers that want structured access.
type Result struct {
	Section string
	Bytes   []byte
	Fields  map[string]any
}

// Run executes q and returns hits in source order across files. Files
// are visited in stable lexical order so results are deterministic
// across runs.
func Run(q Query) ([]Result, error) {
	if q.Path == "" {
		return nil, fmt.Errorf("%w: empty path", ErrInvalidScope)
	}
	resolution, err := resolve(q.Path)
	if err != nil {
		return nil, err
	}
	plan, err := parseScope(resolution.Registry, q.Scope)
	if err != nil {
		return nil, err
	}
	// Type-unconstrained scope pre-validation: a Match/Field name that
	// no type in scope declares is a pure typo and must fail loudly,
	// not silently-zero-hit per record (V2-PLAN §1.1 / §12.7
	// Falsification finding #2). The existing per-record silent-skip
	// in searchFile still handles the legitimate "some types declare
	// this, others don't" heterogeneous case — a name declared on at
	// least one type in scope passes this gate.
	if plan.typeName == "" {
		if err := validateScopeNames(resolution.Registry, plan, q); err != nil {
			return nil, err
		}
	}

	resolver := db.NewResolver(q.Path, resolution.Registry)

	var out []Result
	for _, dbName := range plan.dbOrder {
		dbDecl := resolution.Registry.DBs[dbName]
		instances, err := resolver.Instances(dbName)
		if err != nil {
			return nil, err
		}
		for _, inst := range instances {
			if plan.instance != "" && inst.Slug != plan.instance {
				continue
			}
			if _, err := os.Stat(inst.FilePath); err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return nil, fmt.Errorf("stat %s: %w", inst.FilePath, err)
			}
			results, err := searchFile(dbDecl, inst, plan, q)
			if err != nil {
				return nil, err
			}
			out = append(out, results...)
			// Endpoint-enforced cap with file-boundary early-exit per
			// docs/PLAN.md §3.2 / §3.7 amendment. All=true bypasses the
			// cap; Limit<=0 means "no cap" (callers that want the default
			// substitute it at the adapter/ops layer before calling Run).
			if !q.All && q.Limit > 0 && len(out) >= q.Limit {
				return out[:q.Limit], nil
			}
		}
	}
	return out, nil
}

// resolve is a local mirror of ops.ResolveProject so this package
// does not import ops. Post-V2-PLAN §12.11 the resolver reads the
// single project-local .ta/schema.toml directly — no sentinel trick.
func resolve(projectPath string) (config.Resolution, error) {
	return config.Resolve(projectPath)
}

// searchPlan carries the parsed Query.Scope + the list of dbs to visit.
type searchPlan struct {
	dbOrder  []string
	instance string // "" means "any instance"
	typeName string // "" means "any type"
	idPrefix string // "" means "any id"
}

// parseScope validates Scope against the registry and returns the
// traversal plan. See V2-PLAN §3.7 / §5.5.3.
func parseScope(reg schema.Registry, scope string) (searchPlan, error) {
	if scope == "" {
		names := make([]string, 0, len(reg.DBs))
		for n := range reg.DBs {
			names = append(names, n)
		}
		sort.Strings(names)
		return searchPlan{dbOrder: names}, nil
	}

	parts := strings.Split(scope, ".")
	if parts[0] == "" {
		return searchPlan{}, fmt.Errorf("%w: %q", ErrInvalidScope, scope)
	}
	dbDecl, ok := reg.DBs[parts[0]]
	if !ok {
		return searchPlan{}, fmt.Errorf("%w: unknown db %q", ErrInvalidScope, parts[0])
	}

	plan := searchPlan{dbOrder: []string{dbDecl.Name}}
	switch len(parts) {
	case 1:
		return plan, nil
	case 2:
		// Ambiguous in theory: could be <db>.<instance> (multi) or
		// <db>.<type> (single). Resolve using db shape and presence.
		if dbDecl.Shape == schema.ShapeFile {
			// Single-instance: segment must be a type name.
			if _, ok := dbDecl.Types[parts[1]]; !ok {
				return plan, fmt.Errorf("%w: type %q not declared on db %q",
					ErrInvalidScope, parts[1], dbDecl.Name)
			}
			plan.typeName = parts[1]
			return plan, nil
		}
		// Multi-instance: prefer type match first, else instance.
		if _, ok := dbDecl.Types[parts[1]]; ok {
			plan.typeName = parts[1]
			return plan, nil
		}
		plan.instance = parts[1]
		return plan, nil
	default:
		// 3+ segments:
		//   single-instance: <db>.<type>.<id-prefix>
		//   multi-instance:  <db>.<instance>.<type>(.<id-prefix>)?
		if dbDecl.Shape == schema.ShapeFile {
			if _, ok := dbDecl.Types[parts[1]]; !ok {
				return plan, fmt.Errorf("%w: type %q not declared on db %q",
					ErrInvalidScope, parts[1], dbDecl.Name)
			}
			plan.typeName = parts[1]
			plan.idPrefix = trimGlob(strings.Join(parts[2:], "."))
			return plan, nil
		}
		// multi-instance
		plan.instance = parts[1]
		if _, ok := dbDecl.Types[parts[2]]; !ok {
			return plan, fmt.Errorf("%w: type %q not declared on db %q",
				ErrInvalidScope, parts[2], dbDecl.Name)
		}
		plan.typeName = parts[2]
		if len(parts) > 3 {
			plan.idPrefix = trimGlob(strings.Join(parts[3:], "."))
		}
		return plan, nil
	}
}

// trimGlob strips a trailing "-*" or "*" on the id-prefix segment so
// the common "<db>.<type>.reference-*" form from §5.5.3 degrades to a
// plain prefix match on "reference-".
func trimGlob(s string) string {
	if trimmed, ok := strings.CutSuffix(s, "-*"); ok {
		return trimmed + "-"
	}
	return strings.TrimSuffix(s, "*")
}

// searchFile runs the query against one instance file.
func searchFile(dbDecl schema.DB, inst db.Instance, plan searchPlan, q Query) ([]Result, error) {
	buf, err := os.ReadFile(inst.FilePath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", inst.FilePath, err)
	}
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return nil, err
	}

	// When the scope constrains the type, validate Match + Field once
	// against that type so typos fail loudly (§7.1) even against an
	// empty result set. When scope is type-unconstrained, per-type
	// validation happens inline during record iteration.
	if plan.typeName != "" {
		st, ok := dbDecl.Types[plan.typeName]
		if !ok {
			return nil, fmt.Errorf("%w: type %q not declared on db %q",
				ErrInvalidScope, plan.typeName, dbDecl.Name)
		}
		if err := matchFilterErrors(st, q.Match); err != nil {
			return nil, err
		}
		if err := fieldFilterError(st, q.Field); err != nil {
			return nil, err
		}
		// MD body-only layout (§5.3.3) can only serve the "body" field;
		// a declared non-body MD field is a typed-contract lie and must
		// error loudly, not return silent zero-hits. Mirror the get
		// tool's contract (ops/fields.go:extractMDFields) via the
		// shared md.CheckBackableFields helper so the two entry points
		// cannot drift.
		if err := mdLayoutCheck(dbDecl, st, q); err != nil {
			return nil, err
		}
	}

	// List every declared section in the file; we filter further after
	// locating byte ranges because typed-field filtering needs parsed
	// record state the backend doesn't carry.
	addresses, err := backend.List(buf, backendTypeScope(dbDecl, plan.typeName))
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", inst.FilePath, err)
	}

	// Pre-cache the pelletier-decoded TOML root if we'll need it.
	var tomlRoot map[string]any
	if dbDecl.Format == schema.FormatTOML {
		if err := tomlv2.Unmarshal(buf, &tomlRoot); err != nil {
			return nil, fmt.Errorf("decode %s: %w", inst.FilePath, err)
		}
	}

	var hits []Result
	for _, addr := range addresses {
		// For multi-instance TOML the `addr` from List is the
		// backend-relative bracket path (e.g. "build_task.t1"); for
		// single-instance it already carries the db name. For MD the
		// backend returns the full address.
		fullAddr := fullAddress(dbDecl, inst, addr)

		// Type + id-prefix filter. Type already constrained by
		// List scope for TOML; MD scope is empty, so re-check here.
		recordType, recordID := typeAndID(dbDecl, fullAddr)
		if plan.typeName != "" && recordType != plan.typeName {
			continue
		}
		if plan.idPrefix != "" && !strings.HasPrefix(recordID, plan.idPrefix) {
			continue
		}

		sec, ok, err := backend.Find(buf, addr)
		if err != nil {
			return nil, fmt.Errorf("find %s in %s: %w", addr, inst.FilePath, err)
		}
		if !ok {
			continue
		}
		recordBytes := buf[sec.Range[0]:sec.Range[1]]

		typeSt, ok := dbDecl.Types[recordType]
		if !ok {
			// Should not happen under declared scope, but skip defensively.
			continue
		}

		fields, err := decodeFields(dbDecl, typeSt, tomlRoot, buf, sec, addr)
		if err != nil {
			return nil, err
		}

		// Type-unconstrained scope: a record whose type doesn't declare
		// the Match field or the named regex Field is a non-match (not
		// an error). Loud-fail behavior is reserved for narrowed
		// scopes where the typo is unambiguous.
		skip := false
		if plan.typeName == "" {
			// MD body-only layout violation is ALWAYS loud, even under
			// unconstrained scope, because a declared non-body MD field
			// is a typed-contract lie independent of which types happen
			// to fall in scope. Run this check first so it propagates
			// out of the silent-skip gate below.
			if err := mdLayoutCheck(dbDecl, typeSt, q); err != nil {
				return nil, err
			}
			if matchFilterErrors(typeSt, q.Match) != nil {
				skip = true
			}
			if !skip && fieldFilterError(typeSt, q.Field) != nil {
				skip = true
			}
		}
		if skip {
			continue
		}

		if !matchFilter(typeSt, fields, q.Match) {
			continue
		}

		if q.Query != nil {
			matched, err := regexFilter(typeSt, fields, q.Query, q.Field)
			if err != nil {
				return nil, err
			}
			if !matched {
				continue
			}
		}

		hits = append(hits, Result{
			Section: fullAddr,
			Bytes:   append([]byte(nil), recordBytes...),
			Fields:  fields,
		})
	}
	return hits, nil
}

// buildBackend mirrors ops.buildBackend — duplicated here to keep
// internal/search independent of internal/ops.
func buildBackend(dbDecl schema.DB) (record.Backend, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		types := make([]record.DeclaredType, 0, len(dbDecl.Types))
		for typeName := range dbDecl.Types {
			prefix := tomlDeclaredName(dbDecl, typeName)
			types = append(types, record.DeclaredType{Name: prefix})
		}
		return toml.NewBackend(types), nil
	case schema.FormatMD:
		types := make([]record.DeclaredType, 0, len(dbDecl.Types))
		for typeName, t := range dbDecl.Types {
			types = append(types, record.DeclaredType{
				Name:    typeName,
				Heading: t.Heading,
			})
		}
		b, err := md.NewBackend(types)
		if err != nil {
			return nil, fmt.Errorf("build MD backend for db %q: %w", dbDecl.Name, err)
		}
		return b, nil
	default:
		return nil, fmt.Errorf("%w: db %q format=%q",
			ErrUnsupportedFormat, dbDecl.Name, dbDecl.Format)
	}
}

func tomlDeclaredName(dbDecl schema.DB, typeName string) string {
	if dbDecl.Shape == schema.ShapeFile {
		return dbDecl.Name + "." + typeName
	}
	return typeName
}

// backendTypeScope returns the scope string to hand backend.List to
// narrow enumeration to one type. "" means "every declared section in
// the file" — MD backends do not currently honour a scope filter so we
// always return "" for MD and post-filter.
func backendTypeScope(dbDecl schema.DB, typeName string) string {
	if typeName == "" {
		return ""
	}
	if dbDecl.Format == schema.FormatMD {
		return ""
	}
	return tomlDeclaredName(dbDecl, typeName)
}

// fullAddress prepends the db (+ instance) segments to a backend-relative
// record address so callers see the same address they would pass to
// `get`. For TOML single-instance the backend already returns the
// db-qualified path (e.g. "plans.task.t1"); for TOML multi-instance the
// backend returns the bare "<type>.<id>" and we prepend "<db>.<instance>.".
// For MD the backend returns "<type>.<chain...>" regardless of shape, so
// we always prepend "<db>." (single-instance) or "<db>.<instance>."
// (multi-instance).
func fullAddress(dbDecl schema.DB, inst db.Instance, backendAddr string) string {
	switch dbDecl.Format {
	case schema.FormatTOML:
		if dbDecl.Shape == schema.ShapeFile {
			return backendAddr
		}
		return dbDecl.Name + "." + inst.Slug + "." + backendAddr
	case schema.FormatMD:
		if dbDecl.Shape == schema.ShapeFile {
			return dbDecl.Name + "." + backendAddr
		}
		return dbDecl.Name + "." + inst.Slug + "." + backendAddr
	default:
		return backendAddr
	}
}

// typeAndID splits a full address into (type, id-path).
func typeAndID(dbDecl schema.DB, fullAddr string) (string, string) {
	parts := strings.Split(fullAddr, ".")
	switch dbDecl.Shape {
	case schema.ShapeFile:
		if len(parts) < 2 {
			return "", ""
		}
		return parts[1], strings.Join(parts[2:], ".")
	default:
		if len(parts) < 3 {
			return "", ""
		}
		return parts[2], strings.Join(parts[3:], ".")
	}
}

// decodeFields returns the parsed field map for one located record.
// For TOML: walk the already-decoded root via the record's bracket path.
// For MD body-only (§5.3.3): the "body" field is everything after the
// heading line.
func decodeFields(dbDecl schema.DB, typeSt schema.SectionType, tomlRoot map[string]any, buf []byte, sec record.Section, backendAddr string) (map[string]any, error) {
	switch dbDecl.Format {
	case schema.FormatTOML:
		return walkTOMLPath(tomlRoot, backendAddr)
	case schema.FormatMD:
		raw := buf[sec.Range[0]:sec.Range[1]]
		body := stripHeadingLine(raw)
		out := map[string]any{}
		// Only body is backed by the MVP layout; other declared fields
		// (if any) are absent — they stay absent in the map.
		if _, ok := typeSt.Fields["body"]; ok {
			out["body"] = string(body)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("%w: db %q format=%q",
			ErrUnsupportedFormat, dbDecl.Name, dbDecl.Format)
	}
}

// walkTOMLPath descends the pelletier-decoded root by the dotted segs of
// backendAddr and returns the leaf table's fields. A missing segment
// returns an empty map (the record was listed but somehow has no
// decoded state — treat as empty rather than erroring; callers can still
// filter).
func walkTOMLPath(root map[string]any, backendAddr string) (map[string]any, error) {
	cursor := root
	for seg := range strings.SplitSeq(backendAddr, ".") {
		next, ok := cursor[seg]
		if !ok {
			return map[string]any{}, nil
		}
		nextMap, ok := next.(map[string]any)
		if !ok {
			return map[string]any{}, nil
		}
		cursor = nextMap
	}
	// Shallow clone so callers cannot mutate our decoded tree.
	out := make(map[string]any, len(cursor))
	maps.Copy(out, cursor)
	return out, nil
}

// stripHeadingLine returns raw with the first line (heading) and at
// most one directly-following blank line removed. Mirrors the MVP body-
// only layout in internal/ops/fields.go.
func stripHeadingLine(raw []byte) []byte {
	_, rest, ok := bytes.Cut(raw, []byte{'\n'})
	if !ok {
		return nil
	}
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	}
	return rest
}

// validateScopeNames runs at Run entry for type-unconstrained scope
// and errors when a Match/Field name is declared on zero types in
// scope. A name declared on at least one type in scope passes — the
// existing per-record silent-skip in searchFile correctly handles the
// heterogeneous case where some types declare the field and others
// don't. This closes the "pure typo under bare <db> scope returns
// silent zero-hits" hole (V2-PLAN §12.7 Falsification finding #2).
func validateScopeNames(reg schema.Registry, plan searchPlan, q Query) error {
	names := make([]string, 0, len(q.Match)+1)
	for name := range q.Match {
		names = append(names, name)
	}
	if q.Field != "" {
		names = append(names, q.Field)
	}
	if len(names) == 0 {
		return nil
	}
	for _, name := range names {
		found := false
		for _, dbName := range plan.dbOrder {
			dbDecl := reg.DBs[dbName]
			for _, t := range dbDecl.Types {
				if _, ok := t.Fields[name]; ok {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: %q not declared on any type in scope",
				ErrUnknownField, name)
		}
	}
	return nil
}

// mdLayoutCheck rejects Match keys and the named regex Field when the
// db is MD-format and the name is a declared non-body field. Under the
// body-only layout (§5.3.3) only "body" is readable, so a declared
// non-body field is a typed-contract lie: the schema claims it exists
// but the layout has no on-disk representation. Fails loudly to match
// the contract ops/fields.go:extractMDFields enforces on the `get`
// path — both entry points route through md.CheckBackableFields so
// they cannot drift (V2-PLAN §12.7 Falsification finding #30).
//
// Names not declared on typeSt are left to matchFilterErrors /
// fieldFilterError (the unknown-field surface, scope-dependent).
func mdLayoutCheck(dbDecl schema.DB, typeSt schema.SectionType, q Query) error {
	if dbDecl.Format != schema.FormatMD {
		return nil
	}
	names := make([]string, 0, len(q.Match)+1)
	for name := range q.Match {
		if _, declared := typeSt.Fields[name]; declared {
			names = append(names, name)
		}
	}
	if q.Field != "" {
		if _, declared := typeSt.Fields[q.Field]; declared {
			names = append(names, q.Field)
		}
	}
	if err := md.CheckBackableFields(names); err != nil {
		return fmt.Errorf("%w: %s", ErrUnknownField, err.Error())
	}
	return nil
}

// fieldFilterError validates Query.Field against a declared type. Empty
// field is always legal (means "scan every string field"). A non-empty
// field must be declared and typed string.
func fieldFilterError(typeSt schema.SectionType, field string) error {
	if field == "" {
		return nil
	}
	f, ok := typeSt.Fields[field]
	if !ok {
		return fmt.Errorf("%w: regex field %q not declared on %q",
			ErrUnknownField, field, typeSt.Name)
	}
	if f.Type != schema.TypeString {
		return fmt.Errorf("%w: regex field %q is %s (must be string)",
			ErrUnknownField, field, f.Type)
	}
	return nil
}

// matchFilterErrors returns the first structural error (unknown field,
// non-scalar match) so the caller can fail loudly. It never silently
// drops a match pair.
func matchFilterErrors(typeSt schema.SectionType, match map[string]any) error {
	for name := range match {
		f, ok := typeSt.Fields[name]
		if !ok {
			return fmt.Errorf("%w: %q not declared on %q", ErrUnknownField, name, typeSt.Name)
		}
		switch f.Type {
		case schema.TypeArray, schema.TypeTable:
			return fmt.Errorf("%w: field %q is %s", ErrUnscalarMatch, name, f.Type)
		}
	}
	return nil
}

// matchFilter evaluates the Match pairs against the decoded record. It
// assumes matchFilterErrors has already run to vet each pair.
func matchFilter(typeSt schema.SectionType, fields map[string]any, match map[string]any) bool {
	for name, want := range match {
		f := typeSt.Fields[name]
		got, present := fields[name]
		if !present {
			return false
		}
		if !scalarEqual(f.Type, got, want) {
			return false
		}
	}
	return true
}

// scalarEqual compares a decoded field value against the want value per
// schema type. The want value is whatever the caller passed in — for
// MCP JSON that's always numeric as float64 or json.Number.
func scalarEqual(t schema.Type, got, want any) bool {
	switch t {
	case schema.TypeInteger, schema.TypeFloat:
		return numericEqual(got, want)
	case schema.TypeBoolean:
		gb, gok := toBool(got)
		wb, wok := toBool(want)
		if !gok || !wok {
			return false
		}
		return gb == wb
	default:
		// string, enum, datetime — compare via string fmt.
		return fmt.Sprintf("%v", got) == fmt.Sprintf("%v", want)
	}
}

func numericEqual(a, b any) bool {
	af, aok := toFloat(a)
	bf, bok := toFloat(b)
	if !aok || !bok {
		return false
	}
	return af == bf
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int32:
		return float64(x), true
	case int64:
		return float64(x), true
	case float32:
		return float64(x), true
	case float64:
		return x, true
	case uint:
		return float64(x), true
	case uint32:
		return float64(x), true
	case uint64:
		return float64(x), true
	case string:
		return 0, false
	default:
		return 0, false
	}
}

func toBool(v any) (bool, bool) {
	b, ok := v.(bool)
	return b, ok
}

// regexFilter evaluates q against the record's string fields. When field
// is set, only that named field is scanned; otherwise every declared
// string-typed field is scanned.
func regexFilter(typeSt schema.SectionType, fields map[string]any, re *regexp.Regexp, field string) (bool, error) {
	if field != "" {
		f, ok := typeSt.Fields[field]
		if !ok {
			return false, fmt.Errorf("%w: regex field %q not declared on %q",
				ErrUnknownField, field, typeSt.Name)
		}
		if f.Type != schema.TypeString {
			return false, fmt.Errorf("%w: regex field %q is %s (must be string)",
				ErrUnknownField, field, f.Type)
		}
		return regexOnField(re, fields, field), nil
	}
	for name, f := range typeSt.Fields {
		if f.Type != schema.TypeString {
			continue
		}
		if regexOnField(re, fields, name) {
			return true, nil
		}
	}
	return false, nil
}

func regexOnField(re *regexp.Regexp, fields map[string]any, name string) bool {
	raw, ok := fields[name]
	if !ok {
		return false
	}
	s, ok := raw.(string)
	if !ok {
		s = fmt.Sprintf("%v", raw)
	}
	return re.MatchString(s)
}

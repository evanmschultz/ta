package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	pelletier "github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/schema"
)

// ComputePathsMutation returns the new paths slice for a db after the
// `--paths-append` / `--paths-remove` sugar (PLAN §12.17.9 Phase 9.6) is
// applied to currentPaths. Pure function — no I/O.
//
// At most one of append / remove may be non-empty; passing both is a
// programmer error (caller must enforce the mutex upstream so the user
// gets a clear flag-level diagnostic).
//
// Append semantics: if the entry is already present, the slice is
// returned unchanged (no-op idempotence); otherwise it is appended at
// the end so prior order is preserved.
//
// Remove semantics: if the entry is present, every matching occurrence
// is filtered out; otherwise the slice is returned unchanged. Removing
// a missing entry is a no-op, not an error.
//
// Empty currentPaths + append → single-entry result. Empty append AND
// empty remove returns currentPaths unchanged (the caller is expected
// to skip calling this helper when neither flag is set).
func ComputePathsMutation(currentPaths []string, appendEntry, removeEntry string) ([]string, error) {
	if appendEntry != "" && removeEntry != "" {
		return nil, fmt.Errorf("ops: ComputePathsMutation: append and remove are mutually exclusive")
	}
	if appendEntry == "" && removeEntry == "" {
		// Nothing to do; copy to avoid aliasing the caller's slice.
		out := make([]string, len(currentPaths))
		copy(out, currentPaths)
		return out, nil
	}
	if appendEntry != "" {
		if slices.Contains(currentPaths, appendEntry) {
			// Idempotent: already present, return unchanged copy.
			out := make([]string, len(currentPaths))
			copy(out, currentPaths)
			return out, nil
		}
		out := make([]string, 0, len(currentPaths)+1)
		out = append(out, currentPaths...)
		out = append(out, appendEntry)
		return out, nil
	}
	// removeEntry != "".
	out := make([]string, 0, len(currentPaths))
	for _, p := range currentPaths {
		if p == removeEntry {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// MutateDBPaths is the adapter-shared helper for the `--paths-append`
// / `--paths-remove` CLI flags and the `paths_append` / `paths_remove`
// MCP params (PLAN §12.17.9 Phase 9.6). It loads the current resolved
// db, applies ComputePathsMutation, and routes the result through the
// existing MutateSchema(action="update", kind="db") atomic-rollback
// pipeline so the post-mutation schema is re-validated and rolled back
// on meta-schema violation.
//
// Format and description are carried over from the existing db so the
// caller does not have to re-supply them — the underlying
// applyDBMutation deletes the full meta-field set on update, so any
// missing meta-field would otherwise vanish on write.
//
// Caller must enforce the (append XOR remove) mutex AND the
// no-data-paths-key rule before calling; this helper assumes a clean
// single-mutation request.
func MutateDBPaths(projectPath, dbName, appendEntry, removeEntry string) ([]string, error) {
	resolution, err := resolveFromProjectDir(projectPath)
	if err != nil {
		return nil, fmt.Errorf("resolve schema for %s: %w", projectPath, err)
	}
	dbDecl, ok := resolution.Registry.DBs[dbName]
	if !ok {
		return nil, fmt.Errorf("%w: db %q", ErrUnknownSchemaTarget, dbName)
	}
	newPaths, err := ComputePathsMutation(dbDecl.Paths, appendEntry, removeEntry)
	if err != nil {
		return nil, err
	}
	// Convert to []any so the in-memory shape matches the post-Unmarshal
	// shape every other update-db caller passes (`data["paths"]` =
	// `[]any{...}`). pelletier's Marshal accepts both []string and
	// []any, but the round-trip through schema.LoadBytes' stringSliceVal
	// guard expects []any — keeping the in-memory shape consistent
	// avoids surprises if any future check inspects the map directly.
	pathsAny := make([]any, len(newPaths))
	for i, p := range newPaths {
		pathsAny[i] = p
	}
	// Per F10, format is inferred from path extensions — no `format`
	// key on the data payload.
	data := map[string]any{
		"paths": pathsAny,
	}
	if dbDecl.Description != "" {
		data["description"] = dbDecl.Description
	}
	return MutateSchema(projectPath, "update", "db", dbName, data)
}

// MutateSchema applies action to the project `.ta/schema.toml` located
// at <path>/.ta/schema.toml (creating the dir and file on first use)
// under an atomic-rollback discipline (V2-PLAN §4.6):
//
//  1. Load current bytes (or empty map on first use).
//  2. Apply the mutation to an in-memory map.
//  3. Re-serialize and re-validate via schema.LoadBytes. If validation
//     fails, return ErrMetaSchemaViolation without touching disk.
//  4. On success, atomic-write the new bytes and return the list of
//     resolved schema paths (so callers can surface the cascade sources
//     in their response).
//
// action ∈ {create, update, delete}; kind ∈ {db, type, field, base};
// name is the dotted address per §3.3. For delete actions the caller
// passes data=nil — the handler above enforces the distinction.
func MutateSchema(projectPath, action, kind, name string, data map[string]any) ([]string, error) {
	// Guard: the meta-schema literal is embedded, not user-mutable.
	if name == schema.MetaSchemaPath || strings.HasPrefix(name, schema.MetaSchemaPath+".") {
		return nil, fmt.Errorf("%w: %q", ErrReservedName, name)
	}
	// 1. Pick the write layer: always the project .ta/schema.toml.
	schemaPath := filepath.Join(projectPath, config.SchemaDirName, config.SchemaFileName)

	// 2. Read current bytes (empty map on first use).
	root, err := loadSchemaMap(schemaPath)
	if err != nil {
		return nil, err
	}

	// 3. Apply mutation to the map.
	if err := applyMutation(projectPath, action, kind, name, data, root); err != nil {
		return nil, err
	}

	// 4. Re-serialize.
	newBuf, err := pelletier.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("ops: marshal updated schema: %w", err)
	}
	// 5. Re-validate via schema.LoadBytes. If invalid → rollback (don't write).
	if _, err := schema.LoadBytes(newBuf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMetaSchemaViolation, err)
	}
	// 6. Atomic-write.
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		return nil, fmt.Errorf("ops: mkdir %s: %w", filepath.Dir(schemaPath), err)
	}
	if err := toml.WriteAtomic(schemaPath, newBuf); err != nil {
		return nil, err
	}
	// 7. Invalidate the cache entry for this project so the next read
	// re-resolves the cascade. Catches structural changes (new/removed
	// types, deleted fields) that a bare mtime comparison could miss
	// if the post-write mtime happens to match the pre-write mtime
	// (rare but cheap to guard against). Per V2-PLAN §4.6's "on
	// success, invalidate → re-resolve cascade" rule.
	defaultCache.Invalidate(projectPath)
	// 8. Resolve and return sources for the response. This re-populates
	// the cache with the post-mutation view.
	resolution, err := resolveFromProjectDir(projectPath)
	if err != nil {
		// Unusual: we just wrote a valid schema and cascading re-resolve
		// failed. Surface but do not undo — the written file is valid on
		// its own.
		return nil, fmt.Errorf("post-mutation resolve: %w", err)
	}
	return resolution.Sources, nil
}

func loadSchemaMap(path string) (map[string]any, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("ops: read %s: %w", path, err)
	}
	var root map[string]any
	if err := pelletier.Unmarshal(buf, &root); err != nil {
		return nil, fmt.Errorf("ops: parse %s: %w", path, err)
	}
	if root == nil {
		root = map[string]any{}
	}
	return root, nil
}

// applyMutation is the in-memory editor: no disk I/O apart from the
// existence-scan used by delete(kind=db) and delete(kind=type). The
// caller serializes and re-validates the resulting map; invalid
// post-mutation states never reach disk.
func applyMutation(projectPath, action, kind, name string, data map[string]any, root map[string]any) error {
	switch kind {
	case "db":
		return applyDBMutation(projectPath, action, name, data, root)
	case "type":
		return applyTypeMutation(projectPath, action, name, data, root)
	case "field":
		return applyFieldMutation(action, name, data, root)
	case "base":
		return applyBaseMutation(action, name, data, root)
	default:
		return fmt.Errorf("schema: unknown kind %q (want db|type|field|base)", kind)
	}
}

func applyDBMutation(projectPath, action, name string, data map[string]any, root map[string]any) error {
	if strings.Contains(name, ".") {
		return fmt.Errorf("schema: db name %q must be bare (no dots)", name)
	}
	switch action {
	case "create":
		if _, ok := root[name]; ok {
			return fmt.Errorf("schema: db %q already exists (use action=update)", name)
		}
		entry := cloneMap(data)
		root[name] = entry
		return nil
	case "update":
		existing, ok := root[name]
		if !ok {
			return fmt.Errorf("%w: db %q", ErrUnknownSchemaTarget, name)
		}
		existingMap, _ := existing.(map[string]any)
		if existingMap == nil {
			existingMap = map[string]any{}
		}
		// Replace meta-fields; preserve sub-table record types on update.
		// Per F10 (PLAN §12.17.9), meta-fields are `paths` and
		// `description`; format is inferred from each path's extension.
		// `format` (and the legacy file/directory/collection keys) are
		// rejected at schema-load via the unknown-key path.
		for _, metaKey := range []string{"paths", "description"} {
			delete(existingMap, metaKey)
		}
		maps.Copy(existingMap, data)
		root[name] = existingMap
		return nil
	case "delete":
		if _, ok := root[name]; !ok {
			return fmt.Errorf("%w: db %q", ErrUnknownSchemaTarget, name)
		}
		// §3.3 delete: errors if any data files exist on disk.
		if has, err := dbHasDataOnDisk(projectPath, name, root); err != nil {
			return err
		} else if has {
			return fmt.Errorf("%w: db %q", ErrDBHasData, name)
		}
		delete(root, name)
		return nil
	}
	return fmt.Errorf("schema: unknown action %q", action)
}

func applyTypeMutation(projectPath, action, name string, data map[string]any, root map[string]any) error {
	dbName, typeName, rest := splitTwo(name)
	if dbName == "" || typeName == "" || rest != "" {
		return fmt.Errorf("schema: type name %q must be '<db>.<type>'", name)
	}
	dbAny, ok := root[dbName]
	if !ok {
		return fmt.Errorf("%w: db %q", ErrUnknownSchemaTarget, dbName)
	}
	dbMap, ok := dbAny.(map[string]any)
	if !ok {
		return fmt.Errorf("schema: db %q has non-table entry", dbName)
	}
	switch action {
	case "create":
		if _, exists := dbMap[typeName]; exists {
			return fmt.Errorf("schema: type %q already exists on db %q", typeName, dbName)
		}
		entry := cloneMap(data)
		ensureFieldsTable(entry)
		dbMap[typeName] = entry
		return nil
	case "update":
		existingAny, exists := dbMap[typeName]
		if !exists {
			return fmt.Errorf("%w: type %q on db %q", ErrUnknownSchemaTarget, typeName, dbName)
		}
		existing, _ := existingAny.(map[string]any)
		if existing == nil {
			existing = map[string]any{}
		}
		// Replace meta-fields, preserve any existing fields sub-table.
		for _, metaKey := range []string{"description", "heading"} {
			delete(existing, metaKey)
		}
		maps.Copy(existing, data)
		ensureFieldsTable(existing)
		dbMap[typeName] = existing
		return nil
	case "delete":
		existingAny, exists := dbMap[typeName]
		if !exists {
			return fmt.Errorf("%w: type %q on db %q", ErrUnknownSchemaTarget, typeName, dbName)
		}
		_ = existingAny
		// §3.3 delete: errors if records of this type exist on disk.
		has, err := typeHasRecordsOnDisk(projectPath, dbName, typeName, root)
		if err != nil {
			return err
		}
		if has {
			return fmt.Errorf("%w: type %q on db %q", ErrTypeHasRecords, typeName, dbName)
		}
		delete(dbMap, typeName)
		return nil
	}
	return fmt.Errorf("schema: unknown action %q", action)
}

func applyFieldMutation(action, name string, data map[string]any, root map[string]any) error {
	dbName, typeName, fieldName := splitThree(name)
	if dbName == "" || typeName == "" || fieldName == "" {
		return fmt.Errorf("schema: field name %q must be '<db>.<type>.<field>'", name)
	}
	dbAny, ok := root[dbName]
	if !ok {
		return fmt.Errorf("%w: db %q", ErrUnknownSchemaTarget, dbName)
	}
	dbMap, ok := dbAny.(map[string]any)
	if !ok {
		return fmt.Errorf("schema: db %q has non-table entry", dbName)
	}
	typeAny, ok := dbMap[typeName]
	if !ok {
		return fmt.Errorf("%w: type %q on db %q", ErrUnknownSchemaTarget, typeName, dbName)
	}
	typeMap, ok := typeAny.(map[string]any)
	if !ok {
		return fmt.Errorf("schema: type %q has non-table entry", typeName)
	}
	fields := ensureFieldsTable(typeMap)

	switch action {
	case "create":
		if _, exists := fields[fieldName]; exists {
			return fmt.Errorf("schema: field %q already exists on %q.%q", fieldName, dbName, typeName)
		}
		fields[fieldName] = cloneMap(data)
		return nil
	case "update":
		if _, exists := fields[fieldName]; !exists {
			return fmt.Errorf("%w: field %q on %q.%q", ErrUnknownSchemaTarget, fieldName, dbName, typeName)
		}
		fields[fieldName] = cloneMap(data)
		return nil
	case "delete":
		if _, exists := fields[fieldName]; !exists {
			return fmt.Errorf("%w: field %q on %q.%q", ErrUnknownSchemaTarget, fieldName, dbName, typeName)
		}
		delete(fields, fieldName)
		return nil
	}
	return fmt.Errorf("schema: unknown action %q", action)
}

// applyBaseMutation handles kind=base mutations. `name` is the dotted
// form `<db>.<base-name>` (parallel to kind=type's `<db>.<type>`). The
// data payload accepts `description`, `extends`, and a nested `fields`
// table — the same shape buildBaseDecl parses at load time.
//
// Semantics by action:
//   - create: insert a fresh [<db>.bases.<base-name>] block. Fails if
//     the base already exists.
//   - update: replace the entire block (description, extends, fields)
//     with the supplied payload. Fails if the base does not exist.
//   - delete: remove the block. Fails with ErrBaseStillReferenced when
//     any concrete type or other base extends the target — the message
//     lists every referrer so the caller can break the chain
//     deliberately. The post-mutation atomic-rollback re-validate in
//     MutateSchema also catches this from the bottom up; the explicit
//     check here surfaces a dedicated sentinel and clearer message.
func applyBaseMutation(action, name string, data map[string]any, root map[string]any) error {
	dbName, baseName, rest := splitTwo(name)
	if dbName == "" || baseName == "" || rest != "" {
		return fmt.Errorf("schema: base name %q must be '<db>.<base>'", name)
	}
	dbAny, ok := root[dbName]
	if !ok {
		return fmt.Errorf("%w: db %q", ErrUnknownSchemaTarget, dbName)
	}
	dbMap, ok := dbAny.(map[string]any)
	if !ok {
		return fmt.Errorf("schema: db %q has non-table entry", dbName)
	}
	bases := ensureBasesTable(dbMap)

	switch action {
	case "create":
		if _, exists := bases[baseName]; exists {
			return fmt.Errorf("schema: base %q already exists on db %q", baseName, dbName)
		}
		bases[baseName] = cloneMap(data)
		return nil
	case "update":
		if _, exists := bases[baseName]; !exists {
			return fmt.Errorf("%w: base %q on db %q", ErrUnknownSchemaTarget, baseName, dbName)
		}
		bases[baseName] = cloneMap(data)
		return nil
	case "delete":
		if _, exists := bases[baseName]; !exists {
			return fmt.Errorf("%w: base %q on db %q", ErrUnknownSchemaTarget, baseName, dbName)
		}
		referrers := findBaseReferrers(baseName, root, dbName, baseName)
		if len(referrers) > 0 {
			return fmt.Errorf(
				"%w: base %q on db %q referenced by [%s]",
				ErrBaseStillReferenced, baseName, dbName, strings.Join(referrers, ", "),
			)
		}
		delete(bases, baseName)
		return nil
	}
	return fmt.Errorf("schema: unknown action %q", action)
}

// ensureBasesTable returns dbMap["bases"] as a map[string]any,
// creating the sub-table when missing. Mirrors ensureFieldsTable.
func ensureBasesTable(dbMap map[string]any) map[string]any {
	basesAny, ok := dbMap["bases"]
	if !ok {
		bases := map[string]any{}
		dbMap["bases"] = bases
		return bases
	}
	bases, ok := basesAny.(map[string]any)
	if !ok {
		bases = map[string]any{}
		dbMap["bases"] = bases
	}
	return bases
}

// findBaseReferrers walks the in-memory schema map looking for any
// concrete record type or other base whose `extends` key resolves to
// the base named target. Returns each referrer in dotted form
// (`<db>.<symbol>`) so the caller can render a deterministic
// ErrBaseStillReferenced message. The (excludeDB, excludeBase) pair
// names the base being deleted itself — its own `extends` key, if
// any, is irrelevant to the delete and is filtered out.
//
// Bases live in a Registry-wide namespace (per F22), so the scan
// crosses every db rather than restricting to the target db.
func findBaseReferrers(target string, root map[string]any, excludeDB, excludeBase string) []string {
	var referrers []string
	dbNames := make([]string, 0, len(root))
	for n := range root {
		dbNames = append(dbNames, n)
	}
	sort.Strings(dbNames)
	for _, dbName := range dbNames {
		dbMap, ok := root[dbName].(map[string]any)
		if !ok {
			continue
		}
		// Concrete record types: every non-meta sub-table at the db
		// level is a record-type body that may carry extends.
		typeNames := make([]string, 0, len(dbMap))
		for k := range dbMap {
			typeNames = append(typeNames, k)
		}
		sort.Strings(typeNames)
		for _, key := range typeNames {
			if key == "paths" || key == "description" || key == "types" || key == "bases" {
				continue
			}
			body, ok := dbMap[key].(map[string]any)
			if !ok {
				continue
			}
			ext, _ := body["extends"].(string)
			if ext == target {
				referrers = append(referrers, dbName+"."+key)
			}
		}
		// Other bases.
		basesAny, ok := dbMap["bases"].(map[string]any)
		if !ok {
			continue
		}
		baseNames := make([]string, 0, len(basesAny))
		for k := range basesAny {
			baseNames = append(baseNames, k)
		}
		sort.Strings(baseNames)
		for _, bname := range baseNames {
			if dbName == excludeDB && bname == excludeBase {
				continue
			}
			body, ok := basesAny[bname].(map[string]any)
			if !ok {
				continue
			}
			ext, _ := body["extends"].(string)
			if ext == target {
				referrers = append(referrers, dbName+".bases."+bname)
			}
		}
	}
	return referrers
}

// dbHasDataOnDisk returns true when any backing file for the target db
// exists on disk. Phase 9.2 (PLAN §12.17.9) routes every shape through
// resolver.Instances — single-file mounts surface as one Instance,
// glob mounts as one per concrete file, and collection mounts as one
// per descendant.
func dbHasDataOnDisk(projectPath, dbName string, root map[string]any) (bool, error) {
	reg, err := registryFromRoot(root)
	if err != nil {
		// If the current map can't build a registry (e.g. mid-update),
		// skip the scan — the serializer's downstream re-validate will
		// catch malformed shapes. Treat as no-data so deletion can
		// proceed when the authoring intent is "remove empty entry".
		return false, nil
	}
	if _, ok := reg.DBs[dbName]; !ok {
		return false, nil
	}
	resolver := db.NewResolver(projectPath, reg)
	instances, err := resolver.Instances(dbName)
	if err != nil {
		return false, err
	}
	return len(instances) > 0, nil
}

// typeHasRecordsOnDisk returns true when any instance file of dbName
// contains a declared record of typeName. Scans every instance's
// backing file once, routing through the format's backend.
func typeHasRecordsOnDisk(projectPath, dbName, typeName string, root map[string]any) (bool, error) {
	reg, err := registryFromRoot(root)
	if err != nil {
		return false, nil
	}
	dbDecl, ok := reg.DBs[dbName]
	if !ok {
		return false, nil
	}
	singleFile := schema.IsSingleFileDB(dbDecl)
	resolver := db.NewResolver(projectPath, reg)
	instances, err := resolver.Instances(dbName)
	if err != nil {
		return false, err
	}
	for _, inst := range instances {
		// Per F10 brackets are ids; we cannot scan-by-type from disk
		// alone. Consult the index instead — it is the authoritative
		// type source. If the index is missing, defer to the loud
		// failure path users see on next reboot/rebuild.
		idx, ierr := loadIndexOrSentinel(projectPath)
		if ierr != nil {
			// No index = no records in scope known; allow the delete.
			return false, nil
		}
		var hasRecords bool
		idx.Walk(func(canonical string, e index.Entry) bool {
			// Match canonical ids that begin with this instance's slug.
			if !strings.HasPrefix(canonical, inst.Slug+".") && canonical != inst.Slug {
				return true
			}
			if e.Type == typeName {
				hasRecords = true
				return false
			}
			return true
		})
		if hasRecords {
			return true, nil
		}
		_ = singleFile
	}
	return false, nil
}

// registryFromRoot round-trips the in-memory map through Marshal +
// schema.LoadBytes so callers get the fully-validated registry view
// using the same rules as cold-load. Returns an error when the map
// does not yet satisfy meta-schema constraints — callers should
// treat that as "skip the disk-scan guardrail" because the downstream
// serializer's LoadBytes check will surface the violation anyway.
func registryFromRoot(root map[string]any) (schema.Registry, error) {
	buf, err := pelletier.Marshal(root)
	if err != nil {
		return schema.Registry{}, err
	}
	return schema.LoadBytes(buf)
}

// ensureFieldsTable ensures typeMap["fields"] is a map[string]any and
// returns the fields sub-table. Creates the sub-table if missing.
func ensureFieldsTable(typeMap map[string]any) map[string]any {
	fieldsAny, ok := typeMap["fields"]
	if !ok {
		fields := map[string]any{}
		typeMap["fields"] = fields
		return fields
	}
	fields, ok := fieldsAny.(map[string]any)
	if !ok {
		fields = map[string]any{}
		typeMap["fields"] = fields
	}
	return fields
}

// cloneMap returns a shallow copy of m so callers can mutate the result
// without aliasing the caller's data. Nested maps are shared — we do
// not deep-copy because schema payloads are write-once here.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	maps.Copy(out, m)
	return out
}

// splitTwo returns "<first>.<second>" decomposition.
func splitTwo(s string) (string, string, string) {
	first, after, ok := strings.Cut(s, ".")
	if !ok {
		return s, "", ""
	}
	second, rest, _ := strings.Cut(after, ".")
	return first, second, rest
}

// splitThree returns "<first>.<second>.<third>" decomposition,
// accepting exactly three segments. Any other shape returns empty
// strings for the missing slots so the caller can report a dotted-name
// validation error.
func splitThree(s string) (string, string, string) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return "", "", ""
	}
	return parts[0], parts[1], parts[2]
}

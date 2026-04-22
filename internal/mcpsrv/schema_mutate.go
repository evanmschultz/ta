package mcpsrv

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	pelletier "github.com/pelletier/go-toml/v2"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/schema"
)

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
// action ∈ {create, update, delete}; kind ∈ {db, type, field}; name is
// the dotted address per §3.3. For delete actions the caller passes
// data=nil — the handler above enforces the distinction.
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
		return nil, fmt.Errorf("mcpsrv: marshal updated schema: %w", err)
	}
	// 5. Re-validate via schema.LoadBytes. If invalid → rollback (don't write).
	if _, err := schema.LoadBytes(newBuf); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMetaSchemaViolation, err)
	}
	// 6. Atomic-write.
	if err := os.MkdirAll(filepath.Dir(schemaPath), 0o755); err != nil {
		return nil, fmt.Errorf("mcpsrv: mkdir %s: %w", filepath.Dir(schemaPath), err)
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
		return nil, fmt.Errorf("mcpsrv: read %s: %w", path, err)
	}
	var root map[string]any
	if err := pelletier.Unmarshal(buf, &root); err != nil {
		return nil, fmt.Errorf("mcpsrv: parse %s: %w", path, err)
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
	default:
		return fmt.Errorf("schema: unknown kind %q (want db|type|field)", kind)
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
		for _, metaKey := range []string{"file", "directory", "collection", "format", "description"} {
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

// dbHasDataOnDisk returns true when any backing file for the target db
// exists on disk. The scan uses resolver.Instances so it catches
// dir-per-instance subdirs, collection files, and single-instance
// files uniformly.
func dbHasDataOnDisk(projectPath, dbName string, root map[string]any) (bool, error) {
	reg, err := registryFromRoot(root)
	if err != nil {
		// If the current map can't build a registry (e.g. mid-update),
		// skip the scan — the serializer's downstream re-validate will
		// catch malformed shapes. Treat as no-data so deletion can
		// proceed when the authoring intent is "remove empty entry".
		return false, nil
	}
	dbDecl, ok := reg.DBs[dbName]
	if !ok {
		return false, nil
	}
	if dbDecl.Shape == schema.ShapeFile {
		target := filepath.Join(projectPath, dbDecl.Path)
		if _, err := os.Stat(target); err == nil {
			return true, nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return false, fmt.Errorf("stat %s: %w", target, err)
		}
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
	backend, err := buildBackend(dbDecl)
	if err != nil {
		return false, err
	}
	resolver := db.NewResolver(projectPath, reg)
	instances, err := resolver.Instances(dbName)
	if err != nil {
		return false, err
	}
	scope := typeName
	if dbDecl.Format == schema.FormatTOML && dbDecl.Shape == schema.ShapeFile {
		scope = dbName + "." + typeName
	}
	for _, inst := range instances {
		buf, err := os.ReadFile(inst.FilePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return false, fmt.Errorf("read %s: %w", inst.FilePath, err)
		}
		paths, err := backend.List(buf, scope)
		if err != nil {
			return false, fmt.Errorf("list %s: %w", inst.FilePath, err)
		}
		if len(paths) > 0 {
			return true, nil
		}
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

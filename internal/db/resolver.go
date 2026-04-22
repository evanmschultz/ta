package db

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/evanmschultz/ta/internal/schema"
)

// Resolver owns the project-root + schema.Registry pair that address
// resolution needs. It is safe to reuse across calls; it performs no
// caching, so filesystem changes between calls are always observed.
type Resolver struct {
	root     string
	registry schema.Registry
}

// NewResolver constructs a resolver over the given project root and
// registry. root is the absolute filesystem path whose contents the
// declared db paths are resolved against. registry is the resolved
// (cascade-merged) schema.
func NewResolver(root string, registry schema.Registry) *Resolver {
	return &Resolver{root: root, registry: registry}
}

// Instance is one resolved database instance: Slug is the instance
// identifier (empty for single-instance dbs); DirPath is the filesystem
// directory that owns the instance (empty for single-instance dbs);
// FilePath is the absolute path of the backing file.
type Instance struct {
	Slug     string
	DirPath  string
	FilePath string
}

// canonicalFileName returns the db.<ext> filename a dir-per-instance db
// uses inside each subdir. ShapeDirectory convention per §5.5.1.
func canonicalFileName(db schema.DB) string {
	return "db." + string(db.Format)
}

// Instances enumerates every concrete instance of dbName on disk. The
// return value is shape-dependent:
//
//   - ShapeFile: exactly one Instance with Slug="" and FilePath set to
//     the absolute path of the declared file.
//   - ShapeDirectory: one Instance per immediate subdir of the declared
//     directory that contains a canonical db.<ext> file. Subdirs without
//     the canonical file are skipped; nested canonical files deeper than
//     one level are ignored; stray files at the dir root are ignored.
//   - ShapeCollection: one Instance per file under the declared
//     directory (recursively) whose extension matches the db's format.
//     Dotfiles and dotdirs are skipped. Slug collisions fail loudly with
//     ErrSlugCollision (includes both paths in the error).
//
// Returns an empty slice (no error) when the declared directory does
// not exist yet — first-create is legal and the caller will mkdir it.
func (r *Resolver) Instances(dbName string) ([]Instance, error) {
	db, ok := r.registry.DBs[dbName]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDB, dbName)
	}

	switch db.Shape {
	case schema.ShapeFile:
		return []Instance{{
			Slug:     "",
			DirPath:  "",
			FilePath: filepath.Join(r.root, db.Path),
		}}, nil
	case schema.ShapeDirectory:
		return r.scanDirectory(db)
	case schema.ShapeCollection:
		return r.scanCollection(db)
	default:
		return nil, fmt.Errorf("%w: %q on db %q", ErrUnsupportedShape, db.Shape, db.Name)
	}
}

func (r *Resolver) scanDirectory(db schema.DB) ([]Instance, error) {
	base := filepath.Join(r.root, db.Path)
	entries, err := os.ReadDir(base)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("db %q: scan %s: %w", db.Name, base, err)
	}
	canonical := canonicalFileName(db)

	out := make([]Instance, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		dirPath := filepath.Join(base, e.Name())
		filePath := filepath.Join(dirPath, canonical)
		info, err := os.Stat(filePath)
		if err != nil || info.IsDir() {
			continue
		}
		out = append(out, Instance{
			Slug:     e.Name(),
			DirPath:  dirPath,
			FilePath: filePath,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slug < out[j].Slug })
	return out, nil
}

func (r *Resolver) scanCollection(db schema.DB) ([]Instance, error) {
	base := filepath.Join(r.root, db.Path)
	ext := "." + string(db.Format)

	bySlug := map[string][]Instance{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		rel, rerr := filepath.Rel(base, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		// Skip dotfiles / dotdirs at any depth.
		for _, seg := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.HasPrefix(seg, ".") {
				if d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
		}
		if d.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ext) {
			return nil
		}
		slug := slugFromCollectionPath(rel, string(db.Format))
		if slug == "" {
			return nil
		}
		bySlug[slug] = append(bySlug[slug], Instance{
			Slug:     slug,
			DirPath:  filepath.Dir(path),
			FilePath: path,
		})
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("db %q: walk %s: %w", db.Name, base, err)
	}

	// Collect and detect collisions.
	slugs := make([]string, 0, len(bySlug))
	for s := range bySlug {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)

	out := make([]Instance, 0, len(bySlug))
	for _, s := range slugs {
		group := bySlug[s]
		if len(group) > 1 {
			sort.Slice(group, func(i, j int) bool { return group[i].FilePath < group[j].FilePath })
			paths := make([]string, len(group))
			for i, g := range group {
				paths[i] = g.FilePath
			}
			return nil, fmt.Errorf("%w: slug %q maps to %s",
				ErrSlugCollision, s, strings.Join(paths, " and "))
		}
		out = append(out, group[0])
	}
	return out, nil
}

// MatchSlug reports whether slug matches a scope expression. "*"
// matches anything; "prefix-*" matches slugs that start with "prefix-"
// (the hyphen is part of the literal pattern). No other metacharacters
// are supported; bare strings require an exact match. §5.5.3.
func (r *Resolver) MatchSlug(scope, slug string) bool {
	if scope == "*" {
		return true
	}
	if strings.HasSuffix(scope, "*") {
		prefix := scope[:len(scope)-1]
		return strings.HasPrefix(slug, prefix) && len(slug) > len(prefix)
	}
	return scope == slug
}

// ResolveRead parses section and returns the db, instance, and absolute
// file path to read. For multi-instance dbs the instance must exist on
// disk; a missing instance returns ErrInstanceNotFound. Slug collisions
// propagate as ErrSlugCollision.
func (r *Resolver) ResolveRead(section string) (schema.DB, Instance, string, error) {
	addr, db, err := r.ParseAddress(section)
	if err != nil {
		return schema.DB{}, Instance{}, "", err
	}
	switch db.Shape {
	case schema.ShapeFile:
		inst := Instance{FilePath: filepath.Join(r.root, db.Path)}
		return db, inst, inst.FilePath, nil
	case schema.ShapeDirectory, schema.ShapeCollection:
		instances, err := r.Instances(db.Name)
		if err != nil {
			return schema.DB{}, Instance{}, "", err
		}
		for _, inst := range instances {
			if inst.Slug == addr.Instance {
				return db, inst, inst.FilePath, nil
			}
		}
		return schema.DB{}, Instance{}, "", fmt.Errorf(
			"%w: db %q instance %q", ErrInstanceNotFound, db.Name, addr.Instance)
	default:
		return schema.DB{}, Instance{}, "", fmt.Errorf(
			"%w: %q", ErrUnsupportedShape, db.Shape)
	}
}

// ResolveWrite parses section and returns the db, instance, and
// absolute file path to write. path_hint is consulted only for
// ShapeCollection dbs (file-per-instance) — ShapeFile rejects any
// non-empty hint, and ShapeDirectory ignores it (the canonical filename
// is fixed).
//
// For a new instance on ShapeCollection, empty hint produces a flat
// path (docs/<slug>.<ext>); a non-empty hint is interpreted as a path
// relative to the collection root. For an existing instance, a
// non-empty hint must exactly match the on-disk relative path, else
// ErrPathHintMismatch.
//
// The returned path's parent directory may not exist yet — the caller
// is responsible for mkdir + file creation (§12.5).
func (r *Resolver) ResolveWrite(section, pathHint string) (schema.DB, Instance, string, error) {
	addr, db, err := r.ParseAddress(section)
	if err != nil {
		return schema.DB{}, Instance{}, "", err
	}
	switch db.Shape {
	case schema.ShapeFile:
		if pathHint != "" {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"%w: single-instance db %q does not accept path_hint",
				ErrBadAddress, db.Name)
		}
		inst := Instance{FilePath: filepath.Join(r.root, db.Path)}
		return db, inst, inst.FilePath, nil
	case schema.ShapeDirectory:
		// Canonical filename is fixed; path_hint would have no meaning.
		if pathHint != "" {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"%w: dir-per-instance db %q uses canonical filename, path_hint not allowed",
				ErrBadAddress, db.Name)
		}
		dirPath := filepath.Join(r.root, db.Path, addr.Instance)
		filePath := filepath.Join(dirPath, canonicalFileName(db))
		inst := Instance{Slug: addr.Instance, DirPath: dirPath, FilePath: filePath}
		return db, inst, filePath, nil
	case schema.ShapeCollection:
		return r.resolveWriteCollection(db, addr, pathHint)
	default:
		return schema.DB{}, Instance{}, "", fmt.Errorf(
			"%w: %q", ErrUnsupportedShape, db.Shape)
	}
}

func (r *Resolver) resolveWriteCollection(db schema.DB, addr Address, pathHint string) (schema.DB, Instance, string, error) {
	base := filepath.Join(r.root, db.Path)
	ext := "." + string(db.Format)

	// Find an existing instance with this slug, if any.
	instances, err := r.Instances(db.Name)
	if err != nil {
		return schema.DB{}, Instance{}, "", err
	}
	var existing *Instance
	for i := range instances {
		if instances[i].Slug == addr.Instance {
			existing = &instances[i]
			break
		}
	}

	if existing != nil {
		if pathHint == "" {
			return db, *existing, existing.FilePath, nil
		}
		// Non-empty hint on existing must match the on-disk relative path.
		hintClean := filepath.ToSlash(filepath.Clean(pathHint))
		existingRel, err := filepath.Rel(base, existing.FilePath)
		if err != nil {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"db %q: rel %s: %w", db.Name, existing.FilePath, err)
		}
		existingRelSlash := filepath.ToSlash(existingRel)
		if hintClean != existingRelSlash {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"%w: instance %q exists at %q, hint was %q",
				ErrPathHintMismatch, addr.Instance, existingRelSlash, hintClean)
		}
		return db, *existing, existing.FilePath, nil
	}

	// New instance: derive target path from hint, else flat <slug>.<ext>.
	var relPath string
	if pathHint == "" {
		relPath = addr.Instance + ext
	} else {
		relPath = filepath.Clean(filepath.FromSlash(pathHint))
		// Safety (V2-PLAN §11.D): path_hint must stay inside the
		// collection root. filepath.IsLocal rejects absolute paths,
		// any '..' segment, empty, and Windows reserved names lexically
		// — exactly the guarantees we need so the eventual
		// filepath.Join(base, relPath) cannot escape base.
		if !filepath.IsLocal(relPath) {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"%w: path_hint %q escapes collection root",
				ErrPathHintMismatch, pathHint)
		}
		// Sanity: hint must produce the given slug.
		hintSlug := slugFromCollectionPath(relPath, string(db.Format))
		if hintSlug != addr.Instance {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"%w: hint %q yields slug %q, address instance is %q",
				ErrPathHintMismatch, pathHint, hintSlug, addr.Instance)
		}
	}
	filePath := filepath.Join(base, relPath)
	inst := Instance{
		Slug:     addr.Instance,
		DirPath:  filepath.Dir(filePath),
		FilePath: filePath,
	}
	return db, inst, filePath, nil
}

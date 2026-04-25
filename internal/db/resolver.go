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

// Instance is one resolved database instance: Slug is the dotted
// file-relpath (the address-form of the file); FilePath is the
// absolute path of the backing file; DirPath is filepath.Dir(FilePath).
//
// The Phase 9.2 model unifies the previously-distinct file / directory /
// collection shapes — every concrete file backing the db produces one
// Instance regardless of whether the mount is single-file, glob, or
// collection-rooted.
type Instance struct {
	Slug     string
	DirPath  string
	FilePath string
}

// Instances enumerates every concrete file backing dbName by expanding
// each entry in db.Paths and stat-walking the resulting set. Glob
// segments (`*`) match one path segment; trailing `/` collection roots
// recurse depth-first; `~/...` mounts expand against the user's home
// directory. Returns an empty slice (no error) when the declared
// directory does not exist yet — first-create is legal and the caller
// will mkdir it. ErrSlugCollision fires when two distinct file paths
// produce the same dotted file-relpath.
func (r *Resolver) Instances(dbName string) ([]Instance, error) {
	dbDecl, ok := r.registry.DBs[dbName]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownDB, dbName)
	}
	bySlug := map[string][]Instance{}
	for _, mount := range dbDecl.Paths {
		insts, err := r.expandMount(dbDecl, mount)
		if err != nil {
			return nil, err
		}
		for _, inst := range insts {
			bySlug[inst.Slug] = append(bySlug[inst.Slug], inst)
		}
	}
	slugs := make([]string, 0, len(bySlug))
	for s := range bySlug {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)
	out := make([]Instance, 0, len(slugs))
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

// expandMount resolves one mount entry of dbDecl into a slice of
// concrete Instance entries. Globs are expanded segment-by-segment
// (each `*` matches one non-dotfile path segment); collection roots
// recurse via filepath.WalkDir. The mount is project-root-relative
// unless prefixed with `~/`, in which case it expands against the
// user's home directory.
func (r *Resolver) expandMount(dbDecl schema.DB, mount string) ([]Instance, error) {
	base, mountAfterHome, err := resolveHome(r.root, mount)
	if err != nil {
		return nil, fmt.Errorf("db %q: mount %q: %w", dbDecl.Name, mount, err)
	}
	staticPrefix, residualSegs := splitMountSegments(mountAfterHome)
	collection := strings.HasSuffix(mountAfterHome, "/") || mountAfterHome == "."
	ext := "." + string(dbDecl.Format)

	if collection {
		root := filepath.Join(base, filepath.FromSlash(strings.TrimSuffix(staticPrefix, "/")))
		return walkCollection(root, ext, staticPrefix, base)
	}

	// Non-collection: expand globs by walking each `*` segment in turn.
	expectedSegs := stripFormatExt(residualSegs, dbDecl.Format)
	concretePaths, err := expandGlobs(filepath.Join(base, filepath.FromSlash(strings.TrimSuffix(staticPrefix, "/"))), expectedSegs)
	if err != nil {
		return nil, err
	}
	out := make([]Instance, 0, len(concretePaths))
	for _, segPath := range concretePaths {
		// segPath is the directory + leaf-without-ext; append the format
		// extension to get the file.
		filePath := segPath + ext
		info, err := os.Stat(filePath)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("db %q: stat %s: %w", dbDecl.Name, filePath, err)
		}
		if info.IsDir() {
			continue
		}
		// File-relpath: take the path relative to base, strip ext, dot-replace.
		rel, err := filepath.Rel(base, filePath)
		if err != nil {
			return nil, fmt.Errorf("db %q: rel %s: %w", dbDecl.Name, filePath, err)
		}
		relSlash := filepath.ToSlash(rel)
		// Strip staticPrefix from the front to get the file-relpath
		// portion (consistent with ParseAddress).
		residualPath := strings.TrimPrefix(relSlash, staticPrefix)
		residualPath = strings.TrimSuffix(residualPath, ext)
		slug := strings.ReplaceAll(residualPath, "/", ".")
		out = append(out, Instance{
			Slug:     slug,
			DirPath:  filepath.Dir(filePath),
			FilePath: filePath,
		})
	}
	return out, nil
}

// resolveHome handles the `~/` prefix: when mount starts with `~/`,
// returns (homeDir, mount-without-tilde, nil); otherwise returns
// (root, mount, nil). os.UserHomeDir errors propagate.
func resolveHome(root, mount string) (string, string, error) {
	if !strings.HasPrefix(mount, "~/") {
		return root, mount, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	return home, strings.TrimPrefix(mount, "~/"), nil
}

// walkCollection recursively scans rootDir for files with the given
// extension and produces Instance entries with file-relpath slugs
// computed against base + staticPrefix (so the slug round-trips
// through ParseAddress).
func walkCollection(rootDir, ext, staticPrefix, base string) ([]Instance, error) {
	out := make([]Instance, 0)
	err := filepath.WalkDir(rootDir, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			if errors.Is(werr, fs.ErrNotExist) {
				return nil
			}
			return werr
		}
		if d.IsDir() {
			// Skip dot-dirs (anywhere in the tree).
			if path != rootDir && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(d.Name()), ext) {
			return nil
		}
		rel, err := filepath.Rel(base, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		residualPath := strings.TrimPrefix(relSlash, staticPrefix)
		residualPath = strings.TrimSuffix(residualPath, ext)
		slug := strings.ReplaceAll(residualPath, "/", ".")
		out = append(out, Instance{
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
		return nil, fmt.Errorf("walk %s: %w", rootDir, err)
	}
	return out, nil
}

// expandGlobs expands a sequence of path segments, where each `*`
// matches one non-dotfile entry (a directory or, for the leaf, a
// file basename without extension). The returned slice contains the
// concrete sub-path (no extension) of each match, anchored at base.
//
// For a non-glob suffix the result is a single deterministic path;
// for a `*`-bearing pattern the result is one entry per directory
// scan match. Missing directories along the way are not an error —
// the empty result reflects "no instances yet".
func expandGlobs(base string, segs []string) ([]string, error) {
	if len(segs) == 0 {
		return []string{base}, nil
	}
	current := []string{base}
	for i, seg := range segs {
		isLeaf := i == len(segs)-1
		var next []string
		for _, dir := range current {
			matches, err := matchSegment(dir, seg, isLeaf)
			if err != nil {
				return nil, err
			}
			next = append(next, matches...)
		}
		current = next
	}
	return current, nil
}

// matchSegment expands one path segment against dir. `*` matches every
// non-dotfile entry (directories for non-leaf segments, files for the
// leaf — though leaf files are matched by basename-without-ext at the
// caller's stat step). Literal segments expand to one fixed path.
// Missing dir → empty result (not an error).
func matchSegment(dir, seg string, leaf bool) ([]string, error) {
	if seg != "*" {
		return []string{filepath.Join(dir, seg)}, nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", dir, err)
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		if leaf {
			// Leaf glob matches any entry by basename; caller checks
			// extension and stat. We strip the extension to align with
			// the convention that mount segments are extensionless.
			if e.IsDir() {
				continue
			}
			noExt := strings.TrimSuffix(name, filepath.Ext(name))
			out = append(out, filepath.Join(dir, noExt))
			continue
		}
		if !e.IsDir() {
			continue
		}
		out = append(out, filepath.Join(dir, name))
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

// ResolveRead parses section under the new file-relpath grammar and
// returns the resolved db, instance, and absolute file path. Returns
// ErrInstanceNotFound if the parsed file path does not exist on disk.
func (r *Resolver) ResolveRead(section string) (schema.DB, Instance, string, error) {
	addr, dbDecl, err := r.ParseAddress(section)
	if err != nil {
		return schema.DB{}, Instance{}, "", err
	}
	if _, err := os.Stat(addr.FilePath); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return schema.DB{}, Instance{}, "", fmt.Errorf(
				"%w: db %q file-relpath %q (%s)",
				ErrInstanceNotFound, dbDecl.Name, addr.FileRelPath, addr.FilePath)
		}
		return schema.DB{}, Instance{}, "", fmt.Errorf(
			"stat %s: %w", addr.FilePath, err)
	}
	inst := Instance{
		Slug:     addr.FileRelPath,
		DirPath:  filepath.Dir(addr.FilePath),
		FilePath: addr.FilePath,
	}
	return dbDecl, inst, addr.FilePath, nil
}

// ResolveWrite parses section and returns the db, instance, and
// absolute file path to write. The Phase 9.2 grammar derives the
// target file path entirely from the address — no path_hint is
// consulted. A non-empty pathHint is rejected for a clear error
// during the migration window; Phase 9.4 re-evaluates the parameter.
//
// The returned path's parent directory may not exist yet — the caller
// is responsible for mkdir + file creation.
func (r *Resolver) ResolveWrite(section, pathHint string) (schema.DB, Instance, string, error) {
	addr, dbDecl, err := r.ParseAddress(section)
	if err != nil {
		return schema.DB{}, Instance{}, "", err
	}
	if pathHint != "" {
		return schema.DB{}, Instance{}, "", fmt.Errorf(
			"%w: path_hint %q rejected — Phase 9.2 grammar derives target path from address (PLAN §12.17.9)",
			ErrPathHintMismatch, pathHint)
	}
	inst := Instance{
		Slug:     addr.FileRelPath,
		DirPath:  filepath.Dir(addr.FilePath),
		FilePath: addr.FilePath,
	}
	return dbDecl, inst, addr.FilePath, nil
}

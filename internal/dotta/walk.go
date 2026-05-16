package dotta

// Walk enumerates a `.ta/` dotta tree rooted at `root` into a Tree. It
// is a strict, read-only inspector: it does no I/O outside the root,
// follows no symlinks, and treats `mapping.toml` (see mapping.go) as
// per-subtree metadata that NEVER appears in the enumerated Files list
// at any depth.
//
// Categorisation rules (matching the SkipReason* constants in tree.go):
//   - Symlinks (root or nested) are recorded in Tree.Skipped with
//     SkipReasonSymlink and never traversed.
//   - Directory entries the OS refuses to enumerate (EACCES /
//     fs.ErrPermission) are recorded with SkipReasonPermissionDenied
//     and skipped — the walk continues past them.
//   - Irregular entries (sockets, devices, FIFOs, fs.ModeIrregular)
//     are recorded with SkipReasonIrregular.
//
// Output ordering is deterministic: Tree.RootFiles, Tree.Subtrees,
// each Subtree.Files, and Tree.Skipped are all sorted by their
// RelPath / Name field after enumeration. filepath.WalkDir already
// visits lexically, so the final sort is defence-in-depth — it makes
// the contract independent of any future change in stdlib walk order.

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"
)

// Walk produces a Tree describing the dotta root at `root`. The root
// must be a non-empty path that resolves (via filepath.Abs) to an
// existing directory; any other shape returns a wrapped "dotta:"
// error so the caller can log it without further annotation.
func Walk(root string) (Tree, error) {
	if root == "" {
		return Tree{}, fmt.Errorf("dotta: empty root")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Tree{}, fmt.Errorf("dotta: resolve walk root %q: %w", root, err)
	}

	info, err := os.Stat(absRoot)
	if err != nil {
		return Tree{}, fmt.Errorf("dotta: walk root %s: %w", root, err)
	}
	if !info.IsDir() {
		return Tree{}, fmt.Errorf("dotta: walk root %s: not a directory", root)
	}

	entries, err := os.ReadDir(absRoot)
	if err != nil {
		return Tree{}, fmt.Errorf("dotta: read root %s: %w", absRoot, err)
	}

	tree := Tree{Root: absRoot}

	for _, entry := range entries {
		name := entry.Name()
		absPath := filepath.Join(absRoot, name)

		// Symlinks at the root are recorded and never followed.
		if entry.Type()&fs.ModeSymlink != 0 {
			tree.Skipped = append(tree.Skipped, SkippedEntry{
				RelPath: name,
				Reason:  SkipReasonSymlink,
			})
			continue
		}

		if entry.IsDir() {
			sub, err := walkSubtree(absRoot, name)
			if err != nil {
				return Tree{}, err
			}
			tree.Subtrees = append(tree.Subtrees, sub.subtree)
			tree.Skipped = append(tree.Skipped, sub.skipped...)
			continue
		}

		if entry.Type().IsRegular() {
			fi, err := os.Lstat(absPath)
			if err != nil {
				return Tree{}, fmt.Errorf("dotta: lstat %s: %w", absPath, err)
			}
			tree.RootFiles = append(tree.RootFiles, FileMeta{
				Name:    name,
				AbsPath: absPath,
				RelPath: name,
				Size:    fi.Size(),
				Mode:    fi.Mode(),
			})
			continue
		}

		// Anything else at root (irregular: socket/device/fifo).
		tree.Skipped = append(tree.Skipped, SkippedEntry{
			RelPath: name,
			Reason:  SkipReasonIrregular,
		})
	}

	// Defensive final sort — filepath.WalkDir already visits lexically,
	// but we never want the Tree contract to depend on that.
	sort.Slice(tree.RootFiles, func(i, j int) bool {
		return tree.RootFiles[i].RelPath < tree.RootFiles[j].RelPath
	})
	sort.Slice(tree.Subtrees, func(i, j int) bool {
		return tree.Subtrees[i].RelPath < tree.Subtrees[j].RelPath
	})
	for i := range tree.Subtrees {
		files := tree.Subtrees[i].Files
		sort.Slice(files, func(a, b int) bool {
			return files[a].RelPath < files[b].RelPath
		})
	}
	sort.Slice(tree.Skipped, func(i, j int) bool {
		return tree.Skipped[i].RelPath < tree.Skipped[j].RelPath
	})

	return tree, nil
}

// subtreeResult collects what walkSubtree produced: the Subtree itself
// (always populated) plus any Skipped entries that surfaced inside it
// (those are bubbled up to Tree.Skipped so callers see ONE flat
// skipped list keyed by RelPath against the tree root).
type subtreeResult struct {
	subtree Subtree
	skipped []SkippedEntry
}

// walkSubtree enumerates one first-level directory under absRoot. The
// caller is responsible for symlink detection at the root level —
// walkSubtree is invoked only for entries that are real directories.
func walkSubtree(absRoot, name string) (subtreeResult, error) {
	absSubtree := filepath.Join(absRoot, name)

	mapping, err := LoadMapping(absSubtree)
	if err != nil {
		return subtreeResult{}, fmt.Errorf("dotta: subtree %s: %w", name, err)
	}

	sub := Subtree{
		Name:    name,
		AbsPath: absSubtree,
		RelPath: name,
		Mapping: mapping,
	}
	var skipped []SkippedEntry

	walkErr := filepath.WalkDir(absSubtree, func(path string, d fs.DirEntry, walkErr error) error {
		// Permission-denied during directory enumeration arrives here
		// with err != nil and d != nil; we record the directory as
		// skipped and tell WalkDir to skip its contents (returning
		// fs.SkipDir on a directory entry).
		if walkErr != nil {
			rel, relErr := filepath.Rel(absRoot, path)
			if relErr != nil {
				rel = path
			}
			if errors.Is(walkErr, fs.ErrPermission) || errors.Is(walkErr, syscall.EACCES) {
				skipped = append(skipped, SkippedEntry{
					RelPath: rel,
					Reason:  SkipReasonPermissionDenied,
				})
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			return fmt.Errorf("dotta: walk %s: %w", path, walkErr)
		}

		// Skip the subtree root itself.
		if path == absSubtree {
			return nil
		}

		// mapping.toml is reserved metadata at ANY depth — exclude it
		// from the enumerated Files list.
		if d.Name() == MappingFilename {
			return nil
		}

		rel, err := filepath.Rel(absRoot, path)
		if err != nil {
			return fmt.Errorf("dotta: relpath %s: %w", path, err)
		}

		typ := d.Type()

		// Symlinks: recorded, never followed (WalkDir does not follow
		// symlinks by default, so we just record and continue).
		if typ&fs.ModeSymlink != 0 {
			skipped = append(skipped, SkippedEntry{
				RelPath: rel,
				Reason:  SkipReasonSymlink,
			})
			return nil
		}

		if d.IsDir() {
			return nil
		}

		if typ.IsRegular() {
			fi, err := d.Info()
			if err != nil {
				return fmt.Errorf("dotta: info %s: %w", path, err)
			}
			subRel, err := filepath.Rel(absSubtree, path)
			if err != nil {
				return fmt.Errorf("dotta: subrel %s: %w", path, err)
			}
			sub.Files = append(sub.Files, FileMeta{
				Name:    d.Name(),
				AbsPath: path,
				RelPath: subRel,
				Size:    fi.Size(),
				Mode:    fi.Mode(),
			})
			return nil
		}

		// Irregular: socket / device / fifo / ModeIrregular.
		if typ&fs.ModeIrregular != 0 || typ&fs.ModeType != 0 {
			skipped = append(skipped, SkippedEntry{
				RelPath: rel,
				Reason:  SkipReasonIrregular,
			})
		}
		return nil
	})
	if walkErr != nil {
		return subtreeResult{}, walkErr
	}

	sort.Slice(sub.Files, func(i, j int) bool {
		return sub.Files[i].RelPath < sub.Files[j].RelPath
	})

	return subtreeResult{subtree: sub, skipped: skipped}, nil
}

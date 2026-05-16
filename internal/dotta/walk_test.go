package dotta

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// mkfile writes contents at the requested path (creating parent
// directories on demand) and fails the test on any error. It keeps
// the test bodies short and intent-revealing.
func mkfile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestWalk_EmptyRoot(t *testing.T) {
	root := t.TempDir()
	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk(empty dir) error = %v, want nil", err)
	}
	if len(tree.RootFiles) != 0 || len(tree.Subtrees) != 0 || len(tree.Skipped) != 0 {
		t.Fatalf("Walk(empty dir) = %+v, want all-empty Tree", tree)
	}
}

func TestWalk_MissingRoot(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := Walk(missing)
	if err == nil {
		t.Fatalf("Walk(missing) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "dotta:") {
		t.Fatalf("error %q missing 'dotta:' prefix", err.Error())
	}
}

func TestWalk_NotADirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "iamafile.txt")
	mkfile(t, file, "hello")

	_, err := Walk(file)
	if err == nil {
		t.Fatalf("Walk(file) error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "dotta:") {
		t.Fatalf("error %q missing 'dotta:' prefix", err.Error())
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("error %q missing 'not a directory' substring", err.Error())
	}
}

func TestWalk_EmptyRootString(t *testing.T) {
	_, err := Walk("")
	if err == nil {
		t.Fatalf("Walk(\"\") error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "dotta: empty root") {
		t.Fatalf("error %q missing 'dotta: empty root'", err.Error())
	}
}

func TestWalk_RootFilesOnly(t *testing.T) {
	root := t.TempDir()
	mkfile(t, filepath.Join(root, "schema.toml"), "x=1\n")
	mkfile(t, filepath.Join(root, "index.toml"), "y=2\n")

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(tree.Subtrees) != 0 {
		t.Fatalf("Subtrees = %d, want 0", len(tree.Subtrees))
	}
	if len(tree.RootFiles) != 2 {
		t.Fatalf("RootFiles = %d, want 2 (%+v)", len(tree.RootFiles), tree.RootFiles)
	}
	if tree.RootFiles[0].RelPath != "index.toml" || tree.RootFiles[1].RelPath != "schema.toml" {
		t.Fatalf("RootFiles RelPaths = [%s, %s], want sorted [index.toml schema.toml]",
			tree.RootFiles[0].RelPath, tree.RootFiles[1].RelPath)
	}
	if tree.RootFiles[0].Name != "index.toml" || tree.RootFiles[1].Name != "schema.toml" {
		t.Fatalf("RootFiles Names = [%s, %s], want sorted [index.toml schema.toml]",
			tree.RootFiles[0].Name, tree.RootFiles[1].Name)
	}
}

func TestWalk_SingleSubtreeNoMapping(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "claude_hooks")
	mkfile(t, filepath.Join(sub, "b.sh"), "#!/bin/sh\n")
	mkfile(t, filepath.Join(sub, "a.sh"), "#!/bin/sh\n")

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(tree.Subtrees) != 1 {
		t.Fatalf("Subtrees = %d, want 1", len(tree.Subtrees))
	}
	s := tree.Subtrees[0]
	if s.Name != "claude_hooks" {
		t.Fatalf("Subtree.Name = %q, want claude_hooks", s.Name)
	}
	if s.Mapping != (Mapping{}) {
		t.Fatalf("Mapping = %+v, want zero-value (no mapping.toml)", s.Mapping)
	}
	if len(s.Files) != 2 {
		t.Fatalf("Files = %d, want 2 (%+v)", len(s.Files), s.Files)
	}
	if s.Files[0].RelPath != "a.sh" || s.Files[1].RelPath != "b.sh" {
		t.Fatalf("Files RelPaths = [%s, %s], want sorted [a.sh b.sh]",
			s.Files[0].RelPath, s.Files[1].RelPath)
	}
}

func TestWalk_SingleSubtreeWithMapping(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "claude_hooks")
	mkfile(t, filepath.Join(sub, MappingFilename),
		"destination = \"~/.claude/hooks\"\non_conflict = \"skip\"\n")
	mkfile(t, filepath.Join(sub, "pre-commit.sh"), "#!/bin/sh\n")

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(tree.Subtrees) != 1 {
		t.Fatalf("Subtrees = %d, want 1", len(tree.Subtrees))
	}
	s := tree.Subtrees[0]
	if s.Mapping.Destination != "~/.claude/hooks" {
		t.Fatalf("Mapping.Destination = %q, want ~/.claude/hooks", s.Mapping.Destination)
	}
	if s.Mapping.OnConflict != OnConflictSkip {
		t.Fatalf("Mapping.OnConflict = %q, want %q", s.Mapping.OnConflict, OnConflictSkip)
	}
	// mapping.toml MUST NOT show up in Files.
	if len(s.Files) != 1 {
		t.Fatalf("Files = %+v, want exactly [pre-commit.sh]", s.Files)
	}
	for _, f := range s.Files {
		if f.Name == MappingFilename {
			t.Fatalf("mapping.toml leaked into Files: %+v", f)
		}
	}
}

func TestWalk_MultiSubtree(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"c_dir", "a_dir", "b_dir"} {
		mkfile(t, filepath.Join(root, name, "file.txt"), "x\n")
	}

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(tree.Subtrees) != 3 {
		t.Fatalf("Subtrees = %d, want 3", len(tree.Subtrees))
	}
	names := []string{tree.Subtrees[0].Name, tree.Subtrees[1].Name, tree.Subtrees[2].Name}
	want := []string{"a_dir", "b_dir", "c_dir"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("Subtree order = %v, want %v", names, want)
	}
}

func TestWalk_SymlinkSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(root, "linky")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(tree.Skipped) != 1 {
		t.Fatalf("Skipped = %d, want 1 (%+v)", len(tree.Skipped), tree.Skipped)
	}
	if tree.Skipped[0].Reason != SkipReasonSymlink {
		t.Fatalf("Skipped[0].Reason = %q, want %q", tree.Skipped[0].Reason, SkipReasonSymlink)
	}
	if tree.Skipped[0].RelPath != "linky" {
		t.Fatalf("Skipped[0].RelPath = %q, want linky", tree.Skipped[0].RelPath)
	}
}

func TestWalk_SymlinkInsideSubtree(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on Windows")
	}
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	mkfile(t, filepath.Join(sub, "real.txt"), "ok\n")
	target := t.TempDir()
	if err := os.Symlink(target, filepath.Join(sub, "ln")); err != nil {
		t.Fatalf("symlink: %v", err)
	}

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}

	var found bool
	for _, sk := range tree.Skipped {
		if sk.Reason == SkipReasonSymlink && strings.HasSuffix(sk.RelPath, "ln") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a symlink Skipped entry inside sub, got Skipped=%+v", tree.Skipped)
	}
	// Real file still enumerated.
	if len(tree.Subtrees) != 1 || len(tree.Subtrees[0].Files) != 1 ||
		tree.Subtrees[0].Files[0].RelPath != "real.txt" {
		t.Fatalf("expected exactly real.txt in subtree, got %+v", tree.Subtrees)
	}
}

func TestWalk_MappingBadOnConflict_PropagatesError(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "bad")
	mkfile(t, filepath.Join(sub, MappingFilename),
		"destination = \"~/x\"\non_conflict = \"wreck\"\n")

	_, err := Walk(root)
	if err == nil {
		t.Fatalf("Walk(bad mapping) error = nil, want non-nil")
	}
	for _, want := range []string{"dotta:", "invalid on_conflict"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestWalk_NestedFilesInSubtree(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "sub", "a", "b", "c.txt")
	mkfile(t, nested, "deep\n")

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v", err)
	}
	if len(tree.Subtrees) != 1 {
		t.Fatalf("Subtrees = %d, want 1", len(tree.Subtrees))
	}
	s := tree.Subtrees[0]
	if len(s.Files) != 1 {
		t.Fatalf("Files = %d, want 1 (%+v)", len(s.Files), s.Files)
	}
	wantRel := filepath.Join("a", "b", "c.txt")
	if s.Files[0].RelPath != wantRel {
		t.Fatalf("Files[0].RelPath = %q, want %q", s.Files[0].RelPath, wantRel)
	}
}

func TestWalk_DeterministicOrdering(t *testing.T) {
	root := t.TempDir()
	// Create in deliberately unsorted order.
	mkfile(t, filepath.Join(root, "z_root.txt"), "z\n")
	mkfile(t, filepath.Join(root, "a_root.txt"), "a\n")
	for _, name := range []string{"m_sub", "a_sub", "z_sub"} {
		mkfile(t, filepath.Join(root, name, "z.txt"), "z\n")
		mkfile(t, filepath.Join(root, name, "a.txt"), "a\n")
		mkfile(t, filepath.Join(root, name, "m.txt"), "m\n")
	}

	tree1, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk #1 error = %v", err)
	}
	tree2, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk #2 error = %v", err)
	}

	if !reflect.DeepEqual(tree1, tree2) {
		t.Fatalf("two Walks of the same root produced different trees:\n#1=%+v\n#2=%+v", tree1, tree2)
	}

	if !sort.SliceIsSorted(tree1.RootFiles, func(i, j int) bool {
		return tree1.RootFiles[i].RelPath < tree1.RootFiles[j].RelPath
	}) {
		t.Fatalf("RootFiles not sorted: %+v", tree1.RootFiles)
	}
	if !sort.SliceIsSorted(tree1.Subtrees, func(i, j int) bool {
		return tree1.Subtrees[i].RelPath < tree1.Subtrees[j].RelPath
	}) {
		t.Fatalf("Subtrees not sorted: %+v", tree1.Subtrees)
	}
	for _, s := range tree1.Subtrees {
		if !sort.SliceIsSorted(s.Files, func(i, j int) bool {
			return s.Files[i].RelPath < s.Files[j].RelPath
		}) {
			t.Fatalf("Subtree %s Files not sorted: %+v", s.Name, s.Files)
		}
	}
}

func TestWalk_PermissionDeniedDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod-based permission simulation does not apply on Windows")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses POSIX permission bits; cannot simulate permission-denied dir")
	}

	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	// Visible file at top of subtree so the walk has something to do
	// before it hits the locked dir.
	mkfile(t, filepath.Join(sub, "visible.txt"), "ok\n")
	locked := filepath.Join(sub, "locked")
	mkfile(t, filepath.Join(locked, "hidden.txt"), "secret\n")

	if err := os.Chmod(locked, 0); err != nil {
		t.Fatalf("chmod 0 locked: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(locked, 0o755) // let t.TempDir cleanup remove it
	})

	tree, err := Walk(root)
	if err != nil {
		t.Fatalf("Walk error = %v (want nil — permission-denied is skipped, not fatal)", err)
	}

	var found bool
	for _, sk := range tree.Skipped {
		if sk.Reason == SkipReasonPermissionDenied && strings.HasSuffix(sk.RelPath, "locked") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a permission-denied Skipped entry, got %+v", tree.Skipped)
	}

	// visible.txt must still be enumerated.
	if len(tree.Subtrees) != 1 || len(tree.Subtrees[0].Files) < 1 {
		t.Fatalf("expected visible.txt to still be enumerated, got %+v", tree.Subtrees)
	}
	var seenVisible bool
	for _, f := range tree.Subtrees[0].Files {
		if f.Name == "visible.txt" {
			seenVisible = true
		}
	}
	if !seenVisible {
		t.Fatalf("visible.txt missing from subtree Files: %+v", tree.Subtrees[0].Files)
	}
}

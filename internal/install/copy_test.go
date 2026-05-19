package install

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFile_CopiesBytes(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	want := []byte("hello cascade\n")
	if err := os.WriteFile(src, want, 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	if err := CopyFile(src, dst, ""); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("dst content mismatch:\n  got:  %q\n  want: %q", got, want)
	}
}

func TestCopyFile_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "a", "b", "c", "dst.txt")

	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	if err := CopyFile(src, dst, ""); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	// All three parent directories should exist with at least the parentDirMode bits.
	for _, rel := range []string{"a", "a/b", "a/b/c"} {
		st, err := os.Stat(filepath.Join(dir, rel))
		if err != nil {
			t.Fatalf("stat parent %s: %v", rel, err)
		}
		if !st.IsDir() {
			t.Fatalf("parent %s is not a directory", rel)
		}
		// On macOS/Linux the directory mode reflects (mode &^ umask).
		// Require at least owner-rwx (0o700) — the floor any reasonable umask preserves.
		if st.Mode().Perm()&0o700 != 0o700 {
			t.Fatalf("parent %s perms %o lack owner rwx", rel, st.Mode().Perm())
		}
	}

	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("stat dst: %v", err)
	}
}

func TestCopyFile_RespectsChmodString(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	if err := CopyFile(src, dst, "0600"); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	st, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("stat dst: %v", err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("dst perms: got %#o, want %#o", got, os.FileMode(0o600))
	}
}

func TestCopyFile_RejectsInvalidChmod(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	if err := os.WriteFile(src, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}

	err := CopyFile(src, dst, "not-octal")
	if err == nil {
		t.Fatalf("CopyFile: expected error for invalid chmod, got nil")
	}

	// No file should have been written.
	if _, statErr := os.Stat(dst); !os.IsNotExist(statErr) {
		t.Fatalf("dst should not exist after rejected chmod, stat err: %v", statErr)
	}
}

func TestCopyFile_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "dst.txt")

	if err := os.WriteFile(src, []byte("new content"), 0o644); err != nil {
		t.Fatalf("seed src: %v", err)
	}
	if err := os.WriteFile(dst, []byte("old content"), 0o644); err != nil {
		t.Fatalf("seed dst: %v", err)
	}

	if err := CopyFile(src, dst, ""); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "new content" {
		t.Fatalf("dst content not overwritten:\n  got:  %q\n  want: %q", got, "new content")
	}
}

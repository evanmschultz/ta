package tomlfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomicCreatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	data := []byte("[a]\nx = 1\n")
	if err := WriteAtomic(path, data); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != string(data) {
		t.Errorf("content = %q, want %q", got, data)
	}
}

func TestWriteAtomicReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	if err := os.WriteFile(path, []byte("[old]\nv = 1\n"), 0o644); err != nil {
		t.Fatalf("pre-write: %v", err)
	}
	if err := WriteAtomic(path, []byte("[new]\nv = 2\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "[new]\nv = 2\n" {
		t.Errorf("content = %q", got)
	}
}

func TestWriteAtomicLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.toml")
	if err := WriteAtomic(path, []byte("data\n")); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if n := len(entries); n != 1 {
		names := make([]string, 0, n)
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected one file, got %d: %v", n, names)
	}
}

func TestWriteAtomicRejectsEmptyPath(t *testing.T) {
	if err := WriteAtomic("", []byte("x")); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestWriteAtomicMissingDirErrors(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "does_not_exist", "file.toml")
	if err := WriteAtomic(bogus, []byte("data\n")); err == nil {
		t.Fatal("expected error on missing directory")
	}
}

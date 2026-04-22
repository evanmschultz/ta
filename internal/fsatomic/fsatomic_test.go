package fsatomic_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/evanmschultz/ta/internal/fsatomic"
)

func TestWriteHappyPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := fsatomic.Write(target, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestWriteEmptyPathErrors(t *testing.T) {
	if err := fsatomic.Write("", []byte("x")); err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestWriteOverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := fsatomic.Write(target, []byte("new")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new" {
		t.Errorf("got %q, want new", got)
	}
}

func TestWriteMissingDirErrors(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nope", "out.txt")
	if err := fsatomic.Write(target, []byte("x")); err == nil {
		t.Fatal("expected error writing into non-existent dir")
	}
}

func TestWriteLeavesNoTempOnSuccess(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := fsatomic.Write(target, []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if n := len(entries); n != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("want exactly 1 file (the target), got %d: %v", n, names)
	}
}

package index_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/evanmschultz/ta/internal/index"
)

func TestPathReturnsDotTaIndexToml(t *testing.T) {
	got := index.Path("/abs/proj")
	want := filepath.Join("/abs/proj", ".ta", "index.toml")
	if got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestLoadMissingFileReturnsEmptyIndex(t *testing.T) {
	root := t.TempDir()
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if idx == nil {
		t.Fatal("Load returned nil index without error")
	}
	if idx.FormatVersion != index.FormatVersion {
		t.Errorf("FormatVersion = %d, want %d", idx.FormatVersion, index.FormatVersion)
	}
	if len(idx.Records) != 0 {
		t.Errorf("Records non-empty: %v", idx.Records)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	root := t.TempDir()
	created := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	updated := time.Date(2026, 4, 24, 11, 0, 0, 0, time.UTC)

	idx := &index.Index{
		FormatVersion: index.FormatVersion,
		Records: map[string]index.Entry{
			"phase_1.db.build_task.t1": {Type: "build_task", Created: created, Updated: updated},
			"phase_1.db.build_task.t2": {Type: "build_task", Created: created, Updated: updated},
			"plans.task.task_001":      {Type: "task", Created: created, Updated: updated},
		},
	}
	if err := idx.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.FormatVersion != index.FormatVersion {
		t.Errorf("FormatVersion = %d, want %d", loaded.FormatVersion, index.FormatVersion)
	}
	if got, want := len(loaded.Records), len(idx.Records); got != want {
		t.Fatalf("len(Records) = %d, want %d", got, want)
	}
	for k, want := range idx.Records {
		got, ok := loaded.Records[k]
		if !ok {
			t.Errorf("missing entry %q after round-trip", k)
			continue
		}
		if got.Type != want.Type {
			t.Errorf("entry %q: Type = %q, want %q", k, got.Type, want.Type)
		}
		if !got.Created.Equal(want.Created) {
			t.Errorf("entry %q: Created = %v, want %v", k, got.Created, want.Created)
		}
		if !got.Updated.Equal(want.Updated) {
			t.Errorf("entry %q: Updated = %v, want %v", k, got.Updated, want.Updated)
		}
	}
}

func TestSaveEmitsFormatVersionAtTop(t *testing.T) {
	root := t.TempDir()
	idx := &index.Index{
		FormatVersion: index.FormatVersion,
		Records: map[string]index.Entry{
			"phase_1.db.t1": {
				Type:    "task",
				Created: time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC),
				Updated: time.Date(2026, 4, 24, 10, 30, 0, 0, time.UTC),
			},
		},
	}
	if err := idx.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	buf, err := os.ReadFile(index.Path(root))
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	body := string(buf)
	if !strings.Contains(body, "format_version = 1") {
		t.Errorf("missing format_version scalar in:\n%s", body)
	}
	// format_version must precede any bracket-table header so a future
	// reader can stop reading after it on a version mismatch. go-toml's
	// emitter places top-level scalars before tables.
	versionPos := strings.Index(body, "format_version")
	bracketPos := strings.Index(body, "[")
	if versionPos == -1 {
		t.Fatalf("format_version not found in:\n%s", body)
	}
	if bracketPos != -1 && versionPos > bracketPos {
		t.Errorf("format_version at byte %d is after first bracket at %d:\n%s", versionPos, bracketPos, body)
	}
}

func TestLoadRejectsUnknownFormatVersion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "format_version = 99\n\n[a]\ntype = \"task\"\ncreated = 2026-04-24T10:00:00Z\nupdated = 2026-04-24T10:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := index.Load(root)
	if err == nil {
		t.Fatal("Load: expected error for unknown format_version")
	}
	if !errors.Is(err, index.ErrUnknownFormatVersion) {
		t.Errorf("err = %v, want ErrUnknownFormatVersion", err)
	}
}

func TestLoadRejectsMissingFormatVersion(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "[a]\ntype = \"task\"\ncreated = 2026-04-24T10:00:00Z\nupdated = 2026-04-24T10:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := index.Load(root); err == nil {
		t.Fatal("Load: expected error for missing format_version scalar")
	}
}

func TestLoadParsesNestedTable(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".ta")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	body := "format_version = 1\n\n[phase_1.db.t1]\ntype = \"build_task\"\ncreated = 2026-04-24T10:00:00Z\nupdated = 2026-04-24T10:30:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, "index.toml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	idx, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := len(idx.Records), 1; got != want {
		t.Fatalf("len(Records) = %d, want %d", got, want)
	}
	entry, ok := idx.Records["phase_1.db.t1"]
	if !ok {
		t.Fatalf("missing entry phase_1.db.t1; have: %v", idx.Records)
	}
	if entry.Type != "build_task" {
		t.Errorf("Type = %q, want build_task", entry.Type)
	}
	wantCreated := time.Date(2026, 4, 24, 10, 0, 0, 0, time.UTC)
	if !entry.Created.Equal(wantCreated) {
		t.Errorf("Created = %v, want %v", entry.Created, wantCreated)
	}
}

func TestPutInsertsNewEntryStampingTimestamps(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	before := time.Now().UTC().Add(-time.Second)
	idx.Put("a.b.c", index.Entry{Type: "task"})
	after := time.Now().UTC().Add(time.Second)

	got, ok := idx.Get("a.b.c")
	if !ok {
		t.Fatal("Get: missing after Put")
	}
	if got.Type != "task" {
		t.Errorf("Type = %q, want task", got.Type)
	}
	if got.Created.Before(before) || got.Created.After(after) {
		t.Errorf("Created = %v not in [%v, %v]", got.Created, before, after)
	}
	if got.Updated.Before(before) || got.Updated.After(after) {
		t.Errorf("Updated = %v not in [%v, %v]", got.Updated, before, after)
	}
}

func TestPutPreservesCreatedOnUpdate(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	original := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	idx.Put("a.b.c", index.Entry{Type: "task", Created: original, Updated: original})

	// Caller passes a different Created — Put must ignore it.
	bogus := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	idx.Put("a.b.c", index.Entry{Type: "task", Created: bogus})

	got, _ := idx.Get("a.b.c")
	if !got.Created.Equal(original) {
		t.Errorf("Created = %v, want %v (preserved)", got.Created, original)
	}
	if !got.Updated.After(original) {
		t.Errorf("Updated = %v, expected to be after original %v", got.Updated, original)
	}
}

func TestGetMissingReturnsFalse(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	if _, ok := idx.Get("nope"); ok {
		t.Error("Get on missing key returned ok=true")
	}
}

func TestDeleteRemovesEntry(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	idx.Put("a.b.c", index.Entry{Type: "task"})
	idx.Delete("a.b.c")
	if _, ok := idx.Get("a.b.c"); ok {
		t.Error("Delete left entry behind")
	}
}

func TestDeleteMissingIsNoOp(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	idx.Delete("nope") // must not panic
}

func TestWalkVisitsInSortedOrder(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	keys := []string{"c.x", "a.x", "b.x"}
	for _, k := range keys {
		idx.Put(k, index.Entry{Type: "task"})
	}
	var visited []string
	idx.Walk(func(canonical string, _ index.Entry) bool {
		visited = append(visited, canonical)
		return true
	})
	want := []string{"a.x", "b.x", "c.x"}
	if len(visited) != len(want) {
		t.Fatalf("visited %d, want %d", len(visited), len(want))
	}
	for i, k := range want {
		if visited[i] != k {
			t.Errorf("visited[%d] = %q, want %q", i, visited[i], k)
		}
	}
}

func TestWalkStopsOnFalseReturn(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	idx.Put("a", index.Entry{Type: "task"})
	idx.Put("b", index.Entry{Type: "task"})
	idx.Put("c", index.Entry{Type: "task"})

	var visited int
	idx.Walk(func(canonical string, _ index.Entry) bool {
		visited++
		return canonical != "b"
	})
	if visited != 2 {
		t.Errorf("Walk visited %d items, want 2 (stop after b)", visited)
	}
}

func TestSaveLeavesNoTempFile(t *testing.T) {
	root := t.TempDir()
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{
		"a.b": {Type: "task"},
	}}
	if err := idx.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".ta"))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected 1 file in .ta/, got %d: %v", len(entries), names)
	}
	if entries[0].Name() != "index.toml" {
		t.Errorf("file = %q, want index.toml", entries[0].Name())
	}
}

func TestConcurrentSaveProducesValidFile(t *testing.T) {
	// Best-effort: many goroutines Save the same Index. fsatomic's
	// rename idiom guarantees the file always reflects ONE complete
	// payload (no partial writes), even if which payload "wins" is
	// non-deterministic. We assert the post-condition: the file is
	// always parseable and round-trips.
	root := t.TempDir()
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	idx.Put("a.b.c", index.Entry{Type: "task"})

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = idx.Save(root)
		}()
	}
	wg.Wait()

	loaded, err := index.Load(root)
	if err != nil {
		t.Fatalf("Load after concurrent Save: %v", err)
	}
	if _, ok := loaded.Records["a.b.c"]; !ok {
		t.Errorf("entry a.b.c missing after concurrent Save: %v", loaded.Records)
	}
}

func TestSaveCreatesDotTaDir(t *testing.T) {
	root := t.TempDir()
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	if err := idx.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(filepath.Join(root, ".ta"))
	if err != nil {
		t.Fatalf("stat .ta: %v", err)
	}
	if !info.IsDir() {
		t.Error(".ta exists but is not a directory")
	}
}

func TestSaveRejectsUnknownFormatVersion(t *testing.T) {
	root := t.TempDir()
	idx := &index.Index{FormatVersion: 42, Records: map[string]index.Entry{}}
	err := idx.Save(root)
	if err == nil {
		t.Fatal("Save: expected error for unknown format_version")
	}
	if !errors.Is(err, index.ErrUnknownFormatVersion) {
		t.Errorf("err = %v, want ErrUnknownFormatVersion", err)
	}
}

func TestSaveZeroFormatVersionDefaultsToCurrent(t *testing.T) {
	root := t.TempDir()
	idx := &index.Index{Records: map[string]index.Entry{}}
	idx.Put("a.b", index.Entry{Type: "task"})
	if err := idx.Save(root); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if idx.FormatVersion != index.FormatVersion {
		t.Errorf("FormatVersion = %d, want %d (defaulted)", idx.FormatVersion, index.FormatVersion)
	}
}

func TestPutNormalizesTimestampsToUTC(t *testing.T) {
	idx := &index.Index{FormatVersion: index.FormatVersion, Records: map[string]index.Entry{}}
	loc, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Skipf("LoadLocation: %v", err)
	}
	t1 := time.Date(2026, 4, 24, 10, 0, 0, 0, loc)
	idx.Put("a.b", index.Entry{Type: "task", Created: t1, Updated: t1})

	got, _ := idx.Get("a.b")
	if got.Created.Location() != time.UTC {
		t.Errorf("Created.Location = %v, want UTC", got.Created.Location())
	}
	if got.Updated.Location() != time.UTC {
		t.Errorf("Updated.Location = %v, want UTC", got.Updated.Location())
	}
}

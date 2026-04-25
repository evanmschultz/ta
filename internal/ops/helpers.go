package ops

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
)

// spliceOut returns buf with the bytes in rng removed. Atomic-write
// safe: caller writes the returned buffer to disk under WriteAtomic.
func spliceOut(buf []byte, rng [2]int) []byte {
	out := make([]byte, 0, len(buf)-(rng[1]-rng[0]))
	out = append(out, buf[:rng[0]]...)
	out = append(out, buf[rng[1]:]...)
	return out
}

// readFileIfExists returns the file bytes or nil if the file does not
// exist. Any other error is returned as-is.
func readFileIfExists(path string) ([]byte, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return buf, nil
}

// validationPath rebuilds the "<db>.<type>.<id>" form schema.Validate
// expects from a resolved Address. The validation form is internal and
// independent of the address grammar — it is the pre-9.1 address shape
// used by Validate's two-segment lookup. Phase 9.2 derives it from the
// resolved Address rather than slicing raw segments.
func validationPath(addr db.Address) string {
	parts := []string{addr.DBName, addr.Type}
	if addr.ID != "" {
		parts = append(parts, addr.ID)
	}
	return joinDot(parts)
}

// tomlRelPathForFields returns the backend-relative record path for
// use by extractTOMLFields. Single-file mounts embed the db name in
// every bracket path on disk (legacy `[plans.task.t1]` form), so the
// rel path is `<db>.<type>.<id>`. Multi-file mounts emit bare brackets
// (`[build_task.task_001]`) so the rel path is `<type>.<id>`.
func tomlRelPathForFields(addr db.Address) string {
	base := addr.Type
	if addr.ID != "" {
		base += "." + addr.ID
	}
	if addr.SingleFileMount {
		return addr.DBName + "." + base
	}
	return base
}

// joinDot joins non-empty segments with '.'.
func joinDot(parts []string) string {
	out := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if out == "" {
			out = p
			continue
		}
		out += "." + p
	}
	return out
}

// verifyTypeAgainstAddress checks the caller-supplied typeName against
// the type segment carried by the parsed address. PLAN §12.17.9 Phase
// 9.4: when both are present and disagree, surface ErrTypeMismatch. An
// empty typeName is permitted on read-side ops (Get / Update / Delete /
// Search) — Create's required-type guard fires upstream of this helper.
func verifyTypeAgainstAddress(typeName string, addr db.Address) error {
	if typeName == "" {
		return nil
	}
	if typeName != addr.Type {
		return fmt.Errorf(
			"%w: --type %q disagrees with address type segment %q in %q",
			ErrTypeMismatch, typeName, addr.Type, addr.Canonical())
	}
	return nil
}

// verifyTypeAgainstIndex consults `.ta/index.toml` and surfaces
// ErrIndexMismatch when its recorded type for the canonical address
// disagrees with the address-resolved type. Missing-from-index is NOT
// an error — Phase 9.4 keeps the address grammar carrying the type
// segment, so an empty / partial / not-yet-rebuilt index is tolerated.
// Index load failures other than missing-file propagate wrapped.
func verifyTypeAgainstIndex(projectRoot string, addr db.Address) error {
	idx, err := index.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("ops: load index: %w", err)
	}
	canonical := addr.Canonical()
	entry, ok := idx.Get(canonical)
	if !ok {
		return nil
	}
	if entry.Type != addr.Type {
		return fmt.Errorf(
			"%w: index records type %q for %q but address resolves to %q (run `ta index rebuild`)",
			ErrIndexMismatch, entry.Type, canonical, addr.Type)
	}
	return nil
}

// writeIndexEntry upserts the canonical address into `.ta/index.toml`
// after a successful Create / Update. typeName is the authoritative
// type to record; on update we pass addr.Type so the index never
// silently drifts from the address grammar. The disk write succeeded
// already by the time this helper runs — any error here is a recovery
// hint, not a transactional rollback target. Callers wrap the failure
// so the user sees both the disk-success and the index-failure facts.
func writeIndexEntry(projectRoot string, addr db.Address, typeName string) error {
	idx, err := index.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("ops: load index: %w (record on disk; run `ta index rebuild`)", err)
	}
	now := time.Now().UTC()
	idx.Put(addr.Canonical(), index.Entry{
		Type:    typeName,
		Created: now,
		Updated: now,
	})
	if err := idx.Save(projectRoot); err != nil {
		return fmt.Errorf("ops: save index: %w (record on disk; run `ta index rebuild`)", err)
	}
	return nil
}

// deleteIndexEntry removes the canonical address from `.ta/index.toml`
// after a successful Delete. A missing entry is a no-op (Index.Delete
// already tolerates the absent case). Same recovery-hint shape as
// writeIndexEntry — disk truth has already changed; we surface but
// don't roll back on index-write failures.
func deleteIndexEntry(projectRoot string, addr db.Address) error {
	idx, err := index.Load(projectRoot)
	if err != nil {
		// A missing or unreadable index here is not fatal: the disk
		// delete succeeded. Tolerate format-version errors during the
		// migration window (Phase 9.4 callers may run against projects
		// whose index has not been rebuilt yet) by surfacing the load
		// failure but not blocking the success path. Match other ops
		// that lean on the index opportunistically.
		if errors.Is(err, index.ErrUnknownFormatVersion) {
			return nil
		}
		return fmt.Errorf("ops: load index: %w (record removed from disk; run `ta index rebuild`)", err)
	}
	idx.Delete(addr.Canonical())
	if err := idx.Save(projectRoot); err != nil {
		return fmt.Errorf("ops: save index: %w (record removed from disk; run `ta index rebuild`)", err)
	}
	return nil
}

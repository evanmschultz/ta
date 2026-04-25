package ops

import (
	"fmt"
	"os"

	"github.com/evanmschultz/ta/internal/db"
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

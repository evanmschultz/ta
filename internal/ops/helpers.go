package ops

import (
	"fmt"
	"os"
	"strings"

	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/schema"
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

// validationPath adapts a full address to the "<db>.<type>..." form
// schema.Validate expects (its signature pre-dates multi-instance).
// For multi-instance addresses we rebuild "<db>.<type>..." by stripping
// the <instance> segment.
func validationPath(reg schema.Registry, section string) string {
	segs := strings.Split(section, ".")
	if len(segs) < 2 {
		return section
	}
	dbDecl, ok := reg.DBs[segs[0]]
	if !ok {
		return section
	}
	if dbDecl.Shape == schema.ShapeFile {
		return section
	}
	if len(segs) < 3 {
		return section
	}
	// drop <instance> — segs[1]
	rebuilt := make([]string, 0, len(segs)-1)
	rebuilt = append(rebuilt, segs[0])
	rebuilt = append(rebuilt, segs[2:]...)
	return strings.Join(rebuilt, ".")
}

// tomlRelPathForFields returns the backend-relative record path for
// use by extractTOMLFields. For single-instance TOML the map key path
// is "<db>.<type>.<id>"; for multi-instance it is "<type>.<id>" (the
// file carries only the type and below).
func tomlRelPathForFields(dbDecl schema.DB, addr db.Address) string {
	base := addr.Type
	if addr.ID != "" {
		base += "." + addr.ID
	}
	if dbDecl.Shape == schema.ShapeFile {
		return dbDecl.Name + "." + base
	}
	return base
}

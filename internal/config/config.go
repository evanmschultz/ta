package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/evanmschultz/ta/internal/schema"
)

// ErrNoSchema is returned when the project-local .ta/schema.toml is
// missing.
var ErrNoSchema = errors.New("no .ta/schema.toml found in project directory")

// SchemaFileName is the on-disk name of the schema file, relative to
// a .ta/ directory.
const SchemaFileName = "schema.toml"

// SchemaDirName is the directory name that holds the schema file.
const SchemaDirName = ".ta"

// Resolution is the resolved schema for a project. Sources lists the
// single project-local schema file that contributed (always exactly
// one entry on success). Registry is the loaded schema tree.
//
// Post-V2-PLAN §12.11: the home-layer cascade and ancestor walk are
// gone. The runtime reads one file and only one file — the project's
// own .ta/schema.toml.
type Resolution struct {
	Sources  []string
	Registry schema.Registry
}

// Resolve loads the schema registry for projectPath. It reads exactly
// one file, <projectPath>/.ta/schema.toml, with no ancestor walk and
// no home-layer fallback (V2-PLAN §12.11 / §14.2).
//
// Returns ErrNoSchema when the file is absent. Malformed schema files
// surface their parse error wrapped in context, never ErrNoSchema.
func Resolve(projectPath string) (Resolution, error) {
	abs, err := filepath.Abs(projectPath)
	if err != nil {
		return Resolution{}, fmt.Errorf("config: absolute path for %q: %w", projectPath, err)
	}
	schemaPath := filepath.Join(abs, SchemaDirName, SchemaFileName)

	reg, err := loadSchema(schemaPath)
	if err != nil {
		return Resolution{}, err
	}
	return Resolution{Sources: []string{schemaPath}, Registry: reg}, nil
}

// loadSchema opens schemaPath and parses the registry. Returns
// ErrNoSchema when the file does not exist; any other I/O or parse
// error is wrapped with context.
func loadSchema(path string) (schema.Registry, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return schema.Registry{}, ErrNoSchema
		}
		return schema.Registry{}, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	reg, err := schema.Load(f)
	if err != nil {
		return schema.Registry{}, fmt.Errorf("config: %s: %w", path, err)
	}
	return reg, nil
}

package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/evanmschultz/ta/internal/schema"
)

// ErrNoConfig is returned when no project-local or home-directory config is
// found while resolving a schema for a given file path.
var ErrNoConfig = errors.New("no .ta/config.toml found in project tree or home directory")

// ConfigFileName is the on-disk name of the schema config file, relative to
// a .ta/ directory.
const ConfigFileName = "config.toml"

// ConfigDirName is the directory name that holds the schema config.
const ConfigDirName = ".ta"

// Resolution is a fully-resolved schema config: the absolute path of the
// config file that won the walk-up, and the Registry it produced.
type Resolution struct {
	Path     string
	Registry schema.Registry
}

// Resolve walks up from filePath's directory looking for .ta/config.toml,
// then falls back to ~/.ta/config.toml. The first file found wins — closer
// configs supersede more distant ones. If neither path yields a config,
// Resolve returns ErrNoConfig.
func Resolve(filePath string) (Resolution, error) {
	abs, err := filepath.Abs(filePath)
	if err != nil {
		return Resolution{}, fmt.Errorf("config: absolute path for %q: %w", filePath, err)
	}

	dir := filepath.Dir(abs)
	for {
		candidate := filepath.Join(dir, ConfigDirName, ConfigFileName)
		res, ok, err := loadIfExists(candidate)
		if err != nil {
			return Resolution{}, err
		}
		if ok {
			return res, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return Resolution{}, fmt.Errorf("config: locate home directory: %w", err)
	}
	homeCandidate := filepath.Join(home, ConfigDirName, ConfigFileName)
	res, ok, err := loadIfExists(homeCandidate)
	if err != nil {
		return Resolution{}, err
	}
	if ok {
		return res, nil
	}
	return Resolution{}, ErrNoConfig
}

func loadIfExists(path string) (Resolution, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return Resolution{}, false, nil
		}
		return Resolution{}, false, fmt.Errorf("config: open %s: %w", path, err)
	}
	defer f.Close()

	reg, err := schema.Load(f)
	if err != nil {
		return Resolution{}, false, fmt.Errorf("config: %s: %w", path, err)
	}
	return Resolution{Path: path, Registry: reg}, true, nil
}

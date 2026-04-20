package tomlfile

import (
	gts "github.com/odvcencio/gotreesitter"
)

// Section describes a TOML section (or array-of-tables entry) within a file,
// with byte ranges that make surgical splicing possible.
type Section struct {
	// Path is the bracketed path, e.g. "task.task_001" for [task.task_001].
	Path string
	// HeaderRange is [start, end) bytes of the header line including newline.
	HeaderRange [2]int
	// BodyRange is [start, end) bytes of the whole section including its
	// header and trailing content up to the next section header or EOF.
	BodyRange [2]int
	// ArrayOfTables is true if the section was declared with [[...]].
	ArrayOfTables bool
}

// File is a parsed TOML file: the raw byte buffer plus discovered sections.
// The zero value is not useful; construct via Parse.
type File struct {
	Path     string
	Buf      []byte
	Sections []Section
}

// Parse reads and parses the file at path. Phase 4 lands the tree-sitter
// walk; this stub anchors the gotreesitter dependency.
func Parse(path string) (*File, error) {
	_ = gts.NewParser // anchor the dependency until Phase 4 lands the real body
	return &File{Path: path}, nil
}

// Package dotta describes the on-disk shape of a `.ta/` dotta tree
// rooted at some absolute path. A Tree is the in-memory projection of
// what an enumerator (forthcoming) saw when walking the root: top-level
// files, the recognised subtree directories with their per-subtree
// destination/mapping metadata, and any entries that were intentionally
// skipped (with the reason so callers can report them).
//
// Types here are intentionally inert data containers — no I/O, no
// validation logic. The enumerator and apply layers build on these
// shapes; ExpandTilde lives alongside them as the only shared path
// utility the package needs today.
package dotta

import "io/fs"

// Tree is the enumerated view of a `.ta/` root.
type Tree struct {
	// Root is the absolute, cleaned path of the dotta root directory.
	Root string

	// RootFiles are the regular files that live directly under Root
	// (non-recursive). Subdirectories are surfaced via Subtrees.
	RootFiles []FileMeta

	// Subtrees are the recognised first-level directories under Root,
	// each carrying its own mapping metadata + recursively enumerated
	// files.
	Subtrees []Subtree

	// Skipped collects entries the enumerator chose not to descend
	// into (symlinks, unreadable nodes, irregular files). Each entry
	// records its path relative to Root and the reason it was skipped.
	Skipped []SkippedEntry
}

// Subtree is one recognised first-level directory under a Tree.Root.
type Subtree struct {
	// Name is the directory's basename (e.g. "claude_hooks").
	Name string

	// AbsPath is the absolute path of the subtree directory on disk.
	AbsPath string

	// RelPath is the subtree path relative to Tree.Root.
	RelPath string

	// Mapping captures how this subtree projects onto the target
	// install location (destination + conflict policy).
	Mapping Mapping

	// Files are the files enumerated under this subtree.
	Files []FileMeta
}

// Mapping is the per-subtree projection metadata: where this subtree's
// contents land on the install target and what to do if a file already
// exists there.
//
// Field tags pin the on-disk TOML keys to snake_case (destination,
// on_conflict) so a mapping.toml authored by a human in the natural
// idiom round-trips through pelletier/go-toml/v2 without surprises.
type Mapping struct {
	// Destination is the resolved destination path (may contain a
	// leading `~` until ExpandTilde has run, depending on the caller).
	Destination string `toml:"destination"`

	// OnConflict is one of the OnConflict* string constants below.
	OnConflict string `toml:"on_conflict"`
}

// FileMeta is a single enumerated file's metadata.
type FileMeta struct {
	// Name is the file's basename.
	Name string

	// AbsPath is the absolute path of the file on disk.
	AbsPath string

	// RelPath is the file's path relative to Tree.Root.
	RelPath string

	// Size is the byte size reported by the OS at enumeration time.
	Size int64

	// Mode is the file mode as reported by the OS at enumeration time.
	Mode fs.FileMode
}

// SkippedEntry records one entry the enumerator did not descend into.
type SkippedEntry struct {
	// RelPath is the entry's path relative to Tree.Root.
	RelPath string

	// Reason is one of the SkipReason* string constants below.
	Reason string
}

// OnConflict policy constants. Stored as plain strings so they survive
// TOML round-trips without bespoke (un)marshalling.
const (
	// OnConflictSkip leaves the existing destination file untouched
	// and records the source file as skipped.
	OnConflictSkip = "skip"

	// OnConflictOverwrite replaces the destination file with the
	// source file's contents.
	OnConflictOverwrite = "overwrite"

	// OnConflictMerge invokes the merge handler for the file type
	// (e.g. JSON deep-merge for settings fragments).
	OnConflictMerge = "merge"

	// OnConflictPrompt asks the user interactively. Non-TTY callers
	// must treat this as an error condition, not a silent fallback.
	OnConflictPrompt = "prompt"
)

// SkipReason constants. The enumerator must populate
// SkippedEntry.Reason with exactly one of these values so callers can
// report stable categories.
const (
	// SkipReasonSymlink indicates the entry was a symbolic link.
	// Dotta never follows symlinks during enumeration.
	SkipReasonSymlink = "symlink"

	// SkipReasonUnreadable indicates a stat or open call failed for
	// a reason other than permission denied (e.g. I/O error).
	SkipReasonUnreadable = "unreadable"

	// SkipReasonPermissionDenied indicates the OS refused access to
	// the entry (EACCES / EPERM).
	SkipReasonPermissionDenied = "permission-denied"

	// SkipReasonIrregular indicates the entry was neither a regular
	// file nor a directory (e.g. device node, socket, FIFO).
	SkipReasonIrregular = "irregular"
)

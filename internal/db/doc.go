// Package db resolves an id against a schema.Registry and a project
// root, returning the on-disk file that backs the addressed record.
// See PLAN §12.17.9 F10 for the id grammar and locked decisions.
//
// The id grammar (F10) is:
//
//	id := <FileRelPath>.<BracketKey>
//
// FileRelPath is the dotted-path equivalent of the on-disk file's
// path-relative-to-its-mount-static-prefix (extension stripped, `/`
// replaced with `.`). BracketKey is the bracket-tail-after-file-relpath
// — the rest of the dotted id beyond the file-relpath segments. The id
// is what users / agents pass; it is what `cat <file>.toml` shows as
// the bracket header. Type does NOT live in the id — it lives in the
// runtime index.
//
// Resolution rules:
//
//   - The Registry's dbs are tried in stable name order; for each db,
//     each Paths entry is matched against the id.
//   - A mount entry is split into a static prefix and residual segments
//     (the `*`-or-literal sequence after the static prefix). Id
//     segments are matched left-to-right against residual segments;
//     `*` matches any non-empty segment, literals require equality.
//     The matched prefix yields FileRelPath; everything after yields
//     BracketKey.
//   - Globs (`*`) match one path segment per occurrence and skip
//     dotfiles.
//   - `~/...` mounts expand against the user's home directory.
//   - Trailing-slash collection mounts (`docs/`) and the `.`
//     project-root mount are rejected at schema-load time
//     (ErrCollectionMountUnsupported); use globs instead
//     (`docs/*.md`).
//
// The resolver is lang-agnostic: it never imports any backend package.
// It hands back the schema.DB, the Resolved view, and the absolute
// file path; callers are responsible for reading the file and handing
// its bytes to the correct record.Backend.
//
// Fail-loudly contract: empty id, leading/trailing/empty segments, and
// missing-bracket-key ids error with ErrBadID. Id that does not bind
// to any registered db's mount → ErrIDDoesNotMatchAnyDB.
package db

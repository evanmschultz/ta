// Package db resolves a dotted section address against a schema.Registry
// and a project root, returning the on-disk file that backs the addressed
// record. See V2-PLAN.md §5.5 and PLAN §12.17.9 for the addressing spec.
//
// The Phase 9.2 grammar is uniform across formats:
//
//	<file-relpath>.<type>.<id-tail>
//
// FileRelPath is the dotted-path equivalent of the on-disk file's
// path-relative-to-its-mount-static-prefix (extension stripped, `/`
// replaced with `.`). Type is the record-type segment (which moves to
// a `--type` flag in Phase 9.4). ID-tail is one or more dot-joined
// segments forming the bracket / heading-chain inside the file.
//
// Resolution rules:
//
//   - The Registry's dbs are tried in stable name order; for each db,
//     each Paths entry is matched against the address. Non-collection
//     mounts (single-file or glob) are tried before collection mounts
//     so a specific match always beats a catch-all root.
//   - A mount entry is split into a static prefix (everything up to
//     the slash before the first `*`, or up to the last segment for
//     non-glob mounts) and residual segments. Address segments are
//     matched left-to-right against residual segments; `*` matches any
//     non-empty segment, literals require equality. The matched prefix
//     yields FileRelPath; the next segment yields Type; the remainder
//     yields ID.
//   - Collection mounts (trailing `/` or `.`) recurse: any descendant
//     file with the format extension produces an Instance, with the
//     dotted file-relpath as its slug.
//   - Globs (`*`) match one path segment per occurrence and skip
//     dotfiles.
//   - `~/...` mounts expand against the user's home directory.
//
// The resolver is lang-agnostic: it never imports any backend package.
// It hands back the schema.DB, the resolved instance, and the absolute
// file path; callers are responsible for reading the file and handing
// its bytes to the correct record.Backend.
//
// Fail-loudly contract (§1.1): empty address, leading/trailing/empty
// segments, and missing-id-tail addresses error with ErrBadAddress.
// Unknown db (no mount matches) → ErrUnknownDB. Type segment not
// declared on the resolved db → ErrUnknownType.
package db

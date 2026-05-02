package db

import "errors"

// Sentinel errors returned by the resolver. Callers use errors.Is to
// branch on these; the wrapped error carries the concrete id, db name,
// and paths for human-readable messages.
var (
	// ErrIDDoesNotMatchAnyDB is returned when no db's mount accepts the
	// id. Was ErrUnknownDB pre-F10. Naming reflects the locked id model
	// (PLAN §12.17.9 F10): the id is the user-facing handle; failing to
	// resolve it means it does not bind to any registered db.
	ErrIDDoesNotMatchAnyDB = errors.New("db: id does not match any db")

	// ErrUnknownType is returned when a caller-supplied type does not
	// match any declared record type on the resolved db.
	ErrUnknownType = errors.New("db: unknown type")

	// ErrBadID is returned when the id has the wrong shape for any
	// resolved db's mount, or is empty / has empty segments. Was
	// ErrBadAddress pre-F10.
	ErrBadID = errors.New("db: malformed id")

	// ErrInstanceNotFound is returned by ResolveRead when the named
	// file does not exist on disk (no matching backing file under any
	// of the db's mounts).
	ErrInstanceNotFound = errors.New("db: file not found")

	// ErrSlugCollision is returned when two distinct filesystem paths
	// produce the same slug for a multi-file glob db. The wrapping
	// error text includes both paths.
	ErrSlugCollision = errors.New("db: slug collision")

	// ErrPathHintMismatch is returned by ResolveWrite when the caller
	// supplies a path_hint that disagrees with the existing instance's
	// on-disk location. F10: path-hint is rejected outright; the id
	// derives the target file path.
	ErrPathHintMismatch = errors.New("db: path_hint mismatch")
)

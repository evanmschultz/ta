package db

import "errors"

// Sentinel errors returned by the resolver. Callers use errors.Is to
// branch on these; the wrapped error carries the concrete address, db
// name, and paths for human-readable messages.
var (
	// ErrUnknownDB is returned when the first address segment does not
	// match any registered db.
	ErrUnknownDB = errors.New("db: unknown db")

	// ErrUnknownType is returned when the type segment does not match
	// any declared record type on the resolved db.
	ErrUnknownType = errors.New("db: unknown type")

	// ErrBadAddress is returned when the address has the wrong number
	// of segments for the resolved db's shape, or is empty.
	ErrBadAddress = errors.New("db: malformed address")

	// ErrInstanceNotFound is returned by ResolveRead when the named
	// instance does not exist on disk (no canonical db file in the
	// dir-per-instance case; no matching file in the collection case).
	ErrInstanceNotFound = errors.New("db: instance not found")

	// ErrSlugCollision is returned when two distinct filesystem paths
	// produce the same slug for a file-per-instance db. The wrapping
	// error text includes both paths per §5.5.2.
	ErrSlugCollision = errors.New("db: slug collision")

	// ErrPathHintMismatch is returned by ResolveWrite when the caller
	// supplies a path_hint that disagrees with the existing instance's
	// on-disk location. Changing a path is a manual rename, not a tool
	// operation (§5.5.2).
	ErrPathHintMismatch = errors.New("db: path_hint mismatch")

	// ErrUnsupportedShape is returned when the resolver encounters a
	// shape value it does not know how to handle. Should never fire in
	// practice once the schema loader validates shapes.
	ErrUnsupportedShape = errors.New("db: unsupported shape")
)

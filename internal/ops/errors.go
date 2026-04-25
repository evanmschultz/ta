package ops

import "errors"

// Sentinel errors returned by the data and schema tool handlers. Callers
// branch on these with errors.Is; the wrapped error text carries the
// concrete address, db name, or file path for diagnostics.
//
// These sentinels are exposed at package scope rather than hidden behind
// the handlers so CLI tests and future callers (e.g. a direct Go API)
// can branch on them without reparsing human-readable messages.
var (
	// ErrRecordExists is returned by create when the target record
	// already exists in the backing file. §3.4.
	ErrRecordExists = errors.New("ops: record already exists")

	// ErrRecordNotFound is returned by delete (record-level) when the
	// target address resolves to no section in the file. Read-time
	// equivalent of the create uniqueness guard.
	ErrRecordNotFound = errors.New("ops: record not found")

	// ErrFileNotFound is returned by update when the backing file does
	// not exist. §3.5: update "fails if the file doesn't exist".
	ErrFileNotFound = errors.New("ops: file not found")

	// ErrAmbiguousDelete is returned by delete when the caller names a
	// whole multi-instance db without picking an instance. §3.6.
	ErrAmbiguousDelete = errors.New("ops: ambiguous delete on multi-instance db")

	// ErrReservedName is returned by schema(action=create|update|delete)
	// when name targets a reserved identifier such as "ta_schema". The
	// meta-schema lives in the binary and is not user-mutable.
	ErrReservedName = errors.New("ops: reserved name")

	// ErrMetaSchemaViolation is returned by any schema mutation whose
	// post-mutation bytes fail schema.LoadBytes re-validation. The
	// on-disk bytes are left untouched (atomic rollback per §4.6).
	ErrMetaSchemaViolation = errors.New("ops: meta-schema violation")

	// ErrTypeHasRecords is returned by schema(action=delete, kind=type)
	// when at least one record of the target type exists on disk.
	// §3.3 delete semantics.
	ErrTypeHasRecords = errors.New("ops: type still has records on disk")

	// ErrDBHasData is returned by schema(action=delete, kind=db) when
	// any data file for the target db still exists on disk. §3.3.
	ErrDBHasData = errors.New("ops: db still has data on disk")

	// ErrUnknownSchemaTarget is returned by schema(action=update|delete)
	// when name does not resolve to an existing db / type / field.
	ErrUnknownSchemaTarget = errors.New("ops: schema target not found")

	// ErrUnknownField is returned by get when fields names a field that
	// is not declared on the target type.
	ErrUnknownField = errors.New("ops: unknown field")

	// ErrUnsupportedFormat is returned by the backend factory when a db
	// declares a format no backend implements. Should never fire in
	// practice once the schema loader validates formats.
	ErrUnsupportedFormat = errors.New("ops: unsupported format")

	// ErrCannotClearRequired is returned by Update (PATCH semantics,
	// §3.5) when the caller passes {"<field>": null} on a field that is
	// declared required and has no schema default. Required fields
	// cannot be unset via Update; change the schema or delete +
	// recreate the record.
	ErrCannotClearRequired = errors.New("ops: cannot clear required field")

	// ErrTypeMismatch is returned by Create / Update / Get / Delete /
	// Search when the caller supplies a `--type` (or `typeName`) that
	// disagrees with the type segment carried by the address, OR when
	// the supplied type disagrees with the index's recorded type for
	// the same canonical address. PLAN §12.17.9 Phase 9.4: `--type` is
	// the orthogonal authoritative source; mismatches with the address
	// or the index fail loudly rather than silently picking one.
	ErrTypeMismatch = errors.New("ops: type mismatch")

	// ErrIndexMismatch is returned by read paths when the index entry
	// for a canonical address disagrees with on-disk reality (e.g. the
	// address parses cleanly with type T1 but the index records type
	// T2 for the same canonical key). PLAN §12.17.9 Phase 9.4
	// trust-and-fail-loud doctrine: callers branch on the sentinel and
	// nudge the user toward `ta index rebuild` to reconcile. Missing-
	// from-index is NOT this error — Phase 9.4 keeps the address
	// grammar carrying the type segment, so an empty / partial index
	// is tolerated.
	ErrIndexMismatch = errors.New("ops: index mismatch")
)

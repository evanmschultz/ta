package ops

import "errors"

// Sentinel errors returned by the data and schema tool handlers.
var (
	// ErrRecordExists is returned by create when the target record
	// already exists in the backing file.
	ErrRecordExists = errors.New("ops: record already exists")

	// ErrRecordNotFound is returned by delete (record-level) when the
	// target id resolves to no section in the file.
	ErrRecordNotFound = errors.New("ops: record not found")

	// ErrFileNotFound is returned by update when the backing file does
	// not exist.
	ErrFileNotFound = errors.New("ops: file not found")

	// ErrAmbiguousDelete is returned by delete when the caller names a
	// scope-prefix id rather than a single record.
	ErrAmbiguousDelete = errors.New("ops: ambiguous delete on scope-prefix id")

	// ErrReservedName is returned by schema(action=create|update|delete)
	// when name targets a reserved identifier such as "ta_schema".
	ErrReservedName = errors.New("ops: reserved name")

	// ErrMetaSchemaViolation is returned by any schema mutation whose
	// post-mutation bytes fail schema.LoadBytes re-validation.
	ErrMetaSchemaViolation = errors.New("ops: meta-schema violation")

	// ErrTypeHasRecords is returned by schema(action=delete, kind=type)
	// when at least one record of the target type exists on disk.
	ErrTypeHasRecords = errors.New("ops: type still has records on disk")

	// ErrDBHasData is returned by schema(action=delete, kind=db) when
	// any data file for the target db still exists on disk.
	ErrDBHasData = errors.New("ops: db still has data on disk")

	// ErrUnknownSchemaTarget is returned by schema(action=update|delete)
	// when name does not resolve to an existing db / type / field.
	ErrUnknownSchemaTarget = errors.New("ops: schema target not found")

	// ErrUnknownField is returned by get when fields names a field that
	// is not declared on the target type.
	ErrUnknownField = errors.New("ops: unknown field")

	// ErrUnsupportedFormat is returned by the backend factory when a db
	// declares a format no backend implements.
	ErrUnsupportedFormat = errors.New("ops: unsupported format")

	// ErrCannotClearRequired is returned by Update (PATCH semantics)
	// when the caller passes {"<field>": null} on a field that is
	// declared required and has no schema default.
	ErrCannotClearRequired = errors.New("ops: cannot clear required field")

	// ErrTypeMismatch is returned by Create / Update / Get / Delete /
	// Search when the caller-supplied type disagrees with the index's
	// recorded type for the same canonical id, OR (on Create) when the
	// type argument is empty.
	ErrTypeMismatch = errors.New("ops: type mismatch")

	// ErrTypeUnresolved is returned when a record exists on disk but
	// has no index entry — the type cannot be resolved. Remediation:
	// `ta index rebuild`. Per F10 (PLAN §12.17.9).
	ErrTypeUnresolved = errors.New("ops: type unresolved (run `ta index rebuild`)")

	// ErrIndexMissing is returned when `.ta/index.toml` is absent.
	// Remediation: `ta index rebuild` to create it. Per F10.
	ErrIndexMissing = errors.New("ops: index missing (run `ta index rebuild`)")

	// ErrTypeNotQualified is returned when a caller passes a bare-slug
	// type name (e.g. `task`) where the contract requires the
	// db-qualified form (`plans.task`). Per F10.
	ErrTypeNotQualified = errors.New("ops: type must be db-qualified (e.g. `plans.task`)")
)

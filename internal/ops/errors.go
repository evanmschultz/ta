package ops

import "errors"

// ErrRecordNotFoundFormat is the wrap shape for ErrRecordNotFound errors.
// Contractually locked for cascade drop_002 — L2-A tests reference this name directly.
const ErrRecordNotFoundFormat = "%w: %q in %s"

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

	// ErrUnscopedGlobDelete is returned by delete when an id resolves
	// (via glob expansion of a db's paths slice) to multiple concrete
	// files. Per F19 (and the post-F10 paths-slice model), file-level
	// delete is allowed when the id uniquely identifies one concrete
	// file; an id that matches multiple files refuses with this error.
	// The caller must narrow the id to one specific file.
	ErrUnscopedGlobDelete = errors.New("ops: id matches multiple files")

	// ErrFileDeleteRequiresForce is returned by delete when the caller
	// names a file-level id (one whole file) without setting Force=true
	// in DeleteOptions. The CLI surfaces this off-TTY (where the
	// confirm prompt cannot fire); the MCP delete tool surfaces it whenever
	// `force=true` is omitted (no TTY is ever available on the MCP
	// transport).
	ErrFileDeleteRequiresForce = errors.New("ops: file-level delete requires --force / force=true")

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

	// ErrBaseStillReferenced is returned by schema(action=delete,
	// kind=base) when the target base is still referenced by any
	// concrete type or other base via `extends`. The error message
	// names every referrer so the caller can break the chain
	// deliberately. Per F22 the wire surface for kind=base mirrors
	// kind=type's "delete-only-when-unused" discipline.
	ErrBaseStillReferenced = errors.New("ops: base still referenced via extends")

	// ErrSpawnPartialWrite is returned by Create when a parent record's
	// auto_spawn rule fired and one of the children failed to land on
	// disk after at least one write succeeded. ta has no cross-file
	// transaction primitive; per the F23 locked decision the disk write
	// pass is sequential best-effort once validation passes. The
	// wrapped message lists ids that landed and ids that did not so the
	// operator can reconcile manually.
	ErrSpawnPartialWrite = errors.New("ops: auto_spawn partial write")

	// ErrMoveModeMismatch is returned by Move when src and dst dbs do
	// not agree on the file-record vs section-mode boundary. The two
	// backends have incompatible address contracts and cross-mode emit
	// would either lose body bytes or yield malformed output, so the
	// move is rejected loud.
	ErrMoveModeMismatch = errors.New("ops: move: src and dst dbs differ on file-record vs section-mode")

	// ErrMoveFormatMismatch is returned by Move when src and dst dbs do
	// not share a Format (e.g. one TOML and one MD). Cross-format moves
	// would require re-emitting bytes through a different backend with
	// no shared canonical form, so the move is rejected loud.
	ErrMoveFormatMismatch = errors.New("ops: move: src and dst dbs differ on backend format (MD vs TOML)")

	// ErrMoveSelfMove is returned by Move when src and dst id are the
	// same and Copy is false. Self-move is rejected as ambiguous —
	// splicing src out and re-emitting at the same id is almost always
	// a user mistake.
	ErrMoveSelfMove = errors.New("ops: move: src and dst id are identical (use --copy if you meant to duplicate to self, but that is also rejected as ambiguous)")

	// ErrMoveSelfCopy is returned by Move when src == dst and Copy is
	// true. Copy-to-self is a no-op that would corrupt the index, so
	// the operation is rejected loud.
	ErrMoveSelfCopy = errors.New("ops: move: --copy with src == dst is rejected; copy must produce a distinct dst")

	// ErrMoveDestExists is returned by Move when the destination id
	// already names an existing record on disk and Force is false.
	// Pass --force / force=true to overwrite.
	ErrMoveDestExists = errors.New("ops: move: destination already exists; pass --force to overwrite")

	// ErrMovePartialWrite is returned by Move when the dst write
	// succeeded but the src cleanup splice failed. The dst record is
	// correct on disk; the src record still exists at its original
	// path. The wrapped message names both ids and the recovery hint
	// so the operator can reconcile manually. Mirrors the no-auto-
	// rollback discipline used by ErrSpawnPartialWrite.
	ErrMovePartialWrite = errors.New("ops: move: dst write succeeded but src cleanup failed; both records currently exist on disk")
)

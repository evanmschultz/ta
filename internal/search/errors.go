package search

import "errors"

// Sentinel errors returned by Run. Callers branch on these with
// errors.Is; wrapped text carries the offending field name, value, or
// scope string.
var (
	// ErrInvalidScope is returned when Query.Scope is malformed (e.g.
	// references an unknown db or has too few segments to resolve).
	ErrInvalidScope = errors.New("search: invalid scope")

	// ErrUnknownField is returned when Query.Match or Query.Field names a
	// field not declared on the target type. V2-PLAN §7.1 is explicit
	// about loud failure on typos.
	ErrUnknownField = errors.New("search: unknown field")

	// ErrUnscalarMatch is returned when Query.Match names a non-scalar
	// field (array or table). Exact-match on structured values has no
	// well-defined semantics in the spec — we fail loudly rather than
	// silently always-false or always-true.
	ErrUnscalarMatch = errors.New("search: cannot exact-match non-scalar field")

	// ErrUnsupportedFormat mirrors the ops sentinel; the backend
	// factory in this package refuses formats with no decoder.
	ErrUnsupportedFormat = errors.New("search: unsupported format")
)

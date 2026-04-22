// Package search implements the structured + regex search primitive
// described in V2-PLAN §3.7 / §7. It is lang-agnostic: callers hand it
// a resolved schema.Registry and a project root; the package walks the
// relevant backends, applies exact-match filters on typed fields, then
// applies a Go RE2 regex on string fields, and returns full record
// sections in source order.
//
// Layering: this package depends on internal/schema, internal/config,
// internal/db, internal/record, and the backend packages. Nothing above
// it in the cascade (cmd/ta, internal/mcpsrv) may depend on per-backend
// decoding rules directly — they route through search.Run instead.
package search

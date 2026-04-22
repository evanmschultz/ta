package md

import (
	"errors"
	"fmt"
)

// ErrFieldNotBackable is returned by CheckBackableFields when a
// requested field name cannot be served under the MD body-only layout
// (V2-PLAN §5.3.3). Callers wrap this with their own sentinel (e.g.
// mcpsrv.ErrUnknownField or search.ErrUnknownField) so errors.Is
// branches in the CLI / MCP surface stay consistent.
var ErrFieldNotBackable = errors.New("md: body-only layout does not back this field")

// CheckBackableFields errors when any name in requested is not "body".
// The body-only layout serves exactly one field per record; a declared
// non-body field on an MD type is a typed-contract lie from the schema
// author's side — the schema says the field exists but the layout has
// no way to read it. Calling this before decode prevents silent
// zero-hit / empty-map responses.
//
// Callers are the two field-facing entry points that walk MD record
// bytes — mcpsrv.extractMDFields for the `get` tool and
// search.decodeFields for the `search` tool. Sharing the predicate
// here keeps them from drifting on the same contract (V2-PLAN §12.7
// Falsification finding #30).
func CheckBackableFields(requested []string) error {
	for _, name := range requested {
		if name != "body" {
			return fmt.Errorf("%w: %q (only %q is readable)",
				ErrFieldNotBackable, name, "body")
		}
	}
	return nil
}

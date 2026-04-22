package md

import "errors"

// Sentinel errors for the MD backend. Callers branch with errors.Is.
var (
	// ErrSlugCollision is returned when two declared headings at the
	// same declared level in one file produce the same slug. The
	// wrapped error text names both line numbers. Collisions across
	// non-declared levels are ignored (non-declared headings are
	// content, not records). Consistent with V2-PLAN §5.3.2 / §5.5.2 /
	// §11.5.
	ErrSlugCollision = errors.New("md: slug collision")

	// ErrEmptySection is returned by Emit / Find / Splice when called
	// with an empty section path.
	ErrEmptySection = errors.New("md: empty section path")

	// ErrBadLevel is returned by NewBackend when a declared type's
	// Heading is outside [1, 6].
	ErrBadLevel = errors.New("md: heading level must be in [1, 6]")

	// ErrMalformedSection is returned by Emit when the section path
	// cannot be decomposed into the expected <db>.<type>.<slug>
	// (single-instance) or <db>.<instance>.<type>.<slug>
	// (multi-instance) shape — at minimum, the last dotted segment
	// must exist and is taken as the slug.
	ErrMalformedSection = errors.New("md: malformed section path")

	// ErrNotDeclaredType is returned by Emit when the address's
	// type-name segment does not match any declared type on this
	// backend. The backend cannot choose a heading level without a
	// declared type mapping it.
	ErrNotDeclaredType = errors.New("md: address does not match any declared type")

	// ErrDuplicateHeading is returned by NewBackend when two declared
	// types share the same Heading value. The meta-schema rule at
	// V2-PLAN §4.7 forbids this — each heading level binds to exactly
	// one type per db.
	ErrDuplicateHeading = errors.New("md: two declared types share one heading level")
)

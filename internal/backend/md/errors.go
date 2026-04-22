package md

import "errors"

// Sentinel errors for the MD backend. Callers branch with errors.Is.
var (
	// ErrSlugCollision is returned when two headings at the same level
	// in one file produce the same slug. The wrapped error text names
	// both line numbers. Consistent with V2-PLAN §5.5.2 / §11.5.
	ErrSlugCollision = errors.New("md: slug collision")

	// ErrEmptySection is returned by Emit when called with an empty
	// section path.
	ErrEmptySection = errors.New("md: empty section path")

	// ErrBadLevel is returned by NewBackend when level is outside
	// [1, 6].
	ErrBadLevel = errors.New("md: heading level must be in [1, 6]")

	// ErrMalformedSection is returned by Emit when the section path
	// cannot be decomposed into the expected <db>.<type>.<slug>
	// (single-instance) or <db>.<instance>.<type>.<slug>
	// (multi-instance) shape — at minimum, the last dotted segment
	// must exist and is taken as the slug.
	ErrMalformedSection = errors.New("md: malformed section path")
)

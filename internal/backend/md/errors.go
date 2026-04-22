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

	// ErrParentMissing is returned by Splice when the target address
	// names a declared-ancestor that does not exist in the buffer and
	// therefore cannot host the new nested record. Under V2-PLAN §5.3.2
	// (2026-04-21 hierarchical refinement) a child record's insertion
	// point is the end of its declared parent's body range; without a
	// parent there is no well-defined insertion point and the caller
	// must create the parent first.
	//
	// Strict-orphan case: when the buffer carries an orphan chain — a
	// declared heading whose immediate declared parent level is absent
	// (e.g. an H3 under an H1 with H2 declared-but-missing) — Splice of
	// a NEW sibling at that orphan level returns this sentinel even
	// though the scanner READ path emits the existing orphan with a
	// skip-the-gap chain. This asymmetry is intentional: legacy orphan
	// files remain readable, new orphan-level writes must materialize
	// the missing declared ancestor first (V2-PLAN §5.3.2 orphans
	// paragraph).
	ErrParentMissing = errors.New("md: declared ancestor missing")
)

package db

// Phase 9.2 (PLAN §12.17.9) replaces kebab-cased path-derived slugs
// with dotted file-relpath slugs. The old `kebabCase` /
// `slugFromCollectionPath` helpers are no longer reachable from the
// resolver; they are intentionally removed to keep the package
// surface aligned with the new model.

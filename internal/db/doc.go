// Package db resolves a dotted section address (e.g.
// "plan_db.drop_3.build_task.task_001") against a schema.Registry and a
// project root, returning the on-disk file that backs the addressed
// record. See V2-PLAN.md §5.5 for the full addressing spec.
//
// The resolver is lang-agnostic: it never imports any backend package. It
// hands back the schema.DB, the resolved instance (empty slug for legacy
// single-file dbs), and the absolute file path; callers are responsible
// for reading the file and handing its bytes to the correct
// record.Backend.
//
// Phase 9.1 (PLAN §12.17.9) migrates the schema model from
// `Shape`+`Path` to `Paths []string`. The address parser and resolver in
// this package retain their pre-9.1 segment-count rules during the
// transitional window — they branch on `schema.IsSingleFile(db)` (true
// when Paths == one entry with .toml/.md suffix) to choose the legacy
// single-instance vs multi-instance form. Phase 9.2 rewrites the address
// grammar (drops the db prefix) and the resolver (paths-glob expansion);
// after Phase 9.2 lands, every IsSingleFile call site here disappears.
//
// Pre-9.2 resolution rules:
//
//   - Legacy single-file (IsSingleFile(db) == true): address is
//     "<db>.<type>.<id-path>", 3+ segments; tail joined into addr.ID.
//     Backing file is db.Paths[0] resolved against the project root.
//   - Legacy multi-instance (IsSingleFile(db) == false): address is
//     "<db>.<instance>.<type>.<id-path>", 4+ segments. Phase 9.1 keeps
//     the dir-per-instance scan and collection scan from the pre-9.1
//     resolver; Phase 9.2 rewrites both into a paths-glob expander.
//
// Fail-loudly contract (§1.1): segment-count below the minimum for the
// resolved db's shape MUST error. Empty intermediate segments ("a..b",
// leading/trailing dots) also error.
//
// path_hint safety (§11.D): ResolveWrite rejects any path_hint that
// escapes the collection root, using filepath.IsLocal for the lexical
// check.
package db

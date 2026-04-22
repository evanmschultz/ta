// Package db resolves a dotted section address (e.g.
// "plan_db.drop_3.build_task.task_001") against a schema.Registry and a
// project root, returning the on-disk file that backs the addressed
// record. See V2-PLAN.md §5.5 for the full addressing spec.
//
// The resolver is lang-agnostic: it never imports any backend package. It
// hands back the schema.DB, the resolved instance (empty slug for
// single-instance dbs), and the absolute file path; callers are
// responsible for reading the file and handing its bytes to the correct
// record.Backend.
//
// Shape-driven resolution:
//
//   - ShapeFile (single-instance): address is <db>.<type>.<id> (3 segments
//     required); the backing file is the declared path relative to the
//     project root.
//   - ShapeDirectory (dir-per-instance): address is
//     <db>.<instance>.<type>.<id> (4 segments required); each immediate
//     subdir of the declared directory that contains a canonical
//     `db.<ext>` file is one instance.
//   - ShapeCollection (file-per-instance): address is
//     <db>.<instance>.<type>.<id> (4 segments required); every file under
//     the declared directory (recursively) whose extension matches the
//     db's format is one instance, with the slug derived from its
//     path-from-root.
//
// Fail-loudly contract (§1.1): segment-count mismatches for the resolved
// db's shape MUST error. No silent promotion between single-instance and
// multi-instance forms.
package db

// Package index implements the runtime record-type index that lives at
// `<project-root>/.ta/index.toml`. PLAN §12.17.9 Phase 9.3.
//
// The index is the runtime answer to "for canonical address X, what
// record type is it?" — a flat map[canonical-address]Entry that record
// CRUD (Phase 9.4) and lookup paths consult before opening the backing
// file. The on-disk shape is one bracket-table per record keyed by the
// full canonical address (`<file-relpath>.<type>.<id-tail>`), nested
// naturally by dot-segment so go-toml/v2 emits `[phase_1.db.t1]` rather
// than a quoted single-key form.
//
// Trust-and-fail-loud: the index is NOT an authoritative cache. Reads
// trust the recorded type for routing; mismatches between the index and
// on-disk truth surface a loud error pointing at `ta index rebuild`.
// There is no mtime tracking, no auto-rebuild, no in-process invalidation
// scheduler. The orchestrator owns rebuild via the explicit `ta index
// rebuild` CLI verb.
//
// Atomic writes flow through `internal/fsatomic.Write` (same temp-file +
// rename idiom as schema mutations). Phase 9.3 ships without a sentinel
// `.lock` file because the MCP cache model already pins one project per
// server process; the rename idiom suffices.
//
// Phase 9.3 deliberately does NOT wire ops CRUD into the index — that
// integration lands in Phase 9.4. This package is functionally complete
// and tested as a standalone unit.
//
// Allowed dependencies: `internal/db`, `internal/schema`, `internal/config`,
// `internal/backend/{md,toml}`, `internal/record`, `internal/fsatomic`.
// Must NOT import `internal/ops` — Phase 9.4 imports this package, so the
// reverse dependency would cycle.
package index

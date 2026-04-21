# ta v2 Drop — WORKLOG

Narrative chronological record of the v2 implementation drop. Orchestrator-maintained; each of §12.1 through §12.12 from `docs/V2-PLAN.md` gets one section with build + QA proof + QA falsification outcomes.

Temporary artifact. Will be re-materialized into the dogfood `workflow/ta-v2/db.toml` (§12.10) and eventually deleted along with `docs/` on §12.11 README collapse.

## Drop Status

- **Tag target:** v0.1.0 (pre-stable per V2-PLAN.md §2.6)
- **Coordination:** MD worklog only — no Tillsyn. `ta` is a prototype of Tillsyn's coordination concept, not a user of it.
- **Agent rules:** every build step routes through `go-builder-agent`; every build step gets a `go-qa-proof-agent` pass AND a `go-qa-falsification-agent` pass (in parallel, fresh context each) before the next step starts.
- **Baseline:** `mage check` green at drop start (2026-04-21). All 5 MVP packages pass with race detector.

## Step Index

| #     | Step                                 | Build | Proof | Falsif | Done |
|-------|--------------------------------------|-------|-------|--------|------|
| 12.1  | Backend interface extraction         | ⏳    | —     | —      | —    |
| 12.2  | Schema language update               | —     | —     | —      | —    |
| 12.3  | Address resolution package           | —     | —     | —      | —    |
| 12.4  | MD backend                           | —     | —     | —      | —    |
| 12.5  | Data tool surface                    | —     | —     | —      | —    |
| 12.6  | Schema tool CRUD                     | —     | —     | —      | —    |
| 12.7  | Laslig CLI rendering                 | —     | —     | —      | —    |
| 12.8  | Search                               | —     | —     | —      | —    |
| 12.9  | MCP caching                          | —     | —     | —      | —    |
| 12.10 | Dogfood migration                    | —     | —     | —      | —    |
| 12.11 | README collapse                      | —     | —     | —      | —    |
| 12.12 | Release (tag v0.1.0)                 | —     | —     | —      | —    |

Legend: ⏳ in progress · ✅ passed · ❌ failed (blocks advance) · — not yet started

---

## 12.1 — Backend interface extraction

**Scope (from V2-PLAN.md §12.1):** Define `internal/record/Backend`. Move `internal/tomlfile/` behind it as `internal/backend/toml/`. Zero behavior change; all existing tests keep passing.

### Build — go-builder-agent

Status: ⏳ spawned 2026-04-21.

**Result:** build landed green 2026-04-21. `mage check` clean (fmtcheck + vet + test-race + tidy). Zero behavior change; only existing-package tests touched were their `package` headers (`tomlfile` → `toml`). No test logic edits.

**Added:**

- `internal/record/` — one file, `record.go`. Holds `Record` (`map[string]any`), `Section` (`Path`, `Range [2]int`, `Record`), and the `Backend` interface (`List` / `Find` / `Emit` / `Splice`) per V2-PLAN §5.1. No tests yet — the interface is consumed downstream in §12.2+.
- `internal/backend/toml/backend.go` — new file holding `Backend` struct that implements `record.Backend`. It wraps the existing `ParseBytes` / `EmitSection` / `(*File).Splice` so the lang-agnostic layer can drive TOML through the interface. Compile-time assertion `var _ record.Backend = (*Backend)(nil)`. `List` supports the empty-scope "all sections" case and prefix matching for `<db>` / `<db>.<type>` shape. `Find` returns a locator-only `record.Section` (Record nil) — field decoding is a later layer's job.

**Moved:**

- `internal/tomlfile/` → `internal/backend/toml/`. All six source files copied verbatim except the `package tomlfile` → `package toml` header. `Parse` / `ParseBytes` / `EmitSection` / `(*File).Splice` / `WriteAtomic` / `File` / `Section` / `ErrNotExist` signatures unchanged so existing tests pass unmodified. Error-message prefixes (`"tomlfile: ..."`) kept verbatim for byte-identical failure behavior. Tests copied with only the package header updated.

**Updated call sites:**

- `cmd/ta/commands.go`, `internal/mcpsrv/tools.go`: import path `internal/tomlfile` → `internal/backend/toml`; identifiers `tomlfile.X` → `toml.X`. No call-site collision with pelletier's `go-toml/v2` (only `internal/schema` imports that, in a different file).
- `internal/config/doc.go`, `internal/mcpsrv/doc.go`: package-doc prose updated to reference `internal/backend/toml` instead of `tomlfile`.

**Deleted:** `internal/tomlfile/` (all nine files).

**Surprises:** none. Clean rename + one adapter file.

**Commit:** pending.

### QA Proof — go-qa-proof-agent

Status: pending (gated on build land + green).

### QA Falsification — go-qa-falsification-agent

Status: pending (gated on build land + green).

### Outcome

Pending.

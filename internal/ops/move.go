package ops

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/evanmschultz/ta/internal/backend/toml"
	"github.com/evanmschultz/ta/internal/config"
	"github.com/evanmschultz/ta/internal/db"
	"github.com/evanmschultz/ta/internal/index"
	"github.com/evanmschultz/ta/internal/record"
	"github.com/evanmschultz/ta/internal/schema"
)

// MoveOptions controls optional behavior of Move. Copy preserves the
// source after the destination write succeeds; Force authorizes
// overwriting an existing destination record. Verbose has no behavioral
// effect inside ops; it is propagated onto MoveResult so callers can
// render extra detail. Mirrors DeleteOptions / CreateOptions shape.
type MoveOptions struct {
	// Copy, when true, leaves the source record intact after the
	// destination write succeeds. Default false (move semantics: src is
	// spliced out after dst lands).
	Copy bool

	// Force, when true, authorizes overwriting an existing destination
	// record. Default false (a colliding dst surfaces ErrMoveDestExists
	// before any disk mutation).
	Force bool

	// Verbose has no behavioral effect inside ops; carried through onto
	// MoveResult so CLI / MCP wiring stays symmetric with the other
	// option structs.
	Verbose bool
}

// MoveResult captures the structured outcome of one Move call. SrcID /
// DstID echo the call. Action is "move" or "copy". SrcFilePath and
// DstFilePath are the absolute on-disk file paths of the affected
// files. Sources is the schema-source provenance list, same as other
// mutation endpoints.
type MoveResult struct {
	SrcID       string
	DstID       string
	Action      string
	SrcFilePath string
	DstFilePath string
	Sources     []string
}

// Move relocates a record from srcID to dstID. typeName, when non-empty,
// is the db-qualified target type for the dst (e.g. `cascade.drop`); it
// is used both as a cross-db type defaulting override and as the
// authoritative type for dst validation. Per the F36 locked rules:
//
//  1. Mode mismatch (file-record vs section-mode) and format mismatch
//     (MD vs TOML) reject loud (ErrMoveModeMismatch / ErrMoveFormatMismatch).
//     The two backends have incompatible address contracts and re-emitting
//     across the boundary would silently lose body bytes or yield
//     malformed output.
//  2. srcID == dstID with Copy=false rejects with ErrMoveSelfMove (identity
//     move is almost always a user mistake). srcID == dstID with Copy=true
//     rejects with ErrMoveSelfCopy (copy-to-self is a no-op that would
//     corrupt the index).
//  3. typeName defaulting: when both src and dst dbs declare a type with
//     the same bare name (e.g. both have a `task` type), the dst type
//     defaults to src's bare type. When the dbs share NO type names, the
//     caller must pass --type=<dst-db>.<type> explicitly. An explicit
//     typeName MUST be db-qualified; bare names error with
//     ErrTypeNotQualified.
//  4. Dst-first-src-after invariant: write dst FIRST, then delete src.
//     A dst write failure leaves src intact (clean failure). A successful
//     dst write followed by a src cleanup failure surfaces
//     ErrMovePartialWrite with both ids and the recovery hint; no
//     auto-rollback. Mirrors the F23 ErrSpawnPartialWrite discipline.
//  5. Index single-save: idx.Put(dst) + idx.Delete(src) (move only) +
//     single idx.Save(). See moveOneItem's doc comment for the
//     architectural divergence note.
//
// Cross-db moves are supported. The caller's typeName, when non-empty,
// is the dst type; it is db-qualified so the dst db is implicit in it.
// When omitted the dst db is whichever db the resolver picks for dstID
// (subject to alphabetic-first iteration; F29 constraint applies once
// typeName is non-empty).
func Move(projectPath, srcID, dstID, typeName string, opts MoveOptions) (MoveResult, error) {
	resolution, err := resolveFromProjectDir(projectPath)
	if err != nil {
		return MoveResult{}, fmt.Errorf("resolve schema for %s: %w", projectPath, err)
	}
	resolver := db.NewResolver(projectPath, resolution.Registry)
	return moveOneItem(projectPath, resolver, resolution, srcID, dstID, typeName, opts)
}

// moveOneItem drives the index directly (idx.Put + idx.Delete + single
// idx.Save) to flush dst-add and src-remove in one atomic Save. This
// bypasses the writeIndexEntry/deleteIndexEntry helpers intentionally
// — those helpers each save independently, which would leave the index
// inconsistent if the second save failed mid-move. Future ops helpers
// MUST NOT assume "every disk write goes through writeIndexEntry".
func moveOneItem(
	projectPath string,
	resolver *db.Resolver,
	resolution config.Resolution,
	srcID, dstID, typeName string,
	opts MoveOptions,
) (MoveResult, error) {
	// Self-move / self-copy guards run before resolution so a degenerate
	// call fails fast without touching disk.
	if srcID == dstID {
		if opts.Copy {
			return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
				fmt.Errorf("%w: %q", ErrMoveSelfCopy, srcID)
		}
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("%w: %q", ErrMoveSelfMove, srcID)
	}

	// Resolve src first; it must exist on disk.
	srcResolved, srcDB, err := resolver.ResolveID(srcID)
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: resolve src %q: %w", srcID, err)
	}

	// Resolve dst with the F29 constrain-to-named-db rule when typeName
	// is supplied: a db-qualified --type pins dst to that db so a
	// 2-segment id does not silently fall through to an alphabetically
	// earlier db with a looser mount.
	dstResolved, dstDB, err := resolveIDForCallerType(resolver, dstID, typeName)
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: resolve dst %q: %w", dstID, err)
	}

	// Mode and format compatibility gates run after resolution but
	// before any disk read so the caller sees the loud-fail before we
	// pay the cost of loading bytes.
	if schema.DBHasFileAsRecord(srcDB) != schema.DBHasFileAsRecord(dstDB) {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("%w: src db %q vs dst db %q", ErrMoveModeMismatch, srcDB.Name, dstDB.Name)
	}
	if srcDB.Format != dstDB.Format {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("%w: src db %q format=%q vs dst db %q format=%q",
				ErrMoveFormatMismatch, srcDB.Name, srcDB.Format, dstDB.Name, dstDB.Format)
	}

	// Resolve the src bare type via the index (authoritative per F10);
	// fall back to the resolveTypeForID rules when the index entry is
	// absent.
	srcBareType, err := resolveTypeForID(srcResolved, "", false, projectPath, declaredTypeNames(srcDB))
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: resolve src type for %q: %w", srcID, err)
	}

	// Decide the dst bare type per Decision 4: explicit typeName always
	// wins (must be db-qualified and target dstDB); otherwise default
	// to src's bareType when dstDB declares the same type name.
	dstBareType, err := resolveDstType(srcDB, dstDB, srcBareType, typeName)
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources}, err
	}

	// Read src file once; we'll need its bytes for both the field
	// extraction (to feed dst emit) and the splice-out at cleanup.
	srcBuf, err := os.ReadFile(srcResolved.FilePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
				fmt.Errorf("%w: %s", ErrFileNotFound, srcResolved.FilePath)
		}
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: read src %s: %w", srcResolved.FilePath, err)
	}
	srcBackend, err := buildBackend(srcDB, srcResolved)
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources}, err
	}
	srcSection := backendSectionPath(srcDB, srcResolved, srcBareType)
	srcSec, found, err := srcBackend.Find(srcBuf, srcSection)
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: locate src %q: %w", srcID, err)
	}
	if !found {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("%w: %q", ErrRecordNotFound, srcID)
	}

	// Pull every declared field on src so the dst emit has the same
	// field set. extractAllDeclaredFields silently omits absent
	// declared fields, matching the Update / GetAll contract.
	srcType, ok := srcDB.Types[srcBareType]
	if !ok {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("%w: type %q not declared on src db %q",
				ErrUnknownField, srcBareType, srcDB.Name)
	}
	srcRelPath := tomlRelPathForFields(srcResolved)
	data, err := extractAllDeclaredFields(srcBuf, srcSec, srcDB, srcType, srcRelPath)
	if err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: extract src fields: %w", err)
	}

	// Validate the data against the dst type. A dst-side validation
	// failure aborts before any disk write so src is preserved.
	if err := resolution.Registry.Validate(validationPath(dstResolved, dstBareType), data); err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources}, err
	}

	// Build the dst write plan. When Force is false, ErrRecordExists on
	// a colliding dst is rewrapped as ErrMoveDestExists so callers can
	// branch on the move-specific sentinel. When Force is true we
	// bypass the existence probe and emit + splice unconditionally.
	dstFilePath, dstWriteBuf, err := planMoveDstWrite(dstDB, dstResolved, dstID, dstBareType, data, opts.Force)
	if err != nil {
		return MoveResult{
			SrcID:   srcID,
			DstID:   dstID,
			Sources: resolution.Sources,
		}, err
	}

	// File-as-record dbs and section-mode dbs require different cleanup
	// strategies, so split the dst-write + src-cleanup execution down
	// the file-as-record axis. Both branches MUST observe the F36
	// dst-first-src-after invariant.
	srcIsFileRecord := schema.DBHasFileAsRecord(srcDB)
	sameFile := srcResolved.FilePath == dstResolved.FilePath

	// Phase 1: dst write. Always run it — even in same-file cases the
	// emit-splice already produced the new buffer, and writing dst-then-
	// src keeps the invariant honest in failure cases.
	if err := writeMoveDst(dstFilePath, dstWriteBuf); err != nil {
		return MoveResult{SrcID: srcID, DstID: dstID, Sources: resolution.Sources},
			fmt.Errorf("ops: move: write dst %s: %w", dstFilePath, err)
	}

	// Phase 2: src cleanup. Skipped entirely on copy. For file-as-record
	// section-mode same-file moves are impossible (a file IS the record;
	// distinct ids resolve to distinct files). For section-mode same-
	// file moves the splice has to re-scan the post-dst-write buffer —
	// the dst write may have shifted the src section's byte range.
	if !opts.Copy {
		if srcIsFileRecord {
			// Whole-file record: rm the src file. Splicing emits empty
			// bytes and leaves the file behind, which would leave a
			// zero-byte file lying around — semantically wrong for
			// file-as-record where the file IS the record.
			if err := os.Remove(srcResolved.FilePath); err != nil {
				return MoveResult{
						SrcID:       srcID,
						DstID:       dstID,
						Action:      "move",
						SrcFilePath: srcResolved.FilePath,
						DstFilePath: dstFilePath,
						Sources:     resolution.Sources,
					}, fmt.Errorf(
						"%w: dst %q at %s; src %q at %s still exists; recover with `ta delete %s%s` then `ta index rebuild` (dst is on disk but not yet indexed): %v",
						ErrMovePartialWrite,
						dstID, dstFilePath,
						srcID, srcResolved.FilePath,
						srcID, srcDeleteFlagsHint(srcDB.Name, srcBareType), err)
			}
		} else {
			// Section-mode: splice src bytes out of the source file.
			var writeBuf []byte
			if sameFile {
				// Re-find the src section against the post-dst-write
				// buffer; the dst splice may have moved the src range.
				srcSecAfter, found, err := srcBackend.Find(dstWriteBuf, srcSection)
				if err != nil {
					return MoveResult{
							SrcID:       srcID,
							DstID:       dstID,
							Action:      "move",
							SrcFilePath: srcResolved.FilePath,
							DstFilePath: dstFilePath,
							Sources:     resolution.Sources,
						}, fmt.Errorf(
							"%w: re-locate src in post-dst-write buffer: %v",
							ErrMovePartialWrite, err)
				}
				if !found {
					// Dst write replaced src in place (collision under
					// Force); buffer is already correct.
					writeBuf = dstWriteBuf
				} else {
					writeBuf = spliceOut(dstWriteBuf, srcSecAfter.Range)
				}
			} else {
				writeBuf = spliceOut(srcBuf, srcSec.Range)
			}
			if err := toml.WriteAtomic(srcResolved.FilePath, writeBuf); err != nil {
				return MoveResult{
						SrcID:       srcID,
						DstID:       dstID,
						Action:      "move",
						SrcFilePath: srcResolved.FilePath,
						DstFilePath: dstFilePath,
						Sources:     resolution.Sources,
					}, fmt.Errorf(
						"%w: dst %q at %s; src %q at %s still exists; recover with `ta delete %s%s` then `ta index rebuild` (dst is on disk but not yet indexed): %v",
						ErrMovePartialWrite,
						dstID, dstFilePath,
						srcID, srcResolved.FilePath,
						srcID, srcDeleteFlagsHint(srcDB.Name, srcBareType), err)
			}
		}
	}

	// Index update: drive idx.Put + idx.Delete + single idx.Save
	// directly (Decision 6). A load failure here is the same shape as
	// the existing helpers — dst is on disk; the operator is told to
	// rebuild.
	if err := updateIndexForMove(projectPath, dstResolved, dstBareType, srcResolved, opts.Copy); err != nil {
		return MoveResult{
			SrcID:       srcID,
			DstID:       dstID,
			Action:      moveAction(opts.Copy),
			SrcFilePath: srcResolved.FilePath,
			DstFilePath: dstFilePath,
			Sources:     resolution.Sources,
		}, err
	}

	return MoveResult{
		SrcID:       srcID,
		DstID:       dstID,
		Action:      moveAction(opts.Copy),
		SrcFilePath: srcResolved.FilePath,
		DstFilePath: dstFilePath,
		Sources:     resolution.Sources,
	}, nil
}

// resolveDstType applies the F36 Decision-4 type-defaulting rule. An
// explicit typeName always wins (validated as db-qualified and matching
// dstDB); when typeName is empty, the dst type defaults to src's bare
// type if dstDB declares the same name. When the dbs share NO type
// names the caller must specify --type, and the error message includes
// the explicit guidance.
func resolveDstType(srcDB, dstDB schema.DB, srcBareType, typeName string) (string, error) {
	if typeName != "" {
		dbPart, bareType, ok := strings.Cut(typeName, ".")
		if !ok {
			return "", fmt.Errorf("%w: --type %q must be db-qualified (e.g. `%s.<type>`)",
				ErrTypeNotQualified, typeName, dstDB.Name)
		}
		if dbPart != dstDB.Name {
			return "", fmt.Errorf(
				"%w: --type %q targets db %q but dst id resolved to db %q",
				ErrTypeMismatch, typeName, dbPart, dstDB.Name)
		}
		if bareType == "" {
			return "", fmt.Errorf("%w: empty type after %q.", ErrTypeNotQualified, dbPart)
		}
		if _, ok := dstDB.Types[bareType]; !ok {
			return "", fmt.Errorf("%w: type %q not declared on dst db %q",
				ErrTypeMismatch, bareType, dstDB.Name)
		}
		return bareType, nil
	}
	// No explicit --type. Default to src's bareType when dstDB declares
	// the same name; otherwise instruct the caller to specify --type.
	if _, ok := dstDB.Types[srcBareType]; ok {
		return srcBareType, nil
	}
	return "", fmt.Errorf(
		"%w: src db %q and dst db %q do not share a type named %q; specify --type=%s.<type>",
		ErrTypeMismatch, srcDB.Name, dstDB.Name, srcBareType, dstDB.Name)
}

// planMoveDstWrite returns (dstFilePath, newBuf, err) for the dst-side
// write. When force is false a colliding dst surfaces ErrMoveDestExists
// before any disk mutation. When force is true the existence probe is
// bypassed and the splice replaces any existing record at dstID.
func planMoveDstWrite(
	dstDB schema.DB,
	dstResolved db.Resolved,
	dstID, dstBareType string,
	data map[string]any,
	force bool,
) (string, []byte, error) {
	dstFilePath := dstResolved.FilePath
	backend, err := buildBackend(dstDB, dstResolved)
	if err != nil {
		return "", nil, err
	}
	priorBuf, err := readFileIfExists(dstFilePath)
	if err != nil {
		return "", nil, err
	}
	backendSection := backendSectionPath(dstDB, dstResolved, dstBareType)
	if !force {
		if _, exists, err := backend.Find(priorBuf, backendSection); err != nil {
			return "", nil, fmt.Errorf("ops: move: probe dst %q: %w", dstID, err)
		} else if exists {
			return "", nil, fmt.Errorf("%w: %q at %s", ErrMoveDestExists, dstID, dstFilePath)
		}
	}
	emitted, err := backend.Emit(backendSection, record.Record(data))
	if err != nil {
		return "", nil, fmt.Errorf("ops: move: emit dst %q: %w", dstID, err)
	}
	newBuf, err := backend.Splice(priorBuf, backendSection, emitted)
	if err != nil {
		return "", nil, fmt.Errorf("ops: move: splice dst %q: %w", dstID, err)
	}
	return dstFilePath, newBuf, nil
}

// writeMoveDst mkdirs the dst's parent dir on first write and atomic-
// writes the buffer. Mirrors the disk-write tail of executeRecordWrite
// but inlined so move's failure-path messaging stays scoped to the
// move-specific error sentinels rather than the per-record write
// helper's wrapping.
func writeMoveDst(dstFilePath string, buf []byte) error {
	dir := filepath.Dir(dstFilePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	if err := toml.WriteAtomic(dstFilePath, buf); err != nil {
		return err
	}
	return nil
}

// updateIndexForMove drives the index for a move/copy in a single Save.
// Per Decision 6 this is the architectural divergence — the existing
// writeIndexEntry / deleteIndexEntry helpers each Save independently,
// which would leave the index inconsistent if the second Save failed
// mid-move. We load once, mutate both deltas, Save once.
func updateIndexForMove(
	projectRoot string,
	dstResolved db.Resolved, dstBareType string,
	srcResolved db.Resolved,
	copyOnly bool,
) error {
	idx, err := index.Load(projectRoot)
	if err != nil {
		return fmt.Errorf("ops: move: load index: %w (records on disk; run `ta index rebuild`)", err)
	}
	idx.Put(dstResolved.Canonical(), index.Entry{Type: dstBareType})
	if !copyOnly {
		idx.Delete(srcResolved.Canonical())
	}
	if err := idx.Save(projectRoot); err != nil {
		return fmt.Errorf("ops: move: save index: %w (records on disk; run `ta index rebuild`)", err)
	}
	return nil
}

// moveAction returns the wire-shape Action string for MoveResult.
func moveAction(copyOnly bool) string {
	if copyOnly {
		return "copy"
	}
	return "move"
}

// srcDeleteFlagsHint composes a "--type=<db>.<type>" suffix for the
// recovery hint inside ErrMovePartialWrite messages. The hint is for
// human consumption — it tells the operator the exact `ta delete`
// command to run to clean up an orphaned src after a partial-write
// failure.
func srcDeleteFlagsHint(srcDBName, srcBareType string) string {
	if srcDBName == "" || srcBareType == "" {
		return ""
	}
	return " --type=" + srcDBName + "." + srcBareType
}

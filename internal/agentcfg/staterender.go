package agentcfg

// staterender.go is the stateful boot render harness: the §5 capture-diff
// overlay loop plus the §3.2 first-migration path from
// docs/reference/config-migration-to-prism.md. Compose (compose.go) renders ONE
// surface as a pure function of its layers; ComposeStateful wraps it with the
// per-boot state machine that decides the overlay layer from the sidecar files
// and reports what the caller must persist.
//
// §3.2 originally called that path a BOOTSTRAP that seeds an empty overlay and
// skips capture. B1 inverted it — it now ADOPTS the on-disk file — and the
// docstrings below describe the body as it stands, not as §3.2 wrote it.
//
// It stays PURE — no file I/O, no container. Each caller reads the two sidecars
// and the current surface file, hands their bytes here, and writes back the part
// of StatefulOutput its job needs:
//
//   - internal/entrypoint's boot path (prism.go) is the full one — surface file,
//     last_render and overlay, three writes every boot, plus a fourth (the list-capture
//     sidecar) for a surface with a config-list path.
//   - internal/cli's CAPTURE path (captureSurfaceAt, configdiff.go — behind
//     `yolo config capture` and the capture-on-terminate fold in
//     configcapture.go) writes ONLY the overlay sidecar (and the list-capture sidecar
//     beside it, for a surface that has one). It is here to reuse the
//     engine's capture diff rather than grow a second one that could disagree
//     with the boot render; the surface file and last_render stay the boot's to
//     write.
//
// `yolo config diff` is NOT a caller: it reports the overlay sidecar's own content
// and deliberately does not re-compose (configDiff's docstring says why).
//
// Keeping the state machine here means the hard parts — first-migration
// detection, the §3.3 defensive handling of dangling/corrupt sidecars, and the
// diff/accumulate/render loop — are unit-tested with zero filesystem, and it
// can use the unexported mergeDiff/mergeAccumulate directly.
//
// The two sidecars (§5), which the caller stores in `<workspace>/.yolo/prism/`.
// B3: they are DIFFERENT KINDS of thing, and conflating them has caused real
// mistakes — reason about each by its kind, not by "the sidecars":
//
//   - overlay — DURABLE STATE. The accumulated in-jail edits, and the only record
//     that they ever happened. Nothing else can reconstruct it. Losing it loses
//     every captured edit permanently; that is why `yolo config reset` deleting it
//     IS the discard operation, and why it lives in the workspace rather than a
//     cache dir. ALWAYS JSON: the overlay must carry `null` tombstones (a captured
//     deletion), which the TOML/lines codecs cannot express, and JSON is the one
//     codec that round-trips the generic value model including nulls. Engine-
//     internal — the agent never sees it — so its format is yolo's choice, not the
//     surface's.
//
//   - last_render — a ONE-BOOT PENDING-EDIT BASELINE. Not a cache and not durable
//     state. It is the exact surface-codec bytes yolo wrote last boot, stored in the
//     surface's own codec so it byte-matches the file and diffs cleanly.
//
//     It is tempting to call it a cache, since it is derivable in principle — and
//     that is exactly the mistake. Deleting it does NOT cause a harmless
//     recompute: it destroys the ability to tell an in-jail EDIT from yolo's own
//     previous output, so every edit made since the last boot and not yet captured
//     is silently lost. Before B1 it was worse — a missing baseline discarded the
//     whole file's agent-owned state (the copilot OAuth wipe). It is "state" only
//     for the span of one boot cycle, after which it is rewritten.
//
// Practical consequence: the overlay must be preserved and backed up like data;
// last_render must be preserved across a single restart but is meaningless
// afterwards. Neither is a cache, and nothing here may be pruned as one.
//
// VOCABULARY (E/3.7). Three terms are in use and they are NOT synonyms, which is why a
// blanket rename would lose information rather than add clarity:
//
//	in-jail edit       the ACT: an agent or user writing to a composed file
//	captured edits     the user-facing name for the STATE that survives regeneration.
//	                   This is the umbrella term — what `yolo config diff` shows and
//	                   `yolo config reset` discards
//	captured overlay   the specific LAYER in the fold, between workspace and computed
//
// Deliberately NOT "managed": that already means keys YOLO re-asserts and wins, which
// is the opposite relationship. Using one word for both would make the fold order
// unreadable.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonptr"
)

// StatefulInputs is everything the boot harness needs to render one surface
// with overlay capture. The Base carries the stateless layers (surface,
// host bytes, workspace, script, VM); the harness computes Base.Overlay itself
// from the sidecars, so any Overlay set on Base is ignored.
type StatefulInputs struct {
	// Base is the stateless composition input (compose.go). Its Overlay field is
	// overwritten by the harness — set the surface, host, workspace, and script
	// here, not the overlay.
	Base Inputs

	// CurrentBytes is the current on-disk content of the surface file (the bytes
	// the agent may have edited in-jail), or nil/empty when the file is absent.
	// Decoded with the surface codec for the §5 capture diff.
	CurrentBytes []byte

	// LastRenderPresent reports whether the last_render sidecar exists on disk.
	// Its ABSENCE is the first-migration signal (§3.2). With no baseline there is
	// nothing to diff against, so capture is impossible — and since B1 the harness
	// ADOPTS the on-disk file instead of discarding it, seeding the overlay from
	// what the file holds beyond the pure render. It seeded an EMPTY overlay here
	// before B1, which is what wiped copilot's OAuth tokens.
	LastRenderPresent bool
	// LastRenderBytes is the last_render sidecar content (the surface-codec bytes
	// yolo wrote last boot). Ignored when LastRenderPresent is false. A present
	// but empty or undecodable value is treated as a first migration (§3.3): it
	// cannot be trusted as a diff baseline, so this boot adopts rather than
	// capturing a diff against a baseline that is not there.
	LastRenderBytes []byte

	// OverlayJSON is the overlay sidecar content (JSON), or nil when absent. On a
	// first migration it is IGNORED regardless (§3.3 dangling-overlay case): that
	// boot's overlay is rebuilt from the on-disk file by adoption, so a dangling
	// sidecar cannot leak into it. (Before B1 the rebuilt value was always {},
	// which is why this said "reset to {}".)
	OverlayJSON []byte

	// ListCaptureJSON is the list-capture sidecar content (JSON), or nil when absent: the
	// per-entry capture at each list path (ListRecord, listcontrib.go). Its KEYS are part
	// of the input as well as its records — the set of list paths is STICKY, the live
	// contributions' paths plus every path the sidecar already names — because the capture
	// callers that pass no layers (internal/cli's captureSurfaceAt, behind `yolo config
	// capture` and capture-on-terminate) can learn a surface's list paths from nowhere else,
	// and a capture that did not know a path was a list path would freeze the whole array
	// into the overlay again. Ignored on a first migration, like OverlayJSON.
	ListCaptureJSON []byte

	// InsertRecordJSON is the config-list INSERT record (ListInsertRecord, keyed like the
	// list capture), or nil when absent: the entries yolo itself put at each list path — the
	// `rmw` mechanism's record, which the host also keeps under `own` (InsertRecordFromRender).
	// Read by ADOPTION alone, which otherwise has no way to tell a pack's entry from the
	// user's once the baseline is gone: an entry the record says yolo inserted is never
	// adopted as a user add, and an entry it says the user declined is adopted as a removal.
	// That is what keeps a host ownership switch (`assert` to `own`) from turning a pack's
	// entries into the user's. FAIL-SAFE: an unreadable record claims nothing.
	InsertRecordJSON []byte
}

// StatefulOutput is the render plus the sidecar values the caller must
// persist. The BOOT caller writes Result.Encoded to the surface path,
// LastRenderBytes to the last_render sidecar, and OverlayJSON to the overlay
// sidecar — three writes, unconditionally, every boot — and ListCaptureJSON to the
// list-capture sidecar when it is non-nil. The capture path writes OverlayJSON and
// ListCaptureJSON alone (see the file header).
type StatefulOutput struct {
	// Result is the composed surface (compose.go Result): Config, Encoded bytes,
	// Provenance.
	Result *Result

	// LastRenderBytes is what to write to the last_render sidecar: exactly
	// Result.Encoded (the surface-codec bytes just rendered). Provided as a
	// named field so the caller's intent reads clearly at the write site.
	LastRenderBytes []byte

	// PureBytes is the LAYERS-ALONE render this adoption subtracted to find its
	// residue — Compose(Base) with no capture overlay at all — or nil when this
	// render computed none: every steady-state render, a keyless surface (which
	// adopts nothing), and a current file that does not decode.
	//
	// Reported for the one question a caller cannot otherwise answer: is the file
	// on disk anything other than what yolo's own layers produce? The adoption
	// computes exactly that comparison to build its residue and then drops the
	// operand, so a caller wanting it had to re-run Compose — a second render of
	// the same surface, free to disagree with this one. The caller is
	// entrypoint.archiveAdoption, whose gate has to tell "bytes present" from
	// "bytes yolo wrote" (OQ-CO7, docs/design/config-ownership-and-promotion.md
	// §6.3.3); it is a report, never an input to anything here.
	PureBytes []byte

	// ListCaptureJSON is what to write to the list-capture sidecar, or nil when this
	// surface has no list path — in which case NOTHING is written, so a surface without
	// lists keeps exactly the sidecars it had (TestRenderFingerprintStable).
	ListCaptureJSON []byte

	// ListNotes are one-line boot notes about list paths this render could not treat per
	// entry (a captured deletion or non-array edit at a list path, which stays a whole-value
	// capture and masks every contribution) or converted (a whole-array capture an older
	// yolo left). A REPORT, printed by the caller; never an input.
	ListNotes []string

	// OverlayJSON is what to write to the overlay sidecar (JSON): on a first
	// migration the ADOPTED residue of the on-disk file — {} when there is no file,
	// or nothing in it beyond what yolo already asserts — else the accumulated
	// overlay after this boot's capture. Narrowed against computed and managed
	// either way.
	OverlayJSON []byte

	// FirstMigration reports that this boot took the §3.2 path (absent or untrusted
	// last_render): the overlay came from ADOPTING the on-disk file rather than
	// from a capture diff. The caller uses this to gate the one-time §4.7
	// orphan-file cleanup.
	FirstMigration bool

	// Repairs are the rejected values this render removed from the capture overlay
	// (rejectedvalues.go), empty on every render that removed nothing — which is every
	// render of every surface with no entry, and every second render of a repaired one.
	//
	// It is a REPORT and never an input: the repair is already reflected in Result and
	// OverlayJSON. The caller must print it, because a one-shot mutation of the user's own
	// config state that nobody announced is the defect, not the mutation.
	Repairs []Repair
}

// ComposeStateful runs the per-boot state machine for one surface and returns
// the render plus the sidecar values to persist. It never returns an error for
// a recoverable on-disk condition (corrupt/empty sidecar, corrupt or absent
// current file) — those self-heal by re-seeding or skipping capture, so a
// mangled home can never break the boot. It DOES return an error for a genuine
// programmer error (unknown codec, or a Compose failure such as a shape mismatch),
// matching Compose's fail-closed contract (§3.4).
//
// The two paths (docs/reference/config-migration-to-prism.md §3.2):
//
//	first migration (last_render absent/untrusted):   # B1: ADOPT, don't discard
//	    pure    = Compose(overlay=∅)
//	    overlay = dropNullLeaves(mergeDiff(pure, current_decoded))  # the residue
//	    overlay = dropComputedTables(overlay)                       # wholesale, per NON-EMPTY in-full table
//	steady state (last_render trusted):
//	    delta   = mergeDiff(last_render_decoded, current_decoded)
//	    overlay = mergeAccumulate(overlay, delta)                   # §3.4 tombstones
//	then BOTH paths:
//	    overlay = narrowOverlay(overlay, computed, managed)         # leaf-level
//	    overlay = retireConvergedOverlay(overlay, layers)           # no-op entries
//	    render  = Compose(overlay)
//	    write surface_path, last_render := render, overlay := overlay
//
// AT A LIST PATH (listcontrib.go, OQ-AL1) both branches capture PER ENTRY instead of
// through the merge patch, into the list-capture sidecar (ListCaptureJSON): adoption keeps
// only the entries the file holds beyond the fold below the contributions; steady state
// first converts a legacy whole-array capture (listCapture.migrate), then records this
// boot's per-entry delta and neutralizes the path before mergeDiff. See listCapture for each
// rule. A surface with no list path runs exactly the steps above and writes no fourth file.
func ComposeStateful(in StatefulInputs) (*StatefulOutput, error) {
	c, ok := codec.LookupCodec(in.Base.Surface.Codec)
	if !ok {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: unknown codec %q",
			in.Base.Surface.Agent, in.Base.Surface.Name, in.Base.Surface.Codec)
	}

	kind := in.Base.Surface.Kind()
	var repairs []Repair

	// Decide the effective overlay and whether this is a first migration.
	//
	// A last_render sidecar is TRUSTED only when it is present AND decodes to the
	// surface's own shape. Absent, empty, or undecodable last_render => first
	// migration (§3.2 / §3.3): there is no baseline, so capture is impossible and
	// the branch below ADOPTS the on-disk file instead (B1). It adopts a NARROWED
	// residue rather than the file itself, because taking the whole file would pin
	// stale bespoke output (§3.1) — that narrowing is what the two passes do.
	lastRender, lastOK := decodeKind(c, kind, in.LastRenderBytes)
	firstMigration := !in.LastRenderPresent || !lastOK

	// The current file, decoded ONCE for the three readers below: the adoption
	// residue, the steady-state capture diff, and the literal-null skeleton. It was
	// decoded separately inside each branch until the third reader arrived and
	// needed it on both.
	current, curOK := decodeKind(c, kind, in.CurrentBytes)

	// THE LIST PATHS (listcontrib.go): the live contributions' paths plus every path the
	// list-capture sidecar already names. Empty for every surface without a list — and then
	// every list step below is a no-op and ListCaptureJSON stays nil.
	var lc *listCapture
	if kind == codec.KindObject {
		lc = newListCapture(in, firstMigration)
	}

	// The layers-alone render, kept for StatefulOutput.PureBytes when the adoption branch
	// below computes one. Declared here rather than returned from that branch because only
	// one of the two branches has one at all, and nil is the answer for the other.
	var pureBytes []byte

	var overlay any
	if firstMigration {
		// B1 (⚠ DATA LOSS FIX): ADOPT the on-disk file instead of discarding it.
		//
		// This branch used to seed an EMPTY overlay, which silently destroyed every
		// agent-owned key in the file. copilot/config is the sharp case: it renders
		// with Defaults {"yolo": true} and no host layer, so a boot with an
		// absent/corrupt last_render — a fresh workspace, a deleted sidecar, an
		// interrupted migration — collapsed a file holding copilot_tokens /
		// logged_in_users / last_logged_in_user to {"yolo": true} and logged the user
		// out. Steady state recovered, which is why it went unnoticed.
		//
		// Adopting = seed the overlay with mergeDiff(pureRender, current): the
		// residue of the on-disk file after subtracting what yolo itself would
		// produce. That keeps agent-owned state and lets yolo's own layers win for
		// the keys yolo asserts, with no markers and no recursion.
		//
		// This is safe against `yolo config reset` ONLY because reset also truncates
		// the surface to the pure render (ruling 1, configReset). Without that,
		// reset → no baseline → adopt would resurrect the very edits the user asked
		// to discard, making reset a no-op. The two halves are one change.
		//
		// THE ONE-TIME ARCHIVE THIS BRANCH IS OWED IS BUILT (OQ-CO7, 2026-09-12), and it
		// is NOT built here, deliberately. This function is PURE — no file I/O, by the
		// contract at the top of this file — so the copy belongs to the caller that already
		// holds the bytes and the destination: internal/entrypoint's persistStatefulSurface,
		// through archiveAdoption, which serves the host's `own` adoption from the same line.
		// FirstMigration on StatefulOutput is how that caller learns this branch ran; keep it
		// reported, or the net loses its trigger.
		//
		// Why the net is owed HERE at all, and not only at the host: it has to differ by the
		// primitive available, and a boot has no TTY to prompt on, so a copy is the only net
		// this path can have. The narrowing below keeps the residue honest; it is not a record
		// of what the file held before. If a pass ever drops a key it should have kept, the
		// archive under <workspace>/.yolo/archive/config/ is where it is read back from.
		//
		// "Empty" is nil for a keyless surface, NOT the zero value: an empty
		// string is a real assertion that the file is empty and would win the
		// fold, blanking the render. nil means "this layer says nothing".
		overlay = emptyOverlay(kind)
		if curOK {
			if kind == codec.KindObject {
				// Subtract what a pure render would produce; keep the rest.
				pure, perr := Compose(in.Base)
				if perr != nil {
					return nil, perr
				}
				pureBytes = pure.Encoded
				pureMap, _ := decodeKind(c, kind, pure.Encoded)
				pm, _ := pureMap.(map[string]any)
				curMap, _ := current.(map[string]any)
				// A LIST PATH IS ADOPTED PER ENTRY, never as a whole array: only what the file
				// holds beyond the fold below the contributions, so a pack's entries never
				// enter the capture and an entry already in the user's file stays theirs.
				curMap, err := lc.adopt(curMap, pm)
				if err != nil {
					return nil, err
				}
				residue := dropNullLeaves(mergeDiff(pm, curMap))
				// Narrowing the residue takes TWO passes at DIFFERENT GRANULARITIES,
				// and neither subsumes the other.
				//
				// (a) WHOLESALE, here: a top-level key the COMPUTED layer holds as a
				// NON-EMPTY object that its derive DECLARED it regenerates in full
				// (ctx.in_full, CO13) is a table yolo regenerated this boot, so what sits
				// under it on disk is taken to be yolo's own previous output rather than
				// a captured edit. An EMPTY one regenerated no leaf and so claims none,
				// and one NOT DECLARED IN FULL claims only the leaves it names — which is (b)'s
				// job, not this pass's (dropComputedTables says why each half is so).
				//
				// (b) LEAF-LEVEL, after this branch: the SHARED narrowing both branches
				// run (narrowOverlay), which is the exact dual of the merge each owning
				// layer gets — it keeps a captured SIBLING inside an object-valued
				// owner, because such a sibling really does reach the file.
				//
				// (b) CANNOT SUBSUME (a), which is why both exist: dropOverriddenKeys
				// recurses into an object-valued owner and keeps every key the owner
				// lacks — and a stale entry under a computed table is exactly such a
				// key, so the leaf pass alone resurrects an MCP server dropped from
				// config (§2 principle 1, regenerate-don't-reconcile).
				//
				// Neither may be the OLD blanket drop of every top-level key the pure
				// render holds as an object. That took managed `permissions` with it,
				// so `permissions.ask` — a leaf managed does not hold, which Enforce
				// merges around — was silently lost on every adopting boot, while the
				// very next boot captured it happily. dropOverriddenKeys says why in as
				// many words: a blanket top-level key drop is "simpler and wrong".
				residue = dropComputedTables(residue, in.Base.Computed, in.Base.ComputedInFull)
				if len(residue) > 0 {
					overlay = residue
				}
			}
			// KEYLESS surfaces are deliberately NOT adopted. A raw/lines surface has
			// one "key" — the whole file — so adoption would mean "the existing file
			// wins outright", which defeats the host layer entirely: a readonly
			// host-mirrored file would freeze at whatever stale content was on disk
			// and never pick up host-side changes again. There is also no partial
			// residue to take, so nothing here is recoverable the way an object's
			// unasserted keys are. Object surfaces are where the data-loss risk lives
			// (copilot's tokens), and they get exact key-level adoption instead.
		}
	} else {
		// Steady state. Start from the persisted overlay ({} if absent — §3.3
		// case 3), then accumulate this boot's captured delta.
		overlay = parseOverlayKind(kind, in.OverlayJSON)
		// A WHOLE ARRAY an older yolo captured at what is now a list path is converted to
		// a per-entry record first, before this boot's delta (the migration).
		if lc.active() {
			if om, isMap := overlay.(map[string]any); isMap {
				var listRepairs []Repair
				om, listRepairs = RepairRejectedListRecords(in.Base.Surface, lc.recs, om)
				repairs = append(repairs, listRepairs...)
				converted, err := lc.migrate(om)
				if err != nil {
					return nil, err
				}
				overlay = converted
			} else {
				var listRepairs []Repair
				_, listRepairs = RepairRejectedListRecords(in.Base.Surface, lc.recs, nil)
				repairs = append(repairs, listRepairs...)
			}
		}
		if curOK {
			// §5: diff the on-disk file against the trusted baseline and fold the
			// delta into the durable overlay.
			//
			// For an OBJECT surface that is the RFC-7386 diff: mergeAccumulate
			// preserves null tombstones so a captured deletion persists (§3.4).
			//
			// For a KEYLESS surface (raw/lines) the same loop holds with a
			// degenerate diff: the file has one "key" — itself — so "did it
			// change" is value inequality, and the captured delta is the whole
			// edited value. Accumulate is replacement (the newest edit is the
			// overlay). Nothing else about capture differs, which is why raw
			// files get edit-survives-regeneration for free rather than needing a
			// parallel mechanism.
			if kind == codec.KindObject {
				lastMap, _ := lastRender.(map[string]any)
				curMap, _ := current.(map[string]any)
				overlayMap, _ := overlay.(map[string]any)
				// Per entry at every list path, and the path neutralized so the merge patch
				// never records it (OQ-AL1).
				var cerr error
				curMap, overlayMap, cerr = lc.capture(lastMap, curMap, overlayMap)
				if cerr != nil {
					return nil, cerr
				}
				delta := mergeDiff(lastMap, curMap)
				overlay = mergeAccumulate(overlayMap, delta)
			} else if !reflect.DeepEqual(lastRender, current) {
				overlay = current
			}
		}
		// A corrupt/absent current file (curOK false) skips capture: we bias
		// toward under-capture rather than freezing a spurious delta into the
		// never-aging overlay.
	}

	// THE ONE-SHOT REPAIR, BOTH BRANCHES (rejectedvalues.go). A value yolo itself
	// shipped and the target program cannot LOAD is removed from the decided overlay
	// here, before anything persists it, so `defaults` fills the key by absence.
	//
	// It has to be inside this function rather than in the caller, because BOTH states
	// that can freeze such a value are decided above and neither is visible from
	// outside: the steady-state branch's accumulated overlay, and the first-migration
	// branch's ADOPTED RESIDUE, which is derived from the on-disk file here and is not
	// an input. Repairing the caller's `overlay` bytes would miss adoption entirely, and
	// repairing the caller's `current` bytes would be actively wrong — steady-state
	// capture would then diff the stripped file against a last_render that still holds
	// the value and record a null TOMBSTONE, freezing the key ABSENT forever, which is
	// the same bug one value over.
	//
	// Before the narrowing passes below so they see an already-repaired overlay, and
	// reported on StatefulOutput because this function writes nothing.
	if kind == codec.KindObject {
		if overlayMap, isMap := overlay.(map[string]any); isMap {
			repairs = append(repairs, RepairRejected(in.Base.Surface, MapObject(overlayMap))...)
			if lc.active() {
				var listRepairs []Repair
				overlayMap, listRepairs = RepairRejectedListRecords(in.Base.Surface, lc.recs, overlayMap)
				repairs = append(repairs, listRepairs...)
				overlay = overlayMap
			}
		}
	}

	// ONE RULE, BOTH BRANCHES: strip from the decided overlay everything a
	// higher-ranking layer — computed, then managed — will override anyway.
	// Adoption has narrowed its residue against managed since B1; steady-state
	// capture did NOT, and neither branch narrowed against computed at all, so
	// every boot that saw such a key edited on disk folded it into the sidecar as
	// permanent, un-actionable noise.
	//
	// It runs on the ACCUMULATED overlay rather than on the incoming delta, and
	// that is the whole point: narrowing the delta would only stop NEW
	// contamination and leave every sidecar already carrying dead keys dirty
	// forever. Narrowing after the accumulate makes the store SELF-HEALING — a
	// sidecar an older yolo wrote is canonicalized on the next boot.
	overlay = narrowOverlay(kind, overlay, in.Base.Computed, in.Base.Surface.Managed)
	// The same rule for the list records: one under a computed or managed layer that
	// replaces its path is provably dead.
	lc.narrow(in.Base.Computed, in.Base.Surface.ManagedMap())
	// A capture preserves a divergence, not a historical choice. When a declared
	// layer converges with a captured value, retaining that value makes a no-op
	// sidecar silently become a future override if the declaration moves again.
	// Canonicalize it away while the engine has both the capture and every lower
	// layer available; the next render then follows the declaration normally.
	// The literal-null channel is another capture representation. Include it in
	// both comparisons below so a self-sustaining literal null is not mistaken
	// for a redundant sidecar tombstone.
	base := in.Base
	base.ListCapture = lc.records()
	if kind == codec.KindObject && curOK {
		if curMap, ok := current.(map[string]any); ok {
			base.LiteralNulls = literalNullSkeleton(curMap)
		}
	}
	var err error
	overlay, err = retireConvergedOverlay(base, kind, overlay)
	if err != nil {
		return nil, err
	}

	// Render with the decided overlay. Compose owns decode/merge/enforce/encode
	// and is the exact engine `yolo config render` uses (§6).
	base.Overlay = overlay
	// THE ONE VALUE THE OVERLAY CANNOT CARRY, read off the file instead
	// (literalnull.go; docs/design/config-ownership-and-promotion.md §11, OQ-CO12).
	// Read on BOTH branches, not only on adoption: the mark lives in the file
	// rather than in a sidecar, so every render has to re-read it or the second
	// one drops what the first put back.
	//
	// ⚠ It is set on `base`, AFTER the adoption branch above computed its own
	// Compose(in.Base). That pure render must stay layers-alone — it is what
	// StatefulOutput.PureBytes reports and what entrypoint.archiveAdoption compares
	// against to ask "is this file anything other than yolo's own output?" — and a
	// null of the user's is precisely something other.
	res, err := Compose(base)
	if err != nil {
		return nil, err
	}

	// The overlay sidecar to persist. Marshal deterministically. On a first
	// migration this is the ADOPTED residue, not `{}` — that was the pre-B1 rule,
	// and the whole point of B1 is that the residue is the only surviving record of
	// the agent-owned keys the file held (StatefulOutput.OverlayJSON). It is `{}`
	// only when the file held nothing beyond what yolo already asserts.
	overlayJSON, err := marshalOverlay(overlay)
	if err != nil {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: marshal overlay: %w",
			in.Base.Surface.Agent, in.Base.Surface.Name, err)
	}

	listJSON, err := lc.marshal()
	if err != nil {
		return nil, fmt.Errorf("agentcfg: surface %s/%s: marshal list capture: %w",
			in.Base.Surface.Agent, in.Base.Surface.Name, err)
	}

	return &StatefulOutput{
		Result:          res,
		LastRenderBytes: res.Encoded,
		OverlayJSON:     overlayJSON,
		ListCaptureJSON: listJSON,
		ListNotes:       lc.notesFor(in.Base, overlay),
		FirstMigration:  firstMigration,
		PureBytes:       pureBytes,
		Repairs:         repairs,
	}, nil
}

// decodeKind decodes bytes with the surface codec and reports success only when
// the result is non-empty input that decodes to the surface's own shape. Empty
// input, a decode error, or a shape mismatch all report ok=false — the callers
// treat every one as "cannot trust / cannot capture", which is the conservative
// choice for both the last_render baseline and the current file.
//
// The kind check is what makes this usable for raw/lines surfaces: an object-only
// check reported ok=false for every raw file, which silently disabled capture for
// them (an edit to a raw surface was quietly discarded every boot).
func decodeKind(c codec.Codec, kind codec.Kind, data []byte) (any, bool) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, false
	}
	decoded, err := c.Decode(data)
	if err != nil {
		return nil, false
	}
	if !kind.Matches(decoded) {
		return nil, false
	}
	return decoded, true
}

// dropComputedTables removes from an adopted first-migration residue every
// top-level key the COMPUTED layer holds as a NON-EMPTY OBJECT that its derive DECLARED
// it regenerates in full (inFull — the ctx.in_full sentinel, CO13,
// docs/design/config-ownership-and-promotion.md). The bet it makes is that what sits
// under such a key on disk is yolo's own output from a previous boot rather than agent
// state; last_render — which normally disambiguates the two — is exactly what a first
// migration is missing, so the derive's declaration is what the bet rests on.
//
// Without it, adoption resurrects dropped yolo-owned entries and breaks §2
// principle 1 ("regenerate, don't reconcile"): an MCP server removed from config
// would come back, because the stale entry sits under mcp_servers, a table yolo
// computes wholesale. Verified against the real surfaces — codex's
// mcp_servers.staleServer, opencode's mcp.staleServer and mise's stale baked
// [tools] runtimes all resurrected before this pass existed, and each of those tables
// is now declared in full at its source (the shipped derives; ConfigureMisePrism).
//
// A TABLE NOT DECLARED IN FULL IS LEFT TO THE LEAF PASS, and that is CO13's whole fix. A derive
// does not always regenerate a table it returns: packs/claude/derive.lua asserts
// ENABLE_LSP_TOOL inside claude/settings' `env` and can never own the rest of a user's
// environment, so "regenerates it in full" is false there BY CONSTRUCTION. This pass used
// to take every non-empty computed table whole, and an adopting boot with an LSP
// configured dropped the agent's other variables with it. Now such a table is kept here,
// and narrowOverlay removes the leaves the computed layer names — exactly the leaves yolo
// asserted, since a computed table's key set IS the asserted set (a ctx.tombstone decodes
// to a present key with a nil value, so even a removal counts as asserted).
//
// The declaration exists because the SHAPE could not carry this. Every discriminator a
// computed table offers was measured (2026-09-20) to flip on configuration rather than on
// intent: the key set, the presence of a tombstone, value shape (mise's [tools] holds
// scalars and codex's mcp_servers holds objects, and both are regenerated in full), and
// whether a lower layer also contributes. So the derive that knows says so.
//
// AN EMPTY COMPUTED TABLE ASSERTS NOTHING, SO IT DROPS NOTHING, declared or not — the
// 2026-09-20 ruling (§6.3.1), which predates the declaration and is not superseded by it.
// The whole bet rests on the leaves yolo regenerated; `{}` regenerated none, so there is
// nothing it can claim to be the previous output OF, and the drop would be an unbacked
// deletion of the user's own data. It is `mise use -g neovim` on a jail with no
// YOLO_MISE_TOOLS pin: the computed [tools] table is present-and-empty (the render emits
// it so last_render stays trusted), and the tool the user added by hand went with it.
//
// NOT WHOLESALE AGAINST THE PURE RENDER, which is what this replaced. The pure
// render holds managed's `permissions` as an object too, so the blanket drop took
// Claude's own `permissions.ask` with it: an adopting boot silently discarded the
// agent's permission list, while a steady-state boot kept it. Everything below
// computed — managed, host, workspace, defaults — merges key-by-key, so it gets the
// leaf-level pass (narrowOverlay) instead.
//
// A key this pass does not take is untouched here: either yolo knows nothing about it,
// in which case it is agent state and IS adopted (the copilot_tokens case this whole
// branch exists for), or an owning layer asserts leaves of it and the leaf-level pass
// drops exactly those.
func dropComputedTables(residue map[string]any, computed any, inFull []string) map[string]any {
	computedMap, ok := computed.(map[string]any)
	if !ok || len(inFull) == 0 {
		return residue
	}
	declared := make(map[string]bool, len(inFull))
	for _, k := range inFull {
		declared[k] = true
	}
	out := make(map[string]any, len(residue))
	for k, v := range residue {
		// len(sub) > 0 is the ruling, not a nil guard: an empty table regenerated no
		// leaf, so it can claim no leaf on disk as its own previous output.
		if sub, isObj := computedMap[k].(map[string]any); isObj && len(sub) > 0 && declared[k] {
			continue
		}
		out[k] = v
	}
	return out
}

// narrowOverlay removes from a decided overlay every entry a HIGHER-RANKING
// layer will override anyway, so the sidecar holds only captured edits that can
// actually reach the written file.
//
// TWO layers outrank the capture overlay, and both disqualify a capture for the
// same reason:
//
//   - COMPUTED folds directly above the overlay (compose.go). It is yolo's
//     per-boot regenerated data — the reconciled MCP-server table, claude's
//     LSP-driven enabledPlugins toggles and env.ENABLE_LSP_TOOL, mise's injected
//     [tools] pins — and the §4 slot exists precisely "so it wins over a stale
//     in-jail edit to the same key (§2 principle 1, regenerate-don't-reconcile)".
//   - MANAGED is re-asserted AFTER the fold (enforceManaged), so it wins the
//     file unconditionally.
//
// A capture either layer overrides changes nothing. It only sits in the sidecar
// as permanent noise, and `yolo config diff` reports a phantom "edit" the user
// cannot act on. (A stale managed VALUE on disk, e.g. codex's
// approval_policy=on-request from an old boot, is exactly this case.) Measured on
// a live jail, this was most of what the store held: mise/config's entire `tools`
// capture, codex/config's entire `mcp_servers`, opencode/config's entire `mcp`.
//
// THE COMPETING SEMANTIC, deliberately abandoned: retaining the captured edit
// would mean that if the owning layer later STOPS supplying the key, the old edit
// silently activates. That was rejected because the pending edit is invisible —
// nothing tells you it is queued — and it can sit for months. Both live examples
// are hazards rather than conveniences: for claude/settings the managed key is
// `permissions`, so activation would restore a stale permission grant nobody
// remembers making; and for computed, deleting an MCP server from your config
// would silently resurrect it from a capture taken while it still existed.
//
// KEYLESS surfaces (raw/lines) are covered by the same rule rather than being
// exempt. They have one "key" — the whole file — so a non-nil computed layer
// replaces the rendered value and enforceManaged replaces it again for managed;
// either way a captured whole-file edit is dead. No pack yolo ships declares
// managed or computed on a keyless surface today, but Surface.Managed and
// Inputs.Computed are both `any` precisely so a surface CAN pin a whole file, so
// the case is representable and is handled here rather than argued away.
func narrowOverlay(kind codec.Kind, overlay, computed, managed any) any {
	// Applied in fold order. Order does not change the result — each pass only
	// removes entries — but it reads the way the layers stack.
	overlay = narrowAgainst(kind, overlay, computed)
	return narrowAgainst(kind, overlay, managed)
}

// narrowAgainst is one pass of narrowOverlay against a single owning layer.
func narrowAgainst(kind codec.Kind, overlay, owner any) any {
	if owner == nil {
		return overlay
	}
	ownerMap, ownerIsObj := owner.(map[string]any)
	overlayMap, overlayIsObj := overlay.(map[string]any)
	if ownerIsObj && overlayIsObj {
		return dropOverriddenKeys(overlayMap, ownerMap)
	}
	// Whole-value override: the owner replaces the entire rendered value, so
	// nothing the overlay holds can survive it.
	return emptyOverlay(kind)
}

// dropOverriddenKeys returns m without the entries an OWNING layer will override
// anyway. It is the exact dual of the merge that layer gets — and has to be,
// because an OBJECT-valued owner merges key-by-key, so a captured SIBLING inside
// it really does reach the file and is a real edit. claude/settings is the live
// case: managed asserts `permissions.defaultMode` and friends, while Claude's own
// `permissions.ask` is untouched by it and must survive. A blanket top-level key
// drop would be simpler and wrong — it would discard the agent's permission list
// on every boot.
//
// THE LINE THIS DRAWS: drop only what is PROVABLY dead from the (overlay, owner)
// pair ALONE. The lower layers — host, workspace, another pack's config-overlay —
// are not visible here, so anything whose deadness depends on them is KEPT. That
// is why a null tombstone under an object-valued owner survives (see below):
// keeping a redundant tombstone is noise, dropping a live one is data loss, and
// this repo has already paid for that class once (B1, the copilot OAuth wipe).
//
// A nested object emptied by the recursion is itself dropped, so a fully-owned
// subtree does not survive as a meaningless {}.
func dropOverriddenKeys(m, owner map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		ov, owned := owner[k]
		if !owned {
			out[k] = v
			continue
		}
		oSub, oIsObj := ov.(map[string]any)
		if !oIsObj {
			// The owner replaces the whole value at k (or, for a null, deletes it),
			// so nothing the overlay holds at k — a value or a tombstone — survives.
			continue
		}
		if v == nil {
			// A TOMBSTONE under an object-valued owner is still LIVE: it erases what
			// the lower layers put at k, leaving only the owner's own keys. Whether
			// any lower layer contributes is not knowable here, so it is kept.
			out[k] = v
			continue
		}
		vSub, vIsObj := v.(map[string]any)
		if !vIsObj {
			// A non-object under an object owner is discarded by the merge (RFC 7386:
			// a non-object target under an object patch is treated as empty).
			continue
		}
		if inner := dropOverriddenKeys(vSub, oSub); len(inner) > 0 {
			out[k] = inner
		}
	}
	return out
}

// dropNullLeaves returns m without any null-valued entry, recursively. Used to
// strip RFC-7386 tombstones out of an adopted first-migration overlay: the overlay
// must add the agent's own keys without deleting the ones yolo asserts. A nested
// object that BECOMES empty after stripping is itself dropped, so an all-tombstones
// subtree does not survive as a meaningless {}.
//
// ⚠ AN OBJECT THAT WAS ALREADY EMPTY IS KEPT, and the difference is the whole of
// §11's first measured bug (docs/design/config-ownership-and-promotion.md §11,
// OQ-CO12): `{}` on the way IN is a value the user wrote — `"mcpServers": {}` is
// "explicitly none", which an absent key does not say — while `{}` on the way OUT
// is this function's own residue. Conflating them deleted the user's key on every
// `assert` -> `own` switch, and no loss field named it.
//
// THE TWO ARE DISTINGUISHABLE HERE WITHOUT A HEURISTIC, which is why the fix is a
// length check and not a guess. m is mergeDiff's patch, and a patch cannot hold an
// empty object for any other reason: diffValue emits an ADDED key's whole subtree
// verbatim (so `{}` in means `{}` in the file), and for a key BOTH sides hold it
// emits nothing at all when the recursion finds no difference — `changed` is
// `len(patch) > 0`, so an empty sub-patch is never recorded. An empty object
// arriving here is therefore always the user's own value.
func dropNullLeaves(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		switch t := v.(type) {
		case nil:
			continue
		case map[string]any:
			if len(t) == 0 {
				out[k] = t // the user's own `{}` — see above
				continue
			}
			if inner := dropNullLeaves(t); len(inner) > 0 {
				out[k] = inner
			}
		default:
			out[k] = v
		}
	}
	return out
}

// emptyOverlay is the "no captured edits" overlay for a surface kind.
//
// For an object surface that is `{}` — an empty merge patch, which changes
// nothing. For a keyless surface it is nil, NOT the zero value: Compose treats a
// non-nil keyless layer as a real assertion that wins the fold, so an empty
// string overlay would blank the file instead of deferring to the layers below.
func emptyOverlay(kind codec.Kind) any {
	if kind == codec.KindObject {
		return map[string]any{}
	}
	return nil
}

// retireConvergedOverlay removes every captured assertion that produces the
// same composed configuration as the layers alone. It asks Compose rather than
// comparing values directly: a nested merge patch can be a no-op even when its
// top-level value is not byte-for-byte equal to the corresponding lower-layer
// object.
func retireConvergedOverlay(base Inputs, kind codec.Kind, overlay any) (any, error) {
	pure, err := Compose(base)
	if err != nil {
		return nil, err
	}
	if kind != codec.KindObject {
		candidate := base
		candidate.Overlay = overlay
		withOverlay, err := Compose(candidate)
		if err != nil {
			return nil, err
		}
		if reflect.DeepEqual(pure.Config, withOverlay.Config) {
			return emptyOverlay(kind), nil
		}
		return overlay, nil
	}

	entries, ok := overlay.(map[string]any)
	if !ok || len(entries) == 0 {
		return overlay, nil
	}
	kept := make(map[string]any, len(entries))
	for key, value := range entries {
		candidate := base
		candidate.Overlay = map[string]any{key: value}
		withEntry, err := Compose(candidate)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(pure.Config, withEntry.Config) {
			kept[key] = value
		}
	}
	return kept, nil
}

// parseOverlayKind decodes the overlay sidecar JSON for a surface of kind,
// defaulting to the empty overlay for absent or undecodable content (§3.3: a
// dangling overlay is not trusted). The overlay is always JSON regardless of the
// surface codec (see file header), so a raw surface's captured text is stored as
// a JSON string and a lines surface's as a JSON array.
func parseOverlayKind(kind codec.Kind, data []byte) any {
	if len(bytes.TrimSpace(data)) == 0 {
		return emptyOverlay(kind)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil || v == nil {
		return emptyOverlay(kind)
	}
	// A sidecar whose shape doesn't match the surface is as untrustworthy as one
	// that won't parse (e.g. the surface's codec changed between boots).
	if !kind.Matches(v) {
		return emptyOverlay(kind)
	}
	return v
}

// marshalOverlay serializes the overlay to stable, indented JSON (sorted keys
// via encoding/json), with a nil object overlay rendering as `{}`. Null
// tombstones survive the round-trip — that is the whole reason the overlay is
// JSON and not the surface codec. A nil keyless overlay marshals to `null`,
// which parseOverlayKind reads back as "no captured edits".
func marshalOverlay(overlay any) ([]byte, error) {
	if m, ok := overlay.(map[string]any); ok && m == nil {
		overlay = map[string]any{}
	}
	return json.MarshalIndent(overlay, "", "  ")
}

// ── per-entry capture at list paths ────────────────────────────────────────────────────

// listCapture is ComposeStateful's per-entry capture state for one render of one object
// surface (listcontrib.go for the rule, OQ-AL1 for the ruling). A nil *listCapture — a
// keyless surface — is a valid receiver for every method and does nothing, which is what
// keeps a surface without lists byte-identical to before the kind existed.
type listCapture struct {
	in     StatefulInputs
	paths  []string            // sorted: live contribution paths ∪ sidecar keys
	tokens map[string][]string // path → its parsed tokens
	recs   map[string]ListRecord
	// inserted is the insert record (StatefulInputs.InsertRecordJSON), read on a first
	// migration only: the entries adoption must not take for the user's.
	inserted map[string]ListInsertRecord

	below   map[string]any // B, the fold below the capture WITHOUT contributions (lazy)
	belowOK bool

	converted []string // paths whose legacy whole-array capture this render converted
	notes     []string // edits this render could not keep per entry (reorderNote)
}

// newListCapture resolves the sticky list-path set and seeds the records from the sidecar.
// A first migration ignores the sidecar's RECORDS, exactly as it ignores the overlay
// sidecar (a dangling capture cannot be trusted without its baseline), but keeps its KEYS:
// a path the sidecar names stays a list path either way.
func newListCapture(in StatefulInputs, firstMigration bool) *listCapture {
	sidecar := ParseListCapture(in.ListCaptureJSON)
	lc := &listCapture{in: in, tokens: map[string][]string{}, recs: map[string]ListRecord{}}
	add := func(p string) {
		if _, seen := lc.tokens[p]; seen {
			return
		}
		t, err := jsonptr.Parse(p)
		if err != nil || len(t) == 0 {
			return
		}
		lc.tokens[p] = t
		lc.paths = append(lc.paths, p)
	}
	for _, p := range ListPaths(in.Base.Lists) {
		add(p)
	}
	for p := range sidecar {
		add(p)
	}
	sort.Strings(lc.paths)
	if !firstMigration {
		for p, r := range sidecar {
			lc.recs[p] = r
		}
	} else {
		lc.inserted = ParseListInsertRecords(in.InsertRecordJSON)
	}
	return lc
}

func (lc *listCapture) active() bool { return lc != nil && len(lc.paths) > 0 }

// fold returns B: defaults < host < workspace < config-overlay, with no list contribution,
// no capture, no computed and no managed. It is the baseline adoption and migration measure
// against, because it is what the file would hold at a list path if no pack contributed
// and no edit had been captured — so an entry the user's file holds beyond it is theirs.
func (lc *listCapture) fold() (map[string]any, error) {
	if lc.belowOK {
		return lc.below, nil
	}
	b := lc.in.Base
	b.Lists, b.Overlay, b.ListCapture, b.Computed, b.LiteralNulls = nil, nil, nil, nil, nil
	b.Surface.Managed = nil
	res, err := Compose(b)
	if err != nil {
		return nil, err
	}
	lc.below, lc.belowOK = res.ConfigMap(), true
	return lc.below, nil
}

// arrayAt is the array at tokens, or nil when the path is absent, blocked or not an array.
func arrayAt(m map[string]any, tokens []string) []any {
	v, st, _ := walkPath(m, tokens)
	if st != pathFound {
		return nil
	}
	a, _ := v.([]any)
	return a
}

// neutralize returns cur with the value at tokens replaced by ref's (or removed when ref
// has none, pruning every ancestor the removal empties that ref does not hold either), so
// a merge diff of ref against the result records nothing at the path. The caller only
// neutralizes a path cur holds as an array, so every ancestor in cur is an object.
func neutralize(cur, ref map[string]any, tokens []string) map[string]any {
	if v, st, _ := walkPath(ref, tokens); st == pathFound {
		return setPath(cur, tokens, v)
	}
	return deletePath(cur, tokens, func(prefix []string) bool {
		_, st, _ := walkPath(ref, prefix)
		return st != pathFound
	})
}

// adopt is the first-migration branch at the list paths: a list path the current file holds
// as an array adopts ONLY ADDS, measured against B — so a pack's entries never enter the
// capture, and an entry the user's own file already held that a pack also contributes is
// adopted as theirs (it survives the pack being dropped: "an entry already in the user's
// file that a pack also contributes stays the user's",
// docs/reference/config-migration-to-prism.md#list-paths-capture-per-entry). The path is
// neutralized against the pure render so the residue never adopts the whole array.
//
// An INSERT RECORD beside the surface (StatefulInputs.InsertRecordJSON) narrows that: an entry
// it says yolo inserted is not adopted, and one it says the user declined is adopted as a
// removal. The host keeps one under both contracts, so an ownership switch loses nothing.
//
// The cost, stated: with no insert record (every jail), after a lost last_render, entries a
// pack contributed on an earlier render read as the user's too — nothing distinguishes them
// from the user's own once the baseline is gone.
func (lc *listCapture) adopt(cur, pure map[string]any) (map[string]any, error) {
	if !lc.active() {
		return cur, nil
	}
	for _, p := range lc.paths {
		t := lc.tokens[p]
		v, st, _ := walkPath(cur, t)
		arr, isArr := v.([]any)
		if st != pathFound || !isArr {
			continue // absent, or a non-array the residue adopts whole (a whole-value capture)
		}
		b, err := lc.fold()
		if err != nil {
			return nil, err
		}
		ins := lc.inserted[p]
		lc.recs[p] = ListRecord{
			Add:    subtractEntries(subtractEntries(arr, arrayAt(b, t)), ins.Inserted),
			Remove: subtractEntries(ins.Declined, arr),
		}
		cur = neutralize(cur, pure, t)
	}
	return cur, nil
}

// migrate converts a WHOLE ARRAY the capture overlay holds at a list path — left by the old
// whole-array capture, which mergeAccumulate stores as a replacement — into a per-entry
// record: add = O − B and remove = B − O, O deleted from the overlay with any {} parent it
// leaves. B rather than the last render, because the last render at the path IS O: the
// capture won.
//
// What the conversion loses, stated because it is lossy by construction: O's ORDER relative
// to B (B's order wins; adds append in O's order), DUPLICATES within O, and O's ABSOLUTE PIN
// — from here on a lower-layer change at the path shows through, where the whole array
// masked it. A captured TOMBSTONE or non-array is not converted: it stays a whole-value
// capture (pack-system.md#config-list-precedence) and notesFor names it.
func (lc *listCapture) migrate(overlay map[string]any) (map[string]any, error) {
	for _, p := range lc.paths {
		t := lc.tokens[p]
		v, st, _ := walkPath(overlay, t)
		o, isArr := v.([]any)
		if st != pathFound || !isArr {
			continue
		}
		b, err := lc.fold()
		if err != nil {
			return nil, err
		}
		base := arrayAt(b, t)
		contributed := ContributedEntries(lc.in.Base.Lists, p)
		add := subtractEntries(subtractEntries(o, base), contributed)
		remove := subtractEntries(base, o)
		lc.recs[p] = lc.recs[p].accumulate(add, remove)
		overlay = deletePath(overlay, t, pruneAlways)
		lc.converted = append(lc.converted, p)
	}
	return overlay, nil
}

// capture is the steady-state branch at the list paths: compare last_render@P with
// current@P.
//
//   - A FILE ARRAY at P over an ARRAY: record per-entry adds and removes by whole-value
//     presence, accumulated symmetrically, and neutralize P so the merge patch never records
//     it. What presence cannot express — a REORDER of the surviving entries, or a
//     de-duplication — is not kept, and the boot says so (reorderNote) rather than silently
//     putting the old order back.
//   - A FILE ARRAY REAPPEARING at P over a WHOLE-VALUE CAPTURE — a captured deletion or
//     non-array edit at P, or at an ANCESTOR of P (a deleted or replaced parent object): the
//     capture is cleared from the overlay, and the array is measured against the list that
//     capture was HIDING (hidden) — the assembled list with the path's record applied — not
//     against []. So what the user wrote is what renders: a contributed entry they had
//     removed stays removed, the owner's entries they left out are removals, and a pack's
//     entries never become the user's adds.
//   - A FILE ARRAY over nothing (no capture hides anything): the baseline is [], so every
//     entry in the file is recorded as added.
//   - The array DELETED, or REPLACED by a non-array: a whole-value edit, which capture keeps
//     the power to make (pack-system.md#config-list-precedence). The merge patch records it
//     (a tombstone, or the value) and it outranks every contribution. P's
//     record is KEPT: applyListRecords skips a path the overlay replaces, so it is inert while
//     the capture stands, and it is what hidden applies if the array comes back — a per-entry
//     removal the user made before the deletion is still theirs.
//   - A path the FILE blocks with a non-object ancestor is left to the merge patch.
//
// So an array EMPTIED in-jail is per entry, and the consequence is deliberate: every entry
// present then stays removed, while an entry first contributed LATER still appears.
func (lc *listCapture) capture(last, cur, overlay map[string]any) (map[string]any, map[string]any, error) {
	if !lc.active() {
		return cur, overlay, nil
	}
	for _, p := range lc.paths {
		t := lc.tokens[p]
		old, oldSt, _ := walkPath(last, t)
		nw, newSt, _ := walkPath(cur, t)
		if newSt == pathBlocked {
			continue
		}
		oldArr, oldIsArr := old.([]any)
		newArr, newIsArr := nw.([]any)
		if newSt != pathFound || !newIsArr {
			continue // absent or a non-array: the merge patch's whole-value edit, record kept
		}
		var base []any
		switch {
		case oldSt == pathFound && oldIsArr:
			base = oldArr
			if note := reorderNote(lc.id(), p, oldArr, newArr); note != "" {
				lc.notes = append(lc.notes, note)
			}
		default:
			if depth := replacesPathDepth(overlay, t); depth >= 0 {
				overlay = deletePath(overlay, t[:depth+1], pruneAlways)
				hidden, err := lc.hidden(overlay)
				if err != nil {
					return nil, nil, err
				}
				base = arrayAt(hidden, t)
			}
		}
		lc.recs[p] = lc.recs[p].accumulate(subtractEntries(newArr, base), subtractEntries(base, newArr))
		cur = neutralize(cur, last, t)
	}
	return cur, overlay, nil
}

// hidden is the render with the given overlay and the current records: at a path whose
// whole-value capture the caller just removed from overlay, it is the list that capture was
// hiding from the file.
func (lc *listCapture) hidden(overlay map[string]any) (map[string]any, error) {
	b := lc.in.Base
	b.Overlay, b.ListCapture, b.LiteralNulls = overlay, lc.recs, nil
	res, err := Compose(b)
	if err != nil {
		return nil, err
	}
	return res.ConfigMap(), nil
}

func (lc *listCapture) id() string { return lc.in.Base.Surface.Agent + "/" + lc.in.Base.Surface.Name }

// reorderNote reports an in-jail edit per-entry capture cannot keep: the entries both
// versions hold, in a different relative order or with a different number of repeats. ""
// when the surviving entries read the same in both — an append or a removal keeps order.
func reorderNote(id, path string, old, cur []any) string {
	kept := func(a, in []any) []string {
		var out []string
		for _, e := range a {
			if containsEntry(in, e) {
				out = append(out, entryKey(e))
			}
		}
		return out
	}
	if reflect.DeepEqual(kept(old, cur), kept(cur, old)) {
		return ""
	}
	return fmt.Sprintf("%s: an in-jail reorder or de-duplication of the list at %s is not kept — a "+
		"config-list path captures which entries it holds, not their order or repeats, so the "+
		"rendered order is restored", id, path)
}

// narrow drops every record a computed or managed layer makes dead by holding its path, or
// an ancestor, as a non-object — the dropOverriddenKeys rule for list records. The path
// stays a list path (the set is sticky); only the record empties.
func (lc *listCapture) narrow(computed any, managed map[string]any) {
	if !lc.active() {
		return
	}
	for _, p := range lc.paths {
		t := lc.tokens[p]
		if replacesPath(computed, t) || replacesPath(managed, t) {
			lc.recs[p] = ListRecord{}
		}
	}
}

// records is what Compose applies (Inputs.ListCapture); nil for a surface with no list path.
func (lc *listCapture) records() map[string]ListRecord {
	if !lc.active() {
		return nil
	}
	return lc.recs
}

// marshal is the sidecar to persist: an entry for EVERY list path, empty or not, so the next
// layer-less capture still knows each one; nil — write nothing — when there is none.
func (lc *listCapture) marshal() ([]byte, error) {
	if !lc.active() {
		return nil, nil
	}
	return marshalListCapture(lc.paths, lc.recs)
}

// notesFor reports what this render could not treat per entry, and what it converted.
func (lc *listCapture) notesFor(base Inputs, overlay any) []string {
	if !lc.active() {
		return nil
	}
	id := base.Surface.Agent + "/" + base.Surface.Name
	notes := append([]string(nil), lc.notes...)
	for _, p := range lc.converted {
		notes = append(notes, fmt.Sprintf("%s: converted the whole-array capture at %s into "+
			"per-entry capture; its order relative to the layers below, duplicate entries and "+
			"its pin against lower-layer changes are not kept", id, p))
	}
	overlayMap, _ := overlay.(map[string]any)
	for _, p := range ListPaths(base.Lists) {
		if len(ContributedEntries(base.Lists, p)) == 0 {
			continue
		}
		if _, st, _ := walkPath(overlayMap, lc.tokens[p]); st == pathAbsent {
			continue
		}
		notes = append(notes, fmt.Sprintf("%s: a captured in-jail deletion or non-array edit at %s "+
			"replaces the whole list, so the config-list entries for it are masked — "+
			"`yolo config reset %s` discards the capture", id, p, id))
	}
	return notes
}

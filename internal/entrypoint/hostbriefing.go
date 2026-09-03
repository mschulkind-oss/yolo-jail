package entrypoint

// hostbriefing.go is the host-target BRIEFING render: yolo COMPOSES the destination file
// outright, exactly as the jail does.
//
// Maintainer ruling, 2026-08-04: *"I want to claim briefings as fully generated and controlled.
// If there's something on the host already at apply, we archive it. This is the convention, and
// I will adapt my packs. If you want host instructions, it's like skills, make a pack."*
//
// WHAT THIS REPLACED, AND WHY IT IS THE SIMPLIFICATION. This file used to maintain a
// per-pack DELIMITED MANAGED BLOCK (`<!-- yolo:pack-briefing begin (<pack>) -->`) inside the
// user's own file, with a parser that fail-closed on a crossed or unterminated marker. The
// block existed to solve "source and destination are the same file, so an append grows without
// bound and a wholesale write eats the user's prose" — a framing that ACCEPTED the premise the
// ruling rejects: that a briefing file is jointly owned. Once yolo owns it outright the problem
// does not exist, and one mechanism (wholesale composition) replaces two.
//
// The decisions this encodes:
//   - COMPOSED, in pack order, with the SAME provenance vocabulary the jail emits
//     (`<!-- from pack: NAME -->`, jailcontent.ComposePackBriefings). One file per destination,
//     however many packs contribute to it, so the destination's content is a function of the
//     pack SET rather than of which pack was rendered last.
//   - THE USER'S PROSE MOVES, it is not archived away (§6a, amended). A hand-written
//     destination is migrated into ~/.config/yolo-jail/local/AGENTS.md, where the LOCAL PACK
//     composes it back into every destination — so the migration is behavior-PRESERVING rather
//     than merely non-destructive. Archiving is the FALLBACK for prose that cannot be moved.
//     See MigrateHostBriefings.
//   - A FIRST APPLY THAT ADOPTS A DESTINATION IS CONFIRMED. Taking wholesale ownership of a
//     file the user wrote is a one-way door, and the CLI's confirmHostLosses gate is where it
//     is opened. This package reports what WOULD be adopted (HostBriefingAdoptions) and never
//     decides.
//   - RETIREMENT LEAVES NO ORPHAN. When the last pack contributing to a destination is
//     dropped, yolo no longer owns that file, so PruneHostBriefings archives it rather than
//     leaving a generated file behind with nobody to regenerate it.
//   - The composed body carries the PACKS' prose, not yolo's environment briefing.
//     BriefingContent's body describes a jail (/workspace, no sudo, the shims, the loopholes);
//     at the host there is no jail to describe. `after: "host:…"` is likewise inert here — it
//     exists to pull the user's file INTO a jail staging copy, and the host no longer preserves
//     the user's file in place to pull from.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostBriefingProvenance is the per-section header, shared verbatim with the jail's
// ComposePackBriefings. Prose has no name to disambiguate it, so attribution is the only thing
// that lets a reader of a merged file tell which pack a surprising rule came from — the ruling
// requires it for exactly the union case below.
func hostBriefingProvenance(pack string) string { return "<!-- from pack: " + pack + " -->" }

// HostBriefingRequest carries what a host briefing render needs beyond the packs: the ownership
// record and the archive, both owned by the caller (the CLI owns path layout; this package stays
// pointed at whatever dirs it is given so tests can use temps). Same shape and same reason as
// HostFilesRequest.
type HostBriefingRequest struct {
	// Manifest records which destinations yolo composed, so a later apply can tell its own
	// output from a file the user wrote. Required for the adoption gate to mean anything:
	// without it EVERY destination reads as the user's, which is safe (nothing is adopted
	// without a confirmation) but means yolo can never regenerate its own file unprompted.
	Manifest *hostskills.Manifest
	// ArchiveRoot is where prose that cannot be MOVED is archived, and where a retired
	// destination goes.
	ArchiveRoot hostskills.ArchiveRoot
	// Stamp names the archive generation, so one apply groups its moves together.
	Stamp string
	// LocalPackAGENTS is the absolute path of the local pack's own AGENTS.md
	// (paths.LocalPackDir()/AGENTS.md) — where a pre-existing destination's prose MOVES to.
	// Empty disables the move and falls back to archiving, which is what a caller with no
	// resolvable local pack location should do rather than guess one.
	LocalPackAGENTS string
	// PackSetComplete asserts that every pack the config NAMES resolved this run. Only then
	// can PruneHostBriefings conclude that a destination has no contributor left.
	//
	// It is the briefing analogue of pruneDroppedPackOutput keying on `configured` rather than
	// `active`, and it exists because a fetched pack with an unreachable remote resolves to
	// NOTHING: its prose vanishes from the composition, so an owned destination it was the sole
	// contributor to looks orphaned the first time the user is offline. Under the old delimited
	// block that mistake self-healed for free (the block re-rendered from prose inside the
	// pack); a WHOLESALE destination archived on a bad guess costs the user a trip to the state
	// dir, so the threshold has to match the sharper one.
	//
	// FALSE IS THE FAIL-CLOSED ZERO VALUE: a caller that does not answer retires nothing.
	PackSetComplete bool
}

// HostBriefingDestination is one composed destination: where it goes and which packs contribute.
type HostBriefingDestination struct {
	// Path is the absolute path in the user's home.
	Path string
	// Packs names the contributing packs in composition order — config order, since that is
	// the order the caller's pack list arrives in and the order the jail composes.
	Packs []string
	// Content is the composed file, or "" when no contributing pack ships prose (in which case
	// yolo owns nothing here and the destination is left alone or retired).
	Content string
}

// HostBriefingAdoption is a destination yolo is about to take ownership of that holds prose it
// did not write. It is what the CLI's confirmation gate names, and what MigrateHostBriefings
// acts on once confirmed.
type HostBriefingAdoption struct {
	// Path is the destination whose existing content is at stake.
	Path string
	// Existing is the file's current content, verbatim — the prose that must survive the
	// adoption, by being moved or (fallback) archived.
	Existing string
	// Packs names the packs that will own the destination afterwards, for the report.
	Packs []string
}

// ComposeHostBriefings resolves every briefing destination the given packs name into its
// composed content, in pack order.
//
// It is a PURE function of the pack set — no filesystem writes, no ownership questions — because
// three callers need the same answer for different reasons: the render writes it, the adoption
// gate compares it against what is there, and the prune needs to know which destinations still
// have an owner. Computing it three times from three loops is how the two notches came to
// disagree about `from` in the first place.
//
// The union caveat lives here: several packs naming ONE destination is the `briefing` kind's
// CombineConcat footprint, so they are concatenated in pack order under one provenance header
// each. No dedup-by-similarity is attempted — prose has no name, so "these two sections say the
// same thing" is a judgement yolo would get wrong.
func ComposeHostBriefings(packs []*packload.Pack, homeDir string) []HostBriefingDestination {
	var order []string
	byPath := map[string]*HostBriefingDestination{}
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBriefing || c.Into == "" {
				continue
			}
			path := filepath.Join(homeDir, filepath.FromSlash(c.Into))
			d, seen := byPath[path]
			if !seen {
				d = &HostBriefingDestination{Path: path}
				byPath[path] = d
				order = append(order, path)
			}
			// The destination is recorded even for a pack that ships no prose, because that
			// pack is still an OWNER: the six shipped agent packs declare `briefing` to name
			// the file their agent reads, and the content comes from the user's own packs
			// merging into it. Dropping them from Packs would make a destination with content
			// look ownerless the moment the content pack was the only one listed.
			d.Packs = append(d.Packs, p.Name)
			// One section per CONTRIBUTION, so a pack declaring two destinations delivers to
			// both. This was the host's own asymmetry for a while — the jail's composition took
			// one (pack, text) pair and could deliver only the first — and briefing-audiences.md
			// §5 closed it: run.packBriefingProses now enumerates per contribution too, so both
			// notches honor a pack's second `from`.
			prose, _ := p.BriefingProseFor(c)
			if prose == "" {
				continue
			}
			d.Content = appendHostBriefingSection(d.Content, p.Name, prose)
		}
	}
	out := make([]HostBriefingDestination, 0, len(order))
	for _, path := range order {
		out = append(out, *byPath[path])
	}
	return out
}

// appendHostBriefingSection adds one pack's attributed section, matching
// jailcontent.ComposePackBriefings' spacing byte-for-byte so the same prose reads the same at both
// notches.
func appendHostBriefingSection(base, pack, prose string) string {
	section := hostBriefingProvenance(pack) + "\n" + strings.TrimRight(prose, " \t\r\n") + "\n"
	if base == "" {
		return section
	}
	return strings.TrimRight(base, "\n") + "\n\n" + section
}

// HostBriefingAdoptions returns the destinations this pack set would take over that currently
// hold content yolo cannot prove it wrote — the input to the CLI's one-way-door confirmation.
//
// OWNERSHIP IS PROVED FROM THE RECORD, never inferred from the content. A destination yolo has
// composed before is its own to regenerate; one it has not is the user's until they say
// otherwise. That asymmetry is the whole gate: without it the first apply into a home with a
// hand-written ~/.claude/CLAUDE.md would silently replace it.
//
// An IDENTICAL file is not an adoption. A user who already moved their prose into a pack (or who
// re-runs an apply after a state-dir prune) loses nothing, and prompting there would train them
// to answer blind — the same property confirmHostLosses' docstring insists on.
func HostBriefingAdoptions(packs []*packload.Pack, homeDir string,
	man *hostskills.Manifest) []HostBriefingAdoption {
	var out []HostBriefingAdoption
	for _, d := range ComposeHostBriefings(packs, homeDir) {
		if d.Content == "" {
			continue // nothing would be written, so nothing would be adopted
		}
		existing, err := readHostBriefingFile(d.Path)
		if err != nil || existing == "" || existing == d.Content {
			continue
		}
		if man != nil {
			if owner, recorded := man.Owner(d.Path); recorded && owner == HostBriefingOwner {
				continue // yolo's own previous composition — regenerating it is not an adoption
			}
		}
		out = append(out, HostBriefingAdoption{Path: d.Path, Existing: existing, Packs: d.Packs})
	}
	return out
}

// HostBriefingOwner is the name recorded as the owner of a composed destination.
//
// A single pseudo-owner rather than a contributing pack's name, because a destination is owned
// by the pack SET: `~/.claude/CLAUDE.md` composed from claude + two content packs belongs to no
// one of them, and recording whichever happened to be first would make dropping that pack read
// as "the file is now the user's" while the other two still compose into it. The record answers
// "did yolo write this?", which is the only question the adoption gate and the prune ask.
//
// That is also exactly why the briefing record is a SEPARATE FILE from the skills/files one
// (HostBriefingRequest.Manifest is its own manifest — see the CLI's
// hostBriefingManifestPath). Sharing it looked economical and was wrong within one test run:
// droppedPackOrphans reads every owner in that record as a PACK NAME and archives the paths of
// any owner missing from `packs`, so a pseudo-owner no config can ever name made every composed
// briefing look like a dropped pack's output. Two records answering two different questions with
// two different key spaces cannot make that mistake.
const HostBriefingOwner = "yolo/briefing"

// HostBriefingManifestPath is where the briefing ownership record lives — its OWN file under
// the state dir, beside the skills/files one.
//
// It is spelled HERE, not at the one caller that used to own it, because two notches now ask
// the record the same question and must ask the same FILE. `yolo host apply` writes it
// (internal/cli's applyHostBriefings); the run pipeline reads it, to keep a jail from
// prepending a destination yolo composed — see GeneratedHostBriefings. Two spellings of one
// path is how a gate comes to consult a record nobody writes.
func HostBriefingManifestPath(home string) string {
	return filepath.Join(paths.GlobalStorageUnder(home), "host-briefing-manifest.json")
}

// GeneratedHostBriefings is the set of briefing destinations under home that yolo composed
// ITSELF, as absolute paths — what a caller must not read back in as the user's own prose.
//
// THIS IS THE BRIEFING HALF OF S3, the defect packSkillTargets records for skills: "since
// `yolo host apply` composes those directories, the jail was reading yolo's generated output
// back in as the user's tree". A `briefing` contribution's `after: "host:<path>"` names the
// very path RenderHostBriefings composes, so on any machine where the host notch has run, the
// jail prepended every pack's prose and then composed the same packs again — measured
// 2026-08-31 in a real jail: ~/.claude/CLAUDE.md carried each pack section twice.
//
// OWNERSHIP IS PROVED FROM THE RECORD, never inferred from the content, for the reason
// HostBriefingAdoptions states it: a file that merely LOOKS composed (it has provenance
// headers because the user pasted them) is still the user's.
//
// It FAILS OPEN — an unreadable or absent record yields an empty set, so every destination
// reads as the user's and is prepended as before. That is the opposite posture from the
// adoption gate, and deliberately: there, proving nothing must not overwrite the user's file;
// here, proving nothing must not DROP the user's instructions from their jail. Duplicated
// prose is a cost; silently missing prose is a broken agent.
func GeneratedHostBriefings(home string) map[string]bool {
	man, err := hostskills.LoadManifest(HostBriefingManifestPath(home))
	if err != nil || man == nil {
		return nil
	}
	out := map[string]bool{}
	for dest, owner := range man.Entries {
		if owner == HostBriefingOwner {
			out[dest] = true
		}
	}
	return out
}

// MigrateHostBriefings moves each adopted destination's prose into the local pack's AGENTS.md,
// so the user's instructions keep reaching their agents — through the layer model instead of a
// loose file. Returns one result per adoption.
//
// MOVE, NOT ARCHIVE (§6a as amended). Archiving is safe but it is not a MIGRATION: the prose
// ends up in a timestamped directory nothing reads, and getting it back is manual. Moving it
// into the pack yolo already composes means the same instructions reach the same agents on the
// very next apply. Archiving stays the FALLBACK — for prose that cannot be moved (no local-pack
// location, an unwritable config dir) — so nothing is ever deleted whichever path runs.
//
// The union caveat is at its sharpest here and is handled by ADMITTING it rather than resolving
// it: several agents' briefings merging into one local AGENTS.md are concatenated in destination
// order under a provenance header naming the file each section came from, and the caller warns
// that it happened. Prose has no name to dedup on, so leaving the editing to the user is the
// honest outcome.
//
// observe computes and writes nothing, which is why the caller can preview a migration it has
// not confirmed.
func MigrateHostBriefings(adoptions []HostBriefingAdoption, req HostBriefingRequest,
	observe bool) ([]HostRenderResult, error) {
	var out []HostRenderResult
	for _, a := range adoptions {
		res := HostRenderResult{Surface: "briefing/migrate", Path: a.Path}
		if req.LocalPackAGENTS == "" {
			// No local pack location: fall back to the archive, which loses nothing but does
			// not preserve behavior. Named as the fallback so the difference is visible.
			res.Action, res.WouldChange = hostBriefingArchiveAction(a.Path, req, observe,
				"archived (no local pack to move it into)")
			out = append(out, res)
			continue
		}
		if observe {
			res.Action, res.WouldChange = "would move your prose into "+req.LocalPackAGENTS, true
			out = append(out, res)
			continue
		}
		if err := appendToLocalPackBriefing(req.LocalPackAGENTS, a); err != nil {
			// A failed move must not silently become a wholesale overwrite of the user's file
			// on the render that follows. Archive instead — the fallback exists for exactly
			// this — and report both halves so the user knows which one ran.
			res.Action, res.WouldChange = hostBriefingArchiveAction(a.Path, req, observe,
				fmt.Sprintf("archived (could not move it into the local pack: %v)", err))
			out = append(out, res)
			continue
		}
		res.Action, res.WouldChange = "moved your prose into "+req.LocalPackAGENTS, true
		out = append(out, res)
	}
	return out, nil
}

// hostBriefingArchiveAction archives a path and returns the action line, or a refusal —
// together with the change predicate for it (HostRenderResult.WouldChange).
//
// The predicate is returned rather than derived from the string by the caller, so a new
// wording here cannot silently reclassify itself: only the refusal changes nothing.
func hostBriefingArchiveAction(path string, req HostBriefingRequest, observe bool,
	label string) (string, bool) {
	if observe {
		return "would archive your prose", true
	}
	at, err := hostskills.Archive(req.ArchiveRoot, req.Stamp, "briefing", path)
	if err != nil {
		return "refused: could not archive your existing prose: " + err.Error(), false
	}
	return label + " → " + at, true
}

// appendToLocalPackBriefing appends one adopted destination's prose to the local pack's
// AGENTS.md, attributed to the file it came from.
//
// APPEND, not overwrite: a user migrating three agents' briefings in one apply, or migrating a
// second agent months later, must not have the first one replaced. The attribution comment is
// the union caveat's whole mitigation — it is what makes "why do I have two copies of this rule?"
// answerable by reading the file.
func appendToLocalPackBriefing(dest string, a HostBriefingAdoption) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	existing, err := readHostBriefingFile(dest)
	if err != nil {
		return err
	}
	section := "<!-- migrated from " + a.Path + " -->\n" +
		strings.TrimRight(a.Existing, " \t\r\n") + "\n"
	merged := section
	if strings.TrimSpace(existing) != "" {
		merged = strings.TrimRight(existing, "\n") + "\n\n" + section
	}
	return WriteStringInPlace(dest, merged, 0o644)
}

// RenderHostBriefings composes every briefing destination the pack set names and writes it,
// wholesale. Returns one result per destination.
//
// PACK-SET-WIDE rather than per-pack, and that is the structural consequence of the ruling: the
// file's content is the union of every contributing pack's prose, so a per-pack entry would have
// to either append (unbounded growth) or overwrite (the last pack wins and the others vanish).
// The old delimited-block API was per-pack precisely because a block could be asserted
// independently; nothing in wholesale composition can be.
//
// A destination whose contributing packs ship NO prose is left ALONE HERE, not emptied — but it
// is not left alone by the apply: an OWNED destination with nothing left to compose is exactly
// the orphan PruneHostBriefings archives, and one yolo never wrote is the user's file at a path
// some pack happens to name. Splitting it that way is what keeps "yolo owns this" from licensing
// the truncation of a file yolo never wrote, while still ensuring "the pack stopped shipping
// prose" and "the pack was dropped" leave the same residue (the property the old mechanism's
// empty-prose branch had).
//
// Anything already at an ADOPTED destination must have been migrated first — the caller runs
// MigrateHostBriefings behind its confirmation. This function does not re-check that, for the
// reason RenderHostPack does not re-check its own gate: a second copy of the policy is a second
// place for it to drift.
func RenderHostBriefings(packs []*packload.Pack, homeDir string, req HostBriefingRequest,
	observe bool) ([]HostRenderResult, error) {
	var out []HostRenderResult
	for _, d := range ComposeHostBriefings(packs, homeDir) {
		id := hostBriefingSurfaceID(d)
		if d.Content == "" {
			out = append(out, HostRenderResult{Surface: id, Path: d.Path,
				Action: "skipped: no pack contributes briefing prose here"})
			continue
		}
		existing, err := readHostBriefingFile(d.Path)
		if err != nil {
			return out, fmt.Errorf("%s: %w", id, err)
		}
		action, err := writeHostBriefingFile(d.Path, existing, d.Content, observe)
		if err != nil {
			return out, fmt.Errorf("%s: %w", id, err)
		}
		if !observe && req.Manifest != nil {
			// Recorded on every write, including an unchanged one: the record is what makes the
			// NEXT apply's regeneration unprompted, and a home where the content happened to
			// match already must not stay stuck behind the adoption gate forever.
			req.Manifest.Record(d.Path, HostBriefingOwner)
		}
		// THE CHANGE PREDICATE for a composed briefing, and it needs no new comparison: this
		// destination is a WHOLESALE yolo-owned file, so writeHostBriefingFile has already
		// compared the composed content against the file's own bytes and returned `unchanged`
		// when they match. See HostRenderResult.WouldChange.
		out = append(out, HostRenderResult{Surface: id, Path: d.Path, Action: action,
			WouldChange: action != "unchanged"})
	}
	return out, nil
}

// hostBriefingSurfaceID names a destination in the report by its contributing packs, so a merged
// file says whose prose it holds without the reader opening it.
func hostBriefingSurfaceID(d HostBriefingDestination) string {
	if len(d.Packs) == 0 {
		return "briefing"
	}
	return strings.Join(d.Packs, "+") + "/briefing"
}

// PruneHostBriefings retires a destination yolo composed that NO active pack contributes to any
// more — the orphan case: a generated file left behind with nobody to regenerate it.
//
// It is a separate entry from RenderHostBriefings for the reason the skills prune is separate: a
// pack DROPPED from config never appears in the render loop, so the only way its output leaves
// the user's home is a pass that knows the destinations independently of the active set.
// candidates supplies the paths to look at (every pack yolo can resolve — embedded plus
// configured), active names the packs whose destinations are legitimate.
//
// ARCHIVED, never deleted, and that is a change of posture from the block mechanism: removing a
// delimited block restored the file's own bytes and could be unconditional (old ruling R4), but
// a WHOLESALE file has no user bytes to restore — every byte is yolo's, and moving it under the
// archive is what makes being wrong cost one `mv`. It stays unconfirmed, because the content
// being removed is content yolo wrote.
//
// A nil active set is REFUSED rather than read as "nothing is active": that reading would retire
// every composed destination on a caller bug, which is the one outcome this file exists to
// prevent. An empty non-nil map is the honest "no packs configured".
//
// req.PackSetComplete is the second guard, for the offline-remote case — see its field comment.
func PruneHostBriefings(candidates []*packload.Pack, active map[string]bool, homeDir string,
	req HostBriefingRequest, observe bool) ([]HostRenderResult, error) {
	if active == nil {
		return nil, fmt.Errorf("host briefing prune: refusing to prune with an unknown active pack set")
	}
	if !req.PackSetComplete {
		// A pack the config still names but that did not resolve this run contributes nothing to
		// the composition, so every destination it was the sole contributor to would look
		// orphaned. Retiring nothing is the only reading that cannot archive a working setup's
		// briefing the first time the user is offline.
		return nil, nil
	}
	// The destinations that still HAVE an owner, computed from the active subset of the
	// candidates. Keyed on path rather than on pack, because a destination several packs
	// contribute to survives one of them leaving — which is the case a per-pack prune gets
	// wrong.
	//
	// COMPOSED, not merely declared: a destination whose remaining packs all stopped shipping
	// prose has nothing left to write into it, and leaving a file yolo generated behind with
	// nobody to regenerate it is the same orphan as dropping the pack outright. That is the
	// property the old mechanism's "empty prose removes the stale block" branch carried, kept
	// here rather than duplicated into the render.
	live := map[string]bool{}
	var activePacks []*packload.Pack
	for _, p := range candidates {
		if p != nil && active[p.Name] {
			activePacks = append(activePacks, p)
		}
	}
	for _, d := range ComposeHostBriefings(activePacks, homeDir) {
		if d.Content != "" {
			live[d.Path] = true
		}
	}

	var out []HostRenderResult
	for _, path := range hostBriefingPaths(candidates, homeDir) {
		if live[path] {
			continue
		}
		// OWNERSHIP IS REQUIRED TO RETIRE. A destination yolo never composed is the user's
		// file at a path some pack happens to name, and a prune with no record is a prune with
		// no authority — the same fail-closed reading droppedPackOrphans takes.
		if req.Manifest == nil || !req.Manifest.OwnedBy(path, HostBriefingOwner) {
			continue
		}
		if _, err := os.Lstat(path); err != nil {
			// The record outlived the file. Bookkeeping only: forget it so the next apply does
			// not report a phantom removal.
			if !observe && req.Manifest != nil {
				req.Manifest.Forget(path)
			}
			continue
		}
		res := HostRenderResult{Surface: "briefing/retire", Path: path}
		if observe {
			res.Action = "would archive (no pack contributes briefing prose here any more)"
			res.WouldChange = true
			out = append(out, res)
			continue
		}
		at, err := hostskills.Archive(req.ArchiveRoot, req.Stamp, "briefing", path)
		if err != nil {
			res.Action = "refused: " + err.Error()
			out = append(out, res)
			return out, fmt.Errorf("host briefing retire %s: %w", path, err)
		}
		// Forget only AFTER the move: a record dropped for a file still in the home would make
		// the next apply read it as the user's own, which is the one state yolo can never clean
		// up from.
		req.Manifest.Forget(path)
		res.Action = "archived (no pack contributes briefing prose here any more) → " + at
		res.WouldChange = true
		out = append(out, res)
	}
	return out, nil
}

// hostBriefingPaths is the deduplicated, sorted set of home-absolute briefing destinations the
// given packs declare — the files a prune pass must look at. Sorted (rather than declaration
// order, as the render uses) because a prune's output order should not depend on which pack the
// caller happened to list first.
func hostBriefingPaths(packs []*packload.Pack, homeDir string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindBriefing || c.Into == "" {
				continue
			}
			path := filepath.Join(homeDir, filepath.FromSlash(c.Into))
			if seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	sort.Strings(out)
	return out
}

// readHostBriefingFile reads a destination, treating ABSENT as empty. An absent user briefing is
// the normal case, not an error — the render creates the file.
func readHostBriefingFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return string(data), nil
}

// writeHostBriefingFile commits (or, in observe, does not commit) the composed content and
// returns the action to report. Identical content is reported as "unchanged" in both postures
// and is not rewritten, so a no-op apply does not touch the file's mtime.
//
// The write goes through WriteStringInPlace: 0o644 applies only when creating, so a user who
// chmod'd their own briefing keeps their mode.
func writeHostBriefingFile(path, existing, updated string, observe bool) (string, error) {
	if existing == updated {
		return "unchanged", nil
	}
	if observe {
		return "would render", nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	if err := WriteStringInPlace(path, updated, 0o644); err != nil {
		return "", err
	}
	return "rendered", nil
}

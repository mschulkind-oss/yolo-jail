package capture

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// gc.go reclaims capture entries, and its whole rule is one sentence:
//
//	DELETE EVERY ENTRY THE RESOLVER WOULD NOT SELECT.
//
// Not "every entry nothing references" — there is no unreferenced oracle here, and after
// program-delivery.md OQ-PD17 there is no need for one. Two facts retire the question:
//
//  1. RECLAIMING IS NEVER A CORRECTNESS EVENT. Materialize is a reflink, whose destination
//     gets its own inode; MEASURED 2026-09-04, a reflinked file survives its source's unlink
//     byte-identical, and the hardlink and copy arms strand nothing either. The store is a
//     CACHE, NOT AN ALLOCATOR: there is no dangling pointer in this system, and the worst
//     case of reaping an entry some workspace is running is that the next COLD install
//     somewhere else re-downloads. (The three oracles OQ-PD17 weighed — workspace
//     enumeration via receipts, a store-side reference list, FIEMAP extent sharing — are all
//     retired, along with the st_nlink one that reflink had already made wrong.)
//  2. THE UNREACHABLE SET IS ALREADY EXACTLY COMPUTABLE. Select (select.go) is the only way
//     anything reads this store, and it takes the newest `record` receipt per (bin, platform).
//     So every entry Select does not name is ALREADY unreachable — a fact about the selection
//     function, not an estimate about referrers.
//
// So this file does not implement a rule; it implements the COMPLEMENT of one, from the same
// scan Select chooses from. A reap that took an entry the resolver could still return would
// have to survive `selectFrom` returning it, which is unrepresentable here.
//
// # What is deliberately NOT built
//
// K IS 1, not a tunable. A second entry per program would be a rollback target with nowhere
// to be used: the store is not a version history, it holds whatever a human captured, and a
// materialized older version is updated by evergreen within UPDATE_INTERVAL anyway. Real
// rollback is the vendor's own per-workspace `~/.local/share/<bin>/versions/*` — a different
// axis, and a different sweeper's job.
//
// THERE IS NO AGE FLOOR. It would guard a window the completion marker already covers: an
// in-flight admit has no marker, so EntryKeys never lists it and Select never sees it. The one
// real race — a reap unlinking an entry mid-materialize — is not fixed by an age floor and
// needs no fix: a failed materialize is a MISS, and a miss falls through to the vendor
// installer silently, by design (internal/entrypoint/shims.go).
//
// # What a reap keeps
//
// THE MANIFEST, and everything else beside the tree. `capture-manifest.json` sits BESIDE
// `tree/`, never inside it (manifest.go), so dropping the tree leaves the drift record — what
// this version of this program installed, file by file — for kilobytes. Store.Resolve reads
// the completion marker and nothing else, so an entry stripped to its metadata correctly reads
// as ABSENT: it is not a degraded entry, it is a gone one with a receipt.
//
// The marker goes FIRST and the tree second, which is the inverse of the order Admit writes
// them in, for the same reason Admit uses that order. Interrupted after the marker, the entry
// reads absent while its bytes are still on disk: a miss, then a download, and the next prune
// finishes the job. Interrupted the other way round it would read COMPLETE with no tree, which
// is the one state that turns a miss into a loud mid-materialize failure.
//
// # What it does NOT reap, and why each is deliberate
//
// A TORN entry — one whose marker never landed — is invisible here, because EntryKeys is the
// candidate list and a torn entry and an IN-FLIGHT admit are the same observation. That is the
// marker's whole job, and it is why no age floor is needed to protect a capture in progress. The
// cost is a real if narrow leak: an admit killed between the rename and the marker leaves a tree
// nothing reclaims until the next capture of the same content, which Admit clears itself.
//
// A STALE STAGING DIR is out of scope for a stronger reason. `staging/<id>` is where a capture in
// flight lives, there is no marker to tell a live one from an abandoned one, and unlike an entry a
// staging dir CAN be a correctness event to delete — it would break the running capture that owns
// it. Reclaiming it needs a liveness or age rule this sweep deliberately does not have; today
// Store.Stage clears the previous one when the same id is captured again.
//
// # The reclaimer invariants, by ID (minimal-disk-footprint.md §5)
//
//   - P2 (fail-safe on unknown liveness): this sweep asks NO liveness question, because it does
//     not have one — an entry's reachability is decided by the selection function, not by who
//     is running its bytes. The one unknown it can have is a missing receipt reader, and that
//     is refused rather than defaulted: see PruneSupersededCaptures.
//   - P3 (a reclaim never strands a running jail): structural here, by copy-on-write rather
//     than by link counting. See fact 1 above.
//   - P4 (never delete what an in-flight launch is between steps on): the window exists — a
//     materialize resolves an entry and then reads it — and unlike the cache tar it costs
//     nothing, because the reader's failure mode is a MISS with a vendor-installer fallback
//     rather than a failed launch.
//   - P7 (safe at an arbitrary moment): held by the two above plus the marker-first ordering.
//     The trigger is still a human typing `yolo prune`; nothing on the launch path calls this.

// Superseded names one program a reaped entry recorded, and the entry that won it.
type Superseded struct {
	// Program is the (bin, platform) pair the reaped entry's own receipt claimed.
	Program Program
	// Winner is the key Select chose for that program instead.
	Winner string
}

// ReapedEntry is one entry PruneSupersededCaptures took — or, on a dry run, would take.
type ReapedEntry struct {
	// Key is the entry's directory name under entries/.
	Key string
	// Bytes is the size of the tree's regular files: what the reap frees. Metadata beside
	// the tree survives and is not counted, because it is not what anybody is reclaiming.
	Bytes int64
	// Lost is what this entry recorded and lost, sorted by program. EMPTY when no receipt
	// attributes the entry to any program — which is the SAME rule and not a second one: an
	// entry the resolver cannot name is an entry the resolver would not select.
	Lost []Superseded
	// Err is a removal failure, on the apply path only. The entry is reported either way —
	// a sweep that silently skipped what it could not delete would report a reclaim that
	// did not happen — but its Bytes are not counted toward the total.
	Err error
}

// Reason is the one-line human explanation of why this entry went, for a report that has to
// name what it removed.
func (r ReapedEntry) Reason() string {
	if len(r.Lost) == 0 {
		return "no record receipt — attributed to no program"
	}
	parts := make([]string, 0, len(r.Lost))
	for _, l := range r.Lost {
		parts = append(parts, l.Program.String()+" superseded by "+l.Winner)
	}
	return strings.Join(parts, ", ")
}

// Reap is the result of one PruneSupersededCaptures pass.
type Reap struct {
	// Entries are the entries reaped (dry run: reapable), in key order.
	Entries []ReapedEntry
	// Bytes is the total freed, counting only entries whose removal succeeded.
	Bytes int64
	// Kept is how many entries Select would still return — the ones this pass is the
	// complement of. Reported so "reaped 2, kept 3" is auditable rather than a number the
	// reader has to take on trust.
	Kept int
}

// PruneSupersededCaptures reaps every entry in the store at root that Select would not return:
// for each (bin, platform) the newest recorded capture survives and the rest go, along with any
// entry no receipt attributes to a program at all.
//
// `read` is the receipt reader Select takes; see Records for why the store is handed one
// rather than importing it. A nil reader is a programming error, not a degrade: it would make
// every entry look unattributed and reap the whole store, so it is refused.
//
// apply=false computes the identical numbers without touching anything — the prune house shape
// (internal/prune), so a dry run is a promise about what --apply does rather than a separate
// code path.
//
// An absent store is an empty one, not an error (Store.EntryKeys), which is the state of every
// machine that has never run `yolo capture`.
func PruneSupersededCaptures(root string, read Records, apply bool) (Reap, error) {
	if read == nil {
		return Reap{}, errors.New("capture prune: no receipt reader — refusing to treat " +
			"every entry as unattributed (that would reap the whole store)")
	}
	s := &Store{Dir: root}
	// ONE scan feeds both halves: the winners are what Select picks out of it, and the reap
	// is everything else in it. Reading the store twice would let the two disagree about
	// which entries exist.
	scan, err := scanRecords(s, read)
	if err != nil {
		return Reap{}, err
	}
	selected := selectFrom(scan)
	winners := make(map[string]bool, len(selected))
	for _, sel := range selected {
		winners[sel.Key] = true
	}
	out := Reap{Kept: len(winners)}
	for _, er := range scan {
		if winners[er.Key] {
			continue
		}
		reaped := ReapedEntry{Key: er.Key, Lost: lostBy(er, selected)}
		dir := s.EntryDir(er.Key)
		reaped.Bytes = treeBytes(TreeDir(dir))
		if apply {
			reaped.Err = reapEntry(dir)
		}
		if reaped.Err == nil {
			out.Bytes += reaped.Bytes
		}
		out.Entries = append(out.Entries, reaped)
	}
	return out, nil
}

// lostBy pairs each program a losing entry recorded with the key that beat it.
func lostBy(er entryRecords, selected map[Program]Selected) []Superseded {
	seen := map[Program]bool{}
	var out []Superseded
	for _, r := range er.Records {
		p := Program{Bin: r.Bin, Platform: r.Platform}
		if seen[p] {
			continue
		}
		seen[p] = true
		out = append(out, Superseded{Program: p, Winner: selected[p].Key})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Program.Bin != out[j].Program.Bin {
			return out[i].Program.Bin < out[j].Program.Bin
		}
		return out[i].Program.Platform < out[j].Program.Platform
	})
	return out
}

// reapEntry drops one entry's tree and its claim to exist, keeping the metadata beside it.
//
// MARKER FIRST — see the file comment for why this is the inverse of Admit's order and has to
// be.
func reapEntry(dir string) error {
	if err := os.Remove(filepath.Join(dir, completeMarker)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.RemoveAll(TreeDir(dir))
}

// treeBytes sums the regular files under a tree; an absent or unreadable tree is 0.
//
// The walk is the truth rather than the manifest's TotalBytes: the manifest says what was
// captured, and what a reap frees is what is on disk now. An unreadable subtree is skipped
// rather than failing the sweep, which only understates the reclaim.
func treeBytes(tree string) int64 {
	var total int64
	_ = filepath.WalkDir(tree, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d == nil {
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

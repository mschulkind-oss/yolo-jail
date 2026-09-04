package cli

import (
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// capturematerialize.go is `yolo internal capture-materialize` — the IN-JAIL half of
// install-capture's second verb (docs/design/program-delivery.md §6.3, amended): put an
// already-captured install into this home instead of downloading it.
//
// # Who calls it, and from where
//
// The generated native launcher, from the top of its `_do_install`, before the download it
// otherwise performs (entrypoint's nativeLauncherTemplate). NOT a boot genStep, and that is
// a design constraint rather than an implementation convenience: §5.2 names *"you pay
// nothing for a tool you never invoke"* as the virtue any replacement for lazy install must
// keep, and a boot step would materialize every declared program into every jail whether or
// not anyone ran it. From the launcher, a workspace that never types `claude` never pays for
// claude's 1.2 GB.
//
// It is a SUBCOMMAND rather than shell in the template because the mechanism is a reflink
// ioctl. See capture.Materialize for why reflink and not the hardlink §6.3 originally named.
//
// # Hidden, like capture-run
//
// Same reason: nothing but the generated launcher should emit this argv. It writes a vendor's
// files into the home it is pointed at, which is correct from a launcher and is a way to
// clobber a home from anywhere else.
//
// # A miss is not an error, it is the fallback
//
// Every "no" — no store, no entry for this bin on this platform, a torn entry — exits
// non-zero after ONE line, and the launcher then downloads. install-capture.md's Blockers are
// explicit that making a capture mandatory for the installer class is a behaviour change
// nobody has ruled on: a first run on a machine with no capture must still work.

const captureMaterializeUsage = "usage: yolo internal capture-materialize --store=DIR --bin=NAME " +
	"[--home=DIR] [--declared=URL] [--receipts=PATH]"

// runCaptureMaterialize is the `yolo internal capture-materialize` entry.
func runCaptureMaterialize(args []string) int {
	var opts materializeArgs
	opts.home = os.Getenv("HOME")
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--store="):
			opts.store = strings.TrimPrefix(a, "--store=")
		case strings.HasPrefix(a, "--bin="):
			opts.bin = strings.TrimPrefix(a, "--bin=")
		case strings.HasPrefix(a, "--home="):
			opts.home = strings.TrimPrefix(a, "--home=")
		case strings.HasPrefix(a, "--declared="):
			opts.declared = strings.TrimPrefix(a, "--declared=")
		case strings.HasPrefix(a, "--receipts="):
			opts.receipts = strings.TrimPrefix(a, "--receipts=")
		default:
			fmt.Fprintf(os.Stderr, "capture-materialize: unexpected argument %q\n%s\n",
				a, captureMaterializeUsage)
			return 2
		}
	}
	if opts.store == "" || opts.bin == "" || opts.home == "" {
		fmt.Fprintln(os.Stderr, captureMaterializeUsage)
		return 2
	}
	return materializeCapture(opts, os.Stderr)
}

// materializeArgs is one materialize request, parsed.
type materializeArgs struct {
	// store is the store root AS THIS PROCESS SEES IT — the in-jail mount point, not the
	// host path. Passed rather than derived: paths.CapturesDir() inside a jail names the
	// jail's own per-workspace home, which is not the machine store.
	store string
	// bin is the program the launcher is about to install.
	bin string
	// home is the destination HOME, always the launcher's own $HOME.
	home string
	// declared is the installer URL, copied into the receipt so the workspace log says
	// which declaration these bytes satisfy — the same field the installer receipt
	// carries, so the two are comparable.
	declared string
	// receipts is the workspace receipt log to append to. Empty writes none.
	receipts string
}

// materializeCapture is runCaptureMaterialize with its writer injected.
//
// Returns 0 only when the entry is fully in the home. Everything else is 1, after one line
// naming what happened — never a stack of diagnostics, because the caller's next act is to
// download and its own output is what the user is reading.
func materializeCapture(a materializeArgs, errw io.Writer) int {
	store := &capture.Store{Dir: a.store}
	platform := capture.Platform()
	entry, rec, err := resolveCaptureFor(store, a.bin, platform)
	if err != nil {
		fmt.Fprintf(errw, "  yolo: no capture for %s (%s): %v\n", a.bin, platform, err)
		return 1
	}
	res, err := capture.Materialize(capture.MaterializeOptions{
		Entry:  entry,
		Home:   a.home,
		Stderr: errw,
	})
	if err != nil {
		// LOUD, unlike a miss. A miss means the store had nothing to offer; this means it
		// had something and putting it in place went wrong, possibly halfway — the home
		// may now hold part of a program. The installer that runs next overwrites its own
		// paths, which is the recovery, but a human should see that it happened.
		fmt.Fprintf(errw, "  yolo: materializing capture %s for %s FAILED: %v\n"+
			"  (falling back to the vendor installer; %s may hold a partial tree)\n",
			entry.Key, a.bin, err, a.home)
		return 1
	}
	if a.receipts != "" {
		line := entrypoint.CaptureReceipt{
			Bin:      a.bin,
			Declared: a.declared,
			Key:      entry.Key,
			// From the RECORD receipt, not re-derived: the digest is of the canonical
			// manifest, and recomputing it here would mean walking the tree we just
			// spent the whole design avoiding walking.
			Digest: rec.Digest,
			Bytes:  res.Bytes,
			// THE ENTRY AS THIS PROCESS SEES IT — a jail path (/ctx/captures/entries/<key>),
			// where the `record` receipt beside the entry carries the host path. That is not
			// a discrepancy to fix: the two lines live in different files with different
			// scopes, and every path in <ws>/.yolo/receipts.jsonl is written by an in-jail
			// process and is a jail path (the launcher funnels write $HOME/.local/bin/<bin>).
			// A host path here would be the one string in that file a reader inside the jail
			// could not resolve. `resolved` — the store key — is the identity that crosses
			// both coordinate systems, which is why a reader keys on it and not on this.
			Path:     entry.Root,
			Platform: platform,
			Act:      entrypoint.ReceiptActMaterialize,
			Time:     time.Now(),
		}.Line()
		if err := entrypoint.AppendReceiptLine(a.receipts, line); err != nil {
			// A receipt RECORDS; it never gates (program-delivery.md §9 R1). The bytes
			// are in the home either way, and failing the materialize over its log
			// would send the launcher off to download what is already there.
			fmt.Fprintf(errw, "  yolo: (the materialize receipt could not be written: %v)\n", err)
		}
	}
	fmt.Fprintf(errw, "  Materialized %s from capture %s by %s (%d files, %s)\n",
		a.bin, entry.Key, res.Mechanism(), res.Files, humanBytes(res.Bytes))
	return 0
}

// resolveCaptureFor answers the one question the content-addressed store cannot: WHICH ENTRY
// holds <bin> for <platform>?
//
// BY SCANNING THE ENTRIES' OWN RECEIPTS, and there is deliberately no index.
//
// The store is content-addressed, so no key is computable from a program name; slice 3 put
// the (bin, platform, key) triple in each entry's `receipts.jsonl` and left the lookup to
// slice 4. The two candidate answers were an index file beside the store and this scan, and
// the scan wins on all three counts that matter here:
//
//   - COST. The question is asked from a launcher's cold `if [ ! -x "$REAL_BIN" ]` branch —
//     once per program per workspace, never on a warm launch — and the work is one ReadDir
//     plus one small file read per entry. An index would save microseconds on a path that
//     runs a handful of times in a workspace's life.
//   - CORRECTNESS. An index is a second record that admit AND the GC slice would both have
//     to keep true, and a stale one is a WRONG ANSWER (a key that no longer exists, or an
//     entry reaped out from under it). The receipts live INSIDE the entry they describe, so
//     they cannot go stale relative to it: deleting the entry deletes the claim.
//   - SCHEMA. install-capture.md's Blockers say to stop and ask before adding per-entry
//     metadata a later yolo must parse. A scan adds none.
//
// NEWEST WINS, by receipt time, with the greater key breaking a tie — the stamp has
// one-second resolution, so two captures in one second are possible and an arbitrary answer
// would make a re-capture's effect depend on directory order. An entry whose receipt is
// missing or unreadable is simply not a candidate: it is in the store, but nothing attributes
// it to a program, which is a miss and not an error.
func resolveCaptureFor(store *capture.Store, bin, platform string) (*capture.Entry, *entrypoint.CaptureReceipt, error) {
	keys, err := store.EntryKeys()
	if err != nil {
		return nil, nil, err
	}
	var bestKey string
	var best entrypoint.CaptureReceipt
	for _, key := range keys {
		recs, rerr := entrypoint.ReadCaptureReceipts(capture.ReceiptsPath(store.EntryDir(key)))
		if rerr != nil {
			continue
		}
		for i := range recs {
			r := recs[i]
			if r.Bin != bin || r.Platform != platform || r.Act != entrypoint.ReceiptActRecord {
				continue
			}
			if bestKey == "" || r.Time.After(best.Time) ||
				(r.Time.Equal(best.Time) && key > bestKey) {
				bestKey, best = key, r
			}
		}
	}
	if bestKey == "" {
		return nil, nil, fmt.Errorf("nothing in %s records one (run `yolo capture %s` to make it)",
			store.Dir, bin)
	}
	// Through Resolve, so "listed" and "usable" are one answer: the completion marker is
	// the only thing that says an entry exists, and a receipt beside a torn tree would
	// otherwise be enough to select it.
	entry, err := store.Resolve(bestKey)
	if err != nil {
		return nil, nil, err
	}
	return entry, &best, nil
}

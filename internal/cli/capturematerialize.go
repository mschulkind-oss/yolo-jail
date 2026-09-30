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
	"[--key=KEY] [--home=DIR] [--declared=URL] [--receipts=PATH]"

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
		case strings.HasPrefix(a, "--key="):
			opts.key = strings.TrimPrefix(a, "--key=")
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
	// key, when set, is the ENTRY to materialize — a fork's build, decided on the host
	// (docs/design/forked-programs-as-packs.md FP-D8) — instead of the one selection names for
	// bin. The entry must record a build of bin for this platform, or it is refused.
	key string
}

// materializeCapture is runCaptureMaterialize with its writer injected.
//
// Returns 0 only when the entry is fully in the home. Everything else is 1, after one line
// naming what happened — never a stack of diagnostics, because the caller's next act is to
// download and its own output is what the user is reading.
func materializeCapture(a materializeArgs, errw io.Writer) int {
	store := &capture.Store{Dir: a.store}
	platform := capture.Platform()
	if a.key != "" {
		return materializeForkBuild(store, a, platform, errw)
	}
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
	// A RELOCATED materialize says so, and names both homes. The files it rewrote are this
	// home's own bytes rather than the store's, and a program that misbehaves after a move
	// is one whose first suspect is the rewrite, so the line that would send a reader there
	// has to exist.
	if res.RelocatedFrom != "" {
		fmt.Fprintf(errw, "  (captured under %s: rewrote %d links and %d files to name %s)\n",
			res.RelocatedFrom, res.RewrittenLinks, res.Rewritten, a.home)
	}
	return 0
}

// materializeForkBuild is the --key mode: put the fork build the HOST decided on into this home
// (FP-D8 — the fork lock is in the user config directory, which no jail can read, so the host
// hands the key over). The key is checked against the entry's own `build` record: an entry that
// records another bin or another platform is refused, because a key the launcher was handed by
// mistake must not put some other program's bytes under this bin's name.
func materializeForkBuild(store *capture.Store, a materializeArgs, platform string, errw io.Writer) int {
	entry, err := store.Resolve(a.key)
	if err != nil {
		fmt.Fprintf(errw, "  yolo: fork build %s for %s is not in the store: %v\n", a.key, a.bin, err)
		return 1
	}
	var rec *capture.Record
	recs, _ := captureRecords(entry.Root)
	for i := range recs {
		if recs[i].Source != "" && recs[i].Bin == a.bin && recs[i].Platform == platform {
			rec = &recs[i]
		}
	}
	if rec == nil {
		fmt.Fprintf(errw, "  yolo: store entry %s records no build of %s for %s — refusing to put it "+
			"in place of %s\n", a.key, a.bin, platform, a.bin)
		return 1
	}
	res, err := capture.Materialize(capture.MaterializeOptions{Entry: entry, Home: a.home, Stderr: errw})
	if err != nil {
		fmt.Fprintf(errw, "  yolo: materializing fork build %s for %s FAILED: %v\n  (%s may hold a "+
			"partial tree)\n", entry.Key, a.bin, err, a.home)
		return 1
	}
	if a.receipts != "" {
		line := entrypoint.BuildReceipt{
			Bin: a.bin, Source: a.declared, Key: entry.Key, Digest: rec.Digest, Bytes: res.Bytes,
			Path: entry.Root, Platform: platform, Revision: rec.Revision, Recipe: rec.Recipe,
			Act: entrypoint.ReceiptActMaterialize, Time: time.Now(),
		}.Line()
		if err := entrypoint.AppendReceiptLine(a.receipts, line); err != nil {
			fmt.Fprintf(errw, "  yolo: (the materialize receipt could not be written: %v)\n", err)
		}
	}
	fmt.Fprintf(errw, "  Materialized %s from fork build %s (commit %s) by %s (%d files, %s)\n",
		a.bin, entry.Key, rec.Revision, res.Mechanism(), res.Files, humanBytes(res.Bytes))
	return 0
}

// resolveCaptureFor answers the one question the content-addressed store cannot: WHICH ENTRY
// holds <bin> for <platform>?
//
// THE RULE ITSELF IS capture.Select — newest `record` receipt per (bin, platform), by a scan
// of the entries' own receipts with deliberately no index. It lives in internal/capture rather
// than here because it has a second caller whose whole definition is this one's complement:
// capture.PruneSupersededCaptures reaps every entry Select would not name (program-delivery.md
// OQ-PD17). Derived rather than agreed with, the reader and the reaper cannot drift.
//
// What stays here is what is local to the materialize path: turning "no entry for this
// program" into a MISS whose message names the act that fixes it.
func resolveCaptureFor(store *capture.Store, bin, platform string) (*capture.Entry, *capture.Record, error) {
	selected, err := capture.Select(store, captureRecords)
	if err != nil {
		return nil, nil, err
	}
	best, ok := selected[capture.Program{Bin: bin, Platform: platform}]
	if !ok {
		return nil, nil, fmt.Errorf("nothing in %s records one (run `yolo capture %s` to make it)",
			store.Dir, bin)
	}
	// Through Resolve, so "listed" and "usable" are one answer: the completion marker is
	// the only thing that says an entry exists, and a receipt beside a torn tree would
	// otherwise be enough to select it.
	entry, err := store.Resolve(best.Key)
	if err != nil {
		return nil, nil, err
	}
	return entry, &best.Record, nil
}

// captureRecords is THE ADAPTER between the receipt schema and the capture store's selection:
// it reads one entry's `receipts.jsonl` with the schema's one reader and hands back the
// `record` lines as capture.Records.
//
// ONE ADAPTER, TWO CALLERS — resolveCaptureFor above and `yolo prune`'s superseded-capture
// sweep (internal/cli/commands.go wires it into prune.Options) — because the moment there were
// two of these, the resolver and the reaper could disagree about which lines count as a
// record.
//
// It lives at the CLI boundary rather than in internal/capture because the store must not
// import internal/entrypoint: that is the jail provisioner, it sits above a content-addressed
// directory rather than below it, and it pulls internal/config and with it the whole
// config-validation graph. internal/prune, the sweep's other consumer, deliberately imports
// neither. See capture.Records.
//
// FILTERING IS THE SELECTION'S PRECONDITION, NOT A SECOND RULE: only `act:"record"` lines
// describe a capture that was made (the other act is `materialize`, written per workspace and
// never beside an entry), so anything else is not a candidate for any question.
//
// BOTH RECEIPT KINDS, a vendor installer's capture and a fork's build
// (docs/design/forked-programs-as-packs.md FP-D8), read in the same commit as the first build
// receipt was written: the reap is this reader's complement, so a reader that kept only `capture`
// lines would let `yolo prune --apply` delete every fork entry as unattributed. A build record
// carries its source address, which selection keys on, so neither kind can answer a query for the
// other.
func captureRecords(entryDir string) ([]capture.Record, error) {
	path := capture.ReceiptsPath(entryDir)
	recs, err := entrypoint.ReadCaptureReceipts(path)
	if err != nil {
		return nil, err
	}
	builds, err := entrypoint.ReadBuildReceipts(path)
	if err != nil {
		return nil, err
	}
	out := make([]capture.Record, 0, len(recs)+len(builds))
	for _, r := range recs {
		if r.Act != entrypoint.ReceiptActRecord {
			continue
		}
		out = append(out, capture.Record{
			Bin: r.Bin, Platform: r.Platform, Time: r.Time, Digest: r.Digest,
		})
	}
	for _, r := range builds {
		// An empty source is not a fork's build: selection would file it with the installer
		// captures, which is the near-miss the kind exists to rule out.
		if r.Act != entrypoint.ReceiptActRecord || r.Source == "" {
			continue
		}
		out = append(out, capture.Record{
			Bin: r.Bin, Platform: r.Platform, Source: r.Source, Revision: r.Revision,
			Recipe: r.Recipe, Time: r.Time, Digest: r.Digest,
		})
	}
	return out, nil
}

// resolveForkBuild answers a FORK's query of the store (FP-D8): the entry selection names for
// (bin, platform, source) — newest wins, as for every program — and a HIT only when that entry was
// built at the revision and from the recipe asked for. A newer entry of another revision is a miss,
// not a substitution (§9: never serve a near-miss), and the error says which revision is there.
func resolveForkBuild(store *capture.Store, bin, platform, source, revision, recipe string) (*capture.Entry, *capture.Record, error) {
	selected, err := capture.Select(store, captureRecords)
	if err != nil {
		return nil, nil, err
	}
	best, ok := selected[capture.Program{Bin: bin, Platform: platform, Source: source}]
	if !ok {
		return nil, nil, fmt.Errorf("nothing in %s records a build of %s from %s", store.Dir, bin, source)
	}
	if best.Record.Revision != revision {
		return nil, nil, fmt.Errorf("the newest build of %s from %s is of commit %s, not the pinned %s",
			bin, source, best.Record.Revision, revision)
	}
	if best.Record.Recipe != recipe {
		return nil, nil, fmt.Errorf("the newest build of %s at %s used another build recipe", bin, revision)
	}
	entry, err := store.Resolve(best.Key)
	if err != nil {
		return nil, nil, err
	}
	return entry, &best.Record, nil
}

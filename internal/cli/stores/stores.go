// Package stores implements `yolo stores` — the READ-ONLY inventory of every
// store yolo can see on this machine, and the bounded sample ledger that makes
// growth measurable at all (docs/design/disk-levers-and-backfill.md §5.5,
// OQ-BF9).
//
// WHY IT IS NOT `prune --dry-run`. `yolo prune` prices what it WOULD delete;
// this prices what EXISTS. Every store with no reclaimer and no trigger — the
// unrooted /nix/store outputs, an uncovered cache subdir, the untagged podman
// rows minimal-disk-footprint.md OQ-DF3 REACH ruled permanently off-limits — is
// invisible to prune BY CONSTRUCTION, and those are exactly the rows this
// command exists to surface. A listing that could only show reclaimable stores
// would reproduce the blind spot it exists to close.
//
// FORBIDDEN, and these are the command's whole safety story: it never deletes,
// moves or mutates a store; never takes the nix GC lock (which would stall every
// concurrent host build); never launches a container to measure one; and never
// runs on the launch path or in the housekeeping slot. On demand only.
//
// THE ONE EXCEPTION TO "never mutates" IS THE LEDGER (OQ-BF9, ruled 2026-09-08):
// one dated line per store per run under the state dir, default on, --no-record
// to opt out, this command the SINGLE WRITER, bounded to the last 30 samples per
// store. It is bounded three ways — one writer, one line per store per run,
// thirty samples — so the handle on growth cannot itself become a store that
// grows. A pure-read command that can never report a rate fails the stated
// purpose; a pure-read command that records SILENTLY would be a defect, which is
// why ledger.go's writer is reached from exactly one place (Run, gated on
// !NoRecord) and pinned by TestRunRecordsOnlyWhenRecordingIsOn.
//
// Output discipline, inherited from the design this implements: NO FIGURE IS
// EVER PRINTED WITHOUT SAYING HOW IT WAS OBTAINED. Every size carries its
// Sizing (measured / partial / cached / absent / unknown), a partial figure is
// rendered as a lower bound, and the header states WHICH MACHINE'S VIEW was
// walked — paths.GlobalCache() resolves to the host's tree for a host yolo and
// to a jail's own for an in-jail one, and reading that expression in the wrong
// frame has already produced one wrong retraction in the design corpus.
package stores

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// Usage is what `yolo stores --help` prints. Registered in internal/cli's
// subcommandUsage, which is what makes it reachable.
//
// NO BACKTICKS: this is a raw string literal, and a backtick inside one ends it
// (the bug commit 2c4a10b3 fixed in prune's help two commits before this one).
const Usage = `Usage: yolo stores [--format json] [--age] [--no-record]

Inventory every store yolo can see on this machine: where it is, how big it is
and HOW that size was obtained, how fast it is growing, what reclaims it (or
"none"), what triggers that reclaimer (or "none"), and whether yolo may reclaim
it at all.

This is not "yolo prune --dry-run". Prune prices what it WOULD delete; this
prices what EXISTS — including the stores nothing reclaims, which are precisely
the ones prune cannot show you.

READ-ONLY. It never deletes, moves or mutates a store, never takes the nix GC
lock, and never launches a container. The one thing it writes is its own sample
ledger (see below), which no store reader depends on.

One section is not yours: a content-addressed host cache yolo recognises (pants'
lmdb_store today) is bind-mounted into a jail from the HOST user's own cache,
writable, rather than pooled a second time -- so the jail's private copy is
stranded and the host's bytes are what the tool reads. Those rows get their own
section, are marked "not yolo's", and are never summed into yolo's own footprint
or offered for reclaim. The section also explains every store yolo did NOT alias
and why.

Sizes are apparent sizes (the sum of file sizes), and each store's walk is
bounded to 60s: a store that runs out of budget reports what it had summed so
far as a lower bound, marked "partial".

Flags:
  --format <fmt>  Output format: text (default) or json. JSON is stable,
                  ANSI-free and on stdout -- the form to parse. Also
                  --format=json.
  --json        Shorthand for --format json. This is the spelling stores
                shipped first; both work here and on ps, prune, check,
                loopholes list and broker status.
  --age         Also report how much of each store is older than the cache
                purge's age rule. This is a per-file walk over the whole tree
                and costs minutes on a large cache, so each store prints its own
                elapsed time.
  --no-record   Do not append this run's sizes to the sample ledger. Recording
                is ON by default: a growth rate needs two dated samples, and a
                strictly read-only command can never produce the first one.
  --help, -h    Show this help. Answered before any store is walked, so asking
                what this command does costs nothing.

Examples:
  yolo stores                         # what exists, how big, and what reclaims it
  yolo stores --format json           # the same inventory, for a script or an agent
  yolo stores --age --no-record       # add the age columns; leave the ledger alone

The ledger: one dated line per store per run, under the state dir at
<state>/stores/<store>.samples, bounded to the last 30 samples per store. This
command is its only writer. Delete a file there and that store simply loses its
growth column until two more runs have gone by.`

// storeWalkBudget bounds ONE store's size walk. A store that exceeds it reports
// what it summed as a lower bound rather than a wrong total or an error.
//
// 60s is the design's number (§5.3 "Defaults, with units"), not a re-derivation:
// the walks this command makes are the same walks the housekeeping slot budgets,
// and two different numbers for one measurement would be a second policy.
const storeWalkBudget = 60 * time.Second

// probeTimeout bounds one container-runtime query. Short on purpose: an
// unreachable runtime must report "unknown" quickly, not hang an inventory.
const probeTimeout = 20 * time.Second

// Options configures a Run. Every seam is injectable so the whole command is
// deterministically testable against temp roots — the same discipline
// prune.Options keeps, and for the same reason: nothing here may be exercised
// only against the developer's real disk.
type Options struct {
	// --- flags ---
	JSON     bool // --json
	Age      bool // --age
	NoRecord bool // --no-record

	// --- seams ---
	// Out is where the report goes. nil => os.Stdout.
	Out io.Writer
	// Errs is where a diagnostic goes (never a store figure). nil => os.Stderr.
	Errs io.Writer
	// Color requests ANSI styling, honored only when IsTTYStdout() as well, so
	// piped output stays byte-stable plain text.
	Color       bool
	IsTTYStdout func() bool
	// Now is the clock seam (sample timestamps, walk deadlines, growth rates).
	Now func() time.Time
	// InJail reports whether this yolo runs inside a jail. It decides the FRAME
	// the header states, which is not cosmetic — see the package doc.
	InJail func() bool
	// DetectRuntime returns the effective container runtime. The CLI front door
	// injects the config-aware resolver; nil => a bare YOLO_RUNTIME/platform probe.
	DetectRuntime func() string
	// Exec is the container-runtime query seam, typed as prune's so a test fake
	// written for one works for the other. nil => realProbeExec.
	Exec prune.RunFunc
	// GlobalStorage / GlobalCache / SamplesDir resolve the roots. nil => the real
	// paths.* getters.
	GlobalStorage func() string
	GlobalCache   func() string
	SamplesDir    func() string
	// NixStore is the store directory yolo's own outputs live in. "" =>
	// "/nix/store". Set to a temp dir in tests.
	NixStore string
	// Budget bounds ONE store's walk. 0 => storeWalkBudget.
	Budget time.Duration
	// AgeDays is --age's cutoff in days. 0 => the cache purge's own rule, read
	// live off prune.NewDefaultOptions so the two can never disagree.
	AgeDays float64
	// Walk is the sizing seam. nil => walkTree. Injected by tests that need a
	// store to be unreadable or partial without depending on the filesystem.
	Walk WalkFunc
	// HostCAS answers L9's question — which recognised content-addressed host
	// caches a launch from this frame would ALIAS rather than pool a second copy
	// of (docs/design/disk-levers-and-backfill.md OQ-BF10). nil => the same
	// hostcas.Plan the launcher calls, over this frame's own facts.
	//
	// ONE PREDICATE, TWO READERS: the launch emits the mount and this command
	// explains the decision, and both go through hostcas so a row here cannot
	// describe an alias the launcher would not make. Nothing is recorded in
	// between — the decision is a pure function of the host's filesystem and
	// platform, so there is no stamp to go stale and no writer to name.
	HostCAS func() []hostcas.Disposition
}

// ParseArgs turns `yolo stores`'s argv into Options. args is the dispatched
// argv, so args[0] is the subcommand token.
//
// Unknown flags are ignored rather than fatal, matching every other yolo
// subcommand's parser (prune's, run's): this command has no destructive act to
// guard, so refusing an argument would only make it less useful than the tool it
// replaces.
func ParseArgs(args []string) Options {
	var o Options
	for i := 1; i < len(args); i++ {
		switch a := args[i]; {
		case a == "--json":
			o.JSON = true
		// `--format json` is the CANONICAL spelling across yolo's
		// state-reporting commands (docs/reference/self-documenting-cli.md item 7),
		// and it is accepted here so the family is uniform. `stores` shipped the
		// bare `--json` first and keeps it: an agent that learned one spelling
		// must not have to remember which command wants which, because a flag
		// whose name depends on the subcommand fails the standard it implements.
		//
		// An unknown --format VALUE is refused by the CLI front door before this
		// runs (internal/cli's parseOutputFormat), so nothing here can be reached
		// with one — which is why this only has to recognize the json case.
		case a == "--format="+outfmt.JSON:
			o.JSON = true
		case a == "--format" && i+1 < len(args) && args[i+1] == outfmt.JSON:
			o.JSON = true
			i++
		case a == "--age":
			o.Age = true
		case a == "--no-record":
			o.NoRecord = true
		}
	}
	return o
}

func fillDefaults(o *Options) {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.Errs == nil {
		o.Errs = os.Stderr
	}
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return false }
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.InJail == nil {
		o.InJail = func() bool { return os.Getenv("YOLO_VERSION") != "" }
	}
	if o.DetectRuntime == nil {
		o.DetectRuntime = func() string { return "" }
	}
	if o.Exec == nil {
		o.Exec = realProbeExec
	}
	if o.GlobalStorage == nil {
		o.GlobalStorage = paths.GlobalStorage
	}
	if o.GlobalCache == nil {
		o.GlobalCache = paths.GlobalCache
	}
	if o.SamplesDir == nil {
		o.SamplesDir = paths.StoreSamplesDir
	}
	if o.NixStore == "" {
		o.NixStore = "/nix/store"
	}
	if o.Budget == 0 {
		o.Budget = storeWalkBudget
	}
	if o.AgeDays == 0 {
		// The cache purge's own rule, read live rather than re-typed: --age's
		// "dead" column is only legible against the age a reclaimer actually
		// applies, and a second hardcoded 30 would drift the day that one moves.
		o.AgeDays = float64(prune.NewDefaultOptions().CacheAge)
	}
	if o.Walk == nil {
		o.Walk = walkTree
	}
	if o.HostCAS == nil {
		o.HostCAS = func() []hostcas.Disposition {
			return hostcas.Plan(hostcas.Facts{
				Runtime:       o.DetectRuntime(),
				IsMacOS:       paths.IsMacOS,
				HostPlatform:  hostcas.HostPlatform(),
				JailPlatform:  hostcas.JailPlatform(),
				HostCacheRoot: hostcas.CacheRoot(os.Getenv),
				// THE FRAME AGAIN: paths.GlobalCache() is the host's tree for a host
				// yolo and this jail's own for an in-jail one, so the "stranded"
				// path this row names is the one a launch FROM THIS FRAME would
				// strand. That is the same expression the launcher passes, which is
				// what makes the two answers comparable at all.
				JailCacheHost: o.GlobalCache(),
			})
		}
	}
}

// Run executes `yolo stores`. It returns 0 for any inventory, complete or not:
// ONE UNREADABLE STORE NEVER FAILS THE COMMAND (§5.5 "Degenerate inputs") — a
// partial inventory is the useful answer, and exiting non-zero on it would make
// the command unusable exactly where it matters most. The only non-zero is a
// failure to WRITE the report, which is not a fact about any store.
func Run(o Options) int {
	fillDefaults(&o)

	rep := Inventory(o)
	// Growth is read BEFORE this run's own sample is appended, or every store
	// would report a rate against itself. Pinned by
	// TestGrowthIgnoresThisRunsOwnSample.
	attachGrowth(&rep, o)
	if !o.NoRecord {
		rep.Recorded, rep.RecordErrs = record(rep, o)
		rep.SamplesDir = o.SamplesDir()
	}

	if o.JSON {
		enc, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			fmt.Fprintln(o.Errs, "yolo stores: could not encode the inventory: "+err.Error())
			return 1
		}
		fmt.Fprintln(o.Out, string(enc))
		return 0
	}
	renderText(rep, o)
	return 0
}

// realProbeExec runs one container-runtime query, capturing stdout and honoring
// the timeout, degrading to Ran=false on a missing binary, a start failure or a
// timeout. It is prune.realProbeExec's shape — the same seam type, so a fake
// written for one drives the other — reproduced here rather than shared because
// prune's is unexported.
func realProbeExec(argv []string, timeout time.Duration) prune.ProbeResult {
	if len(argv) == 0 {
		return prune.ProbeResult{}
	}
	if timeout <= 0 {
		timeout = probeTimeout
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return prune.ProbeResult{Ran: false}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return prune.ProbeResult{Ran: false}
	case <-done:
		rc := 0
		if cmd.ProcessState != nil {
			rc = cmd.ProcessState.ExitCode()
		}
		return prune.ProbeResult{Stdout: stdout.String(), RC: rc, Ran: true}
	}
}

// printer is the color-aware line writer, the same wrapper prune's report uses.
type printer struct{ richtext.Printer }

func (p printer) line(msg string) { p.Print(msg) }

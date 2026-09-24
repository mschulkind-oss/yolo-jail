package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/check"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/stores"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const macosSetupUsage = `Usage: yolo macos-setup

Provision the native macOS sandbox user the macos-user backend runs jails as: it
creates the ` + "`_yolojail`" + ` account (random password, no login shell) if it is
missing, sets up the shared group, and prepares the shared root. Idempotent —
re-running it reuses an existing account.

sudo is invoked for the account steps, so macOS may prompt for your admin
password. macOS only: on any other platform it refuses.

  --help, -h  Show this help. Answered before anything is created.

Examples:
  yolo macos-setup                    # create the sandbox account (idempotent)

See ` + "`yolo config-ref`" + ` for the ` + "`backend`" + ` key, and ` + "`yolo macos-teardown`" + ` to remove
the account again.`

const macosTeardownUsage = `Usage: yolo macos-teardown

Remove the ` + "`_yolojail`" + ` sandbox user and its group — the undo of
` + "`yolo macos-setup`" + `. Does nothing (exit 0) when the account does not exist.

sudo is invoked for the deletion, so macOS may prompt for your admin password.
macOS only: on any other platform it refuses.

  --help, -h  Show this help. Answered before anything is deleted.

Examples:
  yolo macos-teardown                 # remove the account and its group`

const macosUnshareUsage = `Usage: yolo macos-unshare <workspace>

Strip yolo-jail's ACLs from <workspace>, so the sandbox user can no longer reach
it. The path is resolved (absolute + symlinks) first and must be a directory.
This is how you take a workspace back out of the macos-user backend's reach
without tearing down the account.

macOS only: on any other platform it refuses.

  --help, -h  Show this help. Answered before any ACL is touched.

Examples:
  yolo macos-unshare ~/code/myproject # take that workspace out of the jail's reach

The inverse — putting the ACLs BACK on files that predate sharing — is
` + "`yolo macos-fix-permissions`" + `.`

const macosFixPermissionsUsage = `Usage: yolo macos-fix-permissions [path]

Retrofit the shared-group ACL onto files that already existed in the shared area
before it was shared. With no [path] it walks the whole shared root; with one it
walks that directory instead.

It refuses a path inside a user home: the macos-user backend only manages ACLs on
neutral ground. macOS only: on any other platform it refuses.

  --help, -h  Show this help. Answered before any ACL is applied.

Examples:
  yolo macos-fix-permissions          # retrofit the ACL across the whole shared root
  yolo macos-fix-permissions /Users/Shared/yolo-jail/myproject   # just this one`

// runMacosSetup/Teardown/Unshare/FixPermissions dispatch the four macos-*
// commands (macOS-only; refuse/no-op on Linux). Each answers its own `--help`
// first: these are sudo-invoking, ACL-rewriting commands, so "what does this do?"
// must be answerable without running them (see subhelp.go).
func runMacosSetup(args []string) int {
	if answerHelp("macos-setup", args, os.Stdout) {
		return 0
	}
	return macosuser.MacosSetup(macosuser.RealDeps(nil, nil, isTTYStdout()))
}
func runMacosTeardown(args []string) int {
	if answerHelp("macos-teardown", args, os.Stdout) {
		return 0
	}
	return macosuser.MacosTeardown(macosuser.RealDeps(nil, nil, isTTYStdout()))
}

func runMacosUnshare(args []string) int {
	if answerHelp("macos-unshare", args, os.Stdout) {
		return 0
	}
	ws := ""
	if len(args) > 1 {
		ws = args[1]
	}
	return macosuser.MacosUnshare(macosuser.RealDeps(nil, nil, isTTYStdout()), ws)
}

func runMacosFixPermissions(args []string) int {
	if answerHelp("macos-fix-permissions", args, os.Stdout) {
		return 0
	}
	path := ""
	if len(args) > 1 {
		path = args[1]
	}
	return macosuser.MacosFixPermissions(macosuser.RealDeps(nil, nil, isTTYStdout()), path)
}

// pruneUsage is what `yolo prune --help` prints. The flag list is exactly the
// flags runPrune parses (TestUsageListsEveryParsedFlag pins that), and the
// defaults are prune.NewDefaultOptions'.
const pruneUsage = `Usage: yolo prune [flags]

Reclaim disk that yolo is holding: stale containers, old jail images, the image
tarball cache, nix build/image GC roots, shadowed jail homes, heavy tool caches,
superseded install captures (every store entry but the newest per program —
what a materialize would never choose again), and the on-disk copies of yolo's
built-in packs: other builds' trees under the state dir's embedded-packs/, and
the per-process copies earlier builds left in TMPDIR. This build's own tree is
never removed, and a copy is removed only when nothing can still be using it —
no running yolo holds its lease or, for an old unleased copy, no live yolo
process could have created it.

DRY-RUN BY DEFAULT. With no flags it reports what it WOULD reclaim and deletes
nothing; --apply is the only thing that removes anything.

Flags:
  --apply                  Actually reclaim. Without it nothing is deleted.
  --no-containers          Skip the stale-container sweep.
  --no-images              Skip the old-jail-image sweep.
  --keep-images <n>        REMOVED, and passing it is an error. Image retention
                           is each workspace's CURRENT image (one pointer per
                           workspace, union'd with what containers are running),
                           so there is no global count to set: a superseded copy
                           is not kept at all. See the error the flag prints.
  --no-image-cache         Skip the image-tarball cache sweep.
  --image-cache-keep <n>   Keep the newest <n> cached image tarballs.
                           Default: 0 on podman (it streams the image straight
                           into the runtime and writes no tar), 3 on Apple
                           Container (which cannot stream, so its tar is the only
                           copy). An offline start still reads whatever tars
                           exist -- this bounds what is KEPT, not what is read.
  --no-build-roots         Skip the nix build GC roots.
  --no-image-roots         Skip the nix image GC roots.
  --no-shadowed-home       Skip the shadowed jail-home sweep.
  --no-embedded-packs      Skip the built-in pack copy sweep (embedded-packs/
                           and TMPDIR).
  --cache-age <days>       Only consider caches older than <days> (default 30;
                           0 skips the pass entirely).
  --purge-heavy-caches     Also purge the heavy tool caches (npm, go, cargo, …).
  --no-hardlink            Do not hardlink-dedup identical cache files.
  --dedup-global           Dedup across every workspace, not just this one.
  --nix-gc                 Run the bounded host nix store GC. Opt-in, host-only,
                           and gated on every known image closure having a
                           durable GC root; it refuses inside a jail.
  --nix-gc-max <bytes>     Ceiling for that GC (default 50 GiB). A ceiling, not a
                           target: nix stops once it has freed this many bytes.
` + outputFormatUsage + `
  --help, -h               Show this help. Answered before the disk is scanned,
                           so asking what prune does costs nothing.

The JSON document carries the usage accounting, the per-category bytes and counts
that make up the total, what would disappear by name, and a ` + "`declined`" + ` flag for a
sweep that could not run. It is the SUMMARY, not the whole report: the teaching
prose (why a cached tarball is reclaimable, why the nix GC declined) stays in the
text form, which is where it was written for a reader.

Examples:
  yolo prune                          # what would this reclaim? (deletes nothing)
  yolo prune --format json            # the same plan, for a script or an agent
  yolo prune --apply                  # actually reclaim it`

// runPrune runs `yolo prune` (disk reclaim). Default dry-run; --apply reclaims.
func runPrune(args []string) int {
	// Before the scan: a full disk report is the LAST thing someone asking what
	// this command does wants to wait for. See subhelp.go.
	if answerHelp("prune", args, os.Stdout) {
		return 0
	}
	// A REMOVED FLAG REFUSES AND NAMES ITS REPLACEMENT, the way the retired
	// config keys do (internal/config/validate.go's `agents`, `docker`,
	// `journal`, `host_processes`). pruneOptions has no default case, so a
	// dropped flag would otherwise be SILENTLY IGNORED -- and for this one that
	// is the worst possible reading: someone passing `--keep-images 8` to hold
	// more images back would get a pass that removes every image no workspace
	// points at, and no indication that the number they typed did nothing.
	if refused := refuseRemovedPruneFlags(args, os.Stderr); refused != 0 {
		return refused
	}
	// Before the scan, for the same reason help is: a rejected --format is misuse,
	// and walking the whole disk only to refuse afterwards spends minutes on an
	// answer nobody gets. See outputformat.go.
	if _, ok := parseOutputFormat("prune", args, os.Stderr); !ok {
		return 2
	}
	// After the REMOVED-flag refusal above, which names replacements — a flag that once existed
	// deserves the better message, and this generic one catches everything else.
	if refuseUnknownFlags("prune", args, pruneKnownFlags, os.Stderr) {
		return 2
	}
	return prune.Run(pruneOptions(args))
}

// refuseRemovedPruneFlags fails the command when argv carries a flag `yolo
// prune` no longer has, naming what replaced it. Returns 0 when there is
// nothing to refuse.
//
// Only `--keep-images` is listed, and the entry stays for one release for the
// same reason removeRetiredGeneratedDirs and the retired config keys do: the
// people who need the message are the ones with the old flag in a script or in
// muscle memory, and they find out either here or by wondering why their number
// stopped mattering.
func refuseRemovedPruneFlags(args []string, out io.Writer) int {
	for _, a := range args {
		if a != "--keep-images" && !strings.HasPrefix(a, "--keep-images=") {
			continue
		}
		fmt.Fprintln(out, "Error: --keep-images was removed (OQ-LS3).")
		fmt.Fprintln(out, "Image retention is no longer a count: yolo keeps each workspace's "+
			"CURRENT image (one pointer per workspace, plus every image a container is "+
			"running on) and removes the superseded ones, because a superseded copy is an "+
			"undo buffer nobody used.")
		fmt.Fprintln(out, "Fix: drop the flag. To keep an image, launch its workspace -- that "+
			"is what records the pointer. `yolo prune` (dry-run) reports how many pointers "+
			"it found and exactly which images it would remove.")
		return 2
	}
	return 0
}

// pruneKnownFlags is prune's whole flag surface, read off pruneOptions' own scan plus the two the
// front door parses (--format/--json). Kept beside the scan it mirrors so the two move together.
var pruneKnownFlags = []string{
	"--apply", "--cache-age", "--dedup-global", "--image-cache-keep", "--no-build-roots",
	"--no-containers", "--no-hardlink", "--no-image-cache", "--no-image-roots", "--no-images",
	"--no-shadowed-home", "--no-embedded-packs", "--purge-heavy-caches",
	"--format", "--json", "--help", "-h", "prune",
}

// pruneOptions turns `yolo prune`'s argv into the engine's Options — the flags plus the three
// things prune cannot resolve for itself (the config-aware runtime, the relocated cache dirs,
// and the capture-receipt reader).
//
// SPLIT OUT OF runPrune SO THE WIRING IS TESTABLE. Each of those three is a line that can be
// deleted with prune's own suite still green and a whole section of the report silently
// degraded; a test that constructs the Options and inspects them is the only thing that fails
// when one goes missing.
func pruneOptions(args []string) prune.Options {
	opts := prune.NewDefaultOptions()
	opts.Color = true
	// The superseded-capture sweep's receipt reader. prune does not import the receipt
	// schema (it lives in internal/entrypoint, which pulls internal/config), so the front
	// door hands over the one adapter the materialize path already uses — the same reader,
	// so the resolver and the reaper cannot disagree about which lines are records. Without
	// it the section declines and says so; see prune.Options.CaptureRecords.
	opts.CaptureRecords = captureRecords
	// The output format. Assigned HERE rather than in runPrune so it sits on the
	// same TESTED seam as CaptureRecords and the runtime resolver below — a line
	// that can be deleted with prune's own suite still green is exactly what this
	// function was split out to make catchable (TestPruneOptionsWiring).
	//
	// The ok is discarded because runPrune already REFUSED an unknown value before
	// the disk was touched, so this call cannot see one the front door let through.
	// Two calls to a pure scan is the price of having the refusal early and the
	// wiring testable, and it is cheaper than either alternative.
	opts.Format, _ = parseOutputFormat("prune", args, io.Discard)
	// args: ["prune", <flags>...]
	for i := 1; i < len(args); i++ {
		a := args[i]
		nextInt := func(def int) int {
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil {
					return n
				}
			}
			return def
		}
		switch {
		case a == "--apply":
			opts.Apply = true
		case a == "--no-hardlink":
			opts.NoHardlink = true
		case a == "--dedup-global":
			opts.DedupGlobal = true
		case a == "--no-containers":
			opts.NoContainers = true
		case a == "--no-images":
			opts.NoImages = true
		case a == "--no-image-cache":
			opts.NoImageCache = true
		case a == "--no-build-roots":
			opts.NoBuildRoots = true
		case a == "--no-image-roots":
			opts.NoImageRoots = true
		case a == "--no-shadowed-home":
			opts.NoShadowedHome = true
		case a == "--no-embedded-packs":
			opts.NoEmbeddedPacks = true
		case a == "--image-cache-keep":
			// The fallback is the CURRENT value, which is prune.ImageCacheKeepUnset
			// unless the flag already appeared — so a malformed value leaves the
			// per-runtime default in place rather than pinning it to a number.
			opts.ImageCacheKeep = nextInt(opts.ImageCacheKeep)
		case a == "--cache-age":
			opts.CacheAge = nextInt(opts.CacheAge)
		case a == "--purge-heavy-caches":
			opts.PurgeHeavyCaches = true
		case a == "--nix-gc":
			opts.NixGC = true
		case a == "--nix-gc-max":
			// A byte ceiling for the bounded store GC (default 50 GiB when unset).
			if i+1 < len(args) {
				i++
				if n, err := strconv.ParseInt(args[i], 10, 64); err == nil {
					opts.NixGCMaxBytes = n
				}
			}
		}
	}
	// Config- and platform-aware runtime resolution (audit findings 4+5): prune
	// otherwise defaults to the config-blind podman-only detector, so on an Apple
	// Container host it enumerates via podman and its stale-tracking sweep could
	// delete live jails' files.
	ws, err := os.Getwd()
	if err != nil {
		ws = "."
	}
	opts.DetectRuntime = func() string { return detectListingRuntime(ws) }
	// cache_relocations is user-scope only (config.LoadCacheRelocations owns the
	// threat model), and internal/prune deliberately does not import
	// internal/config — so the front door resolves the pairs and hands them over
	// as plain data. Without this prune goes blind on a relocated subdir: the
	// host-side cache/<subdir> is an empty bind mountpoint, so the machine's
	// largest consumer vanishes from the report and the heavy purge walks the
	// stub while reporting success.
	//
	// A load error is ignored rather than failing the command: prune's job is
	// reclaiming disk, and it can still do all of that with a stale or
	// unparseable user config. Loader warnings go to stderr so a skipped entry
	// explains the subdir that is missing from the report, without polluting the
	// report's own (stdout, contract-stable) output.
	if rels, err := config.LoadCacheRelocations(func(msg string) {
		fmt.Fprintln(os.Stderr, "Warning: "+msg)
	}); err == nil && len(rels) > 0 {
		m := make(map[string]string, len(rels))
		for _, r := range rels {
			m[r.Subdir] = r.Target
		}
		opts.CacheRelocations = m
	}
	return opts
}

// runStores runs `yolo stores` (the read-only store inventory + its bounded
// sample ledger). Everything it does lives in internal/cli/stores; this front
// door exists to answer --help before any store is walked, and to hand over the
// ONE thing that package cannot resolve for itself.
//
// That one thing is the CONFIG-AWARE RUNTIME, and it is the same line prune's
// front door carries for the same reason: internal/cli/stores does not import
// internal/config, so without this it would fall back to a config-blind probe
// and report an Apple Container host's image store as "no container runtime".
// A line that can be deleted with the stores package's own suite still green,
// which is why storesOptions is split out and pinned by TestStoresOptionsWiring.
func runStores(args []string) int {
	// Before the walk: an inventory of every store on the machine is the LAST
	// thing someone asking what this command does wants to wait for. See subhelp.go.
	if answerHelp("stores", args, os.Stdout) {
		return 0
	}
	return stores.Run(storesOptions(args))
}

// storesOptions turns `yolo stores`'s argv into the engine's Options plus the
// config-aware runtime resolver — see runStores for why that one seam is the
// front door's to inject.
func storesOptions(args []string) stores.Options {
	opts := stores.ParseArgs(args)
	opts.Color = true
	opts.IsTTYStdout = isTTYStdout
	ws, err := os.Getwd()
	if err != nil {
		ws = "."
	}
	opts.DetectRuntime = func() string { return detectListingRuntime(ws) }
	return opts
}

const hostDaemonUsage = `Usage: yolo host-daemon <subcommand> [<name>]

Manage this machine's HOST-WIDE DAEMONS: the processes that run one per host,
outside every jail, serving all of them — the Claude OAuth broker among them. A
loophole creates one by declaring ` + "`host_daemon.scope: \"host\"`" + ` in its manifest, and
that declaration is the only thing that puts a name in this list: yolo derives the
set, it does not carry one.

Nothing supervises these daemons. A launch ENSURES the ones it needs and never
looks again, so this is the whole of the human-facing lifecycle: see one, stop
one, cycle one after a yolo upgrade, read its log.

Subcommands:
  status [<name>]     One daemon's pid, socket, whether the socket is ACCEPTING,
                      and a healthy verdict. With no name: every daemon in the
                      set. Exit 1 if any reported daemon is not healthy.
  stop <name>         Stop it. The next launch that wants it ensures it again.
  restart <name>      Stop it and start a fresh one — the fix for a daemon that
                      is alive but speaks an older wire contract than this yolo.
  logs <name> [flags] Print its log.

THE NAME IS REQUIRED BY EVERY VERB THAT ACTS. Only ` + "`status`" + ` defaults to the whole
set, because it reports rather than acts; ` + "`stop`" + `, ` + "`restart`" + ` and ` + "`logs`" + ` refuse and
list the set instead of picking a daemon for you. A default target across three
daemons is how a management surface comes to act on the wrong one.

logs flags:
  -n, --lines <n>     Show the last <n> lines (default 50; also -n<n> and
                      --lines=<n>).
  -f, --follow        Follow the log as it grows.

status flags:
` + outputFormatUsage + `

  --help, -h          Show this help.

` + "`status --format json`" + ` carries one object per daemon: its loophole name, whether
anything still declares it, the pid, the socket, whether the socket is ACCEPTING
(an accept probe, not a protocol round trip) and a ` + "`healthy`" + ` verdict — the same
conjunction the exit code uses, so you need not re-derive it.

Examples:
  yolo host-daemon status                     # every host-wide daemon
  yolo host-daemon status --format json       # the same, for a script or an agent
  yolo host-daemon restart aws-auth           # cycle one wedged daemon
  yolo host-daemon logs openai-auth-broker -n 100

` + "`yolo broker <subcommand>`" + ` is the retained alias for the Claude OAuth broker, and
is exactly ` + "`yolo host-daemon <subcommand> claude-oauth-broker`" + `.
` + "`yolo loopholes list`" + ` shows which loopholes this machine has and whether they are
wired into a jail.`

const brokerUsage = `Usage: yolo broker <subcommand>

Manage the Claude OAuth broker: the host daemon that SERIALIZES Claude OAuth
refreshes, so several jails sharing one account cannot race and burn the
single-use refresh token. It runs on your machine, outside every jail.

THIS IS AN ALIAS. ` + "`yolo broker <subcommand>`" + ` means
` + "`yolo host-daemon <subcommand> claude-oauth-broker`" + `, and the broker is one of
several host-wide daemons: see ` + "`yolo host-daemon --help`" + ` for the others and for
the surface this one is a shortcut into. It is retained because it is in muscle
memory and in the docs, and it always resolves to the Claude singleton — never to
whichever daemon happens to be running.

Subcommands:
  status              Whether the broker is running, its pid and socket, and the
                      state of the token it is guarding.
  stop                Stop the broker.
  restart             Restart it (the fix for a wedged or stale daemon).
  logs [flags]        Print the broker's log.

logs flags:
  -n, --lines <n>     Show the last <n> lines (default 50; also -n<n> and
                      --lines=<n>).
  -f, --follow        Follow the log as it grows.

status flags:
` + outputFormatUsage + `

  --help, -h          Show this help.

` + "`status --format json`" + ` carries the pid, the socket, whether the socket is
ACCEPTING (an accept probe, not a protocol round trip) and a ` + "`healthy`" + ` verdict —
the same conjunction the exit code uses, so you need not re-derive it.

Examples:
  yolo broker status                  # is the broker up and accepting?
  yolo broker status --format json    # the same, for a script or an agent
  yolo broker restart                 # cycle a wedged daemon
  yolo broker logs -n 100             # the last 100 log lines

The broker is a loophole: ` + "`yolo loopholes list`" + ` shows whether it is wired into
this jail, and ` + "`yolo config-ref`" + ` documents the ` + "`loopholes`" + ` key that enables it.`

// runHostDaemon dispatches `yolo host-daemon {status,stop,restart,logs} [<name>]`,
// the management verb over the HOST-SCOPED SET.
//
// The set is derived per invocation from the manifests this machine can read plus
// the rendezvous files on its disk (broker.Singletons) — never from a list here,
// which is precisely how `yolo broker` stayed at one daemon while three came to
// exist (OQ-HD2, docs/design/host-daemon-ownership.md).
func runHostDaemon(args []string) int {
	if answerHelp(broker.HostDaemonVerb, args, os.Stdout) {
		return 0
	}
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	format, ok := parseOutputFormat(broker.HostDaemonVerb, args, os.Stderr)
	if !ok {
		return 2
	}
	name, rest := hostDaemonTarget(rest)
	return hostDaemonDispatch(broker.HostDaemonVerb, sub, name, rest, format)
}

// runBroker dispatches `yolo broker {status,stop,restart,logs}` — the ALIAS,
// resolving to the Claude singleton and to nothing else. args is the rewritten
// argv[1:] (args[0]=="broker").
//
// It resolves through broker.BrokerSingleton()'s own constants rather than through
// discovery, so the alias keeps working exactly where discovery is empty: in a
// jail, on a host whose `packs` list does not name claude, in any process that
// staged nothing. The daemon it manages is at a path that has not moved.
func runBroker(args []string) int {
	// Answered here rather than in the `default:` branch below, which is MISUSE:
	// help is a request (stdout, exit 0), misuse is an error (stderr, exit 1), and
	// conflating them is what made `yolo broker --help` exit 1 (self-documenting-cli
	// item 3).
	if answerHelp("broker", args, os.Stdout) {
		return 0
	}
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	format, ok := parseOutputFormat("broker", args, os.Stderr)
	if !ok {
		return 2
	}
	return hostDaemonDispatch("broker", sub, broker.BrokerLoopholeName, rest, format)
}

// hostDaemonDispatch is the ONE body both front doors run: the alias and the
// general verb differ in how the target NAME is chosen and in nothing else.
//
// verb is the command the user typed, so every message this prints is a command
// they can retype. name is "" only from the general verb with no name given,
// which `status` reads as "the whole set" and the three acting verbs refuse.
func hostDaemonDispatch(verb, sub, name string, rest []string, format string) int {
	switch sub {
	case "status":
		if name == "" {
			return hostDaemonSetStatus(format)
		}
	case "stop", "restart", "logs":
		// REFUSED, not ignored, on the three verbs that ACT. `status` is the only
		// one that reports state; the others stop, cycle or tail, and answering a
		// request for machine-readable output with prose is the failure the flag
		// exists to prevent (outputformat.go).
		if outfmt.IsJSON(format) {
			// NO PIECE OF THIS MESSAGE MAY BEGIN WITH `--`:
			// TestUsageListsEveryParsedFlag reads every string literal in a handler
			// that starts with two dashes as a flag the handler PARSES, so a
			// continuation line beginning "--format json` does." becomes a demand
			// that the help document a flag spelled with a sentence in it.
			fmt.Fprintf(os.Stderr, "yolo %s %s: json output is not available here "+
				"— this verb acts, it does not report state. Only `yolo %s "+
				"status` reports, and it takes the flag.\n", verb, sub, verb)
			return 2
		}
		if name == "" {
			// A DEFAULT TARGET IS THE DEFECT, so there is not one: acting on "the
			// first" or "the broker" is how a generic verb comes to cycle a daemon
			// nobody named. Exit 2 (misuse), with the derived set for the retype.
			fmt.Fprintf(os.Stderr, "yolo %s %s: name the host-wide daemon to act on.\n  %s\n",
				verb, sub, hostDaemonNames())
			return 2
		}
	default:
		fmt.Fprintf(os.Stderr, "Usage: yolo %s {status|stop|restart|logs}\n", verb)
		return 1
	}

	target, ok := resolveHostDaemon(verb, name)
	if !ok {
		return 2
	}
	deps := broker.CLIDepsFor(target)
	deps.Format = format
	if sub == "status" {
		return broker.PrintStatus(deps)
	}
	return hostDaemonAction(deps, sub, rest)
}

// resolveHostDaemon turns a NAME into the singleton record the command bodies act
// on, refusing a name the derived set does not hold — by name, with the set
// listed.
//
// The Claude broker resolves from this package's own constants when discovery does
// not hold it, which is what makes `yolo broker` an alias rather than a second
// surface that can go dark: see runBroker.
func resolveHostDaemon(verb, name string) (broker.Singleton, bool) {
	set := broker.Singletons(hostDaemonWorkspace())
	if s, ok := broker.ResolveSingleton(set, name); ok {
		return s, true
	}
	fmt.Fprintf(os.Stderr, "yolo %s: %s\n", verb, broker.UnknownSingleton(name, set))
	return broker.Singleton{}, false
}

// hostDaemonSetStatus reports every member of the derived set — what a bare
// `yolo host-daemon status` means, and the only verb that defaults to all of them.
func hostDaemonSetStatus(format string) int {
	return broker.PrintSetStatus(broker.SetDeps{
		Out:         os.Stdout,
		Err:         os.Stderr,
		Color:       true,
		IsTTYStdout: isTTYStdout,
		Format:      format,
		Set:         broker.Singletons(hostDaemonWorkspace()),
		For:         broker.CLIDepsFor,
	})
}

// hostDaemonNames renders the derived set for a refusal, so a user who omitted the
// name learns what the choices are on THIS machine rather than being sent to a doc.
func hostDaemonNames() string {
	set := broker.Singletons(hostDaemonWorkspace())
	if len(set) == 0 {
		return "This machine declares no host-wide daemon and is running none."
	}
	names := make([]string, 0, len(set))
	for _, s := range set {
		names = append(names, s.Name)
	}
	return "Known: " + strings.Join(names, " ")
}

// hostDaemonWorkspace is the tree the PLACEMENT rule judges a daemon's program
// against — the cwd, which is the workspace a launch from here would mount :rw.
// An unreadable cwd narrows the rule to the jail-home tree rather than disabling
// it, which is the same degradation the doctor path takes.
func hostDaemonWorkspace() string {
	ws, err := os.Getwd()
	if err != nil {
		return ""
	}
	return ws
}

// hostDaemonTarget splits the daemon NAME out of a verb's remaining argv: the
// first bare token that is not a flag and not a flag's VALUE.
//
// The value skip is not decoration — `yolo host-daemon logs -n 100 aws-auth`
// would otherwise resolve to a daemon called "100", and report an unknown name
// while the real one sat later in the same argv.
func hostDaemonTarget(rest []string) (string, []string) {
	valueFlags := map[string]bool{"-n": true, "--lines": true, "--format": true}
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if valueFlags[a] {
			i++
			continue
		}
		if strings.HasPrefix(a, "-") {
			continue
		}
		out := append(append([]string{}, rest[:i]...), rest[i+1:]...)
		return a, out
	}
	return "", rest
}

// hostDaemonAction runs the three verbs that ACT rather than report: stop,
// restart, and logs (with logs' own -n/--lines and -f/--follow parse).
//
// Split out of the dispatcher so the json refusal above is ONE branch covering all
// three, instead of the same three-line guard copy-pasted into each case — the
// shape where the fourth verb added later quietly gets no guard.
func hostDaemonAction(deps broker.CLIDeps, sub string, rest []string) int {
	switch sub {
	case "stop":
		return broker.Stop(deps)
	case "restart":
		return broker.Restart(deps)
	}
	// logs: -n/--lines (default 50) and -f/--follow.
	lines, follow := 50, false
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		switch {
		case a == "-f" || a == "--follow":
			follow = true
		case a == "-n" || a == "--lines":
			if i+1 < len(rest) {
				i++
				if n, err := strconv.Atoi(rest[i]); err == nil {
					lines = n
				}
			}
		case strings.HasPrefix(a, "-n"):
			if n, err := strconv.Atoi(a[2:]); err == nil {
				lines = n
			}
		case strings.HasPrefix(a, "--lines="):
			if n, err := strconv.Atoi(a[len("--lines="):]); err == nil {
				lines = n
			}
		}
	}
	return broker.Logs(deps, lines, follow)
}

const initUsage = `Usage: yolo init [--mount <path>]...

Scaffold this workspace: write a commented yolo-jail.jsonc, append .yolo/ to
.gitignore, and print the agent briefing. An existing yolo-jail.jsonc is never
overwritten — re-running init on a configured workspace just reprints the
briefing.

Flags:
  --mount, -m <path>  Mount a host path read-only into the jail, at
                      /ctx/<basename> or at an explicit "host:container" target.
                      Repeatable; also --mount=<path>.
  --help, -h          Show this help. Answered BEFORE anything is written:
                      asking what init does must not scaffold your project.

Examples:
  yolo init                           # scaffold this workspace
  yolo init -m ~/datasets             # ... with ~/datasets mounted at /ctx/datasets
  yolo init -m ~/notes:/ctx/kb        # ... at an explicit target

Every key the generated file can carry is documented by ` + "`yolo config-ref`" + `; the
user-level defaults every workspace inherits are ` + "`yolo init-user-config`" + `.`

// runInit runs `yolo init` (scaffold yolo-jail.jsonc + briefing). Parses
// repeatable --mount/-m.
func runInit(args []string) int {
	// FIRST, before the scaffold. This is the command the whole per-subcommand
	// help item was filed for: `yolo init --help` used to fall through this flag
	// scan and write yolo-jail.jsonc + .gitignore into the cwd, so asking a
	// command what it does changed the project you asked from. See subhelp.go.
	if answerHelp("init", args, os.Stdout) {
		return 0
	}
	if refuseUnknownFlags("init", args, []string{"--mount", "-m", "--help", "-h", "init"},
		os.Stderr) {
		return 2
	}
	var mounts []string
	for i := 1; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--mount" || a == "-m":
			if i+1 < len(args) {
				i++
				mounts = append(mounts, args[i])
			}
		case strings.HasPrefix(a, "--mount="):
			mounts = append(mounts, a[len("--mount="):])
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "cannot resolve cwd: %v\n", err)
		return 1
	}
	return Init(cwd, mounts, os.Stdout, isTTYStdout())
}

const initUserConfigUsage = `Usage: yolo init-user-config

Write the USER-level defaults at ~/.config/yolo-jail/config.jsonc — the settings
every workspace on this machine inherits (packs, runtime, resources, loopholes),
which a workspace's own yolo-jail.jsonc then narrows. An existing file is left
untouched.

  --help, -h  Show this help. Answered before the file is written.

Examples:
  yolo init-user-config               # write this machine's defaults, once

` + "`yolo config-ref`" + ` documents every key; ` + "`yolo init`" + ` does the per-workspace half.`

// runInitUserConfig runs `yolo init-user-config`.
func runInitUserConfig(args []string) int {
	// Before the write, for the same reason as `init`: this one creates
	// ~/.config/yolo-jail/config.jsonc, so an unanswered --help edited the user's
	// machine-wide config. See subhelp.go.
	if answerHelp("init-user-config", args, os.Stdout) {
		return 0
	}
	return InitUserConfig(os.Stdout)
}

func isTTYStdout() bool {
	return tty.IsTerminalFile(os.Stdout)
}

const configRefUsage = `Usage: yolo config-ref

Print the full configuration reference: every key accepted by a workspace
yolo-jail.jsonc and by ~/.config/yolo-jail/config.jsonc, with its type, default
and meaning. This is the schema document, and it is long — pipe it, or search it.

  --help, -h  Show this help. The reference ITSELF is what a bare
              ` + "`yolo config-ref`" + ` prints; this only describes the command.

Examples:
  yolo config-ref                     # the whole schema (long — pipe it)
  yolo config-ref | grep -n loopholes # find the section you need

Scaffold the files it documents with ` + "`yolo init`" + ` and ` + "`yolo init-user-config`" + `.`

// runConfigRef prints the full configuration reference.
func runConfigRef(args []string) int {
	// `config-ref` is not destructive, but a caller typing --help asked about the
	// COMMAND, and answering with 700 lines of schema is not an answer. Same rule
	// as everywhere else: help is what was requested.
	if answerHelp("config-ref", args, os.Stdout) {
		return 0
	}
	return RunStdout()
}

const loopholesUsage = `Usage: yolo loopholes [subcommand]

A loophole is a HOST capability deliberately wired into the jail — a host daemon,
a socket pass-through, a TLS intercept, a device — each one an explicit hole in an
otherwise isolated container. This group is how you see which ones exist, whether
they are active here, and whether they are healthy.

Subcommands:
  list              Every installed loophole, its source (bundled, pack-shipped
                    or config-inline) and whether it is enabled here. This is the
                    default: a bare ` + "`yolo loopholes`" + ` lists.
  status            Run each loophole's own self-check. HOST-SIDE: inside a jail
                    it says so and does nothing, because the things being checked
                    are host daemons.
  enable <name>     Print the config key that turns <name> on …
  disable <name>    … or off. NEITHER WRITES ANYTHING YET: both print the exact
                    ` + "`loopholes`" + ` block to add to ~/.config/yolo-jail/config.jsonc and
                    exit non-zero, rather than silently reformatting a config file
                    you hand-wrote.

Flags for ` + "`list`" + ` and ` + "`status`" + ` (the two that report state):
` + outputFormatUsage + `

  --help, -h        Show this help.

` + "`list --format json`" + ` gives one object per loophole with ` + "`state`" + ` ("active",
"disabled", "inactive") and ` + "`reason`" + ` as SEPARATE fields, so you branch on the
state instead of parsing "inactive (superseded)". ` + "`status --format json`" + ` gives
each loophole's ` + "`state`" + ` and its doctor exit code, with ` + "`rc: null`" + ` for one that
declares no self-check — which is not the same answer as ` + "`rc: 0`" + `.

Examples:
  yolo loopholes list                   # what is wired into this jail?
  yolo loopholes list --format json     # the same, for an agent
  yolo loopholes status                 # host-side: is each one healthy?

The ` + "`loopholes`" + ` config key is documented in ` + "`yolo config-ref`" + `; a pack can ship one
(` + "`yolo pack --help`" + `, the ` + "`loophole`" + ` kind).`

// runLoopholes dispatches the `yolo loopholes {list,status,enable,disable}`
// group. args is the rewritten argv[1:], so args[0] == "loopholes" and args[1]
// is the sub-subcommand.
func runLoopholes(args []string) int {
	// Before the switch: `--help` used to land on the `default:` MISUSE branch and
	// exit 1 with a one-line usage on stderr. Help is a request, not an error
	// (self-documenting-cli item 3) — stdout, exit 0, and the full text.
	if answerHelp("loopholes", args, os.Stdout) {
		return 0
	}
	// args: ["loopholes", <sub>, <rest>...]
	var sub string
	var rest []string
	if len(args) > 1 {
		sub = args[1]
		rest = args[2:]
	}
	format, ok := parseOutputFormat("loopholes", args, os.Stderr)
	if !ok {
		return 2
	}
	deps := loopholes.RealDeps()
	deps.Format = format
	switch sub {
	case "", "list":
		return loopholes.List(deps)
	case "status":
		return loopholes.Status(deps)
	case "enable", "disable":
		// REFUSED, not ignored, on the verbs that ACT. enable/disable print a
		// config block for a human to paste and exit non-zero; there is no state
		// report to serialize, and accepting `--format json` here would answer a
		// request for machine-readable output with prose — the exact failure the
		// flag exists to prevent (outputformat.go).
		if outfmt.IsJSON(format) {
			fmt.Fprintf(os.Stderr, "yolo loopholes %s: --format json is not available "+
				"here — this verb prints a config block to paste, it does not report "+
				"state. `yolo loopholes list --format json` and `… status --format json` do.\n", sub)
			return 2
		}
		if len(rest) < 1 {
			fmt.Fprintf(os.Stderr, "Usage: yolo loopholes %s <name>\n", sub)
			return 1
		}
		return loopholes.CmdSetEnabled(deps, rest[0], sub == "enable")
	default:
		fmt.Fprintf(os.Stderr, "Usage: yolo loopholes {list|status|enable|disable} [name]\n")
		return 1
	}
}

const psUsage = `Usage: yolo ps [--format json]

List the running yolo-* jails and the workspace each one is attached to, so you
can tell whether ` + "`yolo`" + ` in this directory would ATTACH to a live jail or start a
fresh one.

The container runtime is resolved the same way a launch resolves it (YOLO_RUNTIME,
then the ` + "`runtime`" + ` config key, then a platform probe), so on a Mac running Apple
Container this lists that runtime's jails rather than an empty podman.

Flags:
` + outputFormatUsage + `
  --help, -h               Show this help.

The JSON carries one object per jail (name, status, workspace, and a problem
string when it has one) plus an ` + "`enumerated`" + ` boolean. CHECK THAT BOOLEAN FIRST:
false means the runtime could not be reached, which is a different state from an
idle machine and must not be read as "no jails".

Examples:
  yolo ps                       # is a jail already up for this workspace?
  yolo ps --format json         # the same, for a script or an agent

` + "`yolo prune`" + ` reclaims what the jails listed here have left behind.`

// runPs runs `yolo ps` (list running jails). ps takes no flags of its own beyond
// help. Uses platform-aware runtime resolution: on macOS with Apple Container
// running, `podman ps` would be empty and the tracking-prune would delete live
// jails' files.
func runPs(args []string) int {
	if answerHelp("ps", args, os.Stdout) {
		return 0
	}
	// Parsed before the probes for the same reason help is: a rejected --format is
	// misuse, and running the runtime queries first only to refuse afterwards
	// spends the machine on an answer nobody gets. See outputformat.go.
	format, ok := parseOutputFormat("ps", args, os.Stderr)
	if !ok {
		return 2
	}
	if refuseUnknownFlags("ps", args, []string{"--format", "--json", "--help", "-h", "ps"},
		os.Stderr) {
		return 2
	}
	ws, err := os.Getwd()
	if err != nil {
		ws = "."
	}
	detect := func() string { return detectListingRuntime(ws) }
	return psRun(psRealDeps(psRunCmd, detect, format))
}

// checkKnownFlags is check/doctor's whole flag surface. `--format` is parsed by
// parseOutputFormat above; it is listed here so the unknown-flag scan does not refuse it.
var checkKnownFlags = []string{"--build", "--no-build", "--accept-config-changes", "--format",
	"--json", "--help", "-h", "check", "doctor"}

// checkOptions turns `yolo check`/`yolo doctor`'s argv into check's Options.
// ok=false means the caller must return 2: a flag value was refused and the
// reason is already on errw.
//
// SPLIT OUT OF runCheck SO THE WIRING IS TESTABLE, the same reason pruneOptions
// and storesOptions exist. `check` cannot be driven end-to-end in a unit suite —
// it probes the runtime, the nix daemon and, by default, runs a full image build
// — so a line here that hands an option to the engine is a line no test would
// miss the deletion of unless the assembly itself is inspectable. Format was
// exactly that line. See TestCheckOptionsWiring.
func checkOptions(args []string, errw io.Writer) (check.Options, bool) {
	opts := check.NewDefaultOptions()
	opts.Color = true
	// The orphan-cleanup prompt's input. Missing until 2026-09-16, which made that
	// prompt UNREACHABLE: check.Options.Stdin defaulted to nil, the prompt printed,
	// the nil branch answered "N" every time, and the report then prescribed the
	// command it had just declined to run. This one line is the fix, and it is the
	// exact line the doc comment above says a test would otherwise never miss.
	//
	// Unconditional here, gated at the prompt: orphanCleanupPrompt asks only on a
	// terminal, so a piped stdin cannot be read as an implicit yes.
	opts.Stdin = os.Stdin
	// Before the sections, one of which is a nix image build: a rejected --format
	// must not cost minutes. See outputformat.go.
	format, ok := parseOutputFormat("check", args, errw)
	if !ok {
		return opts, false
	}
	opts.Format = format
	// A stray flag is MISUSE, not noise (self-documenting-cli.md requirement 3). This scan used
	// to ignore anything it did not recognise and return check's normal code, so `--no-buld`
	// ran a full build and reported success — the operator asked for one thing, the machine did
	// another, and the exit code agreed with the machine.
	if refuseUnknownFlags("check", args, checkKnownFlags, errw) {
		return opts, false
	}
	for _, a := range args {
		switch a {
		case "--no-build":
			opts.Build = false
		case "--build":
			opts.Build = true
		case "--accept-config-changes":
			opts.AcceptConfigChanges = true
		}
	}
	return opts, true
}

// detectListingRuntime resolves the runtime for the tolerant listing commands
// (ps/prune): env > config `runtime` key > platform probe. Loading the config
// is the piece `yolo ps` lacked entirely (audit finding 5). Config is loaded
// loosely (non-strict, warnings dropped) from the given workspace; any load
// error yields an empty config, so a malformed jsonc degrades to the platform
// probe rather than crashing a diagnostic command.
func detectListingRuntime(workspace string) string {
	cfgRT := ""
	if cfg, err := config.LoadConfig(workspace, false, func(string) {}); err == nil && cfg != nil {
		if v, ok := cfg.Get("runtime"); ok {
			if s, ok := v.(string); ok {
				cfgRT = s
			}
		}
	}
	return runtime.ResolveRuntime(os.Getenv("YOLO_RUNTIME"), cfgRT, paths.IsMacOS, func(bin string) bool {
		_, err := exec.LookPath(bin)
		return err == nil
	})
}

// psRunCmd runs a container-runtime probe and returns (stdout, ok). ok=false on
// a spawn error OR non-zero exit — the tri-state "could not enumerate" that
// pscmd must NOT collapse to "no jails" (else it prunes live jails' tracking
// files, audit §D11).
func psRunCmd(argv []string) (string, bool) {
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

// runRun parses the run flags (--network, --new, --profile, --timing, --dry-run)
// and the post-`--` command from args (the rewritten argv[1:]) and runs the
// container launch.
//
// The front-door RewriteArgv inserts "run" at the `--` position, so flags that
// preceded `--` end up BEFORE the "run" token (e.g. `yolo --new -- true` →
// [--new, run, --, true]). We therefore scan the WHOLE args: skip the "run"
// token wherever it appears, parse flags until `--`, and take everything after
// `--` as the command (ctx.args).
func runRun(args []string) int {
	// `--help`/`-h` is RUN'S OWN flag, and it is answered here — first, before any
	// config load and before the remainder is treated as a command to execute.
	// Both halves matter: without it `yolo run --help` launched a whole jail and
	// ran `--help` in it (exit 127), and help stayed unreachable exactly when
	// yolo-jail.jsonc would not parse. See runcmd.go for why the fix belongs here
	// rather than in wantsTopLevelHelp.
	if runHelp(args, os.Stdout) {
		return 0
	}
	opts := run.NewDefaultOptions()
	opts.Color = true
	// A MISTYPED FLAG IS MISUSE, and this is the refusal the fold itself cannot make
	// (self-documenting-cli.md requirement 3). parseRunArgs either swallows an unknown flag or
	// reads it as the command it follows — so `yolo --dry-runn -- claude` launched for real, and
	// `yolo --netwrok host -- claude` launched on the default network with nothing said.
	//
	// ⚠ IT SCANS ONLY YOLO'S OWN TOKENS, and there are TWO ways they end, not one. The separator
	// is the obvious one: everything after `--` is the WRAPPED program's argv, and refusing
	// `claude --dangerously-skip-permissions` would refuse the launch this tool exists for. The
	// second is an IMPLICIT command start — `yolo run claude --resume` has no separator at all,
	// and the first bare token begins the command, so `--resume` is the inner program's too. A
	// scan that stopped only at `--` refused that form outright (measured: exit 2 on `yolo run
	// bash -c …`, `yolo run claude --resume`), which is why the boundary comes from parseRunArgs
	// — the switch that DEFINES where the command starts — rather than being re-derived here.
	//
	// The parse itself makes no refusal, so running it first commits to nothing.
	//
	// The one refusal the fold used to make (a -p with no readable name) went with the heuristic
	// that needed it — docs/reference/providers.md OQ-PT5.
	yoloArgs := parseRunArgs(args, &opts)
	if refuseUnknownFlags("run", args[:yoloArgs], runKnownFlags(), os.Stderr) {
		return 2
	}
	// The global --verbose / -v never reaches parseRunArgs (the front door strips
	// it before subcommand resolution), so its EXPLICIT half is handed over here.
	// The env var it publishes already turns recording on through the Getenv seam;
	// this field is what additionally makes the report PRINT, which an inherited
	// YOLO_VERBOSE=1 must not do (D12, docs/reference/perf-logging.md).
	opts.Verbose = explicitVerbose()
	// Wire the macos-user native branch. run stays free of the macosuser +
	// darwinpkg deps; the front door injects the handler. packEnv is the launch's
	// composed profile/provider channel, which run.Run composes above the backend
	// dispatch and passes to whichever arm runs — forwarded verbatim.
	opts.MacosUserRun = func(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string,
		repoRoot, packRoot, homeOverlay string, hostCtx macosuser.HostContext, dryRun bool,
		packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool) int {
		return macosUserRun(cfg, workspace, agents, agentArgv, repoRoot, packRoot, homeOverlay,
			hostCtx, dryRun, packEnv, blocked)
	}
	// Wire E3's capture-on-terminate. Same injection shape and same reason: the
	// capture engine lives in THIS package, which imports run, so run cannot call it
	// directly. Warnings go to stderr — the capture is an observability aid, and its
	// failures must not pollute a command's contract-stable stdout.
	opts.CaptureOnTerminate = func(workspace, rt string) {
		captureOnTerminate(workspace, rt, func(msg string) {
			fmt.Fprintln(os.Stderr, "Warning: "+msg)
		})
	}
	// Wire OQ-PD18's auto-capture trigger: every selected pack's `via: "installer"`
	// program the machine store has never recorded is captured, in a throwaway jail,
	// before this launch's container starts. Third seam of the same shape and for the
	// same reason as the two above — captureHost launches jails, so it lives in THIS
	// package, which imports run.
	//
	// INJECTED ON THE LAUNCH PATH ONLY, which is what keeps `yolo capture`'s own jail
	// out of it: runCaptureJail builds its options from run.NewDefaultOptions and never
	// passes through here. That is belt to the pipeline's braces — the trigger also
	// reads Options.CapturesDir, which that jail suppresses (see
	// run.Options.autoCaptureInstallerPrograms).
	opts.AutoCapture = func(bins []string, platform string) {
		autoCapture(bins, platform, os.Stdout, os.Stderr, true)
	}
	// Set the tmux/kitty jail indicator around the run, restoring on exit. The
	// restore runs as subprocesses (kitten/tmux) with no timeout of their own,
	// so it is spanned — the last unmeasured step between the report printing
	// and the shell prompt returning (design H6).
	restore := SetupJailIndicator()
	if restore != nil {
		// ONCE, because BOTH arms call it now. The defer below covers a normal exit;
		// run.Options.RestoreTerminal covers the signal one, where ttyproxy
		// os.Exit(128+n)s and no defer fires — which is why Ctrl-C used to leave the
		// kitty tab wearing the jail's icon and colour permanently. Which arm runs is
		// not this function's business, so it makes the closure safe for either.
		//
		// ⚠ `inner` IS NOT A STYLE CHOICE. The closure must capture the ORIGINAL
		// function by value. Written as `func() { once.Do(restore) }` with `restore`
		// then reassigned to that same closure, the Do becomes RE-ENTRANT — and
		// sync.Once.Do blocks until the first call returns, so a second Do from
		// inside the first deadlocks forever with no error and no stack. Shipped
		// exactly that way for one commit: `exit` at a jail prompt printed the timing
		// line and then hung, and the only way out was the Ctrl-C this same change had
		// just handed to the jail.
		inner := restore
		var once sync.Once
		restoreOnce := func() { once.Do(inner) }
		restore = restoreOnce
		opts.RestoreTerminal = restoreOnce
		// The collector is built INSIDE the pipeline, and Options crosses that
		// seam by value — so spanning this on our own copy's Perf was spanning
		// nil, silently, forever (Span on a nil *Log is a no-op by design). The
		// ref is handed in so the callee can publish the collector back.
		ref := &run.PerfRef{}
		opts.PerfRef = ref
		defer func() {
			sp := ref.Log.Span("process.title_restore")
			restore()
			sp.End()
		}()
	}
	return launchRunPipeline(opts)
}

// launchRunPipeline is run.Run behind a package var, so a test can assert what `yolo run`
// WIRES INTO the pipeline without launching a container.
//
// The seam is here for captureRunPipeline's reason (capturehost.go): the thing worth
// pinning is the CALL SITE. Every injection above — MacosUserRun, CaptureOnTerminate,
// AutoCapture — is a closure that can be deleted from this function with the whole unit
// suite green and the feature silently switched off, which is the exact shape AGENTS.md
// says this repo has shipped five times. Substituting the pipeline lets a test reach the
// composed Options and INVOKE the closure it found, rather than checking it is non-nil —
// a refusal closure is non-nil too.
var launchRunPipeline = run.Run

// macosUserRun is the run.Options.MacosUserRun seam impl: it assembles the
// real macosuser deps (TTY proxy + native darwin nix materialize) and runs the
// Seatbelt-sandboxed launch. repoRoot is the yolo-jail checkout root (the nix
// build root for darwin `packages:`); the native-Go bootstrap self-execs the
// staged yolo binary and needs no source tree. packRoot is the host-side staged
// pack tree, which the run pipeline staged before dispatching here, and packEnv is the
// profile/provider channel it composed before dispatching there too. macos-hardware-gated;
// on Linux macosuser fails closed at its IsMacOS precondition (dry-run works anywhere).
func macosUserRun(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string,
	repoRoot, packRoot, homeOverlay string, hostCtx macosuser.HostContext, dryRun bool,
	packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool) int {
	runProxy := run.RunWithProxy
	materialize := func(nixRoot string, packages []any) (*macosuser.Darwin, bool, error) {
		// system "" → darwinpkg.NativeSystem(), the running platform. NOT a
		// hardcoded aarch64-darwin: this backend is macOS-only but Macs are not
		// all Apple Silicon (BACKLOG E8's bug class).
		system := darwinpkg.NativeSystem()
		pkgs, err := darwinpkg.Materialize(nixRoot, packages, system, os.Stderr)
		if err != nil {
			return nil, false, err
		}
		env := jsonx.NewOrderedMap()
		// darwinpkg env is a small map (at most PKG_CONFIG_PATH); sort for a
		// deterministic OrderedMap ordering.
		keys := make([]string, 0, len(pkgs.Env))
		for k := range pkgs.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			env.Set(k, pkgs.Env[k])
		}
		return &macosuser.Darwin{
			PathPrefix:  pkgs.PathPrefix,
			Env:         env,
			Skipped:     pkgs.Skipped,
			System:      system,
			ProfilePath: pkgs.ProfilePath,
		}, true, nil
	}
	// Mirror run's `Color && IsTTYStdout()`: color is requested for the
	// interactive front door, gated on a real TTY. The dry-run plan render
	// forces color OFF internally (byte-pinned goldens), so this only affects
	// the live setup/teardown chatter.
	return macosuser.RunMacosUser(macosLaunchDeps(runProxy, materialize, isTTYStdout()),
		macosuser.Options{
			Workspace:       workspace,
			Config:          cfg,
			Agents:          agents,
			AgentArgv:       agentArgv,
			RepoRoot:        repoRoot,
			HostPackRoot:    packRoot,
			HostHomeOverlay: homeOverlay,
			HostCtx:         hostCtx,
			BlockedTools:    blocked,
			PackEnv:         packEnv,
			DryRun:          dryRun,
		})
}

// workspaceLockSeam is the macos-user backend's per-workspace launch lock, wired only on
// the LAUNCH path — the four `yolo macos-*` commands build their Deps without it, because
// none of them provisions anything and a setup command blocking on a running jail's launch
// would be a new way to look hung.
//
// The backend holds it across its three privileged steps and releases it before the agent
// (macosuser.RunMacosUser). It is wired HERE rather than inside that package because the
// implementation lives in internal/cli/run, which imports macosuser — see
// run.AcquireWorkspaceLockFor for why the call moves and the implementation does not.
//
// A NAMED FUNCTION rather than a closure at the assignment, for launchRunPipeline's reason
// (see that var): the thing worth pinning is the wiring, and a test can only reach a
// closure written inline by running the whole launch. TestWorkspaceLockSeamReallyLocks
// invokes this one and reads the lock file it takes.
func workspaceLockSeam(ws, cname string) func() {
	return run.AcquireWorkspaceLockFor(ws, cname,
		func(msg string) { fmt.Fprintln(os.Stderr, "Warning: "+msg) },
		func(msg string) { fmt.Fprintln(os.Stdout, msg) })
}

// macosLaunchDeps assembles the macos-user backend's Deps for a LAUNCH: RealDeps plus the
// per-workspace lock the four `yolo macos-*` commands deliberately do not get.
//
// IT IS A FUNCTION SO THE WIRING CAN FAIL A TEST. Assigned inline at the call site, the
// lock seam could be deleted with the whole suite green — the backend degrades to an
// unlocked launch, which looks identical until two terminals provision one workspace at
// once. That is the exact shape AGENTS.md says this repo has shipped five times, and it
// was measured here: deleting the assignment broke nothing.
//
// The non-nil check TestMacosLaunchDepsWiresTheWorkspaceLock makes is weak on its own and
// is not on its own: the only value it can hold is workspaceLockSeam, and
// TestWorkspaceLockSeamReallyLocks proves THAT takes a real flock. The pair is what makes
// "wired" mean something.
func macosLaunchDeps(runProxy func(argv []string) int,
	materialize func(repoRoot string, packages []any) (*macosuser.Darwin, bool, error),
	color bool) macosuser.Deps {
	deps := macosuser.RealDeps(runProxy, materialize, color)
	deps.LockWorkspace = workspaceLockSeam
	return deps
}

const checkUsage = `Usage: yolo check [flags]
       yolo doctor [flags]      (doctor is an alias for check — same body, same flags)

Validate this machine and this workspace: the container runtime, nix, the user and
workspace config, the jail image, the configured packs, the loopholes, and any
running jails. One section per area; exit is non-zero if any section FAILs.

Flags:
  --build                  Build the jail image if it is missing or stale (the default).
  --no-build               Skip the image build entirely. This is the fast preflight to run
                           after editing yolo-jail.jsonc, and the one to use inside a jail.
  --accept-config-changes  Pre-approve workspace config changes and record the host snapshot.
` + outputFormatUsage + `
  --help, -h               Show this help. Answered before any section runs, so asking what
                           check does never triggers a nix build.

The JSON document carries every GRADED finding — its section, its pass/warn/fail
status, its message and its remediation note — plus the three counts. The dim
informational lines and the section prose are not in it: they carry no grade to
act on, and they stay in the text form, which is where they were written for a
reader. ` + "`--format json`" + ` changes the rendering only: every section still runs,
including the image build.

Examples:
  yolo check --no-build               # the fast preflight, and the one to use in a jail
  yolo check --no-build --format json # the same findings, for a script or an agent
  yolo doctor                         # the same command under its alias

` + "`yolo config-ref`" + ` is the schema the config sections validate against.`

// runCheck parses the check/doctor flags (--build/--no-build) from args and runs
// the native Go check. args is the rewritten argv[1:] (subcommand included), so
// the leading token is "check"/"doctor". Exit code: 0 = no failures, 1 = fail.
func runCheck(args []string) int {
	// Before the sections, one of which is a nix image build: `yolo check --help`
	// used to run the whole check (the unknown flag was silently ignored), which is
	// minutes of work in answer to a question about the command. `doctor` shares
	// this body and registers the same text under its own key. See subhelp.go.
	if answerHelp("check", args, os.Stdout) {
		return 0
	}
	opts, ok := checkOptions(args, os.Stderr)
	if !ok {
		return 2
	}
	return check.Check(opts)
}

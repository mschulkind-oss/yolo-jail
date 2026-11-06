package cli

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/outfmt"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/perf"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// yoloAccountUsage is what `yolo yolo-account --help` prints. "The yolo account" is
// `_yolojail`, the macOS account the `seatbelt-yolo-account` setup runs agents as
// (docs/design/named-setups.md, OQ-MT4); each verb answers its own `--help` too
// (yoloAccountVerbUsage).
const yoloAccountUsage = `Usage: yolo yolo-account <create|remove|share|unshare> [path]

Manage the yolo account: ` + "`_yolojail`" + `, the macOS account agents run as when they
should not run as you. It holds none of your files. The four verbs come in two
inverse pairs:

  create            Create the yolo account and its sudo rule (asks for your admin password once;
                    --recheck re-checks the rule with it on a Mac already set up)
  remove            Remove the yolo account, its group and its rule: the undo of create
  share [path]      Give the yolo account an existing directory under the shared root
  unshare <path>    Take a directory back out of the yolo account's reach: the undo of share

Each verb answers ` + "`--help`" + ` on its own (` + "`yolo yolo-account create --help`" + `).
macOS only for now: on any other system each verb refuses.

  --help, -h  Show this help. Answered before anything is created or changed.

Examples:
  yolo yolo-account create                 # once per Mac
  yolo yolo-account share ~/code/myproject # a project moved in after create`

const yoloAccountCreateUsage = `Usage: yolo yolo-account create

Create the yolo account, ` + "`_yolojail`" + `, that agents run as when they should not run as
you: the account (random password, no login shell) if it is missing, the shared
group, and the shared root. Idempotent: re-running it reuses an existing account.

It also creates the staging directory /Users/Shared/yolo-staged, owned by you,
and installs one sudo rule, /etc/sudoers.d/yolo-jail, which lets you run
commands as ` + "`_yolojail`" + `, never as root, without a password. Launches then start
and stop with no password prompt. One macOS user per Mac: create refuses a
second while another user's rule or staging directory is in place.

Run it as your normal admin user, not with sudo. It asks for your admin password
once. On a Mac already set up it asks for none: it checks, with no password, that
sudo runs commands as ` + "`_yolojail`" + ` and that nothing else needs root, and exits 0.
macOS only: on any other system it refuses.

  --recheck   Run every step that needs your admin password even when the Mac is
              already set up, re-checking and rewriting the sudo rule.
  --help, -h  Show this help. Answered before anything is created.

Examples:
  yolo yolo-account create            # create the yolo account and the rule (idempotent)
  yolo yolo-account create --recheck  # re-check the rule with your admin password

` + "`yolo yolo-account remove`" + ` removes the account again.`

const yoloAccountRemoveUsage = `Usage: yolo yolo-account remove

Remove the yolo account, ` + "`_yolojail`" + `, and its group: the undo of
` + "`yolo yolo-account create`" + `. It also removes the sudo rule /etc/sudoers.d/yolo-jail
and, when it is yours, the staging directory /Users/Shared/yolo-staged, which
frees the Mac for another user's ` + "`yolo yolo-account create`" + `. /var/yolo-jail, left by
an older yolo, stays; ` + "`yolo stores`" + ` lists it. Does nothing (exit 0) when the
account does not exist.

sudo is invoked for the deletion, so macOS may prompt for your admin password.
macOS only: on any other system it refuses.

  --help, -h  Show this help. Answered before anything is deleted.

Examples:
  yolo yolo-account remove            # remove the account, its group, the rule and the staging dir`

const yoloAccountUnshareUsage = `Usage: yolo yolo-account unshare <path>

Strip yolo-jail's ACLs from <path>, so the yolo account can no longer reach
it. The path is resolved (absolute + symlinks) first and must be a directory.
This is how you take a workspace back out of the yolo account's reach without
removing the account.

macOS only: on any other system it refuses.

  --help, -h  Show this help. Answered before any ACL is touched.

Examples:
  yolo yolo-account unshare ~/code/myproject   # take that workspace out of the yolo account's reach

The inverse, putting the ACLs back on files that predate sharing, is
` + "`yolo yolo-account share`" + `.`

const yoloAccountShareUsage = `Usage: yolo yolo-account share [path]

Give the yolo account the shared-group ACL on files that already existed in the
shared area before it was shared. With no [path] it walks the whole shared root;
with one it walks that directory instead.

It refuses a path inside a user home: yolo only manages ACLs on neutral ground.
macOS only: on any other system it refuses.

  --help, -h  Show this help. Answered before any ACL is applied.

Examples:
  yolo yolo-account share             # retrofit the ACL across the whole shared root
  yolo yolo-account share /Users/Shared/yolo-jail/myproject   # just this one`

// yoloAccountVerbUsage is each verb's own help, answered by `yolo yolo-account <verb>
// --help`: these are sudo-invoking, ACL-rewriting acts, so "what does this do?" must be
// answerable without running them. It lives beside subcommandUsage rather than in it
// because that table is keyed by registry name (TestSubcommandUsageHasNoStaleEntries).
var yoloAccountVerbUsage = map[string]string{
	"create":  yoloAccountCreateUsage,
	"remove":  yoloAccountRemoveUsage,
	"share":   yoloAccountShareUsage,
	"unshare": yoloAccountUnshareUsage,
}

// yoloAccountActs is what each verb does, split out so a test can stand in for the
// real acts (which ask for sudo and change the Mac). path is the verb's one positional,
// "" when none was given.
type yoloAccountActs struct {
	create         func(recheck bool) int
	remove         func() int
	share, unshare func(path string) int
}

// runYoloAccount is `yolo yolo-account`, the yolo account's verbs (OQ-MT8 of
// docs/design/named-setups.md). The verbs' behavior is the macos-* commands' they replace.
func runYoloAccount(args []string) int {
	deps := func() macosuser.Deps { return macosuser.RealDeps(nil, nil, colorForWriter(os.Stdout)) }
	return yoloAccountMain(args, yoloAccountActs{
		create: func(recheck bool) int {
			return macosuser.AccountCreateWith(deps(), macosuser.CreateOptions{Recheck: recheck})
		},
		remove:  func() int { return macosuser.AccountRemove(deps()) },
		share:   func(p string) int { return macosuser.AccountShare(deps(), p) },
		unshare: func(p string) int { return macosuser.AccountUnshare(deps(), p) },
	}, os.Stdout, os.Stderr)
}

func yoloAccountMain(args []string, acts yoloAccountActs, out, errw io.Writer) int {
	rest := args
	if len(rest) > 0 && rest[0] == "yolo-account" {
		rest = rest[1:]
	}
	if len(rest) > 0 {
		if text, ok := yoloAccountVerbUsage[rest[0]]; ok && helpRequested(rest[1:]) {
			fmt.Fprintln(out, text)
			return 0
		}
	}
	if helpRequested(rest) {
		fmt.Fprintln(out, yoloAccountUsage)
		return 0
	}
	if len(rest) == 0 {
		fmt.Fprintf(errw, "yolo yolo-account: name a verb: create, remove, share or unshare.\n\n%s\n", yoloAccountUsage)
		return 2
	}
	verb, pos := rest[0], rest[1:]
	// create's one flag, taken out before the path checks so `create --recheck` is not read as
	// a path.
	recheck := false
	if verb == "create" {
		if i := slices.Index(pos, "--recheck"); i >= 0 {
			recheck = true
			pos = slices.Delete(slices.Clone(pos), i, i+1)
		}
	}
	path := ""
	if len(pos) > 0 {
		path = pos[0]
	}
	if len(pos) > 1 {
		fmt.Fprintf(errw, "yolo yolo-account %s: takes at most one path, got %d (%s); "+
			"`yolo yolo-account %s --help` says what it takes.\n", verb, len(pos), strings.Join(pos, " "), verb)
		return 2
	}
	switch verb {
	case "create":
		if path != "" {
			fmt.Fprintf(errw, "yolo yolo-account create: takes no path, got %q. Run it bare:\n  yolo yolo-account create\n", path)
			return 2
		}
		return acts.create(recheck)
	case "remove":
		if path != "" {
			fmt.Fprintf(errw, "yolo yolo-account remove: takes no path, got %q. Run it bare:\n  yolo yolo-account remove\n", path)
			return 2
		}
		return acts.remove()
	case "share":
		return acts.share(path)
	case "unshare":
		return acts.unshare(path)
	default:
		fmt.Fprintf(errw, "yolo yolo-account: unknown verb %q; the verbs are create, remove, share "+
			"and unshare (`yolo yolo-account --help` says what each does).\n", verb)
		return 2
	}
}

// renamedVerbs maps each removed top-level verb to the `yolo-account` verb that replaced
// it. They are refused, never aliased (MT-R3 and §10 of docs/design/named-setups.md), and
// they are not registry keys, so `yolo --help` does not list them; the front door answers
// one with refuseRenamedVerb instead of "unknown command".
var renamedVerbs = map[string]string{
	"macos-setup":           "create",
	"macos-teardown":        "remove",
	"macos-fix-permissions": "share",
	"macos-unshare":         "unshare",
}

// refuseRenamedVerb answers a removed verb with the one that replaced it, and reports
// whether it did. The arguments typed after the old verb are carried into the command it
// names, so `yolo macos-fix-permissions <path>` is told the exact command to run.
func refuseRenamedVerb(args []string, errw io.Writer) bool {
	name := firstPositional(args)
	verb, ok := renamedVerbs[name]
	if !ok {
		return false
	}
	next := []string{"yolo", "yolo-account", verb}
	if i := slices.Index(args, name); i >= 0 {
		next = append(next, args[i+1:]...)
	}
	fmt.Fprintf(errw, "yolo %s is now yolo yolo-account %s. Run:\n  %s\n", name, verb, shquote.JoinDisplay(next))
	return true
}

// pruneUsage is what `yolo prune --help` prints. The flag list is exactly the
// flags runPrune parses (TestUsageListsEveryParsedFlag pins that), and the
// defaults are prune.NewDefaultOptions'.
const pruneUsage = `Usage: yolo prune [flags]

Reclaim disk that yolo is holding: stale containers, old jail images, the image
tarball cache, nix build/image GC roots, shadowed jail homes, heavy tool caches,
the shared mise store's tool versions no jail on this machine has used for 30
days (host-only, judged from what every jail records it uses),
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
  --no-image-roots         Skip every nix GC-root section: image, prefix and
                           package-profile roots, yolo's own store outputs, and the
                           build out-links (dangling, retired, image-copier).
  --no-shadowed-home       Skip the shadowed jail-home sweep.
  --no-embedded-packs      Skip the built-in pack copy sweep (embedded-packs/
                           and TMPDIR).
  --cache-age <days>       Only consider caches older than <days> (default 30;
                           0 skips the pass entirely).
  --purge-heavy-caches     Also purge the heavy tool caches (npm, go, cargo, …).
  --no-hardlink            Do not hardlink-dedup identical cache files.
  --dedup-global           Dedup across every workspace, not just this one.
  --nix-gc                 Run the bounded host nix store GC. Opt-in, host-only,
                           and gated on every running jail's image having a
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
	// Host session-home reclamation must run the same owned-host capture before deletion. The
	// callback returns any refusal through prune's per-entry result, which keeps the source.
	opts.CaptureHostSessionHome = func(path string) (string, string, error) {
		return captureHostSessionHomePath(path, io.Discard)
	}
	// The macos-user guest-pack-tree sweep, which prune cannot import (macosuser pulls
	// internal/config). On a machine with no staging root it reports nothing.
	opts.GuestPackTrees = guestPackTreeSweep
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
	opts.DetectRuntime = listingDetect("prune", ws, os.Stderr)
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
	opts.DetectRuntime = listingDetect("stores", ws, os.Stderr)
	return opts
}

const hostDaemonUsage = `Usage: yolo host-daemon <subcommand> [<name>]

See and stop this machine's HOST DAEMONS: the processes a loophole runs on your
machine, outside the jail, for a jail to reach — the Claude OAuth broker among
them. A loophole declares one with ` + "`host_daemon`" + ` in its manifest.

EACH JAIL RUNS ITS OWN. A launch starts the host daemons its loopholes declare
and stops them when the jail ends, so a name has one running copy per jail that
uses it, and none while no jail does. Copies that share a login share its file
and the refresh lock beside it, which is what keeps two jails from spending the
same refresh token. This verb sees what the jails are running; it starts nothing.

Subcommands:
  status [<name>]     Every running copy of the daemon, by jail: its pid, its
                      socket and whether the socket is ACCEPTING. With no name:
                      every host daemon. Exit 1 if any copy is not accepting.
  stop <name>         Stop every running copy. Each jail it served fails those
                      requests until it is launched again. A request in flight
                      gets up to 35 seconds to finish first.
  stop --host-wide <name>
                      Stop only the host-wide copy an older yolo left, and no
                      jail's own: the upgrade's last step, after relaunching
                      the jails that copy served.
  restart <name>      Restarts nothing, and says so: a copy restarts with its
                      jail, so leave that jail and launch it again.
  logs <name> [flags] Print the log every copy appends to.

A host-wide copy an older yolo started, which served every jail on the machine,
is reported as host-wide and stopped by ` + "`stop --host-wide`" + ` (or by ` + "`stop`" + `,
with every jail's copy); no launch of this yolo starts or uses one.

THE NAME IS REQUIRED BY EVERY VERB THAT ACTS. Only ` + "`status`" + ` defaults to every
daemon, because it reports rather than acts; ` + "`stop`" + `, ` + "`restart`" + ` and ` + "`logs`" + `
refuse and list the names instead of picking one for you.

logs flags:
  -n, --lines <n>     Show the last <n> lines (default 50; also -n<n> and
                      --lines=<n>).
  -f, --follow        Follow the log as it grows.

status flags:
` + outputFormatUsage + `

  --help, -h          Show this help.

` + "`status --format json`" + ` is an array with one object per running copy: its loophole
name, its jail (absent for an older yolo's host-wide copy), ` + "`host_wide`" + `, the socket,
the pids and whether the socket is ACCEPTING (an accept probe, not a protocol
round trip) — the verdict the exit code uses. Nothing running is an empty array.

Examples:
  yolo host-daemon status                     # every running copy
  yolo host-daemon status --format json       # the same, for a script or an agent
  yolo host-daemon stop aws-auth              # stop every jail's copy
  yolo host-daemon stop --host-wide aws-auth  # stop only an older yolo's copy
  yolo host-daemon logs openai-auth-broker -n 100

` + "`yolo broker <subcommand>`" + ` is the retained alias for the Claude OAuth broker, and
is exactly ` + "`yolo host-daemon <subcommand> claude-oauth-broker`" + `.
` + "`yolo loopholes list`" + ` shows which loopholes this machine has and whether they are
wired into a jail.`

const brokerUsage = `Usage: yolo broker <subcommand>

See and stop the Claude OAuth broker: the host daemon a jail's Claude reaches
to refresh its login. Each jail runs its own copy, started by its launch and
stopped when it ends; copies that share an account share its credentials file
and the refresh lock beside it, so they cannot race and spend the single-use
refresh token twice. It runs on your machine, outside every jail.

THIS IS AN ALIAS. ` + "`yolo broker <subcommand>`" + ` means
` + "`yolo host-daemon <subcommand> claude-oauth-broker`" + `; see
` + "`yolo host-daemon --help`" + ` for the other host daemons and for the surface this
one is a shortcut into. It is retained because it is in muscle memory and in
the docs, and it always names the Claude broker.

Subcommands:
  status              Every jail's running copy: its pid, its socket and whether
                      the socket is accepting.
  stop                Stop every copy (each jail then needs a relaunch).
  stop --host-wide    Stop only the host-wide copy an older yolo left.
  restart             Restarts nothing, and says so: a copy restarts with its
                      jail, so leave that jail and launch it again.
  logs [flags]        Print the broker's log.

logs flags:
  -n, --lines <n>     Show the last <n> lines (default 50; also -n<n> and
                      --lines=<n>).
  -f, --follow        Follow the log as it grows.

status flags:
` + outputFormatUsage + `

  --help, -h          Show this help.

` + "`status --format json`" + ` is an array with one object per running copy: its jail, the
socket, the pids and whether the socket is ACCEPTING (an accept probe, not a
protocol round trip) — the verdict the exit code uses.

Examples:
  yolo broker status                  # which jails run a broker, and is each accepting?
  yolo broker status --format json    # the same, for a script or an agent
  yolo broker logs -n 100             # the last 100 log lines

The broker is a loophole: ` + "`yolo loopholes list`" + ` shows whether it is wired into
this jail, and ` + "`yolo config-ref`" + ` documents the ` + "`loopholes`" + ` key that enables it.`

// runHostDaemon dispatches `yolo host-daemon {status,stop,restart,logs} [<name>]`,
// the management verb over WHAT THE JAILS ARE RUNNING (docs/design/host-daemon-ownership.md
// §5.2, HD-R1).
//
// The set is derived per invocation from the sockets the jails' daemons bind, plus any
// host-wide copy an older yolo left (broker.RunningInstances) — never from a list here,
// which is precisely how `yolo broker` stayed at one daemon while three came to exist
// (OQ-HD2).
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

// runBroker dispatches `yolo broker {status,stop,restart,logs}` — the ALIAS, naming the
// Claude broker and nothing else. args is the rewritten argv[1:] (args[0]=="broker").
//
// The name is always known (broker.KnownHostDaemon), so the alias keeps working exactly
// where discovery is empty: in a jail, on a host whose `packs` list does not name claude,
// in any process that staged nothing.
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
// which `status` reads as "every daemon" and the three acting verbs refuse.
func hostDaemonDispatch(verb, sub, name string, rest []string, format string) int {
	switch sub {
	case "status":
	case "stop", "restart", "logs":
		// REFUSED, not ignored, on the three verbs that ACT. `status` is the only
		// one that reports state; the others stop, refuse or tail, and answering a
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
			// first" or "the broker" is how a generic verb comes to stop a daemon
			// nobody named. Exit 2 (misuse), with the names for the retype.
			fmt.Fprintf(os.Stderr, "yolo %s %s: name the host daemon to act on.\n  %s\n",
				verb, sub, hostDaemonNames())
			return 2
		}
	default:
		fmt.Fprintf(os.Stderr, "Usage: yolo %s {status|stop|restart|logs}\n", verb)
		return 1
	}
	if name != "" && !broker.KnownHostDaemon(name) {
		fmt.Fprintf(os.Stderr, "yolo %s: no host daemon called %q is declared or running on "+
			"this machine.\n  %s\n", verb, name, hostDaemonNames())
		return 2
	}
	deps := broker.CLIRealDeps()
	deps.IsTTYStdout = isTTYStdout
	deps.Format = format
	switch sub {
	case "status":
		return broker.PrintStatus(deps, name)
	case "stop":
		if slices.Contains(rest, broker.HostWideFlag) {
			return broker.StopHostWide(deps, name)
		}
		return broker.Stop(deps, name)
	case "restart":
		return broker.Restart(deps, name)
	}
	lines, follow := hostDaemonLogsFlags(rest)
	return broker.Logs(deps, name, lines, follow)
}

// hostDaemonNames renders the names a verb could act on, for a refusal, so a user who
// omitted or mistyped one learns the choices on THIS machine rather than being sent to a doc:
// every host daemon a loophole here declares, and any other one running.
func hostDaemonNames() string {
	seen := map[string]bool{}
	var names []string
	for _, n := range broker.DeclaredHostDaemons() {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, i := range broker.RunningInstances("") {
		if !seen[i.Name] {
			seen[i.Name] = true
			names = append(names, i.Name)
		}
	}
	if len(names) == 0 {
		return "This machine declares no host daemon and is running none; `yolo loopholes " +
			"list` shows its loopholes, and " + broker.BrokerLoopholeName + " is always a valid name."
	}
	return "Known: " + strings.Join(names, " ")
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

// hostDaemonLogsFlags parses logs' own -n/--lines (default 50) and -f/--follow.
func hostDaemonLogsFlags(rest []string) (lines int, follow bool) {
	lines = 50
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
	return lines, follow
}

const initUsage = `Usage: yolo init [--mount <path>]...

Scaffold this workspace: write a three-line pointer file, yolo-jail.toml, add
.yolo/ to .git/info/exclude (your .gitignore is left as it is), and print the
agent briefing. A project needs no file to run, so no settings are written. An
existing yolo-jail.toml, yolo-jail.jsonc or yolo-jail.json is never overwritten
or shadowed — re-running init on a configured workspace just reprints the briefing.

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
	return Init(cwd, mounts, os.Stdout, colorForWriter(os.Stdout))
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
	return InitUserConfig(os.Stdout, colorForWriter(os.Stdout))
}

// isTTYStdout reports whether os.Stdout is a real terminal. It is the IsTTYStdout
// seam handed to the engines that take one (run, stores, the host daemons), which
// use it both for interaction (the run's -t flag, its prompts) and as the
// terminal input to their own tty.Color gate. It is NOT a color decision by
// itself: an entry point that needs one asks colorForWriter(os.Stdout).
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
  list              Every installed loophole, its source (pack-shipped or
                    config-inline), whether it is active here, and, for one that
                    declares grants, each grant kind's standing in this workspace:
                    granted, pending (requested, not yet approved), requestable
                    (your config allows a request; none made), or not_allowed
                    (candidate in a jail: held once the host's gate is answered y).
                    This is the default: a bare ` + "`yolo loopholes`" + ` lists.
  status            Run each loophole's own self-check, with the same standings.
                    HOST-SIDE: inside a jail it says so and does nothing, because
                    the things being checked are host daemons.
  enable <name>     RETIRED. A loophole runs whenever its pack is selected, and a
  disable <name>    project gets access by a grant: ` + "`yolo request <pack> <kind>`" + `
                    for one project, ` + "`yolo allow <pack> <kind>`" + ` for every project.
                    Both refuse, naming those two for <name>. ` + "`disable`" + ` first
                    removes a switch an older yolo wrote for the workspace
                    (--workspace <path>, default this folder) and exits 0 when it did.
                    --global, the old every-workspace form, is still accepted and
                    removes nothing.

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
  yolo request journal read             # this project asks to read your journal

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
	deps.Color = colorForWriter(os.Stdout)
	switch sub {
	case "", "list":
		return loopholes.List(deps)
	case "status":
		return loopholes.Status(deps)
	case "enable", "disable":
		// REFUSED, not ignored: the retired verbs report no state to serialize, and
		// accepting `--format json` here would answer a request for machine-readable
		// output with prose — the exact failure the flag exists to prevent
		// (outputformat.go).
		if outfmt.IsJSON(format) {
			fmt.Fprintf(os.Stderr, "yolo loopholes %s: --format json is not available "+
				"here — this verb is retired and reports no "+
				"state. `yolo loopholes list --format json` and `… status --format json` do.\n", sub)
			return 2
		}
		name, opts, problem := parseSetEnabledArgs(rest)
		if problem != "" {
			fmt.Fprintf(os.Stderr, "yolo loopholes %s: %s\nUsage: yolo loopholes %s <name> "+
				"[--workspace <path>] [--global]\n", sub, problem, sub)
			return 2
		}
		return loopholes.CmdSetEnabled(deps, name, sub == "enable", opts)
	default:
		fmt.Fprintf(os.Stderr, "Usage: yolo loopholes {list|status|enable|disable} [name]\n")
		return 1
	}
}

// parseSetEnabledArgs reads the retired `yolo loopholes enable|disable`'s argv after the verb:
// the loophole's name, `--workspace <path>` (or `--workspace=<path>`) and `--global`, still
// parsed so an old script gets the refusal rather than a usage error. The workspace is the one
// whose old switch `disable` removes; it defaults to the working directory, which is the
// workspace a launch made here uses (run.Run's own default), and is returned absolute with its
// symlinks resolved. It is NOT `yolo config`'s walk up to the nearest marked folder: the
// per-workspace file matches its exact folder, so a `disable` here would otherwise miss the
// switch a launch here reads. A leading `~/` is expanded, since
// bash leaves it in `--workspace=~/x`. problem is the usage error, "" when there is none.
func parseSetEnabledArgs(args []string) (name string, opts loopholes.SetEnabledOptions, problem string) {
	workspace := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--global":
			opts.Global = true
		case a == "--workspace":
			if i+1 >= len(args) || args[i+1] == "" {
				return "", opts, "a path must follow --workspace"
			}
			i++
			workspace = args[i]
		case strings.HasPrefix(a, "--workspace="):
			workspace = strings.TrimPrefix(a, "--workspace=")
			if workspace == "" {
				return "", opts, "a path must follow --workspace"
			}
		case a == "--format" || a == "--json" || strings.HasPrefix(a, "--format="):
			// Parsed, and refused for these verbs, by the caller.
			if a == "--format" {
				i++
			}
		case strings.HasPrefix(a, "-"):
			return "", opts, fmt.Sprintf("unknown flag %q", a)
		case name == "":
			name = a
		default:
			return "", opts, fmt.Sprintf("unexpected argument %q: it takes one loophole name", a)
		}
	}
	if name == "" {
		return "", opts, "name the loophole (`yolo loopholes list` shows them)"
	}
	if opts.Global && workspace != "" {
		return "", opts, "pass --global or --workspace, not both: they name every workspace, or one"
	}
	switch {
	case workspace == "":
		if wd, err := os.Getwd(); err == nil {
			workspace = wd
		}
	case workspace == "~":
		workspace = paths.Home()
	case strings.HasPrefix(workspace, "~/"):
		workspace = filepath.Join(paths.Home(), workspace[2:])
	}
	if abs, err := filepath.Abs(workspace); err == nil {
		workspace = abs
	}
	if resolved, err := filepath.EvalSymlinks(workspace); err == nil {
		workspace = resolved
	}
	opts.Workspace = workspace
	return name, opts, ""
}

const psUsage = `Usage: yolo ps [--format json]

List the running yolo-* jails and the workspace each one is attached to, so you
can tell whether ` + "`yolo`" + ` in this directory would ATTACH to a live jail or start a
fresh one.

The container runtime is the backend of the setup a launch would select (YOLO_SETUP,
then the ` + "`setup`" + ` config key, then the OS default), so on a Mac running Apple
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
	detect := listingDetect("ps", ws, os.Stderr)
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
	// The pack-switch nudge's "Add it to packs?" runs `yolo pack add` in-process (check/packnudge.go).
	opts.PackAdd = packAddInProcess
	// The host agent floor the launch itself builds, so the floor rows `yolo check` prints are
	// read from the same construction `yolo host --` installs through (host-agent-environment.md
	// §7). Observe only: the section asks dispositions, never installs.
	opts.HostFloor = func(progs []hostfloor.Program) *hostfloor.Floor { return newHostFloor(io.Discard, progs) }
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

// detectListingRuntime resolves the backend for the tolerant listing commands (ps/prune):
// the backend of the setup a launch from workspace would select (workspaceSetupBackend), so
// a macOS workspace whose setup is seatbelt-self lists the macos-user sessions its launches
// run. Loading the config is the piece `yolo ps` lacked entirely (audit finding 5).
func detectListingRuntime(workspace string) string {
	return workspaceSetupBackend(workspace)
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

// runRun parses the run flags and the post-`--` command from args (the rewritten
// argv[1:]) with parseRunArgs, and runs the container launch.
//
// The front-door RewriteArgv puts "run" FIRST (`yolo --timing -- true` →
// [run, --timing, --, true]), the shape an explicit `yolo run …` has; parseRunArgs
// still skips the first "run" wherever it sits, since an explicit subcommand may
// follow global-looking flags (`yolo -p zai run -- claude`).
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
	// The argv as TYPED, not args (the front door's rewrite), for the launch log's header: the
	// record of how this launch was asked for (run's launchcontext.go).
	opts.Invocation = append([]string{}, os.Args[1:]...)
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
	parsed := parseRunArgs(args, &opts)
	// A value flag given no value, in the words and with the exit code `yolo host` uses for
	// the same typo (readValueFlag, notch-convergence.md row A2).
	if parsed.misuse != nil {
		fmt.Fprintf(os.Stderr, "yolo run: %v\n", parsed.misuse)
		return 2
	}
	if refuseUnknownFlags("run", args[:parsed.boundary], runKnownFlags(), os.Stderr) {
		return 2
	}
	// The global --verbose / -v never reaches parseRunArgs (the front door strips
	// it before subcommand resolution), so its EXPLICIT half is handed over here.
	// The env var it publishes already turns recording on through the Getenv seam;
	// this field is what additionally makes the report PRINT, which an inherited
	// YOLO_VERBOSE=1 must not do (D12, docs/reference/perf-logging.md).
	opts.Verbose = explicitVerbose()
	// The collector is built INSIDE the pipeline, and Options crosses that seam by value, so the
	// front door is handed it through this ref (run.PerfRef): read by the title restore below,
	// after Run returns, and by the macos-user handler while Run runs. Unconditional, since the
	// handler reads it whether or not a terminal indicator was set.
	ref := &run.PerfRef{}
	opts.PerfRef = ref
	// Wire the macos-user native branch. run stays free of the macosuser +
	// darwinpkg deps; the front door injects the handler. packEnv is the launch's
	// composed profile/provider channel, which run.Run composes above the backend
	// dispatch and passes to whichever arm runs — forwarded verbatim. The launch's signal
	// arm is made here and installed by Run (run.MacosUserArm), so the handler can hand the
	// backend its session runner, the answer to "is a signal ending this launch?" and the
	// session-start hook; and the collector, so the backend's steps are spanned on it.
	arm := run.NewMacosUserArm()
	opts.MacosUserArm = arm
	// THE macos-user MODE is the setup's run_as (named-setups.md; plan slice 4a), resolved ONCE by
	// the launch's setup gate and handed to both readers: the pipeline (its package-cache alias and
	// siting, run.Options.MacosUserMode, which the gate sets) and the backend (the whole launch,
	// macosuser.Options.Mode, read here off the SetupRef the gate fills), so the two cannot
	// disagree about who the sandbox runs as.
	setupRef := &run.SetupRef{}
	opts.SetupRef = setupRef
	hostHome, hostUser := macosUserHostIdentity()
	opts.MacosUserHostHome = hostHome
	// The shared-/tmp hatch (OQ-MM8, MMP-D18), read once like the mode and handed to both readers:
	// the briefing (run.Options) and the profile (macosuser.Options).
	sharedTmp := config.MacosUserSharedTmp()
	opts.MacosUserSharedTmp = sharedTmp
	opts.MacosUserRun = func(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string,
		repoRoot, packRoot string, homeOverlay macosuser.HomeOverlay, hostCtx macosuser.HostContext, dryRun bool,
		packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool, jailDaemons macosuser.JailDaemons) int {
		return macosUserRun(cfg, workspace, agents, agentArgv, repoRoot, packRoot, homeOverlay,
			hostCtx, dryRun, packEnv, blocked, jailDaemons, arm, ref.Log,
			macosUserIdentity{Mode: macosuser.ModeFor(setupRef.Setup.RunAs), Setup: setupRef.Setup, SetupSource: setupRef.Source, HostHome: hostHome, HostUser: hostUser, SharedTmp: sharedTmp,
				ShareHostFonts: config.ShareHostFonts()})
	}
	// Wire the fetched model list (docs/design/model-lists-and-pickers.md OQ-MM6): a provider no
	// pack or config gives a list gets the region's from its platform's credential service. A
	// seam the front door fills so that no hand-built Options in a test can reach AWS.
	opts.FetchModelList = run.DefaultFetchModelList
	// And the host keychain read a macos-user launch hands a program its host login with
	// (docs/design/keychain-from-a-jail.md OQ-KC5), for the same reason: no hand-built Options
	// in a test may reach a real keychain.
	opts.HostKeychainRead = run.DefaultHostKeychainRead
	opts.WarnUnknownYOLOEnv = warnUnknownYOLOEnvFromProcess
	// Wire E3's capture-on-terminate. Same injection shape and same reason: the
	// capture engine lives in THIS package, which imports run, so run cannot call it
	// directly. Warnings go to stderr — the capture is an observability aid, and its
	// failures must not pollute a command's contract-stable stdout.
	opts.CaptureOnTerminate = func(workspace, home, rt string) {
		captureOnTerminateIn(workspace, home, rt, func(msg string) {
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
	opts.PreparePrograms = func(req run.ProgramCaptureRequest) (map[string]entrypoint.ProgramCaptureDelivery, error) {
		return prepareOrdinaryForLaunch(req, launchBuildStream(req.Stderr))
	}
	opts.AutoCapture = func(bins []string, platform string, stderr io.Writer, color bool) {
		autoCapture(bins, platform, stderr, stderr, color)
	}
	// And the macos-user launch's pick of what it stages from that store (install-capture.md
	// hand-off H4): through the resolver the sandbox's `capture-materialize` asks, which lives
	// in THIS package. On the launch path only, for AutoCapture's reason; a capture's own jail
	// has no store to stage from either way (its CapturesDir is "").
	opts.MacosUserCaptures = macosUserCaptures
	// Wire the fork build trigger (docs/design/forked-programs-as-packs.md OQ-FP4, FP-D1): every
	// selected fork the store holds no build of at its pin is built now, in a sealed jail, and the
	// jail is handed each fork's store key; a patched fork's advance runs here too
	// (docs/reference/patched-forks.md §7). Fourth seam of the same shape, and on the launch path
	// only for AutoCapture's reason: the build jail runs from NewDefaultOptions, never from here.
	//
	// EVERY LINE ON THE LAUNCH STREAM, stderr teed into launch.log: the builds' lines are launch
	// lines, which never go on the jail command's stdout (`yolo -- <cmd>` passes that through), and
	// one stream keeps a build's start, progress and result lines in order (buildreport.go).
	opts.BuildForks = func(req run.ForkBuildRequest) map[string]entrypoint.ForkDelivery {
		stream := launchBuildStream(req.Stderr)
		return buildForksForLaunch(req, stream, stream, colorForWriter(os.Stderr))
	}
	// And the TREE ARM beside it (docs/reference/pi-extensions.md §6.1, §8.1): each patched
	// extension's advance and the per-launch copy its jail mounts, on the launch path only for the
	// same reason.
	opts.BuildTrees = func(req run.TreeBuildRequest) map[string]run.TreeDelivery {
		stream := launchBuildStream(req.Stderr)
		return deliverTreesForLaunch(req, stream, stream, colorForWriter(os.Stderr))
	}
	// And THE SLOT AS ONE ACT (buildpool.go; docs/design/pi-extension-store-builds.md XB-D10), which a
	// fresh launch calls in place of the two: every key of both halves at once in one pool, under one
	// interrupt scope.
	opts.BuildSlot = func(req run.BuildSlotRequest) (map[string]entrypoint.ForkDelivery, map[string]run.TreeDelivery) {
		var stderr io.Writer
		switch {
		case req.Forks != nil:
			stderr = req.Forks.Stderr
		case req.Trees != nil:
			stderr = req.Trees.Stderr
		}
		return runBuildSlot(req, launchBuildStream(stderr), colorForWriter(os.Stderr))
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
		// ref above is handed in so the callee can publish the collector back.
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

// macosUserIdentity is who a macos-user launch's sandbox runs as: the mode, and the invoking
// user's home and name, which the run-as-you mode runs with.
type macosUserIdentity struct {
	Mode        macosuser.Mode
	Setup       setup.Setup
	SetupSource setup.Source
	HostHome    string
	HostUser    string
	// SharedTmp is the user-scope `macos_user_shared_tmp` hatch (OQ-MM8, MMP-D18).
	SharedTmp bool
	// ShareHostFonts is the user-scope `share_host_fonts` opt-in (OQ-NX9).
	ShareHostFonts bool
}

// runMacosUserBackend is macosuser.RunMacosUser behind a package var, for launchRunPipeline's
// reason: a test can read the Options the handler hands the backend (its mode above all)
// without running a launch.
var runMacosUserBackend = macosuser.RunMacosUser

// macosUserHostIdentity is the invoking user's resolved home and name, which a run-as-you
// launch runs with (macosuser.Options.HostHome, HostUser). The home is resolved through its
// symlinks, since the profile names it verbatim. The name is $USER, else the home's last
// element; yolo is built without cgo, and without cgo os/user on darwin answers from $HOME.
func macosUserHostIdentity() (home, user string) {
	home, _ = os.UserHomeDir()
	if home != "" {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
	}
	user = os.Getenv("USER")
	if user == "" && home != "" {
		user = filepath.Base(home)
	}
	return home, user
}

// macosUserRun is the run.Options.MacosUserRun seam impl: it assembles the
// real macosuser deps (the session runner of the launch's signal arm, and the
// native darwin nix materialize) and runs the Seatbelt-sandboxed launch. repoRoot is the yolo-jail checkout root (the nix
// build root for darwin `packages:`); the native-Go bootstrap self-execs the
// staged yolo binary and needs no source tree. packRoot is the host-side staged
// pack tree, which the run pipeline staged before dispatching here, and packEnv is the
// profile/provider channel it composed before dispatching there too. arm is the launch's signal
// arm, which Run has installed by now, and log its timing collector (nil when not recording).
// macos-hardware-gated; on Linux macosuser fails closed at its IsMacOS precondition (dry-run
// works anywhere).
func macosUserRun(cfg *jsonx.OrderedMap, workspace string, agents, agentArgv []string,
	repoRoot, packRoot string, homeOverlay macosuser.HomeOverlay, hostCtx macosuser.HostContext, dryRun bool,
	packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool, jailDaemons macosuser.JailDaemons,
	arm *run.MacosUserArm, log *perf.Log, who macosUserIdentity) int {
	materialize := func(nixRoot string, packages []any) (*macosuser.Darwin, bool, error) {
		// system "" → darwinpkg.NativeSystem(), the running platform. NOT a
		// hardcoded aarch64-darwin: this backend is macOS-only but Macs are not
		// all Apple Silicon (BACKLOG E8's bug class).
		system := darwinpkg.NativeSystem()
		pkgs, err := darwinpkg.Materialize(nixRoot, packages, system, os.Stderr, log)
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
	return runMacosUserBackend(macosUserSessionDeps(arm, log, materialize),
		macosuser.Options{
			// Who the sandbox runs as (the setup's run_as), and the invoking user the run-as-you mode
			// runs with; the account mode reads neither of the last two.
			Mode:           who.Mode,
			Setup:          who.Setup,
			SetupSource:    who.SetupSource,
			HostHome:       who.HostHome,
			HostUser:       who.HostUser,
			SharedTmp:      who.SharedTmp,
			ShareHostFonts: who.ShareHostFonts,
			// The session-start hook the run pipeline registered on its arm, run by the backend
			// just before the session's command and on no other path.
			OnAgentStart: arm.AgentStarting,
			// The endpoint files of the workspace's macos-user keeper a session granted already, and the
			// record of what this session's stage grants (run's macosUserGrants, carried by the arm).
			SkipGrant:       arm.SkipGrant,
			OnStaged:        arm.Staged,
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
			JailDaemons:     jailDaemons,
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
//
// THE COLOR DECISION IS MADE HERE TOO, for the same reason: it mirrors run's gate
// (tty.Color, via colorForWriter) — requested for the interactive front door, gated on a
// real TTY and on NO_COLOR — and resolved inline at the launch call site it could stop
// consulting that gate with nothing failing, since no unit test runs a launch.
// TestMacosLaunchDepsHonorNoColor reads it off this function. The dry-run plan render
// forces color OFF internally (byte-pinned goldens), so it only affects the live
// setup/teardown chatter.
func macosLaunchDeps(runProxy func(argv []string) int,
	materialize func(repoRoot string, packages []any) (*macosuser.Darwin, bool, error)) macosuser.Deps {
	deps := macosuser.RealDeps(runProxy, materialize, colorForWriter(os.Stdout))
	deps.LockWorkspace = workspaceLockSeam
	deps.GuestBinaries = guestBinariesSeam
	return deps
}

// macosUserSessionDeps is macosLaunchDeps for one launch: the session runs under its signal arm
// (run.MacosUserArm.RunSession, which forwards SIGTERM and SIGHUP to the session's sudo and absorbs
// SIGINT and SIGQUIT), the backend asks the arm at each step whether a signal is ending the launch
// (Ending), and its steps are spanned on the launch's collector. A function so the wiring can fail a
// test, for macosLaunchDeps' reason (TestMacosUserSessionDepsAreTheArms).
func macosUserSessionDeps(arm *run.MacosUserArm, log *perf.Log,
	materialize func(repoRoot string, packages []any) (*macosuser.Darwin, bool, error)) macosuser.Deps {
	deps := macosLaunchDeps(arm.RunSession, materialize)
	deps.Ending = arm.Ending
	deps.Perf = log
	return deps
}

// guestBinariesSeam selects the complete Darwin helper set paired with the
// resolved flake bundle. Launches never build guest helpers.
func guestBinariesSeam(repoRoot string) (string, error) {
	return resolveGuestBinaries(repoRoot)
}

func resolveGuestBinaries(repoRoot string) (string, error) {
	prebuilt := macosuser.PrebuiltGuestBinDir(repoRoot)
	missing := missingGuestBinaries(prebuilt)
	if len(missing) != 0 {
		return "", fmt.Errorf("the selected bundle at %s is missing Darwin guest binaries: %s. Launches do not build helpers; run `just build-dev-bundle`, then invoke the printed launcher by path, or run `just install` for the installed bundle",
			repoRoot, strings.Join(missing, ", "))
	}
	return prebuilt, nil
}

// missingGuestBinaries returns the macosuser.GuestBinaries members that are not
// regular files, in the set's declared order.
func missingGuestBinaries(dir string) []string {
	var missing []string
	for _, name := range macosuser.GuestBinaries {
		if info, err := os.Stat(filepath.Join(dir, name)); err != nil || !info.Mode().IsRegular() {
			missing = append(missing, name)
		}
	}
	return missing
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

// guestPackTreeSweep is prune.Options.GuestPackTrees: macosuser.PrunePackTrees in prune's own
// reap shape, and whether the staging root has a packs directory at all. The selected setup's
// run_as decides whose processes are the sandbox's (actMacosUserMode); any setup not run as you
// reads as the account mode, whose reader declines rather than guesses when the account is absent.
func guestPackTreeSweep(apply bool) (prune.EmbeddedReap, bool) {
	mode := actMacosUserMode()
	r := prunePackTrees(mode, apply)
	return guestPackTreeReap(r), r.Applicable
}

// prunePackTrees is macosuser.PrunePackTrees behind a package var, so a test can read the mode
// the sweep is handed.
var prunePackTrees = macosuser.PrunePackTrees

// guestPackTreeReap converts one macos-user sweep to prune's reap shape.
func guestPackTreeReap(r macosuser.PackTreeReap) prune.EmbeddedReap {
	out := prune.EmbeddedReap{Declined: r.Declined, Bytes: r.Bytes()}
	for _, e := range r.Removed {
		out.Removed = append(out.Removed, prune.EmbeddedEntry{Name: e.Path, Path: e.Path, Bytes: e.Bytes,
			Why: "no sandbox process may read it", Err: e.Err})
	}
	for _, e := range r.Kept {
		out.Kept = append(out.Kept, prune.EmbeddedEntry{Name: e.Path, Path: e.Path, Bytes: e.Bytes,
			Why: e.Reason, Detail: e.Detail})
	}
	return out
}

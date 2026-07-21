// The `yolo prune` command implementation. It reclaims disk from
// yolo-jail storage:
// hardlink-dedup across workspaces, drop stopped containers, sweep old images
// and the image-tar cache, reap orphaned broker relays, reclaim orphan
// build-root generations, purge overlay-shadowed seed subtrees, and age-purge
// re-downloadable cache subdirs. Defaults to DRY-RUN; --apply actually reclaims.
//
// The byte/behavior-critical pieces — the reclaim decisions, FmtBytes numbers,
// and removed-name lists — live in the already-ported, parity-tested
// internal/prune engine (dedup atomicity, tri-state build-root liveness,
// shadowed-home delete-contents-not-dirs, CreatedAt lexical image sort, the
// runtime probes behind the RunFunc seam). This package is the thin
// orchestration: it wires the sections in Python's exact order, applies the
// flag gates, and renders the report.
//
// Output contract: the
// human output reproduces the SECTION ORDERING, the "would remove"/"removed"
// verbs, the disk-usage before-report, and the summary INFO — NOT the
// byte-identical rich ANSI. Rich markup is stripped; the FmtBytes numbers,
// reclaim decisions, and removed-name lists ARE byte-exact vs live Python.
package prune

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/execx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Options configures a prune Run. The flag fields mirror prune_cmd's typer
// options 1:1; the seams (Exec, Now, DetectRuntime, path getters, RelayBase,
// RelayKill, Out) are injectable so the whole command is deterministically
// testable — apply tests point Storage/Home/Cache at a temp root and never
// touch real storage. nil/zero seams are filled with real implementations by
// fillDefaults.
type Options struct {
	// --- flags (prune_cmd typer options) ---
	Apply            bool // --apply           (default false: dry-run)
	NoHardlink       bool // --no-hardlink
	DedupGlobal      bool // --dedup-global
	NoContainers     bool // --no-containers
	NoImages         bool // --no-images
	KeepImages       int  // --keep-images     (default 2)
	NoImageCache     bool // --no-image-cache
	NoBuildRoots     bool // --no-build-roots
	NoShadowedHome   bool // --no-shadowed-home
	ImageCacheKeep   int  // --image-cache-keep (default 3)
	CacheAge         int  // --cache-age        (default 30; 0 skips the pass)
	PurgeHeavyCaches bool // --purge-heavy-caches
	// --- seams ---
	// Color requests ANSI styling. It is honored ONLY when stdout is a real
	// terminal (Color && IsTTYStdout()): piped/redirected output stays byte-
	// identical stripped text, so parity is on the ANSI-stripped text and the
	// numbers/decisions/lists are identical regardless (goldens pin Color=false).
	Color bool
	// IsTTYStdout reports whether stdout is a real terminal. nil => a real
	// os.Stdout isatty probe (the same TCGETS ioctl the run package uses, NOT a
	// char-device mode check). Injectable so tests drive the color gate directly.
	IsTTYStdout func() bool
	// Out is where the report is written. nil => os.Stdout.
	Out io.Writer
	// DetectRuntime returns the effective runtime ("podman"/"container"). nil =>
	// a platform-aware runtime.ResolveRuntime (YOLO_RUNTIME > platform probe; no
	// config here). The CLI front door (runPrune) injects a config-aware variant
	// that also honors the workspace `runtime` key.
	DetectRuntime func() string
	// Exec is the container-runtime probe seam. nil =>
	// realProbeExec (capture_output, text, honoring the per-call timeout, with the
	// FileNotFoundError/OSError/Timeout => Ran=false degrade).
	Exec RunFunc
	// Now is the clock seam (cache-age cutoff, build-root/relay grace floors).
	// nil => time.Now.
	Now func() time.Time
	// GlobalStorage / GlobalHome / GlobalCache resolve the storage roots. nil =>
	// the real paths.* getters. Injected so apply tests use a temp root.
	GlobalStorage func() string
	GlobalHome    func() string
	GlobalCache   func() string
	// RelayBase is the dir scanned for orphaned broker-relay PID files. "" =>
	// "/tmp" (Python's default base in _relay_reap_orphans).
	RelayBase string
	// RelayKill reaps one relay by PID file (SIGTERM/SIGKILL + pid-file removal).
	// nil => realRelayKill. Only invoked on --apply for an orphaned relay.
	RelayKill func(pidFile string)
}

// NewDefaultOptions returns Options with prune_cmd's flag defaults (keep-images
// 2, image-cache-keep 3, cache-age 30) and every seam left nil (filled at Run).
// The front door constructs this, sets Color, overrides flags from argv, then
// calls Run.
func NewDefaultOptions() Options {
	return Options{KeepImages: 2, ImageCacheKeep: 3, CacheAge: 30}
}

func fillDefaults(o *Options) {
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.IsTTYStdout == nil {
		o.IsTTYStdout = func() bool { return isTTY(os.Stdout) }
	}
	if o.DetectRuntime == nil {
		o.DetectRuntime = func() string {
			return runtime.ResolveRuntime(os.Getenv("YOLO_RUNTIME"), "", paths.IsMacOS, func(bin string) bool {
				_, err := exec.LookPath(bin)
				return err == nil
			})
		}
	}
	if o.Exec == nil {
		o.Exec = realProbeExec
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.GlobalStorage == nil {
		o.GlobalStorage = pathsGlobalStorage
	}
	if o.GlobalHome == nil {
		o.GlobalHome = pathsGlobalHome
	}
	if o.GlobalCache == nil {
		o.GlobalCache = pathsGlobalCache
	}
	if o.RelayBase == "" {
		o.RelayBase = "/tmp"
	}
	if o.RelayKill == nil {
		o.RelayKill = realRelayKill
	}
}

// Grace floors + relay defaults, frozen from prune_cmd.py.
const (
	buildRootOlderThanSeconds = 3600.0 // older_than_seconds=3600 for the sweep
	relayOlderThanSeconds     = 3600.0 // _relay_reap_orphans default grace floor
	imagesHintThreshold       = 20 * (1 << 30)
)

// Run executes `yolo prune`, writing the report to Out, and returns the exit
// code (always 0 — prune never fails the process, matching prune_cmd which has
// no failure exit).
func Run(opts Options) int {
	fillDefaults(&opts)
	// Honest color gate: ANSI only when requested AND stdout is a real terminal,
	// so piped output stays byte-identical stripped text (the output contract).
	p := &printer{richtext.Printer{W: opts.Out, Color: opts.Color && opts.IsTTYStdout()}}
	apply := opts.Apply

	rt := opts.DetectRuntime()
	workspaces := FindYoloWorkspaces(rt, opts.Exec)

	mode := "DRY-RUN"
	if apply {
		mode = "APPLY"
	}
	p.line("[bold]yolo prune (" + mode + ")[/bold]")
	p.line(fmt.Sprintf("Runtime: %s  Workspaces tracked: %d", rt, len(workspaces)))
	for _, ws := range workspaces {
		p.line("  • " + ws)
	}
	if len(workspaces) == 0 {
		p.line("[dim]No yolo-* containers found — nothing to dedupe across.[/dim]")
	}

	gs := opts.GlobalStorage()

	// --- Pre-report ---
	before := DiskUsageReport(workspaces, gs)
	p.line("")
	p.line(fmt.Sprintf("[bold]Current usage[/bold]  total=%s  (workspaces=%s, global=%s)",
		FmtBytes(before.Total), FmtBytes(before.Workspaces), FmtBytes(before.GlobalStorage)))
	if len(before.Breakdown) > 0 {
		p.line("  [dim]global-storage breakdown (largest first):[/dim]")
		for _, kv := range sortByValueDesc(before.Breakdown) {
			p.line(fmt.Sprintf("    %-20s %12s", kv.name, FmtBytes(kv.size)))
		}
	}
	if len(before.CacheBreakdown) > 0 {
		top := sortByValueDesc(before.CacheBreakdown)
		if len(top) > 5 {
			top = top[:5]
		}
		p.line("  [dim]cache/ top 5 (largest first):[/dim]")
		for _, kv := range top {
			p.line(fmt.Sprintf("    cache/%-14s %12s", kv.name, FmtBytes(kv.size)))
		}
	}
	if imagesBytes := before.CacheBreakdown["images"]; imagesBytes >= imagesHintThreshold {
		p.line(fmt.Sprintf("  [yellow]hint:[/yellow] cache/images holds %s of jail tarballs.  "+
			"They're streamed once at podman load then unused — consider symlinking this subdir to HDD storage if you have it.",
			FmtBytes(imagesBytes)))
	}

	var totalSaved int64
	var totalLinks int
	var removedContainers []string
	var removedImages []string
	var imageCacheBytes int64
	var imageCacheFiles int

	// --- Hardlink dedup ---
	if !opts.NoHardlink && (len(workspaces) > 0 || opts.DedupGlobal) {
		p.line("")
		p.line("[bold]Hardlink dedup[/bold]")
		var entries []Entry
		if len(workspaces) > 0 {
			entries = append(entries, WalkDedupableWorkspaces(workspaces)...)
		}
		if opts.DedupGlobal {
			entries = append(entries, WalkGlobalDedupable(gs)...)
		}
		p.line(fmt.Sprintf("  candidate files: %s", fmtComma(len(entries))))
		if opts.DedupGlobal {
			p.line("  [dim]scope: workspaces + global cache/mise/home[/dim]")
		} else {
			p.line("  [dim]scope: workspaces only  (pass --dedup-global to include the shared caches)[/dim]")
		}
		saved, links := HardlinkDuplicateFiles(entries, apply)
		p.line(fmt.Sprintf("  %s: %s across %s hardlinks", verb(apply, "would save", "saved"), FmtBytes(saved), fmtComma(links)))
		totalSaved += saved
		totalLinks += links
	}

	// --- Stopped yolo-* containers ---
	if !opts.NoContainers {
		p.line("")
		p.line("[bold]Stopped yolo-* containers[/bold]")
		removedContainers = PruneStoppedContainers(rt, apply, opts.Exec)
		if len(removedContainers) > 0 {
			p.line(fmt.Sprintf("  %s: %d", verb(apply, "would remove", "removed"), len(removedContainers)))
			for _, name := range removedContainers {
				p.line("    • " + name)
			}
		} else {
			p.line("  [dim]none[/dim]")
		}
	}

	// --- Orphaned broker relays ---
	p.line("")
	p.line("[bold]Orphaned broker relays[/bold]")
	var live runtime.LiveSet
	if rt != "" {
		live = LiveYoloContainers(rt, opts.Exec)
	}
	if !live.Known {
		p.line(fmt.Sprintf("  [dim]skipped — could not enumerate running jails (%s); declining to sweep[/dim]", rt))
	} else {
		reaped := ReapRelayOrphans(opts.RelayBase, live.Known, live.Names, relayOlderThanSeconds, apply, opts.Now(), opts.RelayKill)
		if len(reaped) > 0 {
			p.line(fmt.Sprintf("  %s: %d relay(s)", verb(apply, "would reap", "reaped"), len(reaped)))
			for _, pidFile := range reaped {
				p.line("    • " + baseName(pidFile))
			}
		} else {
			p.line("  [dim]none[/dim]")
		}
	}

	// --- Old yolo-jail images ---
	if !opts.NoImages {
		p.line("")
		p.line(fmt.Sprintf("[bold]Old yolo-jail images[/bold]  (keep=%d)", opts.KeepImages))
		removedImages = PruneOldImages(rt, opts.KeepImages, apply, opts.Exec)
		if len(removedImages) > 0 {
			p.line(fmt.Sprintf("  %s: %d", verb(apply, "would remove", "removed"), len(removedImages)))
			for _, img := range removedImages {
				p.line("    • " + img)
			}
		} else {
			p.line("  [dim]none[/dim]")
		}
	}

	// --- Cached image tarballs ---
	if !opts.NoImageCache {
		p.line("")
		p.line(fmt.Sprintf("[bold]Cached image tarballs[/bold]  (keep=%d)", opts.ImageCacheKeep))
		imageCacheBytes, imageCacheFiles = PruneImageCache(joinPath(opts.GlobalCache(), "images"), opts.ImageCacheKeep, apply)
		if imageCacheFiles > 0 {
			p.line(fmt.Sprintf("  %s: %s across %s file(s)", verb(apply, "would remove", "removed"), FmtBytes(imageCacheBytes), fmtComma(imageCacheFiles)))
		} else {
			p.line("  [dim]none[/dim]")
		}
		totalSaved += imageCacheBytes
	}

	// --- Orphaned build-root generations ---
	var buildRootBytes int64
	var buildRootDirs int
	if !opts.NoBuildRoots {
		p.line("")
		p.line("[bold]Orphaned build-root generations[/bold]")
		referenced := FindReferencedBuildRoots(rt, opts.Exec)
		if !referenced.Known {
			p.line(fmt.Sprintf("  [dim]skipped — could not enumerate running jails (%s); declining to sweep[/dim]", rt))
		} else {
			buildRootBytes, buildRootDirs = PruneOrphanBuildRoots(gs, referenced, time.Duration(buildRootOlderThanSeconds*float64(time.Second)), apply, opts.Now())
			if buildRootDirs > 0 {
				p.line(fmt.Sprintf("  %s: %s across %s generation(s)", verb(apply, "would remove", "removed"), FmtBytes(buildRootBytes), fmtComma(buildRootDirs)))
			} else {
				p.line("  [dim]none[/dim]")
			}
		}
		totalSaved += buildRootBytes
	}

	// --- Shadowed seed subtrees ---
	var shadowedBytes int64
	var shadowedItems int
	if !opts.NoShadowedHome {
		p.line("")
		p.line("[bold]Shadowed seed subtrees[/bold]")
		p.line(fmt.Sprintf("  [dim]targets: %s (each overlay-masked at runtime)[/dim]", strings.Join(ShadowedHomePaths, ", ")))
		shadowedBytes, shadowedItems = PruneShadowedHome(opts.GlobalHome(), apply)
		if shadowedItems > 0 {
			p.line(fmt.Sprintf("  %s: %s across %s path(s)", verb(apply, "would remove", "removed"), FmtBytes(shadowedBytes), fmtComma(shadowedItems)))
		} else {
			p.line("  [dim]none[/dim]")
		}
		totalSaved += shadowedBytes
	}

	// --- Cache purge (age-based) ---
	var cacheBytes int64
	var cacheFiles int
	if opts.CacheAge > 0 {
		subdirs := append([]string{}, CachePurgeDefaultSubdirs...)
		if opts.PurgeHeavyCaches {
			subdirs = append(subdirs, CachePurgeHeavySubdirs...)
		}
		p.line("")
		p.line(fmt.Sprintf("[bold]Cache purge[/bold]  (subdirs=%s, age > %dd)", strings.Join(subdirs, ","), opts.CacheAge))
		cacheBytes, cacheFiles = PurgeCacheByAge(joinPath(gs, "cache"), subdirs, float64(opts.CacheAge), apply, opts.Now())
		p.line(fmt.Sprintf("  %s: %s across %s files", verb(apply, "would remove", "removed"), FmtBytes(cacheBytes), fmtComma(cacheFiles)))
		totalSaved += cacheBytes
	}

	// --- Summary ---
	p.line("")
	if apply {
		p.line(fmt.Sprintf("[bold green]Reclaimed %s[/bold green] via %s hardlinks, %d container(s), "+
			"%d image(s), %s image tar(s), %s build-root generation(s), %s shadowed seed path(s), %s cache file(s).",
			FmtBytes(totalSaved), fmtComma(totalLinks), len(removedContainers), len(removedImages),
			fmtComma(imageCacheFiles), fmtComma(buildRootDirs), fmtComma(shadowedItems), fmtComma(cacheFiles)))
	} else {
		p.line(fmt.Sprintf("[bold yellow]DRY-RUN:[/bold yellow] would reclaim %s via %s hardlinks, remove "+
			"%d container(s), %d image(s), %s image tar(s), %s build-root generation(s), %s shadowed seed path(s), %s cache file(s).  "+
			"Re-run with [cyan]--apply[/cyan] to execute.",
			FmtBytes(totalSaved), fmtComma(totalLinks), len(removedContainers), len(removedImages),
			fmtComma(imageCacheFiles), fmtComma(buildRootDirs), fmtComma(shadowedItems), fmtComma(cacheFiles)))
	}
	return 0
}

// --- small helpers ---
// verb picks the dry-run vs apply verb (the "would remove"/"removed" pattern
// throughout prune_cmd).
func verb(apply bool, dry, applied string) string {
	if apply {
		return applied
	}
	return dry
}

// nameSize pairs a breakdown key with its byte total for sorting.
type nameSize struct {
	name string
	size int64
}

// sortByValueDesc sorts a {name: bytes} breakdown largest-first. Python's
// sorted(items, key=lambda kv: kv[1], reverse=True) is a STABLE sort over the
// dict's insertion order; Go map iteration is randomized, so ties would render
// nondeterministically. We break ties by name to keep the display deterministic
// the byte TOTALS and set of names are identical to Python regardless of tie
// order (parity is on the numbers, and ties carry equal bytes).
func sortByValueDesc(m map[string]int64) []nameSize {
	out := make([]nameSize, 0, len(m))
	for k, v := range m {
		out = append(out, nameSize{k, v})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].size != out[j].size {
			return out[i].size > out[j].size
		}
		return out[i].name < out[j].name
	})
	return out
}

// fmtComma renders an int with thousands separators, matching Python's `{n:,}`.
func fmtComma(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func joinPath(a, b string) string {
	if strings.HasSuffix(a, "/") {
		return a + b
	}
	return a + "/" + b
}

// --- color-aware printer (delegates to the shared internal/richtext renderer) ---
// printer wraps richtext.Printer so prune's report lines route through the one
// shared renderer instead of a local strip-always regex. Construct with Color
// already resolved to (requested && on a TTY) — see Run: color renders ANSI on a
// terminal, and stays byte-identical stripped text when piped (the output
// contract; parity is on the ANSI-stripped text).
type printer struct {
	richtext.Printer
}

// line writes one console.print line, rendering known style tags to ANSI when
// color is on and stripping them otherwise (literals like [y/N] survive both).
func (p *printer) line(s string) { p.Print(s) }

// --- real seams ---
// realProbeExec runs a container-runtime probe with the given timeout, returning
// captured stdout. A missing binary / start failure / timeout yields Ran=false
// (the FileNotFoundError/OSError/TimeoutExpired degrade); a completed run yields
// Ran=true with the exit status in RC (Python treats non-zero RC as an
// empty/None degrade in the engine).
func realProbeExec(argv []string, timeout time.Duration) ProbeResult {
	if len(argv) == 0 {
		return ProbeResult{}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		return ProbeResult{Ran: false}
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		<-done
		return ProbeResult{Ran: false} // TimeoutExpired => degrade
	case <-done:
		rc := 0
		if cmd.ProcessState != nil {
			rc = cmd.ProcessState.ExitCode()
		}
		return ProbeResult{Stdout: stdout.String(), RC: rc, Ran: true}
	}
}

// realRelayKill reaps one orphaned broker relay: read its PID, SIGTERM it,
// briefly poll, SIGKILL a straggler, then remove the PID file. Best-effort —
// every step tolerates a missing/dead target. The recycled-PID identity guard
// and pgrep fallback of Python's _relay_kill are omitted here (the same
// simplification internal/cli/run.relayKill documents): the mtime grace floor +
// no-live-hash filter in ReapRelayOrphans make a genuine orphan the overwhelming
// case, and the tri-state liveness probe bounds a misfire. This path is only
// reached under --apply for a relay whose hash matches no live jail.
func realRelayKill(pidFile string) {
	raw, err := os.ReadFile(pidFile)
	if err == nil {
		if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil && pid > 0 {
			if execx.IsAlive(pid) {
				_ = killPID(pid, false) // SIGTERM
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if !execx.IsAlive(pid) {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				if execx.IsAlive(pid) {
					_ = killPID(pid, true) // SIGKILL
				}
			}
		}
	}
	_ = os.Remove(pidFile)
}

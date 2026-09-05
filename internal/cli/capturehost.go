package cli

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// capturehost.go is `yolo capture <bin>` — the HOST act of install-capture
// (docs/design/program-delivery.md §6.3): run a vendor installer once, in a jail that
// exists only for that purpose, and file what it left behind in the machine-wide store.
//
// # The shape, and why every step is where it is
//
//	HonoredInstalls        which pack declares <bin>, and does its ORIGIN permit the installer
//	Store.Stage(<bin>)     a scratch dir INSIDE the store — admission is a rename
//	run.Run(...)           the ORDINARY run pipeline, workspace = that scratch dir
//	Store.AdmitEntry(...)  the finished proto-entry becomes entries/<key>, marker last
//	receipt                appended beside the entry
//
// THE SCRATCH DIR IS THE WORKSPACE, and that is the whole trick. The run pipeline binds the
// workspace at /workspace and binds `<workspace>/.yolo/home/{npm-global,local,go}` at the
// three home surfaces — so inside the jail every captured byte has TWO paths, and only the
// /workspace-side one shares a mount with /workspace/out. rename(2) compares the MOUNT, not
// the device (MEASURED here 2026-09-04: the same directory renames through one path and
// returns EXDEV through the other), so reaching the surfaces through the workspace is the
// difference between moving 1.2 GB and copying it. See paths.WorkspaceHomeState and
// capture.Options.SurfaceRoot; captureJailArgv is where the two facts meet.
//
// Putting the workspace under <CapturesDir>/staging is forced by the OTHER rename: admission
// moves the proto-entry into <CapturesDir>/entries, and Store.Admit refuses a staged tree
// from anywhere else rather than silently copying it.
//
// THE INSTALL IS THE LAUNCHER'S, not a second implementation of it. The jail runs
// `env YOLO_INSTALL_ONLY=1 <bin>`, which resolves to the generated native launcher
// (~/.yolo/bin/launch is near the head of PATH and a fresh capture home has nothing else
// by that name) and takes its `_do_install` path: the same download-to-a-file, the same web-page
// sniff, the same `YOLO_BYPASS_SHIMS=1 bash`. Anything else would capture bytes a launch
// would never have produced, which is the one property slice 4's materialize depends on.
// entrypoint.InstallOnlyEnv is what stops the launcher exec'ing the tool afterwards.

// captureOutLeaf is the entry-shaped scratch directory inside the capture workspace: the
// driver fills <workspace>/out/tree and writes <workspace>/out/capture-manifest.json, which
// is exactly what Store.AdmitEntry consumes.
//
// A SIBLING of .yolo/ rather than inside it: `<workspace>/.yolo` is yolo's own per-workspace
// state and the home overlay lives there, so an out dir underneath it would sit beside the
// surfaces it is draining — one layout change away from being inside one.
const captureOutLeaf = "out"

const captureUsage = `yolo capture — record what a vendor installer leaves behind, once per machine

  yolo capture <bin>      run <bin>'s installer in a throwaway jail and store the result

A ` + "`program via installer`" + ` contribution names a URL whose contents run as a shell
script: there is nothing to pin, because the installer RUN is the resolution. So yolo runs it
once, in a jail with an empty home, records the delta as a content-addressed entry under
~/.local/share/yolo-jail/captures, and writes a receipt beside it.

The capture is machine-local and never distributed. <bin> must be a program some selected
pack installs with ` + "`via: \"installer\"`" + ` — an npm-declared program has a registry
version to name and needs no capture.`

// runCapture is the `yolo capture` dispatch entry.
//
// args[0] is the subcommand token — dispatchNative hands every handler the whole argv slice
// — so it is dropped here, the way runPack does it. Getting this wrong is invisible to a
// unit test that calls captureHost directly and fails on the first real invocation with
// "one program at a time (got \"capture\" and …)".
func runCapture(args []string) int {
	rest := args
	if len(rest) > 0 {
		rest = rest[1:]
	}
	return captureHost(rest, os.Stdout, os.Stderr, true)
}

// captureHost is runCapture with its writers injected, so a test can read what it said.
func captureHost(args []string, out, errw io.Writer, color bool) int {
	var bin string
	for _, a := range args {
		switch {
		case isHelpToken(a):
			fmt.Fprintln(out, captureUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo capture: unexpected flag %q\n\n%s\n", a, captureUsage)
			return 2
		case bin == "":
			bin = a
		default:
			fmt.Fprintf(errw, "yolo capture: one program at a time (got %q and %q)\n\n%s\n",
				bin, a, captureUsage)
			return 2
		}
	}
	if bin == "" {
		fmt.Fprintln(errw, captureUsage)
		return 2
	}
	// ValidBinName before anything else touches the filesystem: the name becomes a staging
	// directory and a lock filename, and packdecl's own gate is the one that decides what a
	// bin name may contain. Store.Stage refuses a traversing segment too — this refuses it
	// with a message that names the actual rule.
	if !packdecl.ValidBinName(bin) {
		fmt.Fprintf(errw, "yolo capture: %q is not a program name\n", bin)
		return 2
	}

	pr := richtext.Printer{W: out, Color: color}
	target, err := resolveCaptureTarget(bin)
	if err != nil {
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		return 1
	}

	// ONE CAPTURE OF ONE BIN AT A TIME, non-blocking. Two concurrent captures of the same
	// program would run the vendor's installer twice into two jails and race to admit the
	// result; the admit itself is idempotent (identical bytes, identical key), but the two
	// runs cost the download twice and can differ, so the second entry would silently be a
	// different package under a different key.
	//
	// It REFUSES rather than waiting, which is where this departs from the launch lock.
	// A launch that cannot take its lock has something to attach to; a capture does not,
	// and waiting would park a human behind another process's multi-gigabyte download with
	// no way to tell how long.
	lock := tryFlockAt(captureLockPath(bin))
	if lock == nil {
		fmt.Fprintf(errw, "yolo capture: another capture of %s is already running "+
			"(lock: %s) — nothing was captured\n", bin, captureLockPath(bin))
		return 1
	}
	defer lock.Close()

	store := &capture.Store{Dir: paths.CapturesDir()}
	staging, err := store.Stage(bin)
	if err != nil {
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		return 1
	}
	cname := runtime.FromWorkspace(staging)
	defer cleanupCaptureWorkspace(staging, cname)

	pr.Printf("[bold]capture[/bold] [cyan]%s[/cyan]  [dim]%s[/dim]", bin, target.URL)
	pr.Printf("[dim]pack %s → jail %s[/dim]", target.Pack, cname)

	if rc := runCaptureJail(staging, bin, out, errw, color); rc != 0 {
		fmt.Fprintf(errw, "yolo capture: the capture jail exited %d — nothing was stored\n", rc)
		return rc
	}

	outDir := filepath.Join(staging, captureOutLeaf)
	m, err := capture.ReadManifest(outDir)
	if err != nil {
		fmt.Fprintf(errw, "yolo capture: reading the capture manifest: %v\n", err)
		return 1
	}
	// AN EMPTY DELTA IS A FAILURE, not an empty package. It is what a bin resolved to
	// something already on PATH looks like (the image bakes a program a pack also claims —
	// the ~/.yolo/bin/launch ordering makes the baked one win), and admitting it would file
	// an entry that materializes nothing and satisfies every later resolve.
	if len(m.Entries) == 0 {
		fmt.Fprintf(errw, "yolo capture: %s's installer left nothing in the capture "+
			"surfaces (%s) — nothing was stored. Either it writes somewhere else, or "+
			"%s already resolved to a program this image bakes.\n",
			bin, strings.Join(m.Surfaces, ", "), bin)
		return 1
	}

	entry, err := store.AdmitEntry(outDir)
	if err != nil {
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		return 1
	}
	receipt := entrypoint.CaptureReceipt{
		Bin:      bin,
		Declared: target.URL,
		Key:      entry.Key,
		Digest:   capture.DigestHash(entry.Digest),
		Bytes:    m.TotalBytes(),
		Path:     entry.Root,
		Platform: m.Platform,
		Act:      entrypoint.ReceiptActRecord,
		Time:     time.Now(),
	}
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), receipt.Line()); err != nil {
		fmt.Fprintf(errw, "yolo capture: writing the capture receipt: %v\n", err)
		return 1
	}
	pr.Printf("[green]captured[/green] %s  [cyan]%s[/cyan]  %d paths, %s  [dim]%s[/dim]",
		bin, entry.Key, len(m.Entries), humanBytes(m.TotalBytes()), entry.Root)
	return 0
}

// captureTarget is the one install declaration a capture is about.
type captureTarget struct {
	// Bin is the program name.
	Bin string
	// URL is the installer URL, which becomes the receipt's `declared`.
	URL string
	// Pack is the pack that declared it, for the report.
	Pack string
}

// resolveCaptureTarget finds the pack-declared native installer for bin.
//
// THROUGH HonoredInstalls, NEVER THE MANIFEST. A fetched pack's installerUrl is refused by
// the origin gate (packload.go:491) precisely so that a git ref cannot make yolo execute a
// shell script; a capture that read InstallContributions directly would run exactly what
// that gate exists to refuse, one layer below where anyone would look for it. Refused
// declarations therefore never reach this function's result at all — a bin that is only
// declared by a refused install reads as "not declared", and the error says so with the
// refusals attached.
func resolveCaptureTarget(bin string) (*captureTarget, error) {
	entries, err := config.LoadPacks(nil)
	if err != nil {
		return nil, err
	}
	var refusals, unresolved, npmBins []string
	for _, e := range entries {
		p := packForCheckDeps(e)
		if p == nil {
			// A fetched pack that was never `yolo pack install`ed, or whose remote is
			// unreachable. Named rather than skipped: "no pack declares <bin>" would be
			// a lie about a config that may well declare it.
			unresolved = append(unresolved, e.Name)
			continue
		}
		granted, refused := p.HonoredInstalls()
		refusals = append(refusals, refused...)
		for _, in := range granted {
			if in.Bin != bin {
				continue
			}
			if in.Kind != packdeclNativeKind {
				npmBins = append(npmBins, p.Name)
				continue
			}
			return &captureTarget{Bin: bin, URL: in.InstallerURL, Pack: p.Name}, nil
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no selected pack installs %q with an installer URL", bin)
	if len(npmBins) > 0 {
		fmt.Fprintf(&b, " — %s declares it via npm, which names a registry version and "+
			"needs no capture", strings.Join(npmBins, ", "))
	}
	for _, r := range refusals {
		fmt.Fprintf(&b, "\n  refused: %s", r)
	}
	if len(unresolved) > 0 {
		fmt.Fprintf(&b, "\n  not resolvable offline (run `yolo pack install`): %s",
			strings.Join(unresolved, ", "))
	}
	return nil, fmt.Errorf("%s", b.String())
}

// packdeclNativeKind is packdecl.Install.Kind for a `via: "installer"` contribution — the
// MIDDLE of the three names for one mechanism (manifest `via:"installer"` → this →
// receipt `kind:"installer"`). Spelled as a named constant here so the switch above reads
// as the same fact the shims.go install switch reads, and so a fourth name is visibly a
// fourth name rather than a bare string that happens not to match.
const packdeclNativeKind = "native"

// captureJailArgv is the command the capture jail runs — the whole in-jail half of a
// capture, as one argv.
//
// PURE, and pinned by a test, because it is where three separate facts have to agree and
// none of them is checkable at run time:
//
//   - the workspace is bound at containerWorkspace, so the out dir is inside that bind;
//   - the home surfaces are ALSO reachable under it, at paths.WorkspaceHomeState of that
//     same path — which is what makes the delta move a rename rather than a 1.2 GB copy;
//   - the installer is the generated launcher, run with entrypoint.InstallOnlyEnv set so it
//     installs and stops instead of exec'ing the tool into the surfaces being captured.
//
// `env` rather than a shell: the driver runs the argv verbatim with exec, so one more argv
// word is a smaller contract than a quoted `bash -c` string, and the variable is visible in
// `ps` and in the driver's own error messages.
func captureJailArgv(bin string) []string {
	return []string{
		"yolo", "internal", "capture-run",
		"--out=" + path.Join(containerWorkspace, captureOutLeaf),
		"--surface-root=" + paths.WorkspaceHomeState(containerWorkspace),
		"--", "env", entrypoint.InstallOnlyEnv + "=1", bin,
	}
}

// runCaptureJail launches the ordinary run pipeline against the scratch workspace.
//
// The ORDINARY pipeline, deliberately: a capture jail must be the same jail a launch
// produces, or the bytes it records are not the bytes a launch would have installed. The
// only thing this changes is the workspace and the command.
func runCaptureJail(workspace, bin string, out, errw io.Writer, color bool) int {
	opts := run.NewDefaultOptions()
	opts.Workspace = workspace
	opts.Args = captureJailArgv(bin)
	opts.Color = color
	opts.Stdout, opts.Stderr = out, errw
	// NO CAPTURE STORE IN A CAPTURE JAIL. Every ordinary launch binds the store :ro so a
	// native launcher can materialize instead of downloading (run/captures.go); this one
	// must not, and the reason is circularity rather than tidiness. The installer a capture
	// runs IS the launcher (see the file comment), and the launcher now tries materialize
	// first — so with the mount present, capturing a program that already has an entry
	// would reflink that entry into the capture home and then record what it found as a
	// fresh capture of bytes no installer produced this time. It would also make §6.3's
	// *update* ("a NEW capture, on an explicit act") impossible: `yolo capture` could never
	// pick up a newer vendor release, because it would keep re-recording the old one.
	//
	// Suppressed at the MOUNT rather than by a second exception inside the launcher, so the
	// property is unrepresentable instead of conditional: there is nothing in that jail to
	// resolve a capture against.
	opts.CapturesDir = func() string { return "" }
	// --new: never attach. A capture must run its installer in a home the BOOT just made,
	// and attaching to a live container for this workspace would run it in whatever state
	// that container is in. The scratch workspace is fresh per capture, so there is nothing
	// to attach to in practice; saying so makes it true by construction rather than by luck.
	opts.New = true
	// The scratch workspace carries no yolo-jail.jsonc, so the effective config is the
	// user's — the same `packs` any jail on this machine gets, which is what makes the
	// launcher for <bin> exist inside. Nothing here is a config a human wrote for this
	// workspace, so there is nothing for a human to approve; granting it up front stops a
	// user-config edit from turning a capture into a prompt about a directory they have
	// never seen.
	opts.AcceptConfigChanges = true
	// macos-user runs the capture natively, under the narrowed Seatbelt profile slice 6
	// built (macosuser.SeatbeltCaptureProfile): no container, a throwaway staging home on
	// neutral ground, and the shared /Users/_yolojail denied for the duration.
	//
	// RunCaptureAct leaves the proto-entry at the SAME dest the container arm writes, so
	// everything after this call in captureHost — read the manifest, refuse an empty delta,
	// AdmitEntry, receipt — is backend-blind and unchanged.
	//
	// ⚠ NOTHING BELOW THIS LINE IS MEASURED ON A MAC. The profile's bytes and both argvs are
	// unit-pinned; that Seatbelt HONORS the profile is not, and cannot be from Linux. The
	// probe that settles it is in install-capture.md slice 6 — a capture that silently wrote
	// to the shared home looks identical to one that did not.
	//
	// The homeOverlay the pipeline composed is DROPPED here, and that is the capture's
	// choice rather than an oversight: the overlay is skills and briefing prose for an
	// agent to read, and this jail runs one installer in a home it then deletes. See the
	// "" argument in macosuser.BuildCapturePlan, which is where the reasoning lives.
	// `blocked` is NOT dropped — the staging home must carry the same shims a launch
	// would, and core contributes none of them by itself.
	opts.MacosUserRun = func(cfg *jsonx.OrderedMap, _ string, _, _ []string,
		_, packRoot, _ string, dryRun bool, packEnv *jsonx.OrderedMap,
		blocked []packload.BlockedTool) int {
		deps := macosuser.RealDeps(nil, nil, color)
		deps.Out = out
		return macosuser.RunCaptureAct(deps, macosuser.CaptureOptions{
			Bin: bin, Config: cfg, HostPackRoot: packRoot, SandboxEnv: packEnv,
			BlockedTools: blocked,
		}, filepath.Join(workspace, captureOutLeaf), dryRun)
	}
	return captureRunPipeline(opts)
}

// captureRunPipeline is run.Run behind a package var, so a test can drive the WHOLE host act
// — resolve, stage, admit, receipt — without a container.
//
// The seam is here rather than at a boundary a caller passes through, for the reason
// hostApplyFlock's is: the thing worth pinning is the CALL SITE, and a test that constructed
// its own options and called the store directly would go green with this call deleted.
// Substituting the pipeline leaves every line above and below it in the test's path.
var captureRunPipeline = run.Run

// cleanupCaptureWorkspace removes what the capture jail left on the host.
//
// A capture boots a whole jail, so the scratch workspace ends up holding a provisioned home
// — bootstrap npm packages, generated scripts, a config overlay — none of which is the
// capture and all of which would accumulate one copy per captured program. The admitted
// entry has already been renamed out of it by the time this runs.
//
// Best-effort throughout: a capture that succeeded must not be reported as failed because
// its litter could not be swept. The next capture of the same bin clears the same paths
// anyway (Store.Stage removes its staging dir before creating it).
func cleanupCaptureWorkspace(workspace, cname string) {
	_ = os.RemoveAll(workspace)
	runtime.CleanupContainerTracking(cname)
	_ = os.RemoveAll(filepath.Join(paths.AgentsDir(), cname))
	_ = os.Remove(filepath.Join(paths.ApprovalsDir(), cname+".json"))
}

// captureLockPath is the per-program capture lock, beside the launch locks — the
// convention internal/cli/run/run.go establishes for everything that serialises on a
// filesystem lock, so `yolo prune` and a person poking around find them together.
//
// Keyed by BIN and not by the store: two captures of different programs are independent
// (different staging dirs, different keys, and admission is a rename per entry), and one
// store-wide lock would serialise them for no reason.
func captureLockPath(bin string) string {
	return filepath.Join(paths.GlobalStorage(), "locks", "capture-"+bin+".lock")
}

// humanBytes renders a byte count for the completion line. Base-10 units, matching the way
// the design and the plan quote this subsystem's numbers ("1.2 GB", not "1.1 GiB").
func humanBytes(n int64) string {
	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "kMGTPE"[exp])
}

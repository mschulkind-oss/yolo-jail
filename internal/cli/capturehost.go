package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
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

  yolo capture <bin>          run <bin>'s installer in a throwaway jail and store the result
  yolo capture <pack>/<name>  build a patched extension's tree now

A ` + "`program via installer`" + ` contribution names a URL whose contents run as a shell
script: there is nothing to pin, because the installer RUN is the resolution. So yolo runs it
once, in a jail with an empty home, records the delta as a content-addressed entry under
~/.local/share/yolo-jail/captures, and writes a receipt beside it.

The capture is machine-local and never distributed. <bin> must be a program some selected
pack installs with ` + "`via: \"installer\"`" + ` — an npm-declared program has a registry
version to name and needs no capture.

A program a FORK builds (` + "`via: \"source\"`" + `) is captured by BUILDING it: <bin>'s pinned
commit (forks.lock.json; pinned by its first launch, or here when nothing has pinned it yet,
and moved only by ` + "`yolo pack update`" + `) is checked out and built in a
sealed jail, which gets no credential, no host file and no host service, and the result is
stored under a build receipt naming the commit. This is the explicit rebuild: it builds even
when the store already holds that commit's build, for instance after the image changed.

A PATCHED EXTENSION (a ` + "`files`" + ` contribution with ` + "`source`" + ` and ` + "`patches`" + `) is
captured by its key, <pack>/<name>: its upstream is checked, its series replayed and the newest
version it fits built now in a sealed jail, ignoring a failed build's back-off.

Examples:
  yolo capture codex                  # record codex's installer once, for every jail
  yolo capture pi                     # rebuild a forked pi at its pinned commit
  yolo capture matt/pi-subagents      # build a patched pi extension now`

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
	return captureHost(rest, os.Stdout, os.Stderr, colorForWriter(os.Stdout))
}

// captureHost is runCapture with its writers injected, so a test can read what it said.
func captureHost(args []string, out, errw io.Writer, color bool) int {
	return captureHostWith(args, out, errw, color, captureAct{})
}

// captureAct is what a caller of the capture act decides about it, beyond its argv.
type captureAct struct {
	// runtime is the runtime the capture jail boots with, handed to the run pipeline for this act
	// alone — never through the process environment, which the agent a host launch execs next would
	// inherit. "" is the runtime a launch resolves. The host floor on a Mac names macos-user (HP-D2):
	// a container capture there records a Linux entry, which a Mac's floor cannot run.
	runtime string
	// jailStdout is where the capture jail's OWN stdout goes — pid 1's and the installer's in a
	// container, the account's commands' on macos-user (run.Options.JailStdout and SessionStdout,
	// captureStreams.jailOut and sessionOut) — when its caller
	// names a stream for it. nil is this process's stdout, a typed `yolo capture`'s. A host verb
	// names this process's stderr (hostJailStdout). The host arm needs none: its installer runs on
	// the act's own out and errw.
	jailStdout io.Writer
}

// captureHostWith is captureHost under act.
func captureHostWith(args []string, out, errw io.Writer, color bool, act captureAct) int {
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
	// A PATCHED EXTENSION is captured by its extension key, `<pack>/<name>`
	// (docs/design/patched-extensions.md §6.1, PF-D12 generalized): the check forced, then the
	// pending candidate built, or the good build's own inputs rebuilt, through the swap.
	if strings.Contains(bin, "/") {
		if rc, handled := captureTree(bin, out, errw, color); handled {
			return rc
		}
	}
	// ValidBinName before anything else touches the filesystem: the name becomes a staging
	// directory and a lock filename, and packdecl's own gate is the one that decides what a
	// bin name may contain. Store.Stage refuses a traversing segment too — this refuses it
	// with a message that names the actual rule.
	if !packdecl.ValidBinName(bin) {
		fmt.Fprintf(errw, "yolo capture: %q is not a program name\n", bin)
		sel := selectConfiguredHostPacks()
		if sel.loadErr != nil {
			// The programs a capture takes are read from the config, so an unreadable one leaves
			// none to list: "None of your packs installs a program" would be a claim about packs
			// this run never read.
			fmt.Fprintf(errw, "  Your config could not be read, so the programs a capture takes are "+
				"not known: %v\n  Fix what it names in %s, then run `yolo capture <program>` for a "+
				"program your packs install.\n", sel.loadErr, paths.UserConfigPath())
			return 2
		}
		fmt.Fprintf(errw, "  %s\n", captureChoicesStep(sel.packs, ""))
		return 2
	}

	pr := richtext.Printer{W: out, Color: color}
	target, err := resolveCaptureTarget(bin)
	if err != nil {
		// The refusal carries its own next step, as every stop below does (resolveCaptureTarget).
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		return 1
	}
	// A FORK'S PROGRAM: `yolo capture <bin>` is its explicit REBUILD, from the pinned commit, in
	// the sealed build jail (forkbuild.go) — never the vendor installer of the base it forks.
	if target.Fork != nil {
		return captureFork(*target.Fork, out, errw, color)
	}
	// WHICH ARM (host-tool-provisioning.md HP-D18): a capture jail, or — on Linux, with no runtime
	// selected and none on PATH — this host, the installer confined by Landlock. A fork's build above
	// never comes here: it builds in a sealed jail or not at all (forked-programs-as-packs.md §12).
	arm := chooseCaptureArm(captureHostGOOS, act.runtime)
	if arm.hostWhy != "" {
		// Said before the jail arm's own refusal of the missing runtime, which names that runtime's
		// install: this is the other way to a capture here, and why it is not taken.
		fmt.Fprintf(errw, "yolo capture: %s, and yolo cannot confine its installer on this host instead (%s)\n",
			arm.blocked, arm.hostWhy)
		fmt.Fprintf(errw, "  Install %s (`yolo check` names how on this machine), or run a kernel with Landlock "+
			"enabled, %s.\n", arm.missing, captureAgain(bin))
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
		fmt.Fprintf(errw, "  %s\n", captureWaitStep(bin))
		return 1
	}
	defer lock.Close()

	store := &capture.Store{Dir: paths.CapturesDir()}
	staging, err := store.Stage(bin)
	if err != nil {
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		fmt.Fprintf(errw, "  Fix what it names, %s.\n", captureAgain(bin))
		return 1
	}
	cname := runtime.FromWorkspace(staging)
	defer cleanupCaptureWorkspace(staging, cname)

	pr.Printf("[bold]capture[/bold] [cyan]%s[/cyan]  [dim]%s[/dim]", bin, target.URL)
	runJail := func() int {
		return runCaptureJail(staging, bin, captureJailArgv(bin), nil,
			captureStreams{out: out, errw: errw, jailOut: act.jailStdout, sessionOut: act.jailStdout}, color,
			captureAct{runtime: arm.runtime})
	}
	if arm.host() {
		pr.Printf("[dim]pack %s → this host, its installer confined by Landlock (ABI %d) to a throwaway home; "+
			"the result serves yolo's host floor, and every jail captures its own[/dim]", target.Pack, arm.hostABI)
		runJail = func() int { return runHostCapture(staging, target, arm.hostABI, out, errw) }
	} else {
		pr.Printf("[dim]pack %s → jail %s[/dim]", target.Pack, cname)
	}

	// THE CAPTURE ACT'S MIDDLE (captureStaged, shared with a fork's build): the jail, the
	// manifest, and the admit. AN EMPTY DELTA IS A FAILURE, not an empty package. It is what a
	// bin resolved to something already on PATH looks like (the image bakes a program a pack also
	// claims — the ~/.yolo/bin/launch ordering makes the baked one win), and admitting it would
	// file an entry that materializes nothing and satisfies every later resolve.
	empty := false
	entry, m, err := captureStaged(store, staging, runJail,
		func(m *capture.Manifest) string {
			empty = true
			if arm.host() {
				// The launcher heads the installer's PATH, so <bin> resolved to it: only the first half
				// of a jail's two reasons is possible here.
				return fmt.Sprintf("%s's installer left nothing in the capture surfaces (%s): it writes "+
					"somewhere else", bin, strings.Join(m.Surfaces, ", "))
			}
			return fmt.Sprintf("%s's installer left nothing in the capture surfaces (%s). Either it "+
				"writes somewhere else, or %s already resolved to a program this image bakes",
				bin, strings.Join(m.Surfaces, ", "), bin)
		}, nil)
	var exit captureJailExit
	switch {
	case errors.As(err, &exit) && arm.host():
		fmt.Fprintf(errw, "yolo capture: the host capture exited %d — nothing was stored\n", exit.rc)
		fmt.Fprintf(errw, "  %s\n", captureJailFailedStep(bin))
		return exit.rc
	case errors.As(err, &exit):
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		fmt.Fprintf(errw, "  %s\n", captureJailFailedStep(bin))
		return exit.rc
	case err != nil && empty && arm.host():
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		fmt.Fprintf(errw, "  Pack %s's installer writes somewhere else: tell that pack's author, or, if yolo "+
			"ships pack %s, report it at %s.\n", target.Pack, target.Pack, entrypoint.IssuesURL)
		return 1
	case err != nil && empty:
		// Neither half is the user's command to run: a `packages` entry already delivers the
		// program to every jail, or the pack's installer writes outside the surfaces, which its
		// author fixes (rung 4), or yolo for a pack it ships.
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		fmt.Fprintf(errw, "  If `packages` in your config lists %s, every jail has it already and it "+
			"needs no capture. Otherwise pack %s's installer writes somewhere else: tell that pack's "+
			"author, or, if yolo ships pack %s, report it at %s.\n", bin, target.Pack, target.Pack,
			entrypoint.IssuesURL)
		return 1
	case err != nil:
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		fmt.Fprintf(errw, "  Fix what it names, %s.\n", captureAgain(bin))
		return 1
	}
	// THE RECORDED ORIGIN: a host capture's receipt names its platform with the host's mark
	// (hostCapturePlatform), so no jail's selection can name it and the host floor's can.
	platform := m.Platform
	if arm.host() {
		platform = hostCapturePlatform(platform)
	}
	receipt := entrypoint.CaptureReceipt{
		Bin:      bin,
		Declared: target.URL,
		Key:      entry.Key,
		Digest:   capture.DigestHash(entry.Digest),
		Bytes:    m.TotalBytes(),
		Path:     entry.Root,
		Platform: platform,
		Act:      entrypoint.ReceiptActRecord,
		Time:     time.Now(),
	}
	if err := entrypoint.AppendReceiptLine(capture.ReceiptsPath(entry.Root), receipt.Line()); err != nil {
		// A re-run admits the same bytes to the same entry and appends its receipt
		// (TestCaptureIsIdempotentAndAppendsASecondReceipt), so it is the fix once the path takes one.
		fmt.Fprintf(errw, "yolo capture: writing the capture receipt: %v\n", err)
		fmt.Fprintf(errw, "  The entry is stored, and a launch finds it by its receipt: fix what it "+
			"names, %s, which writes the receipt.\n", captureAgain(bin))
		return 1
	}
	// A REMEMBERED AUTO-CAPTURE FAILURE ENDS HERE, whichever act asked for this capture: a launch's
	// auto-capture, the `yolo capture <bin>` its warning names as the retry, or the host floor's
	// (docs/design/program-delivery.md OQ-PD26). The program is stored, so the memo has nothing
	// left to say, and a launch that misses again later starts from the first wait. Best-effort:
	// a memo that stays only holds off a capture of a program the store now holds.
	_ = store.ClearAutoFailure(bin, platform)
	pr.Printf("[green]captured[/green] %s  [cyan]%s[/cyan]  %d paths, %s  [dim]%s[/dim]",
		bin, entry.Key, len(m.Entries), humanBytes(m.TotalBytes()), entry.Root)
	return 0
}

// captureFork is `yolo capture <forked bin>`: the explicit REBUILD of a fork at its pinned commit
// (docs/design/forked-programs-as-packs.md §9 "no implicit rebuilds", OQ-FP2's forced rebuild). It
// builds even when the store holds this build, and REFUSES on contention, as a capture does: a human
// who typed it can re-run it, where a launch waits (FP-D1).
func captureFork(f packload.Fork, out, errw io.Writer, color bool) int {
	if f.Patched() {
		return capturePatchedFork(f, out, errw, color)
	}
	pin := forkPinOf(f)
	if pin.Commit == "" && pin.Pinnable { // never inside a jail (forkPinOf)
		// NO PIN YET, and this act needs none made beforehand (FP-D18): it pins the fork as a launch
		// does, through the launch's own pinner, says so, and builds what it pinned.
		pin = run.PinLaunchForks([]packload.Fork{f}, func() (func(string), func()) {
			fmt.Fprintf(errw, "yolo capture: fetching %s to pin fork %s\n", f.Source, f.Key())
			return func(line string) { fmt.Fprintf(errw, "yolo capture: %s\n", line) }, func() {}
		})[0]
		if pin.Pinned {
			fmt.Fprintf(out, "%s\n", pin.PinnedLine())
		}
		if pin.Warning != "" {
			fmt.Fprintf(errw, "Warning: %s\n", pin.Warning)
		}
	}
	if pin.Commit == "" {
		// The pin's reason names what makes one (packload.ForkPin.Reason); this is the rest of the
		// way back to the build the user asked for.
		fmt.Fprintf(errw, "yolo capture: %s\n", pin.Line())
		fmt.Fprintf(errw, "  then: yolo capture %s\n", f.Bin)
		return 1
	}
	// THE BUILD'S PLATFORM IS ITS JAIL'S (FP-D24): a capture under macos-user builds for this Mac, as
	// the Mac's host floor does, and every container backend for Linux. Only macos-user is named to
	// the build, so the jail it boots is the one the darwin platform was read from. A container
	// backend is left to the run pipeline's own resolution (which skips a `container` that is not
	// Apple's or does not answer, and honors the guest notch), and a refusal there then names what
	// the user set rather than a YOLO_RUNTIME they did not.
	rt := captureRuntime()
	if rt != "macos-user" {
		rt = ""
	}
	b := forkBuild{Fork: f, Commit: pin.Commit, Platform: forkBuildPlatform(rt)}
	if _, err := buildFork(b, buildMode{force: true, lock: pidlock.NoWait, runtime: rt}, out, errw, color); err != nil {
		fmt.Fprintf(errw, "yolo capture: %v\n", err)
		var exit captureJailExit
		switch {
		case errors.Is(err, errForkBuildLocked):
			fmt.Fprintf(errw, "  %s\n", captureWaitStep(f.Bin))
		case errors.Is(err, errForkBuildNotStarted):
			// The jail stopped before its build line: what it said, on lines of their own, and who
			// can fix it (PPX-D39, PPX-D42).
			for _, l := range notStartedLines(err, sealPacks(f), "  ", strings.TrimPrefix(captureAgain(f.Bin), "then ")) {
				fmt.Fprintln(errw, l)
			}
		case errors.As(err, &exit):
			fmt.Fprintf(errw, "  %s\n", captureJailFailedStep(f.Bin))
		default:
			fmt.Fprintf(errw, "  Fix what it names, %s.\n", captureAgain(f.Bin))
		}
		return 1
	}
	return 0
}

// capturePatchedFork is `yolo capture <bin>` of a PATCHED fork (docs/design/patched-forks.md
// §8.3, PF-D12): the check forced, then the pending candidate built now, ignoring a back-off; with
// none pending, the good build's own inputs rebuilt, as it force-rebuilds a plain fork. Its build
// goes through the swap like any advance's (patchedadvance.go), and it REFUSES on contention, as a
// capture does. On the host only: the fork's mirror, series and record live there.
func capturePatchedFork(f packload.Fork, out, errw io.Writer, color bool) int {
	if config.InJail() {
		kind := "patched fork"
		if f.IsTree() {
			kind = "patched extension"
		}
		fmt.Fprintf(errw, "yolo capture: %s is a %s, checked, replayed and built on the host — "+
			"run `yolo capture %s` there\n", f.Label(), kind, f.CaptureArg())
		return 1
	}
	r := advancePatchedFork(f, advanceOptions{platform: captureJailPlatform(), out: out, errw: errw, color: color,
		force: true})
	if r.built {
		return 0
	}
	if r.delivery.Key == "" && r.delivery.Reason != "" {
		// A reason that names no key (a build jail's stop, which a launch says once for every key that
		// shares it) is named here.
		reason := r.delivery.Reason
		if !strings.HasPrefix(reason, f.Label()) {
			reason = f.Label() + ": " + reason
		}
		fmt.Fprintf(errw, "yolo capture: %s\n", reason)
	}
	return 1
}

// captureTree is `yolo capture <pack>/<name>`: a patched extension's explicit build, as
// capturePatchedFork is a patched fork's. A key no selected pack declares an extension under is
// refused naming the ones that do; with none declared, or a config that cannot be read, it handles
// nothing, and the name is refused as no program's.
func captureTree(key string, out, errw io.Writer, color bool) (int, bool) {
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return 0, false
	}
	var keys []string
	for _, f := range packload.PatchedTrees(sel.packs) {
		if f.Key() == key {
			return capturePatchedFork(f, out, errw, color), true
		}
		keys = append(keys, f.Key())
	}
	if len(keys) == 0 {
		return 0, false
	}
	fmt.Fprintf(errw, "yolo capture: %q names no patched extension your packs declare — they declare %s; "+
		"run `yolo capture <pack>/<name>` with one of them\n", key, strings.Join(keys, ", "))
	return 1, true
}

// captureAgain is the clause most of a capture's stops end their next step with: running it again
// is safe (rule 6), since an admission of identical bytes returns the entry already stored and a
// run appends its receipt.
func captureAgain(bin string) string {
	return "then run `yolo capture " + bin + "` again"
}

// captureWaitStep is the step for a capture or a fork build another process is already running
// for bin: the store it fills is the machine's, so its result is this one's too.
func captureWaitStep(bin string) string {
	return "Wait for it to finish: what it stores serves every launch on this machine. If it fails, " +
		"run `yolo capture " + bin + "` again."
}

// captureJailFailedStep is the step for a capture jail, or a fork's build jail, that exited
// non-zero: the jail's own output, above, is the only thing that knows why.
func captureJailFailedStep(bin string) string {
	return "Its output above says why: fix what it names, " + captureAgain(bin) + "."
}

// captureChoicesStep is the next step for a program a capture cannot take (happy-path rule 7: the
// programs it can are computed, not left for the user to find). When a pack yolo ships installs
// bin with an installer, it is that pack to select; otherwise the programs the selected packs
// install with an installer or build from a fork, which are what a capture takes; or, when there
// are none, how a pack declares one.
func captureChoicesStep(packs []*packload.Pack, bin string) string {
	selected := map[string]bool{}
	for _, p := range packs {
		selected[p.Name] = true
	}
	if bin != "" {
		for _, p := range packload.Embedded() {
			if selected[p.Name] {
				continue
			}
			installs, _ := p.HonoredInstalls()
			for _, in := range installs {
				if in.Bin != bin {
					continue
				}
				switch in.Kind {
				case packdeclNativeKind:
					return fmt.Sprintf("The %s pack yolo ships installs %s: add %q to \"packs\" in %s, "+
						"then: yolo capture %s", p.Name, bin, p.Name, paths.UserConfigPath(), bin)
				case packdeclNPMKind:
					// Not a capture's at all: the pack's launcher installs it from npm on first use,
					// so the way to the program is selecting the pack and launching it. The list
					// below would have named another program to capture.
					return fmt.Sprintf("The %s pack yolo ships installs %s from npm, which a launch "+
						"does itself, so it needs no capture: add %q to \"packs\" in %s, then: "+
						"yolo -- %s", p.Name, bin, p.Name, paths.UserConfigPath(), bin)
				}
			}
		}
	}
	var choices []string
	seen := map[string]bool{}
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if (in.Kind == packdeclNativeKind || in.Kind == packdecl.InstallKindSource) && !seen[in.Bin] {
				seen[in.Bin] = true
				choices = append(choices, in.Bin)
			}
		}
	}
	if len(choices) == 0 {
		// The manifest line itself (rung 3): `yolo pack --help`, which this used to cite, does not
		// document the `program` kind.
		return "None of your packs installs a program with an installer, so nothing here needs a " +
			"capture. A pack declares one in its pack.json as {\"kind\": \"program\", \"bin\": " +
			"\"<name>\", \"via\": \"installer\", \"url\": \"<its install script>\"}."
	}
	sort.Strings(choices)
	return fmt.Sprintf("A capture records what your packs install with an installer or build from a "+
		"fork: %s. Run: yolo capture %s", strings.Join(choices, ", "), choices[0])
}

// forkPinOf reads f's pin from the fork lock — inside a jail as a jail must say it
// (packload.InJailForkPins: nothing there pins, so an unpinned fork is not pinnable and its reason
// names the host).
func forkPinOf(f packload.Fork) packload.ForkPin {
	if config.InJail() {
		return packload.InJailForkPins(packload.LoadForkPins([]packload.Fork{f}, forkLockPath()))[0]
	}
	return packload.LoadForkPins([]packload.Fork{f}, forkLockPath())[0]
}

// captureJailPlatform is the platform a container capture jail on this machine reports: linux, on
// this machine's architecture (run's containerJailPlatform, which the launch hands its triggers).
func captureJailPlatform() string { return "linux/" + goruntime.GOARCH }

// captureTarget is the one install declaration a capture is about.
type captureTarget struct {
	// Bin is the program name.
	Bin string
	// URL is the installer URL, which becomes the receipt's `declared`.
	URL string
	// Pack is the pack that declared it, for the report.
	Pack string
	// Fork is set when bin is a FORK's program (the base's, rewritten with the fork's delivery):
	// its capture is its build, and URL is empty.
	Fork *packload.Fork
	// Install is the declaration itself, for the host capture, which writes the launcher a jail's
	// boot would have (entrypoint.NativeCaptureLauncher).
	Install packdecl.Install
}

// resolveCaptureTarget finds the pack-declared native installer for bin.
//
// THROUGH HonoredInstalls, NEVER THE MANIFEST, so a capture and the launch's auto-capture
// trigger (installerBins) read one accessor. A fetched pack's installerUrl used to be refused
// by an origin gate there; OQ-TP9 (docs/design/trust-paths.md, 2026-09-04) deleted it, so
// HonoredInstalls refuses nothing today and the refusals this function would attach to its
// error are always empty. The plumbing that would attach them is still here.
func resolveCaptureTarget(bin string) (*captureTarget, error) {
	// The one selection function (selectHostPacks, notch-convergence item 6), so a pack the
	// selection closure joins is searched as a launch would deliver it.
	sel := selectConfiguredHostPacks()
	if sel.loadErr != nil {
		return nil, fmt.Errorf("%w\n  Fix what it names in %s, then: yolo capture %s", sel.loadErr,
			paths.UserConfigPath(), bin)
	}
	var refusals, npmBins []string
	// A git pack the pack store does not have (never `yolo pack install`ed), a local one whose
	// directory is gone, one whose manifest has problems (NS-D14: no installer is captured from a
	// manifest every launch refuses), a malformed entry or a refused closure. Named with the
	// reason rather than skipped: "no pack declares <bin>" would be a lie about a config that may
	// well declare it.
	unresolved := sel.problems()
	for _, p := range sel.packs {
		granted, refused := p.HonoredInstalls()
		refusals = append(refusals, refused...)
		for _, in := range granted {
			if in.Bin != bin {
				continue
			}
			if in.Kind == packdecl.InstallKindSource {
				// A FORK's program: its capture is its BUILD, from the pinned source rather than a
				// vendor installer (docs/design/forked-programs-as-packs.md). Never filed with the
				// npm programs below, which would tell the user it "names a registry version".
				for _, f := range packload.Forks(sel.packs) {
					if f.Bin == bin && f.Pack == in.ForkedBy {
						f := f
						return &captureTarget{Bin: bin, Pack: p.Name, Fork: &f}, nil
					}
				}
			}
			if in.Kind != packdeclNativeKind {
				npmBins = append(npmBins, p.Name)
				continue
			}
			return &captureTarget{Bin: bin, URL: in.InstallerURL, Pack: p.Name, Install: in}, nil
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
	// THE NEXT STEP (happy-path rule 1), each on the line after what it fixes: a pack that did not
	// resolve gets its own fix, in the words `yolo check` gives for it, and the refusal ends with
	// the way back to the capture.
	for _, u := range unresolved {
		fmt.Fprintf(&b, "\n  could not be resolved, so not searched: %s: %s", u.Name, u.Reason)
		fmt.Fprintf(&b, "\n    %s", strings.ReplaceAll(captureUnresolvedStep(u), "\n", "\n    "))
	}
	switch {
	case len(npmBins) > 0:
		fmt.Fprintf(&b, "\n  A launch installs it from npm itself, and needs no capture: yolo -- %s", bin)
	case len(unresolved) > 0:
		fmt.Fprintf(&b, "\n  then: yolo capture %s", bin)
	default:
		fmt.Fprintf(&b, "\n  %s", captureChoicesStep(sel.packs, bin))
	}
	return nil, fmt.Errorf("%s", b.String())
}

// captureUnresolvedStep is the fix for one pack a capture could not search, as check-deps names it
// (checkDepsUnresolvedStep), but for the git pack the store does not have yet: a capture does not
// fetch either, and the words for that are its own.
func captureUnresolvedStep(u unresolvedPack) string {
	if u.NeedsInstall {
		return "Run `yolo pack install` to fetch it (a capture never fetches; the next launch fetches it too)"
	}
	return checkDepsUnresolvedStep(u)
}

// packdeclNativeKind is packdecl.Install.Kind for a `via: "installer"` contribution — the
// MIDDLE of the three names for one mechanism (manifest `via:"installer"` → this →
// receipt `kind:"installer"`). Spelled as a named constant here so the switch above reads
// as the same fact the shims.go install switch reads, and so a fourth name is visibly a
// fourth name rather than a bare string that happens not to match.
const packdeclNativeKind = "native"

// packdeclNPMKind is packdecl.Install.Kind for a `via: "npm"` contribution, which a launch installs
// itself and a capture never takes.
const packdeclNPMKind = "npm"

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
//
// --scan-content-refs: THE FULL REFERENCE SCAN, on the container backends too. A jail
// materializes the entry into the /home/agent it was captured in, which needs no scan; the host
// agent floor materializes the SAME entry into its own prefix on the host
// (docs/design/host-tool-provisioning.md OQ-HP3, "one package inside and outside"), a different
// home, which capture.Materialize allows only for a manifest whose full scan found every
// reference it would have to rewrite. The scan reads the delta once, at capture time — an act
// that already downloads and installs the whole thing.
func captureJailArgv(bin string) []string {
	return []string{
		"yolo", "internal", "capture-run",
		"--out=" + path.Join(containerWorkspace, captureOutLeaf),
		"--surface-root=" + paths.WorkspaceHomeState(containerWorkspace),
		"--scan-content-refs",
		"--", "env", entrypoint.InstallOnlyEnv + "=1", bin,
	}
}

// runCaptureJail launches the ordinary run pipeline against the scratch workspace.
//
// The ORDINARY pipeline, deliberately: a capture jail must be the same jail a launch
// produces, or the bytes it records are not the bytes a launch would have installed. The
// only thing this changes is the workspace and the command — argv, captureJailArgv(bin) for an
// installer — and, for a FORK'S BUILD, the seal (docs/design/forked-programs-as-packs.md FP-D9):
// the pipeline withholds every host crossing (run.Options.Sealed) and narrows the pack selection to
// the packs the seal names. seal nil is the installer capture's jail, unchanged: the design scopes
// the seal to the fork route.
//
// ONE WITHHOLDING REACHES BOTH KINDS, sealed or not: the user config's `mise_tools`
// (docs/design/forked-programs-as-packs.md FP-D19). The pipeline reads the suppressed store below
// as this jail's mark (run.Options.CapturesDir returning ""), and hands such a jail none of them,
// on the container arm (run's jailMiseTools) and the macos-user one (macosuser.BuildCapturePlan)
// alike, through one rule (config.JailMiseTools). A pack's `node_floor` still installs.
//
// s is the jail's writers (captureStreams): the launch's own lines go to s.out and s.errw, the
// jail's own, its runtime client's and pid 1's, to s.jailOut and s.jailErr, and its first
// session's stdout to s.sessionOut — the process's own streams when nil; s.jailReady is called
// once its boot is done.
//
// on, at most one, is what the act's caller decided (captureAct): its runtime reaches the pipeline
// through the pipeline's own Getenv seam, as YOLO_RUNTIME would for this one run, and the process
// environment is left alone, so nothing a host launch execs afterwards inherits it. Its jailStdout
// is not read here: a caller names the jail's writers in s.
func runCaptureJail(workspace, bin string, argv []string, seal *captureSeal, s captureStreams, color bool,
	on ...captureAct) int {
	out, errw := s.out, s.errw
	opts := run.NewDefaultOptions()
	opts.Workspace = workspace
	opts.Args = argv
	opts.Color = color
	if len(on) > 0 && on[0].runtime != "" {
		rt := on[0].runtime
		opts.Getenv = func(k string) string {
			if k == "YOLO_RUNTIME" {
				return rt
			}
			return os.Getenv(k)
		}
	}
	if seal != nil {
		opts.Sealed = true
		opts.OnlyPacks = seal.only
		opts.SealedTree = seal.tree
	}
	opts.Stdout, opts.Stderr = out, errw
	// THE JAIL'S OWN LINES AND ITS SESSION'S STDOUT GO WHERE THE CALLER SAID (captureStreams), or to
	// this process's. The run pipeline relays pid 1's output and runs the first session — the
	// installer, the build — on the process's own streams, whatever Stdout it is handed: so without
	// this, a host floor's capture printed the installer's lines on the stdout of the `yolo host`
	// launch it served, ahead of the agent's own.
	opts.JailStdout, opts.JailStderr, opts.OnJailReady = s.jailOut, s.jailErr, s.jailReady
	opts.SessionStdout = s.sessionOut
	jailStdout := s.sessionOut
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
	// Never attach. A capture must run its installer in a home the BOOT just made,
	// and attaching to a live container for this workspace would run it in whatever state
	// that container is in. The scratch workspace is fresh per capture, so there is nothing
	// to attach to in practice; saying so makes it true by construction rather than by luck.
	// (The internal seam that replaced the removed --new flag.)
	opts.NeverAttach = true
	// NO READINESS ACT IN A CAPTURE OR BUILD JAIL (docs/design/jail-notch-readiness.md). This
	// jail's command IS the install: a capture diffs the home across the installer, and a fork's
	// build jail runs the build that produces the program. An act that installed every declared
	// program first left the capture's delta empty ("installer left nothing"), and refused the
	// build jail for the program it had not built yet, since that jail has no build to run.
	opts.NoProgramReadiness = true
	// The scratch workspace carries no yolo-jail.jsonc, so the effective config is the
	// user's — the same `packs` any jail on this machine gets, which is what makes the
	// launcher for <bin> exist inside, though not its `mise_tools` (FP-D19, above). Nothing
	// here is a config a human wrote for this workspace, so there is nothing for a human to
	// approve; granting it up front stops a user-config edit from turning a capture into a
	// prompt about a directory they have never seen.
	opts.AcceptConfigChanges = true
	// NO CONTROLLING TERMINAL IN A CAPTURE JAIL. A capture is machine-driven and
	// nobody is watching for a question, so the jail must not be able to ask one.
	//
	// ⚠ Detaching the driver's stdin is NOT enough, and that is the whole reason
	// this is here. internal/capture's driver already leaves cmd.Stdin nil, so the
	// installer's stdin is /dev/null — and codex's installer still asked
	// "Start Codex now? [y/N]" and waited, because a vendor installer that has to
	// survive `curl | sh` reads /dev/tty rather than stdin. With -t on the container
	// there IS a /dev/tty, so the redirect is bypassed by design.
	//
	// Forcing both predicates false drops podman's -t, which is what removes the
	// pty. An installer's prompt then either takes its default on EOF or fails
	// loudly, and either is better than a capture that blocks forever on a machine
	// with no human — which is what auto-capture does on a fresh host, three times
	// in a row, before the launch the user actually asked for.
	//
	// It also silences the reclaim offer for the same reason (offer.go gates on
	// IsTTYStdout): a sub-launch nobody typed must not interrupt to ask about disk.
	opts.IsTTYStdout = func() bool { return false }
	opts.IsTTYStdin = func() bool { return false }
	// macos-user runs the capture natively, under the narrowed Seatbelt profile slice 6
	// built (macosuser.SeatbeltCaptureProfile): no container, a throwaway staging home on
	// neutral ground, and the shared /Users/_yolojail denied for the duration.
	//
	// RunCaptureAct leaves the proto-entry at the SAME dest the container arm writes, so
	// everything after this call in captureHost — read the manifest, refuse an empty delta,
	// AdmitEntry, receipt — is backend-blind and unchanged.
	//
	// ⚠ WHAT A MAC HAS MEASURED OF THIS, AND WHAT IT HAS NOT. The profile's bytes and both
	// argvs are unit-pinned. The recording half ran on hardware on 2026-09-11 (the header of
	// internal/macosuser/capture.go): `yolo capture claude` loaded this profile, drove the vendor
	// installer and admitted an entry. Still unmeasured on a Mac: that Seatbelt DENIES the shared
	// home during a capture, since a capture that silently wrote to it looks identical to one
	// that did not (install-capture.md slice 6's item 3, a case with no green run recorded); the
	// materialize half; and the host floor's run of a real capture (host-tool-provisioning.md
	// HP-D2).
	//
	// The homeOverlay the pipeline composed is DROPPED here, and that is the capture's
	// choice rather than an oversight: the overlay is skills and briefing prose for an
	// agent to read, and this jail runs one installer in a home it then deletes. See the
	// "" argument in macosuser.BuildCapturePlan, which is where the reasoning lives.
	// `blocked` is NOT dropped — the staging home must carry the same shims a launch
	// would, and core contributes none of them by itself. The guest's jail daemons are
	// DROPPED too: an installer run in a throwaway home is no client of any of them, and a
	// supervisor started for it would bind the launch's ports for nothing.
	opts.MacosUserRun = func(cfg *jsonx.OrderedMap, _ string, _, _ []string,
		repoRoot, packRoot string, _ macosuser.HomeOverlay, _ macosuser.HostContext, dryRun bool,
		packEnv *jsonx.OrderedMap, blocked []packload.BlockedTool, _ macosuser.JailDaemons) int {
		// A PATCHED FORK'S OR A PATCHED EXTENSION'S BUILD DOES NOT RUN ON THIS BACKEND: only a plain
		// fork's does (FP-D24, below), for the Mac's host floor, and a patched one's advance and its
		// record are built for a container's platform.
		if seal != nil && seal.build == "" {
			// Rung 4: no step of the user's makes macos-user build one, so the line names whose it
			// is and what builds and runs one today, a container backend.
			fmt.Fprintln(errw, "yolo capture: a patched fork or a patched extension is built on a container "+
				"backend only — on macos-user only a plain fork's build runs, for yolo's host floor")
			fmt.Fprintf(errw, "  That is yolo's to wire. A podman jail builds and runs it today: "+
				"YOLO_RUNTIME=podman yolo -- %s\n", bin)
			return 1
		}
		deps := macosuser.RealDeps(nil, nil, color)
		deps.Out = out
		// The act's commands — the account's setup, the bootstrap, the driver and the installer it
		// runs — inherit this process's stdout, unless the caller named the jail's (the Mac floor).
		if jailStdout != nil {
			deps.Run = macosuser.RunStdoutTo(jailStdout)
		}
		if seal != nil {
			// A PLAIN FORK'S BUILD, SEALED, FOR THIS MAC (FP-D24): the capture act running the build line
			// in a staging tree keyed by the build, the checkout in workspace/src copied beside its home,
			// under the sealed capture profile, on the darwin floor's tools. It leaves the proto-entry and
			// the toolchain record where a container build jail leaves them, so buildFork's admit, its
			// checks and its receipt are the same for both.
			// Its toolchain rooted at a link of the build's own, in this staging workspace and removed
			// with it, never at the home's profile root a running macos-user session hangs from.
			deps.MaterializeDarwin = materializeDarwinNativeAt(filepath.Join(workspace, forkToolchainRootLeaf))
			return macForkBuildAct(deps, macosuser.ForkBuildOptions{
				CaptureOptions: macosuser.CaptureOptions{Bin: bin, Config: cfg, HostPackRoot: packRoot,
					SandboxEnv: packEnv, BlockedTools: blocked},
				BuildID: seal.id, Build: seal.build, Source: filepath.Join(workspace, forkSourceLeaf),
				RepoRoot: repoRoot, Toolchain: forkBuildToolchainHead(),
			}, filepath.Join(workspace, captureOutLeaf), filepath.Join(workspace, forkToolchainLeaf), dryRun)
		}
		return macCaptureAct(deps, macosuser.CaptureOptions{
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

// macCaptureAct is the macos-user capture act (macosuser.RunCaptureAct) behind a package var, for
// captureRunPipeline's reason: a test drives runCaptureJail's own macos-user closure and reads the
// account runner it composed, without sudo or Seatbelt.
var macCaptureAct = macosuser.RunCaptureAct

// macForkBuildAct is the macos-user fork-build act (macosuser.RunForkBuildAct) behind a package var,
// for macCaptureAct's reason: a test drives the sealed closure and reads the options it composed.
var macForkBuildAct = macosuser.RunForkBuildAct

// forkBuildToolchainHead is what this process knows of a macos-user build's toolchain, the head of
// its record (macosuser.ForkBuildOptions.Toolchain): the yolo that ran it. The act adds the darwin
// floor's store path and the macOS release, where a container build records its image's identity.
func forkBuildToolchainHead() string {
	v := version.Baked()
	if v == "" {
		v = "unstamped"
	}
	return "yolo " + v
}

// forkToolchainRootLeaf is the GC-root link a macos-user fork build's toolchain is rooted at, in the
// build's host staging workspace: a sibling of out/ and the toolchain record, so the admit never
// reads it, and removed with the workspace when the build ends (cleanupCaptureWorkspace).
const forkToolchainRootLeaf = "toolchain-root"

// forkToolchainMaterialize is the darwin floor build a macos-user fork build's toolchain comes from
// (darwinpkg.MaterializeFloorAt), behind a package var so a test reads which link it is rooted at.
var forkToolchainMaterialize = darwinpkg.MaterializeFloorAt

// materializeDarwinNativeAt is the macos-user fork build's macosuser.Deps.MaterializeDarwin: the
// darwin floor and `packages` built with native nix for this machine's system
// (darwinpkg.NativeSystem), the floor every macos-user launch builds, ROOTED AT outLink. Not the
// home's profile root (darwinpkg.ProfileRootLink), which a launch roots its own closure at: the
// build's package list is the sealed capture's, the user scope's alone, so building there would
// unroot a running session's closure whenever its workspace declares `packages:` of its own, and
// `describe`, `check` and `yolo host apply` would report the build's closure as the launch's.
func materializeDarwinNativeAt(outLink string) func(string, []any) (*macosuser.Darwin, bool, error) {
	return func(nixRoot string, packages []any) (*macosuser.Darwin, bool, error) {
		return materializeDarwinWith(nixRoot, packages, outLink)
	}
}

// materializeDarwinWith is materializeDarwinNativeAt's body: the build, then its result as the
// backend's Darwin.
func materializeDarwinWith(nixRoot string, packages []any, outLink string) (*macosuser.Darwin, bool, error) {
	system := darwinpkg.NativeSystem()
	pkgs, err := forkToolchainMaterialize(nixRoot, packages, system, outLink, os.Stderr)
	if err != nil {
		return nil, false, err
	}
	env := jsonx.NewOrderedMap()
	keys := make([]string, 0, len(pkgs.Env))
	for k := range pkgs.Env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env.Set(k, pkgs.Env[k])
	}
	return &macosuser.Darwin{PathPrefix: pkgs.PathPrefix, Env: env, Skipped: pkgs.Skipped, System: system,
		ProfilePath: pkgs.ProfilePath}, true, nil
}

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
	// Every part of the approval record (docs/design/boundary-broker.md BB-D30;
	// workspace-widening.md WW-D11): a path that deletes the record deletes each part, and
	// deleting one alone fails safe. config.ApprovalRecordFiles is the one list of them.
	for _, part := range config.ApprovalRecordFiles(cname) {
		_ = os.Remove(part)
	}
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

// landlockExecVerb is the hidden `yolo internal` verb the host capture confines its installer
// through (capturelandlock_linux.go): it restricts itself with Landlock and execs the capture driver.
const landlockExecVerb = "landlock-exec"

// captureArm is which act one `yolo capture` of an installer program runs (host-tool-provisioning.md
// HP-D18), and when neither can run, why.
type captureArm struct {
	// runtime is the runtime the capture jail boots with: one its caller named for this act, or ""
	// for the one a launch resolves.
	runtime string
	// hostABI is set for the HOST capture: no jail, the installer confined on this host by Landlock at
	// this ABI.
	hostABI int
	// blocked is why no capture jail can boot here ("" when one can, or when the host capture stands
	// in): no container runtime on PATH. missing is that runtime.
	blocked, missing string
	// hostWhy is why the host capture cannot stand in for the missing runtime: set only when nothing
	// selected a runtime, on Linux, the one case it would have.
	hostWhy string
}

// host reports whether this is the host capture.
func (a captureArm) host() bool { return a.hostABI > 0 }

// captureHostGOOS is the platform `yolo capture` chooses its arm for (chooseCaptureArm): this
// machine's. A var so a test can pin the Linux arms, or a Mac's, on whichever machine runs it.
var captureHostGOOS = goruntime.GOOS

// hostConfinementABI is the Landlock ABI a host capture would run under on this machine, or why
// none can (capture.HostConfinementABI). A var so a test can stand in a kernel with Landlock or
// without it.
var hostConfinementABI = capture.HostConfinementABI

// runHostCapture runs the host capture's installer in staging and leaves its proto-entry where a
// capture jail's would be (runLandlockCapture). A var so a test of the arm choice can see which arm
// ran without confining anything.
var runHostCapture = runLandlockCapture

// chooseCaptureArm decides the arm for a capture on goos, under the runtime its caller names ("" for
// none).
//
// A RUNTIME SOMEONE CHOSE BOOTS THE JAIL, as a launch would boot it: the caller's, then YOLO_RUNTIME,
// then the user config's `runtime`. So does the runtime a launch would find, when it is on PATH. Only
// when nothing chose one and none is on PATH does a Linux host take the host capture, and only when
// its kernel can confine it; a runtime the user named and did not install is the reason the capture
// stops, as it is a launch's, never a cue to run the installer some other way.
func chooseCaptureArm(goos, named string) captureArm {
	if named != "" {
		return captureArm{runtime: named}
	}
	if sel := selectedCaptureRuntime(); sel != "" {
		return captureArm{blocked: runtimeAbsent(sel), missing: sel}
	}
	rt := captureRuntime()
	why := runtimeAbsent(rt)
	if why == "" {
		return captureArm{}
	}
	if goos != "linux" {
		return captureArm{blocked: why, missing: rt}
	}
	abi, err := hostConfinementABI()
	if err != nil {
		return captureArm{blocked: why, missing: rt, hostWhy: err.Error()}
	}
	return captureArm{hostABI: abi}
}

// runtimeAbsent says why rt cannot boot a capture jail on this machine: it is not on PATH. "" when
// it is, and for a runtime that is no program (macos-user), which the run pipeline itself checks.
func runtimeAbsent(rt string) string {
	for _, native := range paths.NativeRuntimes {
		if rt == native {
			return ""
		}
	}
	if _, err := exec.LookPath(rt); err != nil {
		return "no container runtime (" + rt + ") is on PATH to run `yolo capture` with"
	}
	return ""
}

// selectedCaptureRuntime is the runtime someone selected for a capture jail: YOLO_RUNTIME, then the
// user config's `runtime`, each only when it names a runtime yolo knows; "" when neither does, which
// leaves the choice to the platform's default (captureRuntime).
func selectedCaptureRuntime() string {
	if env := os.Getenv("YOLO_RUNTIME"); env != "" && knownRuntime(env) {
		return env
	}
	if v, ok := config.UserScopeConfigOrEmpty().Get("runtime"); ok {
		if s, ok := v.(string); ok && knownRuntime(s) {
			return s
		}
	}
	return ""
}

// knownRuntime reports whether rt is a value the `runtime` key may take.
func knownRuntime(rt string) bool {
	for _, known := range paths.AllRuntimes {
		if rt == known {
			return true
		}
	}
	return false
}

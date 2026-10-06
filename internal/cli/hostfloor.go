package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/agentenv"
	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	runtimepkg "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

// hostfloor.go wires the HOST AGENT FLOOR (internal/hostfloor,
// docs/design/host-tool-provisioning.md) into the host verbs: which programs are in it (the
// user-scope selection's `program` contributions), every policy input it reads (`host_floor`,
// `agent_updates`, the capture store and the capture act), and the one question a launch asks of
// it — which binary runs for a bare name.

// floorPrograms is the floor's candidate set for a selection: one Program per bin across the
// packs' install contributions, the first declaration winning, as a jail's launchers do.
func floorPrograms(packs []*packload.Pack) []hostfloor.Program {
	in := make([]hostfloor.PackPrograms, 0, len(packs))
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		in = append(in, hostfloor.PackPrograms{Pack: p.Name, Installs: installs})
	}
	return hostfloor.Programs(in)
}

// newHostFloor is the machine's floor, with every input read from where it lives: the prefix
// under the state dir, this host's platform, the user-scope `host_floor` and `agent_updates`, the
// capture store and the capture act. out receives its progress lines, which are a launch's
// stderr: an agent's stdout is routinely parsed, so nothing here may write to it.
//
// A var so a test can hand a launch a floor whose Node comes from a local server and whose
// captures are a fixture; nothing but a test reassigns it.
var newHostFloor = func(out io.Writer, progs []hostfloor.Program) *hostfloor.Floor {
	store := &capture.Store{Dir: paths.CapturesDir()}
	floorWire, updatesWire := config.HostFloorWire(), config.AgentUpdatesWire()
	pins := floorForkPins(progs)
	f := &hostfloor.Floor{
		Dir:       paths.HostFloorDir(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		NodeFloor: hostfloor.HighestNodeFloor(progs),
		Include:   func(pack string) bool { return entrypoint.PackPolicyAllows(floorWire, pack) },
		UpdatesAllowed: func(pack string) bool {
			return entrypoint.PackPolicyAllows(updatesWire, pack)
		},
		// Either origin (resolveFloorCapture): a capture jail's, or this host's own capture (HP-D18),
		// which no jail ever selects.
		ResolveCapture: func(bin string) (*capture.Entry, error) {
			entry, _, err := resolveFloorCapture(store, bin, capture.Platform())
			return entry, err
		},
		// A fork's build boots a jail, so a machine whose runtime is not installed cannot make one —
		// and a fork with no build in the store then has no floor entry here, rather than an install
		// bound to fail. The build act itself finds the runtime on PATH, so this asks the same
		// question it will: the AMBIENT PATH, never `host_path`, since the build boots a jail and a
		// jail launch finds its runtime on the PATH it was started with (host-agent-environment.md,
		// one resolver: exempt by name). An installer's capture asks CaptureActUnavailable instead.
		CaptureUnavailable: func() string {
			rt := captureRuntime()
			for _, native := range paths.NativeRuntimes {
				if rt == native {
					// No program to find on PATH: macos-user is an account, and it boots no container
					// for a build to run in.
					return "the runtime selected here, " + rt + ", boots no container to run a capture jail in"
				}
			}
			if _, err := exec.LookPath(rt); err != nil {
				return "no container runtime (" + rt + ") is on PATH to run `yolo capture` with"
			}
			return ""
		},
		// A FORK's program (docs/design/forked-programs-as-packs.md FP-D4): the fork lock's pin, read
		// once for this floor, or made by its install (FP-D18, below); the store's build at that pin,
		// by the hit check a jail launch makes;
		// and the build act a jail launch runs on a miss — the sealed capture jail, waiting, bounded,
		// for a build of the same key another launch is running (FP-D1). Never `yolo capture
		// <forked bin>`, which is the explicit REBUILD and refuses on contention.
		ForkPin: func(p hostfloor.Program) (string, string) {
			pin, ok := pins[p.Bin()]
			if !ok {
				return "", "no fork in this selection builds it"
			}
			return pin.Commit, pin.Reason
		},
		// THE LAUNCH'S PIN (FP-D18): a fork the lock does not pin for its declared source is pinned
		// by the install that needs it — `yolo host -- <bin>` or `yolo host apply --assert` — through
		// the one pinner a jail launch uses (run.PinLaunchForks), never by a status.
		ForkPinnable: func(p hostfloor.Program) bool { return pins[p.Bin()].Pinnable },
		PinFork: func(p hostfloor.Program, say func(string)) (string, string) {
			f := floorForkBuild(p, "").Fork
			pin := run.PinLaunchForks([]packload.Fork{f}, func() (func(string), func()) {
				say("fetching " + f.Source + " to pin fork " + f.Key())
				return say, func() {}
			})[0]
			if pin.Pinned {
				say(pin.PinnedLine())
			}
			if pin.Warning != "" {
				say("Warning: " + pin.Warning)
			}
			return pin.Commit, pin.Reason
		},
		// The store's build at that pin and the build act (ResolveBuild, Build) are wired below, by
		// the floor's platform (wireFloorBuild).
		// A PATCHED FORK's program (docs/design/patched-forks.md §9, PF-D14): no pin, so the floor reads
		// the GOOD BUILD where a plain fork's reads the pin — offline, from this machine's check record
		// and capture store — and its install runs the fork's ADVANCE first, the one a fresh jail launch
		// runs (patchedadvance.go), waiting for it as that launch does (PF-D25).
		Patched: floorPatchedState,
		Advance: func(ctx context.Context, p hostfloor.Program, installed *hostfloor.Record) hostfloor.PatchedState {
			floorAdvance(floorForkBuild(p, "").Fork, out, floorServingCopy(installed), actInterruptOf(ctx))
			return floorPatchedState(p)
		},
		Home:   paths.Home(),
		Out:    out,
		Prefix: "yolo host: ",
	}
	wireFloorCapture(f, out)
	wireFloorBuild(f, store, out)
	return f
}

// wireFloorBuild gives f its fork builds — the hit check, the build act, and why the act cannot run —
// by the FLOOR'S platform (f.GOOS, read at each call, the one its dispositions are decided for), as
// wireFloorCapture gives it its captures, since the two platforms build differently:
//
//   - LINUX: the build act a jail launch runs on a miss, in the sealed capture jail of the runtime a
//     launch resolves. Its blocker is that runtime's absence (CaptureUnavailable), asked of the
//     ambient PATH.
//   - A MAC: the macos-user fork-build act (forked-programs-as-packs.md FP-D24), named for this one
//     build whatever `runtime` is configured: a container build here is a Linux build, which no floor
//     on a Mac runs. Its blockers are the act's own refusals, asked first (macBuildBlocked).
//
// Either way the build is filed under this host's platform (floorForkBuild), which is the one its act
// makes: a container jail's on Linux, and on a Mac darwin, as the sandbox account runs it there.
func wireFloorBuild(f *hostfloor.Floor, store *capture.Store, out io.Writer) {
	f.ResolveBuild = func(p hostfloor.Program, commit string) (*capture.Entry, error) {
		b := floorForkBuild(p, commit)
		entry, _, err := resolveForkBuild(store, b.Fork.Bin, b.Platform, b.Fork.Source, commit, b.recipe())
		return entry, err
	}
	f.Build = func(p hostfloor.Program, commit string) (*capture.Entry, error) {
		mode := buildMode{lock: pidlock.Mode{Wait: true, Bound: forkBuildWaitBound}, jailStdout: hostJailStdout()}
		if f.GOOS == "darwin" {
			mode.runtime = "macos-user"
		}
		return buildFork(floorForkBuild(p, commit), mode, out, out, false)
	}
	f.BuildActUnavailable = func(bin, does string) string {
		if f.GOOS == "darwin" {
			return macBuildBlocked(hostFloorMacProbes(), bin, does)
		}
		if f.CaptureUnavailable != nil {
			if why := f.CaptureUnavailable(); why != "" {
				return why + hostfloor.RuntimeStep(does)
			}
		}
		return ""
	}
}

// wireFloorCapture gives f its installer capture — the act, why it cannot run, and how it runs — by
// the FLOOR'S platform (f.GOOS, read at each call, the one its dispositions are decided for), since
// the two platforms capture differently:
//
//   - LINUX: the same `yolo capture <bin>` a human runs and a jail launch's auto-capture calls, with
//     its report on the launch's stderr too, in a capture jail when a runtime is selected or on PATH,
//     and on a machine with neither, on this host under Landlock (HP-D18), which chooseCaptureArm
//     decides inside the act. Its blockers ask the same question the act will, of the AMBIENT PATH.
//   - A MAC: the macos-user capture act (HP-D2) — Seatbelt, a throwaway HOME, the sandbox account —
//     with that runtime handed to the act for this one run (hostFloorCaptureActOn), never through the
//     environment the agent is exec'd with afterwards. A container capture here would record a Linux
//     entry, which no floor on a Mac can run. Its blockers are the act's own refusals, asked first.
func wireFloorCapture(f *hostfloor.Floor, out io.Writer) {
	f.Capture = func(bin string) error {
		var rc int
		if f.GOOS == "darwin" {
			rc = hostFloorCaptureActOn("macos-user", []string{bin}, out, out, false)
		} else {
			rc = hostFloorCaptureAct([]string{bin}, out, out, false)
		}
		if rc != 0 {
			return fmt.Errorf("`yolo capture %s` exited %d", bin, rc)
		}
		return nil
	}
	f.CaptureActUnavailable = func(bin, does string) string {
		if f.GOOS == "darwin" {
			return macCaptureBlocked(hostFloorMacProbes(), bin, does)
		}
		return linuxCaptureBlocked(f.GOOS, does)
	}
	f.CaptureHow = func() string {
		switch {
		case f.GOOS == "darwin":
			return "the macos-user sandbox account runs its installer once, in a throwaway home under " +
				"Seatbelt; sudo may ask for your password"
		case chooseCaptureArm(f.GOOS, "").host():
			return "with no container runtime here, its installer runs once on this host, confined by " +
				"Landlock to a throwaway home; jails capture their own"
		}
		return ""
	}
}

// linuxCaptureBlocked is a Linux floor's CaptureActUnavailable: "" when chooseCaptureArm finds an arm
// that can run, otherwise why not, with the step — for a runtime the user selected and has not
// installed, that runtime; for one nothing selected, that runtime or a kernel with Landlock.
func linuxCaptureBlocked(goos, does string) string {
	arm := chooseCaptureArm(goos, "")
	switch {
	case arm.blocked == "":
		return ""
	case arm.hostWhy != "":
		return arm.blocked + ", and yolo cannot confine its installer on this host instead (" + arm.hostWhy +
			") — install " + arm.missing + " (`yolo check` names how on this machine), or run a kernel with " +
			"Landlock enabled, and the next `yolo host` launch " + does
	}
	return arm.blocked + hostfloor.RuntimeStep(does)
}

// macCaptureProbes is what the floor's Mac capture predicate reads off the machine: the macos-user
// capture act's own refusals (macosuser.RunCapturePlan's order), and whether its sudo can be asked.
type macCaptureProbes struct {
	Geteuid           func() int
	Which             func(name string) bool
	SandboxUserExists func() bool
	// StdinIsTerminal reports whether sudo can ask for a password on this launch's terminal.
	StdinIsTerminal func() bool
	// SudoWithoutPassword reports whether `sudo -n true` succeeds: credentials sudo already holds,
	// or a rule that asks for none. Asked only with no terminal.
	SudoWithoutPassword func() bool
}

// hostFloorMacProbes is the machine's answers. A var so a test can stand a Mac in on any OS.
var hostFloorMacProbes = func() macCaptureProbes {
	d := macosuser.RealDeps(nil, nil, false)
	return macCaptureProbes{
		Geteuid:           d.Geteuid,
		Which:             d.Which,
		SandboxUserExists: d.SandboxUserExists,
		StdinIsTerminal:   func() bool { return tty.IsTerminalFile(os.Stdin) },
		SudoWithoutPassword: func() bool {
			cmd := exec.Command("sudo", "-n", "true")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
			return cmd.Run() == nil
		},
	}
}

// macCaptureBlocked is a Mac floor's CaptureActUnavailable: why the macos-user capture act cannot
// capture bin here now, with its step, or "" when it can. Each refusal is one RunCapturePlan would
// make after the jail's preparation began, asked here first so the floor says it as no floor entry —
// the launch then runs the PATH copy (OQ-HE11) — rather than failing an install.
func macCaptureBlocked(p macCaptureProbes, bin, does string) string {
	return macActBlocked(p, macActWords{act: "a capture", confines: "a capture's installer",
		runsAs: "a capture on a Mac runs its installer as"}, bin, does)
}

// macBuildBlocked is a Mac floor's BuildActUnavailable: why the macos-user fork-build act
// (macosuser.RunForkBuildAct, FP-D24) cannot build bin here now, with its step, or "" when it can —
// the capture act's own refusals, since the build is that act running a build line.
func macBuildBlocked(p macCaptureProbes, bin, does string) string {
	return macActBlocked(p, macActWords{act: "a fork's build", confines: "a fork's build",
		runsAs: "a fork's build on a Mac runs as"}, bin, does)
}

// macActWords is how a refusal of macActBlocked names its act: the act ("a capture"), what Seatbelt
// confines in it, and the clause the sandbox account's sentence runs on.
type macActWords struct{ act, confines, runsAs string }

// macActBlocked is the macos-user capture act's refusals, asked before it runs, in w's words.
func macActBlocked(p macCaptureProbes, w macActWords, bin, does string) string {
	switch {
	case p.Geteuid() == 0:
		return "yolo is running as root, and " + w.act + " on a Mac runs as your own user, asking for sudo " +
			"itself at each step that needs it — run `yolo host` without sudo, and it " + does
	case !p.Which("sandbox-exec"):
		return "sandbox-exec (Apple Seatbelt), which confines " + w.confines + " on a Mac, is not on PATH " +
			"— put /usr/bin back on it, and the next `yolo host` launch " + does
	case !p.SandboxUserExists():
		return "the sandbox account " + macosuser.SandboxUser + ", which " + w.runsAs + ", does not exist " +
			"— run the one-time setup, `yolo macos-setup`, and the next `yolo host` launch " + does
	case !p.StdinIsTerminal() && !p.SudoWithoutPassword():
		return w.act + " on a Mac asks for sudo, and this launch has no terminal to ask on — run " +
			"`YOLO_RUNTIME=macos-user yolo capture " + bin + "` once in a terminal, and every later " +
			"`yolo host` launch uses its result"
	}
	return ""
}

// floorForkBuild is the build a floor program of a fork asks for: the fork as its pack declares it,
// read back off the base's rewritten program — the selection's rewrite (packload.forkedProgram)
// copies the fork's source, build, produces and platforms into it verbatim, and names the fork pack
// in ForkedBy — at commit, for this host's platform, which is the only one the floor runs a build of.
func floorForkBuild(p hostfloor.Program, commit string) forkBuild {
	in := p.Install
	return forkBuild{
		// Patches and Follow too, so a patched fork reads as one to the pinner, which never pins it
		// (packload.PinForks, PF-D16); and the fork pack's root, which its series is read from.
		Fork: packload.Fork{Pack: in.ForkedBy, Base: p.Pack, Bin: in.Bin, Source: in.Source, Build: in.Build,
			Produces: in.Produces, Platforms: in.Platforms, Patches: in.Patches, Follow: in.Follow,
			Root: in.ForkRoot},
		Commit:   commit,
		Platform: capture.Platform(),
	}
}

// floorPatchedPlatform is the platform a patched fork's floor build is of: this host's, as a plain
// fork's floor build is (floorForkBuild), the only one a materialize on the host takes. The floor
// holds a PATCHED fork's build on Linux only (noEntryReason refuses every other host first), where it
// is a capture jail's too, so the floor's lookups and a jail launch's name one build.
func floorPatchedPlatform() string { return capture.Platform() }

// floorAdvance is the floor's advance of a patched fork (hostfloor.Floor.Advance): the fresh
// launch's own (advancePatchedFork) as a LAUNCH — the check throttled, a back-off honored, the wait
// interruptible while a good build serves (PF-D25) — at the host (advanceOptions.host), its lines on
// the launch's stderr, as the floor's are. installed is the floor's own copy that serves, nil for
// none (PF-D55). act is the verb's act interrupt (PF-D57), which the floor's Ensure carried in its
// context (withActInterrupt). It hands nothing: the floor installs the good build the record names
// once it returns. A var so a test can count the floor's advances.
var floorAdvance = func(f packload.Fork, out io.Writer, installed *installedCopy, act *run.ActInterrupt) {
	advancePatchedFork(f, advanceOptions{platform: floorPatchedPlatform(), out: out, errw: out, launch: true, host: true,
		installed: installed, act: act})
}

// actInterruptKey is the context key the verb's act interrupt rides under through the floor's Ensure,
// whose Advance it reaches by the context alone (hostfloor.Floor.Advance).
type actInterruptKey struct{}

// withActInterrupt is ctx carrying act to the floor's Advance (PF-D57); ctx itself for a nil act.
func withActInterrupt(ctx context.Context, act *run.ActInterrupt) context.Context {
	if act == nil {
		return ctx
	}
	return context.WithValue(ctx, actInterruptKey{}, act)
}

// actInterruptOf is the act interrupt ctx carries, nil for none.
func actInterruptOf(ctx context.Context) *run.ActInterrupt {
	act, _ := ctx.Value(actInterruptKey{}).(*run.ActInterrupt)
	return act
}

// floorServingCopy is the floor's installed copy the floor hands its advance as serving
// (hostfloor.Floor.Advance), as the advance reads it: the upstream commit and recipe it is a build
// of, and its label. nil for none.
func floorServingCopy(rec *hostfloor.Record) *installedCopy {
	if rec == nil {
		return nil
	}
	return &installedCopy{commit: rec.Revision, recipe: rec.Recipe, label: rec.Version}
}

// floorPatchedState is the floor's offline read of a patched fork's program (hostfloor.Floor.Patched):
// the recipe its series asks for now, and the good build that serves it — the check record's, or,
// with no record, the one the capture store holds for this recipe (recoverGoodBuild, which writes
// nothing) — with its store entry by the exact lookup. File reads only: no git, no network.
func floorPatchedState(p hostfloor.Program) hostfloor.PatchedState {
	f := floorForkBuild(p, "").Fork
	series, err := f.ReadSeries()
	if err != nil {
		return hostfloor.PatchedState{Reason: fmt.Sprintf("%v — correct it, and the next `yolo host -- %s` builds it",
			err, f.Bin)}
	}
	recipe := forkBuild{Fork: f, Series: series}.recipe()
	ps := hostfloor.PatchedState{Recipe: recipe}
	platform := floorPatchedPlatform()
	store := &capture.Store{Dir: paths.CapturesDir()}
	var g *packsrc.GoodBuild
	lookup := recipe // the recipe the store entry's receipt names, for the exact lookup below
	if rec, err := run.LoadPatchedRecord(patchedForkStore(), f, series); err == nil && rec.Good != nil {
		g = rec.Good
	} else if g = recoverGoodBuild(store, f.Key(), platform, recipe, series.Len()); g == nil &&
		series.LegacyDigest != "" && series.LegacyDigest != series.Digest {
		// A BUILD RECEIPTED UNDER THE SERIES' LEGACY DIGEST is a build of these files (PF-D62): it
		// serves, found under the recipe its receipt names. This read writes nothing; the next advance
		// re-keys it, as its own recovery does.
		legacy := run.PatchedRecipe(f, series.LegacyDigest)
		if g = recoverGoodBuild(store, f.Key(), platform, legacy, series.Len()); g != nil {
			g.Series, g.Recipe, lookup = series.Digest, recipe, legacy
		}
	}
	switch {
	case g == nil:
		ps.Reason = "fork " + f.Key() + " has no build on this machine yet"
		return ps
	case g.Recipe != recipe:
		// THE USER'S EDIT (PF-D23): nothing serves until the edited series or recipe builds.
		ps.Reason = "fork " + f.Key() + "'s patch series or build recipe changed since its good build " +
			run.GoodBuildLabel(g) + " — reverting the edit brings that build back"
		return ps
	}
	ps.Good = &hostfloor.PatchedBuild{Commit: g.Commit, Recipe: g.Recipe,
		Label: run.GoodBuildLabel(g) + " + " + run.PatchCount(g.Patches)}
	if e, _, err := resolvePatchedBuild(store, f.Key(), f.Bin, platform, patchedBuildSource(f.Source), g.Commit,
		lookup); err == nil {
		ps.Good.Entry = e
	}
	return ps
}

// floorForkPins reads the fork lock once for every source-built program among progs, keyed by bin,
// through the reader every caller that must not fetch uses (packload.LoadForkPins), so the host and
// a jail ask for one commit and a lock that cannot be read pins nothing for either. A pin the
// floor's install makes goes through the jail launch's own pinner (PinFork, run.PinLaunchForks).
func floorForkPins(progs []hostfloor.Program) map[string]packload.ForkPin {
	var forks []packload.Fork
	for _, p := range progs {
		if p.Install.Kind == packdecl.InstallKindSource {
			forks = append(forks, floorForkBuild(p, "").Fork)
		}
	}
	pins := packload.LoadForkPins(forks, forkLockPath())
	out := make(map[string]packload.ForkPin, len(pins))
	for _, pin := range pins {
		out[pin.Fork.Bin] = pin
	}
	return out
}

// hostFloorCaptureAct is the capture act the production floor runs on Linux: `yolo capture <bin>`
// itself, its capture jail's own stdout on this process's stderr (hostJailStdout). A var only so a
// test can stand in for the jail it boots and still drive the floor's own Capture wiring; nothing
// but a test reassigns it.
var hostFloorCaptureAct = func(args []string, out, errw io.Writer, color bool) int {
	return captureHostWith(args, out, errw, color, captureAct{jailStdout: hostJailStdout()})
}

// hostFloorCaptureActOn is the capture act under a runtime its caller names for this act alone
// (captureAct.runtime): the Mac floor's, on macos-user (HP-D2), its jail's stdout on this process's
// stderr as hostFloorCaptureAct's is. A var for hostFloorCaptureAct's reason.
var hostFloorCaptureActOn = func(rt string, args []string, out, errw io.Writer, color bool) int {
	return captureHostWith(args, out, errw, color, captureAct{runtime: rt, jailStdout: hostJailStdout()})
}

// hostJailStdout is where a jail a host verb boots writes its OWN stdout (captureAct.jailStdout):
// the host floor's capture jail and build jail, and the host's advances' build jails. It is this
// process's stderr, where that jail's stderr already goes: the verb serves a `yolo host` launch that
// execs an agent whose stdout is routinely parsed (newHostFloor), and the run pipeline would
// otherwise relay the jail's stdout to the process's own, ahead of the agent's output. The raw
// stream, never the launch's teed errw: the jail's output is not yolo's own lines, and the host
// launch log takes only those (startHostLaunchTrace).
func hostJailStdout() io.Writer { return os.Stderr }

// captureRuntime is the runtime a `yolo capture` would boot its jail with: YOLO_RUNTIME, then the
// user config's `runtime`, then the platform default — run's precedence without its probe.
func captureRuntime() string {
	cfgRT := ""
	if v, ok := config.UserScopeConfigOrEmpty().Get("runtime"); ok {
		if s, ok := v.(string); ok {
			cfgRT = s
		}
	}
	return runtimepkg.ResolveRuntime(os.Getenv("YOLO_RUNTIME"), cfgRT, paths.IsMacOS, func(bin string) bool {
		_, err := exec.LookPath(bin)
		return err == nil
	})
}

// hostFloorBinDir is the floor's bin/, the directory a host launch appends last to its child's
// PATH (HE-D1).
func hostFloorBinDir() string { return (&hostfloor.Floor{Dir: paths.HostFloorDir()}).BinDir() }

// floorDeliveredBins is every program of packs the floor holds or can provision on this machine
// — the programs those packs DELIVER (host-agent-environment.md's launch PATH terms) — with its status, keyed by
// bin. A dependency probe answers these from the floor rather than from any PATH
// (host-agent-environment.md, one resolver). It reads the prefix and nothing else, and it is empty in a jail,
// whose own launchers answer for its programs.
func floorDeliveredBins(packs []*packload.Pack) map[string]hostfloor.Status {
	out := map[string]hostfloor.Status{}
	if config.InJail() {
		return out
	}
	progs := floorPrograms(packs)
	if len(progs) == 0 {
		return out
	}
	floor := newHostFloor(io.Discard, progs)
	for _, p := range progs {
		if st := floor.Status(p); st.Disposition != hostfloor.NoEntry {
			out[p.Bin()] = st
		}
	}
	return out
}

// floorDepClause is what a dependency line says about a program the floor answers for.
func floorDepClause(st hostfloor.Status) string {
	if st.Disposition == hostfloor.Provisioned {
		return "yolo's floor copy " + st.Record.Version + " at " + homeTilde(st.Launcher)
	}
	if st.Newer {
		// No install of this yolo's replaces a newer yolo's record (Ensure refuses), so the
		// clause is that refusal, whose next step is `yolo update`.
		return "yolo's floor will not install it: " + richtext.Escape(st.Reason)
	}
	return "yolo's floor installs it (`yolo host apply --assert`, or the first `yolo host -- " +
		st.Program.Bin() + "`)"
}

// floorDepMark is the mark a dependency line gives a program the floor answers for: a pass,
// except for a newer yolo's record, which nothing here installs (floorDepClause says why).
func floorDepMark(st hostfloor.Status) string {
	if st.Newer {
		return "[yellow]![/yellow]"
	}
	return "[green]✓[/green]"
}

// applyHostFloor is `yolo host apply`'s floor stage: every program the selected packs declare,
// by disposition, and — under --assert — the same provisioning a launch does (Ensure: install what
// is missing, reinstall what moved, the throttled refresh), then the removal of every entry no
// selected pack delivers any more (§4, "Deselection"). The dry run says what an --assert would do
// and changes nothing.
//
// complete is whether this run resolved every configured pack: a pack that did not resolve would
// otherwise look deselected, and its agent would be deleted over a network blip, so removal waits
// for a run that can see the whole selection.
//
// Never reached from the launch gate's apply (hostApplySurvey.floorStage): a launch installs the
// one agent it starts, and removes nothing.
func applyHostFloor(pr richtext.Printer, out io.Writer, packs []*packload.Pack, write, complete bool,
	survey *hostApplySurvey) int {
	if config.InJail() {
		return 0
	}
	progs := floorPrograms(packs)
	floor := newHostFloor(out, progs)
	floor.Prefix = "    "
	if survey != nil && survey.advanceDeferred != "" {
		// THE ACT BUILDS NONE (PF-D12, PF-D56): the floor installs a good build already admitted, and a
		// patched fork with none says which act builds it.
		floor.Advance, floor.NoAdvance = nil, survey.advanceDeferred
	}
	rc := 0
	// note records a row for the machine document (hostApplyDoc.HostFloor), which only the dry
	// run emits, so the dry run's rows are the ones it carries.
	note := func(row hostApplyDocFloorEntry) {
		if survey != nil && !write {
			survey.floor = append(survey.floor, row)
		}
	}
	for _, p := range progs {
		st := floor.Status(p)
		row := hostApplyDocFloorEntry{Bin: p.Bin(), Pack: p.Pack, Disposition: string(st.Disposition),
			Action: "none", Reason: st.Reason, Launcher: st.Launcher}
		switch {
		case st.Disposition == hostfloor.NoEntry && floor.NoAdvance != "" && strings.Contains(st.Reason, floor.NoAdvance):
			// A patched fork this act builds none of (PF-D56): not the floor's NoEntry, whose launch
			// runs the copy on the PATH, since a `yolo host` launch of it builds it.
			pr.Printf("  [cyan]%-20s[/cyan] %s: not installed yet — %s", "host_floor", p.Bin(), st.Reason)
			note(row)
			continue
		case st.Disposition == hostfloor.NoEntry:
			pr.Printf("  [cyan]%-20s[/cyan] %s: no floor entry — %s; `yolo host -- %s` runs the one on "+
				"your PATH", "host_floor", p.Bin(), st.Reason, p.Bin())
			note(row)
			continue
		case !write && st.Newer:
			// A newer yolo's record, which the --assert refuses to install over (Ensure): said
			// as that, with the refusal's own next step, never as an install.
			pr.Printf("  [cyan]%-20s[/cyan] %s: will not install it: %s", "host_floor", p.Bin(),
				richtext.Escape(st.Reason))
			row.Action = "would refuse"
			note(row)
			survey.noteFloorRefused(p.Bin()) // the verdict names it (hostApplyOutcome)
			continue
		case !write && st.Disposition == hostfloor.Provisioned && st.Pending == "":
			detail(pr, "  [cyan]%-20s[/cyan] %s %s  [dim]%s[/dim]", "host_floor", p.Bin(),
				st.Record.Version, homeTilde(st.Launcher))
			row.Version = st.Record.Version
			note(row)
			continue
		case !write:
			why := st.Reason
			if st.Pending != "" {
				why = st.Pending
			}
			pr.Printf("  [cyan]%-20s[/cyan] %s: would install (%s)  [dim]%s[/dim]", "host_floor",
				p.Bin(), why, homeTilde(st.Launcher))
			row.Action, row.Reason = "would install", why
			if st.Record != nil {
				row.Version = st.Record.Version
			}
			note(row)
			continue
		}
		after, outcome, err := floor.Ensure(withActInterrupt(context.Background(), survey.actInterrupt()), p)
		if errors.Is(err, hostfloor.ErrNewerRecord) {
			// Refused, not failed, in the dry run's words: a newer yolo's record, which Ensure
			// does not install over. The run still does not complete, so it exits 1.
			pr.Printf("  [red]%-20s %s: will not install it: %s[/red]", "host_floor", p.Bin(),
				richtext.Escape(err.Error()))
			survey.noteFloorRefused(p.Bin()) // the verdict names it (hostApplyOutcome)
			rc = 1
			continue
		}
		if err != nil {
			pr.Printf("  [red]%-20s %s: could not install it: %v[/red]", "host_floor", p.Bin(), err)
			survey.noteFloorFailed(p.Bin()) // the verdict names it (hostApplyOutcome)
			rc = 1
			continue
		}
		line := fmt.Sprintf("  [cyan]%-20s[/cyan] %s %s, %s  [dim]%s[/dim]", "host_floor", p.Bin(),
			after.Record.Version, outcome, homeTilde(after.Launcher))
		if outcome == hostfloor.Current {
			detail(pr, "%s", line)
		} else {
			pr.Printf("%s", line)
		}
	}
	if !complete {
		return rc
	}
	for _, r := range floor.Reconcile(progs, write) {
		verb := "would remove"
		if write {
			verb = "removed"
		}
		pr.Printf("  [cyan]%-20s[/cyan] %s: %s (%s)", "host_floor", r.Bin, verb, r.Why)
		note(hostApplyDocFloorEntry{Bin: r.Bin, Disposition: "deselected", Action: verb, Reason: r.Why})
	}
	return rc
}

// floorProgram finds bin among progs.
func floorProgram(progs []hostfloor.Program, bin string) (hostfloor.Program, bool) {
	for _, p := range progs {
		if p.Bin() == bin {
			return p, true
		}
	}
	return hostfloor.Program{}, false
}

// hostTargetOrigin is where the binary a host launch execs came from — the fact the launch's
// "starting" line names, so a slow startup is visibly the agent's and not yolo's.
type hostTargetOrigin int

const (
	// originFloor: yolo's floor copy of a program a selected pack delivers (HP-DIR4).
	originFloor hostTargetOrigin = iota
	// originPath: looked up on this launch's PATH — a program no selected pack delivers, or one
	// the floor cannot hold here (OQ-HE11, until it is ruled).
	originPath
	// originGiven: the target was given as a path and is exec'd as given.
	originGiven
)

// hostTarget is what a host launch execs.
type hostTarget struct {
	Path   string
	Origin hostTargetOrigin
	// Part is which part of the child's launch PATH an originPath target was found on — the PATH
	// yolo was started with, a `host_path` folder, or the stand-in for a launch started with none —
	// so the starting line names the real source rather than calling every hit "your PATH".
	Part hostpath.Part
}

// resolveHostLaunchTarget decides which binary `yolo host -- <cmd0>` runs, by the three exec rules
// of host-agent-environment.md, which copy runs (HP-DIR4, OQ-HE10):
//
//   - a BARE NAME of a program a selected pack delivers runs the FLOOR's copy, by path, whatever
//     the caller's PATH holds — installed first when missing (a launch installs what it needs,
//     HP-D3, with progress lines: a launch has no quiet mode);
//   - a target GIVEN AS A PATH is exec'd as given;
//   - anything else is looked up on the child's PATH, as the user's shell would find it — and so
//     is a selected pack's program the floor cannot hold on this machine, with one line saying
//     so, which keeps today's behavior while OQ-HE11 is open.
//
// That lookup is the launch PATH's (lp, host-agent-environment.md, the launch PATH: the PATH yolo was started
// with, then `host_path`'s folders not already on it) through its one lookup (HE-D5), as the
// child searches it (hostChildLaunch) — the child's PATH less the floor's bin/, which it ends with
// (HE-D1). Every name in bin/ is a floor entry, and one a selected pack delivers never reaches the
// lookup (it runs by path), so a hit there could only be a DESELECTED ENTRY: one a deselected pack
// — or one `host_floor` now leaves out — left behind until the next `yolo host apply --assert`
// removes it (§4, "Deselection"). yolo host runs its floor copy of what a selected pack delivers
// and of nothing else (HP-DIR4), so a deselected entry is never run, in either case, and a launch
// that finds nothing else says what it is and what removes it.
//
// A bare name the lookup misses prints the MISS LINE (§4.2, HE-D2) in place of the lookup's own
// error: the whole PATH searched, `host_path`'s part marked, and the `host_path` fix.
//
// The second return is the exit code of a launch this refuses (127: the program is not
// available), 0 otherwise. In a jail there is no floor: the jail's own launchers are on PATH.
func resolveHostLaunchTarget(packs []*packload.Pack, cmd0 string, lp *hostpath.Launch, errw io.Writer,
	act *run.ActInterrupt) (hostTarget, int) {
	floorBin := hostFloorBinDir()
	child := hostChildLaunch(lp)
	onPath := func() (hostTarget, int) {
		target, err := child.LookPathSkipping(cmd0, floorBin)
		if err != nil {
			line := ""
			if !strings.ContainsRune(cmd0, os.PathSeparator) {
				miss := declarersOf(packs)[cmd0].miss(cmd0, true)
				miss.Skipping = []string{floorBin}
				line = child.MissLine(miss)
			}
			if line == "" {
				line = err.Error()
			}
			fmt.Fprintf(errw, "yolo host: %s\n", line)
			if !config.InJail() && !strings.ContainsRune(cmd0, os.PathSeparator) {
				if _, lerr := os.Lstat(filepath.Join(floorBin, cmd0)); lerr == nil {
					fmt.Fprintf(errw, "yolo host: yolo's floor still holds a copy of %s that it no longer "+
						"keeps (no selected pack delivers it here); yolo host does not run it, and "+
						"`yolo host apply --assert` removes it\n", cmd0)
				}
			}
			return hostTarget{}, 127
		}
		if strings.ContainsRune(cmd0, os.PathSeparator) {
			return hostTarget{Path: target, Origin: originGiven}, 0
		}
		return hostTarget{Path: target, Origin: originPath, Part: child.PartOf(filepath.Dir(target))}, 0
	}
	if config.InJail() || strings.ContainsRune(cmd0, os.PathSeparator) || !selectedPackInstalls(packs, cmd0) {
		return onPath()
	}
	progs := floorPrograms(packs)
	prog, ok := floorProgram(progs, cmd0)
	if !ok {
		return onPath()
	}
	floor := newHostFloor(errw, progs)
	st, _, err := floor.Ensure(withActInterrupt(context.Background(), act), prog)
	if err == nil || errors.Is(err, hostfloor.ErrNoEntry) {
		// The agent starts: so do the MCP servers its config names (HC-D28).
		ensureMCPPrograms(packs, progs, floor, cmd0, errw, act)
	}
	switch {
	case errors.Is(err, hostfloor.ErrNoEntry):
		// OQ-HE11 is open: keep today's behavior — the launch's PATH — and say, once, that the
		// copy about to run is not yolo's.
		fmt.Fprintf(errw, "yolo host: yolo has no copy of %s %s (%s); looking for it on your PATH\n",
			cmd0, noCopyWhere(floor.GOOS, prog), st.Reason)
		// The same lookup as any other name's, the floor's own bin/ skipped: an entry left there
		// from before `host_floor` left the pack out is a deselected entry, not what "no copy" may
		// run.
		return onPath()
	case errors.Is(err, hostfloor.ErrNewerRecord):
		// Refused, not failed: the floor holds a newer yolo's copy, and the refusal names the
		// update that runs it.
		fmt.Fprintf(errw, "yolo host: will not install %s over a newer yolo's copy in yolo's floor: %v\n",
			cmd0, err)
		return hostTarget{}, 127
	case err != nil:
		fmt.Fprintf(errw, "yolo host: could not install %s into yolo's floor: %v\n", cmd0, err)
		return hostTarget{}, 127
	}
	if prog.Install.IsPatchedFork() && st.Record != nil {
		// A PATCHED FORK'S LINE (docs/design/patched-forks.md §7, PF-D11, PF-D53), the one a jail
		// launch's fork block prints, of the build the FLOOR runs: the series, that build, and the held
		// suffix while something holds the newest upstream back — or, when the floor runs another build
		// than the good build, both. A disclosure, so on every launch (OQ-RO3).
		line, _ := run.PatchedForkLine(floorForkBuild(prog, "").Fork, run.FloorCopy{Commit: st.Record.Revision,
			Recipe: st.Record.Recipe, Label: st.Record.Version}, "the next `yolo host -- "+cmd0+"`")
		fmt.Fprintf(errw, "yolo host: %s\n", line)
	}
	return hostTarget{Path: st.Launcher, Origin: originFloor}, 0
}

// ensureMCPPrograms is decision HC-D28 (docs/design/host-computed-layer.md): a `yolo host --
// <agent>` launch also makes sure yolo's floor holds every program a selected pack's MCP server
// runs (the `mcp` contribution's `bin`), since the agent starts that server whenever it starts
// and the host has no lazy launcher to install it on first use, as a jail does. Only a server the
// host's table carries counts (hostMCPPacks: a fetched pack's is not written there), and never
// cmd0, which the caller has just ensured. Nothing here refuses the launch: a program the floor
// cannot hold, or an install that fails, is one line naming what then happens and the next step,
// and the agent starts without that server rather than not at all — a missing MCP server is no
// reason to refuse an agent (mcp-presets-removal.md §9.1).
func ensureMCPPrograms(packs []*packload.Pack, progs []hostfloor.Program, floor *hostfloor.Floor,
	cmd0 string, errw io.Writer, act *run.ActInterrupt) {
	composed, _ := hostMCPPacks(packs)
	seen := map[string]bool{cmd0: true}
	for _, held := range packload.HeldMCPServers(composed) {
		bin, server := held.Server.Bin, held.Server.Name
		if bin == "" || seen[bin] {
			continue
		}
		seen[bin] = true
		prog, ok := floorProgram(progs, bin)
		if !ok {
			fmt.Fprintf(errw, "yolo host: MCP server %s runs %s, which no selected pack installs; it "+
				"starts only if %s is on the agent's PATH — select the pack that ships it in `packs`\n",
				server, bin, bin)
			continue
		}
		st, _, err := floor.Ensure(withActInterrupt(context.Background(), act), prog)
		switch {
		case errors.Is(err, hostfloor.ErrNoEntry):
			fmt.Fprintf(errw, "yolo host: yolo has no copy of %s %s (%s), which MCP server %s runs; "+
				"the server looks for it on the agent's PATH\n", bin, noCopyWhere(floor.GOOS, prog),
				st.Reason, server)
		case err != nil:
			fmt.Fprintf(errw, "yolo host: could not install %s, which MCP server %s runs, into yolo's "+
				"floor: %v — the agent starts without that server; `yolo host apply --assert` "+
				"installs it\n", bin, server, err)
		}
	}
}

// noCopyWhere names the machine the no-copy line is about. goos is the floor's own platform
// (Floor.GOOS), the one its disposition was decided for. The reason after it says why this machine
// holds no copy and, where something ends that, what does: none is only a matter of time.
func noCopyWhere(goos string, _ hostfloor.Program) string {
	if goos == "darwin" {
		return "on this Mac"
	}
	return "on this machine"
}

// hostStartingLine is the one line `yolo host` prints just before it hands over to the command:
// what it starts and where that binary came from. It exists so a slow agent startup is visibly
// the agent's and not yolo's — and it names the copy, so running the floor's rather than your own
// (HP-DIR4) is never a surprise. A launch has no quiet mode (OQ-RO3), so it is unconditional.
func hostStartingLine(cmd0 string, t hostTarget) string {
	var where string
	switch t.Origin {
	case originFloor:
		where = "yolo's floor copy"
	case originGiven:
		where = "as given"
	case originPath:
		switch t.Part {
		case hostpath.PartHostPath:
			where = "from host_path"
		case hostpath.PartStandIn:
			where = "from " + hostpath.StandInPhrase
		default:
			where = "from your PATH"
		}
	}
	return fmt.Sprintf("yolo host: starting %s (%s, %s)", cmd0, where, homeTilde(t.Path))
}

// homeTilde writes a path under the home as ~/…, for a line a person reads.
func homeTilde(p string) string {
	home := paths.Home()
	if home != "" && home != "/" && strings.HasPrefix(p, home+string(os.PathSeparator)) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// hostChildPath is the PATH a host launch hands its child (OQ-HE10, ruled (c), and HE-D1): the
// launch PATH — the caller's own PATH, then each `host_path` folder not already on it (HE-D3) —
// then the floor's bin/, duplicates removed with the first kept. The caller's PATH comes first
// because the commands a host agent runs see the user's own environment, mise included (OQ-HP7);
// `host_path`'s folders follow it, so they fill in for a launcher that lacks them and never shadow
// what the caller's PATH finds; the floor's bin/ comes last because it holds agent names only, so
// it supplies one only where nothing of the user's has it.
//
// A caller that passed NO PATH (`env -i`) gets the system baseline (hostfloor.BaselinePath) in
// its place, ahead of `host_path`'s folders and the floor's bin/ (hostChildLaunch, HP-D12).
// Handing that child the floor's bin/ alone would leave every command the agent runs by name —
// git, sh — unfound, where before the floor a child with no PATH at least had libc's default
// search path (/bin:/usr/bin).
func hostChildPath(lp *hostpath.Launch, floorBin string) string {
	var out []string
	seen := map[string]bool{}
	for _, d := range append(hostChildLaunch(lp).Entries(), floorBin) {
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, d)
	}
	return strings.Join(out, string(os.PathListSeparator))
}

// hostRefreshProgram is the program whose PRE-LAUNCH REFRESH (packdecl.Refresh, a coined term) a
// `yolo host -- <cmd0>` runs before its exec, and the selection's floor programs it was found among:
// the selected packs' declaration of cmd0's base name, the first one winning as the floor's and the
// jail launcher generator's do, when that declaration has a refresh. Whichever copy the launch
// execs, so a target given as a path or found on the launch PATH is refreshed too
// (docs/design/host-tool-provisioning.md HP-D19). Never in a jail, whose own launcher on PATH runs
// the refresh (internal/entrypoint's prelaunchrefresh.go) and whose state this would not be.
func hostRefreshProgram(packs []*packload.Pack, cmd0 string) (hostfloor.Program, []hostfloor.Program, bool) {
	if config.InJail() {
		return hostfloor.Program{}, nil, false
	}
	progs := floorPrograms(packs)
	prog, ok := floorProgram(progs, filepath.Base(cmd0))
	if !ok || prog.Install.Refresh == nil {
		return hostfloor.Program{}, nil, false
	}
	return prog, progs, true
}

// hostPrelaunchRefresh runs prog's pre-launch refresh against target, the binary the launch is about
// to exec, through the machine's floor (Floor.PrelaunchRefresh: its stamp, its lock and the
// `agent_updates` policy), in the environment refreshEnviron builds, its lines and its output on
// errw. It returns 0 to go on — a refresh that failed, timed out or was interrupted with Ctrl-C
// included, since launching the program outranks refreshing it — and the status to end the launch
// with when a SIGTERM or SIGHUP stopped the refresh, as the jail's launcher ends.
func hostPrelaunchRefresh(launch *hostComposition, prog hostfloor.Program, progs []hostfloor.Program,
	target, childPath string, errw io.Writer) int {
	res := newHostFloor(errw, progs).PrelaunchRefresh(prog, target, launch.refreshEnviron(childPath))
	if res.Outcome != hostfloor.RefreshStopped {
		return 0
	}
	name := res.Signal.String()
	switch res.Signal {
	case syscall.SIGTERM:
		name = "SIGTERM"
	case syscall.SIGHUP:
		name = "SIGHUP"
	}
	fmt.Fprintf(errw, "yolo host: %s: the pre-launch refresh was stopped by %s, so the launch ends here "+
		"without starting %s; run it again to start %s\n", prog.Bin(), name, prog.Bin(), prog.Bin())
	return 128 + int(res.Signal)
}

// refreshEnviron is the environment a launch's pre-launch refresh runs in: the one this process
// inherited, with the part of the program's own composition that EVERY process of the launch
// receives applied over it, then the child's PATH. That part is each entry whose winner is the
// credential gate's shared composition's too (packload.CredentialScope.SharedEnv): the ungated pack
// env, such as pi's PI_TELEMETRY=0, the env_sources values no provider claims, such as a proxy, a
// CA bundle or a registry, and the env_sources removals. It is what a jail's refresh holds. The
// jail's shared file reaches every process there, the launcher's refresh included; an entry whose
// winner for the agent differs from the shared one (its provider's claimed credentials, its
// profile-gated pack env, its env derive's shape vars and their tombstones) is in the agent's own
// file instead (internal/cli/run's agentEnvFileContent, by the same comparison), which the launcher
// sources after the refresh, because the refresh needs no credential and should run holding none
// (internal/entrypoint's agentenv.go). So a credential the composition scopes to the program
// reaches the program alone, and the refresh holds no value of yolo's the program does not: what the
// program's environment keeps out (a service-only doorway pointer, a wire table under a composed
// name) is not in its vars to begin with. The PATH is the child's before the blocked tools join it:
// the jail runs its refresh with the blockers bypassed (YOLO_BYPASS_SHIMS=1), as it does every
// installer.
func (c *hostComposition) refreshEnviron(childPath string) []string {
	shared := c.scope.SharedEnv()
	var everyProcess []agentenv.Var
	for _, v := range c.vars {
		if s, ok := shared.Lookup(v.Key); ok && s.Unset == v.Unset && s.Value == v.Value {
			everyProcess = append(everyProcess, v)
		}
	}
	env := agentenv.Apply(os.Environ(), everyProcess)
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+childPath)
}

// childEnviron is environ() with the child's PATH overlaid LAST — after every pack env, profile
// and removal, so none of them can replace it (host-agent-environment.md, which copy runs). In a jail there is
// no floor, and the environment is left as it is.
func (c *hostComposition) childEnviron(childPath string) []string {
	env := c.environ()
	if config.InJail() {
		return env
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if !strings.HasPrefix(kv, "PATH=") {
			out = append(out, kv)
		}
	}
	return append(out, "PATH="+childPath)
}

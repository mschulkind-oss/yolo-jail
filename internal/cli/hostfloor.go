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

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/hostpath"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/pidlock"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	runtimepkg "github.com/mschulkind-oss/yolo-jail/internal/runtime"
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
	return &hostfloor.Floor{
		Dir:       paths.HostFloorDir(),
		GOOS:      runtime.GOOS,
		GOARCH:    runtime.GOARCH,
		NodeFloor: hostfloor.HighestNodeFloor(progs),
		Include:   func(pack string) bool { return entrypoint.PackPolicyAllows(floorWire, pack) },
		UpdatesAllowed: func(pack string) bool {
			return entrypoint.PackPolicyAllows(updatesWire, pack)
		},
		ResolveCapture: func(bin string) (*capture.Entry, error) {
			entry, _, err := resolveCaptureFor(store, bin, capture.Platform())
			return entry, err
		},
		// The capture act itself — the same `yolo capture <bin>` a human runs and a jail
		// launch's auto-capture calls — with its report on the launch's stderr too.
		Capture: func(bin string) error {
			if rc := hostFloorCaptureAct([]string{bin}, out, out, false); rc != 0 {
				return fmt.Errorf("`yolo capture %s` exited %d", bin, rc)
			}
			return nil
		},
		// A capture boots a jail, so a machine whose runtime is not installed cannot make one —
		// and an installer program with no capture in the store then has no floor entry here,
		// rather than an install bound to fail. The capture act itself finds the runtime on PATH,
		// so this asks the same question it will: the AMBIENT PATH, never `host_path`, since the
		// capture boots a jail and a jail launch finds its runtime on the PATH it was started with
		// (host-agent-environment.md, one resolver: exempt by name).
		CaptureUnavailable: func() string {
			rt := captureRuntime()
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
		ResolveBuild: func(p hostfloor.Program, commit string) (*capture.Entry, error) {
			b := floorForkBuild(p, commit)
			entry, _, err := resolveForkBuild(store, b.Fork.Bin, b.Platform, b.Fork.Source, commit, b.recipe())
			return entry, err
		},
		Build: func(p hostfloor.Program, commit string) (*capture.Entry, error) {
			return buildFork(floorForkBuild(p, commit),
				buildMode{lock: pidlock.Mode{Wait: true, Bound: forkBuildWaitBound}}, out, out, false)
		},
		// A PATCHED FORK's program (docs/design/patched-forks.md §9, PF-D14): no pin, so the floor reads
		// the GOOD BUILD where a plain fork's reads the pin — offline, from this machine's check record
		// and capture store — and its install runs the fork's ADVANCE first, the one a fresh jail launch
		// runs (patchedadvance.go), waiting for it as that launch does (PF-D25).
		Patched: floorPatchedState,
		Advance: func(_ context.Context, p hostfloor.Program, installed *hostfloor.Record) hostfloor.PatchedState {
			floorAdvance(floorForkBuild(p, "").Fork, out, floorServingCopy(installed))
			return floorPatchedState(p)
		},
		Home:   paths.Home(),
		Out:    out,
		Prefix: "yolo host: ",
	}
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
// holds a fork's build on Linux only (noEntryReason refuses every other host first), where it is a
// capture jail's too, so the floor's lookups and a jail launch's name one build.
func floorPatchedPlatform() string { return capture.Platform() }

// floorAdvance is the floor's advance of a patched fork (hostfloor.Floor.Advance): the fresh
// launch's own (advancePatchedFork) as a LAUNCH — the check throttled, a back-off honored, the wait
// interruptible while a good build serves (PF-D25) — at the host (advanceOptions.host), its lines on
// the launch's stderr, as the floor's are. installed is the floor's own copy that serves, nil for
// none (PF-D52). It hands nothing: the floor installs the good build the record names once it
// returns. A var so a test can count the floor's advances.
var floorAdvance = func(f packload.Fork, out io.Writer, installed *installedCopy) {
	advancePatchedFork(f, advanceOptions{platform: floorPatchedPlatform(), out: out, errw: out, launch: true, host: true,
		installed: installed})
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
	if rec, err := patchedForkStore().LoadCheckRecord(f.Key()); err == nil && rec.Good != nil {
		g = rec.Good
	} else {
		g = recoverGoodBuild(store, f.Key(), platform, recipe, series.Len())
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
		recipe); err == nil {
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

// hostFloorCaptureAct is the capture act the production floor runs: `yolo capture <bin>` itself.
// A var only so a test can stand in for the jail it boots and still drive the floor's own Capture
// wiring; nothing but a test reassigns it.
var hostFloorCaptureAct = captureHost

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
		after, outcome, err := floor.Ensure(context.Background(), p)
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
func resolveHostLaunchTarget(packs []*packload.Pack, cmd0 string, lp *hostpath.Launch, errw io.Writer) (hostTarget, int) {
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
	st, _, err := floor.Ensure(context.Background(), prog)
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
		// A PATCHED FORK'S LINE (docs/design/patched-forks.md §7, PF-D11, PF-D50), the one a jail
		// launch's fork block prints, of the build the FLOOR runs: the series, that build, and the held
		// suffix while something holds the newest upstream back — or, when the floor runs another build
		// than the good build, both. A disclosure, so on every launch (OQ-RO3).
		line, _ := run.PatchedForkLine(floorForkBuild(prog, "").Fork, run.FloorCopy{Commit: st.Record.Revision,
			Recipe: st.Record.Recipe, Label: st.Record.Version}, "the next `yolo host -- "+cmd0+"`")
		fmt.Fprintf(errw, "yolo host: %s\n", line)
	}
	return hostTarget{Path: st.Launcher, Origin: originFloor}, 0
}

// noCopyWhere names the machine the no-copy line is about — and says "yet" for the one case that
// is a matter of time: an installer agent on a Mac, until the host capture (HP-D2) ships. goos is
// the floor's own platform (Floor.GOOS), the one its disposition was decided for.
func noCopyWhere(goos string, p hostfloor.Program) string {
	switch {
	case p.Install.Kind == packdecl.InstallKindSource:
		// A fork's build: the reason says why this machine holds none — no pin, a build made for
		// the jail's home only, no runtime to build with, or a Mac, which no Linux build runs on.
		// None of those is a matter of time, so no "yet".
		if goos == "darwin" {
			return "on this Mac"
		}
		return "on this machine"
	case goos == "darwin" && p.Install.Kind == "native":
		return "on this Mac yet"
	case goos == "darwin":
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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// hostreadiness.go is the host notch's READINESS ACT (docs/reference/host-agent-environment.md,
// HNR-D1), the host's copy of the jail's (internal/entrypoint/readiness.go, OQ-JR1): every
// `yolo host -- <cmd>` launch installs every program a user-scope selected pack declares into
// yolo's floor BEFORE it resolves the target, whatever the command, and a program it cannot
// install STOPS the launch unless paths.AllowMissingProgramsEnv is set. A typed patch-application
// failure follows the one rule a jail launch follows too (docs/reference/patched-forks.md PF-D83): when
// an intact admitted build of the series, or an installed copy, is on this machine, the missing-program
// hatch does not waive it and only paths.AllowPatchFailuresEnv goes on, running that build; when
// nothing of the program would run (hostfloor.ErrPatchFailureNothingRuns), the missing-program hatch
// waives it by launching without the program, as for any program that could not be installed. A failed
// update past a newer version found (hostfloor.UpdateRefusal, PD-NF1 of docs/design/program-delivery.md)
// is refused the same way: the installed version is still on the floor, so the missing-program hatch
// would not start without it, and only patchrecovery.UseInstalledEnv, which the floor reads, runs it.
//
// There is one installer: the act calls Floor.Ensure per program, as `yolo host apply --assert`
// and the target's own install do. Ensure on an installed entry is the throttled evergreen
// refresh, so a warm floor is a probe, not a download.
//
// # Which programs
//
// floorPrograms' set — the one `yolo host apply` reports — less what the jail's act also leaves
// out, and less the user's explicit choices:
//
//   - a kind the jail's act does not install ahead of the command (only npm, native and source
//     programs have launchers there);
//   - a program its vendor publishes no build of for this platform (the jail keeps the
//     generator's line for that class, launchercollision.go);
//   - a pack the user-scope `host_floor` leaves out, and a program the user's `provisioners` order
//     gives to another manager: both are the user's own decision that yolo's floor does not hold
//     it (HNR-D4), so they are not a failure to install;
//   - the program the launch itself runs: for a bare name, target resolution installs it with its
//     own refusal, which the bypass never reaches (HNR-D3); a target given as a path is the user's
//     own copy and runs as given, so the floor's copy of its base name is not installed for it
//     (HNR-D5).

// hostReadiness is what the act settled: the bins it ensured or failed, so a later caller
// (ensureMCPPrograms) does not run the same install a second time on one launch.
type hostReadiness struct {
	settled map[string]bool
}

// done reports whether the act already settled bin on this launch.
func (r hostReadiness) done(bin string) bool { return r.settled[bin] }

// hostReadinessFailure is one program the act could not make ready: its words for the refusal
// ("program <bin> (pack <name>)", the jail's) and why.
type hostReadinessFailure struct {
	who, pack string
	err       error
}

// hostFreshProgramSet is the owner-scoped host part of --update: the requested declared program
// plus the yolo-owned programs backing the MCP servers this host launch projects for it.
func hostFreshProgramSet(packs []*packload.Pack, cmd0 string) []hostfloor.Program {
	all := floorPrograms(packs)
	var selected []hostfloor.Program
	seen := map[string]bool{}
	add := func(p hostfloor.Program) {
		if seen[p.Bin()] {
			return
		}
		seen[p.Bin()] = true
		selected = append(selected, p)
	}
	if !strings.ContainsAny(cmd0, string(filepath.Separator)) {
		if target, ok := floorProgram(all, cmd0); ok {
			add(target)
		}
	}
	composed, _ := hostMCPPacks(packs)
	cfg := config.UserScopeConfigOrEmpty()
	userMCP, _ := cfg.Get("mcp_servers")
	userServers, _ := userMCP.(*jsonx.OrderedMap)
	effective, _ := hostServerEntries(packload.ComposeMCPServers(userServers, composed, paths.Home()), "mcp_servers", paths.Home())
	for _, held := range packload.HeldMCPServers(composed) {
		entry, present := effective.Get(held.Server.Name)
		applied, ok := entry.(*jsonx.OrderedMap)
		if !present || !ok {
			continue
		}
		command, _ := applied.Get("command")
		declared, valid := packload.DecodeMCPEntry(held.Server, paths.Home())
		if !valid {
			continue
		}
		declaredCommand, _ := declared.Get("command")
		if command != declaredCommand {
			continue
		}
		if held.Server.Bin == "" {
			continue
		}
		if p, ok := floorProgram(all, held.Server.Bin); ok {
			add(p)
		}
	}
	return selected
}

func freshFloorRoute(p hostfloor.Program) bool {
	switch p.Install.Kind {
	case "npm", "native", packdecl.InstallKindSource:
		return true
	default:
		return false
	}
}

// hostFreshProgramPreparation runs only hostFreshProgramSet's declared yolo-owned programs. A
// scoped plain-fork pin is re-resolved first; unrelated selected programs and lock entries remain
// untouched. Ensure's manual mode bypasses automatic policy/timing holds, not fixed pins or ownership.
func hostFreshProgramPreparation(packs []*packload.Pack, cmd0 string, errw io.Writer,
	act *run.ActInterrupt) int {
	programs := hostFreshProgramSet(packs, cmd0)
	if len(programs) == 0 {
		return 0
	}
	floor := newHostFloor(errw, floorPrograms(packs))
	floor.ManualUpdate = true
	rc := 0
	act.Scope(func(scope context.Context) {
		ctx := withActInterrupt(scope, act)
		for _, p := range programs {
			if floor.OutsideTheFloor(p) {
				continue
			}
			if !freshFloorRoute(p) {
				fmt.Fprintf(errw, "yolo host --update: %s in pack %s has no supported fresh installer route — preserve its current owner and run without --update, or select a supported pack declaration\n", p.Bin(), p.Pack)
				rc = 1
				return
			}
			if p.Install.IsForkBuild() && !p.Install.IsPatchedFork() { // a pack-tree program has no pin; Ensure refuses it
				var exact *packload.Fork
				for _, f := range packload.Forks(packs) {
					if f.Key() == floorForkBuild(p, "").Fork.Key() && f.Bin == p.Bin() && !f.Patched() {
						fork := f
						exact = &fork
						break
					}
				}
				if exact == nil {
					fmt.Fprintf(errw, "yolo host --update: %s in pack %s has no selected plain-fork declaration — repair the selected pack before launching\n", p.Bin(), p.Pack)
					rc = 1
					return
				}
				if err := freshHostPlainSource(ctx, *exact, floorPatchedPlatform(), errw); err != nil {
					fmt.Fprintf(errw, "yolo host --update: %v — repair the named source problem, then retry\n", err)
					rc = 1
					return
				}
				// The floor's ForkPin reader snapshots pins when constructed. Reopen it after
				// the scoped repin so Ensure consumes the newly authorized commit.
				floor = newHostFloor(errw, floorPrograms(packs))
				floor.ManualUpdate = true
			}
			programCtx := ctx
			if p.Install.IsPatchedFork() {
				f := floorForkBuild(p, "").Fork
				identities, boundaries, errs := map[string]string{}, map[string]packsrc.CheckStart{}, map[string]error{}
				if err := registerFreshPreparation(ctx, patchedAdvanceStore(true), f, floorPatchedPlatform(), identities, boundaries, errs); err != nil {
					fmt.Fprintf(errw, "yolo host --update: could not register %s: %v — repair the named problem and retry\n", p.Bin(), err)
					rc = 1
					return
				}
				programCtx = context.WithValue(ctx, floorFreshRequestKey{}, floorFreshRequest{identity: identities[f.Key()], boundary: boundaries[f.Key()]})
			}
			_, outcome, err := floor.Ensure(programCtx, p)
			if act.Interrupted() {
				rc = 1
				return
			}
			if err != nil || outcome == hostfloor.Kept {
				fmt.Fprintf(errw, "yolo host --update: fresh preparation of %s (pack %s) failed: %v — fix the named install/build problem, then retry\n",
					p.Bin(), p.Pack, err)
				rc = 1
				return
			}
		}
	})
	if act.Interrupted() {
		return 130
	}
	return rc
}

// hostReadinessAct runs the act for a launch of cmd. It returns what it settled and the launch's
// exit code: nonzero when a program could not be installed and the bypass is not set, the refusal
// already printed on errw.
func hostReadinessAct(packs []*packload.Pack, cmd []string, errw io.Writer, act *run.ActInterrupt) (hostReadiness, int) {
	r := hostReadiness{settled: map[string]bool{}}
	if config.InJail() || len(cmd) == 0 {
		// No floor in a jail: the jail's own act already ran at its boot.
		return r, 0
	}
	// The program this launch runs: its bare name, or the base name of a target given as a path,
	// which is the user's own copy of it (HP-DIR4) and keys its composition the same way.
	cmd0 := filepath.Base(cmd[0])
	progs := floorPrograms(packs)
	var todo []hostfloor.Program
	for _, p := range progs {
		switch p.Install.Kind {
		case "npm", "native", packdecl.InstallKindSource:
		default:
			continue
		}
		if p.Bin() == cmd0 {
			// A bare name's install is target resolution's, with its own refusal, which the bypass
			// never reaches (HNR-D3); a path target runs as given, so the floor's copy is not needed
			// to run it (HNR-D5).
			continue
		}
		todo = append(todo, p)
	}
	if len(todo) == 0 {
		return r, 0
	}
	floor := newHostFloor(errw, progs)
	floor.PatchBypassCommand = "yolo host -- " + shquote.Quote(cmd0)
	ctx := withActInterrupt(context.Background(), act)
	var failed []hostReadinessFailure
	// patchFailure is a patch failure the missing-program hatch does not waive (PF-D83).
	patchFailure := false
	// patchSeries is set when one of them is a series that does not apply, rather than only a newer
	// build that failed (OQ-PPX3), which the summary line names.
	patchSeries := false
	// updateFailure is a failed update past a newer version found (PD-NF1): the installed version is
	// still on the floor, so the missing-program hatch — "start without it" — would not start without
	// it, and only the refusal's own hatch (YOLO_USE_INSTALLED) runs it.
	updateFailure := false
	for _, p := range todo {
		if floor.OutsideTheFloor(p) {
			continue
		}
		_, _, err := floor.Ensure(ctx, p)
		r.settled[p.Bin()] = true
		if err != nil {
			// A PATCH FAILURE WITH SOMETHING TO RUN is not the missing-program hatch's (PF-D83).
			var refusal *hostfloor.UpdateRefusal
			if errors.As(err, &refusal) {
				updateFailure = true
			}
			var applicationFailure *packsrc.PatchFailure
			if errors.As(err, &applicationFailure) && !errors.Is(err, hostfloor.ErrPatchFailureNothingRuns) {
				patchFailure = true
				if applicationFailure.Kind != packsrc.PatchFailureBuild {
					patchSeries = true
				}
			}
			failed = append(failed, hostReadinessFailure{who: "program " + p.Bin() + " (pack " + p.Pack + ")",
				pack: p.Pack, err: err})
		}
	}
	if len(failed) == 0 {
		return r, 0
	}
	var list strings.Builder
	var leaveOut []string
	seenPack := map[string]bool{}
	for _, f := range failed {
		// A newer yolo's record is not an install failure: Ensure refused to install over it, and
		// its error already names `yolo update`.
		if errors.Is(f.err, hostfloor.ErrNewerRecord) {
			fmt.Fprintf(&list, "      %s: will not install over a newer yolo's copy: %v\n", f.who, f.err)
		} else {
			fmt.Fprintf(&list, "      %s: %v\n", f.who, f.err)
		}
		if !seenPack[f.pack] {
			seenPack[f.pack] = true
			leaveOut = append(leaveOut, "`"+config.HostFloorLeaveOutCommand(f.pack)+"`")
		}
	}
	if os.Getenv(paths.AllowMissingProgramsEnv) != "" && !patchFailure && !updateFailure {
		fmt.Fprintf(errw, "yolo host: ⚠ %s is set, so this launch starts WITHOUT what a selected pack declares:\n%s"+
			"    The next `yolo host` launch tries each install again.\n", paths.AllowMissingProgramsEnv, list.String())
		return r, 0
	}
	if patchFailure || updateFailure {
		// The error block above names the failure, its repair and the hatch that runs a build here.
		what := "a newer build of a patched program failed"
		switch {
		case patchSeries:
			what = "a patch series does not apply"
		case !patchFailure:
			what = "a newer version of a program could not be installed"
		}
		fmt.Fprintf(errw, "yolo host: refusing to launch: %s\n%s", what, list.String())
		return r, 1
	}
	// REFUSAL, not a warning, in the jail's words: a launch without a program its own config
	// selected is not the environment that config promised.
	ways := "      Fix what each line names (an install needs the network), or drop the pack from your packs list.\n"
	if len(leaveOut) > 0 {
		ways = "      Fix what each line names (an install needs the network), drop the pack from your packs list, or\n" +
			"      leave it out of yolo's floor with " + strings.Join(leaveOut, " and ") + ".\n"
	}
	fmt.Fprintf(errw, "yolo host: REFUSING to launch: a program a selected pack declares could not be installed.\n%s%s"+
		"      To launch without it:\n"+
		"          %s=1 yolo host -- %s\n", list.String(), ways,
		paths.AllowMissingProgramsEnv, shquote.Join(cmd))
	return r, 1
}

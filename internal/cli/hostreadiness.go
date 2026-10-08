package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// hostreadiness.go is the host notch's READINESS ACT (docs/design/host-notch-readiness.md,
// HNR-D1), the host's copy of the jail's (internal/entrypoint/readiness.go, OQ-JR1): every
// `yolo host -- <cmd>` launch installs every program a user-scope selected pack declares into
// yolo's floor BEFORE it resolves the target, whatever the command, and a program it cannot
// install STOPS the launch unless paths.AllowMissingProgramsEnv is set.
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
	ctx := withActInterrupt(context.Background(), act)
	var failed []hostReadinessFailure
	for _, p := range todo {
		if floor.OutsideTheFloor(p) {
			continue
		}
		_, _, err := floor.Ensure(ctx, p)
		r.settled[p.Bin()] = true
		if err != nil {
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
		fmt.Fprintf(&list, "      %s: %v\n", f.who, f.err)
		if !seenPack[f.pack] {
			seenPack[f.pack] = true
			leaveOut = append(leaveOut, fmt.Sprintf("%q: false", f.pack))
		}
	}
	if os.Getenv(paths.AllowMissingProgramsEnv) != "" {
		fmt.Fprintf(errw, "yolo host: ⚠ %s is set, so this launch starts WITHOUT what a selected pack declares:\n%s"+
			"    The next `yolo host` launch tries each install again.\n", paths.AllowMissingProgramsEnv, list.String())
		return r, 0
	}
	// REFUSAL, not a warning, in the jail's words: a launch without a program its own config
	// selected is not the environment that config promised.
	fmt.Fprintf(errw, "yolo host: REFUSING to launch: a program a selected pack declares could not be installed.\n%s"+
		"      Fix what each line names (an install needs the network), drop the pack from your packs list, or\n"+
		"      leave it out of yolo's floor with `\"host_floor\": {%s}` in the user config.\n"+
		"      To launch without it:\n"+
		"          %s=1 yolo host -- %s\n", list.String(), strings.Join(leaveOut, ", "),
		paths.AllowMissingProgramsEnv, shquote.Join(cmd))
	return r, 1
}

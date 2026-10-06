package entrypoint

// readiness.go is the jail notch's READINESS ACT (docs/design/jail-notch-readiness.md, OQ-JR1):
// every program a selected pack declares is installed in the provisioning stage, before the
// command runs, and a program the stage cannot install STOPS the launch.
//
// # Where it runs, and what it runs
//
// In the bootstrap script (shell.go), after the Node floors and before the stage's verdict, so
// it runs after the CA bundle and after `mise install`, in the one place a launch already
// installs over the network (§3 of the design names why it cannot go in launcher generation:
// `yolo check` runs the generators, and an observe verb must install nothing).
//
// What it runs is each program's OWN LAUNCHER, in install-only mode (InstallOnlyEnv): the npm,
// installer and source launchers each install when the program is absent and stop without
// running it, and do nothing at all when it is present. So there is one implementation of each
// install, the launcher's, and the readiness act is a caller of it, as `yolo pack update` and
// `yolo capture` are. Install-only never refreshes a program that is present: currency stays at
// invocation (OQ-PD12a), and readiness is about presence alone (§1 of the design).
//
// # Which programs
//
// Every program a SELECTED pack declares that got a launcher this boot (OQ-JR2, answered by
// HP-DIR2: no narrowing to what the launch might run). The set is decided by the same
// predicates GenerateAgentLaunchers applies, in the same order, so a program it declined is not
// one readiness asks for: a name the image or a declared mise tool already provides is ready
// already, and a program whose vendor publishes no build for this platform has no launcher and
// keeps the generator's own line, as launchercollision.go rules for that fault class. A test
// holds the two lists together (TestReadinessAsksForExactlyTheLaunchersTheBootWrote).
//
// # What a failure does
//
// It refuses the launch, naming the pack, the program and the install's error, offline
// included, and a degraded launch is opt-in, never the default (the maintainer's ruling on
// OQ-JR1, 2026-10-05: "we can have a bypass var or whatever, but by default, no"). The refusal
// is provision.RefusedStatus, the one status the stage passes through without asking.
// paths.AllowMissingProgramsEnv is the hatch the refusal offers: the jail starts and lists each
// program it could not install, and each installs on first use, as before.
//
// # macos-user
//
// Not there yet (JR-D2): readiness ships container-first, and that backend's stage does not
// start for a missing program. Its launch names each declared program it finds absent instead
// (warnProgramsNotReady), and the programs install on first use there, as before.

import (
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// readyProgram is one program the readiness act installs: its bin, which names its launcher in
// the launch dir, and "program <bin> (pack <name>)", the words the refusal says. inst is the
// declaration, for the macos-user report's presence check.
type readyProgram struct {
	Bin  string
	Who  string
	inst packdecl.Install
}

// declaredReadyPrograms is the set the readiness act installs, in pack-load order.
//
// A boot that cannot load its packs contributes nothing here: it has a louder problem, reported
// on its own path, and a pack that cannot say what it installs cannot be shown to need it.
func declaredReadyPrograms(e *Env) []readyProgram {
	packs, err := LoadJailPacks(e)
	if err != nil {
		return nil
	}
	return readyProgramsOf(e, packs)
}

// readyProgramsOf is declaredReadyPrograms over packs already loaded. Its skips are
// GenerateAgentLaunchers', in that function's order: an unusable bin name, a kind with no
// launcher, a name the image or a mise tool provides (launcherShadows), a platform the vendor
// does not publish for (UnpublishedReason), and a bin an earlier pack already took.
func readyProgramsOf(e *Env, packs []*packload.Pack) []readyProgram {
	probePath, miseBins := imageProbePath(e), declaredMiseBins(e)
	seen := map[string]bool{}
	var out []readyProgram
	for _, p := range packs {
		if p == nil {
			continue
		}
		installs, _ := p.HonoredInstalls()
		for _, inst := range installs {
			if !packdecl.ValidBinName(inst.Bin) {
				continue
			}
			switch inst.Kind {
			case "npm", "native", packdecl.InstallKindSource:
			default:
				continue
			}
			if launcherShadows(inst.Bin, probePath, miseBins) != "" {
				continue
			}
			if inst.UnpublishedReason(runtime.GOOS, runtime.GOARCH) != "" {
				continue
			}
			if seen[inst.Bin] {
				continue
			}
			seen[inst.Bin] = true
			out = append(out, readyProgram{
				Bin:  inst.Bin,
				Who:  "program " + inst.Bin + " (pack " + p.Name + ")",
				inst: inst,
			})
		}
	}
	return out
}

// readinessChecks renders the readiness act's calls for the bootstrap: one `_yolo_ready <bin>
// <who>` line per program, or "" when no selected pack declares one, so a jail with none runs
// and prints exactly what it did before (the design's §7 item 3).
//
// Every value is shquote'd into a bare word: the bin is validated, the pack name is a
// pack-supplied string, and this is shell source.
//
// Two environments render something else, each saying so:
//   - macos-user (Env.DeferProgramReadiness, JR-D2) renders nothing; its launch names what is
//     absent (warnProgramsNotReady).
//   - NoProgramReadinessEnv renders one notice naming what it left, and installs nothing.
func readinessChecks(e *Env) string {
	if e.DeferProgramReadiness {
		return ""
	}
	progs := declaredReadyPrograms(e)
	if len(progs) == 0 {
		return ""
	}
	if e.Getenv(paths.NoProgramReadinessEnv) != "" {
		var who []string
		for _, p := range progs {
			who = append(who, p.Who)
		}
		return "echo " + shquote.Quote("  ↳ "+paths.NoProgramReadinessEnv+
			" is set, so nothing is installed ahead of the command; each of these installs the "+
			"first time it is run: "+strings.Join(who, ", ")) + " >&2"
	}
	lines := make([]string, 0, len(progs))
	for _, p := range progs {
		lines = append(lines, "_yolo_ready "+shquote.Quote(p.Bin)+" "+shquote.Quote(p.Who))
	}
	return strings.Join(lines, "\n")
}

// allowMissingPrograms is the hatch's value as the bootstrap bakes it: "1" or "0". Baked, like
// every other value in that script, so the stage reads the launch's decision and not whatever its
// own environment happens to hold.
func allowMissingPrograms(e *Env) string {
	return boolFlag(e.Getenv(paths.AllowMissingProgramsEnv) != "")
}

// programRealBin is where a program's launcher looks for it — its REAL_BIN — for the
// macos-user report: the npm prefix's bin for npm, ~/.local/bin for an installer.
func programRealBin(e *Env, inst packdecl.Install) string {
	if inst.Kind == "npm" {
		return filepath.Join(e.NpmBin(), inst.Bin)
	}
	return filepath.Join(e.LocalBin(), inst.Bin)
}

// warnProgramsNotReady is JR-D2's line for macos-user: the readiness act does not run on this
// backend yet, so its launch names each declared program that is not installed, rather than
// leaving the absence to be found when the program is first run ("Warned", never "Dropped",
// docs/design/backend-parity.md). Silent when every declared program is present.
func warnProgramsNotReady(e *Env) {
	if !e.DeferProgramReadiness {
		return
	}
	var absent []string
	for _, p := range declaredReadyPrograms(e) {
		// A fork's program is not delivered on this backend at all, and the launch already says
		// so and why (FP-D3, run.noteMacosUserForks); "installs the first time it is run" would
		// be false of it.
		if p.inst.Kind == packdecl.InstallKindSource {
			continue
		}
		if !isExecutableFile(programRealBin(e, p.inst)) {
			absent = append(absent, p.Who)
		}
	}
	if len(absent) == 0 {
		return
	}
	e.warn("macos-user does not install a selected pack's programs before the launch yet " +
		"(docs/design/jail-notch-readiness.md JR-D2), so these are not installed: " +
		strings.Join(absent, ", ") + ". Each installs the first time it is run; run one with " +
		"--version to install it now.")
}

package cli

// packupdate.go implements the half of `yolo pack update` that `yolo pack install`
// deliberately does not do: refreshing a pack's declared `program`, of any delivery
// mechanism.
//
// WHY THE TWO VERBS HAD TO SPLIT. They were one case arm (`case "install", "update":`)
// for as long as both meant "sync the git mirror and write the lockfile", and that was
// honest while nothing else could move. It stopped being honest the moment the ruling in
// docs/design/trust-paths.md §1 row 1 took the npm launcher's hourly reinstall away: the
// launcher used to be the thing that made an agent CLI current, on a timer, with nobody
// present. Delete that and a user needs SOME act that resolves a new version — and the
// ruling names which one:
//
//	install  installs what is recorded, and never asks the registry what is latest
//	update   the only act that resolves a new version
//	the poll may only SAY that a newer version exists; it may never reinstall
//
// So the verbs must end up BEHAVING differently, not merely printing differently. This
// file is that difference, and it is the whole of it.
//
// ⚠ **THE JOB IS SMALLER SINCE 2026-09-04, and the npm-only restriction is gone.**
// program-delivery.md §3.5 (OQ-PD12) makes agent dependencies evergreen at the user's own
// invocation, so this is no longer the only way an agent CLI moves. It is still the way to
// move one NOW — without restarting the jail — and the only way to refresh a pack whose
// `agent_updates` is false. It used to skip every non-`npm` install kind, which is the
// defect OQ-PD14 names: core had no way to know how an installer-delivered program updates
// itself, so it declined to try. The pack declares that now (`Install.UpdateVerb`), so this
// walks the same set the launchers do.
//
// WHAT THIS IS NOT. It is not the lockfile half of the ruling. There is nowhere yet to
// record which npm version `update` resolved: LockEntry has no field for a package
// version, and the lockfile exists per FETCHED pack while all four packs that declare npm
// programs (pi, copilot, codex, opencode) are EMBEDDED. That is OQ-TP4, still open, and
// inventing a location for the pin here would be answering it by accident. Until it is
// answered, `install` obeys a pin it can see (a `package` string carrying a version,
// which npmspec.go already honours) and otherwise leaves whatever is installed alone —
// which is the ruling's shape minus its record.
//
// WHERE IT RUNS. An npm-declared program is installed INSIDE a jail, into that jail's
// $NPM_CONFIG_PREFIX, by the launcher the entrypoint generated for it. So this refresh is
// a jail-side act: on the host there is no launcher, no npm prefix and nothing to
// refresh, and saying so is better than silently doing nothing.

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// launcherUpdateEnv puts a generated launcher into UPDATE MODE: refresh the program now —
// ignoring both the stamp and the `agent_updates` policy, because a human asked — and exit
// without exec'ing the real binary.
//
// The variable is the seam between the two halves of the split, and it is deliberately
// the ONLY one. The launcher already knows how to talk to npm — the install spec, the
// scoped-package rule, the stale-temp-dir cleanup, the spec-file bookkeeping that keeps a
// failed upgrade retryable — and re-deriving any of that in Go beside it is the "two
// implementations of one client" drift AGENTS.md keeps deleting. `yolo pack update` is
// therefore a caller of the launcher, not a second npm client.
//
// The obvious hazard, stated so it is not a surprise: a user who EXPORTS this variable
// turns every launcher invocation into an update-and-exit and never launches their agent.
// That is why the launcher announces what it did on stderr in this mode, and why nothing
// but `yolo pack update` sets it.
const launcherUpdateEnv = "YOLO_PACK_UPDATE"

// launcherRunner runs one launcher script in update mode. A seam so a test can observe
// WHICH launchers a refresh reaches without executing npm — the production one is
// execLauncherUpdate.
type launcherRunner func(bin, path string) error

// programRefresh is the update verb's second half, as a package-level seam.
//
// It is a var rather than a direct call because the property worth testing is not "the
// refresh works" but "INSTALL NEVER REACHES IT" — the ruling is about which act may
// resolve a version, and a test that could only drive the refresh function directly would
// be asserting the plumbing rather than the rule.
var programRefresh = refreshProgramsFromOS

// packUpdate is `yolo pack update`: everything `install` does, plus the program refresh.
//
// The git/lockfile half runs first and unconditionally. A failure there does not skip the
// npm half — the two are independent (a pack whose git remote is offline says nothing
// about whether an agent CLI can reach the npm registry), and an update that silently
// stopped after the first error would leave the user unable to tell which half ran.
func packUpdate(out, errw io.Writer, color bool) int {
	rc := packInstall(out, errw, color)
	if n := programRefresh(richtext.Printer{W: out, Color: color}, errw); n != 0 && rc == 0 {
		rc = n
	}
	return rc
}

// refreshProgramsFromOS is the production programRefresh: it reads the real environment,
// and refuses to guess when it is not looking at a jail.
//
// YOLO_PACK_ROOT is the discriminator rather than YOLO_VERSION, and the difference
// matters: it does not merely say "in a jail", it says "this process can see the staged
// pack tree", which is the input the refresh actually needs. The host has neither.
func refreshProgramsFromOS(pr richtext.Printer, errw io.Writer) int {
	e := entrypoint.EnvFromOS()
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		pr.Printf("[dim]No staged packs here — a pack-declared program is installed " +
			"INSIDE a jail, so run `yolo pack update` there to refresh it.[/dim]")
		return 0
	}
	return refreshPrograms(e, pr, errw, execLauncherUpdate(pr.W, errw))
}

// execLauncherUpdate runs a launcher with launcherUpdateEnv set. Output goes straight
// through: the launcher's own "Updating x → y" / "already current" lines are the report,
// and paraphrasing them here would give the same event two spellings.
func execLauncherUpdate(out, errw io.Writer) launcherRunner {
	return func(bin, path string) error {
		cmd := exec.Command(path)
		cmd.Env = append(os.Environ(), launcherUpdateEnv+"=1")
		cmd.Stdout = out
		cmd.Stderr = errw
		return cmd.Run()
	}
}

// refreshPrograms runs the update path of every declared `program` in the packs staged for
// this jail.
//
// It walks the PACKS rather than the launcher directory, and that is not incidental: the
// launcher dir also holds the PACKAGE-MANAGER launchers (pnpm), which no pack declares and
// which have no update mode — running one of those with YOLO_PACK_UPDATE=1 would simply
// exec pnpm. The manifest is what says which launcher belongs to a pack, so it is what we
// read.
//
// EVERY KIND, not just npm. The `inst.Kind != "npm"` skip here was the visible half of
// OQ-PD14: core had no way to know how an installer-delivered program updates itself, so
// `yolo pack update` silently did nothing for claude, agy and codex. Both launcher
// templates now have an update mode, driven by the pack's declared verb or by their `via`'s
// fallback, so the filter has nothing left to protect. An install whose Kind is EMPTY is
// still skipped: that is a `via` this build does not know (packdecl.unknownViaSkip), for
// which no launcher was generated either.
//
// HonoredInstalls, not InstallContributions: the origin gate is per contribution, and a
// refresh has no business honouring a declaration the load path refused.
func refreshPrograms(e *entrypoint.Env, pr richtext.Printer, errw io.Writer, run launcherRunner) int {
	packs, err := entrypoint.LoadJailPacks(e)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack update: reading staged packs: %v\n", err)
		return 1
	}
	rc := 0
	found := 0
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for i := range installs {
			inst := &installs[i]
			if inst.Kind == "" {
				continue
			}
			found++
			launcher := filepath.Join(e.LaunchDir(), inst.Bin)
			if _, err := os.Stat(launcher); err != nil {
				// The pack declares the program but this jail has no launcher for it:
				// either the boot path never generated one (two packs claiming one bin
				// name — first writer wins) or the home predates the declaration. Either
				// way there is nothing to refresh and silence would read as success.
				fmt.Fprintf(errw, "yolo pack update: %s: no launcher at %s — nothing to "+
					"refresh (relaunch the jail to generate one)\n", inst.Bin, launcher)
				rc = 1
				continue
			}
			pr.Printf("[dim]%s: refreshing %s[/dim]", inst.Bin, declaredSource(inst))
			if err := run(inst.Bin, launcher); err != nil {
				fmt.Fprintf(errw, "yolo pack update: %s: %v\n", inst.Bin, err)
				rc = 1
			}
		}
	}
	if found == 0 {
		pr.Printf("[dim]No pack-declared programs to refresh.[/dim]")
	}
	return rc
}

// declaredSource names what a refresh is going to resolve against, for the one line this
// command prints per program: the npm package, or the installer URL. It exists because the
// walk is no longer npm-only and `inst.Package` is empty for half the set — printing it
// unconditionally made the installer rows read as "claude: refreshing " with nothing after
// the colon.
func declaredSource(inst *packdecl.Install) string {
	if inst.Package != "" {
		return inst.Package
	}
	if inst.InstallerURL != "" {
		return inst.InstallerURL
	}
	return inst.Kind
}

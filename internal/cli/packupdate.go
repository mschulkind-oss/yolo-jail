package cli

// packupdate.go implements the half of `yolo pack update` that `yolo pack install`
// deliberately does not do: refreshing a pack's npm-declared `program`.
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
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// npmLauncherUpdateEnv puts a generated npm launcher into UPDATE MODE: resolve a new
// version, install it, and exit without exec'ing the real binary.
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
const npmLauncherUpdateEnv = "YOLO_PACK_UPDATE"

// launcherRunner runs one launcher script in update mode. A seam so a test can observe
// WHICH launchers a refresh reaches without executing npm — the production one is
// execLauncherUpdate.
type launcherRunner func(bin, path string) error

// npmRefresh is the update verb's second half, as a package-level seam.
//
// It is a var rather than a direct call because the property worth testing is not "the
// refresh works" but "INSTALL NEVER REACHES IT" — the ruling is about which act may
// resolve a version, and a test that could only drive the refresh function directly would
// be asserting the plumbing rather than the rule.
var npmRefresh = refreshNpmProgramsFromOS

// packUpdate is `yolo pack update`: everything `install` does, plus the npm refresh.
//
// The git/lockfile half runs first and unconditionally. A failure there does not skip the
// npm half — the two are independent (a pack whose git remote is offline says nothing
// about whether an agent CLI can reach the npm registry), and an update that silently
// stopped after the first error would leave the user unable to tell which half ran.
func packUpdate(out, errw io.Writer, color bool, stdin io.Reader) int {
	rc := packInstall(out, errw, color, stdin)
	if n := npmRefresh(richtext.Printer{W: out, Color: color}, errw); n != 0 && rc == 0 {
		rc = n
	}
	return rc
}

// refreshNpmProgramsFromOS is the production npmRefresh: it reads the real environment,
// and refuses to guess when it is not looking at a jail.
//
// YOLO_PACK_ROOT is the discriminator rather than YOLO_VERSION, and the difference
// matters: it does not merely say "in a jail", it says "this process can see the staged
// pack tree", which is the input the refresh actually needs. The host has neither.
func refreshNpmProgramsFromOS(pr richtext.Printer, errw io.Writer) int {
	e := entrypoint.EnvFromOS()
	if e.Getenv("YOLO_PACK_ROOT") == "" {
		pr.Printf("[dim]No staged packs here — an npm-declared program is installed " +
			"INSIDE a jail, so run `yolo pack update` there to resolve a new version.[/dim]")
		return 0
	}
	return refreshNpmPrograms(e, pr, errw, execLauncherUpdate(pr.W, errw))
}

// execLauncherUpdate runs a launcher with npmLauncherUpdateEnv set. Output goes straight
// through: the launcher's own "Updating x → y" / "already current" lines are the report,
// and paraphrasing them here would give the same event two spellings.
func execLauncherUpdate(out, errw io.Writer) launcherRunner {
	return func(bin, path string) error {
		cmd := exec.Command(path)
		cmd.Env = append(os.Environ(), npmLauncherUpdateEnv+"=1")
		cmd.Stdout = out
		cmd.Stderr = errw
		return cmd.Run()
	}
}

// refreshNpmPrograms runs the update path of every npm-declared `program` in the packs
// staged for this jail.
//
// It walks the PACKS rather than the launcher directory, and that is not incidental: the
// launcher dir also holds native-installer launchers (which exec the vendor's own updater)
// and the package-manager launchers (pnpm), and running one of those as if it were an npm
// program would exec a tool instead of refreshing it. The manifest is what says which is
// which, so it is what we read.
//
// HonoredInstalls, not InstallContributions: the origin gate is per contribution, and a
// refresh has no business honouring a declaration the load path refused.
func refreshNpmPrograms(e *entrypoint.Env, pr richtext.Printer, errw io.Writer, run launcherRunner) int {
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
			if inst.Kind != "npm" {
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
			pr.Printf("[dim]%s: resolving %s[/dim]", inst.Bin, inst.Package)
			if err := run(inst.Bin, launcher); err != nil {
				fmt.Fprintf(errw, "yolo pack update: %s: %v\n", inst.Bin, err)
				rc = 1
			}
		}
	}
	if found == 0 {
		pr.Printf("[dim]No npm-declared programs to refresh.[/dim]")
	}
	return rc
}

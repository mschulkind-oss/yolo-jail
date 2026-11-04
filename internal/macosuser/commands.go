package macosuser

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// refuseOffMacOS is each `yolo yolo-account` verb's answer on any system but macOS, where
// the yolo account is not built yet (MT-R2 of docs/design/named-setups.md): agents there run
// as you, so the next step is the launch itself.
func refuseOffMacOS(out printer, verb string) {
	out.printf("[bold red]yolo yolo-account %s requires macOS.[/bold red] The yolo account is "+
		"not built on this system yet; agents here run as you, in a container: `yolo -- <cmd>`.", verb)
}

// AccountCreate creates the yolo account (one-time, needs admin).
// Idempotent. Returns the exit code (0 success; 1 on failure/refusal) so the
// front door maps it to a process exit.
func AccountCreate(deps Deps) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		refuseOffMacOS(out, "create")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}

	// 0. ENROLLMENT, BEFORE ANYTHING IS CREATED (task 7 of
	// docs/design/macos-user-privilege-lifecycle-plan.md; OQ-MP10 in
	// docs/design/macos-user-privilege-lifecycle.md). One host user per Mac: the rule and the
	// staging root are this user's or nobody's, so a name that cannot go into a sudo rule, a
	// squatted root and a second user's root are refused before the first sudo, and a second
	// user's rule at the first.
	hostUser := deps.HostUser()
	rule, err := SudoersRule(hostUser)
	if err != nil {
		out.printf("[bold red]Your user name %s cannot be written into a sudo rule safely.[/bold red] "+
			"The macos-user backend supports names of letters, digits, '_', '.' and '-'.", hostUser)
		return 1
	}
	if rc := refuseForeignEnrollment(deps, out); rc != 0 {
		return rc
	}
	announceSudo(deps, out, "set up the yolo account and its rule ("+SudoersFile+")")
	if rc := refuseAnotherUsersRule(deps, out, hostUser); rc != 0 {
		return rc
	}
	out.printf("• Granting %s one standing right: run commands as %s, never as root, without a "+
		"password (%s). Launches then start and stop with no prompt.", hostUser, SandboxUser, SudoersFile)

	// 1. Account — create if missing, otherwise reuse (idempotent).
	if deps.SandboxUserExists() {
		out.printf("• The yolo account [bold]%s[/bold] already exists.", SandboxUser)

		// 1a. THE HOME, CHECKED INDEPENDENTLY OF THE ACCOUNT. Every home step lives in
		// the creation branch below, so an account whose home had been DELETED got none
		// of them and this command still finished with "✓ macos-user backend ready …
		// preconditions pass". Nothing else repairs it either: /Users is root-owned
		// 0755, so the sandbox uid cannot create its own home, and a launch in that
		// state fails twenty config generators deep on `mkdir /Users/_yolojail:
		// permission denied`.
		//
		// NOT A HYPOTHETICAL STATE — it is what the runbook's own remedy leaves behind
		// (`sudo rm -rf /Users/_yolojail && yolo yolo-account create`, prescribed for an account
		// predating the home-tier layout), so the one command told to repair the machine
		// was the one that could not. Measured on hardware 2026-09-12.
		//
		// Gated on the home being absent rather than run unconditionally: `chown -R`
		// over a populated account home is O(files) and buys nothing on a re-run.
		if !deps.PathIsDir(SandboxHome()) {
			out.printf("• Sandbox home [bold]%s[/bold] is missing — reprovisioning it "+
				"(the account record itself is left alone).", SandboxHome())
			for _, cmd := range ProvisionHomeCommands() {
				if deps.Run(rootArgv(cmd)) != 0 {
					out.printf("[bold red]✗ setup step failed:[/bold red] %s",
						strings.Join(cmd, " "))
					return 1
				}
			}
			out.printf("  [green]reprovisioned[/green] %s.", SandboxHome())
		}
	} else {
		uid := NextFreeID(deps.TakenIDs(), sandboxMinID)
		out.printf("• Creating the yolo account [bold]%s[/bold] (uid %d).", SandboxUser, uid)
		for _, cmd := range CreateUserCommands(uid, uid, hostUser) {
			if deps.Run(rootArgv(cmd)) != 0 {
				out.printf("[bold red]✗ setup step failed:[/bold red] %s", strings.Join(cmd, " "))
				return 1
			}
		}
		// Random password, piped via stdin (never argv). Finding 6: the return
		// value was previously dropped, so even a failed set was silent — check
		// it and fail loud (an unset/empty password is a security hole).
		if !deps.SetRandomPassword() {
			out.print("[bold red]✗ could not set a random password on " + SandboxUser +
				"[/bold red] — the account may have no password. Aborting setup.")
			return 1
		}
		out.printf("  [green]created[/green] %s.", SandboxUser)
	}

	// 1b. Provision the neutral shared root (idempotent).
	out.printf("• Provisioning shared root [bold]%s[/bold] "+
		"(setgid + inheriting ACL, group _yolojail, no other-access) — "+
		"projects created under it are shared automatically, no per-run walk.", SharedRootDefault())
	for _, cmd := range SharedRootProvisionCommands("", hostUser) {
		if deps.Run(rootArgv(cmd)) != 0 {
			out.printf("[bold red]✗ setup step failed:[/bold red] %s", strings.Join(cmd, " "))
			return 1
		}
	}

	// 1c. SHARE WHAT IS ALREADY THERE. Provisioning the root above covers everything
	// created FROM NOW ON and nothing that already exists — macOS applies inheritable
	// ACL entries at create time and never retroactively. So setup finishes the job by
	// running the same retrofit `yolo yolo-account share` runs, over the same root.
	//
	// UNCONDITIONAL AND UNPROMPTED, because it is idempotent and because setup is
	// already the "make this machine ready" command — a user who ran it and then hit
	// `permission denied` on a workspace that was sitting there the whole time was
	// told setup was done when it was not. On a fresh machine the root is empty and
	// this costs nothing; on a populated one it is ~0.16ms per object (measured
	// 2026-09-03), which is the right price at setup and the wrong one on a launch.
	//
	// NOT run under sudo: FixPermissionsScript applies the grant as the invoking
	// user, and running it as root would assign the shared-workspace ACL to root
	// instead (the reason refuseIfRoot guards this whole command).
	out.printf("• Sharing existing projects under [bold]%s[/bold] with %s "+
		"(idempotent; safe to re-run).", SharedRootDefault(), SandboxUser)
	if failed, rc := shareTree(deps, SharedRootDefault()); rc != 0 {
		reportACLFailures(out, SharedRootDefault(), hostUser, failed)
	} else {
		out.print("  [green]done[/green]")
	}

	// 1d. The staging root and the capture root become the host user's own (PL-6), then the rule
	// is installed, then the files older sessions left are removed (PL-8), and last the rule is
	// proved with an empty credential cache. Setup declares ready only after that proof.
	if rc := ensureHostOwnedRoot(deps, out, StagingRoot(), hostUser, false); rc != 0 {
		return rc
	}
	if rc := ensureHostOwnedRoot(deps, out, CaptureRootDefault(), hostUser, true); rc != 0 {
		return rc
	}
	if rc := installSudoersRule(deps, out, rule); rc != 0 {
		return rc
	}
	sweepLegacySessions(deps, out)
	if rc := proveRunAsGrant(deps, out); rc != 0 {
		return rc
	}

	// 2. Readiness checks — report each; neither fatal to setup. No interpreter
	// readiness check is needed: the sandbox self-execs the staged yolo binary
	// (J2 §3).
	var warnings []string

	if !deps.Which("sandbox-exec") {
		warnings = append(warnings,
			"sandbox-exec not found on PATH — it ships with macOS, so this is "+
				"unusual; the run path needs it for the Seatbelt profile.")
		out.print("• Apple Seatbelt (sandbox-exec): [yellow]not found[/yellow]")
	} else {
		out.print("• Apple Seatbelt (sandbox-exec): [green]available[/green]")
	}

	// NIX IS REQUIRED FOR EVERY LAUNCH, not only for a config that declares
	// `packages:`. This warning said "configs with packages get no declared tools"
	// while the closure was built under `if len(pkgs) > 0`; since the FLOOR landed
	// (2026-09-12) the build is unconditional and a launch that cannot run nix is
	// REFUSED host-side, so the old wording told the owner of an empty
	// `yolo-jail.jsonc` they were ready when no launch could start.
	if !deps.Which("nix") {
		// The getting-started guide's install for this Mac's chip, the one `yolo check` names
		// too (storage.NixInstall). It used to be a link to nixos.org's download page, which
		// leaves the choice of installer to the reader.
		intro, cmds := storage.NixInstall(runtime.GOARCH == "amd64")
		warnings = append(warnings,
			"nix not found on PATH — every launch materializes the package floor "+
				"(mise, node, git, ripgrep, …) plus any `packages:` via native "+
				"nix, so without it NO launch starts, not just one that declares "+
				"packages. "+intro+":\n      "+strings.Join(cmds, "\n      ")+
				"\n    Then open a new terminal, so nix is on its PATH.")
		out.print("• nix (native package floor; required by every launch): " +
			"[yellow]not found[/yellow]")
	} else {
		out.print("• nix (native package floor; required by every launch): " +
			"[green]available[/green]")
	}

	// 3. One clear verdict + next steps.
	out.print("")
	if len(warnings) > 0 {
		out.print("[bold yellow]⚠ Setup done, but the macos-user backend is not " +
			"ready to run yet:[/bold yellow]")
		for _, w := range warnings {
			out.printf("  • %s", w)
		}
		out.print("\nResolve the above, then verify with " +
			"[bold]yolo run --dry-run[/bold] (prints the full plan; needs no " +
			"further setup).")
	} else {
		out.printf("[bold green]✓ macos-user backend ready.[/bold green] "+
			"The yolo account '%s' is provisioned and preconditions "+
			"pass.", SandboxUser)
		out.printf("Next: put your project under [bold]%s/"+
			"<name>[/bold] (the agent can only share neutral ground, never a "+
			"path inside your home), make the yolo account's setup your default "+
			"([bold]yolo config set setup seatbelt-yolo-account --user[/bold]), then from that "+
			"directory run [bold]yolo run --dry-run[/bold] to preview, or "+
			"[bold]yolo[/bold] to launch.\n"+
			"Launches start and stop with no password from now on.", SharedRootDefault())

	}
	// NAME THE RETROFIT, and do it OUTSIDE the verdict branches — it belongs on
	// every setup, warnings or not. It sat inside the no-warnings arm until
	// 2026-09-03, so a machine missing nix (the common first-run state) got the
	// warning list and never this line, which is precisely the machine whose owner
	// is about to move a project in and hit the failure it prevents.
	//
	// Printed whether or not anything needed sharing just now: macOS grants the
	// shared ACL when a directory is CREATED, so a project MOVED or copied into the
	// root later arrives unshared, and the symptom is `permission denied` partway
	// through a launch with no hint of its cause. This is the one moment the user is
	// reading about how sharing works, so it is where the remedy has to be
	// learnable — a command mentioned only in a failure is one the reader meets at
	// their worst moment.
	out.printf("\n[dim]If you MOVE an existing project into %s later, share it with "+
		"[bold]yolo yolo-account share <path>[/bold] (no path = all of them, and no "+
		"password needed). Moving a directory does not re-apply the ACL the way "+
		"creating one does. Idempotent — safe to run any time something under there is "+
		"unexpectedly unwritable.[/dim]", SharedRootDefault())
	return 0
}

// AccountRemove deletes the yolo account + home (needs admin). macOS only.
func AccountRemove(deps Deps) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		refuseOffMacOS(out, "remove")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}
	if !deps.SandboxUserExists() {
		// An account removed by hand leaves this user's enrollment behind (PL-3: the enrolled
		// user is whoever owns the staging root), and this command is the remedy setup's
		// second-user refusal names, so it still removes the rule and the root it owns.
		if JudgeStagingRoot(rootFacts(deps, StagingRoot()), deps.Geteuid()) == StagingRootOK {
			out.printf("The yolo account '%s' does not exist; removing your enrollment.", SandboxUser)
			announceSudo(deps, out, "remove yolo's sudo rule ("+SudoersFile+")")
			removeEnrollment(deps, out)
			return 0
		}
		out.printf("The yolo account '%s' does not exist — nothing to do.", SandboxUser)
		return 0
	}
	announceSudo(deps, out, "remove the yolo account and its rule ("+SudoersFile+")")
	for _, cmd := range DeleteUserCommands(deps.HostUser()) {
		deps.Run(rootArgv(cmd))
	}
	out.printf("[green]✓ Removed the yolo account '%s'.[/green]", SandboxUser)
	removeEnrollment(deps, out)
	// One line about what survives the account, because it is not obvious: an ACL
	// names a UUID, so the grants naming this account outlive it and go inert. Not
	// stripped here — a teardown is often temporary, and walking every workspace to
	// remove grants the user may want back in an hour is the wrong default; a later
	// `yolo-account create` re-applies them for the current account anyway.
	out.printf("[dim]ACL entries naming it remain under %s and are now inert (an ACL "+
		"names a UUID, not a name). A later `yolo yolo-account create` re-applies them; "+
		"`yolo yolo-account unshare <workspace>` removes them now.[/dim]", SharedRootDefault())
	return 0
}

// AccountUnshare strips the yolo-jail ACLs from a shared workspace (chmod -h -N).
// macOS only. The workspace path is resolved (abs + symlinks) first.
func AccountUnshare(deps Deps, workspace string) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		refuseOffMacOS(out, "unshare")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}
	ws := resolvePathAbs(workspace)
	if !deps.PathIsDir(ws) {
		out.printf("[bold red]Not a directory:[/bold red] %s", ws)
		return 1
	}
	if deps.RunBash(WorkspaceACLStripScript(ws)) != 0 {
		out.printf("[yellow]ACL strip reported an error on %s.[/yellow]", ws)
		return 1
	}
	out.printf("[green]✓ Stripped yolo-jail ACLs from %s.[/green]", ws)
	return 0
}

// AccountShare retrofits the shared-group ACL onto pre-existing files in
// the shared area. macOS only. With no path it defaults to the whole shared root.
func AccountShare(deps Deps, path string) int {
	out := printer{w: deps.Out, color: deps.Color}
	if !deps.IsMacOS() {
		refuseOffMacOS(out, "share")
		return 1
	}
	if rc := refuseIfRoot(deps); rc != 0 {
		return rc
	}
	target := SharedRootDefault()
	if path != "" {
		target = resolvePathAbs(path)
	}
	if !deps.PathIsDir(target) {
		out.printf("[bold red]Not a directory:[/bold red] %s", target)
		return 1
	}
	if _, inHome := HomeContaining(target); inHome {
		out.printf("[bold red]%s is inside a user home[/bold red] — the "+
			"macos-user backend only manages ACLs on neutral ground "+
			"(under %s or another non-home root).", target, SharedRootDefault())
		return 1
	}
	if failed, rc := shareTree(deps, target); rc != 0 {
		reportACLFailures(out, target, deps.HostUser(), failed)
		return 1
	}
	out.printf("[green]✓ Applied shared-group ACLs under %s.[/green]", target)
	return 0
}

// refuseIfRoot fails fast if invoked as root (under sudo). The macos-*
// commands self-escalate; running the whole command under sudo misassigns the
// shared-group ACL to root. Returns 1 (with the message printed) when euid==0,
// else 0.
func refuseIfRoot(deps Deps) int {
	if deps.Geteuid() == 0 {
		printer{w: deps.Out, color: deps.Color}.print(
			"[bold red]Don't run this under sudo.[/bold red]  Run it as your " +
				"normal admin user — it escalates each privileged step itself " +
				"(prompting for your password).  Running the whole command as root " +
				"would grant the shared-workspace ACL to 'root' instead of you and " +
				"silently break host↔sandbox file sharing.")
		return 1
	}
	return 0
}

// accountNameOfUID names the account a uid belongs to, "" when it cannot. primaryGroupOf names a
// user's primary group, "" when it cannot. Variables so a test can name its fixture's accounts;
// production asks the directory service (real.go).
var (
	accountNameOfUID = accountNameOfUIDReal
	primaryGroupOf   = primaryGroupOfReal
)

// The administrator act G0's tools, by absolute path (point 4 of the 2026-10-15 walkthrough in
// docs/design/macos-user-privilege-lifecycle.md).
const (
	visudoBin = "/usr/sbin/visudo"
	grepBin   = "/usr/bin/grep"
	trueBin   = "/usr/bin/true"
)

// issuesURL is where a refusal that is yolo's own fault sends the report.
const issuesURL = "https://github.com/mschulkind-oss/yolo-jail/issues"

// firstHumanUID is the lowest uid macOS gives an account a person logs in to.
const firstHumanUID = 501

// rootArgv is cmd run as root through sudo, which only the administrator act G0 (setup and
// teardown) does; every routine sudo is RunAsSandboxArgv's.
func rootArgv(cmd []string) []string { return append([]string{sudoBin}, cmd...) }

// rootFacts asks deps about a root, a nil probe being one that could not look.
func rootFacts(deps Deps, path string) StagingRootFacts {
	if deps.StagingRootFacts == nil {
		return StagingRootFacts{Err: errors.New("no probe for " + path)}
	}
	return deps.StagingRootFacts(path)
}

// ownerName is how a refusal names the account owning a root.
func ownerName(uid uint32) string {
	if name := accountNameOfUID(uid); name != "" {
		return name
	}
	if uid == 0 {
		return "root"
	}
	return "uid " + strconv.FormatUint(uint64(uid), 10)
}

// refuseForeignEnrollment refuses, before any sudo, a staging root or capture root setup must not
// adopt. A staging root another person's account owns is that person's enrollment (one user per
// Mac); one any other account owns, the yolo account first among them, is a squat, because
// whoever owns it controls what the sandbox reads. The capture root may be root's, which captures
// before the one-rule contract left behind: setup gives that one back (ensureHostOwnedRoot).
func refuseForeignEnrollment(deps Deps, out printer) int {
	euid := deps.Geteuid()
	for _, c := range []struct {
		path       string
		rootMayOwn bool
	}{{StagingRoot(), false}, {CaptureRootDefault(), true}} {
		f := rootFacts(deps, c.path)
		switch JudgeStagingRoot(f, euid) {
		case StagingRootAbsent, StagingRootOK:
			continue
		case StagingRootUnknown:
			out.printf("[bold red]Could not inspect %s (%v).[/bold red] Setup does not stage into what "+
				"it cannot see. Inspect it with `ls -le %s`, then run [bold]yolo yolo-account create[/bold] again.",
				c.path, f.Err, c.path)
			return 1
		case StagingRootUnsafe:
			if !f.IsSymlink && f.IsDir {
				continue // this user's directory, its mode or access list reset below
			}
			out.printf("[bold red]%s is a symbolic link or not a directory.[/bold red] Setup does not "+
				"stage through it. Inspect it with `ls -le %s`, remove it with `sudo rm -rf %s`, then "+
				"run [bold]yolo yolo-account create[/bold] again.", c.path, c.path, c.path)
			return 1
		case StagingRootForeign:
			if c.rootMayOwn && f.UID == 0 && f.IsDir {
				continue
			}
			name := accountNameOfUID(f.UID)
			if c.path == StagingRoot() && f.UID >= firstHumanUID && name != SandboxUser {
				refuseSecondUser(out, ownerName(f.UID))
				return 1
			}
			out.printf("[bold red]%s already exists and belongs to %s, not to you.[/bold red] Setup "+
				"does not adopt it: whoever owns it controls what the sandbox reads. Inspect it with "+
				"`ls -leR %s`, remove it with `sudo rm -rf %s`, then run [bold]yolo yolo-account create[/bold] "+
				"again.", c.path, ownerName(f.UID), c.path, c.path)
			return 1
		}
	}
	return 0
}

// refuseSecondUser prints the one-user-per-Mac refusal, naming the enrolled user.
func refuseSecondUser(out printer, name string) {
	out.printf("[bold red]This Mac's macos-user backend is set up for another macOS user (%s).[/bold red] "+
		"One user per Mac. That user runs [bold]yolo yolo-account remove[/bold], then you run "+
		"[bold]yolo yolo-account create[/bold]. If that account is gone, an administrator runs "+
		"`sudo rm -f %s && sudo rm -rf %s`, then you run [bold]yolo yolo-account create[/bold].",
		name, SudoersFile, StagingRoot())
}

// announceSudo says why sudo is about to ask for a password, before the first sudo that may ask.
// It asks `sudo -n` first and says nothing when that runs: the credentials are cached, so no
// prompt comes, and a line announcing one would be wrong. The probe runs captured, so sudo's
// "a password is required" does not reach the terminal.
func announceSudo(deps Deps, out printer, why string) {
	if _, rc := runQuietly(deps, []string{sudoBin, "-n", trueBin}); rc == 0 {
		return
	}
	out.printf("• sudo needs your password to %s.", why)
}

// refuseAnotherUsersRule asks whether the installed rule is this user's, through sudo because the
// file is 0440 root (PL-3): grep exits 0 for this user's rule and 2 when there is no file, and
// setup goes on for both; 1 is a file without this user's rule, which is another user's
// enrollment. -s keeps grep quiet about the missing file, the normal first run; it changes no
// exit status. ⚠ sudo also exits 1 when it could not authenticate, so a 1 is read as grep's only
// once `sudo -n` shows sudo did run (the credentials just given are cached).
func refuseAnotherUsersRule(deps Deps, out printer, user string) int {
	ours := user + " ALL=(" + SandboxUser + ") NOPASSWD: " + envBin
	if deps.Run(rootArgv([]string{grepBin, "-s", "-qxF", "-e", ours, SudoersFile})) != 1 {
		return 0
	}
	if deps.Run([]string{sudoBin, "-n", trueBin}) != 0 {
		out.printf("[bold red]✗ sudo did not run the check of %s, so nothing was changed.[/bold red] "+
			"Run [bold]yolo yolo-account create[/bold] again and give your admin password when sudo asks.",
			SudoersFile)
		return 1
	}
	refuseSecondUser(out, "named in "+SudoersFile)
	return 1
}

// ensureHostOwnedRoot makes path a 0755 directory the host user owns with no access list (PL-6):
// created as the host user when absent (/Users/Shared is 1777, and a plain mkdir fails rather
// than adopt one a squatter made in between), given back with sudo when rootMayOwn and root owns
// it, and its mode and access list reset either way. refuseForeignEnrollment has already refused
// every other owner.
func ensureHostOwnedRoot(deps Deps, out printer, path, user string, rootMayOwn bool) int {
	f := rootFacts(deps, path)
	var cmds [][]string
	switch {
	case !f.Exists:
		cmds = append(cmds, []string{mkdirBin, "-m", categoryDirMode, path})
	case rootMayOwn && f.UID == 0:
		owner := user
		if g := primaryGroupOf(user); g != "" {
			owner += ":" + g
		}
		cmds = append(cmds, rootArgv([]string{chownBin, owner, path}))
	}
	cmds = append(cmds, []string{chmodBin, categoryDirMode, path}, []string{chmodBin, "-N", path})
	for _, cmd := range cmds {
		if deps.Run(cmd) != 0 {
			out.printf("[bold red]✗ setup step failed:[/bold red] %s", shquote.JoinDisplay(cmd))
			return 1
		}
	}
	out.printf("• [bold]%s[/bold] is yours, mode %s, with no access list.", path, categoryDirMode)
	return 0
}

// aclPathsShown caps the paths reportACLFailures puts on its command lines; a re-run after the
// fix lists any left.
const aclPathsShown = 20

// reportACLFailures is the stop for a shared-group ACL walk under root that did not finish:
// what failed, then the commands that show and fix it, with the real paths.
func reportACLFailures(out printer, root, user string, failed []string) {
	out.blank()
	if len(failed) == 0 {
		out.printf("[bold yellow]⚠ The shared-group ACL walk under %s failed (its error is above).[/bold yellow]",
			richtext.Escape(root))
		out.blank()
		out.commands(shquote.JoinDisplay([]string{"yolo", "yolo-account", "share", root}))
		out.blank()
		return
	}
	out.printf("[bold yellow]⚠ %d paths under %s did not get the shared-group ACL.[/bold yellow]",
		len(failed), richtext.Escape(root))
	out.blank()
	shown := failed
	if len(shown) > aclPathsShown {
		shown = shown[:aclPathsShown]
	}
	out.commands(shquote.JoinDisplay(append([]string{"ls", "-ledO"}, shown...)))
	out.blank()
	out.print("Make them yours, then run the walk again:")
	out.blank()
	owner := user
	if owner == "" {
		owner = "$USER"
	}
	out.commands(
		"sudo chown "+owner+" "+shquote.JoinDisplay(shown),
		shquote.JoinDisplay([]string{"yolo", "yolo-account", "share", root}),
	)
	if len(failed) > len(shown) {
		out.printf("[dim]  (%d more not shown; the re-run lists them)[/dim]", len(failed)-len(shown))
	}
	out.blank()
}

// installSudoersRule installs rule as SudoersFile (PL-4): written as the host into a temporary
// file, copied by root under the inert staging name, owned root:wheel 0440, checked by visudo,
// renamed into place, and the whole configuration checked again. Anything that fails leaves no
// rule behind.
func installSudoersRule(deps Deps, out printer, rule string) int {
	stepFailed := func(cmd []string) int {
		out.printf("[bold red]✗ setup step failed:[/bold red] %s", shquote.JoinDisplay(cmd))
		return 1
	}
	// stop prints a refusal's headline and visudo's output, each set off by a blank line.
	stop := func(headline, output string) {
		out.blank()
		out.print("[bold red]✗ " + headline + "[/bold red]")
		if strings.TrimSpace(output) != "" {
			out.blank()
			out.verbatim(output)
		}
		out.blank()
	}
	reportBug := func() int {
		out.print("This is a yolo bug. Report it with the output above and this rule:")
		out.blank()
		out.verbatim(rule)
		out.blank()
		out.commands(issuesURL)
		out.blank()
		return 1
	}
	dir, err := os.MkdirTemp("", "yolo-sudoers-")
	if err != nil {
		out.printf("[bold red]✗ setup step failed:[/bold red] could not make a temporary directory (%v)", err)
		return 1
	}
	defer os.RemoveAll(dir)
	tmp := filepath.Join(dir, "yolo-jail")
	if !deps.WriteHostFile(tmp, rule, 0o644) {
		out.printf("[bold red]✗ setup step failed:[/bold red] could not write %s", tmp)
		return 1
	}
	dropStaged := rootArgv([]string{rmBin, "-f", SudoersStagingFile})
	for _, cmd := range [][]string{
		rootArgv([]string{cpBin, tmp, SudoersStagingFile}),
		rootArgv([]string{chownBin, "root:wheel", SudoersStagingFile}),
		rootArgv([]string{chmodBin, "0440", SudoersStagingFile}),
	} {
		if deps.Run(cmd) != 0 {
			deps.Run(dropStaged)
			return stepFailed(cmd)
		}
	}
	if output, rc := runQuietly(deps, rootArgv([]string{visudoBin, "-c", "-f", SudoersStagingFile})); rc != 0 {
		deps.Run(dropStaged)
		stop("visudo rejected yolo's sudo rule. Nothing was installed.", output)
		return reportBug()
	}
	// notInstalled is the stop for a failed move of the checked rule into place: the rule is not
	// installed, the complaints are fixed first, and setup runs again.
	notInstalled := func(mv []string, complaints []string) int {
		deps.Run(dropStaged)
		stop("yolo's sudo rule is not installed: "+shquote.JoinDisplay(mv)+" failed.",
			strings.Join(complaints, "\n"))
		out.commands(append(sudoersRemedies(complaints), "yolo yolo-account create")...)
		out.blank()
		return 1
	}
	if mv := rootArgv([]string{mvBin, "-f", SudoersStagingFile, SudoersFile}); deps.Run(mv) != 0 {
		return notInstalled(mv, nil)
	}
	configCheck := rootArgv([]string{visudoBin, "-c"})
	withRule, rc := runQuietly(deps, configCheck)
	if rc == 0 {
		out.printf("• Installed [bold]%s[/bold]; visudo accepts it.", SudoersFile)
		return 0
	}
	// The whole configuration fails with the rule in place. Setup parks the rule under the inert
	// staging name, checks again, and compares: the rule stays only if it adds no failure.
	if park := rootArgv([]string{mvBin, "-f", SudoersFile, SudoersStagingFile}); deps.Run(park) != 0 {
		stop("visudo rejected your sudo configuration with yolo's rule in place, and removing the "+
			"rule failed.", withRule)
		out.print("Remove the rule, then run setup again:")
		out.blank()
		out.commands("sudo rm -f " + SudoersFile)
		out.print("  [dim]# or, if sudo itself fails:[/dim]")
		out.commands(
			`osascript -e 'do shell script "/bin/rm -f `+SudoersFile+`" with administrator privileges'`,
			"yolo yolo-account create",
		)
		out.blank()
		return 1
	}
	without, rc := runQuietly(deps, configCheck)
	if rc == 0 {
		deps.Run(dropStaged)
		stop("visudo rejected your sudo configuration with yolo's rule in place. The rule was "+
			"removed again.", withRule)
		return reportBug()
	}
	pre := sudoersComplaints(without)
	added := complaintsAdded(sudoersComplaints(withRule), pre)
	if len(pre) > 0 && len(added) == 0 {
		// Every failure is one the configuration had before yolo's rule. sudo itself reads a
		// sudoers.d file that is root's and not writable by others, whatever its other mode bits,
		// and skips one it cannot trust without dropping the rest; only visudo insists on 0440. So
		// the rule goes back in, and proveRunAsGrant then shows sudo honors it.
		if mv := rootArgv([]string{mvBin, "-f", SudoersStagingFile, SudoersFile}); deps.Run(mv) != 0 {
			return notInstalled(mv, pre)
		}
		out.blank()
		out.printf("[bold yellow]⚠ Installed %s. visudo -c also fails without it:[/bold yellow]", SudoersFile)
		out.blank()
		out.verbatim(strings.Join(pre, "\n"))
		out.blank()
		out.commands(append(sudoersRemedies(pre), "sudo visudo -c")...)
		out.blank()
		return 0
	}
	deps.Run(dropStaged)
	if len(added) > 0 {
		stop("visudo rejected your sudo configuration with yolo's rule in place. The rule was "+
			"removed again.", strings.Join(added, "\n"))
		return reportBug()
	}
	stop("Your sudo configuration is invalid without yolo's rule too. Nothing was installed.", without)
	out.print("Fix it, then run setup again:")
	out.blank()
	out.commands(append(sudoersRemedies(pre), "sudo visudo -c", "yolo yolo-account create")...)
	out.blank()
	return 1
}

// runQuietly runs a check whose output the user needs only when it fails, returning that
// output (deps.RunCapture). With no RunCapture it runs through deps.Run, the output printed as
// it comes and none returned.
func runQuietly(deps Deps, argv []string) (string, int) {
	if deps.RunCapture == nil {
		return "", deps.Run(argv)
	}
	return deps.RunCapture(argv)
}

// sudoersComplaints is each line of `visudo -c` output that reports a problem: every non-blank
// line but the "<file>: parsed OK" ones, trimmed, in order.
func sudoersComplaints(output string) []string {
	var lines []string
	for _, l := range strings.Split(output, "\n") {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasSuffix(l, ": parsed OK") {
			lines = append(lines, l)
		}
	}
	return lines
}

// complaintsAdded is each complaint in with that without lacks.
func complaintsAdded(with, without []string) []string {
	var added []string
	for _, l := range with {
		if !slices.Contains(without, l) {
			added = append(added, l)
		}
	}
	return added
}

// The two complaints visudo's permission check prints, after the file's path
// (plugins/sudoers/visudo.c, check_file: "%s: bad permissions, should be mode 0%o" and
// "%s: wrong owner (uid, gid) should be (%u, %u)"), an older visudo prefixing "visudo: ".
var (
	visudoBadMode  = regexp.MustCompile(`^(?:visudo: )?(/[^\s:]+): bad permissions, should be mode (0[0-7]+)$`)
	visudoBadOwner = regexp.MustCompile(`^(?:visudo: )?(/[^\s:]+): wrong owner \(uid, gid\) should be \((\d+), (\d+)\)$`)
)

// visudoBlame matches the file any other visudo complaint names: `>>> /etc/sudoers.d/x: syntax
// error near line 3 <<<`, `/etc/sudoers.d/x:3:5: syntax error`, `visudo: /etc/sudoers.d/x: …`
// and `/etc/sudoers.d/x: …`.
var visudoBlame = regexp.MustCompile(`^(?:>>> |visudo: )?(/[^\s:]+):`)

// sudoersRemedies is the commands that fix complaints, at the paths visudo printed: one chmod
// per mode for every file at a bad mode, one chown per owner for every file with a wrong owner,
// then `sudo visudo -f` for each other file blamed. SudoersFile is never named: setup has
// installed it or taken it out by the time this is printed.
func sudoersRemedies(complaints []string) []string {
	type group struct {
		verb, arg string
		files     []string
	}
	var groups []*group
	add := func(verb, arg, file string) {
		for _, g := range groups {
			if g.verb == verb && g.arg == arg {
				if !slices.Contains(g.files, file) {
					g.files = append(g.files, file)
				}
				return
			}
		}
		groups = append(groups, &group{verb, arg, []string{file}})
	}
	var edit []string
	ours := func(f string) bool { return f == SudoersFile || f == "/private"+SudoersFile }
	for _, l := range complaints {
		if m := visudoBadMode.FindStringSubmatch(l); m != nil {
			if !ours(m[1]) {
				add("chmod", m[2], m[1])
			}
			continue
		}
		if m := visudoBadOwner.FindStringSubmatch(l); m != nil {
			if !ours(m[1]) {
				owner := m[2] + ":" + m[3]
				if owner == "0:0" {
					owner = "root:wheel"
				}
				add("chown", owner, m[1])
			}
			continue
		}
		if m := visudoBlame.FindStringSubmatch(l); m != nil && !ours(m[1]) && !slices.Contains(edit, m[1]) {
			edit = append(edit, m[1])
		}
	}
	var cmds []string
	for _, verb := range []string{"chmod", "chown"} {
		for _, g := range groups {
			if g.verb == verb {
				cmds = append(cmds, shquote.JoinDisplay(append([]string{"sudo", g.verb, g.arg}, g.files...)))
			}
		}
	}
	for _, f := range edit {
		cmds = append(cmds, shquote.JoinDisplay([]string{"sudo", "visudo", "-f", f}))
	}
	return cmds
}

// sweepLegacySessions is the administrator's half of PL-8: for each session record this user's
// launches kept because an older yolo's session left root-owned files in LegacyStateDir, and only
// once that session has ended (its record is free), it removes those exact five files with sudo,
// then the record. The launch sweep (sweepGoneSessions) decides the same way and keeps what is
// left here. A live session, a session with no legacy profile, and the legacy guest trees are
// never touched: `yolo stores` lists those trees, with the command that removes them.
func sweepLegacySessions(deps Deps, out printer) {
	if deps.SessionRecordDir != nil && deps.PathExists != nil {
		if dir := deps.SessionRecordDir(); dir != "" {
			sweepLegacyRecords(deps, out, dir)
		}
	}
	if deps.PathIsDir != nil && deps.PathIsDir(LegacyStateDir()) {
		out.printf("[dim]%s, from an older yolo, stays: sessions it started may still use it. "+
			"`yolo stores` lists what it holds, with the command that removes it.[/dim]", LegacyStateDir())
	}
}

func sweepLegacyRecords(deps Deps, out printer, dir string) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+sessionRecordSuffix))
	if err != nil {
		return
	}
	removed := 0
	for _, path := range matches {
		key := strings.TrimSuffix(filepath.Base(path), sessionRecordSuffix)
		if _, _, ok := parseSessionKey(key); !ok {
			continue
		}
		f, state := claimGoneSessionRecord(path)
		if state != sessionGone {
			continue
		}
		if !deps.PathExists(SessionProfilePath(key, LegacyStateDir())) {
			_ = f.Close() // this yolo's own session: the launch sweep's
			continue
		}
		argv := rootArgv(append([]string{rmBin, "-f"}, SessionFilePaths(key, LegacyStateDir())...))
		if deps.Run(argv) != 0 {
			out.printf("[yellow]Warning: could not remove the files an older yolo's session left in "+
				"%s[/yellow] (`%s` failed). Run [bold]yolo yolo-account create[/bold] again, or that command.",
				LegacyStateDir(), shquote.JoinDisplay(argv))
			_ = f.Close()
			continue
		}
		_ = os.Remove(path)
		_ = f.Close()
		removed++
	}
	if removed > 0 {
		out.printf("• Removed the root-owned files of %d ended session(s) an older yolo left in %s.",
			removed, LegacyStateDir())
	}
}

// proveRunAsGrant is readiness probe G2 as the setup principal, after `sudo -k`: the credentials
// setup just used would otherwise satisfy `sudo -n` through the admin's own broad rule and hide a
// rule sudo never reads. Through deps.Run, not deps.RunAsGrant, so the order is the one recorded.
func proveRunAsGrant(deps Deps, out printer) int {
	deps.Run([]string{sudoBin, "-k"})
	if deps.Run(RunAsGrantProbeArgv()) != 0 {
		out.printf("[bold red]✗ The rule is installed, but sudo still asks for a password to run as "+
			"%s.[/bold red] Check that /etc/sudoers reads /private/etc/sudoers.d (`sudo grep -n "+
			"includedir /etc/sudoers`); a managed Mac may block it, and then your administrator has to "+
			"allow the rule. Run [bold]yolo yolo-account create[/bold] again afterwards.", SandboxUser)
		return 1
	}
	out.printf("• `%s` runs with no password and an empty sudo cache.",
		strings.Join(RunAsGrantProbeArgv(), " "))
	return 0
}

// removeEnrollment is teardown's undo of setup's enrollment (PL-5): the rule, with sudo, then the
// staging root, as the host user, when it is this user's. The legacy state dir is left, as setup
// leaves it.
func removeEnrollment(deps Deps, out printer) {
	rm := rootArgv([]string{rmBin, "-f", SudoersFile})
	if deps.Run(rm) != 0 {
		out.printf("[yellow]Could not remove %s[/yellow]; remove it with `%s`.", SudoersFile,
			shquote.JoinDisplay(rm))
	} else {
		out.printf("[green]✓ Removed the run-as rule %s.[/green]", SudoersFile)
	}
	root := StagingRoot()
	f := rootFacts(deps, root)
	switch {
	case !f.Exists && f.Err == nil:
	case f.Err == nil && f.IsDir && !f.IsSymlink && f.UID == uint32(deps.Geteuid()):
		ok := true
		for _, cmd := range removeSealedCommands(root) {
			if deps.Run(cmd) != 0 {
				ok = false
				break
			}
		}
		if ok {
			out.printf("[green]✓ Removed the staging directory %s.[/green]", root)
		} else {
			out.printf("[yellow]Could not remove %s[/yellow]; remove it with `%s -R u+w %s && %s -rf %s`.",
				root, chmodBin, root, rmBin, root)
		}
	default:
		out.printf("[yellow]%s is not yours, so teardown left it.[/yellow] An administrator inspects it "+
			"with `ls -leR %s` and removes it with `sudo rm -rf %s`.", root, root, root)
	}
	if deps.PathIsDir != nil && deps.PathIsDir(LegacyStateDir()) {
		out.printf("[dim]%s, from an older yolo, is left; `yolo stores` lists it, with the command "+
			"that removes it.[/dim]", LegacyStateDir())
	}
}

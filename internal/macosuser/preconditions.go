package macosuser

import (
	"errors"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// preconditions.go is THE list of machine and workspace conditions a macos-user launch refuses
// without, asked before the nix build. RunMacosUser asks them in this order and stops at the
// first that does not hold; `yolo check` asks all of them and reports each as a row. One list
// for both, because the check used to keep its own: it passed a Mac whose workspace sat under
// the user's home, whose sandbox home had been deleted, or whose project had been moved in
// without its ACL, and graded a missing sandbox account as a warning — every one of which the
// launch refuses.
//
// WHAT IS NOT HERE, and why. The launch refuses later for reasons that are not a fact about
// the machine or the workspace's place on it: the nix build itself (yolo check probes for
// `nix` on its own, since the build is not cheap enough to run as a check), an unapproved
// config change, a selected pack's missing credential, a host service that did not start, and
// the plan invariants, which catch yolo bugs rather than setup a user can fix. The repo-root
// refusal is run.Run's and yolo check reports it at the top of its report.

// LaunchProbes is what the preconditions read off the machine: the subset of Deps they need,
// so a caller that is not a launch can answer the same questions with its own seams.
type LaunchProbes struct {
	IsMacOS           func() bool
	Geteuid           func() int
	Which             func(name string) bool
	SandboxUserExists func() bool
	PathIsDir         func(path string) bool
	// RunBash runs a bash script and returns its exit status.
	RunBash func(script string) int
	// StagingRoot observes the staging root at path without following a link
	// (stagingRootFactsReal); JudgeStagingRoot decides it. nil is a root that could not be
	// inspected, which refuses.
	StagingRoot func(path string) StagingRootFacts
	// RunAsGrant reports whether readiness probe G2 (RunAsGrantProbeArgv) exits 0. nil answers
	// no, which refuses.
	RunAsGrant func() bool
}

// errNoStagingRootProbe is the facts of a caller that wired no StagingRoot probe: unknown, which
// refuses, never a pass.
var errNoStagingRootProbe = errors.New("no staging-root probe")

// stagingRootVerdict is JudgeStagingRoot over this machine's staging root, as the caller runs.
func (p LaunchProbes) stagingRootVerdict() StagingRootVerdict {
	f := StagingRootFacts{Err: errNoStagingRootProbe}
	if p.StagingRoot != nil {
		f = p.StagingRoot(StagingRoot())
	}
	return JudgeStagingRoot(f, p.Geteuid())
}

// launchProbes is the launch's own answer to every question below.
func (d Deps) launchProbes() LaunchProbes {
	return LaunchProbes{
		IsMacOS:           d.IsMacOS,
		Geteuid:           d.Geteuid,
		Which:             d.Which,
		SandboxUserExists: d.SandboxUserExists,
		PathIsDir:         d.PathIsDir,
		RunBash:           d.RunBash,
		StagingRoot:       d.StagingRootFacts,
		RunAsGrant:        d.RunAsGrant,
	}
}

// Precondition IDs, for the Requires lists and for callers that key on one.
const (
	PreconditionMacOS            = "macos"
	PreconditionNotRoot          = "not-root"
	PreconditionSeatbelt         = "sandbox-exec"
	PreconditionSandboxUser      = "sandbox-user"
	PreconditionSandboxHome      = "sandbox-home"
	PreconditionStagingRoot      = "staging-root"
	PreconditionStagingRootOwner = "staging-root-owner"
	PreconditionStagingRootSafe  = "staging-root-safe"
	PreconditionRunAsGrant       = "run-as-grant"
	PreconditionNeutralGround    = "workspace-location"
	PreconditionWorkspaceShared  = "workspace-shared"
)

// Precondition is one condition a macos-user launch refuses without.
//
// Every text is a function of the workspace, which callers pass RESOLVED (resolvePathAbs):
// the in-home rule judges the resolved path, and a remedy has to name the path it acts on.
type Precondition struct {
	// ID names it (the Precondition* constants).
	ID string
	// Name is how a report refers to it when it was not checked ("the sandbox home").
	Name string
	// Requires lists the preconditions whose failure makes this one's probe meaningless or its
	// remedy wrong. Each is earlier in the list, which is what lets the launch simply stop at
	// the first failure: by the time a precondition is asked, what it requires has held.
	Requires []string
	// Ready is the report line when it holds; Unmet the line when it does not, and Fix the
	// remedy beside it. Plain text: yolo check renders them.
	Ready, Unmet, Fix func(workspace string) string
	// Refusal is the launch's whole message when it does not hold, in rich markup.
	Refusal func(workspace string) string

	holds func(p LaunchProbes, workspace string) bool
}

// Holds asks the machine.
func (c Precondition) Holds(p LaunchProbes, workspace string) bool { return c.holds(p, workspace) }

// LaunchPreconditions returns the list, in the order the launch asks it.
func LaunchPreconditions() []Precondition {
	return []Precondition{
		{
			// Fail closed BEFORE any subprocess when we can't run here.
			ID: PreconditionMacOS, Name: "macOS",
			holds: func(p LaunchProbes, _ string) bool { return p.IsMacOS() },
			Ready: func(string) string { return "Running on macOS" },
			Unmet: func(string) string { return "runtime 'macos-user' requires macOS" },
			Fix: func(string) string {
				return "It isolates the agent in a dedicated macOS user account; use 'podman' or " +
					"'container' on this host. `yolo --dry-run` still prints the plan here."
			},
			Refusal: func(string) string {
				return "[bold red]runtime 'macos-user' requires macOS.[/bold red] " +
					"Use 'podman' or 'container' on this host.\n" +
					"[dim]Tip: `yolo run --dry-run` prints the full plan on any OS.[/dim]"
			},
		},
		{
			// Must NOT be run under sudo: the launch stages as the host user and crosses into the
			// sandbox through the one run-as rule, and running as root makes the host user
			// 'root', misassigning the git identity + ACL.
			ID: PreconditionNotRoot, Name: "running as your own user",
			Requires: []string{PreconditionMacOS},
			holds:    func(p LaunchProbes, _ string) bool { return p.Geteuid() != 0 },
			Ready:    func(string) string { return "Running as your own user, not root" },
			Unmet:    func(string) string { return "yolo is running as root (under sudo)" },
			Fix: func(string) string {
				return "Run `yolo` as your normal user: a launch needs no root, and run as root it " +
					"would give root's git identity and ACL to the sandbox."
			},
			Refusal: func(string) string {
				return "[bold red]Don't run `yolo` under sudo for the macos-user " +
					"backend.[/bold red]  It needs no root, and running as root breaks " +
					"the per-user identity/ACL. Run it as your normal user."
			},
		},
		{
			ID: PreconditionSeatbelt, Name: "Apple Seatbelt (sandbox-exec)",
			Requires: []string{PreconditionMacOS},
			holds:    func(p LaunchProbes, _ string) bool { return p.Which("sandbox-exec") },
			Ready:    func(string) string { return "Apple Seatbelt (sandbox-exec) available" },
			Unmet:    func(string) string { return "sandbox-exec not found" },
			Fix: func(string) string {
				return "Seatbelt ships with macOS as /usr/bin/sandbox-exec; a missing one means " +
					"/usr/bin is not on PATH."
			},
			Refusal: func(string) string {
				return "[bold red]sandbox-exec not found[/bold red] — the macos-user " +
					"backend needs Apple Seatbelt (built into macOS)."
			},
		},
		{
			ID: PreconditionSandboxUser, Name: "the yolo account",
			Requires: []string{PreconditionMacOS},
			holds:    func(p LaunchProbes, _ string) bool { return p.SandboxUserExists() },
			Ready:    func(string) string { return "The yolo account '" + SandboxUser + "' exists" },
			Unmet:    func(string) string { return "The yolo account '" + SandboxUser + "' does not exist" },
			Fix: func(string) string {
				return "Run the one-time setup, as your normal user: yolo yolo-account create"
			},
			Refusal: func(string) string {
				return "[bold red]The yolo account '" + SandboxUser + "' does not exist.[/bold red]\n" +
					"Run the one-time setup to create it (`yolo yolo-account create`; see " +
					"`docs/reference/macos-no-vm-direction.md`)."
			},
		},
		{
			// THE HOME IS A SEPARATE FACT FROM THE ACCOUNT, and this check used to make only
			// the one above. A DELETED home — what `sudo rm -rf /Users/_yolojail` leaves,
			// which the runbook prescribes for an account predating the home-tier layout — is
			// unrepairable from inside: /Users is root-owned 0755, so the sandbox uid cannot
			// create it. The launch therefore ran the entire native nix build and then died in
			// the bootstrap with twenty `mkdir /Users/_yolojail: permission denied` generator
			// failures, under a diagnosis that blamed the WORKSPACE ACL and prescribed
			// `yolo-account share`, a remedy that cannot reach this path. Same rule the
			// sidecar-mirror refusal was fixed for: name the remedy that reaches the path it
			// names. Measured on hardware 2026-09-12, and cheap here — one stat, before the
			// build.
			ID: PreconditionSandboxHome, Name: "the sandbox home",
			Requires: []string{PreconditionSandboxUser},
			holds:    func(p LaunchProbes, _ string) bool { return p.PathIsDir(SandboxHome()) },
			Ready:    func(string) string { return "Sandbox home " + SandboxHome() + " present" },
			Unmet:    func(string) string { return "Sandbox home " + SandboxHome() + " is missing" },
			Fix: func(string) string {
				return "The yolo account cannot recreate it (/Users is root-owned). Reprovision it; " +
					"this is idempotent and leaves the account alone: yolo yolo-account create"
			},
			Refusal: func(string) string {
				return "[bold red]Sandbox home '" + SandboxHome() + "' is missing.[/bold red]\n" +
					"The account '" + SandboxUser + "' exists, so this is a home that was DELETED " +
					"rather than a machine that\nwas never set up — and the yolo account cannot " +
					"recreate it itself (/Users is root-owned).\n\n" +
					"Reprovision it — idempotent, and it leaves the account record alone:\n" +
					"  [bold]yolo yolo-account create[/bold]"
			},
		},
		{
			// THE STAGING ROOT, the host-owned directory every launch stages what the sandbox
			// reads into (PL-1): `yolo yolo-account create` creates it, so a missing one is a Mac set up by
			// an older yolo or not at all. Unreadable is asked here too, because unknown is a
			// refusal, never a pass. The three staging-root rows are one probe judged three ways
			// (JudgeStagingRoot), split so each refusal names the one remedy that reaches it.
			// OQ-MP10 in docs/design/macos-user-privilege-lifecycle.md#decision-ledger is the ruling.
			ID: PreconditionStagingRoot, Name: "the staging directory",
			Requires: []string{PreconditionMacOS},
			holds: func(p LaunchProbes, _ string) bool {
				v := p.stagingRootVerdict()
				return v != StagingRootAbsent && v != StagingRootUnknown
			},
			Ready: func(string) string { return "Staging directory " + StagingRoot() + " is present" },
			Unmet: func(string) string {
				return "The staging directory " + StagingRoot() + " does not exist or cannot be read"
			},
			Fix: func(string) string {
				return "Run the one-time setup as your normal admin user (not with sudo): yolo yolo-account create"
			},
			Refusal: func(string) string {
				return "[bold red]The macos-user staging directory " + StagingRoot() + " is missing.[/bold red]\n" +
					"This Mac was set up by an older yolo, or not at all. Run the one-time setup as your " +
					"normal admin user, not with sudo; it asks for your password once:\n" +
					"  [bold]yolo yolo-account create[/bold]"
			},
		},
		{
			// SQUATTING. The enrolled host user is whoever owns the staging root (PL-3): whoever
			// owns it controls what the sandbox reads, so a launch refuses one it does not own, and
			// one macOS user per Mac runs the backend. It requires not running as root too: under
			// sudo the user's own root reads as someone else's, and this remedy would delete it.
			ID: PreconditionStagingRootOwner, Name: "the staging directory's owner",
			Requires: []string{PreconditionNotRoot, PreconditionStagingRoot},
			holds:    func(p LaunchProbes, _ string) bool { return p.stagingRootVerdict() != StagingRootForeign },
			Ready:    func(string) string { return "Staging directory " + StagingRoot() + " is yours" },
			Unmet: func(string) string {
				return "The staging directory " + StagingRoot() + " belongs to another account"
			},
			Fix: func(string) string {
				return "One macOS user per Mac runs the macos-user backend. If another user set it up, they " +
					"run yolo yolo-account remove; otherwise an administrator inspects it (ls -le " + StagingRoot() +
					") and removes it (sudo rm -rf " + StagingRoot() + "). Then run yolo yolo-account create."
			},
			Refusal: func(string) string {
				return "[bold red]Refusing the macos-user launch: " + StagingRoot() +
					" belongs to another account.[/bold red]\n" +
					"A launch stages what the sandbox reads there, so it must be yours.\n" +
					"If another macOS user set this Mac up, they run [bold]yolo yolo-account remove[/bold] first. " +
					"Otherwise an administrator inspects it with `ls -le " + StagingRoot() + "` and removes it " +
					"with `sudo rm -rf " + StagingRoot() + "`.\n" +
					"Then run [bold]yolo yolo-account create[/bold] as your normal admin user."
			},
		},
		{
			// A link, a mode another account can write through, or an access list could let
			// another account change what the sandbox reads, however the owner is right.
			ID: PreconditionStagingRootSafe, Name: "the staging directory's safety",
			Requires: []string{PreconditionStagingRootOwner},
			holds:    func(p LaunchProbes, _ string) bool { return p.stagingRootVerdict() == StagingRootOK },
			Ready: func(string) string {
				return "Staging directory " + StagingRoot() + " is a directory only you can write, with no access list"
			},
			Unmet: func(string) string {
				return "The staging directory " + StagingRoot() +
					" is a link, writable by others, or carries an access list"
			},
			Fix: func(string) string {
				return "Run yolo yolo-account create as your normal admin user; it resets the directory's mode and access list."
			},
			Refusal: func(string) string {
				return "[bold red]Refusing the macos-user launch: " + StagingRoot() +
					" is not safe to stage into[/bold red] (a symbolic link, writable by another account, " +
					"or carrying an access list).\n" +
					"Repair it as your normal admin user:\n" +
					"  [bold]yolo yolo-account create[/bold]"
			},
		},
		{
			// READINESS PROBE G2: the one-time setup's rule, `<you> ALL=(_yolojail) NOPASSWD:
			// /usr/bin/env`, through which every crossing into the yolo account goes. Without
			// it the launch would stop at a password prompt, or fail with no terminal; asked here,
			// before the build, it refuses naming the setup. It needs the account to name.
			ID: PreconditionRunAsGrant, Name: "the run-as rule",
			Requires: []string{PreconditionSandboxUser},
			holds:    func(p LaunchProbes, _ string) bool { return p.RunAsGrant != nil && p.RunAsGrant() },
			Ready: func(string) string {
				return "sudo runs commands as " + SandboxUser + " without a password (" + SudoersFile + ")"
			},
			Unmet: func(string) string {
				return "sudo cannot run commands as " + SandboxUser + " without a password"
			},
			Fix: func(string) string {
				return "Run the one-time setup as your normal admin user: yolo yolo-account create. It installs one rule, " +
					SudoersFile + ", which runs commands as " + SandboxUser + ", never as root. On a managed " +
					"Mac that refuses it, ask your administrator to allow that rule."
			},
			Refusal: func(string) string {
				return "[bold red]This launch would stop at a password prompt:[/bold red] sudo cannot run " +
					"commands as " + SandboxUser + " without one (`" + strings.Join(RunAsGrantProbeArgv(), " ") +
					"` failed).\n" +
					"Run the one-time setup as your normal admin user, not with sudo; it asks for your " +
					"password once:\n" +
					"  [bold]yolo yolo-account create[/bold]\n" +
					"On a managed Mac that refuses it, ask your administrator to allow the rule it prints. " +
					"`yolo check --no-build` asks the same question."
			},
		},
		{
			// THE WORKSPACE MUST NOT BE IN A USER'S HOME — and this is asked BEFORE the ACL
			// probe below, which is the whole point of where it sits. A workspace under a home
			// has no sandbox-group ACE either, so the probe fails for it too, and its refusal
			// names `yolo yolo-account share`: a command that refuses every path under a
			// home on purpose (AccountShare). With the probe first, a project under ~
			// looped — the launch said to run the fix, the fix said no, the launch said it
			// again. Before this check the rule lived only in PlanInvariants, which a launch
			// reaches after the nix build, behind the probe. PlanInvariants keeps it for the
			// dry-run, which asks none of these.
			ID: PreconditionNeutralGround, Name: "the workspace's location",
			Requires: []string{PreconditionMacOS},
			holds: func(_ LaunchProbes, ws string) bool {
				_, inHome := HomeContaining(ws)
				return !inHome
			},
			Ready: func(ws string) string { return "Workspace " + ws + " is outside every user's home" },
			Unmet: func(ws string) string {
				home, _ := HomeContaining(ws)
				return "Workspace " + ws + " is inside the home folder " + home
			},
			Fix: func(ws string) string {
				home, _ := HomeContaining(ws)
				return inHomeWorkspaceFix(ws, home)
			},
			Refusal: func(ws string) string {
				home, _ := HomeContaining(ws)
				return inHomeWorkspaceRefusal(ws, home)
			},
		},
		{
			// THE WORKSPACE MUST BE SHARED WITH THE SANDBOX, and this is the cheapest place
			// to learn it is not. `yolo-account create` shares everything under the shared root, and
			// anything CREATED there afterwards inherits the grant — so by the time a launch
			// runs, one route is left: a project MOVED or copied in, because rename() creates
			// nothing and inherits nothing. That is the case this message assumes, because
			// after setup it is the only one left.
			//
			// It REFUSES and names the command rather than offering to fix it inline. An
			// earlier cut prompted y/N here; the command is the better answer because it is
			// one thing to learn, it is idempotent, `yolo-account create` has already named it, and
			// it is in `yolo --help` and the diagnosing-the-jail skill. A prompt in every
			// path teaches nothing and still needs the command to exist.
			//
			// Refusing also keeps the O(files) walk off the hot path — ~0.16ms per object,
			// so ~16s on a repo with a fat node_modules, which is what 84c55268 removed by
			// moving to an inheriting entry. The check itself is one `ls`.
			//
			// It REQUIRES the yolo account (no account, no group for an ACE to name, and the
			// remedy is setup) and neutral ground (its remedy refuses a path under a home).
			ID: PreconditionWorkspaceShared, Name: "the workspace's sharing",
			Requires: []string{PreconditionSandboxUser, PreconditionNeutralGround},
			holds: func(p LaunchProbes, ws string) bool {
				return p.RunBash(WorkspaceGrantedScript(ws, "")) == 0
			},
			Ready: func(ws string) string {
				return "Workspace " + ws + " is shared with the yolo account"
			},
			Unmet: func(ws string) string {
				return "Workspace " + ws + " is not shared with the yolo account"
			},
			Fix: func(ws string) string {
				return "It carries no ACL entry for the " + SandboxGroup + " group, most likely " +
					"because it was moved in (a move inherits nothing). Share it; this is " +
					"idempotent: yolo yolo-account share " + shquote.Quote(ws)
			},
			Refusal: func(ws string) string {
				return "[bold yellow]" + ws + " is not shared with the yolo account.[/bold yellow]\n" +
					"It carries no usable ACL entry for the [bold]" + SandboxGroup + "[/bold] group, " +
					"so the sandbox cannot write here\nand the launch would fail partway through " +
					"provisioning.\n\n" +
					"Most likely this project was MOVED or copied into " + SharedRootDefault() +
					": macOS applies the\nshared ACL when a directory is CREATED, and a move " +
					"inherits nothing. (An ACL also names a\nUUID rather than a name, so entries " +
					"made before the yolo account was last recreated are\ninert while still " +
					"looking correct in `ls -le`.)\n\n" +
					"Share it — idempotent, safe to re-run:\n" +
					"  [bold]yolo yolo-account share " + shquote.Quote(ws) + "[/bold]"
			},
		},
	}
}

// unmetLaunchPrecondition is the launch's walk: the first precondition that does not hold, in
// order, asking nothing after it — a later probe answers a question the refusal has already
// made moot, and the ACL probe in particular is a subprocess.
func unmetLaunchPrecondition(p LaunchProbes, workspace string) (Precondition, bool) {
	for _, c := range LaunchPreconditions() {
		if !c.Holds(p, workspace) {
			return c, true
		}
	}
	return Precondition{}, false
}

// selfModePreconditionIDs are the preconditions a run-as-you launch asks (slice 6 of
// docs/design/macos-user-model-plan.md): the machine (macOS, not root, Seatbelt) and the staging
// root it stages into. None of the account's: no sandbox user or home, no run-as rule, no
// neutral-ground rule (a workspace may be anywhere, design §7) and no workspace sharing (the
// sandbox is you, so Unix permissions already let it write your workspace).
var selfModePreconditionIDs = []string{
	PreconditionMacOS, PreconditionNotRoot, PreconditionSeatbelt,
	PreconditionStagingRoot, PreconditionStagingRootOwner, PreconditionStagingRootSafe,
}

// unmetSelfModePrecondition is the run-as-you launch's walk over selfModePreconditionIDs, in the
// launch's order, and the refusal for the first that does not hold. An ABSENT staging root is
// created first (MMP-D8): `/Users/Shared` is 1777, so making it needs no root and no setup, and it
// is made through the setup's own step (ensureHostOwnedRoot's mkdir branch), mode 0755, yours,
// with no access list. A root that is foreign, a link or unsafe stays a refusal.
func unmetSelfModePrecondition(deps Deps, out printer, workspace string) (refusal string, unmet bool) {
	p := deps.launchProbes()
	for _, c := range LaunchPreconditions() {
		if !slices.Contains(selfModePreconditionIDs, c.ID) {
			continue
		}
		if c.ID == PreconditionStagingRoot && p.stagingRootVerdict() == StagingRootAbsent {
			if ensureHostOwnedRoot(deps, out, StagingRoot(), "", false) != 0 {
				return "[bold red]Could not create the macos-user staging directory " + StagingRoot() +
					".[/bold red] The run-as-you mode makes it itself, as you, with no admin step; the " +
					"step that failed is above. Create it yourself and launch again:\n" +
					"  [bold]mkdir -m 0755 " + StagingRoot() + "[/bold]", true
			}
		}
		if !c.Holds(p, workspace) {
			return c.Refusal(workspace), true
		}
	}
	return "", false
}

// privilegePreconditions are the setup's privilege facts: the staging root's three and the run-as
// rule. PrivilegeUnmet asks them and what they require.
var privilegePreconditions = []string{
	PreconditionStagingRoot, PreconditionStagingRootOwner, PreconditionStagingRootSafe, PreconditionRunAsGrant,
}

// PrivilegeUnmet is the first of the privilege preconditions (the staging root's three and the
// run-as rule), or of what they require, that does not hold on p, in the launch's order; false when
// all hold. For an act that crosses into the yolo account without a workspace of its own — the
// capture and fork-build acts and the host floor that asks for them — so it refuses for the same
// reasons as the launch, in the launch's words.
func PrivilegeUnmet(p LaunchProbes) (Precondition, bool) {
	return PrivilegeUnmetFor(ModeAccount, p)
}

// selfModePrivilegePreconditions are the run-as-you mode's privilege facts (slice 9 of
// docs/design/macos-user-model-plan.md): the staging root's three alone. There is no account to
// cross into and so no run-as rule; the capture root, the other root an act stages in, is judged
// by the act itself (captureRootUsable).
var selfModePrivilegePreconditions = []string{
	PreconditionStagingRoot, PreconditionStagingRootOwner, PreconditionStagingRootSafe,
}

// PrivilegeUnmetFor is PrivilegeUnmet for an act's mode. In the run-as-you mode it asks the staging
// root's three only, and an ABSENT staging root holds, since the act makes it itself, as you, as a
// run-as-you launch does (unmetSelfModePrecondition).
func PrivilegeUnmetFor(mode Mode, p LaunchProbes) (Precondition, bool) {
	ids := privilegePreconditions
	if mode == ModeSelf {
		if p.stagingRootVerdict() == StagingRootAbsent {
			return Precondition{}, false
		}
		ids = selfModePrivilegePreconditions
	}
	all := LaunchPreconditions()
	byID := map[string]Precondition{}
	for _, c := range all {
		byID[c.ID] = c
	}
	asked := map[string]bool{}
	var want func(id string)
	want = func(id string) {
		if asked[id] {
			return
		}
		asked[id] = true
		for _, req := range byID[id].Requires {
			want(req)
		}
	}
	for _, id := range ids {
		want(id)
	}
	for _, c := range all {
		if asked[c.ID] && !c.Holds(p, "") {
			return c, true
		}
	}
	return Precondition{}, false
}

// PreconditionResult is one precondition as a report sees it.
type PreconditionResult struct {
	Precondition
	// Workspace is the resolved spelling the texts are to be rendered with.
	Workspace string
	// Checked is false when a precondition it Requires did not hold, which Blocker names; Held
	// is meaningful only when Checked.
	Checked bool
	Held    bool
	Blocker Precondition
}

// CheckLaunchPreconditions asks every precondition, in the launch's order, except one whose
// requirement did not hold: that one is returned unchecked, naming the requirement, because
// its answer would be meaningless (no account to name) or its remedy wrong (the ACL fix under
// a home). `workspace` is resolved here, the way the launch resolves it.
func CheckLaunchPreconditions(p LaunchProbes, workspace string) []PreconditionResult {
	return CheckLaunchPreconditionsFor(ModeAccount, p, workspace)
}

// CheckLaunchPreconditionsFor is CheckLaunchPreconditions for a launch's mode. The run-as-you
// mode's report holds the rows its launch asks and no others (selfModePreconditionIDs), and an
// absent staging root holds there, since the launch makes it (unmetSelfModePrecondition).
func CheckLaunchPreconditionsFor(mode Mode, p LaunchProbes, workspace string) []PreconditionResult {
	ws := resolvePathAbs(workspace)
	var out []PreconditionResult
	byID := map[string]PreconditionResult{}
	for _, c := range LaunchPreconditions() {
		if mode == ModeSelf {
			if !slices.Contains(selfModePreconditionIDs, c.ID) {
				continue
			}
			if p.stagingRootVerdict() == StagingRootAbsent {
				switch c.ID {
				case PreconditionStagingRoot:
					c.holds = func(LaunchProbes, string) bool { return true }
					c.Ready = func(string) string {
						return "Staging directory " + StagingRoot() + " is absent; the launch creates it, as you"
					}
				case PreconditionStagingRootOwner, PreconditionStagingRootSafe:
					continue // nothing there yet to own or to be unsafe; the launch makes it right
				}
			}
		}
		r := PreconditionResult{Precondition: c, Workspace: ws, Checked: true}
		for _, req := range c.Requires {
			got := byID[req]
			if got.Checked && got.Held {
				continue
			}
			// The blocker is the requirement that was ASKED and failed: when the direct
			// requirement was itself not checked, name what blocked it, so the report points
			// at a row with a fix on it rather than at another skip.
			r.Checked, r.Blocker = false, got.Precondition
			if !got.Checked {
				r.Blocker = got.Blocker
			}
			break
		}
		if r.Checked {
			r.Held = c.Holds(p, ws)
		}
		byID[c.ID] = r
		out = append(out, r)
	}
	return out
}

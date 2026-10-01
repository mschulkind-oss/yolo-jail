package macosuser

import "github.com/mschulkind-oss/yolo-jail/internal/shquote"

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
	}
}

// Precondition IDs, for the Requires lists and for callers that key on one.
const (
	PreconditionMacOS           = "macos"
	PreconditionNotRoot         = "not-root"
	PreconditionSeatbelt        = "sandbox-exec"
	PreconditionSandboxUser     = "sandbox-user"
	PreconditionSandboxHome     = "sandbox-home"
	PreconditionNeutralGround   = "workspace-location"
	PreconditionWorkspaceShared = "workspace-shared"
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
			// Must NOT be run under sudo — the launch self-escalates, and running as root makes
			// the host user 'root', misassigning the git identity + ACL.
			ID: PreconditionNotRoot, Name: "running as your own user",
			Requires: []string{PreconditionMacOS},
			holds:    func(p LaunchProbes, _ string) bool { return p.Geteuid() != 0 },
			Ready:    func(string) string { return "Running as your own user, not root" },
			Unmet:    func(string) string { return "yolo is running as root (under sudo)" },
			Fix: func(string) string {
				return "Run yolo as your normal user: a launch asks for sudo itself at each step " +
					"that needs it, and run as root it would give root's git identity and ACL to " +
					"the sandbox."
			},
			Refusal: func(string) string {
				return "[bold red]Don't run `yolo` under sudo for the macos-user " +
					"backend.[/bold red]  It escalates each step itself; running as " +
					"root breaks the per-user identity/ACL."
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
			ID: PreconditionSandboxUser, Name: "the sandbox user",
			Requires: []string{PreconditionMacOS},
			holds:    func(p LaunchProbes, _ string) bool { return p.SandboxUserExists() },
			Ready:    func(string) string { return "Sandbox user '" + SandboxUser + "' exists" },
			Unmet:    func(string) string { return "Sandbox user '" + SandboxUser + "' does not exist" },
			Fix: func(string) string {
				return "Run the one-time setup, as your normal user: yolo macos-setup"
			},
			Refusal: func(string) string {
				return "[bold red]Sandbox user '" + SandboxUser + "' does not exist.[/bold red]\n" +
					"Run the one-time setup to create it (`yolo macos-setup`; see " +
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
			// `macos-fix-permissions`, a remedy that cannot reach this path. Same rule the
			// sidecar-mirror refusal was fixed for: name the remedy that reaches the path it
			// names. Measured on hardware 2026-09-12, and cheap here — one stat, before the
			// build.
			ID: PreconditionSandboxHome, Name: "the sandbox home",
			Requires: []string{PreconditionSandboxUser},
			holds:    func(p LaunchProbes, _ string) bool { return p.PathIsDir(SandboxHome()) },
			Ready:    func(string) string { return "Sandbox home " + SandboxHome() + " present" },
			Unmet:    func(string) string { return "Sandbox home " + SandboxHome() + " is missing" },
			Fix: func(string) string {
				return "The sandbox user cannot recreate it (/Users is root-owned). Reprovision it; " +
					"this is idempotent and leaves the account alone: yolo macos-setup"
			},
			Refusal: func(string) string {
				return "[bold red]Sandbox home '" + SandboxHome() + "' is missing.[/bold red]\n" +
					"The account '" + SandboxUser + "' exists, so this is a home that was DELETED " +
					"rather than a machine that\nwas never set up — and the sandbox user cannot " +
					"recreate it itself (/Users is root-owned).\n\n" +
					"Reprovision it — idempotent, and it leaves the account record alone:\n" +
					"  [bold]yolo macos-setup[/bold]"
			},
		},
		{
			// THE WORKSPACE MUST NOT BE IN A USER'S HOME — and this is asked BEFORE the ACL
			// probe below, which is the whole point of where it sits. A workspace under a home
			// has no sandbox-group ACE either, so the probe fails for it too, and its refusal
			// names `yolo macos-fix-permissions`: a command that refuses every path under a
			// home on purpose (MacosFixPermissions). With the probe first, a project under ~
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
			// to learn it is not. `macos-setup` shares everything under the shared root, and
			// anything CREATED there afterwards inherits the grant — so by the time a launch
			// runs, one route is left: a project MOVED or copied in, because rename() creates
			// nothing and inherits nothing. That is the case this message assumes, because
			// after setup it is the only one left.
			//
			// It REFUSES and names the command rather than offering to fix it inline. An
			// earlier cut prompted y/N here; the command is the better answer because it is
			// one thing to learn, it is idempotent, `macos-setup` has already named it, and
			// it is in `yolo --help` and the diagnosing-the-jail skill. A prompt in every
			// path teaches nothing and still needs the command to exist.
			//
			// Refusing also keeps the O(files) walk off the hot path — ~0.16ms per object,
			// so ~16s on a repo with a fat node_modules, which is what 84c55268 removed by
			// moving to an inheriting entry. The check itself is one `ls`.
			//
			// It REQUIRES the sandbox user (no account, no group for an ACE to name, and the
			// remedy is setup) and neutral ground (its remedy refuses a path under a home).
			ID: PreconditionWorkspaceShared, Name: "the workspace's sharing",
			Requires: []string{PreconditionSandboxUser, PreconditionNeutralGround},
			holds: func(p LaunchProbes, ws string) bool {
				return p.RunBash(WorkspaceGrantedScript(ws, "")) == 0
			},
			Ready: func(ws string) string {
				return "Workspace " + ws + " is shared with the sandbox user"
			},
			Unmet: func(ws string) string {
				return "Workspace " + ws + " is not shared with the sandbox user"
			},
			Fix: func(ws string) string {
				return "It carries no ACL entry for the " + SandboxGroup + " group, most likely " +
					"because it was moved in (a move inherits nothing). Share it; this is " +
					"idempotent: yolo macos-fix-permissions " + shquote.Quote(ws)
			},
			Refusal: func(ws string) string {
				return "[bold yellow]" + ws + " is not shared with the sandbox user.[/bold yellow]\n" +
					"It carries no usable ACL entry for the [bold]" + SandboxGroup + "[/bold] group, " +
					"so the sandbox cannot write here\nand the launch would fail partway through " +
					"provisioning.\n\n" +
					"Most likely this project was MOVED or copied into " + SharedRootDefault() +
					": macOS applies the\nshared ACL when a directory is CREATED, and a move " +
					"inherits nothing. (An ACL also names a\nUUID rather than a name, so entries " +
					"made before the sandbox account was last recreated are\ninert while still " +
					"looking correct in `ls -le`.)\n\n" +
					"Share it — idempotent, safe to re-run:\n" +
					"  [bold]yolo macos-fix-permissions " + shquote.Quote(ws) + "[/bold]"
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
	ws := resolvePathAbs(workspace)
	var out []PreconditionResult
	byID := map[string]PreconditionResult{}
	for _, c := range LaunchPreconditions() {
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

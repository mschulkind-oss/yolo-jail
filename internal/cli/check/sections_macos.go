package check

import (
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// sandboxUserExists reports whether `id <user>` returns 0 — the launch's own probe
// (macosuser's sandboxUserExistsReal), through this package's Exec seam.
func (o *Options) sandboxUserExists() bool {
	res := o.Exec([]string{"id", macosuser.SandboxUser}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	return res.RC == 0
}

// macosLaunchProbes answers the launch's precondition questions with this package's seams:
// the same questions, the same predicates, asked of the same machine.
func (o *Options) macosLaunchProbes() macosuser.LaunchProbes {
	return macosuser.LaunchProbes{
		IsMacOS: func() bool { return o.IsMacOS },
		Geteuid: o.Geteuid,
		Which: func(name string) bool {
			_, ok := o.LookPath(name)
			return ok
		},
		SandboxUserExists: o.sandboxUserExists,
		PathIsDir:         o.PathIsDir,
		RunBash: func(script string) int {
			res := o.Exec([]string{"bash", "-c", script}, "", nil, 10*time.Second)
			if !res.Ran || res.Timeout {
				return 1
			}
			return res.RC
		},
	}
}

// checkMacosUserBackend reports what stands between this machine and a macos-user launch.
// Never runs inside a jail.
//
// IT ASKS THE LAUNCH'S OWN QUESTIONS. Each row above the build step is one of
// macosuser.LaunchPreconditions, the list RunMacosUser refuses from, rendered with the fix
// beside it: running on macOS and not as root, Seatbelt, the sandbox account and its home,
// the workspace outside every user's home, and the workspace shared with the sandbox. This
// section used to keep its own shorter list and open with "Experimental backend — readiness
// only, NOT verified end-to-end", which was the honest thing to say about a list that did not
// match the launch: it graded a missing sandbox account as a warning and never looked at the
// workspace, so it passed a Mac every launch from that workspace refused. A row that did not
// look, because a requirement above it failed, is a SKIP naming that requirement, never a
// pass.
//
// THE BUILD STEP is the next refusal after those: every launch builds the tool closure with
// the host's `nix`, off PATH, so a missing one refuses it. flake.lock is a warning, not a
// refusal: nothing refuses a launch for its absence.
//
// IT NO LONGER REPORTS THE `packages:` PROFILE. That report is
// sectionPackageProfile's, gated on the notch composing no `render.PrimBakedImage`
// rather than on this section running, because the platform was the wrong predicate
// for it: the mechanism is (see section_packageprofile.go, provisioner-sets.md §9
// step 2). This section keeps the probes that really are macOS-only.
//
// NO INTERPRETER PROBE. It had one until this comment was written, hard-FAILing
// a python3-less Mac — and it had been dead since J2 swapped the launch path to
// self-exec of the staged Go binary (544a8069, 2026-07-21). Nothing the backend
// runs has needed python since; `MacosSetup` already says so in as many words
// ("No interpreter readiness check is needed: the sandbox self-execs the staged
// yolo binary", internal/macosuser/commands.go). check was the last caller of a
// requirement the backend had already dropped, which is the worst polarity for a
// readiness probe: it refuses a host that would have launched fine.
func (o *Options) checkMacosUserBackend(r *reporter) {
	r.sectionHeader("macOS-user backend")
	if o.inJail() {
		r.skip("Inside jail — macos-user checks skipped", "That backend runs on the host with no container at all; run `yolo check` there.")
		return
	}
	for _, res := range macosuser.CheckLaunchPreconditions(o.macosLaunchProbes(), o.Workspace) {
		switch {
		case !res.Checked:
			r.skip("Not checked: "+res.Name,
				"Needs "+res.Blocker.Name+" first; its row above says how.")
		case res.Held:
			r.ok(res.Ready(res.Workspace))
		default:
			r.fail(res.Unmet(res.Workspace), res.Fix(res.Workspace))
		}
	}
	if !o.IsMacOS {
		return
	}
	if _, ok := o.LookPath("nix"); ok {
		r.ok("nix available (the launch builds the sandbox's tools with it)")
	} else {
		r.fail("nix not found",
			"Every macos-user launch builds its tools (git, node, mise and anything in "+
				"`packages:`) with the host's nix, and refuses without it.\n"+o.nixInstallNote())
	}
	repoRes, ok := o.RepoRoot()
	repoRoot := repoRes.Root
	if ok && fileExists(filepath.Join(repoRoot, "flake.lock")) {
		r.ok("flake.lock present (pinned nixpkgs for native packages)")
	} else {
		r.warn("flake.lock not found at the repo root",
			"Native packages resolve against the repo's pinned "+
				"nixpkgs; without the lock they can't be pinned.")
	}
}

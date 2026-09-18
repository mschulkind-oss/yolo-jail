package check

import (
	"path/filepath"
	"time"
)

// sandboxUser is the dedicated macOS sandbox account.
const sandboxUser = "_yolojail"

// sandboxUserExists reports whether `id <user>` returns 0.
func (o *Options) sandboxUserExists() bool {
	res := o.Exec([]string{"id", sandboxUser}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	return res.RC == 0
}

// checkMacosUserBackend probes readiness of the
// native macos-user backend (OS, Seatbelt, sandbox account, nix + flake.lock).
// Never runs inside a jail.
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
	r.line(r.style("macOS-user backend", ansiBold) + " " + r.style("(experimental)", ansiDim))
	if o.inJail() {
		r.skip("Inside jail — macos-user checks skipped", "That backend runs on the host with no container at all; run `yolo check` there.")
		return
	}
	r.warn("Experimental backend — readiness only, NOT verified end-to-end",
		"A green check here means the preconditions are in place, not that a "+
			"run will succeed on this hardware.  Inspect the full plan with "+
			"`yolo --dry-run`; the definitive test is a real run on a Mac "+
			"(docs/reference/macos-no-vm-direction.md).")
	if !o.IsMacOS {
		r.fail("runtime 'macos-user' requires macOS",
			"It isolates via a dedicated macOS user account; use 'podman' "+
				"or 'container' on this host.  `yolo --dry-run` still prints the "+
				"plan here for inspection.")
		return
	}
	if _, ok := o.LookPath("sandbox-exec"); ok {
		r.ok("Apple Seatbelt (sandbox-exec) available")
	} else {
		r.fail("sandbox-exec not found",
			"Seatbelt ships with macOS; a missing binary means an unusual PATH.")
	}
	if o.sandboxUserExists() {
		r.ok("Sandbox user '" + sandboxUser + "' exists")
	} else {
		r.warn("Sandbox user '"+sandboxUser+"' not provisioned",
			"Run `yolo macos-setup` to create it.")
	}
	if _, ok := o.LookPath("nix"); ok {
		r.ok("nix available (native package materialization)")
	} else {
		r.fail("nix not found",
			"This backend materializes `packages:` via native nix; "+
				"install it (https://nixos.org/download) or the agent gets no "+
				"declared tools.")
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

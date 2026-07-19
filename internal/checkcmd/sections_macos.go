package checkcmd

import (
	"path/filepath"
	"time"
)

// sandboxUser is macos_user.SANDBOX_USER — the dedicated macOS sandbox account.
const sandboxUser = "_yolojail"

// with /usr/bin/python3 LAST (it may be the xcode-select stub).
var pythonCandidates = []string{
	"/opt/homebrew/bin/python3",
	"/usr/local/bin/python3",
	"/usr/bin/python3",
}

// sandboxUserExists ports macos_user._sandbox_user_exists: `id <user>` returns 0.
func (o *Options) sandboxUserExists() bool {
	res := o.Exec([]string{"id", sandboxUser}, "", nil, 5*time.Second)
	if !res.Ran || res.Timeout {
		return false
	}
	return res.RC == 0
}

// resolvePython ports macos_user.resolve_python: the first existing candidate
// interpreter, or "" if none exist.
func (o *Options) resolvePython() string {
	for _, cand := range pythonCandidates {
		if o.PathExists(cand) {
			return cand
		}
	}
	return ""
}

// checkMacosUserBackend ports _check_macos_user_backend: probe readiness of the
// native macos-user backend (OS, Seatbelt, sandbox account, interpreter, nix +
// flake.lock). Never runs inside a jail.
func (o *Options) checkMacosUserBackend(r *reporter) {
	r.line(r.style("macOS-user backend", ansiBold) + " " + r.style("(experimental)", ansiDim))
	if o.inJail() {
		r.ok("Inside jail — macos-user checks skipped (host-side backend)")
		return
	}
	r.warn("Experimental backend — readiness only, NOT verified end-to-end",
		"A green check here means the preconditions are in place, not that a "+
			"run will succeed on this hardware.  Inspect the full plan with "+
			"`yolo --dry-run`; the definitive test is a real run on a Mac "+
			"(docs/design/macos-no-vm-direction.md).")
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
	interp := o.resolvePython()
	if interp == "" {
		r.fail("no python3 interpreter found for the sandbox user",
			"Install one (`brew install python` or `xcode-select --install`); "+
				"the bare /usr/bin/python3 stub can't run as a service account.")
	} else {
		r.ok("Interpreter for sandbox user: " + interp)
	}
	if _, ok := o.LookPath("nix"); ok {
		r.ok("nix available (native darwin package materialization)")
	} else {
		r.fail("nix not found",
			"The macos-user backend materializes `packages:` via native nix; "+
				"install it (https://nixos.org/download) or the agent gets no "+
				"declared tools.")
	}
	repoRoot, ok := o.RepoRoot()
	if ok && fileExists(filepath.Join(repoRoot, "flake.lock")) {
		r.ok("flake.lock present (pinned nixpkgs for darwin packages)")
	} else {
		r.warn("flake.lock not found at the repo root",
			"Native darwin packages resolve against the repo's pinned "+
				"nixpkgs; without the lock they can't be pinned.")
	}
}

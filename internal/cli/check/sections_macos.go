package check

import (
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/darwinpkg"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
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
// native macos-user backend (OS, Seatbelt, sandbox account, nix + flake.lock,
// and the resolved `packages:` profile). Never runs inside a jail.
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
	o.checkPackageProfile(r)
}

// checkPackageProfile reports the RESOLVED `packages:` nix profile and its GC root — the
// two facts that make a non-container notch's tool closure inspectable (N2's fourth
// sub-item; docs/design/noncontainer-nix-environment.md §8 Option 1).
//
// Read from the GC-ROOT SYMLINK, never by invoking nix: check already owns the one place a
// real build is allowed (the --build-gated image section), and resolving a profile here
// would be a second surprise build. The root is also the exactly-right oracle — it is what
// the last materialization pointed at, so it answers "which closure would a launch use".
//
// PASS/WARN split by what the user can act on. A root that resolves is the healthy state
// worth naming; an ABSENT root is a WARN rather than a FAIL because it is also the normal
// pre-first-run state — a launch creates it — and the remedy is the same either way. The
// one genuinely bad state, a root pointing at a store path that is GONE, gets its own FAIL:
// that means a GC collected the closure despite the root, which is the defect N1 fixed and
// therefore the thing worth reporting loudly if it ever recurs.
func (o *Options) checkPackageProfile(r *reporter) {
	link := darwinpkg.ProfileRootLink(paths.Home())
	target, err := os.Readlink(link)
	if err != nil {
		r.warn("No `packages:` nix profile resolved yet",
			"A run materializes it and GC-roots it at "+link+".  Nothing is "+
				"wrong if you have not launched this backend yet.")
		return
	}
	if !o.PathExists(target) {
		r.fail("The `packages:` GC root points at a store path that no longer exists",
			"Root: "+link+"\nTarget: "+target+"\nA nix GC collected a ROOTED "+
				"closure, which should be impossible — the next run rebuilds it, "+
				"but please report this.")
		return
	}
	r.ok("`packages:` profile resolved and GC-rooted: " + target)
}

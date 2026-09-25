package paths

import "path/filepath"

// HomeSkeletonRoot returns AGENTS_DIR/<cname>/home, the directory holding a podman jail's
// home skeletons. A skeleton (a term coined in
// docs/design/base-home-legacy-state.md#1-the-question-and-the-answer) is a per-jail
// directory bound read-only at /home/agent that holds only the mountpoints and redirect links
// a launch needs — never file content.
//
// ONE spelling of the location, because three things must agree about it: the builder that
// creates a skeleton under it, the podman argv that binds one, and the reaper that removes
// it. The reaper is prune.PruneOrphanAgentStaging, which removes the WHOLE AGENTS_DIR/<cname>
// once no container of that name is live or tracked (OQ-BH9 in
// docs/design/base-home-legacy-state.md#10-decision-ledger), so a skeleton needs no reaper of
// its own.
//
// Each fresh launch builds a NEW directory under this root and never edits an old one (the
// maintainer's OQ-BH10 ruling, same ledger): removing a mountpoint under a live jail silently
// detaches its bind, and a new directory per launch removes nothing.
//
// Host-only: a podman jail sees AGENTS_DIR/<cname> only through :ro binds of its skills,
// briefing and pack-staging children, which is why a skeleton lives here and not in the
// workspace overlay (<workspace>/.yolo/home), which the jail can write.
func HomeSkeletonRoot(cname string) string { return filepath.Join(AgentsDir(), cname, "home") }

// HomeSkeletonCoreDirs returns the home-relative directories every podman jail's home
// skeleton gets whatever packs it selected: core's own mountpoints and their parents.
// buildHomeSkeleton (internal/cli/run) appends the SELECTED packs' writable and shared dirs.
//
// No pack's directory is here. `.pi/agent` used to be, as a line of the list the shared base
// was provisioned from: in a skeleton it put a ~/.pi in every jail whether pi was selected or
// not, against DIR-BH1 ("a non-selected pack can never have an impact",
// docs/design/base-home-legacy-state.md#10-decision-ledger). A pi jail loses nothing: the pi
// pack declares `.pi` itself, so the mountpoint comes with the pack. BaseHomeCoreDirs keeps
// `.pi/agent` for the legacy walk, which is the one reason there are two functions.
func HomeSkeletonCoreDirs() []string {
	return []string{
		filepath.Join(".config", "git"),
		".npm-global",
		".local",
		"go",
		// One dir, two generated-script children (block/ and launch/). The nested path
		// matters: the OCI runtime cannot mkdirat inside the :ro /home/agent bind, so the
		// mountpoint's PARENT has to exist here or the launch fails with an opaque
		// crun/conmon error rather than a useful one.
		".yolo",
		".yolo/bin",
		".config",
		".cache",
		".ssh",
	}
}

// HomeFileMountpoints returns the home-relative paths that must exist as FILES, not
// directories, in a podman jail's home skeleton, for the single-file binds of the per-workspace
// overlay files onto /home/agent (podmanBaseMounts in internal/cli/run).
//
// A bind of a file over a directory (or the reverse) aborts container creation, and the OCI
// runtime cannot create a mountpoint inside the read-only /home/agent bind, so each one has to
// exist, as a file, before the container starts.
func HomeFileMountpoints() []string {
	return []string{
		".bash_history",
		".yolo-bootstrap.sh",
		".yolo-venv-precreate.sh",
		".yolo-perf.log",
		".yolo-socat.log",
		".yolo-entrypoint.lock",
		".yolo-ca-bundle.crt",
	}
}

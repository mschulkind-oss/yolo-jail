package image

import (
	"strings"
)

// storewrite.go answers ONE question, and answers it BEFORE anything runs: from
// which user namespace must the copier write podman's `containers-storage`?
//
// docs/design/layer-aware-image-delivery.md §3.4b.
//
// # Why a byte-copier needs a namespace at all
//
// The obvious reading of `skopeo copy nix:<manifest> containers-storage:<ref>` is
// that it moves bytes, and a byte-mover has no business with kernel namespaces.
// It is wrong, and the chain is four steps — MEASURED on a purpose-built Ubuntu
// 24.04 VM (kernel 6.8.0-138), rootless podman with subuid ranges delegated:
//
//  1. **A rootless store has to reproduce ownership, not just content.** Each
//     layer records the uid/gid of every file it carries, almost all of them uid
//     0, and a rootless `containers-storage` lands those as the user's mapped
//     subuid (100000 and up, from /etc/subuid). Creating a file owned by 100000
//     means being inside a user namespace that HOLDS that id.
//  2. **So containers/storage creates an unprivileged user namespace.** That is
//     what `MaybeReexecUsingUserNamespace` is; `Error during unshare(...)` and the
//     `%s-in-a-user-namespace` argv are both its strings, verified present in the
//     copier binary (skopeo 1.24.0).
//  3. **AppArmor 4 mediates unprivileged user-namespace creation**, and Ubuntu
//     24.04 ships that mediation ON. It is the denied syscall, established by a
//     single-variable flip on one VM with one binary and one command:
//
//     kernel.apparmor_restrict_unprivileged_userns=1 → Error during unshare(...): Operation not permitted
//     kernel.apparmor_restrict_unprivileged_userns=0 → Writing manifest to image destination, and the ref is really there
//
//     skopeo is not talking to AppArmor. It needs a capability AppArmor now gates.
//  4. **podman is not subject to it in practice**, because it establishes the
//     mapping through the setuid helpers — `/usr/bin/newuidmap` is
//     `-rwsr-xr-x root` — rather than by unsharing on its own account.
//
// Step 3 is the CI failure of 2026-09-09 reproduced exactly: 2 integration jobs
// and 14 pack-install jobs, both arches, all four lines of output identical.
//
// ⚠ IT IS NOT "AN UNPROFILED BINARY IS DENIED WHILE PODMAN IS ALLOWLISTED". That
// was the first theory and it is disproved in the same VM: `/usr/bin/unshare`,
// which ships an AppArmor profile of its own, failed identically, and a copy of
// that binary at an unprofiled path failed identically again. The executable's
// path made no difference to any outcome, so nothing here may be explained by
// which binary carries a profile — the restriction is blanket.
//
// # The fix is to borrow podman's machinery, not to change destination
//
// `podman unshare` exists for exactly this: run a non-podman tool inside the
// rootless user namespace podman itself uses. Podman does the privileged setup
// with its setuid helpers, then execs the child inside the FINISHED namespace and
// sets `_CONTAINERS_USERNS_CONFIGURED=done` — the marker containers/storage reads
// to know it must not unshare again, and which keeps the store it resolves the
// ROOTLESS one despite the euid of 0 inside. Measured end to end on the VM above:
//
//	bare copier → containers-storage:   Error during unshare(...): Operation not permitted
//	`podman unshare` + the SAME copier  rc 0, and `podman images` really holds the ref
//	`podman unshare env`                _CONTAINERS_USERNS_CONFIGURED=done
//
// (`_CONTAINERS_USERNS_CONFIGURED` and `_CONTAINERS_ROOTLESS_UID` are both present
// as strings in podman 5.8.6 AND in the copier, verified 2026-09-09.)
//
// SHIPPING OUR OWN APPARMOR PROFILE WOULD ALSO WORK AND IS REFUSED. A profile
// lives in /etc/apparmor.d/, needs root plus an `apparmor_parser` reload, is
// per-distro, and would have a launch mutate the host's security configuration to
// win a privilege the podman it already invokes has anyway. `podman unshare`
// reaches the same result at the distro's secure default, touching nothing.
//
// ⚠ THIS IS NOT THE FALLBACK OQ-LI5 DELETED, and the distinction is not a
// nicety. There is still exactly ONE delivery mechanism per launch and ONE
// destination on Linux: `skopeo copy` into `containers-storage`, keeping every
// byte of the layer negotiation that buys. Nothing degrades, nothing is retried
// into a second shape, and NO FAILURE selects between two paths — the mode podman
// is in selects, from a fact read before the copy starts, exactly as the backend
// selects the archive on macOS (§3.4a). A fallback would be "copy, and if it
// fails write an archive instead"; this file is why that is not needed.
//
// # And why the wrapper cannot be unconditional
//
// `podman unshare` REFUSES on a rootful podman — "please use unshare with
// rootless", measured in this jail (podman 5.8.6 running as root, 2026-09-09) —
// and the refusal is correct: a root store needs no mapping, so there is no
// namespace to enter. Wrapping unconditionally would break the one case that
// never had the problem.

// PodmanRootless is what a launch knows about the store it is about to write. It
// is a TRI-STATE because "podman says it is rootful" and "yolo could not ask"
// must not collapse: the first selects a mode, the second is the absence of a
// fact, and this repo's rule is that an unproven fact emits nothing
// (internal/cli/run/hostloopback.go states it at length).
type PodmanRootless int

const (
	// RootlessUnknown: `podman info` did not run, did not parse, or was never
	// asked. Nothing is emitted onto the copier's argv — see StoreWritePrefix for
	// why that is the branch that works here rather than merely the quiet one.
	RootlessUnknown PodmanRootless = iota
	// RootlessYes: podman reported host.security.rootless. The copier must write
	// from inside podman's own user namespace.
	RootlessYes
	// RootlessNo: podman reported a rootful store. No namespace is needed and
	// `podman unshare` would refuse.
	RootlessNo
)

// StoreWritePrefix is the argv prefix that puts the copier in the namespace the
// destination store requires: nil for a store that needs none, and
// `<runtime> unshare --` for a rootless one.
//
// PURE, and the whole decision. The impure half is one `podman info`
// (PodmanRootlessness), which is what makes every branch here reachable from a
// table test on a machine that has neither a rootless podman nor a kernel that
// refuses the mapping — and this jail has neither.
//
// AN UNKNOWN ANSWER EMITS NOTHING, which is the tri-state rule applied to a case
// where "the branch that works everywhere" does not exist: the wrapper is
// refused by a rootful podman and the bare copy is refused by a rootless one on
// the configuration above, so neither branch is universally safe and the tie has
// to be broken by evidence. Three facts break it toward silence:
//
//   - Emitting nothing is TODAY'S BEHAVIOUR. It cannot newly break a host that
//     works, which is the rule hostloopback.go is built around ("the worst case
//     must be today's behaviour").
//   - The unknown branch DOES NOT COVER THE BUG. On every host where the copy is
//     refused, `podman info` demonstrably answers — it is how the failing CI jobs
//     printed `rootless: True` in the step before the one that died — so the case
//     this branch handles is not the case that is broken.
//   - A launch that cannot read `podman info` is about to fail at `podman run`
//     anyway, and it should fail there, saying that, rather than here saying
//     something about namespaces.
//
// The `--` is not decoration. `podman unshare` takes its own flags first and the
// command after, so without the separator podman would try to parse the copier's
// `--insecure-policy` as its own and refuse the launch with an unknown-flag
// error that names none of this.
func StoreWritePrefix(runtime string, rootless PodmanRootless) []string {
	if rootless != RootlessYes {
		return nil
	}
	return []string{runtime, "unshare", "--"}
}

// PodmanRootlessness asks the runtime whether the store it writes is rootless.
// capture runs an argv and returns its stdout; ok=false for anything that did
// not run cleanly, which is RootlessUnknown.
//
// Cost: one subprocess, 22 ms measured in this jail, and only on a launch that is
// about to copy — the same launch that may spend two minutes building the copier.
// A warm launch whose image is already loaded never asks.
func PodmanRootlessness(runtime string, capture func(argv []string) (string, bool)) PodmanRootless {
	if capture == nil {
		return RootlessUnknown
	}
	out, ok := capture(PodmanInfoCmd(runtime))
	if !ok {
		return RootlessUnknown
	}
	switch rootlessField(out) {
	case "true":
		return RootlessYes
	case "false":
		return RootlessNo
	}
	return RootlessUnknown
}

// PodmanInfoCmd is the argv PodmanRootlessness runs.
func PodmanInfoCmd(runtime string) []string {
	return []string{runtime, "info", "--format", "json"}
}

// rootlessField extracts host.security.rootless from `podman info --format json`
// as the literal token podman wrote — "true", "false", or "" when the key is not
// there at all.
//
// A scan for the key rather than encoding/json into a struct of bools, purely so
// that MISSING and FALSE stay different answers. Decoding gives both the same
// zero value, and a launch that read "I could not find the field" as "the store
// is rootful" would take the bare-copy branch on precisely the host that cannot
// use it. `host.security.rootless` has been in `podman info` since long before
// any podman this project supports (measured present at 4.9.3 on the CI
// runners), so its absence means "this is not podman info", never "this podman is
// old".
func rootlessField(infoJSON string) string {
	i := strings.Index(infoJSON, `"rootless"`)
	if i < 0 {
		return ""
	}
	rest := strings.TrimSpace(infoJSON[i+len(`"rootless"`):])
	rest = strings.TrimSpace(strings.TrimPrefix(rest, ":"))
	switch {
	case strings.HasPrefix(rest, "true"):
		return "true"
	case strings.HasPrefix(rest, "false"):
		return "false"
	}
	return ""
}

// StoreWriteNote is the line a launch prints about the namespace it chose, and it
// is printed on EVERY delivery rather than only on the interesting one. §3.4a's
// rule for a decision with more than one outcome is that "the launch says which
// it took": a mode nobody can see is a mode nobody can debug, and the two
// failures this decision can produce — a refused `podman unshare`, a copier that
// could not map ownership — are told apart by exactly this line.
func StoreWriteNote(rootless PodmanRootless) string {
	switch rootless {
	case RootlessYes:
		return "  Store write: inside podman's own user namespace (`podman unshare`) — " +
			"a rootless store maps layer ownership through /etc/subuid."
	case RootlessNo:
		return "  Store write: direct (podman is rootful, so no user namespace is needed)."
	}
	return "  Store write: direct — yolo could not read `podman info`, so it is not changing " +
		"how the copy runs. If this podman is rootless, the copy below may be unable to map " +
		"layer ownership, and it will say so."
}

// retryWouldHelp reports whether an immediate second attempt at a failed copy can
// possibly do better, and the reason when it cannot.
//
// A REFUSAL IS NOT A LOST RACE. §3.6 bounds the retry at exactly one and argues
// it from transience — a neighbour holding the c/storage lock, a blob write
// interrupted — and every one of those recovers because the second attempt meets
// a different world. A kernel that refused the mapping refuses identically one
// millisecond later, so the retry buys nothing and pays the whole failure path
// twice. That was not free: sixteen CI jobs each bought a second refusal on
// 2026-09-09.
//
// IT IS A DENYLIST AND MUST STAY ONE. Retrying is the default and an unrecognised
// failure keeps the retry it has today; only causes MEASURED to be permanent are
// named here. An allowlist of transient causes would silently drop the retry for
// every failure nobody has seen yet, which is the direction that turns a
// recoverable launch into a dead one.
func retryWouldHelp(tail []string) (bool, string) {
	joined := strings.Join(tail, "\n")
	// The copier's own failed namespace setup (containers/storage's
	// MaybeReexecUsingUserNamespace). Measured on both arches of GitHub's
	// ubuntu-24.04 runners and reproduced on a noble VM, 2026-09-09.
	if strings.Contains(joined, "Error during unshare(") {
		return false, "the copier could not establish the user namespace a rootless store " +
			"needs, and a second attempt cannot either.\n" +
			"  A rootless containers-storage maps layer ownership through /etc/subuid, so the\n" +
			"  copy has to run inside podman's own namespace — yolo does that only when\n" +
			"  `podman info` reports a rootless store. Check\n" +
			"  `podman info --format '{{.Host.Security.Rootless}}'` and `podman unshare id`."
	}
	// Our own wrapper on a rootful podman — a mode error, not a transient one.
	if strings.Contains(joined, "please use unshare with rootless") {
		return false, "podman refused `unshare` because it is not rootless, and it will " +
			"refuse again.\n" +
			"  yolo asked for the namespace because `podman info` reported a rootless store;\n" +
			"  check `podman info --format '{{.Host.Security.Rootless}}'`."
	}
	return true, ""
}

// copyArgv is the whole command one delivery copy runs: an optional namespace
// prefix, then the copier and its arguments.
//
// Spelled as a function so the prefix decision and the argv are ONE fact a test
// can compare against StoreWritePrefix's output. The alternative — building the
// argv inline at the exec — is the shape where a prefix can be computed,
// printed, and then not passed.
func copyArgv(prefix []string, copier, imageJSON, dest string) []string {
	argv := make([]string, 0, len(prefix)+5)
	argv = append(argv, prefix...)
	return append(argv, copier, "--insecure-policy", "copy", "nix:"+imageJSON, dest)
}

// unshareProbeArgv is the command that establishes, without copying anything,
// that this host will let the copier run inside podman's user namespace. `yolo
// check` runs it; the launch does not (a launch that is about to copy learns the
// same thing from the copy, in skopeo's own words).
//
// `/bin/sh -c :` because it is the one executable every Linux is required to have
// at a fixed path — `true`, `id` and `cat` are PATH lookups a nix-profile-only
// host can answer differently or not at all.
func unshareProbeArgv(runtime string) []string {
	return append(StoreWritePrefix(runtime, RootlessYes), "/bin/sh", "-c", ":")
}

// DeliveryPreflight is what `yolo check` reports about image delivery: one line,
// its severity, the remedy when there is one, and the command the answer was
// based on.
//
// An EMPTY Line means "say nothing", which is the tri-state rule applied to a
// preflight: on a host where yolo could not read `podman info` there is no route
// to report, and the missing-or-broken podman is already the subject of the
// runtime section two blocks up. A second warning about the same fact trains the
// reader to skip both.
type DeliveryPreflight struct {
	Line string
	Warn bool
	// Hint is the remedy, printed under the line. "" when the line needs none.
	Hint string
	// Ran is the command the verdict rests on, "" when none was run. It exists so
	// a reader can re-run the probe by hand — the same reason
	// hostLoopbackFacts.probeCmd does.
	Ran string
}

// UnsharePreflight is `yolo check`'s question: given what podman reports, will
// image delivery work, and by which route?
//
// THE PROBE IS THE POINT. Reporting the route from `podman info` alone would
// restate the decision rather than test it; RUNNING the namespace the copy will
// use is what makes this a preflight — it moves a failure that would otherwise be
// found by a launch dying partway through a multi-gigabyte delivery into a line
// of a report that costs milliseconds.
//
// probe runs an argv and reports whether it exited 0. hasSh is whether /bin/sh
// exists, which is the probe's one precondition: without it a failed probe would
// mean "no shell" and be reported as a broken delivery path.
func UnsharePreflight(runtime string, rootless PodmanRootless, hasSh bool,
	probe func(argv []string) bool) DeliveryPreflight {
	switch rootless {
	case RootlessNo:
		return DeliveryPreflight{Line: "Image delivery: direct copy into podman's rootful store"}
	case RootlessUnknown:
		return DeliveryPreflight{}
	}
	if !hasSh || probe == nil {
		return DeliveryPreflight{
			Line: "Image delivery: copy into podman's rootless store via `podman unshare`"}
	}
	argv := unshareProbeArgv(runtime)
	cmd := strings.Join(argv, " ")
	if !probe(argv) {
		return DeliveryPreflight{
			Line: "`" + cmd + "` failed — image delivery into podman's rootless store will fail",
			Warn: true,
			Hint: unshareFailureHint,
			Ran:  cmd,
		}
	}
	return DeliveryPreflight{
		Line: "Image delivery: copy into podman's rootless store via `podman unshare` (verified)",
		Ran:  cmd,
	}
}

// unshareFailureHint names the two things that can make `podman unshare` fail on
// a host whose podman calls itself rootless, because they send a reader to
// different places: no subuid delegation, or a host that refuses podman the
// namespace as well.
const unshareFailureHint = "A rootless store maps layer ownership through /etc/subuid, so the " +
	"copy has to run inside podman's user namespace. Check that this user has a delegated " +
	"range (`grep \"$USER\" /etc/subuid`) and that podman can enter the namespace at all " +
	"(`podman unshare id`)."

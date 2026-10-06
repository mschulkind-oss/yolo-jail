package macosuser

// seatbeltcapture.go is the narrowed Seatbelt profile an INSTALL CAPTURE runs under
// (docs/design/program-delivery.md §6.3, docs/plans/install-capture.md slice 6).
//
// # Why this backend needs a second profile at all
//
// On the container backends a capture is an ephemeral jail whose per-workspace home binds start
// empty, so "the bind-dir contents ARE the delta" and no new containment is needed. macos-user
// has no binds and no ephemeral home: its home is one persistent, machine-constant
// /Users/_yolojail (SandboxHome), and it stays that way deliberately — the home-tier layout
// symlinks the per-workspace directories out of it into <workspace>/.yolo/home rather than
// moving HOME, so a capture still faces one shared account home holding the machine tier
// (docs/design/macos-user-home-tiers.md, alternative A′; a per-workspace HOME is alternative
// A and is the recorded runner-up).
//
// ⚠ The earlier claim here, that the single home IS this backend's shared-credentials
// mechanism, is RETRACTED (§3). The mechanism is the `shared_credentials` hook, which runs
// on every backend; the home supplies only the backing of the pack-declared scope:machine
// directory. What follows is unchanged either way: that directory holds the credentials a
// capture must not reach.
//
// A capture must not touch it. What it needs instead is a FRESH, ENUMERABLE, KERNEL-BOUNDED
// write surface, and this backend already has the control point for one: the Seatbelt profile
// is generated fresh per session (SeatbeltProfile, installed root-owned at SessionProfilePath),
// so a capture can carry a different profile without changing anything about how a launch
// works. This is that profile.
//
// # What it changes against SeatbeltProfile
//
//   - The writable set is the STAGING ROOT, /tmp and the /var/folders scratch. No workspace
//     (a capture has none) and, above all, no sandbox home.
//   - The shared home is denied EXPLICITLY, after the allow, so it stays denied even if someone
//     later widens the allow list. `deny file-write* /` already covers it; the explicit deny is
//     the statement of intent in the artifact a human reads, and the thing a test can pin.
//   - Reads under /Users are denied with the sandbox home NOT re-allowed, which is what keeps a
//     captured installer away from the shared credential store rather than merely away from
//     writing to it. An installer that phoned home with ~/.claude/.credentials.json would be a
//     capture doing exfiltration; the read deny is the half that stops it.
//
// Everything else is SeatbeltProfile's policy verbatim and for its reasons: `(allow default)`
// with targeted denies, last-match-wins, /Volumes reads denied off the boot volume, raw disk
// and bpf denied, keychains denied, process-info and sysctl-read allowed.
//
// # NETWORK IS ALLOWED, deliberately
//
// `(allow default)` permits network, and a capture is the one act in this subsystem that needs
// it: the whole point is to run the vendor's installer against its CDN once, so every later
// materialize is offline. §6.3 calls this out as the explicit, network-OK act. A fork's BUILD keeps
// the network too, less the host's loopback and the nix daemon (SeatbeltSealedCaptureProfile, below).
//
// # MEASURED vs. DESIGNED-AGAINST-READ-CODE — read this before trusting the file
//
// MEASURED (by the unit tests beside this file, on any OS): the exact bytes of the generated
// profile, the ordering the last-match-wins policy depends on, and that BuildCapturePlan uses
// THIS generator rather than the session one.
//
// NOT YET MEASURED: that Seatbelt honors it. The earlier claim here — "no kernel has ever loaded
// this profile" — is STALE twice over: capture.go records this profile being loaded on hardware
// 2026-09-11, and the SESSION profile beside it is asserted on the macOS CI runs
// (integration/macosuserseatbelt_test.go, which loads a generated profile with `sandbox-exec` and
// checks the kernel refused what it names). A case for THIS profile exists now and has no green
// run recorded: integration/macosusercaptureseatbelt_test.go's
// TestMacosUserCaptureSeatbeltProfileDeniesTheSharedHome builds a capture plan over a root of its
// own under /Users/Shared, makes the staging tree with the plan's PrepareCommands, and loads
// plan.Seatbelt as the sandbox account, a bare control first for every case: the shared home
// unwritable and unreadable, the staging home and out dir writable, the capture root traversable,
// a sibling under it and a neutral /private/var/tmp path refused. It runs nothing else of a
// capture. A Linux jail cannot run sandbox-exec and podman-in-podman cannot exercise this backend
// at all, so the gap closes on a Mac. Two specific things a human there must check rather than
// assume, the first of which that case does not touch:
//
//  1. That an installer's shell tolerates a home whose passwd entry (getpwuid → /Users/_yolojail)
//     is unreadable while $HOME points elsewhere. Tools that resolve the home through the passwd
//     database rather than $HOME will hit the /Users read deny.
//  2. That the staging root's own path is reachable — `ancestorLiterals` grants traversal for a
//     path under /Users/Shared/, and a capture root sited anywhere else under /Users would be
//     denied by the /Users read deny with nothing to re-allow it. CaptureRootDefault is under
//     /Users/Shared for exactly this reason. The case's capture root is under /Users/Shared too,
//     so a green run answers this for that siting.

import "strings"

// SeatbeltCaptureProfile generates the SBPL profile for one install capture: deny writes
// everywhere, then re-allow ONLY the capture's own staging root plus the OS scratch dirs.
//
// stagingRoot is the whole per-capture tree — the staging HOME and the entry-shaped out dir
// both live inside it (CaptureStagingHome, CaptureStagingOut). install-capture.md names this
// parameter `stagingHome`; the ROOT is the right granularity because the driver must also write
// the tree it is assembling, and an out dir INSIDE the home being captured is one layout change
// away from being inside a capture surface, which the driver refuses outright. One allow, one
// subtree, and everything the capture touches is under it — which is what makes the invariant
// checkable rather than merely intended.
//
// An empty stagingRoot yields a profile with NO writable path but the OS scratch dirs. That is
// the fail-closed direction and it is deliberate: a caller that lost its staging path gets a
// capture that cannot write rather than one that writes into the shared home.
func SeatbeltCaptureProfile(stagingRoot string) string {
	home := sbplStr(SandboxHome())
	ancestors := ancestorLiterals(stagingRoot)
	// One clause, emitted into both the write allow and the read allow, or neither. An
	// empty stagingRoot must not produce `(subpath "")` — SBPL would read that as a
	// prefix match on every path, turning the fail-closed case into a fail-wide-open one.
	stagingClause := ""
	if stagingRoot != "" {
		stagingClause = "    (subpath " + sbplStr(stagingRoot) + ")\n"
	}
	return "(version 1)\n" +
		";; yolo-jail macOS-user INSTALL CAPTURE profile — program-delivery.md §6.3.\n" +
		";; One throwaway staging tree is writable; the shared sandbox home is not.\n" +
		"(allow default)\n" +
		"\n" +
		";; --- Writes: deny everywhere, then re-allow ONLY the capture staging tree ---\n" +
		";;     No workspace (a capture has none) and no sandbox home (see below).\n" +
		"(deny file-write* (subpath \"/\"))\n" +
		"(allow file-write*\n" +
		stagingClause +
		"    (subpath \"/tmp\")\n" +
		"    (subpath \"/private/tmp\")\n" +
		"    (subpath \"/var/folders\")\n" +
		"    (subpath \"/private/var/folders\")\n" +
		"    (subpath \"/dev\"))\n" +
		"\n" +
		";; --- The shared sandbox home is DENIED for the duration.  It is the machine's\n" +
		";;     one credential store for every workspace and session, and a capture must\n" +
		";;     record what a VENDOR INSTALLER leaves behind, not what it did to that.\n" +
		";;     Redundant against the deny above and kept anyway: it must survive a later\n" +
		";;     edit that widens the allow list, and last match wins. ---\n" +
		"(deny file-write* (subpath " + home + "))\n" +
		"\n" +
		";; --- Volumes: deny reads except the boot volume ---\n" +
		"(deny file-read* (subpath \"/Volumes\"))\n" +
		"(allow file-read* (subpath \"/Volumes/Macintosh HD\"))\n" +
		"\n" +
		";; --- Raw disk + packet capture: never ---\n" +
		"(deny file-read* file-write*\n" +
		"    (regex #\"^/dev/r?disk\")\n" +
		"    (regex #\"^/private/dev/r?disk\")\n" +
		"    (regex #\"^/dev/bpf\"))\n" +
		"\n" +
		";; --- Homes: deny reads under /Users and re-allow ONLY the traversal entries and\n" +
		";;     the staging tree.  The sandbox home is deliberately NOT re-allowed here —\n" +
		";;     unlike a session, a capture has no business reading the shared credential\n" +
		";;     store, and an installer that could read it could also ship it somewhere. ---\n" +
		"(deny file-read* (subpath \"/Users\"))\n" +
		"(allow file-read*\n" +
		ancestors +
		stagingClause +
		"    (literal \"/Users\")\n" +
		"    (literal \"/Users/Shared\"))\n" +
		"\n" +
		";; --- Keychains: System.keychain is world-readable (0644) on stock macOS ---\n" +
		"(deny file-read* (subpath \"/Library/Keychains\"))\n" +
		"\n" +
		";; --- Process introspection an installer's shell needs ---\n" +
		"(allow process-info*)\n" +
		"(allow sysctl-read)\n"
}

// THE SEALED CAPTURE PROFILE, a fork's build's (docs/design/forked-programs-as-packs.md FP-D19,
// carrying FP-D9 and FP-D13 to this backend).
//
// A fork's build is arbitrary code from a repository a pack named, so on the container backends it
// runs SEALED: on the runtime's own bridge, never the host's network namespace, with no host
// loopback forwarded in and no nix daemon socket bound (internal/cli/run's seal.go). This backend
// has no namespace to put the build in — the sandbox account shares the host's network stack, so
// the host's loopback is every service the host binds there, every running jail's loophole daemons
// among them — and no bind to withhold. Seatbelt is the one control point, so the seal is two
// denies, added after everything SeatbeltCaptureProfile allows (`(allow default)` first, and last
// match wins):
//
//   - every outbound connection to the loopback, by `(remote ip "localhost:*")`, SBPL's one
//     spelling for the loopback addresses;
//   - the nix daemon's socket, by connect and by path: a build that reached it could add to the
//     store every jail and every macos-user session reads its tools from.
//
// The network beyond the loopback stays, as it does for a container build: a build fetches its
// dependencies. NOT YET MEASURED that the kernel refuses either: integration/macosuserseatbelt_test.go's
// TestMacosUserSeatbeltSealedBuildProfileDeniesTheLoopbackAndTheNixDaemon loads this profile and
// tries both, each beside a bare control, on a Mac (macos-user.yml), with no green run recorded.

// The seal's denies, spelled once for the profile and for sealedProfileProblems, which finds them: the
// loopback, the nix daemon's socket by connect, and its directory by path.
const (
	nixDaemonSocketDir       = "/nix/var/nix/daemon-socket"
	nixDaemonSocket          = nixDaemonSocketDir + "/socket"
	sealedBuildLoopbackDeny  = `(deny network-outbound (remote ip "localhost:*"))`
	sealedBuildNixSocketDeny = `(deny network-outbound (remote unix-socket (path-literal "` + nixDaemonSocket + `")))`
	sealedBuildNixPathDeny   = `(deny file-read* file-write* (subpath "` + nixDaemonSocketDir + `"))`
)

// SeatbeltSealedCaptureProfile is the profile a fork's build runs under: the capture profile over
// stagingRoot, then the seal's denies.
func SeatbeltSealedCaptureProfile(stagingRoot string) string {
	return SeatbeltCaptureProfile(stagingRoot) +
		"\n" +
		";; --- THE SEAL (forked-programs-as-packs.md FP-D19): a fork's build is code from a\n" +
		";;     repository a pack named, and this account shares the host's network stack.  The\n" +
		";;     host's loopback (every service it binds there, every jail's loophole daemons\n" +
		";;     among them) and the nix daemon are denied; the network beyond the loopback\n" +
		";;     stays, because a build fetches its dependencies. ---\n" +
		sealedBuildLoopbackDeny + "\n" +
		sealedBuildNixSocketDeny + "\n" +
		sealedBuildNixPathDeny + "\n"
}

// sealedProfileProblems checks a profile carries the seal: each deny present, and after the
// `(allow default)` it narrows, since a deny before it would be no deny at all.
func sealedProfileProblems(profile string) []string {
	allowAt := strings.Index(profile, "(allow default)")
	var problems []string
	for _, d := range []struct{ deny, what string }{
		{sealedBuildLoopbackDeny, "the host's loopback, where every service the host binds and every jail's " +
			"loophole daemons listen"},
		{sealedBuildNixSocketDeny, "the nix daemon, whose socket would let the build add to the store every " +
			"jail reads"},
		{sealedBuildNixPathDeny, "the nix daemon's socket directory"},
	} {
		at := strings.LastIndex(profile, d.deny)
		switch {
		case at < 0:
			problems = append(problems, "the fork build's Seatbelt profile does not deny "+d.what+
				" — it is not sealed (SeatbeltSealedCaptureProfile is the one to use)")
		case at < allowAt:
			problems = append(problems, "the fork build's Seatbelt profile denies "+d.what+" BEFORE "+
				"`(allow default)`; SBPL is last-match-wins, so that deny does nothing")
		}
	}
	return problems
}

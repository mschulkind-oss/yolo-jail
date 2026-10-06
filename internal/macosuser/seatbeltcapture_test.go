package macosuser

import (
	"strings"
	"testing"
)

// seatbeltcapture_test.go pins the generated INSTALL-CAPTURE profile.
//
// What these tests measure and what they do not, stated once so nobody reads more into a green
// run than it carries: they measure the BYTES this repo emits — the clauses present, the clauses
// absent, and the ordering the last-match-wins policy depends on. They measure nothing about
// Apple Seatbelt. This profile HAS been kernel-loaded — capture.go records the run, on hardware
// 2026-09-11 — and its denials have a case of their own now, with no green run recorded:
// integration/macosusercaptureseatbelt_test.go's TestMacosUserCaptureSeatbeltProfileDeniesTheSharedHome,
// which loads a capture plan's profile as the sandbox account (macos-user.yml, on a Mac). A Linux
// jail cannot run sandbox-exec at all; what the case leaves to a human with a Mac is in
// docs/plans/install-capture.md's slice 6 hardware checklist.

const testStagingRoot = "/Users/Shared/yolo-captures/probetool"

// captureWriteAllowBlock returns the profile text from the `(allow file-write*` form to the
// blank line that ends it — the block whose contents ARE the capture's write policy.
func captureWriteAllowBlock(t *testing.T, profile string) string {
	t.Helper()
	start := strings.Index(profile, "(allow file-write*")
	if start < 0 {
		t.Fatalf("no write-allow block in profile:\n%s", profile)
	}
	rest := profile[start:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// The shared home is the whole point of the slice: a capture must not be able to write it.
func TestCaptureProfileDeniesWritesToTheSharedHome(t *testing.T) {
	p := SeatbeltCaptureProfile(testStagingRoot)
	if strings.Contains(captureWriteAllowBlock(t, p), SandboxHome()) {
		t.Errorf("the capture profile ALLOWS writes to the shared home %s — that is the "+
			"session profile's policy, not a capture's:\n%s", SandboxHome(), p)
	}
	deny := `(deny file-write* (subpath "` + SandboxHome() + `"))`
	if !contains(p, deny) {
		t.Errorf("capture profile missing %q:\n%s", deny, p)
	}
	// SBPL is last-match-wins, so a deny BEFORE the re-allow is not a deny.
	if idx(p, deny) <= idx(p, "(allow file-write*") {
		t.Error("the shared-home deny must FOLLOW the write-allow block, or the allow wins")
	}
}

// The read side is the other half: a capture that could READ /Users/_yolojail could read the
// machine's one credential store, and an installer is arbitrary vendor code.
func TestCaptureProfileDoesNotReAllowReadsOfTheSharedHome(t *testing.T) {
	p := SeatbeltCaptureProfile(testStagingRoot)
	usersDeny := `(deny file-read* (subpath "/Users"))`
	if !contains(p, usersDeny) {
		t.Fatalf("capture profile missing the /Users read deny:\n%s", p)
	}
	after := p[idx(p, usersDeny):]
	if strings.Contains(after, SandboxHome()) {
		t.Errorf("the capture profile re-allows reads under the shared home %s after the "+
			"/Users deny — a capture has no business reading the credential store:\n%s",
			SandboxHome(), p)
	}
}

// Exactly one project-specific writable subtree, plus the OS scratch dirs, and no workspace.
func TestCaptureProfileWritableSetIsTheStagingTreeAndScratchOnly(t *testing.T) {
	p := SeatbeltCaptureProfile(testStagingRoot)
	block := captureWriteAllowBlock(t, p)
	for _, want := range []string{
		`(subpath "` + testStagingRoot + `")`,
		`(subpath "/tmp")`,
		`(subpath "/private/tmp")`,
		`(subpath "/var/folders")`,
		`(subpath "/private/var/folders")`,
		`(subpath "/dev")`,
	} {
		if !strings.Contains(block, want) {
			t.Errorf("write-allow block missing %q:\n%s", want, block)
		}
	}
	// Six clauses and no more. A seventh is a widening nobody meant.
	if got := strings.Count(block, "(subpath "); got != 6 {
		t.Errorf("write-allow block has %d subpath clauses, want 6:\n%s", got, block)
	}
	if !contains(p, `(deny file-write* (subpath "/"))`) {
		t.Errorf("capture profile does not deny writes at / first:\n%s", p)
	}
	if idx(p, `(deny file-write* (subpath "/"))`) >= idx(p, "(allow file-write*") {
		t.Error("the root write deny must precede the re-allow")
	}
}

// The staging tree must be READABLE too, and every intermediate dir under /Users/Shared needs a
// traversal grant — the same gap that broke `git ls-files` for a nested workspace.
func TestCaptureProfileGrantsTraversalToTheStagingTree(t *testing.T) {
	p := SeatbeltCaptureProfile(testStagingRoot)
	for _, want := range []string{
		`(literal "/Users")`,
		`(literal "/Users/Shared")`,
		`(literal "/Users/Shared/yolo-captures")`,
		`(subpath "` + testStagingRoot + `")`,
	} {
		if !contains(p, want) {
			t.Errorf("capture profile missing read grant %q:\n%s", want, p)
		}
	}
	// The intermediate is traversal only — a subpath grant there would re-allow reads of
	// every OTHER program's capture staging tree beside this one.
	if contains(p, `(subpath "/Users/Shared/yolo-captures")`) {
		t.Error("the capture root is granted as a subpath — that exposes every other " +
			"program's staging tree to this installer")
	}
}

// A caller that lost its staging path must get a profile that cannot write, never one that can
// write everywhere. `(subpath "")` is a prefix match on every path.
func TestCaptureProfileWithNoStagingRootIsFailClosed(t *testing.T) {
	p := SeatbeltCaptureProfile("")
	if contains(p, `(subpath "")`) {
		t.Errorf("an empty staging root emitted `(subpath \"\")`, which matches every "+
			"path — the fail-closed case became fail-wide-open:\n%s", p)
	}
	block := captureWriteAllowBlock(t, p)
	if got := strings.Count(block, "(subpath "); got != 5 {
		t.Errorf("write-allow block has %d subpath clauses, want the 5 scratch dirs only:\n%s",
			got, block)
	}
}

// The same denies the session profile carries. A capture is MORE confined than a session, never
// less, so anything the session refuses this must refuse too.
func TestCaptureProfileKeepsTheSessionProfileDenies(t *testing.T) {
	p := SeatbeltCaptureProfile(testStagingRoot)
	for _, want := range []string{
		"(allow default)",
		`(deny file-read* (subpath "/Volumes"))`,
		`(allow file-read* (subpath "/Volumes/Macintosh HD"))`,
		`#"^/dev/r?disk"`,
		`#"^/private/dev/r?disk"`,
		`#"^/dev/bpf"`,
		`(deny file-read* (subpath "/Library/Keychains"))`,
		"(allow process-info*)",
		"(allow sysctl-read)",
	} {
		if !contains(p, want) {
			t.Errorf("capture profile is LESS confined than a session: missing %q:\n%s", want, p)
		}
	}
}

// SBPL escaping, same as the session profile — a staging root is a path yolo composed from a
// program name, but the escaping must not depend on that staying true.
func TestCaptureProfileEscapesTheStagingPath(t *testing.T) {
	p := SeatbeltCaptureProfile(`/Users/Shared/a"b\c`)
	if !contains(p, `\"`) || !contains(p, `\\`) {
		t.Errorf("SBPL escaping absent:\n%s", p)
	}
}

// THE SEALED CAPTURE PROFILE (FP-D24) is the capture profile with the seal after it: everything the
// capture profile pins still holds, and the loopback and the nix daemon are denied AFTER
// `(allow default)` — last match wins, so a deny before it would be none.
func TestSealedCaptureProfileDeniesTheLoopbackAndTheNixDaemonAfterAllowDefault(t *testing.T) {
	p := SeatbeltSealedCaptureProfile(testStagingRoot)
	if problems := captureProfileProblems(p); len(problems) > 0 {
		t.Errorf("the sealed profile breaks the capture profile's own rules: %v", problems)
	}
	if !strings.HasPrefix(p, SeatbeltCaptureProfile(testStagingRoot)) {
		t.Error("the sealed profile is not the capture profile with the seal appended")
	}
	allow := idx(p, "(allow default)")
	for _, deny := range []string{
		`(deny network-outbound (remote ip "localhost:*"))`,
		`(deny network-outbound (remote unix-socket (path-literal "/nix/var/nix/daemon-socket/socket")))`,
		`(deny file-read* file-write* (subpath "/nix/var/nix/daemon-socket"))`,
	} {
		if !contains(p, deny) {
			t.Errorf("the sealed profile lacks %q:\n%s", deny, p)
		} else if idx(p, deny) < allow {
			t.Errorf("%q precedes `(allow default)`, so it denies nothing", deny)
		}
	}
	if problems := sealedProfileProblems(p); len(problems) > 0 {
		t.Errorf("the sealed profile fails its own check: %v", problems)
	}
	// The network beyond the loopback stays: a build fetches its dependencies.
	if contains(p, `(deny network-outbound (remote ip "*:*"))`) || contains(p, "(deny network*") {
		t.Errorf("the sealed profile denies the network a build fetches from:\n%s", p)
	}
}

// The plain capture profile, an installer's, is NOT sealed: the seal is a fork build's alone (FP-D9
// scopes it to the fork route), and the check says so of it.
func TestTheInstallerCaptureProfileIsNotSealed(t *testing.T) {
	if problems := sealedProfileProblems(SeatbeltCaptureProfile(testStagingRoot)); len(problems) != 3 {
		t.Errorf("the installer's capture profile reads as sealed: %v", problems)
	}
}

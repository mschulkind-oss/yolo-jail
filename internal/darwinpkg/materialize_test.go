package darwinpkg

import (
	"reflect"
	"strings"
	"testing"
)

// TestProfilePathsFromStdout covers the pure tail of Materialize: the last
// non-blank line of `--print-out-paths` stdout is the profile; blank/no output
// yields nil (the "no store path" error branch).
func TestProfilePathsFromStdout(t *testing.T) {
	// No pkgconfig dir → PATH prefix is <out>/bin, env empty.
	noPkg := func(string) bool { return false }

	// Multiple lines: the LAST non-blank is the profile.
	got := ProfilePathsFromStdout("/nix/store/aaa\n/nix/store/bbb\n\n", []string{"skip1"}, noPkg)
	if got == nil {
		t.Fatal("expected a result, got nil")
	}
	if want := []string{"/nix/store/bbb/bin"}; !reflect.DeepEqual(got.PathPrefix, want) {
		t.Errorf("PathPrefix = %q, want %q (last line wins)", got.PathPrefix, want)
	}
	if !reflect.DeepEqual(got.Skipped, []string{"skip1"}) {
		t.Errorf("Skipped = %q, want [skip1]", got.Skipped)
	}
	if len(got.Env) != 0 {
		t.Errorf("Env = %v, want empty (no pkgconfig)", got.Env)
	}

	// pkgconfig dir present → PKG_CONFIG_PATH exposed.
	yesPkg := func(string) bool { return true }
	got = ProfilePathsFromStdout("/nix/store/ccc\n", nil, yesPkg)
	if got == nil || got.Env["PKG_CONFIG_PATH"] != "/nix/store/ccc/lib/pkgconfig" {
		t.Errorf("expected PKG_CONFIG_PATH, got %+v", got)
	}

	// Empty / whitespace-only stdout → nil (the no-store-path branch).
	if ProfilePathsFromStdout("", nil, noPkg) != nil {
		t.Error("empty stdout must yield nil")
	}
	if ProfilePathsFromStdout("   \n\t\n", nil, noPkg) != nil {
		t.Error("whitespace-only stdout must yield nil")
	}
}

// Materialize's build must be ROOTED (N1) — asserted on the argv it actually runs, because
// Materialize itself needs a real nix. This test exists because of a SURVIVING mutation:
// replacing the out-link with "" restored the unrooted `--no-link` build, and every other
// test stayed green. BuildProfileArgv's own test proves --out-link works when asked for; only
// this one proves Materialize asks.
func TestMaterializeArgvIsRooted(t *testing.T) {
	argv := materializeArgv("aarch64-darwin", "/homes/alice")
	joined := strings.Join(argv, " ")
	want := ProfileRootLink("/homes/alice")
	if !strings.Contains(joined, "--out-link "+want) {
		t.Errorf("Materialize must root its build at %q (an unrooted profile is the N1 "+
			"defect — a nix GC can collect the agent's toolset mid-session):\n%s", want, joined)
	}
	if strings.Contains(joined, "--no-link") {
		t.Errorf("Materialize must not build with --no-link:\n%s", joined)
	}
	// The root path must be under the PASSED home, not the process $HOME.
	t.Setenv("HOME", "/homes/bob")
	if again := strings.Join(materializeArgv("aarch64-darwin", "/homes/alice"), " "); again != joined {
		t.Errorf("materializeArgv must key on its home argument, not $HOME:\n%s", again)
	}
}

// The profile's own store path is carried on the result, not just its bin/ subdir: it is
// what `describe` and `check --at host` report and what the GC root pins, and a reporter
// that had to strip "/bin" back off the PATH prefix would be reconstructing a fact the
// materializer already knew.
func TestProfilePathsFromStdoutCarriesProfilePath(t *testing.T) {
	got := ProfilePathsFromStdout("/nix/store/xyz-yolo-noncontainer-packages\n", nil,
		func(string) bool { return false })
	if got == nil {
		t.Fatal("expected a result")
	}
	if got.ProfilePath != "/nix/store/xyz-yolo-noncontainer-packages" {
		t.Errorf("ProfilePath = %q, want the store out path itself", got.ProfilePath)
	}
	// And it must be the PARENT of the PATH prefix, not the prefix — the two are
	// different answers and confusing them puts a nonexistent dir on PATH.
	if len(got.PathPrefix) != 1 || got.PathPrefix[0] != got.ProfilePath+"/bin" {
		t.Errorf("PathPrefix %q should be ProfilePath + /bin (%q)", got.PathPrefix, got.ProfilePath)
	}
}

// TestStderrTailBounded confirms the ring keeps only the last N lines (the
// Python stderr_tail cap at 30).
func TestStderrTailBounded(t *testing.T) {
	tail := newStderrTail(3)
	for _, l := range []string{"a", "b", "c", "d", "e"} {
		tail.push(l)
	}
	if want := []string{"c", "d", "e"}; !reflect.DeepEqual(tail.lines(), want) {
		t.Errorf("tail = %q, want %q", tail.lines(), want)
	}
}

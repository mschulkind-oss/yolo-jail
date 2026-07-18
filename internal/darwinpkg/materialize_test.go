package darwinpkg

import (
	"reflect"
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

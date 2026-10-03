package capture

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// autofailure_test.go covers the store's memo of a failed AUTO-capture: what the launch trigger
// (internal/cli/autocapture.go) reads to stop re-running an installer on every launch. The policy
// — how long to wait, and what resets it — is the trigger's; the store only keeps the record, one
// per (bin, platform), beside the entries it failed to add to.

// A recorded failure reads back as it was written, per (bin, platform), and clearing it leaves
// nothing to read.
func TestAnAutoFailureRoundTripsPerProgramAndPlatform(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	last := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	want := AutoFailure{Bin: "claude", Platform: "linux/arm64", Version: "0.11.2", Failures: 2, Last: last}
	must(t, s.RecordAutoFailure(want))

	got, ok := s.AutoFailure("claude", "linux/arm64")
	if !ok {
		t.Fatal("a recorded failure did not read back")
	}
	if got.Bin != want.Bin || got.Platform != want.Platform || got.Version != want.Version ||
		got.Failures != want.Failures || !got.Last.Equal(want.Last) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
	// Another platform's capture is another record: a Mac's podman jail and its Apple Container
	// jail are both linux, but an arm64 failure says nothing about amd64.
	if _, ok := s.AutoFailure("claude", "linux/amd64"); ok {
		t.Error("a failure on linux/arm64 read back for linux/amd64")
	}
	if _, ok := s.AutoFailure("codex", "linux/arm64"); ok {
		t.Error("claude's failure read back for codex")
	}

	must(t, s.ClearAutoFailure("claude", "linux/arm64"))
	if _, ok := s.AutoFailure("claude", "linux/arm64"); ok {
		t.Error("a cleared failure still reads back")
	}
	// Clearing what is not there is not an error: a capture that succeeds first time clears.
	must(t, s.ClearAutoFailure("claude", "linux/arm64"))
}

// The memo lives BESIDE the entries, never among them: the resolver scans entries/, and a file
// there would be read as a torn entry.
func TestAnAutoFailureIsKeptOutsideTheEntries(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	must(t, s.RecordAutoFailure(AutoFailure{Bin: "agy", Platform: "linux/arm64", Failures: 1, Last: time.Now()}))
	if keys, err := s.EntryKeys(); err != nil || len(keys) != 0 {
		t.Errorf("EntryKeys after recording a failure = %v, %v; want none", keys, err)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, autoFailuresLeaf)); err != nil {
		t.Errorf("the memo is not under %s: %v", autoFailuresLeaf, err)
	}
}

// A program name that is not one path segment is refused rather than written somewhere else, the
// rule Stage applies to the same names.
func TestAnAutoFailureRefusesATraversingName(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	for _, bin := range []string{"", ".", "..", "../x", "a/b"} {
		if err := s.RecordAutoFailure(AutoFailure{Bin: bin, Platform: "linux/arm64", Failures: 1}); err == nil {
			t.Errorf("RecordAutoFailure(%q) wrote a memo", bin)
		}
		if _, ok := s.AutoFailure(bin, "linux/arm64"); ok {
			t.Errorf("AutoFailure(%q) read a memo", bin)
		}
	}
}

// An unreadable memo is no memo: the trigger then captures, which is what it did before any memo
// existed, rather than suppressing a capture on a record it cannot read.
func TestACorruptAutoFailureReadsAsNone(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	must(t, s.RecordAutoFailure(AutoFailure{Bin: "claude", Platform: "linux/arm64", Failures: 1, Last: time.Now()}))
	p, err := s.autoFailurePath("claude", "linux/arm64")
	must(t, err)
	must(t, os.WriteFile(p, []byte("{not json"), 0o644))
	if _, ok := s.AutoFailure("claude", "linux/arm64"); ok {
		t.Error("a corrupt memo read as a failure")
	}
}

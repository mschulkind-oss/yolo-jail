package packsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// An unreadable identity index is refused naming the command that removes it and why that is
// safe, never a bare "delete it".
func TestAnUnreadableIdentityIndexNamesTheCommandThatRemovesIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), IdentityIndexName)
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadIdentityIndex(path)
	if err == nil {
		t.Fatal("a corrupt index loaded")
	}
	msg := err.Error()
	for _, want := range []string{"`rm " + path + "`", "removing it is safe", "next launch writes it again"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal lacks %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "delete it") {
		t.Errorf("the refusal still says to delete it by hand: %s", msg)
	}
}

// ClaimedHandles keeps parked handles and the handles of indexed identities not selected now, and
// claims every handle when the index cannot be read.
func TestClaimedHandlesKeepsParkedAndUnselectedIndexedHandles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, IdentityIndexName)
	idx := &IdentityIndex{Packs: map[string]string{"dir:/a": "acme/a", "dir:/b": "acme/b"}}
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	claimed := ClaimedHandles(path, []string{"acme/b"})
	for handle, want := range map[string]bool{"acme/a": true, "acme/b": false, "acme/c": false, ParkedHandle("dir:/z"): true} {
		if got := claimed(handle); got != want {
			t.Errorf("claimed(%q) = %v, want %v", handle, got, want)
		}
	}
	if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ClaimedHandles(path, nil)("acme/c") {
		t.Error("an unreadable index claims nothing, so a prune would drop what it cannot judge")
	}
}

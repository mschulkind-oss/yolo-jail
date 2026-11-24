package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The pairing in readableScopeCheckWithSettings is by identity: a projection that lists the same
// sources in another order lends no pending writes to a source of a different name. Deleting the
// identity guard (or the pairedProjection call) makes the git source take github's pending
// projection, which the swapped run below would show.
func TestAReorderedProjectionLendsNoPendingWritesToAnotherSource(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	writeReadableScopeUser(t, home, "[allow.git]\nalways = [\"push\"]\n")
	ws := t.TempDir()
	writeReadableScopeRemote(t, ws, "[remote \"origin\"]\n\turl = git@github.com:acme/repo.git\n")
	if err := os.WriteFile(filepath.Join(ws, WorkspaceTOMLConfigName), []byte("[requests.git]\npush = [\"main\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pending := func() map[string]bool {
		check := vacuousScopeCheckFor(t, ws, true)
		out := map[string]bool{}
		for _, s := range check.Sources {
			out[s.Source] = s.policyPending != nil || len(s.policyWriteLines) > 0
		}
		return out
	}
	if got := pending(); !got["git"] {
		t.Fatalf("control: the git source holds a pending write projection, got %v", got)
	}
	real := scopeProjectionFor
	t.Cleanup(func() { scopeProjectionFor = real })
	scopeProjectionFor = func(w string, b []ScopeBroker, in ReadableGateInput, ind bool) *ScopeCheck {
		c := real(w, b, in, ind)
		slices.Reverse(c.Sources)
		return c
	}
	if got := pending(); got["git"] || got["github"] {
		t.Fatalf("a reordered projection lent pending writes across sources: %v", got)
	}
}

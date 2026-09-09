package cli

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/stores"
)

// TestStoresOptionsWiring pins the front door's two injections, each of which is
// a single line that can be deleted with internal/cli/stores' own suite still
// fully green — the failure shape AGENTS.md names (a test that pins the callee
// while the call site is unpinned).
//
// DetectRuntime is the one thing that package cannot resolve for itself: it does
// not import internal/config, so without this line an Apple Container host's
// image store reports "no container runtime on this notch" — a whole section of
// the inventory silently wrong, with no test anywhere failing.
//
// MUTATION: delete either assignment in storesOptions and this fails.
func TestStoresOptionsWiring(t *testing.T) {
	opts := storesOptions([]string{"stores"})
	if opts.DetectRuntime == nil {
		t.Error("storesOptions did not inject the config-aware runtime resolver; the image-store " +
			"section goes blind on any notch whose runtime the config decides")
	}
	if opts.IsTTYStdout == nil {
		t.Error("storesOptions did not inject the TTY probe; with Color set and no probe, ANSI " +
			"would be written down a pipe")
	}
	if !opts.Color {
		t.Error("storesOptions did not request color; the printer gates it on the TTY probe anyway")
	}
}

// TestStoresFlagsReachTheEngine: the front door parses argv through the engine's
// own ParseArgs, so a flag cannot be recognized by one and ignored by the other.
func TestStoresFlagsReachTheEngine(t *testing.T) {
	opts := storesOptions([]string{"stores", "--json", "--age", "--no-record"})
	if !opts.JSON || !opts.Age || !opts.NoRecord {
		t.Errorf("flags did not reach the engine: %+v", stores.Options{
			JSON: opts.JSON, Age: opts.Age, NoRecord: opts.NoRecord,
		})
	}
}

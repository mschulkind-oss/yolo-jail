package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/stores"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// TestStoresOptionsWiring pins the front door's injections, each of which is a single line that
// can be deleted with internal/cli/stores' own suite still fully green — the failure shape
// AGENTS.md names (a test that pins the callee while the call site is unpinned).
//
// SelectedRuntime and Sources are what that package cannot resolve for itself: it imports
// neither internal/config nor `yolo ps`'s discovery, so without them the machine-wide rows name
// the wrong prune and no technology-bound section is collected at all.
//
// MUTATION: delete any assignment in storesOptions and this fails.
func TestStoresOptionsWiring(t *testing.T) {
	stubStoresDiscover(t)
	opts, rc := storesOptions([]string{"stores"}, io.Discard)
	if rc != 0 {
		t.Fatalf("rc=%d", rc)
	}
	if opts.SelectedRuntime == nil {
		t.Error("storesOptions did not inject the config-aware runtime resolver")
	}
	if len(opts.Sources) != 1 || opts.Sources[0].Confinement != "podman" ||
		len(opts.Sources[0].Jails) != 1 || opts.Sources[0].Jails[0].Workspace != "/w" ||
		opts.Sources[0].Setup != "podman-self" {
		t.Errorf("storesOptions handed the engine sources %+v, want ps's discovery", opts.Sources)
	}
	if opts.IsTTYStdout == nil {
		t.Error("storesOptions did not inject the TTY probe; with Color set and no probe, ANSI " +
			"would be written down a pipe")
	}
	if !opts.Color {
		t.Error("storesOptions did not request color; the printer gates it on the TTY probe anyway")
	}
	if wd, _ := os.Getwd(); opts.Workspace != wd {
		t.Errorf("storesOptions named workspace %q, want the directory it ran in (%q), whose rescues are listed with no jail running", opts.Workspace, wd)
	}
}

// TestStoresFlagsReachTheEngine: the front door parses argv through the engine's
// own ParseArgs, so a flag cannot be recognized by one and ignored by the other.
func TestStoresFlagsReachTheEngine(t *testing.T) {
	stubStoresDiscover(t)
	opts, _ := storesOptions([]string{"stores", "--json", "--age", "--no-record"}, io.Discard)
	if !opts.JSON || !opts.Age || !opts.NoRecord {
		t.Errorf("flags did not reach the engine: %+v", stores.Options{
			JSON: opts.JSON, Age: opts.Age, NoRecord: opts.NoRecord,
		})
	}
}

// TestStoresSetupNarrowsTheDiscovery (named-setups.md §12.6): `yolo stores --setup <name>` asks
// only that setup's technology, resolved as a launch resolves it, and an undefined name is refused
// with the launch's message, exit 2.
func TestStoresSetupNarrowsTheDiscovery(t *testing.T) {
	asked := stubStoresDiscover(t)
	opts, rc := storesOptions([]string{"stores", "--setup", "apple-container-self", "--json"}, io.Discard)
	if rc != 0 || !opts.JSON {
		t.Fatalf("rc=%d json=%v", rc, opts.JSON)
	}
	if got := *asked; got == nil || got.Name != "apple-container-self" {
		t.Errorf("the discovery was narrowed to %+v, want apple-container-self", got)
	}
	var errw strings.Builder
	if _, rc := storesOptions([]string{"stores", "--setup", "fast"}, &errw); rc != 2 ||
		!strings.Contains(errw.String(), "yolo stores: ") || !strings.Contains(errw.String(), `"fast"`) {
		t.Errorf("an undefined setup: rc=%d err=%q", rc, errw.String())
	}
}

// stubStoresDiscover replaces the discovery with one listed Podman source running a jail of /w,
// and returns where the setup it was narrowed to is recorded.
func stubStoresDiscover(t *testing.T) **setup.Setup {
	t.Helper()
	saved := storesDiscover
	t.Cleanup(func() { storesDiscover = saved })
	var asked *setup.Setup
	storesDiscover = func(only *setup.Setup) []*psSource {
		asked = only
		return []*psSource{{spec: psSourceSpecs[0], state: runtime.SourceListed,
			rows: []*psRow{{name: "yolo-w-1", workspace: "/w"}}}}
	}
	return &asked
}

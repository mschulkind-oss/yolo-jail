package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// A fork build's capture jail is keyed by the setup its act ran as (named-setups-plan.md SUP-D14),
// which a later process cannot re-derive, so the staging tree's runtime record carries the name
// (writeForkBuildRuntime) and every later reader takes it from there (stagingJailName). A record an
// older yolo wrote, with the runtime alone, names the project key, as that yolo's jail was keyed; a
// staging tree with no record names what the caller predicted (stagingJailNameOr).
func TestAForkBuildsRecordNamesItsJail(t *testing.T) {
	staging := filepath.Join(t.TempDir(), "fork-staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := runtime.FromWorkspace(staging)
	predicted := runtime.JailName(staging, "predicted")
	if got := stagingJailNameOr(staging, predicted); got != predicted {
		t.Errorf("no record: name = %q, want the predicted %q", got, predicted)
	}
	if got := stagingJailName(staging); got != legacy {
		t.Errorf("no record: stagingJailName = %q, want the legacy %q", got, legacy)
	}

	recorded := runtime.JailName(staging, "apple-container-self")
	if err := writeForkBuildRuntime(staging, "container", recorded); err != nil {
		t.Fatal(err)
	}
	if got := stagingJailNameOr(staging, predicted); got != recorded {
		t.Errorf("recorded: name = %q, want %q", got, recorded)
	}
	if rt, err := readForkBuildRuntime(staging); err != nil || rt != "container" {
		t.Errorf("the name line broke the runtime record: %q, %v", rt, err)
	}

	// An older yolo's record: the runtime alone.
	if err := os.WriteFile(forkBuildRuntimeRecordPath(staging), []byte("podman\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := stagingJailNameOr(staging, predicted); got != legacy {
		t.Errorf("an older record: name = %q, want the legacy %q", got, legacy)
	}

	// A name that is not this staging tree's jail is not believed.
	if err := writeForkBuildRuntime(staging, "podman", runtime.JailName(t.TempDir(), "podman-self")); err != nil {
		t.Fatal(err)
	}
	if got := stagingJailNameOr(staging, predicted); got != legacy {
		t.Errorf("a foreign name: name = %q, want the legacy %q", got, legacy)
	}
}

package cli

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// pipelineBackend is the backend a run pipeline handed o would launch on when the caller named
// one: that of the setup o.SetupName names, "" when it names none. A verb that boots a jail for
// a backend it chose (a capture, a fork build) names it as a setup (runCaptureJail), never as
// the removed YOLO_RUNTIME.
func pipelineBackend(o run.Options) string {
	if o.SetupName == "" {
		return ""
	}
	s, err := setup.Lookup(o.SetupName, nil)
	if err != nil {
		return "unknown setup " + o.SetupName
	}
	return s.Backend()
}

// TestListingVerbsFollowTheSelectedSetup: the tolerant listing verbs (ps, prune, stop, describe,
// capture) act on the backend of the setup a launch from the workspace would select, through the
// one selection (config.SelectSetup), and a selection that refuses still answers. Driven through
// detectListingRuntime, the call every listing verb makes, so it fails if that call stops
// reading the selection.
func TestListingVerbsFollowTheSelectedSetup(t *testing.T) {
	_, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{}`)
	t.Setenv("YOLO_VERSION", "")

	t.Setenv("YOLO_RUNTIME", "")
	t.Setenv("YOLO_SETUP", "apple-container-self")
	if got := detectListingRuntime(repo); got != "container" {
		t.Errorf("YOLO_SETUP=apple-container-self lists %q, want container", got)
	}
	t.Setenv("YOLO_SETUP", "seatbelt-self")
	if got := detectListingRuntime(repo); got != "macos-user" {
		t.Errorf("YOLO_SETUP=seatbelt-self lists %q, want macos-user", got)
	}
	// A removed YOLO_RUNTIME on the host refuses a launch; a listing verb still reads what it meant.
	t.Setenv("YOLO_SETUP", "")
	t.Setenv("YOLO_RUNTIME", "container")
	if got := detectListingRuntime(repo); got != "container" {
		t.Errorf("a removed YOLO_RUNTIME=container lists %q, want what it meant (container)", got)
	}
	// The removed `runtime` key, until slice 5, chooses its built-in when nothing else selects.
	t.Setenv("YOLO_RUNTIME", "")
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"runtime": "container"}`)
	if got := detectListingRuntime(repo); got != "container" {
		t.Errorf("a workspace `runtime: container` lists %q, want container", got)
	}
}

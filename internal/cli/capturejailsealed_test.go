package cli

import (
	"bytes"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// THE INSTALLER CAPTURE JAIL GETS NO CREDENTIAL. `yolo capture <bin>` runs a vendor installer
// script fetched, unpinned, from the internet, so its jail must be the SEALED one (run.Options.Sealed,
// run/seal.go): an empty credential channel, no host service or broker, no shared-credentials
// directory, no host file or mount. run.TestACaptureJailsLaunchCrossesNoCredential holds what that
// seal withholds over the container argv; this holds the call site that asks for it, over the
// options the whole host act (resolve, stage, jail, admit) hands the pipeline. Red if the
// installer capture stops being sealed.
func TestTheInstallerCaptureJailIsSealed(t *testing.T) {
	captureFixtureHome(t, captureFixtureInstaller)
	var seen run.Options
	withFakeCaptureJail(t, fakeCaptureJail(t, &seen, []capture.ManifestEntry{
		{Path: ".local", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin", Kind: capture.KindDir, Mode: "0755"},
		{Path: ".local/bin/probetool", Kind: capture.KindFile, Mode: "0755", Size: 64},
	}))
	var out, errw bytes.Buffer
	if rc := captureHost([]string{"probetool"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc = %d\nstdout: %s\nstderr: %s", rc, out.String(), errw.String())
	}
	if !seen.Sealed {
		t.Error("the installer capture jail is not sealed: it is handed the user's provider credentials, " +
			"shared-credentials directories and host services while it runs an unpinned installer script")
	}
	if seen.SealedTree != "" {
		t.Errorf("the installer capture jail is told it builds the patched extension %q", seen.SealedTree)
	}
	// A fork's build records its runtime for its own recovery; an installer capture has no such record.
	if seen.OnRuntimeResolved != nil {
		t.Error("the installer capture jail is handed a fork build's runtime witness")
	}
}

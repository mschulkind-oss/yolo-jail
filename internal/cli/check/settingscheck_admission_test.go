package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// validatorMarkerPack writes a settings-validated loophole whose validator touches marker.
func validatorMarkerPack(t *testing.T, root, name, marker string) string {
	t.Helper()
	return settingsCheckPackArgv(t, root, name,
		[]string{"/bin/sh", "-c", "printf x >> " + shquote.Quote(marker), "validator", "{settings}"},
		filepath.Join(t.TempDir(), "doctor-ran"))
}

// A BACKEND THAT DOES NOT START THE SERVICE DOES NOT RUN ITS VALIDATOR. Apple Container starts
// only the OpenAI credential service (run.HostServiceAdmittedOn), so `yolo check` for a workspace
// configured for it reports any other service as not started there, instead of grading settings
// no launch reads. The runtime comes from the workspace config, the path check itself resolves.
func TestCheckLoopholesSkipsTheValidatorOfAServiceTheBackendDoesNotStart(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	marker := filepath.Join(t.TempDir(), "validator-ran")
	validatorMarkerPack(t, moduleRoot, "acme-validator", marker)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(`{"runtime":"container"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	r, out := runCheckLoopholes(t, workspace)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("check ran the validator of a service Apple Container does not start: %v\n%s", err, out)
	}
	if r.failed != 0 || !strings.Contains(out, "loophole acme-validator: not started on container (settings validator not run)") {
		t.Fatalf("no not-started row for the unadmitted service: failed=%d\n%s", r.failed, out)
	}
}

// And on a backend that starts it, the validator still runs.
func TestCheckLoopholesRunsTheValidatorOnABackendThatStartsTheService(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	marker := filepath.Join(t.TempDir(), "validator-ran")
	validatorMarkerPack(t, moduleRoot, "acme-validator", marker)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(`{"runtime":"podman"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, out := runCheckLoopholes(t, workspace)
	if data, err := os.ReadFile(marker); err != nil || string(data) != "x" {
		t.Fatalf("validator did not run on podman: data=%q err=%v\n%s", data, err, out)
	}
	if strings.Contains(out, "not started on") {
		t.Fatalf("podman reported as not starting the service:\n%s", out)
	}
}

// AN UNGATED VALIDATOR IS NOT RUN, AND THE ROW SAYS WHAT TO DO. A pack module nothing vouched for
// (HostExecApproved false: since OQ-TP9 only a caller that resolved no packs produces one) gets a
// warning naming where to report it, the same next step the doctor gate gives.
func TestCheckLoopholesUnvouchedValidatorNamesTheReport(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	retiredLoopholeDir(t)
	moduleRoot := t.TempDir()
	marker := filepath.Join(t.TempDir(), "validator-ran")
	module := validatorMarkerPack(t, moduleRoot, "acme-validator", marker)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, HostExecApproved: false}})
	t.Cleanup(loopholes.ResetPackModules)
	var buf bytes.Buffer
	r := newReporter(&buf, false)
	o := &Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}
	fillDefaults(o)
	o.checkLoopholes(r)
	out := buf.String()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("an unvouched pack's validator ran: %v\n%s", err, out)
	}
	for _, want := range []string{"settings validator not run", "Report it at " + issuesURL, "`yolo --version`"} {
		if !strings.Contains(out, want) {
			t.Errorf("unvouched-validator row lacks %q:\n%s", want, out)
		}
	}
}

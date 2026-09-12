package run

// hostlayerreport_test.go pins the LAUNCHER's half of the fail-closed host-layer read
// (OQ-CO10): the jail refuses a host layer that was delivered and cannot be read, so what
// "delivered" means has to be recorded by the only half that can tell the difference
// between "the user has no such file" and "it did not arrive".

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A grant whose source EXISTS is mounted and recorded; one whose source does not is
// neither. The second half is what keeps the jail from refusing the common case — a user
// who has never written a settings.json — and it cannot be tested from inside the jail,
// which is why it is pinned here.
func TestHostLayerReportRecordsOnlyWhatWasMounted(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	// Two surfaces declaring a host layer; only one of them has a file on this host.
	if err := os.WriteFile(filepath.Join(src, "pack.json"), []byte(`{"name":"acme","contributes":[
	  {"kind":"config","config":[
	    {"agent":"acme","name":"settings","codec":"json","path":"~/.acme/settings.json",
	     "readsHost":true,"managed":{"x":1}},
	    {"agent":"acme","name":"prefs","codec":"json","path":"~/.acme/prefs.json",
	     "readsHost":true,"managed":{"y":1}}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)
	if err := os.MkdirAll(filepath.Join(home, ".acme"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".acme", "settings.json"),
		[]byte(`{"user":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-hostlayers")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	in := &assembleInput{
		wsState:      filepath.Join(home, ".yolo", "home"),
		mountTargets: map[string]struct{}{},
		packs:        loaded,
	}
	mounts := strings.Join(o.hostFileArgs(in), " ")
	env := strings.Join(o.hostLayerEnv(in), " ")

	if len(in.hostLayersDelivered) != 1 ||
		!strings.HasSuffix(in.hostLayersDelivered[0], "/settings.json") {
		t.Fatalf("delivered = %v, want exactly the one grant whose source exists",
			in.hostLayersDelivered)
	}
	// The recorded string is the MOUNT DESTINATION, not the host path: it is what the jail
	// opens, and the report is a claim about the jail's filesystem.
	if !strings.Contains(mounts, in.hostLayersDelivered[0]) {
		t.Errorf("the report names %q and the argv does not mount it:\n%s",
			in.hostLayersDelivered[0], mounts)
	}
	if strings.Contains(env, "prefs.json") {
		t.Errorf("prefs.json is reported as delivered and its source does not exist — the "+
			"jail would refuse to start for a file the user has simply never written:\n%s", env)
	}
	if !strings.Contains(env, packload.HostLayerEnvVar+"=") ||
		!strings.Contains(env, `"delivery":"supported"`) {
		t.Errorf("the launch did not report its host layers: %s", env)
	}
}

// EMITTED ON EVERY LAUNCH, including one that delivered nothing. This is the property the
// jail's tolerance of an ABSENT variable rests on: absence has to mean "a launcher older
// than this variable" and nothing else, or the two halves lose the ability to tell a real
// delivery failure from an old host binary.
func TestHostLayerReportIsEmittedWithNothingDelivered(t *testing.T) {
	o := &Options{Workspace: t.TempDir()}
	env := strings.Join(o.hostLayerEnv(&assembleInput{}), " ")
	if want := packload.HostLayerEnvVar + `={"delivery":"supported"}`; !strings.Contains(env, want) {
		t.Errorf("hostLayerEnv = %q, want it to contain %q", env, want)
	}
}

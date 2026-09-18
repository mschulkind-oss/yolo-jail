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
	"github.com/mschulkind-oss/yolo-jail/internal/render"
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

// THE LABEL IS THE LAUNCHER'S, AND IT IS READ OFF THE MARK. Two identical deliveries of the
// same shape, differing only in whether yolo has already rendered that surface into this
// home ([OQ-CR6], docs/design/config-target-resolution.md): the one it has is labelled a
// RENDER, so the jail keeps it as a baseline instead of folding yolo's own keys back in as
// the user's.
//
// The mark, not the posture. An ABSENT `host_management` resolves to `assert` (OQ-CO2), so
// labelling from the declared contract would mark every default machine's settings.json a
// render and stop every jail composing the settings file its user already has — which is
// the onboarding path [P7] keeps frictionless. This test writes NO host_management at all,
// which is that default, and still expects the unrendered home to be unlabelled.
func TestHostLayerReportLabelsOnlyWhatYoloHasRendered(t *testing.T) {
	home := packHome(t)
	src := filepath.Join(t.TempDir(), "acme")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "pack.json"), []byte(`{"name":"acme","contributes":[
	  {"kind":"config","config":[
	    {"agent":"acme","name":"settings","codec":"json","path":"~/.acme/settings.json",
	     "readsHost":true,"managed":{"x":1}},
	    {"agent":"acme","name":"prefs","codec":"json","path":"~/.acme/prefs.json",
	     "readsHost":true,"managed":{"y":1}}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `["file://`+src+`"]`)
	for _, name := range []string{"settings.json", "prefs.json"} {
		if err := os.MkdirAll(filepath.Join(home, ".acme"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".acme", name), []byte(`{"user":true}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// The one difference between the two grants: yolo has rendered acme/settings into this
	// home and has never touched acme/prefs. Written through the Target that decides where
	// the record goes, so a launcher labelling off a path the renderer had moved would fail
	// here rather than mislabel in production.
	mark := render.Host(home, nil, render.OwnershipUnstated).ProvenancePath("acme", "settings")
	if err := os.MkdirAll(filepath.Dir(mark), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	o := &Options{Workspace: t.TempDir()}
	_, loaded, _, err := o.stagePacks("yolo-test-hostlayerlabels")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	in := &assembleInput{
		wsState:      filepath.Join(home, ".yolo", "home"),
		mountTargets: map[string]struct{}{},
		packs:        loaded,
	}
	o.hostFileArgs(in)
	env := strings.Join(o.hostLayerEnv(in), " ")

	if len(in.hostLayersRendered) != 1 ||
		!strings.HasSuffix(in.hostLayersRendered[0], "/settings.json") {
		t.Fatalf("rendered = %v, want exactly the surface yolo has rendered into this home. "+
			"Labelling none leaves the jail folding yolo's own output back in as the user's; "+
			"labelling both stops a jail composing a file yolo has never written",
			in.hostLayersRendered)
	}
	// ONE WIRE, not a second variable: the label rides on the report the boot render already
	// switches on, so the fifth disposition is a fact about the same delivery.
	if !strings.Contains(env, packload.HostLayerEnvVar+"=") ||
		!strings.Contains(env, `"rendered":[`) {
		t.Errorf("the label is not on the host-layer report:\n%s", env)
	}
	// And the labelled path is still DELIVERED: the label says what arrived, not whether
	// anything did, so the fail-closed witness keeps its subject.
	if len(in.hostLayersDelivered) != 2 {
		t.Errorf("delivered = %v, want both grants — a labelled delivery is still a delivery",
			in.hostLayersDelivered)
	}
}

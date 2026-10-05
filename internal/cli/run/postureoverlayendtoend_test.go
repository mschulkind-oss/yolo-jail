package run

// postureoverlayendtoend_test.go drives OQ-3's host-only SCALAR end to end
// (docs/design/notch-scoped-config-contributions.md NS-D19 to NS-D24), through the same three
// production steps as hostlayerendtoend_test.go and with nothing hand-written between them:
// the host apply under `own` (RenderHostPack over a host-posture Collect), each backend's launcher, then
// the jail boot (ConfigurePackSurfaces) handed that launcher's wire and bytes.
//
// The personal pack's guarded posture sets a scalar in pi's settings — a surface the shipped
// pi pack owns — and nothing else. The host file gets it; a jail launched afterwards, on either
// backend, does not: the jail's own posture does not select it, and the managed home's host
// file, which now holds it, is a baseline rather than a layer.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const e2eHostScalar = `{"kind":"autonomy","guarded":{"config":[{"agent":"pi","name":"settings",` +
	`"codec":"json","path":"~/.pi/agent/settings.json","managed":{"hostOnlyScalar":"on-the-host"}}]}}`

// piSettingsAt reads a home's pi settings file as an object.
func piSettingsAt(t *testing.T, home string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(home, ".pi", "agent", "settings.json"))
	if err != nil {
		t.Fatalf("read pi settings under %s: %v", home, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("pi settings is not JSON: %v\n%s", err, data)
	}
	return m
}

// THE HOST-ONLY SCALAR, FROM THE HOST APPLY TO THE BOOT. The apply is `own`'s, the one contract
// that writes since the `assert` retirement (OQ-CO14). Delete the posture overlays from Collect
// and the host never gets the scalar; place them regardless of the posture bit and the jail
// does; drop the render label from either launcher and the host file's scalar composes into
// the jail as the user's own.
func TestAHostOnlyScalarReachesTheHostAndNoJailLaunchedAfterIt(t *testing.T) {
	for _, l := range e2eLaunchers {
		t.Run(l.name, func(t *testing.T) {
			home, loaded := e2eHomeWith(t, e2eHostScalar)
			e2eHostOwn(t, home, loaded)
			if got := piSettingsAt(t, home)["hostOnlyScalar"]; got != "on-the-host" {
				t.Fatalf("host hostOnlyScalar after the apply = %v, want the guarded posture's value", got)
			}

			wire, ctxRoot := l.launch(t, loaded)
			if !strings.Contains(wire, `"rendered":[`) {
				t.Fatalf("the %s launcher did not label the managed home's host file:\n%s", l.name, wire)
			}
			jailHome, log := e2eBootHome(t, loaded, ctxRoot, wire)
			if got, leaked := piSettingsAt(t, jailHome)["hostOnlyScalar"]; leaked {
				t.Errorf("a jail launched after the apply has hostOnlyScalar = %v — the host-only "+
					"scalar reached it", got)
			}
			if !strings.Contains(log, "baseline and not a layer") {
				t.Errorf("the boot did not record that it kept the host copy as a baseline:\n%s", log)
			}
		})
	}
}

// THE TWIN, without the label: the same pack in a home never written into. The jail still
// lacks the scalar, because the jail's posture never selects it — which is what makes the case
// above about the posture AND the label, rather than about a missing host file.
func TestAHostOnlyScalarIsAbsentFromAJailWhoseHostWasNeverAsserted(t *testing.T) {
	for _, l := range e2eLaunchers {
		t.Run(l.name, func(t *testing.T) {
			_, loaded := e2eHomeWith(t, e2eHostScalar)
			wire, ctxRoot := l.launch(t, loaded)
			jailHome, _ := e2eBootHome(t, loaded, ctxRoot, wire)
			if got, leaked := piSettingsAt(t, jailHome)["hostOnlyScalar"]; leaked {
				t.Errorf("the jail has hostOnlyScalar = %v from the guarded posture", got)
			}
		})
	}
}

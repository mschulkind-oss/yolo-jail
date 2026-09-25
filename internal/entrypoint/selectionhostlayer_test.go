package entrypoint

import (
	"os"
	"path/filepath"
	"testing"
)

// writePiHostSettings puts the user's own ~/.pi/agent/settings.json on the host-file mount
// the harness's ctx root stands in for, which is what makes it pi's host LAYER.
// newHostLayerRender is newPioencodeRender with the zai profile table installed up front.
// The harness lowers a table only on the FIRST render and keeps it, so a sequence that
// starts with a plain launch would otherwise never have zai to select.
func newHostLayerRender(t *testing.T) *pioencodeRender {
	t.Helper()
	r := newPioencodeRender(t, zaiReachableJSON)
	r.wireProfiles(`{"zai":{"provider":"zai","model":"glm-5.3"}}`)
	return r
}

func writePiHostSettings(t *testing.T, body string) {
	t.Helper()
	root := os.Getenv("YOLO_CTX_ROOT")
	if root == "" {
		t.Fatal("newPioencodeRender set no YOLO_CTX_ROOT")
	}
	if err := os.WriteFile(filepath.Join(root, "host-pi", "settings.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPiSelectionOutranksAHostLayerValue is OQ-SW1 (ruled B, 2026-09-25): a selection
// outranks a value that came from the user's HOST config. The failure it pins: once one
// plain launch rendered the host's defaultProvider/defaultModel into the jail's own
// settings.json, ApplySelection found keys it held no record for and read them as the
// user's in-jail edit, so `-p zai -- pi` kept the host value forever.
func TestPiSelectionOutranksAHostLayerValue(t *testing.T) {
	r := newHostLayerRender(t)
	writePiHostSettings(t, `{"defaultProvider":"anthropic","defaultModel":"claude-host"}`)

	r.render(t, ``)
	if got := r.piSettings(t)["defaultModel"]; got != "claude-host" {
		t.Fatalf("a plain launch did not render the host layer's model (got %v); the test's premise is wrong", got)
	}

	r.render(t, `{"pi":"zai"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-5.3")

	// A deselect clears yolo's write (OQ-PSW2), so the host value comes back; a later
	// reselect must override it again rather than read it as the user's.
	r.render(t, ``)
	if got := r.piSettings(t)["defaultModel"]; got != "claude-host" {
		t.Errorf("after the deselect pi's model = %v, want the host layer's claude-host back", got)
	}
	r.render(t, `{"pi":"zai"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-5.3")
}

// TestPiSelectionFollowsAHostValueThatChangedOnTheHost: the jail's file still holds the
// host value an EARLIER launch rendered, and the host file has since moved on. That stale
// copy is still the host's (the previous render's provenance says so, and the file is
// unchanged since), so a selection overrides it too.
func TestPiSelectionFollowsAHostValueThatChangedOnTheHost(t *testing.T) {
	r := newHostLayerRender(t)
	writePiHostSettings(t, `{"defaultProvider":"anthropic","defaultModel":"host-v1"}`)
	r.render(t, ``)
	writePiHostSettings(t, `{"defaultProvider":"anthropic","defaultModel":"host-v2"}`)

	r.render(t, `{"pi":"zai"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-5.3")
}

// TestPiSelectionKeepsAnInJailEditOverAHostValue is the OQ-CS2 hazard B must still refuse:
// the user changed pi's model inside the jail after the host value was rendered. That
// value differs from the host layer's, so it is the user's and a same selection keeps it.
func TestPiSelectionKeepsAnInJailEditOverAHostValue(t *testing.T) {
	r := newHostLayerRender(t)
	writePiHostSettings(t, `{"defaultProvider":"anthropic","defaultModel":"claude-host"}`)
	r.render(t, ``)
	r.edit(t, []string{".pi", "agent", "settings.json"}, "defaultModel", "picked-in-jail")
	r.edit(t, []string{".pi", "agent", "settings.json"}, "defaultProvider", "mine")

	for i := 0; i < 2; i++ { // the edit is captured on the first boot and must survive the second
		r.render(t, `{"pi":"zai"}`)
		s := r.piSettings(t)
		if s["defaultModel"] != "picked-in-jail" || s["defaultProvider"] != "mine" {
			t.Errorf("boot %d: pi's pair = %v/%v, want the in-jail edit mine/picked-in-jail kept",
				i+1, s["defaultProvider"], s["defaultModel"])
		}
	}
}

// TestPiSelectionEqualToTheHostValueChangesNothing: the host already names what the
// selection picks, so the file holds that value before and after.
func TestPiSelectionEqualToTheHostValueChangesNothing(t *testing.T) {
	r := newHostLayerRender(t)
	writePiHostSettings(t, `{"defaultProvider":"zai","defaultModel":"glm-5.3"}`)
	r.render(t, ``)
	r.render(t, `{"pi":"zai"}`)
	requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-5.3")
}

package entrypoint

import (
	"os"
	"strings"
	"testing"
)

// A deselect on an ADOPTING boot (OQ-PSW2, docs/reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote):
// the surface's last_render sidecar is absent or does not decode, so the render seeds the
// captured overlay from the file on disk
// (docs/reference/config-migration-to-prism.md#adoption-what-the-first-migration-keeps). The
// failure these pin: adoption re-captured the id yolo's own selection wrote as if the user had
// written it, so the file kept it while the record entry was dropped and boot.log said
// "cleared", and no later deselect could ever clear it. They drive the real pi pack across
// boots through the boot render, so they fail if the render stops handing the clears to the
// compose, not only if the compose stops honoring them.

// piSelectionKeys are the three keys pi's derive writes under the selection namespace.
var piSelectionKeys = []string{"defaultProvider", "defaultModel", "enabledModels"}

// breakLastRender makes the next boot of pi/settings an adopting one, the two ways a boot
// can be: the sidecar gone, or present and not a document the codec decodes.
func breakLastRender(t *testing.T, r *pioencodeRender, how string) {
	t.Helper()
	p := prismLastRenderPath(r.e, "pi", "settings")
	var err error
	switch how {
	case "absent":
		err = os.Remove(p)
	case "undecodable":
		err = os.WriteFile(p, []byte("{not json"), 0o644)
	default:
		t.Fatalf("unknown way to break last_render: %q", how)
	}
	if err != nil {
		t.Fatalf("breaking last_render (%s): %v", how, err)
	}
}

// TestADeselectOnAnAdoptingBootClearsYolosOwnWrite: every key yolo's selection wrote and nobody
// edited leaves the file on the adopting deselect, and stays gone on the next one; boot.log
// records exactly those keys.
func TestADeselectOnAnAdoptingBootClearsYolosOwnWrite(t *testing.T) {
	for _, how := range []string{"absent", "undecodable"} {
		t.Run("last_render "+how, func(t *testing.T) {
			r := newPioencodeRender(t, zaiReachableJSON)
			r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
			r.render(t, `{"pi":"zai"}`)
			settings := r.piSettings(t)
			requirePiSelection(t, settings, r.piModels(t), "zai", "glm-5.3")
			if _, ok := settings["enabledModels"]; !ok {
				t.Fatalf("the selection wrote no enabledModels, so the premise is wrong: %v", settings)
			}

			breakLastRender(t, r, how)
			log := withBootLog(r)
			r.render(t, ``)
			settings = r.piSettings(t)
			for _, k := range piSelectionKeys {
				if v, ok := settings[k]; ok {
					t.Errorf("adopting deselect: %s = %v survived; yolo wrote it and nobody edited it, "+
						"so the clear must hold on an adopting boot too", k, v)
				}
				if !strings.Contains(log.String(), "selection: cleared pi/settings "+k+" ") {
					t.Errorf("adopting deselect: boot.log does not record clearing %s:\n%s", k, log.String())
				}
			}

			r.render(t, ``)
			settings = r.piSettings(t)
			for _, k := range piSelectionKeys {
				if v, ok := settings[k]; ok {
					t.Errorf("second deselect: %s = %v is back", k, v)
				}
			}
		})
	}
}

// TestAnAdoptingDeselectKeepsWhatTheUserWrote: on the same adopting boot, a selection key the
// user changed survives (it is not yolo's value), and a key the user added that no layer
// asserts is still adopted, as every adopting boot adopts it. Only yolo's untouched write
// leaves.
func TestAnAdoptingDeselectKeepsWhatTheUserWrote(t *testing.T) {
	r := newPioencodeRender(t, zaiReachableJSON)
	r.wireProfiles(`{"zai": {"provider": "zai", "model": "glm-5.3"}}`)
	r.render(t, `{"pi":"zai"}`)
	rel := []string{".pi", "agent", "settings.json"}
	r.edit(t, rel, "defaultModel", "glm-5.3-flash")
	r.edit(t, rel, "userAddedKey", "mine")

	breakLastRender(t, r, "absent")
	log := withBootLog(r)
	r.render(t, ``)
	settings := r.piSettings(t)
	if got := settings["defaultModel"]; got != "glm-5.3-flash" {
		t.Errorf("defaultModel = %v, want the user's glm-5.3-flash kept", got)
	}
	if got := settings["userAddedKey"]; got != "mine" {
		t.Errorf("userAddedKey = %v, want the user's own key adopted", got)
	}
	if v, ok := settings["defaultProvider"]; ok {
		t.Errorf("defaultProvider = %v survived; yolo wrote it and nobody edited it", v)
	}
	if strings.Contains(log.String(), "pi/settings defaultModel") {
		t.Errorf("a kept user edit was recorded as cleared:\n%s", log.String())
	}
}

// TestAClearTheFileStillHoldsIsNotRecorded is OQ-PSW4's line held to the file at the CALL
// SITE: boot.log says `selection: cleared` only for a value that actually left the file. The
// case where it does not is a host layer supplying the same value yolo's selection wrote —
// the clear omits yolo's copy, the host's identical one falls through, and the file is
// unchanged. The key beside it that the host does not supply really does leave, and is
// recorded. Run on a steady-state boot and on an adopting one, where the second key leaves
// only because the render keeps the clear out of the adopted overlay.
func TestAClearTheFileStillHoldsIsNotRecorded(t *testing.T) {
	for _, adopting := range []bool{false, true} {
		name := "steady state"
		if adopting {
			name = "adopting boot"
		}
		t.Run(name, func(t *testing.T) {
			r := newHostLayerRender(t)
			writePiHostSettings(t, `{"defaultProvider":"zai"}`)
			r.render(t, `{"pi":"zai"}`)
			requirePiSelection(t, r.piSettings(t), r.piModels(t), "zai", "glm-5.3")
			if adopting {
				breakLastRender(t, r, "absent")
			}

			log := withBootLog(r)
			r.render(t, ``)
			settings := r.piSettings(t)
			if got := settings["defaultProvider"]; got != "zai" {
				t.Fatalf("defaultProvider = %v, want the host layer's zai to stand; the premise is wrong", got)
			}
			if strings.Contains(log.String(), "pi/settings defaultProvider") {
				t.Errorf("boot.log records clearing defaultProvider, but the file still holds %q:\n%s",
					settings["defaultProvider"], log.String())
			}
			if _, ok := settings["defaultModel"]; ok {
				t.Fatalf("defaultModel = %v survived the deselect", settings["defaultModel"])
			}
			if !strings.Contains(log.String(), `selection: cleared pi/settings defaultModel (was "glm-5.3")`) {
				t.Errorf("boot.log lacks the clear of defaultModel, which did leave the file:\n%s", log.String())
			}
		})
	}
}

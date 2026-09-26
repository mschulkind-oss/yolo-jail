package agentcfg

import (
	"encoding/json"
	"reflect"
	"testing"
)

// The deselect clear inside the stateful render (OQ-PSW2,
// docs/reference/providers.md#deselection-clear-what-yolo-wrote-keep-what-the-user-wrote). A
// clear works by OMISSION from the computed layer, so it holds only if no capture overlay
// carries the key either. On an ADOPTING boot the overlay is seeded from the file itself, and
// the file holds exactly the value being cleared, so ComposeStateful is handed the clears
// (StatefulInputs.SelectionCleared) and keeps them out of the overlay; it then reports which
// of them actually left the rendered file (StatefulOutput.SelectionCleared).

// composeClear renders piSurface over the given file, host layer and clears.
func composeClear(t *testing.T, in StatefulInputs) (map[string]any, map[string]any, *StatefulOutput) {
	t.Helper()
	in.Base.Surface = piSurface()
	out, err := ComposeStateful(in)
	if err != nil {
		t.Fatalf("ComposeStateful: %v", err)
	}
	file := map[string]any{}
	if err := json.Unmarshal(out.Result.Encoded, &file); err != nil {
		t.Fatalf("decode render: %v\n%s", err, out.Result.Encoded)
	}
	overlay := map[string]any{}
	if err := json.Unmarshal(out.OverlayJSON, &overlay); err != nil {
		t.Fatalf("decode overlay: %v\n%s", err, out.OverlayJSON)
	}
	return file, overlay, out
}

var zaiClears = []SelectionClear{
	{Key: "defaultModel", Value: "glm-5.3"},
	{Key: "defaultProvider", Value: "zai"},
}

// TestAnAdoptingRenderDoesNotAdoptAClearedKey: the file holds yolo's own selection write and
// an unrelated key the user added. Adoption keeps the user's key, and the cleared keys neither
// reach the overlay nor the file.
func TestAnAdoptingRenderDoesNotAdoptAClearedKey(t *testing.T) {
	file, overlay, out := composeClear(t, StatefulInputs{
		CurrentBytes:      []byte(`{"defaultProvider":"zai","defaultModel":"glm-5.3","userKey":"mine"}`),
		LastRenderPresent: false,
		SelectionCleared:  zaiClears,
	})
	if !out.FirstMigration {
		t.Fatal("the render did not adopt; the premise is wrong")
	}
	for _, c := range zaiClears {
		if v, ok := file[c.Key]; ok {
			t.Errorf("file %s = %v: a cleared key survived the adopting render", c.Key, v)
		}
		if v, ok := overlay[c.Key]; ok {
			t.Errorf("overlay %s = %v: adoption captured yolo's own write as the user's", c.Key, v)
		}
	}
	if file["userKey"] != "mine" || overlay["userKey"] != "mine" {
		t.Errorf("the user's own key was not adopted: file %v, overlay %v", file, overlay)
	}
	if !reflect.DeepEqual(out.SelectionCleared, zaiClears) {
		t.Errorf("reported clears = %v, want both, since both left the file", out.SelectionCleared)
	}
}

// TestAnAdoptingRenderWithNoClearsAdoptsTheSameKeys is the control: the same file and no
// clears is the ordinary adoption, which keeps every key yolo does not assert.
func TestAnAdoptingRenderWithNoClearsAdoptsTheSameKeys(t *testing.T) {
	file, _, out := composeClear(t, StatefulInputs{
		CurrentBytes:      []byte(`{"defaultProvider":"zai","defaultModel":"glm-5.3","userKey":"mine"}`),
		LastRenderPresent: false,
	})
	if file["defaultProvider"] != "zai" || file["defaultModel"] != "glm-5.3" || file["userKey"] != "mine" {
		t.Errorf("adoption without clears lost a key: %v", file)
	}
	if out.SelectionCleared != nil {
		t.Errorf("reported clears = %v with none handed in", out.SelectionCleared)
	}
}

// TestASteadyStateClearIgnoresAStaleOverlayEntry: the overlay can still carry an old captured
// value for a selection key (a capture between boots, the file since set back to yolo's
// value). A clear omits yolo's value so the key falls to the host layer or the agent's
// default, never to that stale capture.
func TestASteadyStateClearIgnoresAStaleOverlayEntry(t *testing.T) {
	current := `{"defaultProvider":"zai","theme":"system","defaultProjectTrust":"always"}`
	file, overlay, _ := composeClear(t, StatefulInputs{
		CurrentBytes:      []byte(current),
		LastRenderPresent: true,
		LastRenderBytes:   []byte(current),
		OverlayJSON:       []byte(`{"defaultProvider":"anthropic"}`),
		SelectionCleared:  []SelectionClear{{Key: "defaultProvider", Value: "zai"}},
	})
	if v, ok := file["defaultProvider"]; ok {
		t.Errorf("file defaultProvider = %v, want it cleared rather than handed to a stale capture", v)
	}
	if v, ok := overlay["defaultProvider"]; ok {
		t.Errorf("overlay still carries defaultProvider = %v", v)
	}
}

// TestAClearReportsOnlyWhatLeftTheFile: the host layer supplies defaultProvider with the same
// value yolo wrote, so omitting yolo's copy leaves the file unchanged there and that clear is
// not reported. defaultModel has no host value and leaves. A host value that DIFFERS replaces
// yolo's, which is yolo's value leaving, and is reported.
func TestAClearReportsOnlyWhatLeftTheFile(t *testing.T) {
	file, _, out := composeClear(t, StatefulInputs{
		Base:              Inputs{HostBytes: []byte(`{"defaultProvider":"zai"}`)},
		CurrentBytes:      []byte(`{"defaultProvider":"zai","defaultModel":"glm-5.3"}`),
		LastRenderPresent: false,
		SelectionCleared:  zaiClears,
	})
	if file["defaultProvider"] != "zai" {
		t.Fatalf("defaultProvider = %v, want the host layer's zai; the premise is wrong", file["defaultProvider"])
	}
	want := []SelectionClear{{Key: "defaultModel", Value: "glm-5.3"}}
	if !reflect.DeepEqual(out.SelectionCleared, want) {
		t.Errorf("reported clears = %v, want %v: the file still holds defaultProvider = zai", out.SelectionCleared, want)
	}

	file, _, out = composeClear(t, StatefulInputs{
		Base:              Inputs{HostBytes: []byte(`{"defaultProvider":"anthropic"}`)},
		CurrentBytes:      []byte(`{"defaultProvider":"zai"}`),
		LastRenderPresent: false,
		SelectionCleared:  []SelectionClear{{Key: "defaultProvider", Value: "zai"}},
	})
	if file["defaultProvider"] != "anthropic" {
		t.Errorf("defaultProvider = %v, want the host layer's anthropic back", file["defaultProvider"])
	}
	if len(out.SelectionCleared) != 1 {
		t.Errorf("reported clears = %v, want defaultProvider: yolo's zai left the file", out.SelectionCleared)
	}
}

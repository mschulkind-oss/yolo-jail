package callbrokerinput

import (
	"encoding/json"
	"testing"
)

func TestSnapshotLaunchRemovesFrozenAuthorityWhileLegacyLaunchKeepsItsBytes(t *testing.T) {
	legacy := Launch{Version: "yolo.call-broker-launch/2", Roots: []string{"team:alpha"}, ReadStanding: true, ReadGrantStanding: "granted"}
	type oldLaunch Launch
	want, err := json.Marshal(oldLaunch(legacy))
	if err != nil {
		t.Fatal(err)
	}
	got, err := json.Marshal(legacy)
	if err != nil || string(got) != string(want) {
		t.Fatal("/2 serialized bytes changed")
	}
	current := Launch{Version: SnapshotLaunchVersion}
	data, err := json.Marshal(current)
	if err != nil || ValidateAuthorityVersion(current, data) != nil {
		t.Fatalf("valid /3: %s %v", data, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"roots", "effective_cells", "read_standing", "read_grant_standing", "read_withheld"} {
		if _, exists := raw[key]; exists {
			t.Fatalf("/3 serializes frozen authority %s", key)
		}
		raw[key] = json.RawMessage(`null`)
		bad, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if ValidateAuthorityVersion(current, bad) == nil {
			t.Fatalf("/3 accepted frozen authority %s even as null", key)
		}
		delete(raw, key)
	}
	current.Roots = []string{"team:alpha"}
	if _, err := json.Marshal(current); err == nil {
		t.Fatal("/3 accepted populated frozen roots")
	}
}

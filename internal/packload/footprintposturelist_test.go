package packload_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A POSTURE LIST IS DISCLOSED ON THE AUTONOMY CLAIM, NAMING ITS POSTURE
// (docs/design/notch-scoped-config-contributions.md NS-D2). `yolo pack footprint` and
// `yolo pack lint` are where an author sees what a pack does before any launch, and a posture
// list appends into a surface another pack owns — squarely a statement of what the pack does
// to its environment — so it cannot be absent from the claim for the kind that declares it.
//
// The claim is UNCONDITIONAL (the footprint reports what a pack wants; which posture renders
// is a notch fact), and the posture is the gate a reader needs beside the entries, so it is
// in the detail: the `profile` modifier's precedent. The target spelling is the config-list
// claim's, `agent/name#pointer`, so a reader searching for who appends to an array finds it.
func TestFootprintNamesAPostureListUnderItsPosture(t *testing.T) {
	m, probs := packdecl.Decode([]byte(`{"name":"matt","contributes":[{"kind":"autonomy",` +
		`"autonomous":{"lists":[{"surface":"pi/settings","path":"/packages","add":[]}]},` +
		`"guarded":{"lists":[{"surface":"pi/settings","path":"/packages",` +
		`"add":["npm:@czottmann/pi-automode@1.17.0"]}]}}]}`))
	if len(probs) != 0 {
		t.Fatalf("decoding the fixture: %v", probs)
	}
	var autonomy []string
	for _, c := range packload.FootprintOf(&packload.Pack{Name: "matt", Decl: m}).Claims {
		switch c.Kind {
		case packdecl.KindAutonomy:
			autonomy = append(autonomy, c.Detail)
		case packdecl.KindConfigList:
			// The claim leads with the kind the author WROTE (NS-D2): a config-list claim here
			// would name a declaration this pack does not make.
			t.Errorf("a posture list was claimed as a config-list: %+v", c)
		}
	}
	if len(autonomy) != 1 {
		t.Fatalf("autonomy claims = %v, want one", autonomy)
	}
	want := `guarded appends 1 entry: "npm:@czottmann/pi-automode@1.17.0" to pi/settings#/packages`
	if !strings.Contains(autonomy[0], want) {
		t.Errorf("the autonomy claim does not name the guarded posture's list:\n got %q\nwant it to contain %q",
			autonomy[0], want)
	}
	// An empty add says so, rather than claiming an append that does not happen.
	if !strings.Contains(autonomy[0], "autonomous appends nothing (empty `add`, a no-op) to pi/settings#/packages") {
		t.Errorf("the empty autonomous list is not described as a no-op: %q", autonomy[0])
	}
}

package packload_test

import (
	"os"
	"path/filepath"
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

// A POSTURE OVERLAY IS DISCLOSED ON THE AUTONOMY CLAIM TOO (NS-D23): a posture's config patch
// on a surface its pack does not declare contributes keys to another pack's config file, which
// is the config-overlay claim's statement, so it is named with its posture and config-overlay's
// precedence clause. A patch on the pack's OWN surface is not named: it tightens the pack's own
// file, which the pack's config claim already covers.
func TestFootprintNamesAPostureOverlayUnderItsPosture(t *testing.T) {
	m, probs := packdecl.Decode([]byte(`{"name":"matt","contributes":[
	  {"kind":"config","config":[{"agent":"matt","name":"notes","codec":"json",
	     "path":"~/.matt/notes.json","managed":{"k":1}}]},
	  {"kind":"autonomy",
	   "autonomous":{"config":[{"agent":"matt","name":"notes","codec":"json",
	     "path":"~/.matt/notes.json","managed":{"own":true}}]},
	   "guarded":{"config":[{"agent":"pi","name":"settings","codec":"json",
	     "path":"~/.pi/agent/settings.json","managed":{"hostOnly":true}}]}}]}`))
	if len(probs) != 0 {
		t.Fatalf("decoding the fixture: %v", probs)
	}
	var autonomy []string
	for _, c := range packload.FootprintOf(&packload.Pack{Name: "matt", Decl: m}).Claims {
		switch c.Kind {
		case packdecl.KindAutonomy:
			autonomy = append(autonomy, c.Detail)
		case packdecl.KindConfigOverlay:
			t.Errorf("a posture overlay was claimed as a config-overlay: %+v", c)
		}
	}
	if len(autonomy) != 1 {
		t.Fatalf("autonomy claims = %v, want one", autonomy)
	}
	if want := "guarded contributes keys to pi/settings (owner still wins)"; !strings.Contains(autonomy[0], want) {
		t.Errorf("the autonomy claim does not name the guarded posture's overlay:\n got %q\nwant it to contain %q",
			autonomy[0], want)
	}
	if strings.Contains(autonomy[0], "matt/notes") {
		t.Errorf("the own-surface patch is named as a contribution to another pack's file: %q", autonomy[0])
	}
}

// A SECOND AUTONOMY CONTRIBUTION, THROUGH THE LOADER (NS-D11). The footprint walks every
// contribution while the posture readers take the first, so the two used to disagree: two
// claims, one of them describing lists no notch rendered. On the host (strict) the pack is
// refused; in a jail (tolerant) the second is dropped with a skew note, and the footprint then
// makes exactly the one claim the render acts on.
func TestASecondAutonomyContributionNeverReachesTheFootprint(t *testing.T) {
	dir := t.TempDir()
	manifest := `{"name":"matt","contributes":[` +
		`{"kind":"autonomy","guarded":{"lists":[{"surface":"pi/settings","path":"/packages","add":["host-only"]}]}},` +
		`{"kind":"autonomy","autonomous":{"lists":[{"surface":"pi/settings","path":"/packages","add":["jail-only"]}]}}]}`
	if err := os.WriteFile(filepath.Join(dir, packdecl.ManifestName), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, problems := packload.LoadDir(dir, "matt"); len(problems) == 0 ||
		!strings.Contains(strings.Join(problems, "\n"), `second "autonomy"`) {
		t.Errorf("the host load accepted a second autonomy contribution: %v", problems)
	}

	restore := packload.OverrideSkewTolerance(true)
	defer restore()
	p, problems := packload.LoadDir(dir, "matt")
	if len(problems) != 0 {
		t.Fatalf("the jail load must not fail on it: %v", problems)
	}
	if len(p.SkewNotes) != 1 || !strings.Contains(p.SkewNotes[0], "pack matt: contributes[1]") {
		t.Errorf("SkewNotes = %v, want the one note naming the dropped contribution", p.SkewNotes)
	}
	var autonomy []string
	for _, c := range packload.FootprintOf(p).Claims {
		if c.Kind == packdecl.KindAutonomy {
			autonomy = append(autonomy, c.Detail)
		}
	}
	if len(autonomy) != 1 || strings.Contains(autonomy[0], "jail-only") {
		t.Errorf("autonomy claims = %q, want one, not naming the dropped contribution's list", autonomy)
	}
}

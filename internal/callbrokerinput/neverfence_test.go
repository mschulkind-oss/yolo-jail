package callbrokerinput

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
)

// A broker started by a yolo that predates `never` reads only always/on_request, so a source never
// is invisible to it. Its private input is the only record of what started it: a launch that
// carries NeverAware was made by a build whose broker honors never.
func TestBrokersIgnoringNeverNamesOnlyThoseThatPredateIt(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	files := []brokerscope.LiveFile{
		{File: brokerscope.File{LaunchID: "old", CallInput: write("old.json", `{"version":"yolo.call-broker-launch/2","source":"aws"}`)}},
		{File: brokerscope.File{LaunchID: "new", CallInput: write("new.json", `{"version":"yolo.call-broker-launch/2","source":"aws","never_aware":true}`)}},
		{File: brokerscope.File{LaunchID: "gone", CallInput: filepath.Join(dir, "missing.json")}},
	}
	got := BrokersIgnoringNever(files)
	if len(got) != 2 || got[0] != "old" || got[1] != "gone" {
		t.Fatalf("got %v, want the broker with no marker and the one whose input cannot be read (unknown is not aware)", got)
	}
}

func TestLaunchCarriesTheNeverAwareMarkerInItsBytes(t *testing.T) {
	data, err := Launch{Version: "yolo.call-broker-launch/2", NeverAware: true}.MarshalJSON()
	if err != nil || !contains(string(data), `"never_aware":true`) {
		t.Fatalf("%s %v", data, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

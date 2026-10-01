package updatehint

import (
	"strings"
	"testing"
)

// TestNewerSchemaNamesTheUpdateForWhereItRuns: on the host the step is `yolo update`; in a jail,
// whose yolo is the host's copy, it is `yolo update` on the host. Every reader of a file a newer
// yolo wrote prints this sentence.
func TestNewerSchemaNamesTheUpdateForWhereItRuns(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	host := NewerSchema("/x/packs.lock.json", 9, 2).Error()
	if want := "/x/packs.lock.json: schema 9 is newer than this yolo understands (2), so a newer " +
		"yolo wrote it; run `yolo update` rather than letting this one misread the file"; host != want {
		t.Errorf("host refusal =\n  %s\nwant\n  %s", host, want)
	}
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	if jail := NewerSchema("/x/packs.lock.json", 9, 2).Error(); !strings.Contains(jail,
		"; run `yolo update` on the host rather than") {
		t.Errorf("in a jail the refusal does not send the reader to the host: %s", jail)
	}
}

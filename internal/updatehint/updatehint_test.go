package updatehint

import (
	"strings"
	"testing"
)

// TestNewerSchemaNamesTheUpdateForWhereItRuns: on the host the step is `yolo update`; in a jail,
// whose yolo is the host's copy, it is `yolo update` on the host, then a relaunch. Every reader of
// a file a newer yolo wrote prints this sentence.
func TestNewerSchemaNamesTheUpdateForWhereItRuns(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	host := NewerSchema("/x/packs.lock.json", 9, 2).Error()
	if want := "/x/packs.lock.json: schema 9 is newer than this yolo understands (2), so a newer " +
		"yolo wrote it; run `yolo update` rather than letting this one misread the file"; host != want {
		t.Errorf("host refusal =\n  %s\nwant\n  %s", host, want)
	}
	// In a jail the step is the host's update and then a relaunch: the jail's yolo is fixed for
	// the jail's life, so an update on the host alone leaves this jail refusing the same file.
	t.Setenv("YOLO_VERSION", "9.9.9-test")
	if jail := NewerSchema("/x/packs.lock.json", 9, 2).Error(); !strings.Contains(jail,
		"; run `yolo update` on the host and relaunch this jail rather than") {
		t.Errorf("in a jail the refusal does not send the reader to the host and back: %s", jail)
	}
}

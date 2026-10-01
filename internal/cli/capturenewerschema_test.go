package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
)

// TestACaptureFromANewerYoloNamesYoloUpdate: a capture whose manifest a newer yolo wrote is
// refused rather than misread, and the refusal used to end at "upgrade yolo", leaving the reader
// to work out how this copy was installed. It now names `yolo update`, as the pack lockfile's
// refusal does (docs/reference/happy-path-principle.md, rule 7). In a jail, where this yolo is
// the host's copy and its own `yolo update` only says to run it on the host, the refusal says
// that directly, then to relaunch the jail, whose yolo the update on the host does not replace.
//
// Driven through materializeCapture, the in-jail act every generated launcher runs before its
// download, so it fails if the message stops reaching the person reading the launch.
func TestACaptureFromANewerYoloNamesYoloUpdate(t *testing.T) {
	for _, tc := range []struct {
		name, yoloVersion, want string
	}{
		{"host", "", "run `yolo update` rather than"},
		{"jail", "9.9.9-test", "run `yolo update` on the host and relaunch this jail rather than"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("YOLO_VERSION", tc.yoloVersion)
			home := t.TempDir()
			store := &capture.Store{Dir: t.TempDir()}
			entry := admitCaptureEntry(t, store, "newtool", "newtool", capture.Platform(),
				home, "#!/bin/sh\necho new\n", time.Now())
			// The tree's digest names the entry, and the manifest beside it is not part of it,
			// so the entry stays admitted with a manifest only a newer yolo writes.
			raw, err := os.ReadFile(capture.ManifestPath(entry.Root))
			if err != nil {
				t.Fatal(err)
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				t.Fatal(err)
			}
			m["schema"] = capture.ManifestSchema + 1
			raw, _ = json.Marshal(m)
			if err := os.WriteFile(capture.ManifestPath(entry.Root), raw, 0o644); err != nil {
				t.Fatal(err)
			}

			var errw bytes.Buffer
			rc := materializeCapture(materializeArgs{store: store.Dir, bin: "newtool", home: home}, &errw)
			if rc == 0 {
				t.Fatalf("a capture from a newer yolo was materialized:\n%s", errw.String())
			}
			got := errw.String()
			if !strings.Contains(got, tc.want) {
				t.Errorf("the refusal does not say %q:\n%s", tc.want, got)
			}
			if strings.Contains(got, "upgrade yolo") {
				t.Errorf("the refusal still says only \"upgrade yolo\":\n%s", got)
			}
		})
	}
	if _, ok := registry["update"]; !ok {
		t.Error("the refusal names `yolo update`, which is not a command")
	}
}

package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestPackStatusKeyFindsASlashedPackUnresolved: a fork key is `<pack>/<bin>`, and a readable-config
// pack's name is a source handle holding a "/" itself (OQ-RC15), so `yolo-packs/pi-fork/pi` names the
// pack yolo-packs/pi-fork. Split at the FIRST "/" it named a pack `yolo-packs` that nothing configures,
// and a configured fork that did not resolve here fell through to "nothing selected builds it".
func TestPackStatusKeyFindsASlashedPackUnresolved(t *testing.T) {
	sel := hostPackSet{unresolved: []unresolvedPack{{Name: "yolo-packs/pi-fork", Reason: "not fetched"}}}
	var errw bytes.Buffer
	rc, said := packStatusKeyUnresolved(&errw, sel, "yolo-packs/pi-fork/pi")
	if !said || rc != 1 || !strings.Contains(errw.String(), "the pack yolo-packs/pi-fork is configured") {
		t.Errorf("packStatusKeyUnresolved = %d, %v, %q; want the unresolved pack yolo-packs/pi-fork named",
			rc, said, errw.String())
	}
	errw.Reset()
	rc, said = packStatusKeyUnresolved(&errw, sel, "yolo-packs/pi-fork/pi#upstream")
	if !said || rc != 1 {
		t.Errorf("an upstream build's key: packStatusKeyUnresolved = %d, %v, %q", rc, said, errw.String())
	}
}

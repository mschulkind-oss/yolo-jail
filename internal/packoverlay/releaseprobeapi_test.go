package packoverlay

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// TestReleaseDecodeProbeAPIIsStable is internal/packload's pin of the same name, for the one call
// packs/releasedecode_test.go's probe makes into this package when it is compiled inside the last
// release's tree: Collect over the whole pack set, and the Problems it reports, which is how every
// release's in-jail boot has collected config-overlays. Change either and the check breaks at the
// next tag; the probe must learn the new spelling first.
func TestReleaseDecodeProbeAPIIsStable(t *testing.T) {
	var (
		collect  func([]*packload.Pack, bool, map[string][]string) *OverlaySet = Collect
		problems []string                                                      = (&OverlaySet{}).Problems
	)
	if collect == nil || problems != nil {
		t.Fatal("unreachable: a nil function value, or a zero set with problems")
	}
}

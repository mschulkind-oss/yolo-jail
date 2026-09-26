package packload

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// TestReleaseDecodeProbeAPIIsStable pins what packs/releasedecode_test.go's probe reads from this
// package when it is compiled inside the LAST RELEASE's tree: TolerateSkew, then LoadDir per pack
// and the Pack.SkewNotes it fills, then each pack's SurfacesForReport, which is how every
// release's in-jail boot has read a staged pack and its surfaces. This tree is the next release,
// so these are a contract with every future run of that test — change one and the check breaks
// at the next tag, in a tree nobody can edit any more. If a change really must move one, the
// probe must learn to read both spellings first. internal/packoverlay pins the overlay half.
func TestReleaseDecodeProbeAPIIsStable(t *testing.T) {
	var (
		tolerate func()                                                       = TolerateSkew
		load     func(root, name string) (*Pack, []string)                    = LoadDir
		surfaces func(*Pack, bool) ([]manifest.Surface, []string, []FoldNote) = (*Pack).SurfacesForReport
		notes    []string                                                     = (&Pack{}).SkewNotes
	)
	if tolerate == nil || load == nil || surfaces == nil || notes != nil {
		t.Fatal("unreachable: a nil function value, or a zero Pack with skew notes")
	}
}

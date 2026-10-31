package packload

// barelanding_test.go pins PN-D7's "bare means unchanged" at the two names a bare pack lands under
// (pack-naming review finding M3): with no Landing (packsrc gives a bare pack none), a pack named
// Matt_Tools lands its files under its own name and its /ctx grants under its staged directory,
// exactly as before pack naming, never under a normalized `matt-tools`.

import (
	"path/filepath"
	"testing"
)

func TestABarePackLandsExactlyAsBefore(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Matt_Tools_2fMatt_Tools")
	p := &Pack{Name: "Matt_Tools/Matt_Tools", Root: root}
	if got := p.LandingName(); got != "Matt_Tools/Matt_Tools" {
		t.Errorf("LandingName = %q, want the pack's own name", got)
	}
	if got := p.CtxName(); got != "Matt_Tools_2fMatt_Tools" {
		t.Errorf("CtxName = %q, want the staged directory", got)
	}
	p.LaunchName = "Matt_Tools/Matt_Tools"
	p.Name = "Matt_Tools_2fMatt_Tools"
	if got := p.LandingName(); got != "Matt_Tools/Matt_Tools" {
		t.Errorf("a recorded tree's LandingName = %q, want the launch's name", got)
	}
}

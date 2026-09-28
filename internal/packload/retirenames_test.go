package packload

import (
	"strings"
	"testing"
)

// EVERY NAME A SHIPPED SURFACE RETIRES UNREAD IS ONE ONLY YOLO WRITES, which today means a
// `yolo-` prefix, and a vendor name may be retired ONLY in the match-guarded form.
//
// retireOnFirstRender deletes the file with no check of what is in it, and a computed or rmw
// surface runs it after every write, so a name that the agent, one of its extensions or the
// user can also write is a file yolo deletes at every boot. That shipped: the pi pack retired
// `mcp.json` after moving its own render to `mcp-adapter.json`, and deleted the copy
// pi-subagents reads (docs/design/agent-directory-map.md, Appendix B).
//
// retireIfMatchesRender is where such a name goes instead: it deletes a file only while it
// holds exactly the surface's own render, so nobody else's content can be in it. Its names
// carry no prefix rule, and the test requires the shipped pi use to stay in that form (AM-R1).
func TestShippedRetireNamesAreYolosOwn(t *testing.T) {
	var checked, guarded int
	piLegacyGuarded := false
	for _, p := range Embedded() {
		surfaces, problems := p.Surfaces()
		if len(problems) != 0 {
			t.Fatalf("pack %s: %v", p.Name, problems)
		}
		for _, s := range surfaces {
			for _, name := range s.RetireOnFirstRender {
				checked++
				if !strings.HasPrefix(name, "yolo-") {
					t.Errorf("surface %s retires %q unread, a name yolo does not own: the "+
						"boot deletes whatever sits there, including a file the agent, an "+
						"extension or the user wrote. Retire only a name yolo alone writes, "+
						"or move it to retireIfMatchesRender, which reads the file first",
						s.Key(), name)
				}
			}
			for _, name := range s.RetireIfMatchesRender {
				guarded++
				if s.Key().String() == "pi/mcp" && name == "mcp.json" {
					piLegacyGuarded = true
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no shipped surface declares retireOnFirstRender, so this test checks nothing")
	}
	if guarded == 0 || !piLegacyGuarded {
		t.Fatal("pi/mcp no longer retires mcp.json through retireIfMatchesRender: the copy " +
			"0.10.0 wrote there stays forever (AM-R1). Keep it, in the match-guarded form")
	}
}

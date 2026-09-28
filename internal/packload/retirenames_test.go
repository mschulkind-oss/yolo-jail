package packload

import (
	"strings"
	"testing"
)

// EVERY NAME A SHIPPED SURFACE RETIRES IS ONE ONLY YOLO WRITES, which today means a `yolo-`
// prefix. retireOnFirstRender deletes the file with no check of what is in it, and a
// computed or rmw surface runs it after every write, so a name that the agent, one of its
// extensions or the user can also write is a file yolo deletes at every boot. That shipped:
// the pi pack retired `mcp.json` after moving its own render to `mcp-adapter.json`, and
// deleted the copy pi-subagents reads (docs/design/agent-directory-map.md, Appendix B).
func TestShippedRetireNamesAreYolosOwn(t *testing.T) {
	var checked int
	for _, p := range Embedded() {
		surfaces, problems := p.Surfaces()
		if len(problems) != 0 {
			t.Fatalf("pack %s: %v", p.Name, problems)
		}
		for _, s := range surfaces {
			for _, name := range s.RetireOnFirstRender {
				checked++
				if !strings.HasPrefix(name, "yolo-") {
					t.Errorf("surface %s retires %q, a name yolo does not own: the boot "+
						"deletes whatever sits there, including a file the agent, an "+
						"extension or the user wrote. Retire only a name yolo alone writes",
						s.Key(), name)
				}
			}
		}
	}
	if checked == 0 {
		t.Fatal("no shipped surface declares retireOnFirstRender, so this test checks nothing")
	}
}

package packload

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// A `whenListed` condition reads its list from a surface's file as THIS render leaves it, so
// the named surface must be one the same pack declares EARLIER: the render walks a pack's
// surfaces in declaration order. A later one, or another pack's, is a problem that names the
// condition, never a condition read one boot stale.
func TestWhenListedMustNameAnEarlierSurfaceOfTheSamePack(t *testing.T) {
	const gated = `{"agent":"pi","name":"gated","codec":"json","path":"~/.g.json",
		"whenListed":{"surface":"pi/settings","path":"/packages","matches":"x"}}`
	const settings = `{"agent":"pi","name":"settings","codec":"json","path":"~/.s.json"}`
	for name, tc := range map[string]struct {
		raw  string
		want bool
	}{
		"earlier":      {"[" + settings + "," + gated + "]", true},
		"later":        {"[" + gated + "," + settings + "]", false},
		"not declared": {"[" + gated + "]", false},
	} {
		t.Run(name, func(t *testing.T) {
			p := &Pack{Name: "pi", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
				{Kind: packdecl.KindConfig, Raw: []byte(tc.raw)},
			}}}
			_, problems := p.Surfaces()
			if ok := len(problems) == 0; ok != tc.want {
				t.Fatalf("problems = %v, want accepted = %v", problems, tc.want)
			}
			if !tc.want && !strings.Contains(strings.Join(problems, "\n"), "whenListed") {
				t.Errorf("the problem must name the condition: %v", problems)
			}
		})
	}
}

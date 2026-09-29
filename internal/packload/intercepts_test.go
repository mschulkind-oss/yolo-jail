package packload

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

func TestInterceptsCollectsAndFootprints(t *testing.T) {
	a := writePack(t, "a", "a", `{"name":"a","contributes":[
		{"kind":"intercept","bin":"gh","forward":["yolo","gh","--"]}]}`)
	got := Intercepts([]*Pack{a})
	if want := []Intercept{{Name: "gh", Forward: []string{"yolo", "gh", "--"}, Pack: "a"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("intercepts %+v", got)
	}
	var claim *Claim
	for _, c := range FootprintOf(a).Claims {
		if c.Kind == packdecl.KindIntercept {
			c := c
			claim = &c
		}
	}
	if claim == nil || claim.Target != "gh" || claim.Detail != "forwards to yolo gh --" || claim.ReviewWorthy {
		t.Fatalf("claim %+v", claim)
	}
}

// One name, one forwarder: two packs intercepting one command collide.
func TestTwoPacksInterceptingOneNameCollide(t *testing.T) {
	a := writePack(t, "a", "a", `{"name":"a","contributes":[{"kind":"intercept","bin":"gh","forward":["x"]}]}`)
	b := writePack(t, "b", "b", `{"name":"b","contributes":[{"kind":"intercept","bin":"gh","forward":["y"]}]}`)
	for _, c := range Collisions([]*Pack{a, b}) {
		if c.Kind == packdecl.KindIntercept && c.Target == "gh" {
			return
		}
	}
	t.Fatal("no intercept collision reported")
}

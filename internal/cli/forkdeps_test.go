package cli

// forkdeps_test.go: a fork pack is never a declarer of its base's bin in the host dependency
// reports (docs/design/forked-programs-as-packs.md FP-D5). The base's program — rewritten with the
// fork's delivery — is the one declaration, so a miss line names the base alone.

import (
	"slices"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

func forkDepPacks(t *testing.T) []*packload.Pack {
	t.Helper()
	base := &packload.Pack{Name: "basepack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "tool", Via: "npm", Package: "tool"}}}}
	fork := &packload.Pack{Name: "forkpack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "tool", Via: packdecl.ViaSource, ForkOf: "basepack",
		Source: "git+https://example.invalid/tool-fork?ref=main", Build: "make install",
		Produces: []string{".local/bin/tool"}}}}}
	packs, err := packload.ApplyForks([]*packload.Pack{base, fork})
	if err != nil {
		t.Fatal(err)
	}
	return packs
}

func TestAForkPackDeclaresNoHostDependency(t *testing.T) {
	packs := forkDepPacks(t)
	d := declarersOf(packs)["tool"]
	if d == nil || !slices.Equal(d.programs, []string{"basepack"}) {
		t.Errorf("tool's declarers = %+v, want the base alone", d)
	}
	fields := render.HostFields()
	for _, c := range packs[1].Decl.Contributions() {
		if isProbedDep(fields, c) {
			t.Errorf("the fork's own contribution is probed as a host dependency: %+v", c)
		}
	}
}

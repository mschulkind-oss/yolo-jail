package check

// forkdeps_test.go: `yolo check`'s launch-PATH section never names a fork pack as a declarer of its
// base's bin (docs/design/forked-programs-as-packs.md FP-D5) — the base's program is the one
// declaration of it.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestTheLaunchPathSectionSkipsAForksOwnContribution(t *testing.T) {
	fork := &packload.Pack{Name: "forkpack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{{
		Kind: packdecl.KindProgram, Bin: "tool", Via: packdecl.ViaSource, ForkOf: "basepack",
		Source: "git+https://example.invalid/tool-fork?ref=main", Build: "make install",
		Produces: []string{".local/bin/tool"}}}}}
	byBin := map[string]*launchPathDep{}
	addPackDeps(byBin, fork, nil)
	if d := byBin["tool"]; d != nil {
		t.Errorf("the fork pack was recorded as a declarer of tool: %+v", d.miss)
	}
}

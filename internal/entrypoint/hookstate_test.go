package entrypoint

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hookstate_test.go pins the boot half of the shared-dir hook check `yolo pack lint` and
// `yolo check` now make too (docs/design/pack-conventions.md PC-D15): the boot refuses exactly
// the hooks packdecl.HookLinksIntoMachineState names, with packdecl's own sentence. If the two
// sets drift, the host passes a pack whose boot fails again, or refuses one that boots.
//
// The pack is decoded as the jail decodes it (DecodeTolerant, which runs no manifest-level
// check), because the host reads now refuse this manifest before a boot could see it.
func TestTheBootRefusesExactlyTheHooksTheHostRefuses(t *testing.T) {
	for _, hook := range packdecl.KnownHooks {
		t.Run(hook, func(t *testing.T) {
			decl, problems, _ := packdecl.DecodeTolerant([]byte(`{"name":"hookfix","contributes":[
				{"kind":"hook","hook":"` + hook + `","from":".x/thing","at":".x-shared"}]}`))
			if len(problems) != 0 {
				t.Fatalf("the jail's decode refused the fixture: %v", problems)
			}
			e, _, _ := sharedDirEnv(t, packdecl.Hook{File: ".x/thing", SharedDir: ".x-shared"})
			RunPackHooks(e, []*packload.Pack{{Name: "hookfix", Decl: decl}})
			fails := strings.Join(e.GenFailures(), "\n")
			want := packdecl.UndeclaredHookStateProblem(".x-shared")
			refused := strings.Contains(fails, want)
			if refused != packdecl.HookLinksIntoMachineState(hook) {
				t.Fatalf("boot refusal with the shared sentence = %v, host predicate = %v:\n%s",
					refused, packdecl.HookLinksIntoMachineState(hook), fails)
			}
			if refused && !strings.Contains(fails, "pack hookfix: hook "+hook) {
				t.Errorf("the boot's refusal does not name the pack and hook:\n%s", fails)
			}
		})
	}
}

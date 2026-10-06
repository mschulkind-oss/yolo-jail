package check

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// `yolo check` refuses a selected pack's shared-dir hook whose `at` names no machine `state` of
// the pack, with the boot's own sentence (docs/design/pack-conventions.md PC-D15). It used to
// pass the pack, and every boot of it then failed at the hook step.
func TestSectionPacksRefusesASharedDirHookWithNoMachineState(t *testing.T) {
	for _, c := range []struct {
		name, state string
		refused     bool
	}{
		{"without the state", ``, true},
		{"beside the state", `{"kind":"state","at":".x-shared","scope":"machine","because":"one login"},`, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			// A host read, so the host's decoder: an earlier test in this binary that reached
			// the jail's pack load (packload.TolerateSkew) leaves the process tolerant, and the
			// tolerant decoder leaves this check to the boot.
			t.Cleanup(packload.OverrideSkewTolerance(false))
			pack := t.TempDir()
			manifest := `{"name":"hookfix","contributes":[` + c.state +
				`{"kind":"hook","hook":"shared_credentials","from":".x/creds.json","at":".x-shared"}]}`
			if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
			packsFixture(t, `{"packs": ["file://`+pack+`"]}`)

			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r, jsonx.NewOrderedMap())

			want := packdecl.UndeclaredHookStateProblem(".x-shared")
			if got := r.failed != 0 && strings.Contains(buf.String(), want); got != c.refused {
				t.Errorf("refused with the boot's sentence = %v, want %v:\n%s", got, c.refused, buf.String())
			}
		})
	}
}

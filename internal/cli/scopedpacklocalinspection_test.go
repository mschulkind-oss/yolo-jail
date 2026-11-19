package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackExplainScopedTrustedLocalPlanUncertaintyDoesNotChangeEligibility(t *testing.T) {
	for _, fixture := range []string{"selected-symlink", "unselected-large-unsafe"} {
		t.Run(fixture, func(t *testing.T) {
			project := testconfig.Workspace(t, "")
			user := testconfig.User(t, "packs=[{pack='local/corpus',skills={dir='raw',only=['chosen']}}]\n")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_PACK_ROOT", "")
			root := filepath.Join(filepath.Dir(user.Path), "packs/corpus")
			chosen := filepath.Join(root, "raw/chosen")
			outside := filepath.Join(root, "trusted-chosen")
			if fixture == "selected-symlink" {
				scopedCLIWrite(t, filepath.Join(outside, "SKILL.md"), "chosen\n")
				if err := os.MkdirAll(filepath.Dir(chosen), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, chosen); err != nil {
					t.Fatal(err)
				}
			} else {
				scopedCLIWrite(t, filepath.Join(chosen, "SKILL.md"), "chosen\n")
				large := filepath.Join(root, "unselected-large")
				f, err := os.Create(large)
				if err != nil {
					t.Fatal(err)
				}
				if err := f.Truncate(257 << 20); err != nil {
					f.Close()
					t.Fatal(err)
				}
				f.Close()
				if err := os.Symlink("/never-read", filepath.Join(root, "unselected-unsafe")); err != nil {
					t.Fatal(err)
				}
			}
			// Compare only the selected bytes and named state roots. The large
			// unselected file is sparse and never read by the inspector or test.
			before := scopedCLISnapshot(t, chosen, outside, user.Path, paths.PacksDir())
			rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/corpus"})
			if rc != 0 || errw != "" || !strings.Contains(out, "selected: [chosen]") {
				t.Fatalf("optional plan altered eligibility: %d %s %s", rc, out, errw)
			}
			for _, want := range []string{"inspected: local directory", "license: determined at publication", "supporting-data omissions: determined at publication"} {
				if !strings.Contains(out, want) {
					t.Fatalf("uncertainty not honest %q: %s", want, out)
				}
			}
			if after := scopedCLISnapshot(t, chosen, outside, user.Path, paths.PacksDir()); !reflect.DeepEqual(before, after) {
				t.Fatal("local inspection wrote source/config/store/carrier")
			}
			if fixture == "selected-symlink" {
				if target, err := os.Readlink(chosen); err != nil || target != outside {
					t.Fatalf("local carrier symlink was changed: %q %v", target, err)
				}
			} else if info, err := os.Stat(filepath.Join(root, "unselected-large")); err != nil || info.Size() != 257<<20 {
				t.Fatalf("unselected bytes changed: %v %v", info, err)
			}
		})
	}
}

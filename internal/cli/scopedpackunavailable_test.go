package cli

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestPackExplainUnavailableLocalSourceIsUnverifiedWithoutInventedCounts(t *testing.T) {
	for _, authored := range []string{"'local/missing'", "{pack='local/missing',skills={only=['chosen']}}"} {
		t.Run(authored, func(t *testing.T) {
			project := testconfig.Workspace(t, "")
			user := testconfig.User(t, "packs=["+authored+"]\n")
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			t.Setenv("YOLO_PACK_ROOT", "")
			before := scopedCLISnapshot(t, user.Root)
			rc, out, errw := runPackCapture(t, project.Root, []string{"explain", "local/missing"})
			if rc != 0 || errw != "" || !strings.Contains(out, "selection not yet verified; resolved at next launch") {
				t.Fatalf("unavailable local inspection: %d %s %s", rc, out, errw)
			}
			for _, forbidden := range []string{"candidates:", "selected:", "license:", "omitted:", "file(s)", "conventional-skill-folders="} {
				if strings.Contains(out, forbidden) {
					t.Fatalf("unverified local inspection fabricated %q: %s", forbidden, out)
				}
			}
			after := scopedCLISnapshot(t, user.Root)
			if len(before) != len(after) {
				t.Fatal("unavailable inspection created state")
			}
			for path, value := range before {
				if after[path] != value {
					t.Fatalf("unavailable inspection changed %s", path)
				}
			}
		})
	}
}

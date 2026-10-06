package cli

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
)

// TestTheFrontDoorInstallsTheModelListFetch: the run pipeline fetches a provider's model list only
// through Options.FetchModelList, which a hand-built Options leaves nil so no test reaches AWS
// (docs/design/model-lists-and-pickers.md OQ-MM6). So the front door must install the production
// fetch, or a real launch would never fetch one. It fails with the front door's assignment removed.
func TestTheFrontDoorInstallsTheModelListFetch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	var seen run.Options
	prev := launchRunPipeline
	launchRunPipeline = func(o run.Options) int { seen = o; return 0 }
	t.Cleanup(func() { launchRunPipeline = prev })
	captureBoth(t, func() {
		if rc := Main([]string{"yolo", "--", "true"}); rc != 0 {
			t.Errorf("Main rc = %d with the pipeline stubbed", rc)
		}
	})
	if seen.FetchModelList == nil ||
		reflect.ValueOf(seen.FetchModelList).Pointer() != reflect.ValueOf(run.DefaultFetchModelList).Pointer() {
		t.Error("the front door did not install run.DefaultFetchModelList")
	}
}

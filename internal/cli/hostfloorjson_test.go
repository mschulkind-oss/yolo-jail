package cli

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// TestHostApplyJSONCarriesTheFloorStage: `yolo host apply --format json` says what the floor
// stage would do, as the text report does — the program to install and, after deselection, the
// entry to remove, a loss — rather than being the one stage the document leaves out. Driven
// through the verb, so it also pins the floor stage the JSON branch runs (floorStage). The user
// config declares `host_management: "own"`, without which the verb refuses (OQ-CO14's unset
// `none`) before the floor stage, and `host_wrappers: false`, which `own` would derive on.
func TestHostApplyJSONCarriesTheFloorStage(t *testing.T) {
	floorHostFixture(t, `,"host_management":"own","host_wrappers":false`)
	doc, raw, _ := hostApplyJSON(t, "--format", "json")
	if len(doc.HostFloor) != 1 || doc.HostFloor[0].Bin != "floorcli" || doc.HostFloor[0].Action != "would install" ||
		doc.HostFloor[0].Pack != "floorpack" || doc.HostFloor[0].Disposition != string(hostfloor.Missing) {
		t.Fatalf("host_floor = %+v, want floorcli to install:\n%s", doc.HostFloor, raw)
	}
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("--assert rc=%d:\n%s", rc, report)
	}
	doc, raw, _ = hostApplyJSON(t, "--format", "json")
	if len(doc.HostFloor) != 1 || doc.HostFloor[0].Action != "none" || doc.HostFloor[0].Version != "1.0.0" ||
		doc.HostFloor[0].Disposition != string(hostfloor.Provisioned) {
		t.Fatalf("host_floor once installed = %+v, want floorcli 1.0.0 provisioned:\n%s", doc.HostFloor, raw)
	}
	writeFile(t, paths.UserConfigPath(), `{"packs":[],"host_management":"own","host_wrappers":false}`)
	doc, raw, _ = hostApplyJSON(t, "--format", "json")
	if len(doc.HostFloor) != 1 || doc.HostFloor[0].Action != "would remove" || doc.HostFloor[0].Disposition != "deselected" {
		t.Fatalf("host_floor after deselection = %+v, want floorcli to remove:\n%s", doc.HostFloor, raw)
	}
}

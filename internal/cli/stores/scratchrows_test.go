package stores

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// The scratch volumes are a store of their own: the live jails' and the gone jails',
// counted and sized from each volume's mountpoint, and "unknown" — never zero — when
// podman cannot be asked.
func TestScratchVolumeRows(t *testing.T) {
	live := prune.ScratchVolumeName("yolo-live-11111111", "1111111111111111", "tmp")
	gone := prune.ScratchVolumeName("yolo-gone-22222222", "2222222222222222", "var-lib-containers")
	listing := `[{"Name":"` + live + `","Mountpoint":"/v/volumes/` + live + `/_data","CreatedAt":"2026-03-11T00:00:00Z"},` +
		`{"Name":"userdata","Mountpoint":"/v/volumes/userdata/_data","CreatedAt":"2026-03-11T00:00:00Z"},` +
		`{"Name":"` + gone + `","Mountpoint":"/v/volumes/` + gone + `/_data","CreatedAt":"2026-03-11T00:00:00Z"}]`

	o, _ := testOptions(t)
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		switch k := strings.Join(argv, " "); {
		case strings.HasSuffix(k, "volume ls --format json"):
			return prune.ProbeResult{Ran: true, Stdout: listing}
		case strings.Contains(k, "dangling=true"):
			return prune.ProbeResult{Ran: true, Stdout: gone + "\nuserdata\n"}
		}
		return prune.ProbeResult{Ran: false}
	}
	walked := map[string]bool{}
	o.Walk = func(root string, _ time.Time, _ time.Duration, _ func() time.Time) (WalkResult, error) {
		walked[root] = true
		if strings.Contains(root, "userdata") {
			t.Errorf("walked a volume that is not a scratch volume: %s", root)
		}
		return WalkResult{Bytes: 1000, Files: 10}, nil
	}
	rep := Inventory(o)
	l, g := storeByKey(t, rep, "volumes.scratch.live"), storeByKey(t, rep, "volumes.scratch.gone")
	if l.Count != 1 || l.Bytes != 1000 || l.Sizing != SizingMeasured || l.Section != SectionVolumes {
		t.Errorf("live row = %+v", l)
	}
	if g.Count != 1 || g.Bytes != 1000 || g.Reclaimer.Func != "PruneScratchVolumes" {
		t.Errorf("gone row = %+v", g)
	}
	if len(walked) != 2 {
		t.Errorf("walked %v", walked)
	}

	// Could not ask: unknown, not zero.
	o.Exec = func([]string, time.Duration) prune.ProbeResult { return prune.ProbeResult{Ran: false} }
	rep = Inventory(o)
	if s := storeByKey(t, rep, "volumes.scratch.gone"); s.Sizing != SizingUnknown {
		t.Errorf("unreachable podman gave %+v", s)
	}

	// A mountpoint this machine cannot see (podman's VM): unknown, not absent.
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		if strings.HasSuffix(strings.Join(argv, " "), "--format json") {
			return prune.ProbeResult{Ran: true, Stdout: listing}
		}
		return prune.ProbeResult{Ran: true, Stdout: gone + "\n"}
	}
	o.Walk = func(string, time.Time, time.Duration, func() time.Time) (WalkResult, error) {
		return WalkResult{}, os.ErrNotExist
	}
	rep = Inventory(o)
	if s := storeByKey(t, rep, "volumes.scratch.gone"); s.Sizing != SizingUnknown || s.Count != 1 {
		t.Errorf("a VM-side mountpoint gave %+v", s)
	}
}

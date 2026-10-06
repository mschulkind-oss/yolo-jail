package stores

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// Apple Container's tool disks (OQ-MB1): each workspace's /mise is a disk of its own, and
// `yolo stores` lists every one — whose it is, its size on the Mac's disk, and whether
// `yolo prune` removes it — so the per-workspace copies stay visible and bounded.
func TestToolDiskRows(t *testing.T) {
	liveWS := t.TempDir()
	goneWS := filepath.Join(t.TempDir(), "deleted")
	live := prune.MiseVolumeName(runtime.FromResolved(liveWS))
	gone := prune.MiseVolumeName(runtime.FromResolved(goneWS))
	bare := prune.MiseVolumeName(runtime.FromResolved("/Users/m/bare"))
	row := func(name, ws string) map[string]any {
		labels := map[string]string{}
		if ws != "" {
			labels[prune.MiseVolumeWorkspaceLabel] = ws
		}
		return map[string]any{"id": name, "configuration": map[string]any{
			"name": name, "driver": "local", "format": "ext4", "labels": labels,
			"source": "/vols/" + name + "/volume.img", "sizeInBytes": 549755813888,
		}}
	}
	listing, err := json.Marshal([]map[string]any{
		row(live, liveWS), row(gone, goneWS), row(bare, ""), row(prune.SharedMiseVolume, ""), row("userdata", ""),
	})
	if err != nil {
		t.Fatal(err)
	}

	o, _ := testOptions(t)
	o.DetectRuntime = func() string { return "container" }
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		if slices.Equal(argv, []string{"container", "volume", "ls", "--format", "json"}) {
			return prune.ProbeResult{Ran: true, Stdout: string(listing)}
		}
		return prune.ProbeResult{Ran: false}
	}
	// Allocated bytes, never the 512 GB a sparse image's size says.
	o.DiskBytes = func(path string) (int64, error) {
		if strings.Contains(path, "userdata") {
			t.Errorf("sized a volume that is not a tool disk: %s", path)
		}
		return 200 << 20, nil
	}
	rep := Inventory(o)

	l := storeByKey(t, rep, "volumes.mise."+runtime.FromResolved(liveWS))
	if l.Section != SectionToolDisks || l.Name != live || l.Bytes != 200<<20 || l.Sizing != SizingMeasured ||
		l.Verdict != VerdictYolo || l.Reclaimer.Func != "PruneMiseVolumes" || !strings.Contains(l.Note, liveWS) {
		t.Errorf("live workspace's disk row = %+v", l)
	}
	g := storeByKey(t, rep, "volumes.mise."+runtime.FromResolved(goneWS))
	if g.Verdict != VerdictYolo || !strings.Contains(g.Note, "is gone") || !strings.Contains(g.Note, "yolo prune --apply") {
		t.Errorf("removed workspace's disk row = %+v", g)
	}
	s := storeByKey(t, rep, "volumes.mise.shared")
	if s.Name != prune.SharedMiseVolume || s.Verdict != VerdictYolo || s.Reclaimer.Func != "PruneMiseVolumes" {
		t.Errorf("shared disk row = %+v", s)
	}
	b := storeByKey(t, rep, "volumes.mise."+runtime.FromResolved("/Users/m/bare"))
	if b.Verdict != VerdictHuman || b.Reclaimer.Func != "" || !strings.Contains(b.Note, "`container volume rm "+bare+"`") {
		t.Errorf("an unlabelled disk is not the user's to decide about, with the command: %+v", b)
	}

	// The text report has the section and each disk's note.
	var out strings.Builder
	o.Out = &out
	renderText(rep, o)
	for _, want := range []string{SectionToolDisks, live, gone, "workspace " + goneWS + " is gone"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report does not say %q:\n%s", want, out.String())
		}
	}

	// A size that cannot be read is unknown, never zero.
	o.DiskBytes = func(string) (int64, error) { return 0, errors.New("permission denied") }
	if r := storeByKey(t, Inventory(o), "volumes.mise.shared"); r.Sizing != SizingUnknown {
		t.Errorf("an unreadable disk image gave %+v", r)
	}

	// Could not ask: one unknown row, never an empty section that reads as "no disks".
	o.Exec = func([]string, time.Duration) prune.ProbeResult { return prune.ProbeResult{Ran: false} }
	if r := storeByKey(t, Inventory(o), "volumes.mise"); r.Sizing != SizingUnknown {
		t.Errorf("an unreachable runtime gave %+v", r)
	}

	// Podman has no per-workspace tool disk, so no row and no question asked.
	o.DetectRuntime = func() string { return "podman" }
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		if argv[0] == "container" {
			t.Errorf("podman's inventory asked Apple Container %v", argv)
		}
		return prune.ProbeResult{Ran: false}
	}
	for _, r := range Inventory(o).Stores {
		if r.Section == SectionToolDisks {
			t.Errorf("a podman inventory has a tool disk row: %+v", r)
		}
	}
}

// The real DiskBytes reads allocated blocks: a sparse image with one block written costs a
// block or so, not the size it was truncated to.
func TestAllocatedBytesIsTheSparseImagesCostNotItsCeiling(t *testing.T) {
	img := filepath.Join(t.TempDir(), "volume.img")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	const ceiling = 1 << 30
	if err := f.Truncate(ceiling); err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(make([]byte, 4096), 0); err != nil {
		t.Fatal(err)
	}
	f.Close()
	got, err := allocatedBytes(img)
	if err != nil {
		t.Fatal(err)
	}
	if got <= 0 || got >= ceiling {
		t.Errorf("allocatedBytes = %d for a sparse %d-byte image with 4 KiB written; want its allocation", got, ceiling)
	}
	if _, err := allocatedBytes(filepath.Dir(img)); err == nil {
		t.Error("a directory was sized as a disk image")
	}

	// And it is the default: an inventory left to its own sizing reports the allocation.
	o, _ := testOptions(t)
	o.DetectRuntime = func() string { return "container" }
	listing, _ := json.Marshal([]map[string]any{{"id": prune.SharedMiseVolume, "configuration": map[string]any{
		"name": prune.SharedMiseVolume, "source": img, "labels": map[string]string{}}}})
	o.Exec = func(argv []string, _ time.Duration) prune.ProbeResult {
		return prune.ProbeResult{Ran: true, Stdout: string(listing)}
	}
	if r := storeByKey(t, Inventory(o), "volumes.mise.shared"); r.Sizing != SizingMeasured || r.Bytes != got {
		t.Errorf("the default sizing gave %+v, want %d allocated bytes", r, got)
	}
}

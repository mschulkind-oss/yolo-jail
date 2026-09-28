package prune

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// scratchFake is a RunFunc over canned answers keyed by the argv's joined form, recording
// every call. An argv with no answer did not run (Ran=false).
type scratchFake struct {
	answers map[string]ProbeResult
	calls   []string
}

func (f *scratchFake) run(argv []string, _ time.Duration) ProbeResult {
	k := strings.Join(argv, " ")
	f.calls = append(f.calls, k)
	if r, ok := f.answers[k]; ok {
		return r
	}
	return ProbeResult{Ran: false}
}

const (
	lsAll      = "podman volume ls --format json"
	lsDangling = "podman volume ls --filter dangling=true --format {{.Name}}"
	scratchA   = "yolo-app-1a2b3c4d.scratch.0123456789abcdef.tmp"
	scratchB   = "yolo-app-1a2b3c4d.scratch.0123456789abcdef.var-lib-containers"
	scratchC   = "yolo-other-99999999.scratch.fedcba9876543210.var-tmp"
)

func volJSON(entries ...[3]string) string {
	var parts []string
	for _, e := range entries {
		parts = append(parts, `{"Name":"`+e[0]+`","Mountpoint":"`+e[1]+`","CreatedAt":"`+e[2]+`"}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func mp(name string) string { return "/store/volumes/" + name + "/_data" }

func TestParseScratchVolumeName(t *testing.T) {
	cname, id, slot, ok := ParseScratchVolumeName(ScratchVolumeName("yolo-app-1a2b3c4d", "0123456789abcdef", "var-cache-containers"))
	if !ok || cname != "yolo-app-1a2b3c4d" || id != "0123456789abcdef" || slot != "var-cache-containers" {
		t.Fatalf("round trip = %q %q %q %v", cname, id, slot, ok)
	}
	for _, s := range ScratchSlots {
		if _, _, _, ok := ParseScratchVolumeName(ScratchVolumeName("yolo-x-1", "0123456789abcdef", s.Name)); !ok {
			t.Errorf("slot %q is mounted under a name the reaper does not recognise", s.Name)
		}
	}
	for _, n := range []string{
		"",
		"325ca1a85b88982c055494925c1148ffa62e1be25276376c73c09eb7374c5b07", // an anonymous volume
		"yolo-app-1a2b3c4d.scratch..tmp",                                   // no launch id
		"yolo-app-1a2b3c4d.scratch.0123456789abcdef.home",                  // not a slot
		"mydata.scratch.0123456789abcdef.tmp",                              // not a yolo container
		"yolo-app-1a2b3c4d.scratch.0123456789abcdef.tmp.x",
	} {
		if _, _, _, ok := ParseScratchVolumeName(n); ok {
			t.Errorf("%q parsed as a scratch volume; the reaper would delete it", n)
		}
	}
}

func TestListScratchVolumesTriState(t *testing.T) {
	good := volJSON(
		[3]string{scratchA, mp(scratchA), "2026-09-28T00:14:59.612908636-04:00"},
		[3]string{"userdata", mp("userdata"), "2026-09-28T00:14:59.612908636-04:00"},
		[3]string{scratchB, mp(scratchB), "2026-09-28T00:14:59Z"},
	)
	cases := []struct {
		name    string
		answers map[string]ProbeResult
		known   bool
	}{
		{"runtime absent", map[string]ProbeResult{}, false},
		{"listing refused", map[string]ProbeResult{lsAll: {Ran: true, RC: 125}}, false},
		{"listing unparseable", map[string]ProbeResult{lsAll: {Ran: true, Stdout: "Error: boom"}}, false},
		// A volume missing from the dangling answer is IN USE, so a failed second query
		// cannot be read as "none dangling" — nor, worse, the other way round.
		{"dangling query refused", map[string]ProbeResult{lsAll: {Ran: true, Stdout: good}, lsDangling: {Ran: true, RC: 125}}, false},
		{"dangling query did not run", map[string]ProbeResult{lsAll: {Ran: true, Stdout: good}}, false},
		{"answered", map[string]ProbeResult{lsAll: {Ran: true, Stdout: good}, lsDangling: {Ran: true, Stdout: scratchB + "\nuserdata\n"}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &scratchFake{answers: tc.answers}
			vols, known := ListScratchVolumes("podman", f.run)
			if known != tc.known {
				t.Fatalf("known = %v, want %v", known, tc.known)
			}
			if !known {
				if len(vols) != 0 {
					t.Errorf("could-not-ask returned volumes %v", vols)
				}
				return
			}
			if len(vols) != 2 {
				t.Fatalf("want the two scratch volumes only (never userdata); got %+v", vols)
			}
			if vols[0].Name != scratchA || vols[0].Dangling || vols[0].Cname != "yolo-app-1a2b3c4d" {
				t.Errorf("vols[0] = %+v", vols[0])
			}
			if vols[1].Name != scratchB || !vols[1].Dangling || vols[1].Created.IsZero() {
				t.Errorf("vols[1] = %+v", vols[1])
			}
		})
	}
	if _, known := ListScratchVolumes("container", (&scratchFake{}).run); known {
		t.Error("Apple Container has no scratch volumes to list; it must not claim an answer")
	}
}

func TestReapableScratchVolumes(t *testing.T) {
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	vols := []ScratchVolume{
		{Name: "old-dangling", Dangling: true, Created: now.Add(-2 * time.Hour)},
		{Name: "old-in-use", Dangling: false, Created: now.Add(-2 * time.Hour)},
		{Name: "young-dangling", Dangling: true, Created: now.Add(-10 * time.Second)},
		{Name: "unknown-age", Dangling: true},
	}
	got := ReapableScratchVolumes(vols, now, ScratchVolumeGrace)
	if len(got) != 1 || got[0].Name != "old-dangling" {
		t.Fatalf("reapable = %+v, want only old-dangling", got)
	}
}

func TestEmptyScratchVolumeArgvAndGuard(t *testing.T) {
	defer func(f func() int) { geteuid = f }(geteuid)
	v := ScratchVolume{Name: scratchA, Mountpoint: mp(scratchA)}

	geteuid = func() int { return 1000 }
	f := &scratchFake{answers: map[string]ProbeResult{}}
	emptyScratchVolume("podman", v, f.run)
	if want := "podman unshare rm -rf -- " + mp(scratchA); !slices.Equal(f.calls, []string{want}) {
		t.Errorf("rootless empty = %v, want %q", f.calls, want)
	}

	geteuid = func() int { return 0 }
	f = &scratchFake{answers: map[string]ProbeResult{}}
	emptyScratchVolume("podman", v, f.run)
	if want := "rm -rf -- " + mp(scratchA); !slices.Equal(f.calls, []string{want}) {
		t.Errorf("rootful empty = %v, want %q (podman refuses unshare when rootful)", f.calls, want)
	}

	for _, bad := range []string{
		"", "relative/volumes/" + scratchA + "/_data", "/", "/store/volumes/" + scratchA,
		"/store/volumes/other/_data", "/store/volumes/" + scratchA + "/_data/..", "/home/me/" + scratchA + "/_data",
	} {
		f = &scratchFake{answers: map[string]ProbeResult{}}
		if emptyScratchVolume("podman", ScratchVolume{Name: scratchA, Mountpoint: bad}, f.run) || len(f.calls) != 0 {
			t.Errorf("mountpoint %q was emptied (%v); only <root>/volumes/<name>/_data may be", bad, f.calls)
		}
	}
}

func TestRemoveScratchVolumeNeverForces(t *testing.T) {
	defer func(f func() int) { geteuid = f }(geteuid)
	geteuid = func() int { return 1000 }
	v := ScratchVolume{Name: scratchA, Mountpoint: mp(scratchA)}
	f := &scratchFake{answers: map[string]ProbeResult{
		"podman unshare rm -rf -- " + mp(scratchA): {Ran: true},
		"podman volume rm " + scratchA:             {Ran: true},
	}}
	if !RemoveScratchVolume("podman", v, f.run) {
		t.Fatal("removal reported failure")
	}
	want := []string{"podman unshare rm -rf -- " + mp(scratchA), "podman volume rm " + scratchA}
	if !reflect.DeepEqual(f.calls, want) {
		t.Errorf("calls = %v, want the empty then the plain rm %v", f.calls, want)
	}
	for _, c := range f.calls {
		if strings.Contains(c, "--force") || strings.Contains(c, " -f ") {
			t.Errorf("%q forces; --force removes any container using the volume", c)
		}
	}

	// A refused rm of a volume that is gone anyway (another remover won) is success; one
	// that still exists is not.
	gone := &scratchFake{answers: map[string]ProbeResult{
		"podman volume rm " + scratchA:     {Ran: true, RC: 1},
		"podman volume exists " + scratchA: {Ran: true, RC: 1},
	}}
	if !RemoveScratchVolume("podman", v, gone.run) {
		t.Error("a volume removed by someone else is the wanted outcome")
	}
	stuck := &scratchFake{answers: map[string]ProbeResult{
		"podman volume rm " + scratchA:     {Ran: true, RC: 2},
		"podman volume exists " + scratchA: {Ran: true, RC: 0},
	}}
	if RemoveScratchVolume("podman", v, stuck.run) {
		t.Error("a volume that still exists was reported removed")
	}
}

func TestPruneScratchVolumes(t *testing.T) {
	defer func(f func() int) { geteuid = f }(geteuid)
	geteuid = func() int { return 1000 }
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	old := now.Add(-time.Hour).Format(time.RFC3339Nano)
	answers := func() map[string]ProbeResult {
		return map[string]ProbeResult{
			lsAll: {Ran: true, Stdout: volJSON(
				[3]string{scratchA, mp(scratchA), old},
				[3]string{scratchB, mp(scratchB), old},
				[3]string{scratchC, mp(scratchC), now.Format(time.RFC3339Nano)},
			)},
			// A is in use (a live jail), B dangling and old, C dangling but young.
			lsDangling:                     {Ran: true, Stdout: scratchB + "\n" + scratchC + "\n"},
			"podman volume rm " + scratchB: {Ran: true},
		}
	}
	dry := &scratchFake{answers: answers()}
	removed, failed, known := PruneScratchVolumes("podman", false, now, dry.run)
	if !known || !slices.Equal(removed, []string{scratchB}) || len(failed) != 0 {
		t.Fatalf("dry-run = %v %v %v", removed, failed, known)
	}
	for _, c := range dry.calls {
		if strings.Contains(c, " rm ") {
			t.Errorf("dry-run removed something: %q", c)
		}
	}
	wet := &scratchFake{answers: answers()}
	removed, _, _ = PruneScratchVolumes("podman", true, now, wet.run)
	if !slices.Equal(removed, []string{scratchB}) || !slices.Contains(wet.calls, "podman volume rm "+scratchB) {
		t.Fatalf("apply removed %v; calls %v", removed, wet.calls)
	}
	for _, c := range wet.calls {
		if strings.Contains(c, scratchA) && strings.Contains(c, " rm") || strings.Contains(c, scratchC) && strings.Contains(c, " rm") {
			t.Errorf("apply touched a volume it had no evidence for: %q", c)
		}
	}
	if _, _, known := PruneScratchVolumes("podman", true, now, (&scratchFake{}).run); known {
		t.Error("an unreachable runtime must report could-not-ask")
	}
}

// THE CALL SITE: `yolo prune` runs the section and names what it would remove. Delete the
// section from Run and this fails.
func TestPruneRunListsGoneJailsScratchVolumes(t *testing.T) {
	defer func(f func() int) { geteuid = f }(geteuid)
	geteuid = func() int { return 1000 }
	o, _ := baseOpts(t)
	old := o.Now().Add(-time.Hour).Format(time.RFC3339Nano)
	var calls []string
	o.Exec = stubExec(map[string]string{
		k("podman", "volume", "ls", "--format", "json"):                                   volJSON([3]string{scratchB, mp(scratchB), old}),
		k("podman", "volume", "ls", "--filter", "dangling=true", "--format", "{{.Name}}"): scratchB + "\n",
	}, &calls)
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	for _, want := range []string{"Scratch volumes of gone jails", "  would remove: 1 volume(s)", "    • " + scratchB} {
		if !hasLine(&buf, want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
	for _, c := range calls {
		if strings.Contains(c, "volume rm") {
			t.Errorf("dry-run removed: %q", c)
		}
	}
}

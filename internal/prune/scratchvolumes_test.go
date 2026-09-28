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

// sharedClock pins whether the runtime reads as stamping volumes on the host's clock, so a
// test means the same thing on a Linux and a darwin runner.
func sharedClock(t *testing.T, shared bool) {
	t.Helper()
	old := runtimeSharesHostClock
	runtimeSharesHostClock = func() bool { return shared }
	t.Cleanup(func() { runtimeSharesHostClock = old })
}

func TestReapableScratchVolumes(t *testing.T) {
	sharedClock(t, true)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	vols := []ScratchVolume{
		{Name: "old-dangling", Dangling: true, Created: now.Add(-2 * time.Hour)},
		{Name: "old-in-use", Dangling: false, Created: now.Add(-2 * time.Hour)},
		{Name: "young-dangling", Dangling: true, Created: now.Add(-10 * time.Second)},
		{Name: "unknown-age", Dangling: true},
		{Name: "future", Dangling: true, Created: now.Add(10 * time.Minute)},
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

// A PODMAN MACHINE's volumes are stamped by the VM's clock, and a VM running behind the host
// made a volume created a moment ago read as minutes old against the host's clock. On the
// remote arm the age is taken between two times the VM stamped, so no skew can make a volume
// older than it is. The listing is what a macOS podman client prints: RFC 3339 with
// nanoseconds, in the VM's UTC.
func TestARemoteRuntimesSkewCannotAgeAVolume(t *testing.T) {
	sharedClock(t, false)
	host := time.Date(2026, 9, 28, 13, 43, 30, 0, time.UTC)
	vm := host.Add(-5 * time.Minute) // the VM's clock, five minutes behind the host's
	stamp := func(d time.Duration) string { return vm.Add(d).Format(time.RFC3339Nano) }
	leftover := "yolo-scratchreap-1a2b3c4d.scratch.00000000deadbeef.var-lib-containers"
	young := "yolo-scratchreap-1a2b3c4d.scratch.00000000cafef00d.tmp"
	own := "yolo-scratchreap-1a2b3c4d.scratch.0123456789abcdef.tmp" // this launch's, in use
	f := &scratchFake{answers: map[string]ProbeResult{
		lsAll: {Ran: true, Stdout: volJSON(
			[3]string{leftover, mp(leftover), stamp(-90 * time.Second)},
			[3]string{young, mp(young), stamp(-20 * time.Second)},
			[3]string{own, mp(own), stamp(-2 * time.Second)},
		)},
		lsDangling: {Ran: true, Stdout: leftover + "\n" + young + "\n"},
	}}
	vols, known := ListScratchVolumes("podman", f.run)
	if !known {
		t.Fatal("the listing answered")
	}
	var names []string
	for _, v := range ReapableScratchVolumes(vols, host, ScratchVolumeGrace) {
		names = append(names, v.Name)
	}
	if !slices.Equal(names, []string{leftover}) {
		t.Errorf("reapable = %v, want only the leftover made a floor and more before the newest "+
			"volume: the young one is 5 min old by the host's clock and 18 s old by the VM's", names)
	}

	// The same listing compared against the host's clock is the defect this arm exists for.
	sharedClock(t, true)
	var hostArm []string
	for _, v := range ReapableScratchVolumes(vols, host, ScratchVolumeGrace) {
		hostArm = append(hostArm, v.Name)
	}
	if !slices.Contains(hostArm, young) {
		t.Fatalf("the fixture no longer shows the skew: the host-clock arm spared %s", young)
	}
}

// On the remote arm a listing with nothing newer than the volume itself proves no age, and a
// row with no RuntimeNewest proves none either.
func TestARemoteRuntimeReapsNothingItCannotProveOld(t *testing.T) {
	sharedClock(t, false)
	now := time.Date(2026, 9, 28, 12, 0, 0, 0, time.UTC)
	created := now.Add(-3 * time.Hour)
	for _, v := range []ScratchVolume{
		{Name: "alone", Dangling: true, Created: created, RuntimeNewest: created},
		{Name: "no-newest", Dangling: true, Created: created},
	} {
		if got := ReapableScratchVolumes([]ScratchVolume{v}, now, ScratchVolumeGrace); len(got) != 0 {
			t.Errorf("%s: reaped %+v with no evidence of its age on the runtime's clock", v.Name, got)
		}
	}
}

// Every creation time a client might print either parses to its instant or is unknown, and an
// unknown one is never reapable. The two RFC 3339 shapes (a Linux podman prints its local
// offset, a podman machine's VM prints Z) are one instant here; Go's default time.String form
// and a zone-less time do not parse, so they read as young on both arms.
func TestScratchCreationTimeShapes(t *testing.T) {
	const newest = "2026-09-28T13:50:00Z"
	for _, tc := range []struct {
		createdAt string
		parses    bool
	}{
		{"2026-09-28T13:43:21.612908636Z", true},
		{"2026-09-28T09:43:21.612908636-04:00", true},
		{"2026-09-28T13:43:21Z", true},
		{"2026-09-28 13:43:21.612908636 +0000 UTC", false},
		{"2026-09-28T13:43:21.612908636", false},
		{"", false},
	} {
		f := &scratchFake{answers: map[string]ProbeResult{
			lsAll: {Ran: true, Stdout: volJSON(
				[3]string{scratchA, mp(scratchA), tc.createdAt},
				[3]string{"userdata", mp("userdata"), newest},
			)},
			lsDangling: {Ran: true, Stdout: scratchA + "\n"},
		}}
		vols, known := ListScratchVolumes("podman", f.run)
		if !known || len(vols) != 1 {
			t.Fatalf("%q: listing = %+v %v", tc.createdAt, vols, known)
		}
		if got := !vols[0].Created.IsZero(); got != tc.parses {
			t.Errorf("%q: parsed = %v, want %v", tc.createdAt, got, tc.parses)
		}
		if tc.parses && !vols[0].Created.Equal(time.Date(2026, 9, 28, 13, 43, 21, vols[0].Created.Nanosecond(), time.UTC)) {
			t.Errorf("%q: parsed to %s, not the instant it names", tc.createdAt, vols[0].Created)
		}
		if want, _ := time.Parse(time.RFC3339, newest); !vols[0].RuntimeNewest.Equal(want) {
			t.Errorf("%q: RuntimeNewest = %s, want the newest volume of ANY kind, %s", tc.createdAt, vols[0].RuntimeNewest, want)
		}
		for _, shared := range []bool{true, false} {
			sharedClock(t, shared)
			reaped := len(ReapableScratchVolumes(vols, time.Date(2026, 9, 28, 14, 0, 0, 0, time.UTC), ScratchVolumeGrace)) == 1
			if reaped != tc.parses {
				t.Errorf("%q (shared clock %v): reaped = %v, want %v", tc.createdAt, shared, reaped, tc.parses)
			}
		}
	}
}

func TestPruneScratchVolumes(t *testing.T) {
	sharedClock(t, true)
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
	sharedClock(t, true)
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

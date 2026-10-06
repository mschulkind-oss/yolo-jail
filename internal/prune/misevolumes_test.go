package prune

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// misevolumes_test.go pins OQ-MB1 (docs/research/macos-backend-performance.md, ruled A on
// 2026-10-05): on Apple Container each workspace's /mise is a disk of its own, named from its
// container name and labelled with its workspace, and `yolo prune` removes the disk of a
// workspace that is gone and the one shared disk every jail used before.

// appleVolumeNamePattern is Apple Container's own rule for a volume name
// (VolumeStorage.volumeNamePattern, container 1.1.0 and 1.5.0 alike), copied so a name this
// package spells is checked against the runtime's rule rather than against its own regexp.
var appleVolumeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

func TestMiseVolumeNameIsPerWorkspaceAndANamedVolume(t *testing.T) {
	a := runtime.FromResolved("/Users/m/code/app")
	b := runtime.FromResolved("/Users/m/code/other")
	long := runtime.FromResolved("/Users/m/" + strings.Repeat("x", 80))
	if MiseVolumeName(a) == MiseVolumeName(b) {
		t.Fatalf("two workspaces got one tool disk, %q: the second jail's attachment is the one "+
			"VZ refuses (VZErrorDomain Code=2)", MiseVolumeName(a))
	}
	for _, cname := range []string{a, b, long} {
		name := MiseVolumeName(cname)
		if !appleVolumeNamePattern.MatchString(name) || len(name) > 255 {
			t.Errorf("%q is not a volume name Apple Container accepts", name)
		}
		// No slash, so `container run -v <name>:/mise` reads it as a NAMED VOLUME — an ext4 disk
		// image, case-sensitive — and never as a host folder shared over virtiofs onto APFS.
		if strings.Contains(name, "/") || name == "." || name == ".." {
			t.Errorf("%q would be read as a host path by `container run -v`", name)
		}
		if name == SharedMiseVolume {
			t.Errorf("a workspace's disk is named like the shared one: %q", name)
		}
		got, ok := ParseMiseVolumeName(name)
		if !ok || got != cname {
			t.Errorf("ParseMiseVolumeName(%q) = %q, %v; want %q", name, got, ok, cname)
		}
	}
	for _, name := range []string{
		SharedMiseVolume, "yolo-app-1a2b3c4d", "yolo-app-1a2b3c4d.mise.bak", "YOLO-app-1a2b3c4d.mise",
		"other.mise", ScratchVolumeName(a, "0123456789abcdef", "tmp"), "",
	} {
		if _, ok := ParseMiseVolumeName(name); ok {
			t.Errorf("ParseMiseVolumeName(%q) claims a volume yolo did not name for a workspace", name)
		}
	}
}

func TestMiseVolumeCreateArgvRecordsTheWorkspace(t *testing.T) {
	ws := "/Users/m/My Code/a=b"
	cname := runtime.FromResolved(ws)
	got := MiseVolumeCreateArgv(cname, ws)
	want := []string{"container", "volume", "create",
		"--label", "org.yolo-jail.owner=yolo",
		"--label", "org.yolo-jail.workspace=" + ws,
		cname + ".mise"}
	if !slices.Equal(got, want) {
		t.Fatalf("create argv =\n%q\nwant\n%q", got, want)
	}
	if MiseVolumeOwnerLabel != JailImageOwnerLabel || MiseVolumeOwnerValue != JailImageOwnerValue {
		t.Error("the tool disk's owner label drifted from the image's: one key says \"yolo made this\"")
	}
}

// acVolumeListing renders rows the way `container volume ls --format json` does (container
// 1.1.0 and the 1.5.0 source read alike): an array of {id, configuration}.
func acVolumeListing(t *testing.T, rows ...map[string]any) string {
	t.Helper()
	var out []map[string]any
	for _, r := range rows {
		out = append(out, map[string]any{"id": r["name"], "configuration": r})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func acVolume(name, workspace string) map[string]any {
	labels := map[string]string{}
	if workspace != "" {
		labels[MiseVolumeOwnerLabel] = MiseVolumeOwnerValue
		labels[MiseVolumeWorkspaceLabel] = workspace
	}
	return map[string]any{
		"name": name, "driver": "local", "format": "ext4",
		"source":       "/Users/m/Library/Application Support/com.apple.container/volumes/" + name + "/volume.img",
		"creationDate": "2026-10-05T12:00:00Z", "labels": labels, "options": map[string]string{},
		"sizeInBytes": 549755813888,
	}
}

// volumeFake answers `container volume ls --format json` and records every call.
type volumeFake struct {
	listing string
	listOK  bool
	rmFails map[string]bool
	calls   []string
}

func (f *volumeFake) run(argv []string, _ time.Duration) ProbeResult {
	f.calls = append(f.calls, strings.Join(argv, " "))
	switch {
	case slices.Equal(argv, []string{"container", "volume", "ls", "--format", "json"}):
		if !f.listOK {
			return ProbeResult{Ran: true, RC: 1}
		}
		return ProbeResult{Ran: true, Stdout: f.listing}
	case len(argv) == 4 && argv[0] == "container" && argv[1] == "volume" && argv[2] == "rm":
		if f.rmFails[argv[3]] {
			return ProbeResult{Ran: true, RC: 1}
		}
		return ProbeResult{Ran: true, Stdout: argv[3] + "\n"}
	}
	return ProbeResult{Ran: true}
}

func (f *volumeFake) removals() []string {
	var out []string
	for _, c := range f.calls {
		if strings.HasPrefix(c, "container volume rm ") {
			out = append(out, strings.TrimPrefix(c, "container volume rm "))
		}
	}
	return out
}

// toolDiskFixture is one machine's Apple Container volumes: a live workspace's disk, a
// removed workspace's, one with no workspace recorded, one whose label names another
// workspace, the shared disk, and two volumes that are not yolo's tool disks at all.
type toolDiskFixture struct {
	live, gone, unlabelled, mismatched string
	goneWS                             string
	fake                               *volumeFake
}

func newToolDiskFixture(t *testing.T) toolDiskFixture {
	t.Helper()
	liveWS := t.TempDir()
	goneWS := filepath.Join(t.TempDir(), "deleted-project")
	f := toolDiskFixture{
		live:       MiseVolumeName(runtime.FromResolved(liveWS)),
		gone:       MiseVolumeName(runtime.FromResolved(goneWS)),
		unlabelled: MiseVolumeName(runtime.FromResolved("/Users/m/unlabelled")),
		mismatched: MiseVolumeName(runtime.FromResolved("/Users/m/renamed")),
		goneWS:     goneWS,
	}
	f.fake = &volumeFake{listOK: true, listing: acVolumeListing(t,
		acVolume(f.live, liveWS),
		acVolume(f.gone, goneWS),
		acVolume(f.unlabelled, ""),
		// A label naming a workspace that is gone, on a disk whose name is not that
		// workspace's: the label is not this disk's evidence, so it is kept.
		acVolume(f.mismatched, filepath.Join(t.TempDir(), "elsewhere")),
		acVolume(SharedMiseVolume, ""),
		acVolume("userdata", ""),
		acVolume(ScratchVolumeName(runtime.FromResolved(liveWS), "0123456789abcdef", "tmp"), ""),
	)}
	return f
}

func TestListMiseVolumesReadsAppleContainersListing(t *testing.T) {
	f := newToolDiskFixture(t)
	vols, known := ListMiseVolumes("container", f.fake.run)
	if !known {
		t.Fatal("a listing that answered read as could-not-ask")
	}
	var names []string
	for _, v := range vols {
		names = append(names, v.Name)
	}
	want := []string{f.gone, f.live, f.mismatched, f.unlabelled, SharedMiseVolume}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Fatalf("tool disks = %v, want %v (a volume that is not yolo's was listed, or one of yolo's was missed)", names, want)
	}
	for _, v := range vols {
		if v.Source == "" || !strings.HasSuffix(v.Source, "/volume.img") {
			t.Errorf("%s: source %q not read", v.Name, v.Source)
		}
		if v.Name == f.gone && v.Workspace != f.goneWS {
			t.Errorf("%s: workspace %q, want %q", v.Name, v.Workspace, f.goneWS)
		}
		if (v.Name == SharedMiseVolume) != v.Shared {
			t.Errorf("%s: Shared = %v", v.Name, v.Shared)
		}
	}

	// A flat row (a VolumeConfiguration without the {id, configuration} wrapper) reads too.
	flat, _ := json.Marshal([]map[string]any{acVolume(f.gone, f.goneWS)})
	vols, known = ListMiseVolumes("container", (&volumeFake{listOK: true, listing: string(flat)}).run)
	if !known || len(vols) != 1 || vols[0].Workspace != f.goneWS {
		t.Errorf("flat listing gave %+v, %v", vols, known)
	}

	for name, fake := range map[string]*volumeFake{
		"the listing failed":       {listOK: false},
		"the listing is not JSON":  {listOK: true, listing: "NAME TYPE\n"},
		"the listing is an object": {listOK: true, listing: `{"error":"x"}`},
	} {
		if _, known := ListMiseVolumes("container", fake.run); known {
			t.Errorf("%s: read as an answer, so a sweep would act on nothing it knows", name)
		}
	}
	podman := &volumeFake{listOK: true, listing: f.fake.listing}
	if _, known := ListMiseVolumes("podman", podman.run); known || len(podman.calls) != 0 {
		t.Errorf("podman was asked for Apple Container's tool disks (%v); its /mise is the machine's", podman.calls)
	}
}

func TestToolDiskStates(t *testing.T) {
	f := newToolDiskFixture(t)
	vols, _ := ListMiseVolumes("container", f.fake.run)
	want := map[string]MiseVolumeState{
		f.live:           MiseVolumeLive,
		f.gone:           MiseVolumeWorkspaceGone,
		f.unlabelled:     MiseVolumeUnattributed,
		f.mismatched:     MiseVolumeUnattributed,
		SharedMiseVolume: MiseVolumeRetired,
	}
	for _, v := range vols {
		got, why := v.State()
		if got != want[v.Name] {
			t.Errorf("%s: state %v (%s), want %v", v.Name, got, why, want[v.Name])
		}
		if why == "" {
			t.Errorf("%s: a state with no reason", v.Name)
		}
		if got.Reclaimable() != (got == MiseVolumeWorkspaceGone || got == MiseVolumeRetired) {
			t.Errorf("%s: Reclaimable() = %v", v.Name, got.Reclaimable())
		}
	}
}

func TestPruneMiseVolumesRemovesOnlyGoneWorkspacesAndTheSharedDisk(t *testing.T) {
	f := newToolDiskFixture(t)
	wantGone := []string{f.gone, SharedMiseVolume}
	slices.Sort(wantGone)

	removed, failed, _, known := PruneMiseVolumes("container", false, f.fake.run)
	if !known || !slices.Equal(names(removed), wantGone) || len(failed) != 0 {
		t.Fatalf("dry-run = %v %v %v, want %v", names(removed), names(failed), known, wantGone)
	}
	if r := f.fake.removals(); len(r) != 0 {
		t.Fatalf("a dry run removed %v", r)
	}

	wet := newToolDiskFixture(t)
	wet.fake.rmFails = map[string]bool{SharedMiseVolume: true}
	removed, failed, unattributed, known := PruneMiseVolumes("container", true, wet.fake.run)
	if !known || !slices.Equal(names(removed), []string{wet.gone}) || !slices.Equal(names(failed), []string{SharedMiseVolume}) {
		t.Fatalf("apply = removed %v failed %v (%v)", names(removed), names(failed), known)
	}
	got := wet.fake.removals()
	slices.Sort(got)
	wantRm := []string{wet.gone, SharedMiseVolume}
	slices.Sort(wantRm)
	if !slices.Equal(got, wantRm) {
		t.Fatalf("apply ran `container volume rm` on %v, want %v: a live or unattributed disk was touched", got, wantRm)
	}
	wantKept := []string{wet.mismatched, wet.unlabelled}
	slices.Sort(wantKept)
	if !slices.Equal(names(unattributed), wantKept) {
		t.Errorf("unattributed = %v, want %v", names(unattributed), wantKept)
	}

	if _, _, _, known := PruneMiseVolumes("container", true, (&volumeFake{}).run); known {
		t.Error("an unreachable runtime must report could-not-ask")
	}
}

func names(vols []MiseVolume) []string {
	var out []string
	for _, v := range vols {
		out = append(out, v.Name)
	}
	slices.Sort(out)
	return out
}

// THE CALL SITE: `yolo prune` on Apple Container runs the section and names each disk it
// would remove. Delete the section from Run and this fails.
func TestPruneRunListsRemovedWorkspacesToolDisks(t *testing.T) {
	f := newToolDiskFixture(t)
	o, _ := baseOpts(t)
	o.DetectRuntime = func() string { return "container" }
	o.Exec = f.fake.run
	var buf bytes.Buffer
	o.Out = &buf
	Run(o)
	for _, want := range []string{
		"Apple Container tool disks  (/mise: one per workspace)",
		"  would remove: 2 disk(s)",
		"    • " + f.gone + "  workspace " + f.goneWS + " is gone",
		"  kept 2 disk(s) whose workspace yolo cannot tell; `yolo stores` lists them",
	} {
		if !hasLine(&buf, want) {
			t.Errorf("missing %q in:\n%s", want, buf.String())
		}
	}
	if !strings.Contains(buf.String(), "    • "+SharedMiseVolume+"  ") {
		t.Errorf("the shared disk is not named:\n%s", buf.String())
	}
	if r := f.fake.removals(); len(r) != 0 {
		t.Errorf("a dry run removed %v", r)
	}

	// --format json names them too.
	f = newToolDiskFixture(t)
	o.Exec = f.fake.run
	o.Format = "json"
	buf.Reset()
	Run(o)
	var rep Report
	if err := json.Unmarshal(buf.Bytes(), &rep); err != nil {
		t.Fatalf("decoding the JSON report: %v\n%s", err, buf.String())
	}
	wantGone := []string{f.gone, SharedMiseVolume}
	slices.Sort(wantGone)
	if !slices.Equal(rep.RemovedToolDisks, wantGone) {
		t.Errorf("removed_tool_disks = %v, want %v", rep.RemovedToolDisks, wantGone)
	}

	// On podman the section says it does not apply, and asks nothing.
	o, _ = baseOpts(t)
	buf.Reset()
	o.Out = &buf
	Run(o)
	if !hasLine(&buf, "  not applicable — only Apple Container gives each workspace a tool disk of its own") {
		t.Errorf("the podman report does not say the section is Apple Container's:\n%s", buf.String())
	}
}

// The workspace a gone disk names is read with os.Stat, so a workspace that exists through a
// symbolic link (macOS's /var -> /private/var) is not called gone.
func TestAToolDiskWhoseWorkspaceIsReachedThroughALinkIsLive(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "ws")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("cannot make a symlink here:", err)
	}
	v := MiseVolume{Name: MiseVolumeName(runtime.FromResolved(link)), Cname: runtime.FromResolved(link), Workspace: link}
	if st, why := v.State(); st != MiseVolumeLive {
		t.Errorf("state %v (%s), want live", st, why)
	}
}

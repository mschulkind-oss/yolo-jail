package runtime

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// TestALaunchRecordRoundTripsEveryField is slice 11b (named-setups.md SU-D11): the tracking file
// is the launch record, and every field the launch wrote reads back, under the §12.4 keys.
func TestALaunchRecordRoundTripsEveryField(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	started := time.Date(2026, 11, 6, 14, 2, 11, 0, time.UTC)
	want := LaunchRecord{
		Name:        "yolo-thing-8c1e04aa-podman-self",
		Setup:       "podman-self",
		Confinement: setup.ConfinementPodman,
		RunAs:       setup.RunAsSelf,
		Workspace:   "/home/matt/code/thing",
		Started:     started,
		Packs:       []string{"claude", "codex"},
		YoloVersion: "0.17.0+12",
	}
	if err := WriteLaunchRecord(want); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadLaunchRecord(want.Name)
	if !ok {
		t.Fatal("the record just written does not read")
	}
	if got.Name != want.Name || got.Setup != want.Setup || got.Confinement != want.Confinement ||
		got.RunAs != want.RunAs || got.Workspace != want.Workspace || !got.Started.Equal(started) ||
		!slices.Equal(got.Packs, want.Packs) || got.YoloVersion != want.YoloVersion {
		t.Errorf("read %+v, want %+v", got, want)
	}
	raw, err := os.ReadFile(filepath.Join(paths.ContainerDir(), want.Name))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"name"`, `"setup": "podman-self"`, `"confinement": "podman"`, `"run_as": "self"`,
		`"workspace"`, `"started": "2026-11-06T14:02:11Z"`, `"packs"`, `"yolo_version": "0.17.0+12"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("the record lacks %s:\n%s", key, raw)
		}
	}
	if ws, ok := ReadContainerWorkspace(want.Name); !ok || ws != want.Workspace {
		t.Errorf("ReadContainerWorkspace = %q, %v; want the record's workspace", ws, ok)
	}

	// A launch that selected no pack records none, which is not unknown.
	none := LaunchRecord{Name: "yolo-none", Workspace: "/ws", Packs: []string{}}
	if err := WriteLaunchRecord(none); err != nil {
		t.Fatal(err)
	}
	if got, _ := ReadLaunchRecord("yolo-none"); got.Packs == nil || len(got.Packs) != 0 {
		t.Errorf("an empty pack list read back as %#v, want empty and known", got.Packs)
	}
	leftovers, _ := filepath.Glob(filepath.Join(paths.ContainerDir(), ".*"))
	if len(leftovers) != 0 {
		t.Errorf("the write left temporary files: %v", leftovers)
	}
}

// TestALegacyTrackingFileReadsAsTheWorkspaceAlone: a record an older yolo wrote holds the
// workspace on one line, and reads as that workspace with every other field unknown.
func TestALegacyTrackingFileReadsAsTheWorkspaceAlone(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(paths.ContainerDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.ContainerDir(), "yolo-old-3f9a2c1d"), []byte("/home/matt/old-thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := ReadLaunchRecord("yolo-old-3f9a2c1d")
	want := LaunchRecord{Name: "yolo-old-3f9a2c1d", Workspace: "/home/matt/old-thing"}
	if !ok || got.Name != want.Name || got.Workspace != want.Workspace || got.Setup != "" ||
		got.Confinement != "" || got.RunAs != "" || !got.Started.IsZero() || got.Packs != nil || got.YoloVersion != "" {
		t.Errorf("legacy record read as %+v, %v; want %+v with every other field unknown", got, ok, want)
	}
	if ws, ok := ReadContainerWorkspace("yolo-old-3f9a2c1d"); !ok || ws != want.Workspace {
		t.Errorf("ReadContainerWorkspace = %q, %v", ws, ok)
	}
}

// TestARecordNamingItsTechnologyIsJudgedByThatTechnology is the technology half of SU-D10: a
// record naming podman goes on Podman's listed answer alone while Apple Container is unreachable,
// and a PATH miss (not-installed) alone never authorizes deleting a record that names it. A record
// naming no technology keeps the all-sources rule, under which a PATH miss is no answer either.
func TestARecordNamingItsTechnologyIsJudgedByThatTechnology(t *testing.T) {
	const podmanDead, acRec, legacy = "yolo-pd-dead", "yolo-ac-rec", "yolo-legacy"
	seed := func(t *testing.T) {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		for _, r := range []LaunchRecord{
			{Name: podmanDead, Workspace: "/ws/p", Confinement: setup.ConfinementPodman},
			{Name: acRec, Workspace: "/ws/a", Confinement: setup.ConfinementAppleContainer},
		} {
			if err := WriteLaunchRecord(r); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(paths.ContainerDir(), legacy), []byte("/ws/l\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tracked := func(n string) bool { _, ok := ReadLaunchRecord(n); return ok }
	podmanEmpty := SourceAnswer{Confinement: "podman", State: SourceListed, Names: map[string]struct{}{}}

	t.Run("podman listed, apple-container unreachable", func(t *testing.T) {
		seed(t)
		removed := PruneStaleTrackingFiles([]SourceAnswer{podmanEmpty,
			{Confinement: "apple-container", State: SourceUnreachable}})
		if !slices.Equal(removed, []string{podmanDead}) || !tracked(acRec) || !tracked(legacy) {
			t.Errorf("removed %v; want only the podman record (apple-container's kept %v, the legacy one kept %v)",
				removed, tracked(acRec), tracked(legacy))
		}
	})
	t.Run("apple-container not installed", func(t *testing.T) {
		seed(t)
		removed := PruneStaleTrackingFiles([]SourceAnswer{podmanEmpty,
			{Confinement: "apple-container", State: SourceNotInstalled}})
		sort.Strings(removed)
		if !slices.Equal(removed, []string{podmanDead}) || !tracked(acRec) || !tracked(legacy) {
			t.Errorf("removed %v; a PATH miss must delete neither the record naming apple-container (kept %v) "+
				"nor one naming no technology (kept %v)", removed, tracked(acRec), tracked(legacy))
		}
	})
	t.Run("podman timed out", func(t *testing.T) {
		seed(t)
		removed := PruneStaleTrackingFiles([]SourceAnswer{{Confinement: "podman", State: SourceTimedOut},
			{Confinement: "apple-container", State: SourceListed, Names: map[string]struct{}{}}})
		if !slices.Equal(removed, []string{acRec}) || !tracked(podmanDead) || !tracked(legacy) {
			t.Errorf("removed %v; want only the apple-container record", removed)
		}
	})
	t.Run("a listed name stays", func(t *testing.T) {
		seed(t)
		removed := PruneStaleTrackingFiles([]SourceAnswer{
			{Confinement: "podman", State: SourceListed, Names: map[string]struct{}{podmanDead: {}}},
			{Confinement: "apple-container", State: SourceListed, Names: map[string]struct{}{}}})
		sort.Strings(removed)
		if !slices.Equal(removed, []string{acRec, legacy}) || !tracked(podmanDead) {
			t.Errorf("removed %v; want the running podman jail's record kept", removed)
		}
	})
}

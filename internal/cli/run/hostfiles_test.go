package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostFileIn builds a minimal assembleInput carrying the given host_files entries.
func hostFileIn(t *testing.T, entries ...config.HostFileEntry) *assembleInput {
	t.Helper()
	return &assembleInput{
		wsState:      filepath.Join(t.TempDir(), "home"),
		mountTargets: map[string]struct{}{},
		hostFiles:    entries,
	}
}

// TestHostFilesEnvOnlyWhenPresent is the golden-argv guard: a jail with no
// host_files must emit no YOLO_HOST_FILES at all (not an empty one), so the frozen
// argv of every existing jail is unchanged by this feature.
func TestHostFilesEnvOnlyWhenPresent(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	if got := o.hostFilesEnv(hostFileIn(t)); got != nil {
		t.Errorf("no entries emitted env %v, want nil", got)
	}
	in := hostFileIn(t, config.HostFileEntry{
		Path: ".config/a.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce,
	})
	got := o.hostFilesEnv(in)
	if len(got) != 2 || got[0] != "-e" || !strings.HasPrefix(got[1], "YOLO_HOST_FILES=") {
		t.Fatalf("hostFilesEnv = %v, want [-e YOLO_HOST_FILES=<json>]", got)
	}
	// The wire form must round-trip: the entrypoint decodes exactly this string.
	wire := strings.TrimPrefix(got[1], "YOLO_HOST_FILES=")
	back, err := config.UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatalf("emitted env does not decode: %v", err)
	}
	if len(back) != 1 || back[0].Path != ".config/a.json" {
		t.Errorf("round-trip lost the entry: %+v", back)
	}
}

// TestHostUserFileArgsMountsSourcesOnly: only a source-bearing entry gets a :ro
// /ctx/host-user/<slug> mount, and the slug must be the one the entrypoint
// derives — that agreement is the whole contract between the two halves.
func TestHostUserFileArgsMountsSourcesOnly(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "config.json")
	if err := os.WriteFile(src, []byte(`{"k":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sourceBearing := config.HostFileEntry{
		Path: ".config/mytool/config.json", Source: src, Codec: "json",
		Mode: config.HostFileModeReadonly,
	}
	sourceLess := config.HostFileEntry{
		Path: ".config/seed.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce,
	}

	o := goldenOptions("/ws", t.TempDir())
	args := strings.Join(o.hostUserFileArgs(hostFileIn(t, sourceBearing, sourceLess)), " ")
	want := src + ":/ctx/host-user/" + sourceBearing.Slug() + ":ro"
	if !strings.Contains(args, want) {
		t.Errorf("hostUserFileArgs = %q, want a mount %q", args, want)
	}
	if strings.Contains(args, sourceLess.Slug()) {
		t.Errorf("source-less entry got a /ctx mount (it has no host file to cross): %q", args)
	}
}

// TestHostUserFileArgsSkipsMissingSource: podman kills the whole container on a
// missing bind source ("statfs …: no such file or directory"), and a host dotfile
// the user has not created is a normal state — so an absent source must be
// skipped, letting the surface fall back to its defaults layer.
func TestHostUserFileArgsSkipsMissingSource(t *testing.T) {
	o := goldenOptions("/ws", t.TempDir())
	in := hostFileIn(t, config.HostFileEntry{
		Path: ".config/absent.json", Source: filepath.Join(t.TempDir(), "nope.json"),
		Codec: "json", Mode: config.HostFileModeReadonly,
	})
	if got := o.hostUserFileArgs(in); got != nil {
		t.Errorf("missing source emitted %v, want no mount", got)
	}
}

// TestHostUserFileArgsDirSource: a directory entry binds its tree directly (the
// single-file deref exists for file binds only).
func TestHostUserFileArgsDirSource(t *testing.T) {
	srcDir := filepath.Join(t.TempDir(), "themes")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := config.HostFileEntry{
		Path: ".pi/agent/themes", Source: srcDir, IsDir: true, Mode: config.HostFileModeCopy,
	}
	o := goldenOptions("/ws", t.TempDir())
	args := strings.Join(o.hostUserFileArgs(hostFileIn(t, entry)), " ")
	if !strings.Contains(args, srcDir+":/ctx/host-user/"+entry.Slug()+":ro") {
		t.Errorf("dir entry args = %q, want a :ro tree mount", args)
	}
}

// TestHostFileWritableDirArgs: only a destination that needs a new writable
// subtree gets one, deduped, with nested paths dropped (a duplicate or nested -v
// for one destination is a hard podman error / a shadowed tree).
func TestHostFileWritableDirArgs(t *testing.T) {
	entries := []config.HostFileEntry{
		// Already writable via the .config bind — must NOT be staged.
		{Path: ".config/mytool/a.json", Codec: "json", HasContent: true},
		// Two entries sharing one new top-level parent — one bind.
		{Path: "foo/a.json", Codec: "json", HasContent: true},
		{Path: "foo/b.json", Codec: "json", HasContent: true},
		// A deeper path under the same root — nested, so dropped.
		{Path: "foo/deep/c.json", Codec: "json", HasContent: true},
	}
	got := hostFileWritableDirs(entries)
	if len(got) != 1 || got[0] != "foo" {
		t.Fatalf("hostFileWritableDirs = %v, want exactly [foo]", got)
	}

	o := goldenOptions("/ws", t.TempDir())
	in := hostFileIn(t, entries...)
	args := o.hostFileWritableDirArgs(in)
	if len(args) != 2 {
		t.Fatalf("writable-dir args = %v, want one -v pair", args)
	}
	if !strings.HasSuffix(args[1], ":/home/agent/foo") {
		t.Errorf("writable-dir bind = %q, want it to land at /home/agent/foo", args[1])
	}
	if !strings.Contains(args[1], filepath.Join(config.WritableHomeBackingSubdir, "foo")) {
		t.Errorf("writable-dir bind %q must be backed by the wsState writable-home subdir", args[1])
	}
}

// TestPrepareHostFilesStagesSymlinkDangling is the load-bearing one for the
// ~/.npmrc case: the GlobalHome symlink must be created RELATIVE and left
// DANGLING, because HostFileModeOnce seeds only a file it cannot stat. A
// pre-created target would suppress the seed forever.
func TestPrepareHostFilesStagesSymlinkDangling(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	entry := config.HostFileEntry{
		Path: ".npmrc", Source: "/host/.npmrc", Codec: "raw", Mode: config.HostFileModeReadonly,
	}
	prepareHostFiles(wsState, []config.HostFileEntry{entry})

	link := filepath.Join(paths.GlobalHome(), ".npmrc")
	target, err := os.Readlink(link)
	if err != nil {
		t.Fatalf("no symlink staged for a home-root destination: %v", err)
	}
	if filepath.IsAbs(target) {
		t.Errorf("symlink target %q is absolute; it must be relative so it resolves through the container mount table", target)
	}
	if target != entry.SymlinkTarget() {
		t.Errorf("symlink target = %q, want %q", target, entry.SymlinkTarget())
	}
	// DANGLING: Stat (which follows the link) must fail, or `once` never seeds.
	if _, err := os.Stat(link); !os.IsNotExist(err) {
		t.Errorf("staged symlink resolves (err=%v) — `once` would skip the seed forever", err)
	}
	// The overlay dir holding the targets must exist, so the entrypoint's write
	// through the link lands in a real directory.
	if st, err := os.Stat(filepath.Join(wsState, "config", "yolo-home")); err != nil || !st.IsDir() {
		t.Errorf("wsState config/yolo-home not created: %v", err)
	}
}

// TestPrepareHostFilesStagesWritableDirBothEnds: the writable_home_dirs recipe
// needs BOTH the backing dir (a missing bind source kills the container) and the
// GlobalHome mountpoint (deterministic mode/ownership under the :ro base).
func TestPrepareHostFilesStagesWritableDirBothEnds(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	prepareHostFiles(wsState, []config.HostFileEntry{
		{Path: "foo/bar.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	})

	backing := filepath.Join(wsState, config.WritableHomeBackingSubdir, "foo")
	if st, err := os.Stat(backing); err != nil || !st.IsDir() {
		t.Errorf("backing dir %s not created (podman would fail the container): %v", backing, err)
	}
	mountpoint := filepath.Join(paths.GlobalHome(), "foo")
	if st, err := os.Stat(mountpoint); err != nil || !st.IsDir() {
		t.Errorf("GlobalHome mountpoint %s not created: %v", mountpoint, err)
	}
}

// TestPrepareHostFilesLeavesWritableDestsAlone: a destination already under a rw
// bind must get NO staging — provisioning there would shadow a yolo mount.
func TestPrepareHostFilesLeavesWritableDestsAlone(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	wsState := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}

	prepareHostFiles(wsState, []config.HostFileEntry{
		{Path: ".config/mytool/c.json", Codec: "json", HasContent: true, Mode: config.HostFileModeOnce},
	})

	if _, err := os.Stat(filepath.Join(wsState, config.WritableHomeBackingSubdir)); !os.IsNotExist(err) {
		t.Errorf("staged a writable-home backing dir for an already-writable destination (err=%v)", err)
	}
	if entries, _ := os.ReadDir(paths.GlobalHome()); len(entries) != 0 {
		t.Errorf("touched GlobalHome for an already-writable destination: %v", entries)
	}
}

// TestHostFilesWireCarriesEveryFieldTheEntrypointNeeds: the env is the ONLY
// channel to the entrypoint, so a field dropped in transit silently changes
// behavior in the jail (a lost Mode would fall through to `copy`, discarding
// in-jail edits).
func TestHostFilesWireCarriesEveryFieldTheEntrypointNeeds(t *testing.T) {
	entry := config.HostFileEntry{
		Path:     ".config/mytool/settings.json",
		Source:   "/host/settings.json",
		Codec:    "json",
		Managed:  map[string]any{"telemetry": false},
		Defaults: map[string]any{"theme": "dark"},
		Mode:     config.HostFileModeCapture,
	}
	o := goldenOptions("/ws", t.TempDir())
	got := o.hostFilesEnv(hostFileIn(t, entry))
	wire := strings.TrimPrefix(got[1], "YOLO_HOST_FILES=")

	var raw []map[string]any
	if err := json.Unmarshal([]byte(wire), &raw); err != nil {
		t.Fatalf("wire is not decodable JSON: %v", err)
	}
	for _, key := range []string{"path", "source", "codec", "managed", "defaults", "mode"} {
		if _, ok := raw[0][key]; !ok {
			t.Errorf("wire form dropped %q: %s", key, wire)
		}
	}
	back, err := config.UnmarshalHostFiles(wire)
	if err != nil {
		t.Fatal(err)
	}
	if back[0].Mode != config.HostFileModeCapture {
		t.Errorf("Mode did not survive: %q (a lost mode falls through to copy)", back[0].Mode)
	}
	if back[0].Slug() != entry.Slug() {
		t.Errorf("slug diverged across the wire: %q vs %q — the /ctx mount would not match",
			back[0].Slug(), entry.Slug())
	}
}

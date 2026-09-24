package lingerprobe

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestExitDirCandidatesRootfulAndRootless(t *testing.T) {
	if got := ExitDirCandidates(0, envOf(nil), ""); !reflect.DeepEqual(got, []string{"/run/libpod/exits"}) {
		t.Errorf("rootful = %v", got)
	}
	got := ExitDirCandidates(1000, envOf(map[string]string{"XDG_RUNTIME_DIR": "/run/user/1000"}), "")
	want := []string{
		"/run/user/1000/libpod/tmp/exits",
		"/run/user/1000/libpod/tmp/exits",
		"/tmp/podman-run-1000/libpod/tmp/exits",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rootless = %v, want %v", got, want)
	}
	got = ExitDirCandidates(1000, envOf(nil), "/srv/podtmp")
	if got[0] != "/srv/podtmp/exits" {
		t.Errorf("a containers.conf tmp_dir must be tried first; got %v", got)
	}
}

func TestDetectExitDirPicksTheFirstThatExists(t *testing.T) {
	xdg := t.TempDir()
	env := envOf(map[string]string{"XDG_RUNTIME_DIR": xdg, "HOME": t.TempDir(), "TMPDIR": t.TempDir()})
	if _, ok := DetectExitDir(1000, env); ok {
		t.Fatal("found an exit dir where none exists: detection must answer, not guess")
	}
	want := filepath.Join(xdg, "libpod", "tmp", "exits")
	if err := os.MkdirAll(want, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, ok := DetectExitDir(1000, env); !ok || got != want {
		t.Errorf("DetectExitDir = %q, %v; want %q", got, ok, want)
	}
}

func TestConfiguredTmpDirReadsTheEngineSectionLastFileWins(t *testing.T) {
	dir := t.TempDir()
	sys := filepath.Join(dir, "sys.conf")
	user := filepath.Join(dir, "user.conf")
	if err := os.WriteFile(sys, []byte("[containers]\ntmp_dir = \"/not/engine\"\n[engine]\ntmp_dir = \"/sys/tmp\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(user, []byte("[engine]\n# tmp_dir = \"/commented\"\n  tmp_dir=\"/run/user/$UID/pod\" # trailing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ConfiguredTmpDir([]string{sys}, 1000, envOf(nil)); got != "/sys/tmp" {
		t.Errorf("sys only = %q", got)
	}
	if got := ConfiguredTmpDir([]string{sys, user, filepath.Join(dir, "missing")}, 1000, envOf(nil)); got != "/run/user/1000/pod" {
		t.Errorf("user overrides sys = %q", got)
	}
}

func TestMatchesExitFile(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	for _, tc := range []struct {
		name, id string
		want     bool
	}{
		{full, full, true},
		{full, full[:12], true},              // `podman ps -q` prints the short id
		{full + ".Z3kQ1x", full[:12], false}, // glib's temp file before the rename
		{"fedcba9876543210" + full[16:], full[:12], false},
		{full, "0123", false}, // too short to be an id at all
	} {
		if got := matchesExitFile(tc.name, tc.id); got != tc.want {
			t.Errorf("matchesExitFile(%q, %q) = %v, want %v", tc.name, tc.id, got, tc.want)
		}
	}
}

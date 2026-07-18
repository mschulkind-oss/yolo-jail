package runmount

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestScratchMountArgs(t *testing.T) {
	vol := ScratchMountArgs("volume")
	wantVol := []string{
		"-v", "/tmp", "-v", "/var/tmp", "-v", "/var/lib/containers",
		"-v", "/var/cache/containers", "--tmpfs", "/run", "--tmpfs", "/dev/shm:size=2g",
	}
	if !reflect.DeepEqual(vol, wantVol) {
		t.Errorf("volume args = %v", vol)
	}
	tmp := ScratchMountArgs("tmpfs")
	wantTmp := []string{
		"--tmpfs", "/tmp:exec,mode=1777", "--tmpfs", "/var/tmp:exec,mode=1777",
		"--tmpfs", "/var/lib/containers", "--tmpfs", "/var/cache/containers",
		"--tmpfs", "/run", "--tmpfs", "/dev/shm:size=2g",
	}
	if !reflect.DeepEqual(tmp, wantTmp) {
		t.Errorf("tmpfs args = %v", tmp)
	}
	// Unknown mode falls back to volume.
	if !reflect.DeepEqual(ScratchMountArgs("garbage"), wantVol) {
		t.Error("unknown mode should fall back to volume")
	}
	if !reflect.DeepEqual(ScratchMountArgs(""), wantVol) {
		t.Error("empty mode should fall back to volume")
	}
}

func TestBindMountTargets(t *testing.T) {
	dir := t.TempDir()
	mi := filepath.Join(dir, "mountinfo")
	// A couple of realistic mountinfo lines; field 5 (index 4) is the mount point.
	content := "36 35 98:0 /mnt1 /a/mount/point rw,noatime shared:1 - ext4 /dev/x rw\n" +
		"37 35 98:0 /mnt2 /b/other rw - ext4 /dev/y rw\n" +
		"short line\n"
	must(t, os.WriteFile(mi, []byte(content), 0o644))
	targets := bindMountTargetsFrom(mi)
	if _, ok := targets["/a/mount/point"]; !ok {
		t.Error("/a/mount/point should be a target")
	}
	if _, ok := targets["/b/other"]; !ok {
		t.Error("/b/other should be a target")
	}
	if len(targets) != 2 {
		t.Errorf("targets = %v, want 2", targets)
	}
	// Missing file -> empty.
	if len(bindMountTargetsFrom(filepath.Join(dir, "nope"))) != 0 {
		t.Error("missing mountinfo should yield empty set")
	}
}

func TestIsBindMountpoint(t *testing.T) {
	targets := map[string]struct{}{"/a/file": {}}
	if !IsBindMountpoint("/a/file", targets) {
		t.Error("/a/file should be detected")
	}
	if IsBindMountpoint("/other", targets) {
		t.Error("/other should not be detected")
	}
}

func TestROFileMountArgDirect(t *testing.T) {
	// Not a bind mountpoint -> direct mount, no copy.
	args := ROFileMountArg("/host/cfg.json", "/home/agent/cfg.json", "/ws", "cfg.json", map[string]struct{}{}, nil)
	want := []string{"-v", "/host/cfg.json:/home/agent/cfg.json:ro"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("direct = %v", args)
	}
}

func TestROFileMountArgDeref(t *testing.T) {
	ws := t.TempDir()
	host := filepath.Join(t.TempDir(), "cfg.json")
	must(t, os.WriteFile(host, []byte("data"), 0o644))
	// host is a "bind mountpoint" -> copy to ws/rel, mount that.
	targets := map[string]struct{}{host: {}}
	args := ROFileMountArg(host, "/home/agent/cfg.json", ws, "sub/cfg.json", targets, nil)
	deref := filepath.Join(ws, "sub", "cfg.json")
	want := []string{"-v", deref + ":/home/agent/cfg.json:ro"}
	if !reflect.DeepEqual(args, want) {
		t.Errorf("deref = %v, want %v", args, want)
	}
	if data, _ := os.ReadFile(deref); string(data) != "data" {
		t.Errorf("deref content = %q", data)
	}
	// Copy failure -> fall back to direct mount.
	failCopy := func(_, _ string) error { return os.ErrPermission }
	args = ROFileMountArg(host, "/c", ws, "sub2/cfg.json", targets, failCopy)
	if !reflect.DeepEqual(args, []string{"-v", host + ":/c:ro"}) {
		t.Errorf("copy-fail fallback = %v", args)
	}
}

// TestScratchParity byte-diffs ScratchMountArgs against live _scratch_mount_args.
func TestScratchParity(t *testing.T) {
	py := pythonRunner(t)
	if py == nil {
		t.Skip("python unavailable")
	}
	script := `
import sys; sys.path.insert(0, 'src')
import json
from cli.run_cmd import _scratch_mount_args
print(json.dumps({
  "volume": _scratch_mount_args("volume"),
  "tmpfs": _scratch_mount_args("tmpfs"),
  "bad": _scratch_mount_args(123),
}))
`
	out, err := py("-c", script).Output()
	if err != nil {
		t.Skipf("python run_cmd import failed: %v", err)
	}
	var want map[string][]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(ScratchMountArgs("volume"), want["volume"]) {
		t.Errorf("volume go=%v py=%v", ScratchMountArgs("volume"), want["volume"])
	}
	if !reflect.DeepEqual(ScratchMountArgs("tmpfs"), want["tmpfs"]) {
		t.Errorf("tmpfs go=%v py=%v", ScratchMountArgs("tmpfs"), want["tmpfs"])
	}
	// Python passed a non-string (123) → falls back to volume; Go models that
	// as any non-volume/tmpfs string.
	if !reflect.DeepEqual(ScratchMountArgs(""), want["bad"]) {
		t.Errorf("bad-mode go=%v py=%v", ScratchMountArgs(""), want["bad"])
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func pythonRunner(t *testing.T) func(args ...string) *exec.Cmd {
	t.Helper()
	root := repoRoot(t)
	if _, err := exec.LookPath("uv"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("uv", append([]string{"run", "python"}, args...)...)
			c.Dir = root
			return c
		}
	}
	if _, err := exec.LookPath("python3"); err == nil {
		return func(args ...string) *exec.Cmd {
			c := exec.Command("python3", args...)
			c.Dir = root
			return c
		}
	}
	return nil
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

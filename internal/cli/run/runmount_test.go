package run

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

func TestScratchMountArgs(t *testing.T) {
	const cname, id = "yolo-ws-abcd1234", "0123456789abcdef"
	vol := ScratchMountArgs("volume", cname, id)
	wantVol := []string{
		"-v", "yolo-ws-abcd1234.scratch.0123456789abcdef.tmp:/tmp",
		"-v", "yolo-ws-abcd1234.scratch.0123456789abcdef.var-tmp:/var/tmp",
		"-v", "yolo-ws-abcd1234.scratch.0123456789abcdef.var-lib-containers:/var/lib/containers",
		"-v", "yolo-ws-abcd1234.scratch.0123456789abcdef.var-cache-containers:/var/cache/containers",
		"--tmpfs", "/run", "--tmpfs", "/dev/shm:size=2g",
	}
	if !reflect.DeepEqual(vol, wantVol) {
		t.Errorf("volume args = %v", vol)
	}
	tmp := ScratchMountArgs("tmpfs", cname, id)
	wantTmp := []string{
		"--tmpfs", "/tmp:exec,mode=1777", "--tmpfs", "/var/tmp:exec,mode=1777",
		"--tmpfs", "/var/lib/containers", "--tmpfs", "/var/cache/containers",
		"--tmpfs", "/run", "--tmpfs", "/dev/shm:size=2g",
	}
	if !reflect.DeepEqual(tmp, wantTmp) {
		t.Errorf("tmpfs args = %v", tmp)
	}
	// Unknown mode falls back to volume.
	if !reflect.DeepEqual(ScratchMountArgs("garbage", cname, id), wantVol) {
		t.Error("unknown mode should fall back to volume")
	}
	if !reflect.DeepEqual(ScratchMountArgs("", cname, id), wantVol) {
		t.Error("empty mode should fall back to volume")
	}
}

// NO SCRATCH MOUNT IS ANONYMOUS. An anonymous `-v /tmp` under `podman run --rm` is
// deleted by the attached client before it exits, one unlinkat per file, with the
// terminal held: the 32 s quit this whole change exists for. A podman `-v` with a single
// path is the anonymous form, so every -v value must carry a source.
func TestScratchMountsAreNeverAnonymous(t *testing.T) {
	for _, mode := range []string{"volume", "", "garbage"} {
		args := ScratchMountArgs(mode, "yolo-ws-abcd1234", "0123456789abcdef")
		for i := 0; i < len(args); i++ {
			if args[i] != "-v" {
				continue
			}
			if !strings.Contains(args[i+1], ":") {
				t.Errorf("mode %q mounts an ANONYMOUS volume %q; --rm would delete it in the client", mode, args[i+1])
			}
		}
	}
}

// The remover is handed ScratchVolumeNames, the argv mounts ScratchMountArgs: the two
// must name the same four volumes, each one the reaper recognises, or a launch leaves
// a volume nothing ever deletes.
func TestScratchVolumeNamesAreWhatTheArgvMounts(t *testing.T) {
	const cname, id = "yolo-ws-abcd1234", "0123456789abcdef"
	names := ScratchVolumeNames("volume", cname, id)
	args := ScratchMountArgs("volume", cname, id)
	var mounted []string
	for i := 0; i < len(args); i++ {
		if args[i] == "-v" {
			mounted = append(mounted, strings.SplitN(args[i+1], ":", 2)[0])
		}
	}
	if !reflect.DeepEqual(names, mounted) {
		t.Fatalf("remover would get %v, argv mounts %v", names, mounted)
	}
	for _, n := range names {
		if c, gotID, _, ok := prune.ParseScratchVolumeName(n); !ok || c != cname || gotID != id {
			t.Errorf("%q is not a scratch volume the reaper recognises as %s's", n, cname)
		}
	}
	if got := ScratchVolumeNames("tmpfs", cname, id); len(got) != 0 {
		t.Errorf("tmpfs mode mounts no volumes, yet would remove %v", got)
	}
	if got := ScratchVolumeNames("garbage", cname, id); !reflect.DeepEqual(got, names) {
		t.Errorf("an unknown mode falls back to volumes in the argv, so the removal must too; got %v", got)
	}
}

// A fresh id per launch, and one the reaper's parse accepts.
func TestScratchLaunchIDIsFreshAndParseable(t *testing.T) {
	a, b := newScratchLaunchID(), newScratchLaunchID()
	if a == b {
		t.Fatalf("two launches got the same scratch id %q; the second would be handed the first's volumes", a)
	}
	if _, _, _, ok := prune.ParseScratchVolumeName(prune.ScratchVolumeName("yolo-x-12345678", a, "tmp")); !ok {
		t.Errorf("id %q does not parse as a scratch volume id", a)
	}
}

func TestBindMountTargets(t *testing.T) {
	dir := t.TempDir()
	mi := filepath.Join(dir, "mountinfo")
	// A couple of realistic mountinfo lines; field 5 (index 4) is the mount point.
	content := "36 35 98:0 /mnt1 /a/mount/point rw,noatime shared:1 - ext4 /dev/x rw\n" +
		"37 35 98:0 /mnt2 /b/other rw - ext4 /dev/y rw\n" +
		// The kernel's escapes: a mount point with a space in it is the path with the space.
		"38 35 98:0 /mnt3 /home/agent/My\\040Stuff/x.json ro - ext4 /dev/z rw\n" +
		"short line\n"
	must(t, os.WriteFile(mi, []byte(content), 0o644))
	targets := bindMountTargetsFrom(mi)
	if _, ok := targets["/a/mount/point"]; !ok {
		t.Error("/a/mount/point should be a target")
	}
	if _, ok := targets["/b/other"]; !ok {
		t.Error("/b/other should be a target")
	}
	if _, ok := targets["/home/agent/My Stuff/x.json"]; !ok {
		t.Errorf("/home/agent/My Stuff/x.json, spelled My\\040Stuff in the table, should be a target: %v", targets)
	}
	if len(targets) != 3 {
		t.Errorf("targets = %v, want 3", targets)
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
	failCopy := func(_, _, _ string) error { return os.ErrPermission }
	args = ROFileMountArg(host, "/c", ws, "sub2/cfg.json", targets, failCopy)
	if !reflect.DeepEqual(args, []string{"-v", host + ":/c:ro"}) {
		t.Errorf("copy-fail fallback = %v", args)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

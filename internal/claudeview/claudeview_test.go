package claudeview

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// workspace makes <tmp>/ws/.yolo/home/claude and returns the view's location.
func workspace(t *testing.T) Location {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	loc := Location{Workspace: filepath.Join(root, "ws"), Subdir: "claude"}
	if err := os.MkdirAll(filepath.Join(loc.Workspace, ".yolo", "home", "claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestSelectedDefaultsPerRuntimeAndHonorsTheDial(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == SwitchEnv {
				return v
			}
			return ""
		}
	}
	for _, tc := range []struct {
		rt, val string
		want    bool
	}{
		{"podman", "", false},
		{"podman", "1", true},
		{"podman", "yes", true},
		{"macos-user", "", false},
		{"macos-user", "1", true},
		{"container", "", false},
		{"container", "1", true},
		{"podman", "garbage", false},
		{HostRuntime, "", false},
		{HostRuntime, "1", true},
		{HostRuntime, "0", false},
	} {
		if got := Selected(tc.rt, env(tc.val)); got != tc.want {
			t.Errorf("Selected(%s, %s=%q) = %v, want %v", tc.rt, SwitchEnv, tc.val, got, tc.want)
		}
	}
}

func TestProjectCarriesNoRefreshTokenAndOnlyTheAllowlist(t *testing.T) {
	oa := jsonx.NewOrderedMap()
	oa.Set("accessToken", "at")
	oa.Set("refreshToken", "rt")
	oa.Set("refreshTokenExpiresAt", jsonx.IntValue(1))
	oa.Set("expiresAt", jsonx.IntValue(2))
	oa.Set("scopes", []any{"user:inference"})
	oa.Set("subscriptionType", "team")
	oa.Set("rateLimitTier", "t")
	oa.Set("someFutureSecret", "s")
	got := Project(oa)
	if want := []string{"accessToken", "expiresAt", "scopes", "subscriptionType", "rateLimitTier"}; strings.Join(got.Keys(), ",") != strings.Join(want, ",") {
		t.Errorf("Project keys = %v, want %v", got.Keys(), want)
	}
	if Project(jsonx.NewOrderedMap()) != nil {
		t.Error("an empty login projected to a view")
	}
}

func TestConfinedWritesRefuseASymlinkedViewPath(t *testing.T) {
	loc := workspace(t)
	outside := filepath.Join(filepath.Dir(loc.Workspace), "host-file")
	if err := os.WriteFile(outside, []byte("host"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, loc.Path()); err != nil {
		t.Fatal(err)
	}
	err := loc.Write([]byte(`{"claudeAiOauth":{}}`))
	if !errors.Is(err, ErrViewIsLink) {
		t.Fatalf("Write through a symlinked view = %v, want ErrViewIsLink", err)
	}
	if got, _ := os.ReadFile(outside); string(got) != "host" {
		t.Fatalf("the write followed the link: the host file holds %q", got)
	}
	if _, err := loc.Read(); !errors.Is(err, ErrViewIsLink) {
		t.Errorf("Read through a symlinked view = %v, want ErrViewIsLink", err)
	}
}

func TestConfinedWritesRefuseASymlinkedDirectoryAboveTheView(t *testing.T) {
	for _, level := range []string{"claude", "home", ".yolo"} {
		t.Run(level, func(t *testing.T) {
			loc := workspace(t)
			elsewhere := filepath.Join(filepath.Dir(loc.Workspace), "elsewhere")
			if err := os.MkdirAll(filepath.Join(elsewhere, "home", "claude"), 0o755); err != nil {
				t.Fatal(err)
			}
			var at, target string
			switch level {
			case "claude":
				at, target = filepath.Join(loc.Workspace, ".yolo", "home", "claude"), filepath.Join(elsewhere, "home", "claude")
			case "home":
				at, target = filepath.Join(loc.Workspace, ".yolo", "home"), filepath.Join(elsewhere, "home")
			case ".yolo":
				at, target = filepath.Join(loc.Workspace, ".yolo"), elsewhere
			}
			if err := os.RemoveAll(at); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, at); err != nil {
				t.Fatal(err)
			}
			if err := loc.Write([]byte("{}")); !errors.Is(err, ErrViewIsLink) {
				t.Fatalf("Write with %s linked = %v, want ErrViewIsLink", level, err)
			}
			if _, err := os.Stat(filepath.Join(elsewhere, "home", "claude", ViewFile)); !os.IsNotExist(err) {
				t.Fatalf("a view landed through the linked %s: %v", level, err)
			}
		})
	}
}

func TestAViewWriteIsARenameInsideItsDirectory(t *testing.T) {
	loc := workspace(t)
	if err := loc.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	before, _ := os.Stat(loc.Path())
	if err := loc.Write([]byte("two")); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(loc.Path())
	if os.SameFile(before, after) {
		t.Error("the rewrite reused the inode: a view must be replaced by rename, so a directory " +
			"bind sees it and nothing ever reads a torn file")
	}
	if after.Mode().Perm() != 0o600 {
		t.Errorf("view mode = %v, want 0600", after.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(loc.Path()))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp.") || e.Name() == StorageLockDir {
			t.Errorf("the write left %s behind", e.Name())
		}
	}
}

func TestAViewWriteTakesClaudesStorageLockAndBreaksOnlyAStaleOne(t *testing.T) {
	loc := workspace(t)
	lock := filepath.Join(filepath.Dir(loc.Path()), StorageLockDir)

	// A lock Claude holds right now: the write waits for it, then goes ahead without it.
	if err := os.Mkdir(lock, 0o755); err != nil {
		t.Fatal(err)
	}
	saved := StorageLockWait
	StorageLockWait = 200 * time.Millisecond
	t.Cleanup(func() { StorageLockWait = saved })
	start := time.Now()
	wrote, locked, err := loc.Update(func([]byte) ([]byte, error) { return []byte("x"), nil })
	if err != nil || !wrote || locked {
		t.Fatalf("Update under a held lock = wrote %v, locked %v, %v; want written without the lock", wrote, locked, err)
	}
	if time.Since(start) < 150*time.Millisecond {
		t.Error("the write did not wait for Claude's lock")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Error("the write removed a lock Claude still holds")
	}

	// A lock older than proper-lockfile's stale limit is abandoned: broken, taken, released.
	old := time.Now().Add(-time.Minute)
	if err := os.Chtimes(lock, old, old); err != nil {
		t.Fatal(err)
	}
	var sawLock bool
	wrote, locked, err = loc.Update(func([]byte) ([]byte, error) {
		fi, err := os.Stat(lock)
		sawLock = err == nil && fi.ModTime().After(old)
		return []byte("y"), nil
	})
	if err != nil || !wrote || !locked || !sawLock {
		t.Fatalf("Update over a stale lock = wrote %v, locked %v, held a fresh lock %v, %v", wrote, locked, sawLock, err)
	}
	if _, err := os.Stat(lock); !os.IsNotExist(err) {
		t.Error("the write did not release the lock it took")
	}
}

func TestRemoveLegacyLinkRemovesOnlyYolosOwnLink(t *testing.T) {
	loc := workspace(t)
	if err := os.Symlink("/etc/passwd", loc.Path()); err != nil {
		t.Fatal(err)
	}
	if removed, _ := loc.RemoveLegacyLink(); removed {
		t.Fatal("removed a link yolo did not make")
	}
	_ = os.Remove(loc.Path())
	if err := os.Symlink(LegacyLinkTarget, loc.Path()); err != nil {
		t.Fatal(err)
	}
	if removed, err := loc.RemoveLegacyLink(); !removed || err != nil {
		t.Fatalf("RemoveLegacyLink = %v, %v; want the shared_credentials link removed", removed, err)
	}
	if _, err := os.Lstat(loc.Path()); !os.IsNotExist(err) {
		t.Error("the legacy link is still there")
	}
}

func TestAMissingDirectoryIsDirGoneAndEnsureDirCreatesIt(t *testing.T) {
	loc := workspace(t)
	gone := Location{Workspace: filepath.Join(filepath.Dir(loc.Workspace), "never"), Subdir: "claude"}
	if _, err := gone.Read(); !errors.Is(err, ErrDirGone) {
		t.Fatalf("Read of a missing workspace = %v, want ErrDirGone", err)
	}
	if err := os.MkdirAll(gone.Workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := gone.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if err := gone.Write([]byte("{}")); err != nil {
		t.Errorf("Write after EnsureDir: %v", err)
	}
}

// TestAHostLocationIsADirectoryYoloManages pins the host notch's view (CL-D27): a Location with an
// absolute Dir, created 0700, read and written beneath that directory alone, refusing a link
// where it should be, and with no legacy link to remove.
func TestAHostLocationIsADirectoryYoloManages(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	loc := HostLocation("claude")
	if want := filepath.Join(home, ".local", "share", "yolo-jail", "host-agents", "claude"); loc.Dir != want {
		t.Fatalf("HostLocation(claude).Dir = %s, want %s", loc.Dir, want)
	}
	if !loc.IsHost() || !loc.Valid() || loc.Path() != filepath.Join(loc.Dir, ViewFile) {
		t.Fatalf("HostLocation = %+v (IsHost %v, Valid %v, Path %s)", loc, loc.IsHost(), loc.Valid(), loc.Path())
	}
	if _, err := loc.Read(); !errors.Is(err, ErrDirGone) {
		t.Errorf("a missing host dir reads as %v, want ErrDirGone", err)
	}
	// No hook ever ran in yolo's own directory, so there is no legacy link to look for: the
	// question costs no read, and its answer is the same before the directory exists.
	if removed, err := loc.RemoveLegacyLink(); removed || err != nil {
		t.Errorf("RemoveLegacyLink on a host view with no directory yet = %v, %v; want false, nil", removed, err)
	}
	if err := loc.EnsureDir(); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(loc.Dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the host dir is %v (%v), want 0700", fi.Mode().Perm(), err)
	}
	if err := loc.Write([]byte(`{"claudeAiOauth":{"accessToken":"at"}}`)); err != nil {
		t.Fatal(err)
	}
	if got, err := loc.Read(); err != nil || !strings.Contains(string(got), `"at"`) {
		t.Errorf("Read = %q, %v", got, err)
	}
	if removed, err := loc.RemoveLegacyLink(); removed || err != nil {
		t.Errorf("RemoveLegacyLink on a host view = %v, %v; there is no hook link there", removed, err)
	}

	// A link standing where the directory should be is refused, not written through.
	linked := Location{Dir: filepath.Join(home, "linked")}
	target := filepath.Join(home, "elsewhere")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linked.Dir); err != nil {
		t.Fatal(err)
	}
	if err := linked.EnsureDir(); !errors.Is(err, ErrViewIsLink) {
		t.Errorf("EnsureDir over a linked host dir = %v, want ErrViewIsLink: the registration fails "+
			"rather than writing through it", err)
	}
	if err := linked.Write([]byte("{}")); !errors.Is(err, ErrViewIsLink) {
		t.Errorf("a write through a linked host dir = %v, want ErrViewIsLink", err)
	}
	if entries, _ := os.ReadDir(target); len(entries) != 0 {
		t.Errorf("the link's target was written: %v", entries)
	}

	// Neither shape, or both, is not a location.
	for _, bad := range []Location{{}, {Dir: "relative"}, {Dir: "/abs", Workspace: "/ws", Subdir: "claude"}} {
		if bad.Valid() {
			t.Errorf("%+v is Valid", bad)
		}
	}
}

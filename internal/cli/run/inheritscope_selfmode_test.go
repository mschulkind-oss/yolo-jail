package run

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// inheritedRelaunchFixture drives Run through the real home-overlay installer for one macos-user
// mode, laying the sandbox home the way that mode's layout does: account mode links the account's
// ~/.config into the sidecar's `config`, self mode makes HOME the sidecar with a real `.config` in
// it (entrypoint.DeriveDarwinWorkspaceHome).
type inheritedRelaunchFixture struct {
	sidecar   string
	ws        string
	sealed    string // the staged overlay the installer delivers from
	delivered string
	// dispatched counts the launches that reached the sandbox, where the installer runs.
	dispatched int
	launch     func() (rc int, stderr string, installErr error)
}

func newInheritedRelaunchFixture(t *testing.T, mode macosuser.Mode) *inheritedRelaunchFixture {
	t.Helper()
	ws := floortest.ResolvedTemp(t)
	sidecar := paths.WorkspaceHomeState(ws)
	f := &inheritedRelaunchFixture{sidecar: sidecar, ws: ws}
	guestHome := sidecar
	if mode == macosuser.ModeSelf {
		if err := os.MkdirAll(filepath.Join(sidecar, ".config"), 0o755); err != nil {
			t.Fatal(err)
		}
		f.delivered = filepath.Join(sidecar, ".config", "yolo-jail", "config.jsonc")
	} else {
		guestHome = floortest.ResolvedTemp(t)
		if err := os.MkdirAll(filepath.Join(sidecar, "config"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(sidecar, "config"), filepath.Join(guestHome, ".config")); err != nil {
			t.Fatal(err)
		}
		f.delivered = filepath.Join(sidecar, "config", "yolo-jail", "config.jsonc")
	}
	stageRoot := floortest.ResolvedTemp(t)
	session := yoloruntime.FromWorkspace(ws)
	previousRoot := inheritedScopeStagingRoot
	inheritedScopeStagingRoot = func() string { return stageRoot }
	t.Cleanup(func() { inheritedScopeStagingRoot = previousRoot })
	sealed := macosuser.StagedHomeOverlay(session, stageRoot)
	f.sealed = sealed
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", stageRoot).Run() })
	f.launch = func() (int, string, error) {
		t.Helper()
		entries, err := config.LoadPacks(nil)
		if err != nil {
			t.Fatal(err)
		}
		var selected []*packload.Pack
		for _, entry := range entries {
			p, problems := packload.LoadDir(strings.TrimPrefix(entry.Source, "file://"), entry.Name)
			if p == nil || len(problems) != 0 {
				t.Fatalf("loading selected pack %s: %v", entry.Name, problems)
			}
			selected = append(selected, p)
		}
		var stdout, stderr bytes.Buffer
		var installErr error
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.MacosUserMode = mode
		o.MacosUserLaunchProbes = func() macosuser.LaunchProbes {
			p := setUpMacProbes()
			p.Geteuid = os.Geteuid
			p.StagingRoot = func(path string) macosuser.StagingRootFacts {
				fi, err := os.Lstat(path)
				if err != nil {
					return macosuser.StagingRootFacts{Err: err}
				}
				stat, ok := fi.Sys().(*syscall.Stat_t)
				if !ok {
					return macosuser.StagingRootFacts{Err: errors.New("missing stat uid")}
				}
				return macosuser.StagingRootFacts{Exists: true, IsDir: fi.IsDir(), IsSymlink: fi.Mode()&os.ModeSymlink != 0,
					UID: stat.Uid, Mode: fi.Mode().Perm()}
			}
			return p
		}
		o.CapturesDir = func() string { return filepath.Join(stageRoot, "captures") }
		o.BuildSlot = deliverTreeCopyWithFile(t)
		o.MacosUserRun = fakeMacosUserRun(func(c macosUserCall) int {
			f.dispatched++
			if !slices.Contains(c.overlay.Dests, inheritPreflightLegacyRel) {
				t.Fatal("fixture did not render the inherited JSONC fallback")
			}
			if !macosuser.HomeOverlayStagedCurrent(c.overlay.Tree, session, stageRoot) {
				for _, argv := range macosuser.StageHomeOverlayCommands(c.overlay.Tree, session, stageRoot) {
					if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
						t.Fatalf("synthetic overlay staging %v: %v\n%s", argv, err, out)
					}
				}
			}
			e := entrypoint.DarwinEnvFrom(map[string]string{
				"HOME": guestHome, "JAIL_HOME": guestHome,
				entrypoint.DarwinHomeSidecarEnv: sidecar,
				"YOLO_DARWIN_HOME_OVERLAY":      sealed,
			}, guestHome)
			e.Stderr = &stderr
			if installErr = entrypoint.InstallHomeOverlay(e, selected); installErr != nil {
				return 1
			}
			return 0
		})
		return Run(*o), stderr.String(), installErr
	}
	return f
}

// THE SELF-MODE RELAUNCH (MMP-D5): HOME is the sidecar and ~/.config a real `.config` in it, so
// the inherited user scope is delivered at <sidecar>/.config/yolo-jail/config.jsonc, not the
// account layout's <sidecar>/config. Judged at its own path, an unchanged prior delivery is
// admitted and kept, as account mode keeps its own.
func TestMacosUserSelfRelaunchKeepsDeliveredInheritedJSONC(t *testing.T) {
	treeLaunchHome(t, true)
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil {
		t.Fatalf("first Run() = %d, installer error %v\nstderr:\n%s", rc, err, stderr)
	}
	before := overlayFileInfo(t, f.delivered)
	if before.Mode().Perm() != 0o444 {
		t.Fatalf("first install delivered %s with mode %v, want the sealed 0444 hardlink", f.delivered, before.Mode())
	}
	rc, stderr, err := f.launch()
	if rc != 0 || err != nil {
		t.Fatalf("unchanged self-mode relaunch = %d, installer error %v\nstderr:\n%s", rc, err, stderr)
	}
	if after := overlayFileInfo(t, f.delivered); !os.SameFile(before, after) {
		t.Fatal("unchanged self-mode relaunch replaced the delivered inode")
	}
}

// THE HOST JUDGES THE PATH THE SELF-MODE HOME DELIVERS AT. A relaunch whose prior delivery was
// replaced (here by a same-bytes copy, which no longer shares the sealed source's inode) is
// refused on the host, before the sandbox starts, naming <sidecar>/.config and the `mv` that
// clears it, and leaves the file as it was. Red when the host looks at the account layout's
// <sidecar>/config instead: it found nothing there, dispatched, and left the refusal to the
// installer, the refusal TestMacosUserPackTreeProgram*/self met on its second launch.
func TestMacosUserSelfRelaunchJudgesTheDestinationWhereItsHomeKeepsIt(t *testing.T) {
	treeLaunchHome(t, true)
	f := newInheritedRelaunchFixture(t, macosuser.ModeSelf)
	if rc, stderr, err := f.launch(); rc != 0 || err != nil || f.dispatched != 1 {
		t.Fatalf("first Run() = %d, dispatched %d, installer error %v\nstderr:\n%s", rc, f.dispatched, err, stderr)
	}
	body, err := os.ReadFile(f.delivered)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(f.delivered); err != nil {
		t.Fatal(err)
	}
	// The owner's own bytes: a byte-identical copy is one the delivery record admits (issue #70).
	body = append(body, []byte("# owner edit\n")...)
	if err := os.WriteFile(f.delivered, body, 0o444); err != nil {
		t.Fatal(err)
	}
	owner := overlayFileInfo(t, f.delivered)
	rc, stderr, installErr := f.launch()
	if rc == 0 || f.dispatched != 1 {
		t.Fatalf("relaunch over a replaced delivery = %d, dispatched %d (want a host refusal before dispatch), installer error %v\nstderr:\n%s",
			rc, f.dispatched, installErr, stderr)
	}
	if want := f.delivered + " is in the way"; !strings.Contains(stderr, want) {
		t.Errorf("the refusal does not name the self-mode destination (want %q):\n%s", want, stderr)
	}
	if !strings.Contains(stderr, f.delivered+".moved-aside") {
		t.Errorf("the refusal does not name the mv that clears the self-mode destination:\n%s", stderr)
	}
	got, err := os.ReadFile(f.delivered)
	if after := overlayFileInfo(t, f.delivered); err != nil || !bytes.Equal(got, body) || !os.SameFile(owner, after) {
		t.Fatalf("the refusal changed the owner's file: read error %v", err)
	}
}

// The inherited destinations map through the same layout the bootstrap lays, per mode: account
// mode's ~/.config is a link into the sidecar's `config`, run-as-you mode's HOME is the sidecar.
func TestMacosUserInheritedStorageFollowsTheModesHomeLayout(t *testing.T) {
	ws := floortest.ResolvedTemp(t)
	for _, tc := range []struct {
		mode macosuser.Mode
		dir  string
	}{
		{macosuser.ModeAccount, "config"},
		{macosuser.ModeSelf, ".config"},
	} {
		o := &Options{Workspace: ws, MacosUserMode: tc.mode}
		for _, rel := range []string{inheritPreflightLegacyRel, inheritPreflightRel} {
			got, err := o.macosUserInheritedStorage(rel)
			want := filepath.Join(tc.dir, filepath.FromSlash(strings.TrimPrefix(rel, ".config/")))
			if err != nil || got != want {
				t.Errorf("%s: storage of %s = %q, %v; want %q", tc.mode, rel, got, err, want)
			}
		}
	}
}

// A self-mode launch that renders no inherited file removes the generated one a previous launch
// left at its own `.config`, and never touches the account layout's `config`, which this mode
// does not deliver to.
func TestMacosUserSelfStaleCleanupRemovesOnlyItsOwnDestination(t *testing.T) {
	ws := floortest.ResolvedTemp(t)
	sidecar := paths.WorkspaceHomeState(ws)
	o := &Options{Workspace: ws, MacosUserMode: macosuser.ModeSelf}
	plant := func(dir string) string {
		p := filepath.Join(sidecar, dir, "yolo-jail", "config.jsonc")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(inheritHeaderFirstLine()+"\n{}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	own, account := plant(".config"), plant("config")
	removeStaleInheritedScope(sidecar, o.macosUserInheritedStorage, nil)
	if _, err := os.Lstat(own); !os.IsNotExist(err) {
		t.Errorf("the stale generated file at the self-mode destination %s was kept (err %v)", own, err)
	}
	if _, err := os.Lstat(account); err != nil {
		t.Errorf("self-mode cleanup removed %s, which it never delivered: %v", account, err)
	}
}

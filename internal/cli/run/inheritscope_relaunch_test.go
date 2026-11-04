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

// A real first install must not turn an unchanged account relaunch into an owner-file
// refusal. This is synthetic account delivery, not native Seatbelt execution or refresh.
func TestMacosUserAccountRelaunchKeepsDeliveredInheritedJSONC(t *testing.T) {
	treeLaunchHome(t, true) // Object pack entries exercise RenderFilteredForDelivery's JSONC fallback.
	ws := floortest.ResolvedTemp(t)
	sidecar := paths.WorkspaceHomeState(ws)
	guestHome := sidecar // HOME is the workspace's own home in account mode too (MMP-D25)
	if err := os.MkdirAll(filepath.Join(sidecar, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	stageRoot := floortest.ResolvedTemp(t)
	session := yoloruntime.FromWorkspace(ws)
	previousRoot := inheritedScopeStagingRoot
	inheritedScopeStagingRoot = func() string { return stageRoot }
	t.Cleanup(func() { inheritedScopeStagingRoot = previousRoot })
	sealed := macosuser.StagedHomeOverlay(session, stageRoot)
	// Production staging seals the source before the installer hardlinks it. Keep
	// that same private source across launches; never install from writable Run staging.
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", stageRoot).Run() })
	calls := 0
	launch := func() (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
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
			calls++
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
			if err := entrypoint.InstallHomeOverlay(e, selected); err != nil {
				t.Fatalf("real installer: %v\n%s", err, stderr.String())
			}
			return 0
		})
		return Run(*o), stderr.String()
	}
	if rc, stderr := launch(); rc != 0 || calls != 1 {
		t.Fatalf("first Run() = %d, callbacks = %d\nstderr:\n%s", rc, calls, stderr)
	}
	delivered := filepath.Join(sidecar, ".config", "yolo-jail", "config.jsonc")
	before := overlayFileInfo(t, delivered)
	body, err := os.ReadFile(delivered)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != 0o444 || !os.SameFile(before, overlayFileInfo(t, filepath.Join(sealed, filepath.FromSlash(inheritPreflightLegacyRel)))) {
		t.Fatal("first install did not hardlink the sealed 0444 inherited snapshot")
	}
	rc, stderr := launch()
	after := overlayFileInfo(t, delivered)
	got, err := os.ReadFile(delivered)
	if err != nil || !bytes.Equal(body, got) || before.Mode() != after.Mode() || !os.SameFile(before, after) {
		t.Fatalf("relaunch changed delivered inode, mode or bytes: read error %v", err)
	}
	if rc != 0 || calls != 2 {
		t.Fatalf("unchanged second Run() = %d, callbacks = %d; want successful dispatch with preserved delivery\nstderr:\n%s", rc, calls, stderr)
	}
}

// lateOwnerEdit makes a late owner file's bytes its own.
const lateOwnerEdit = "# late owner edit\n"

type inheritedDeliveryProofFixture struct {
	o           *Options
	stagingRoot string
	source      string
	destination string
	sidecar     string
	probeFacts  func() macosuser.StagingRootFacts
}

func newInheritedDeliveryProofFixture(t *testing.T) inheritedDeliveryProofFixture {
	t.Helper()
	ws := floortest.ResolvedTemp(t)
	stageRoot := floortest.ResolvedTemp(t)
	previousRoot := inheritedScopeStagingRoot
	inheritedScopeStagingRoot = func() string { return stageRoot }
	t.Cleanup(func() { inheritedScopeStagingRoot = previousRoot })
	cname := yoloruntime.FromWorkspace(ws)
	source := filepath.Join(macosuser.StagedHomeOverlay(cname, stageRoot), filepath.FromSlash(inheritPreflightLegacyRel))
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(stageRoot, "home-overlay"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("sealed rendered scope\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(source, 0o444); err != nil {
		t.Fatal(err)
	}
	for dir := filepath.Dir(source); dir != filepath.Join(stageRoot, "home-overlay"); dir = filepath.Dir(dir) {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	sidecar := filepath.Join(ws, ".yolo", "home")
	destination := filepath.Join(sidecar, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(source, destination); err != nil {
		t.Fatal(err)
	}
	facts := func() macosuser.StagingRootFacts {
		fi, err := os.Lstat(stageRoot)
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
	o := &Options{Workspace: ws, MacosUserLaunchProbes: func() macosuser.LaunchProbes {
		return macosuser.LaunchProbes{Geteuid: os.Geteuid, StagingRoot: func(string) macosuser.StagingRootFacts { return facts() }}
	}}
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", stageRoot).Run() })
	return inheritedDeliveryProofFixture{o: o, stagingRoot: stageRoot, source: source, destination: destination,
		sidecar: sidecar, probeFacts: facts}
}

func TestPriorInheritedDeliveryRequiresAnUnchangedSealedHardlink(t *testing.T) {
	t.Run("same inode", func(t *testing.T) {
		f := newInheritedDeliveryProofFixture(t)
		if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); !ok {
			t.Fatal("unchanged sealed hardlink was not admitted")
		}
	})
	for _, receipt := range []struct {
		name string
		body []byte
	}{
		{name: "missing receipt"},
		{name: "corrupt receipt", body: []byte("{")},
		{name: "forged writable receipt", body: []byte(`{"destinations":[".config/yolo-jail/config.jsonc"],"digests":{".config/yolo-jail/config.jsonc":"forged"}}`)},
	} {
		t.Run("independent copied matching file with "+receipt.name, func(t *testing.T) {
			f := newInheritedDeliveryProofFixture(t)
			body, err := os.ReadFile(f.source)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(f.destination); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.destination, body, 0o444); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(f.destination, 0o444); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(f.destination)
			if err != nil {
				t.Fatal(err)
			}
			if receipt.body != nil {
				if err := os.WriteFile(filepath.Join(f.sidecar, entrypoint.HomeOverlayInstalledName), receipt.body, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); ok {
				t.Fatal("matching independent inode was admitted")
			}
			after, err := os.Lstat(f.destination)
			got, readErr := os.ReadFile(f.destination)
			if err != nil || readErr != nil || !os.SameFile(before, after) || before.Mode() != after.Mode() || !bytes.Equal(body, got) {
				t.Fatalf("refusal changed copied owner file: before %v, after %v, bytes %q, errors %v/%v", before, after, got, err, readErr)
			}
		})
	}
	t.Run("changed hardlink mode", func(t *testing.T) {
		f := newInheritedDeliveryProofFixture(t)
		if err := os.Chmod(f.destination, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); ok {
			t.Fatal("changed hardlink mode was admitted")
		}
	})
	t.Run("destination link", func(t *testing.T) {
		f := newInheritedDeliveryProofFixture(t)
		if err := os.Remove(f.destination); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(f.source, f.destination); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); ok {
			t.Fatal("destination symlink was admitted")
		}
	})
	t.Run("parent link", func(t *testing.T) {
		f := newInheritedDeliveryProofFixture(t)
		parent := filepath.Dir(f.destination)
		if err := os.Rename(parent, parent+".saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(parent+".saved", parent); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); ok {
			t.Fatal("destination parent symlink was admitted")
		}
	})
	t.Run("unsealed source directory", func(t *testing.T) {
		f := newInheritedDeliveryProofFixture(t)
		if err := os.Chmod(filepath.Dir(f.source), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); ok {
			t.Fatal("writable source directory was admitted")
		}
	})
	t.Run("foreign staging root", func(t *testing.T) {
		f := newInheritedDeliveryProofFixture(t)
		f.o.MacosUserLaunchProbes = func() macosuser.LaunchProbes {
			return macosuser.LaunchProbes{Geteuid: os.Geteuid, StagingRoot: func(string) macosuser.StagingRootFacts {
				facts := f.probeFacts()
				facts.UID++
				return facts
			}}
		}
		if _, ok := f.o.priorInheritedDelivery(f.sidecar, inheritPreflightLegacyRel); ok {
			t.Fatal("foreign staging root was admitted")
		}
	})
}

// The Run caller must reject a content-identical independent owner inode, and the real installer
// must preserve a matching independent inode that replaces the admitted hardlink after Run.
func TestMacosUserAccountRelaunchRefusesOwnerReplacementAtBothBoundaries(t *testing.T) {
	treeLaunchHome(t, true)
	ws := floortest.ResolvedTemp(t)
	sidecar := paths.WorkspaceHomeState(ws)
	guestHome := sidecar // HOME is the workspace's own home in account mode too (MMP-D25)
	if err := os.MkdirAll(filepath.Join(sidecar, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	stageRoot := floortest.ResolvedTemp(t)
	session := yoloruntime.FromWorkspace(ws)
	previousRoot := inheritedScopeStagingRoot
	inheritedScopeStagingRoot = func() string { return stageRoot }
	t.Cleanup(func() { inheritedScopeStagingRoot = previousRoot })
	sealed := macosuser.StagedHomeOverlay(session, stageRoot)
	t.Cleanup(func() { _ = exec.Command("chmod", "-R", "u+w", stageRoot).Run() })
	calls := 0
	lateReplace := false
	var lateOwner os.FileInfo
	var lateOwnerPath string
	var installErr error
	launch := func() (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
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
			calls++
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
			if lateReplace {
				lateOwnerPath = filepath.Join(sidecar, ".config", "yolo-jail", "config.jsonc")
				late := lateOwnerPath + ".late-owner"
				body, err := os.ReadFile(lateOwnerPath)
				if err != nil {
					t.Fatal(err)
				}
				// The owner's own bytes: a byte-identical file is one the delivery record admits.
				body = append(body, []byte(lateOwnerEdit)...)
				if err := os.WriteFile(late, body, 0o444); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(late, 0o444); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(late, lateOwnerPath); err != nil {
					t.Fatal(err)
				}
				lateOwner, err = os.Lstat(lateOwnerPath)
				if err != nil {
					t.Fatal(err)
				}
			}
			e.Stderr = &stderr
			installErr = entrypoint.InstallHomeOverlay(e, selected)
			if installErr != nil {
				return 1
			}
			return 0
		})
		return Run(*o), stderr.String()
	}
	if rc, stderr := launch(); rc != 0 || calls != 1 {
		t.Fatalf("first Run() = %d, callbacks = %d\nstderr:\n%s", rc, calls, stderr)
	}
	delivered := filepath.Join(sidecar, ".config", "yolo-jail", "config.jsonc")
	source := filepath.Join(sealed, filepath.FromSlash(inheritPreflightLegacyRel))
	body, err := os.ReadFile(delivered)
	if err != nil {
		t.Fatal(err)
	}
	// WITHOUT THE DELIVERY RECORD (issue #70) matching bytes prove nothing: the agent can write
	// them. With it, the same copy is admitted (TestMacosUserRelaunchAdmitsACopiedDelivery).
	if err := os.Remove(entrypoint.InheritedDeliveryRecordPath(sealed)); err != nil {
		t.Fatal(err)
	}
	copyPath := delivered + ".owner-copy"
	if err := os.WriteFile(copyPath, body, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(copyPath, 0o444); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(copyPath, delivered); err != nil {
		t.Fatal(err)
	}
	owner, err := os.Lstat(delivered)
	if err != nil {
		t.Fatal(err)
	}
	if rc, stderr := launch(); rc == 0 || calls != 1 {
		t.Fatalf("Run accepted an independent matching owner inode: rc %d callbacks %d\nstderr:\n%s", rc, calls, stderr)
	}
	got, err := os.ReadFile(delivered)
	after, statErr := os.Lstat(delivered)
	if err != nil || statErr != nil || !os.SameFile(owner, after) || owner.Mode() != after.Mode() || !bytes.Equal(body, got) {
		t.Fatalf("caller refusal changed independent owner entry: before %v, after %v, body %q, errors %v/%v", owner, after, got, err, statErr)
	}
	if err := os.Remove(delivered); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(source, delivered); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(delivered, 0o644); err != nil {
		t.Fatal(err)
	}
	changedMode, err := os.Lstat(delivered)
	if err != nil {
		t.Fatal(err)
	}
	if rc, stderr := launch(); rc == 0 || calls != 1 {
		t.Fatalf("Run accepted a changed-mode prior hardlink: rc %d callbacks %d\nstderr:\n%s", rc, calls, stderr)
	}
	modeAfter, err := os.Lstat(delivered)
	if err != nil || !os.SameFile(changedMode, modeAfter) || modeAfter.Mode().Perm() != 0o644 {
		t.Fatalf("changed-mode caller refusal altered the owner inode or mode: before %v after %v error %v", changedMode, modeAfter, err)
	}
	if err := os.Chmod(delivered, 0o444); err != nil {
		t.Fatal(err)
	}
	newline := bytes.IndexByte(body, '\n')
	if newline < 0 {
		t.Fatal("generated inherited snapshot has no header line")
	}
	edited := append(append([]byte(nil), body[:newline+1]...), []byte("owner-edited-body\n")...)
	if err := os.Chmod(delivered, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delivered, edited, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(delivered, 0o444); err != nil {
		t.Fatal(err)
	}
	editedInfo, err := os.Lstat(delivered)
	if err != nil {
		t.Fatal(err)
	}
	if rc, stderr := launch(); rc == 0 || calls != 1 {
		t.Fatalf("Run accepted a header-preserving edited snapshot: rc %d callbacks %d\nstderr:\n%s", rc, calls, stderr)
	}
	editedAfter, err := os.Lstat(delivered)
	editedBody, readErr := os.ReadFile(delivered)
	if err != nil || readErr != nil || !os.SameFile(editedInfo, editedAfter) || editedAfter.Mode().Perm() != 0o444 || !bytes.Equal(edited, editedBody) {
		t.Fatalf("header-preserving refusal changed owner snapshot: before %v after %v body %q errors %v/%v", editedInfo, editedAfter, editedBody, err, readErr)
	}
	if err := os.Chmod(delivered, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(delivered, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(delivered, 0o444); err != nil {
		t.Fatal(err)
	}
	if rc, stderr := launch(); rc != 0 || calls != 2 || installErr != nil {
		t.Fatalf("unchanged relaunch after restoring the prior hardlink = %d, callbacks = %d, installer error %v\nstderr:\n%s", rc, calls, installErr, stderr)
	}

	lateReplace = true
	if rc, stderr := launch(); rc == 0 || calls != 3 || installErr == nil {
		t.Fatalf("installer accepted a late owner replacement: rc %d callbacks %d installer error %v\nstderr:\n%s", rc, calls, installErr, stderr)
	}
	lateAfter, err := os.Lstat(lateOwnerPath)
	got, readErr = os.ReadFile(lateOwnerPath)
	if err != nil || readErr != nil || !os.SameFile(lateOwner, lateAfter) || lateOwner.Mode() != lateAfter.Mode() ||
		!bytes.Equal(append(append([]byte(nil), body...), lateOwnerEdit...), got) {
		t.Fatalf("installer refusal changed late owner entry: before %v, after %v, body %q, errors %v/%v", lateOwner, lateAfter, got, err, readErr)
	}
}

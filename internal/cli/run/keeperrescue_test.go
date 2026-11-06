package run

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/stateguard"
)

// rescueWorkspace is a workspace with a setup home holding a Claude dir with a session in it, a
// ~/.local, and the files of yolo's own the rescue skips.
func rescueWorkspace(t *testing.T) (ws, home string) {
	t.Helper()
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home = paths.SetupHomeState(ws, "podman-self")
	for _, d := range []string{"claude/projects", "local", "npm-global", "yolo-bin"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "claude", "projects", "s.jsonl"), []byte("a session"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ws, home
}

// podmanRescueArgv is a podman launch's argv in the shapes podmanBaseMounts and the pack dirs emit.
func podmanRescueArgv(ws, home, cache, hostCache string) []string {
	return []string{"podman", "run",
		"-v", ws + ":/workspace",
		"-v", "/skel:/home/agent:ro",
		"-v", home + "/npm-global:/home/agent/.npm-global",
		"-v", home + "/local:/home/agent/.local",
		"-v", home + "/yolo-bin:/home/agent/.yolo/bin",
		"-v", cache + ":/home/agent/.cache",
		"-v", hostCache + ":/home/agent/.local/share/pnpm/store",
		"-v", home + "/yolo-perf.log:/home/agent/.yolo-perf.log",
		"-v", home + "/bash_history:/home/agent/.bash_history",
		"-v", home + "/venv-shadows/.venv:/workspace/.venv",
		"-v", home + "/claude:/home/agent/.claude",
		"-v", "/global/home/.shared:/home/agent/.shared",
		"--mount", "type=tmpfs,destination=/ctx,ro=true",
		"yolo-image:tag",
		"-v", home + "/after-image:/home/agent/after",
	}
}

func TestTheRescuePlanIsTheLaunchsOwnHomeBinds(t *testing.T) {
	ws, home := "/w", "/w/.yolo/setups/podman-self/home"
	got := planRescue(podmanRescueArgv(ws, home, "/global/cache", "/host/pnpm"), "yolo-image:tag", home)
	want := []rescueItem{
		{Dest: "/home/agent/.npm-global", Source: home + "/npm-global", Rel: "npm-global"},
		// The host's pnpm store is aliased under ~/.local: never copied as this home's.
		{Dest: "/home/agent/.local", Source: home + "/local", Rel: "local", Excludes: []string{".local/share/pnpm/store"}},
		{Dest: "/home/agent/.bash_history", Source: home + "/bash_history", Rel: "bash_history"},
		{Dest: "/home/agent/.claude", Source: home + "/claude", Rel: "claude"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan:\n got %+v\nwant %+v", got, want)
	}
}

func TestAppleContainersWholeHomeIsOneItemLessItsNestedBinds(t *testing.T) {
	home := "/w/.yolo/setups/apple-container-self/home"
	argv := []string{"container", "run", "-v", "/w:/workspace", "-v", home + ":/home/agent",
		"-v", "/global/cache:/home/agent/.cache", "-v", "/mise-disk:/mise", "img"}
	got := planRescue(argv, "img", home)
	want := []rescueItem{{Dest: "/home/agent", Source: home, Rel: ".", Excludes: []string{"agent/.cache"}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("plan:\n got %+v\nwant %+v", got, want)
	}
	tarArgv := rescueTarArgv("container", "yolo-x", got[0])
	wantArgv := []string{"container", "exec", "yolo-x", "tar", "-c", "-f", "-", "-C", "/home",
		"--anchored", "--no-wildcards", "--exclude=agent/.cache", "--", "agent"}
	if !reflect.DeepEqual(tarArgv, wantArgv) {
		t.Errorf("tar argv:\n got %q\nwant %q", tarArgv, wantArgv)
	}
}

// claudeTar is a tar of ~/.claude as `tar -C /home/agent .claude` streams it.
func claudeTar(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range []*tar.Header{
		{Name: ".claude/", Typeflag: tar.TypeDir, Mode: 0o700},
		{Name: ".claude/projects/", Typeflag: tar.TypeDir, Mode: 0o700},
		{Name: ".claude/projects/s.jsonl", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len("a session"))},
	} {
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			_, _ = tw.Write([]byte("a session"))
		}
	}
	_ = tw.Close()
	return buf.Bytes()
}

// TestTheKeepersStopRescuesTheHomesBeforeItStopsTheJail is SD-D13 at the keeper: with the
// workspace's `.yolo` moved after the keeper stamped it, the stop streams each home bind out of the
// running container first, into a fresh rescue directory, then stops the container, and its log
// names the directory and the restore command.
func TestTheKeepersStopRescuesTheHomesBeforeItStopsTheJail(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, home := rescueWorkspace(t)
	cname := "yolo-rescued"
	p := &keeperPlan{Cname: cname, Runtime: "podman", Workspace: ws, Home: home, ImageRef: "yolo-image:tag",
		RunCmd: podmanRescueArgv(ws, home, t.TempDir(), t.TempDir())}
	jail := newFakeJail(t, cname)
	k := newKeeper(p, KeeperSeams{}, nil, nil, nil, nil)
	k.o.Exec = jail.exec
	k.pid = os.Getpid()
	var log lockedBuffer
	k.sink.setLog(&log)
	k.record.StateIdentity = k.stampStateIdentity()
	if k.record.StateIdentity == nil {
		t.Fatal("no stamp taken")
	}
	k.jm = &jailMain{exited: make(chan struct{})}
	close(k.jm.exited)

	// The incident: `.yolo` moved out of the workspace while the jail runs.
	if err := os.Rename(filepath.Join(ws, ".yolo"), filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var streamed []string
	stopsAtStream := -1
	old := rescueStream
	rescueStream = func(argv []string) (io.Reader, func() error, error) {
		mu.Lock()
		defer mu.Unlock()
		streamed = append(streamed, argv[len(argv)-1])
		stopsAtStream = max(stopsAtStream, jail.stopCount())
		if argv[len(argv)-1] == ".claude" {
			return bytes.NewReader(claudeTar(t)), func() error { return nil }, nil
		}
		var empty bytes.Buffer
		_ = tar.NewWriter(&empty).Close()
		return &empty, func() error { return nil }, nil
	}
	t.Cleanup(func() { rescueStream = old })

	k.endJail(keeperSignalledReason(k.pid), true)

	if stopsAtStream != 0 {
		t.Errorf("the rescue streamed after %d stop(s) of the jail; it must run while the jail still holds the binds", stopsAtStream)
	}
	if jail.stopCount() != 1 {
		t.Errorf("the jail was stopped %d times", jail.stopCount())
	}
	if want := []string{".npm-global", ".local", ".bash_history", ".claude"}; !slices.Equal(streamed, want) {
		t.Errorf("streamed %v, want %v", streamed, want)
	}
	listed := stateguard.List(ws)
	if len(listed) != 1 {
		t.Fatalf("rescues: %+v\nlog:\n%s", listed, log.String())
	}
	got, err := os.ReadFile(filepath.Join(listed[0].Dir, "home", "claude", "projects", "s.jsonl"))
	if err != nil || string(got) != "a session" {
		t.Fatalf("the rescued session: %q, %v", got, err)
	}
	if want := stateguard.StopLine(ws, listed[0].Dir, home); !strings.Contains(log.String(), want) {
		t.Errorf("the stop did not print\n%s\nlog:\n%s", want, log.String())
	}
	if listed[0].Restore != stateguard.RestoreCommand(listed[0].Dir, home) {
		t.Errorf("the record names restore %q", listed[0].Restore)
	}
}

// TestAStopWithTheStateInPlaceCopiesNothing: the ordinary stop streams nothing and makes no
// rescue directory.
func TestAStopWithTheStateInPlaceCopiesNothing(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, home := rescueWorkspace(t)
	p := &keeperPlan{Cname: "yolo-unmoved", Runtime: "podman", Workspace: ws, Home: home, ImageRef: "yolo-image:tag",
		RunCmd: podmanRescueArgv(ws, home, t.TempDir(), t.TempDir())}
	jail := newFakeJail(t, p.Cname)
	k := newKeeper(p, KeeperSeams{}, nil, nil, nil, nil)
	k.o.Exec = jail.exec
	k.pid = os.Getpid()
	k.record.StateIdentity = k.stampStateIdentity()
	k.jm = &jailMain{exited: make(chan struct{})}
	close(k.jm.exited)
	old := rescueStream
	rescueStream = func(argv []string) (io.Reader, func() error, error) {
		t.Errorf("an unmoved workspace streamed %v", argv)
		return bytes.NewReader(nil), func() error { return nil }, nil
	}
	t.Cleanup(func() { rescueStream = old })
	k.endJail(keeperSignalledReason(k.pid), true)
	if _, err := os.Lstat(filepath.Join(ws, ".yolo", "rescue")); err == nil {
		t.Error("an unmoved workspace got a rescue directory")
	}
}

// TestAFailedStreamIsRecordedAndTheRestIsKept: one bind the runtime cannot stream does not lose the
// others.
func TestAFailedStreamIsRecordedAndTheRestIsKept(t *testing.T) {
	ws, home := rescueWorkspace(t)
	items := planRescue(podmanRescueArgv(ws, home, "/c", "/h"), "yolo-image:tag", home)
	old := rescueStream
	rescueStream = func(argv []string) (io.Reader, func() error, error) {
		if argv[len(argv)-1] == ".claude" {
			return bytes.NewReader(claudeTar(t)), func() error { return nil }, nil
		}
		return bytes.NewReader(nil), func() error { return io.ErrUnexpectedEOF }, nil
	}
	t.Cleanup(func() { rescueStream = old })
	dir, rec, err := runRescue(ws, home, "podman", "yolo-x", items, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(rec.Copied, []string{"claude"}) || len(rec.Failed) != 3 {
		t.Errorf("copied %v, failed %v", rec.Copied, rec.Failed)
	}
	if _, err := os.Stat(filepath.Join(dir, "home", "claude", "projects", "s.jsonl")); err != nil {
		t.Error(err)
	}
}

// TestAnArrivalIsWarnedOfAMovedState is SD-D5's attach half: the warning is printed from the
// running jail's record, and nothing when its state is in place.
func TestAnArrivalIsWarnedOfAMovedState(t *testing.T) {
	ws, home := rescueWorkspace(t)
	rec := keeperRecord{PID: 1, StateIdentity: stateguard.Take(ws, home, []string{home})}
	o := goldenOptions(ws, t.TempDir())
	var out bytes.Buffer
	o.Stderr = &out
	o.warnStateMoved(rec)
	if out.Len() != 0 {
		t.Fatalf("an unmoved workspace warned:\n%s", out.String())
	}
	if err := os.Rename(filepath.Join(ws, ".yolo"), filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	o.warnStateMoved(rec)
	if !strings.Contains(out.String(), "still intact inside the running jail") {
		t.Errorf("the arrival's warning:\n%s", out.String())
	}
	out.Reset()
	rec.Notch = keeperNotchMacosUser
	o.warnStateMoved(rec)
	if !strings.Contains(out.String(), "moved with it") {
		t.Errorf("the macos-user arrival's warning:\n%s", out.String())
	}
}

// TestCheckFindsOnlyALiveKeepersMovedState is the finding `yolo check` reads: a moved state of a
// jail whose keeper holds it, and nothing for one whose keeper is gone.
func TestCheckFindsOnlyALiveKeepersMovedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, home := rescueWorkspace(t)
	cname := yoloruntime.FromWorkspace(ws)
	if err := writeKeeperRecord(cname, keeperRecord{PID: os.Getpid(), Workspace: ws,
		StateIdentity: stateguard.Take(ws, home, []string{home})}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(ws, ".yolo"), filepath.Join(t.TempDir(), "moved")); err != nil {
		t.Fatal(err)
	}
	if got := StateMovedFindings(ws); len(got) != 0 {
		t.Errorf("a jail whose keeper is gone was reported: %v", got)
	}
	live, err := holdLivenessLock(cname)
	if err != nil {
		t.Fatal(err)
	}
	defer releaseLock(live)
	got := StateMovedFindings(ws)
	if len(got) == 0 || !strings.Contains(got[0], filepath.Join(ws, ".yolo")) {
		t.Errorf("findings: %v", got)
	}
}

// TestCheckFindsASetupJailsMovedState: a jail keyed by its setup-qualified name
// (runtime.JailName, named-setups-plan.md SUP-D2) is found too, by its keeper's record, after the
// `.yolo` that held its setup's directory has moved; another workspace's keeper is not.
func TestCheckFindsASetupJailsMovedState(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ws, home := rescueWorkspace(t)
	other, otherHome := rescueWorkspace(t)
	for _, k := range []struct{ cname, ws, home string }{
		{yoloruntime.JailName(ws, "podman-self"), ws, home},
		{keeperKey(yoloruntime.JailName(ws, "seatbelt-self"), keeperNotchMacosUser), ws, home},
		{yoloruntime.JailName(other, "podman-self"), other, otherHome},
	} {
		if err := writeKeeperRecord(k.cname, keeperRecord{PID: os.Getpid(), Workspace: k.ws,
			StateIdentity: stateguard.Take(k.ws, k.home, []string{k.home})}); err != nil {
			t.Fatal(err)
		}
		live, err := holdLivenessLock(k.cname)
		if err != nil {
			t.Fatal(err)
		}
		defer releaseLock(live)
	}
	for _, w := range []string{ws, other} {
		if err := os.Rename(filepath.Join(w, ".yolo"), filepath.Join(t.TempDir(), "moved")); err != nil {
			t.Fatal(err)
		}
	}
	got := StateMovedFindings(ws)
	n := 0
	for _, l := range got {
		if l == got[0] {
			n++
		}
		if strings.Contains(l, other) {
			t.Errorf("another workspace's jail was reported: %q", l)
		}
	}
	if n != 2 || !strings.Contains(got[0], filepath.Join(ws, ".yolo")) {
		t.Errorf("want one warning per setup jail (2), got %d in %v", n, got)
	}
}

package run

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// TestARefusedFreshLaunchLeavesNoHostServicesDir drives Run() down the podman fresh-launch
// path to a refusal that lands after the image is ready and before any host service starts,
// and asks whether the per-jail host-services dir exists afterwards.
func TestARefusedFreshLaunchLeavesNoHostServicesDir(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `[]`)
	ws := t.TempDir()
	cname := runtime.FromWorkspace(ws)

	// THE FAULT: the home skeleton's root is a regular file, so buildHomeSkeleton refuses the
	// launch. It is a filesystem fault rather than a config one on purpose — no pack, profile
	// or provider rule can move it out from under this test.
	if err := os.MkdirAll(filepath.Join(paths.AgentsDir(), cname), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.HomeSkeletonRoot(cname), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	// And a SAFETY NET: nothing named podman is reachable, so a launch that stopped refusing
	// here could not start a real container. It would fail as "runtime not found" instead,
	// which the refusal assertion below tells apart.
	t.Setenv("PATH", t.TempDir())

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	repo, _ := o.RepoRoot()
	o.PathExists = func(p string) bool {
		return p == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "info" {
			return ExecResult{Ran: true, RC: 0, Stdout: "host: {}"}
		}
		// Every other runtime question answers "nothing here": no container of this name.
		return ExecResult{Ran: true, RC: 0}
	}
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult {
		return image.LoadResult{OK: true, Ref: goldenImageRef}
	}

	dir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if fileExists(dir) {
		t.Fatalf("fixture is not clean: %s already exists", dir)
	}

	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1 (the skeleton refusal)\nstdout:\n%s\nstderr:\n%s",
			rc, stdout.String(), stderr.String())
	}
	if !strings.Contains(stdout.String(), "home skeleton") {
		t.Fatalf("the launch did not refuse at the skeleton, so this test says nothing about "+
			"the refusal path:\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}
	if fileExists(dir) {
		t.Errorf("a launch refused before any host service started left %s behind. Only "+
			"startLoopholesMatching may create it: every path out of that call reaches "+
			"stopLoopholes, and no path out of an earlier creation does", dir)
	}
}

// TestTeardownRemovesTheHostServicesDirOnlyOnAnAnswer is the tri-state rule at the teardown:
// stopLoopholes deletes the per-jail dir only when the runtime ANSWERED that no container of
// this name EXISTS, running or not. "Could not ask" is not that answer, and neither is "none
// running": a relaunch whose container is still `created` has dropped its workspace lock once
// onStarted's poll gave up, runs no container yet, and has live endpoint files in this dir.
//
// The fake answers the running-only listing and the all-states listing separately, the way
// a real runtime does, so a teardown that asked the narrower question fails the created and
// stopped cases. The endpoint file is there to show what the removal would destroy: a live
// jail's credential.
func TestTeardownRemovesTheHostServicesDirOnlyOnAnAnswer(t *testing.T) {
	// Apple Container's listing, as `container ls` prints it: a header, then one row per
	// container, the name first. `ls` lists running ones, `ls --all` every one.
	const acHeader = "ID  IMAGE  OS  ARCH  STATE\n"
	const acOther = acHeader + "yolo-ws-other  img  linux  arm64  running\n"
	const acMine = acHeader + "yolo-ws-tristate  img  linux  arm64  running\n"
	const acMineStopped = acHeader + "yolo-ws-tristate  img  linux  arm64  stopped\n"
	none := ExecResult{Ran: true, RC: 0}
	cases := []struct {
		name     string
		rt       string
		running  ExecResult // `ps -q` / `container ls`
		all      ExecResult // `ps -a -q` / `container ls --all`
		wantGone bool
		wantSaid string
	}{
		{"the runtime answered: no such container", "podman", none, none, true, ""},
		{"the runtime answered: still running", "podman",
			ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"}, ExecResult{Ran: true, RC: 0, Stdout: "abc123\n"},
			false, "still exists"},
		{"the runtime answered: created, not yet running (a relaunch)", "podman",
			none, ExecResult{Ran: true, RC: 0, Stdout: "cid-created\n"}, false, "still exists"},
		{"the runtime could not be run", "podman", ExecResult{Ran: false}, ExecResult{Ran: false},
			false, "could not ask"},
		{"the runtime failed", "podman", ExecResult{Ran: true, RC: 125, Stderr: "cannot connect"},
			ExecResult{Ran: true, RC: 125, Stderr: "cannot connect"}, false, "could not ask"},
		{"the runtime timed out", "podman", ExecResult{Ran: true, RC: -1, Timeout: true},
			ExecResult{Ran: true, RC: -1, Timeout: true}, false, "could not ask"},
		{"Apple Container answered: another jail only", "container",
			ExecResult{Ran: true, RC: 0, Stdout: acOther}, ExecResult{Ran: true, RC: 0, Stdout: acOther},
			true, ""},
		{"Apple Container answered: still running", "container",
			ExecResult{Ran: true, RC: 0, Stdout: acMine}, ExecResult{Ran: true, RC: 0, Stdout: acMine},
			false, "still exists"},
		{"Apple Container answered: present but not running", "container",
			ExecResult{Ran: true, RC: 0, Stdout: acHeader}, ExecResult{Ran: true, RC: 0, Stdout: acMineStopped},
			false, "still exists"},
		{"Apple Container could not be run", "container", ExecResult{Ran: false}, ExecResult{Ran: false},
			false, "could not ask"},
		{"Apple Container failed", "container", ExecResult{Ran: true, RC: 1}, ExecResult{Ran: true, RC: 1},
			false, "could not ask"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			socketsDir := filepath.Join(t.TempDir(), hostServicesDirPrefix+"0badcafe")
			if err := os.MkdirAll(socketsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			endpoint := filepath.Join(socketsDir, "svc"+paths.ServiceEndpointExt)
			if err := os.WriteFile(endpoint, []byte("live"), 0o600); err != nil {
				t.Fatal(err)
			}
			var buf strings.Builder
			o := &Options{Stdout: &buf}
			fillDefaults(o)
			asked := false
			o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
				listing := len(argv) >= 2 && (argv[1] == "ps" || (argv[0] == "container" && argv[1] == "ls"))
				if !listing {
					return ExecResult{}
				}
				asked = true
				for _, a := range argv[2:] {
					if a == "-a" || a == "--all" {
						return c.all
					}
				}
				return c.running
			}

			o.stopLoopholes(nil, socketsDir, "yolo-ws-tristate", c.rt)

			if !asked {
				t.Fatal("stopLoopholes never asked the runtime; this case says nothing")
			}
			if gone := !fileExists(socketsDir); gone != c.wantGone {
				t.Errorf("host-services dir gone = %v, want %v\noutput: %s", gone, c.wantGone, buf.String())
			}
			if c.wantSaid != "" && !strings.Contains(buf.String(), c.wantSaid) {
				t.Errorf("the teardown kept the dir without saying %q:\n%s", c.wantSaid, buf.String())
			}
		})
	}
}

// TestTeardownKeepsTheUpstreamSocketsWhenAContainerMayExist: a fronted daemon's host-only
// upstream socket is keyed by the jail's hash (frontSocketFile), which a relaunch of the
// workspace reuses. So on either early return — a container of this name still exists, or
// the runtime could not be asked — the socket may be a live relaunch's and stays; only the
// known-gone answer retires it.
func TestTeardownKeepsTheUpstreamSocketsWhenAContainerMayExist(t *testing.T) {
	cases := []struct {
		name     string
		ps       ExecResult
		wantGone bool
	}{
		{"known gone", ExecResult{Ran: true, RC: 0}, true},
		{"still exists", ExecResult{Ran: true, RC: 0, Stdout: "cid\n"}, false},
		{"could not ask", ExecResult{Ran: false}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			// A hash no real jail has, so the /tmp file this plants is this test's alone.
			hash := sha1Hex8(t.Name())
			socketsDir := filepath.Join(t.TempDir(), hostServicesDirPrefix+hash)
			if err := os.MkdirAll(socketsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			upstream := frontSocketFile(hash, "svc")
			if err := os.WriteFile(upstream, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(upstream) })
			o := &Options{Stdout: discardBuf()}
			fillDefaults(o)
			o.Exec = func([]string, string, []string, time.Duration) ExecResult { return c.ps }

			o.stopLoopholes(nil, socketsDir, "yolo-ws-upstream", "podman")

			if gone := !fileExists(upstream); gone != c.wantGone {
				t.Errorf("upstream socket %s gone = %v, want %v", upstream, gone, c.wantGone)
			}
		})
	}
}

// TestHostServicesDirsAreIsolatedHere: this package's tests start host services under
// container names they choose, and some never tear down. TestMain's
// testsupport.IsolateHostSingletons is what keeps those dirs off the machine-wide /tmp —
// it moves paths.HostSingletonDir, and the per-jail dir is built in it. Before that, the
// tests here that never tear down left an empty /tmp/yolo-host-services-<8hex> behind on
// every run.
func TestHostServicesDirsAreIsolatedHere(t *testing.T) {
	dir := hostServiceSocketsDir("yolo-ws-isolated", false)
	if filepath.Dir(dir) != paths.HostSingletonDir || paths.HostSingletonDir == paths.DefaultHostSingletonDir {
		t.Fatalf("host-services dir %s is not in this package's private singleton dir %s",
			dir, paths.HostSingletonDir)
	}
}

// TestOnlyTheSpawnCreatesTheHostServicesDir: the per-jail dir has ONE creator,
// startLoopholesMatching, because every path out of that call reaches stopLoopholes. A
// creation anywhere else has no teardown on the paths between it and the spawn — which is
// the leak runContainer had, on both arms of its broker gate.
func TestOnlyTheSpawnCreatesTheHostServicesDir(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	creators := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		for _, decl := range f.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && callsIn(fn)["mkdirHostServicesDir"] {
				creators[fn.Name.Name] = true
			}
		}
	}
	if !creators["startLoopholesMatching"] {
		t.Error("startLoopholesMatching no longer creates the host-services dir, so a launch " +
			"whose broker is off binds a directory that does not exist")
	}
	delete(creators, "startLoopholesMatching")
	for name := range creators {
		t.Errorf("%s creates the host-services dir. Only startLoopholesMatching may: a "+
			"refusal between another creation and the spawn leaves the dir behind", name)
	}
}

// reapFixture plants owner-pid records and populated host-services dirs for the named
// jails, and returns a podman fake: `ps -a --format` lists every jail as running (the reap's
// enumeration), `stop` records the name, and the per-name probes answer through probe, which
// is told whether the question was the all-states one (`-a`) and whether the name was stopped.
func reapFixture(t *testing.T, owners map[string]string,
	probe func(name string, all, stopped bool) ExecResult) (*Options, map[string]string, map[string]bool) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(ownerPIDDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	dirs := map[string]string{}
	var listing strings.Builder
	for c, pid := range owners {
		if err := os.WriteFile(ownerPIDFile(c), []byte(pid+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		d := hostServiceSocketsDir(c, false)
		dirs[c] = d
		t.Cleanup(func() { _ = os.RemoveAll(d) })
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "svc"+paths.ServiceEndpointExt), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		listing.WriteString(c + " running\n")
	}

	stopped := map[string]bool{}
	o := &Options{Stdout: discardBuf()}
	fillDefaults(o)
	o.PIDAlive = func(pid int) bool { return pid == 4242 }
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		has := func(flag string) bool {
			for _, a := range argv {
				if a == flag {
					return true
				}
			}
			return false
		}
		switch {
		case len(argv) > 1 && argv[1] == "ps" && has("--format"):
			return ExecResult{Ran: true, RC: 0, Stdout: listing.String()}
		case len(argv) > 1 && argv[1] == "stop":
			stopped[argv[len(argv)-1]] = true
			return ExecResult{Ran: true, RC: 0}
		case len(argv) > 1 && argv[1] == "ps" && has("-q"):
			name := strings.TrimSuffix(strings.TrimPrefix(argv[len(argv)-1], "name=^/"), "$")
			return probe(name, has("-a"), stopped[name])
		}
		return ExecResult{Ran: true, RC: 0}
	}
	return o, dirs, stopped
}

// TestReapingAnOrphanRemovesItsHostServicesDir: a launch killed without its teardown
// (SIGKILL, OOM) leaves its jail running and its host-services dir behind, with endpoint
// files naming fronts that died with it. The next launch on the machine reaps that jail
// (reapOrphanedJails), and the dir goes with it through the same guarded teardown. A jail
// whose owner is alive keeps both.
func TestReapingAnOrphanRemovesItsHostServicesDir(t *testing.T) {
	const orphan, live = "yolo-ws-orphan00", "yolo-ws-live0000"
	o, dirs, stopped := reapFixture(t, map[string]string{orphan: "999999", live: "4242"},
		func(name string, _, stopped bool) ExecResult {
			if stopped {
				return ExecResult{Ran: true, RC: 0}
			}
			return ExecResult{Ran: true, RC: 0, Stdout: "cid-" + name + "\n"}
		})

	o.reapOrphanedJails("podman")

	if !stopped[orphan] || stopped[live] {
		t.Fatalf("fixture wrong: stopped = %v, want only %s", stopped, orphan)
	}
	if fileExists(dirs[orphan]) {
		t.Errorf("the reaped orphan's host-services dir %s survived; nothing else will ever "+
			"remove it until that workspace launches again", dirs[orphan])
	}
	if !fileExists(dirs[live]) {
		t.Errorf("a jail whose owner is alive lost its host-services dir %s", dirs[live])
	}
}

// TestReapingAnOrphanKeepsTheDirOfARelaunch is why the reap goes through stopLoopholes
// rather than deleting the dir itself (HSD-3 in docs/reference/jail-home.md). While one
// launch reaps workspace X's orphan, the user may be relaunching X, and that relaunch
// publishes its endpoint files into the SAME dir. Its lock, a container that exists but is
// not yet running, or a runtime that cannot be asked each keep the dir; a bare rmtree in
// the reap would delete the relaunch's live endpoints in all three.
func TestReapingAnOrphanKeepsTheDirOfARelaunch(t *testing.T) {
	const orphan = "yolo-ws-relaunch"
	cases := []struct {
		name     string
		holdLock bool
		probe    func(all, stopped bool) ExecResult
	}{
		{"the relaunch holds the workspace lock", true,
			func(_, stopped bool) ExecResult { return ExecResult{Ran: true, RC: 0} }},
		{"the relaunch's container is created, not yet running", false,
			func(all, _ bool) ExecResult {
				if all {
					return ExecResult{Ran: true, RC: 0, Stdout: "cid-created\n"}
				}
				return ExecResult{Ran: true, RC: 0}
			}},
		{"the runtime cannot be asked after the stop", false,
			func(_, _ bool) ExecResult { return ExecResult{Ran: false} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o, dirs, stopped := reapFixture(t, map[string]string{orphan: "999999"},
				func(_ string, all, stopped bool) ExecResult { return c.probe(all, stopped) })
			if c.holdLock {
				lock, ok := tryWorkspaceLock(orphan)
				if !ok {
					t.Fatal("could not take the fixture's workspace lock")
				}
				t.Cleanup(lock.Close)
			}

			o.reapOrphanedJails("podman")

			if !stopped[orphan] {
				t.Fatalf("fixture wrong: the orphan was not reaped (stopped = %v)", stopped)
			}
			if !fileExists(dirs[orphan]) {
				t.Errorf("the reap removed %s although a relaunch of that workspace may be "+
					"publishing into it", dirs[orphan])
			}
		})
	}
}

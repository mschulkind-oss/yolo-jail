package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// peerOptions is a launch of setupName in ws that finds no runtime on PATH, so only a keeper lock or a
// session record can say a peer runs.
func peerOptions(ws, setupName string, stderr io.Writer) *Options {
	o := &Options{Workspace: ws, Setup: setup.MustBuiltIn(setupName), Stderr: stderr}
	o.Now = time.Now
	o.LookPath = func(string) (string, bool) { return "", false }
	o.Exec = func([]string, string, []string, time.Duration) ExecResult { return ExecResult{} }
	return o
}

// holdKeeperKey holds key's liveness lock as a running keeper does, until the test ends.
func holdKeeperKey(t *testing.T, key string) {
	t.Helper()
	f, err := holdLivenessLock(key)
	if err != nil {
		t.Fatalf("hold %s: %v", key, err)
	}
	t.Cleanup(func() { releaseLock(f) })
}

func makePeerSetup(t *testing.T, ws, name string) {
	t.Helper()
	if err := os.MkdirAll(paths.SetupStateDir(ws, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

// The class of a peer is the class of the witness that answered, never a setup definition that may
// since have been removed (UH §7.2).
func TestPeerClassComesFromTheWitness(t *testing.T) {
	for _, tc := range []struct {
		name string
		hold func(t *testing.T, cname string, o *Options)
		want peerClass
	}{
		{"a guest key", func(t *testing.T, cname string, _ *Options) {
			holdKeeperKey(t, keeperKey(cname, keeperNotchGuest))
		}, peerClassGuest},
		{"a macos-user key", func(t *testing.T, cname string, _ *Options) {
			holdKeeperKey(t, keeperKey(cname, keeperNotchMacosUser))
		}, peerClassGuest},
		{"a container keeper", func(t *testing.T, cname string, _ *Options) { holdKeeperKey(t, cname) }, peerClassContainer},
		{"a runtime's answer", func(_ *testing.T, cname string, o *Options) {
			answerContainerOf(o, cname, "podman", "c0ffee\n", 0)
		}, peerClassContainer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			// The peer's setup is not defined anywhere: its name is only a directory.
			const peer = "gone-setup"
			makePeerSetup(t, ws, peer)
			o := peerOptions(ws, "podman-self", io.Discard)
			cname := yoloruntime.JailName(ws, peer)
			tc.hold(t, cname, o)
			got := o.jailStatus(cname)
			if got.Answer != projectJailsRunning || got.Class != tc.want {
				t.Errorf("jailStatus = %+v, want running of class %v", got, tc.want)
			}
		})
	}
}

// TestJailStatusSaysWhichRuntimeCouldNotBeAsked: only a runtime that could not be asked, with every
// keeper lock and session record saying not running, is RuntimeOnly (OQ-UH3).
func TestJailStatusSaysWhichRuntimeCouldNotBeAsked(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	o := peerOptions(ws, "podman-self", io.Discard)
	o.LookPath = func(name string) (string, bool) { return "/usr/bin/" + name, name == "podman" }
	got := o.jailStatus(yoloruntime.JailName(ws, "x"))
	if got.Answer != projectJailsUnknown || !got.RuntimeOnly || len(got.Unasked) != 1 || got.Unasked[0] != "podman" {
		t.Errorf("jailStatus = %+v, want unknown, RuntimeOnly, podman unasked", got)
	}
	if _, how := o.jailRuns(yoloruntime.JailName(ws, "x")); how != "" {
		t.Errorf("jailRuns' how = %q for an unanswered runtime", how)
	}
}

// TestExclusionRule is §7.1's table through the pure decision (the facts a fake jailStatus would give).
func TestExclusionRule(t *testing.T) {
	running := func(c peerClass) jailAnswer { return jailAnswer{Answer: projectJailsRunning, Class: c} }
	stopped := jailAnswer{Answer: projectJailsStopped}
	podmanUnasked := jailAnswer{Answer: projectJailsUnknown, RuntimeOnly: true, Unasked: []string{"podman"}}
	keeperUnknown := jailAnswer{Answer: projectJailsUnknown, KeeperUnknown: true, Unasked: []string{"podman"}}
	podman, apple := setup.MustBuiltIn("podman-self"), setup.MustBuiltIn("apple-container-self")
	seatbelt, account := setup.MustBuiltIn("seatbelt-self"), setup.MustBuiltIn("seatbelt-yolo-account")
	const refuseRunning = "Refusing to launch: `podman-self` is running for this workspace and shares its home with " +
		"`apple-container-self`. Stop it with `yolo stop --setup podman-self`, then launch again."
	for _, tc := range []struct {
		name       string
		self       setup.Setup
		peer       string
		status     jailAnswer
		wantRefuse string
		wantNote   string
	}{
		{"container beside a container", apple, "podman-self", running(peerClassContainer), refuseRunning, ""},
		{"container beside a guest", podman, "seatbelt-self", running(peerClassGuest), "", ""},
		{"guest beside a container", seatbelt, "podman-self", running(peerClassContainer), "", ""},
		{"guest beside a guest", account, "seatbelt-self", running(peerClassGuest),
			"Refusing to launch: `seatbelt-self` is running for this workspace and shares its home with " +
				"`seatbelt-yolo-account`. Stop it with `yolo stop --setup seatbelt-self`, then launch again.", ""},
		{"a peer that is stopped", apple, "podman-self", stopped, "", ""},
		{"a keeper that cannot be read", apple, "podman-self", keeperUnknown,
			"Refusing to launch: could not tell whether `podman-self` is running for this workspace. Run " +
				"`yolo stop --setup podman-self`, then launch again.", ""},
		{"a runtime that cannot be asked, keepers say not running", apple, "podman-self", podmanUnasked, "",
			"Could not ask Podman whether `podman-self` is running for this workspace; its keeper and sessions say " +
				"it is not, so this launch goes ahead."},
	} {
		t.Run(tc.name, func(t *testing.T) {
			v := decidePeers(tc.self, []peerFact{{Name: tc.peer, Status: tc.status}})
			if v.Refusal != tc.wantRefuse {
				t.Errorf("refusal = %q\nwant      %q", v.Refusal, tc.wantRefuse)
			}
			if note := strings.Join(v.Notes, "\n"); note != tc.wantNote {
				t.Errorf("note = %q\nwant   %q", note, tc.wantNote)
			}
		})
	}
	t.Run("an unknown refusal names the runtime and its listing command", func(t *testing.T) {
		v := decidePeers(apple, []peerFact{{Name: "podman-self", Status: jailAnswer{
			Answer: projectJailsUnknown, Unasked: []string{"podman"}}}})
		want := "Refusing to launch: could not ask Podman whether `podman-self` is running for this workspace. " +
			"Run `podman ps -a` to see, then launch again."
		if v.Refusal != want {
			t.Errorf("refusal = %q\nwant      %q", v.Refusal, want)
		}
	})
	t.Run("a running peer outranks an unknown one", func(t *testing.T) {
		v := decidePeers(apple, []peerFact{
			{Name: "a-setup", Status: keeperUnknown},
			{Name: "podman-self", Status: running(peerClassContainer)},
		})
		if v.Refusal != refuseRunning {
			t.Errorf("refusal = %q", v.Refusal)
		}
	})
}

// TestExclusionRuleReadsRealWitnesses drives refuseContainerPeer end to end over held keeper locks.
func TestExclusionRuleReadsRealWitnesses(t *testing.T) {
	for _, tc := range []struct {
		name       string
		notch      string
		wantRefuse bool
	}{
		{"a container keeper refuses", "", true},
		{"a guest keeper allows", keeperNotchGuest, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			makePeerSetup(t, ws, "apple-container-self")
			holdKeeperKey(t, keeperKey(yoloruntime.JailName(ws, "apple-container-self"), tc.notch))
			var stderr strings.Builder
			o := peerOptions(ws, "podman-self", &stderr)
			if got := o.refuseContainerPeer(); got != tc.wantRefuse {
				t.Fatalf("refuseContainerPeer = %v, want %v\n%s", got, tc.wantRefuse, stderr.String())
			}
			if tc.wantRefuse && !strings.Contains(stderr.String(), "`yolo stop --setup apple-container-self`") {
				t.Errorf("the refusal does not name the stop:\n%s", stderr.String())
			}
		})
	}
	t.Run("the launch's own setup is no peer", func(t *testing.T) {
		packHome(t)
		ws := t.TempDir()
		makePeerSetup(t, ws, "podman-self")
		holdKeeperKey(t, yoloruntime.JailName(ws, "podman-self"))
		if peerOptions(ws, "podman-self", io.Discard).refuseContainerPeer() {
			t.Error("a running jail of the same setup refused its own launch")
		}
	})
	t.Run("a fresh-home launch and a dry run are not asked", func(t *testing.T) {
		packHome(t)
		ws := t.TempDir()
		makePeerSetup(t, ws, "apple-container-self")
		holdKeeperKey(t, yoloruntime.JailName(ws, "apple-container-self"))
		o := peerOptions(ws, "podman-self", io.Discard)
		o.FreshHome = true
		if o.refuseContainerPeer() {
			t.Error("--fresh-home shares nothing and must not be refused")
		}
		o = peerOptions(ws, "podman-self", io.Discard)
		o.DryRun = true
		if o.refuseContainerPeer() {
			t.Error("a dry run writes nothing and must not be refused")
		}
	})
	t.Run("a guest launch is slice 2's", func(t *testing.T) {
		packHome(t)
		ws := t.TempDir()
		makePeerSetup(t, ws, "podman-self")
		holdKeeperKey(t, yoloruntime.JailName(ws, "podman-self"))
		if peerOptions(ws, "seatbelt-self", io.Discard).refuseContainerPeer() {
			t.Error("a guest launch's behavior changed in slice 1")
		}
	})
}

// TestEveryContainerLaunchTakesTheProjectLock (UHP-D4): selectHome holds the project lock for a
// container setup whether or not a legacy home exists, never for macos-user without one, and a
// fresh-home launch (which never reaches selectHome) holds none.
func TestEveryContainerLaunchTakesTheProjectLock(t *testing.T) {
	for _, tc := range []struct {
		setup string
		want  bool
	}{
		{"podman-self", true},
		{"apple-container-self", true},
		{"seatbelt-yolo-account", false},
	} {
		t.Run(tc.setup, func(t *testing.T) {
			packHome(t)
			ws := t.TempDir()
			o := peerOptions(ws, tc.setup, io.Discard)
			defer o.releaseHomeLock()
			o.selectHome()
			held := o.homeLock != nil && !o.homeLock.isClosed()
			if held != tc.want {
				t.Fatalf("project lock held = %v, want %v", held, tc.want)
			}
			if held && o.homeLock.path != launchLockPath(yoloruntime.FromWorkspace(ws)) {
				t.Errorf("the lock is %q, not the project's launch lock", o.homeLock.path)
			}
		})
	}
	t.Run("a fresh-home launch", func(t *testing.T) {
		packHome(t)
		ws := t.TempDir()
		o := peerOptions(ws, "podman-self", io.Discard)
		o.FreshHome = true
		defer o.releaseLaunchLock()
		if !o.selectFreshHome() {
			t.Fatal("selectFreshHome refused")
		}
		if o.homeLock != nil {
			t.Error("a fresh-home launch took the project lock")
		}
	})
}

// funcCalls returns the positions of each call name in the named function of file.
func funcCalls(t *testing.T, file, fn string) map[string][]token.Pos {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]token.Pos{}
	found := false
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != fn {
			continue
		}
		found = true
		ast.Inspect(fd, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				switch fun := call.Fun.(type) {
				case *ast.SelectorExpr:
					out[fun.Sel.Name] = append(out[fun.Sel.Name], call.Pos())
				case *ast.Ident:
					out[fun.Name] = append(out[fun.Name], call.Pos())
				}
			}
			return true
		})
	}
	if !found {
		t.Fatalf("%s no longer declares %s", file, fn)
	}
	return out
}

// TestTheKeeperIsRegisteredBeforeTheProjectLockIsReleased pins the race the rule rests on (UH §7.2):
// launch X passes under the project lock and its keeper takes the jail's liveness lock BEFORE it says
// ready, and X releases the project lock only after ready, so launch Y, which then takes the project
// lock, sees X's keeper flock. Two call orders: the keeper takes its liveness lock before it waits for
// ready, and runContainer releases the project lock only after the keeper's ready relay returns.
func TestTheKeeperIsRegisteredBeforeTheProjectLockIsReleased(t *testing.T) {
	k := funcCalls(t, "keeper.go", "run")
	live, ready := k["awaitLivenessLock"], k["beforeReady"]
	if len(live) == 0 || len(ready) == 0 || live[0] > ready[0] {
		t.Errorf("the keeper must take its liveness lock (awaitLivenessLock) before it can say ready (beforeReady)")
	}
	r := funcCalls(t, "run.go", "runContainer")
	relay, release := r["relay"], r["releaseHomeLock"]
	if len(relay) == 0 || len(release) == 0 {
		t.Fatalf("runContainer no longer relays the keeper's ready and releases the project lock")
	}
	for _, p := range release {
		if p < relay[0] {
			t.Errorf("runContainer releases the project lock before the keeper's ready relay returns")
		}
	}
}

// TestCommitHomeChecksTheExclusionFirst: commitHome's FIRST statement is the exclusion check, above
// the homeMovePending early return, so a launch with no legacy home is checked too.
func TestCommitHomeChecksTheExclusionFirst(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "legacyhome.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, decl := range f.Decls {
		fd, ok := decl.(*ast.FuncDecl)
		if !ok || fd.Name.Name != "commitHome" {
			continue
		}
		first, ok := fd.Body.List[0].(*ast.IfStmt)
		if !ok {
			t.Fatalf("commitHome's first statement is not the exclusion check")
		}
		call, ok := first.Cond.(*ast.CallExpr)
		if !ok {
			t.Fatalf("commitHome's first condition is not a call")
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "refuseContainerPeer" {
			t.Errorf("commitHome's first statement must call refuseContainerPeer")
		}
		return
	}
	t.Fatal("commitHome is gone")
}

// TestCommitHomeRefusesAContainerPeerBeforeAnyWrite: with a container peer's keeper held, commitHome
// returns false and the workspace tree is what it was, byte for byte.
func TestCommitHomeRefusesAContainerPeerBeforeAnyWrite(t *testing.T) {
	packHome(t)
	ws := t.TempDir()
	makePeerSetup(t, ws, "apple-container-self")
	seedLegacyHome(t, ws)
	holdKeeperKey(t, yoloruntime.JailName(ws, "apple-container-self"))
	before := treeListing(t, ws)
	o := peerOptions(ws, "podman-self", io.Discard)
	// No move is pending: a launch with no legacy home is checked too.
	if o.commitHome() {
		t.Fatal("commitHome committed beside a running container peer")
	}
	if after := treeListing(t, ws); after != before {
		t.Errorf("the workspace changed:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := os.Lstat(paths.SetupStateDir(ws, "podman-self")); !os.IsNotExist(err) {
		t.Errorf(".yolo/setups/podman-self exists after a refusal (err %v)", err)
	}
}

func treeListing(t *testing.T, root string) string {
	t.Helper()
	var b strings.Builder
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil {
			rel, _ := filepath.Rel(root, p)
			b.WriteString(rel + "\n")
		}
		return nil
	})
	return b.String()
}

package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// brokeredFixture records one brokered pack loophole whose "daemon" copies the scope file
// it is handed to marker and exits, so the test can read back exactly what the launch
// handed it; and a workspace whose .git/config names a GitHub remote.
func brokeredFixture(t *testing.T) (o *Options, buf *strings.Builder, marker string) {
	t.Helper()
	isolatePackModules(t)
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	marker = filepath.Join(t.TempDir(), "handed")
	mod := filepath.Join(t.TempDir(), "gb")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "gb", "default_enabled": true, "transport": "loopback-tls",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/cp", "{repository_scope}", "` + marker + `"], "publishes": "socket"},
	  "brokered": {"source": "gbsrc", "remote_host": "github.com"}}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: mod, HostExecApproved: true}})

	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ws, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, ".git", "config"),
		[]byte("[remote \"origin\"]\n\turl = git@github.com:o/r.git\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf = &strings.Builder{}
	o = &Options{}
	fillDefaults(o)
	o.Workspace, o.Stdout, o.Stderr = ws, buf, buf
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsMacOS = false
	return o, buf, marker
}

// The gate reads the workspace's remotes when a brokered loophole will start, puts the
// labeled scope block in front of the reader, and a launch with no terminal refuses.
func TestTheGateReadsTheRemotesOfABrokerThatWillStart(t *testing.T) {
	o, buf, _ := brokeredFixture(t)
	if o.checkConfigChanges(jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), "podman") {
		t.Fatal("a first launch with a GitHub remote and no terminal must refuse")
	}
	out := buf.String()
	for _, want := range []string{"repository scope", "gb repository scope, read from",
		`+ o/r  remote "origin"  added`, "--accept-config-changes", filepath.Join(o.Workspace, ".git", "config")} {
		if !strings.Contains(out, want) {
			t.Errorf("the refusal does not show %q:\n%s", want, out)
		}
	}
	if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatal("a refused launch recorded the scope part")
	}
}

// Apple Container starts no broker, so it reads nothing and asks nothing (§5.6).
func TestTheGateAsksNothingWhereNoBrokerStarts(t *testing.T) {
	o, _, _ := brokeredFixture(t)
	if !o.checkConfigChanges(jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), "container") {
		t.Fatal("an Apple Container launch must not be asked about a broker it does not start")
	}
	if _, err := os.Stat(config.ApprovalScopePath(o.Workspace)); !os.IsNotExist(err) {
		t.Fatal("a launch that starts no broker gained a scope part")
	}
}

// The whole launch-side chain: the gate reads the remotes and records the approval, the
// spawn writes this launch's scope file and hands its path to the daemon through
// {repository_scope}, and the file goes with the daemon.
func TestAFreshLaunchHandsTheApprovedScopeToTheBroker(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	o.AcceptConfigChanges = true
	if !o.checkConfigChanges(jsonx.NewOrderedMap(), jsonx.NewOrderedMap(), "podman") {
		t.Fatalf("an accepted gate refused:\n%s", buf.String())
	}
	if got := config.ApprovedScope(o.Workspace, "gbsrc"); len(got) != 1 || got[0] != "o/r" {
		t.Fatalf("approved scope %v", got)
	}

	handles := o.startLoopholes("yolo-brokered-test", "podman", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir("yolo-brokered-test", false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the daemon was never handed a scope file (%v); output:\n%s", err, buf.String())
	}
	var f brokerscope.File
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Repos) != 1 || f.Repos[0] != "o/r" || f.Workspace != o.Workspace || f.PID != os.Getpid() ||
		f.Container != "yolo-brokered-test" || f.Source != "gbsrc" {
		t.Fatalf("scope file %+v", f)
	}
	if !strings.Contains(buf.String(), "gb: scope for this workspace: o/r") {
		t.Errorf("the launch did not disclose the scope:\n%s", buf.String())
	}
	// The fixture daemon exits instead of serving, so its start fails — and the scope file
	// written for it goes too, rather than outliving a daemon that never ran.
	entries, _ := os.ReadDir(filepath.Dir(paths.BrokerScopeFile("gbsrc", "x")))
	if len(entries) != 0 {
		t.Fatalf("a scope file outlived its daemon: %v", entries)
	}
}

// A launch whose gate recorded no scope hands the daemon an EMPTY one and says so.
func TestASpawnWithNoApprovedScopeFailsClosed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fixture daemon is /bin/cp")
	}
	o, buf, marker := brokeredFixture(t)
	handles := o.startLoopholes("yolo-brokered-test2", "podman", jsonx.NewOrderedMap())
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir("yolo-brokered-test2", false), "", "") })
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("no scope file handed: %v\n%s", err, buf.String())
	}
	var f brokerscope.File
	_ = json.Unmarshal(data, &f)
	if len(f.Repos) != 0 || !strings.Contains(buf.String(), "no repository scope was approved") {
		t.Fatalf("scope %+v, output:\n%s", f, buf.String())
	}
}

// {repository_scope} with no file written is a refusal to start, never a literal token.
func TestTheScopeTokenWithNoFileRefusesTheSpawn(t *testing.T) {
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	spec := jsonx.NewOrderedMap()
	spec.Set("command", []any{"/bin/true", "--scope-file", "{repository_scope}"})
	if _, ok := o.resolveDaemonArgv("gb", spec, "/tmp/x.sock"); ok {
		t.Fatal("a daemon named its scope file and started without one")
	}
	o.scopeFiles = map[string]string{"gb": "/s/f.json"}
	argv, ok := o.resolveDaemonArgv("gb", spec, "/tmp/x.sock")
	if !ok || argv[len(argv)-1] != "/s/f.json" {
		t.Fatalf("argv %v", argv)
	}
}

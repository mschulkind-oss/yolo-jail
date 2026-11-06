package run

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const launchInputChildEnv = "YOLO_TEST_LAUNCH_INPUT_CHILD"

// TestLaunchInputFixtureDaemon is the real selected-daemon subprocess used by the caller test.
func TestLaunchInputFixtureDaemon(t *testing.T) {
	if os.Getenv(launchInputChildEnv) != "1" {
		return
	}
	args := os.Args
	sep := -1
	for i, arg := range args {
		if arg == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || len(args) != sep+4 {
		t.Fatal("fixture daemon expected -- <input> <socket> <marker>")
	}
	inputPath, socketPath, marker := args[sep+1], args[sep+2], args[sep+3]
	if os.Getenv("YOLO_TEST_LAUNCH_INPUT_WRAPPER") == "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestLaunchInputFixtureDaemon$", "--", inputPath, socketPath, marker)
		cmd.Env = append(os.Environ(), "YOLO_TEST_LAUNCH_INPUT_WRAPPER=child")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer cmd.Process.Release()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if conn, err := net.DialTimeout("unix", socketPath, 50*time.Millisecond); err == nil {
				_ = conn.Close()
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("fixture wrapper child never became reachable")
	}
	if os.Getenv("YOLO_TEST_LAUNCH_INPUT_DETACH") == "1" {
		term := make(chan os.Signal, 1)
		signal.Notify(term, syscall.SIGTERM)
		defer signal.Stop(term)
		if _, err := os.ReadFile(inputPath); err != nil {
			t.Fatal(err)
		}
		restart := os.Getenv("YOLO_TEST_LAUNCH_INPUT_RESTART") == "1"
		stopping := false
		if restart {
			if _, err := os.Stat(marker + ".first"); os.IsNotExist(err) {
				listener, err := net.Listen("unix", socketPath)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
				if err := os.WriteFile(marker+".first", nil, 0o600); err != nil {
					t.Fatal(err)
				}
				for {
					if _, err := os.Stat(marker + ".crash"); err == nil {
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
			if err := os.WriteFile(marker+".restarted", nil, 0o600); err != nil {
				t.Fatal(err)
			}
			// Deliberately remain in the restart readiness wait through teardown.
			for !stopping {
				select {
				case <-term:
					stopping = true
				default:
				}
				if _, err := os.Stat(marker + ".ready"); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
		}
		if !stopping {
			listener, err := net.Listen("unix", socketPath)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			<-term
		}
		body, err := os.ReadFile(inputPath)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(marker+".shutdown", body, 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(marker + ".release"); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return
	}
	body, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(fmt.Sprintf("%o\n%s\n%s", st.Mode().Perm(), inputPath, body)), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_ = conn.Close()
	}
}

func TestSelectedDaemonReceivesItsGateFrozenLaunchInput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a Unix host daemon")
	}
	redirectState(t)
	const name = "launch input fixture"
	marker := filepath.Join(t.TempDir(), "daemon.marker")
	manifest := fmt.Sprintf(`{"name":%q,"description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":[%q,"-test.run=^TestLaunchInputFixtureDaemon$","--","{launch_input}","{socket}",%q],"publishes":"socket","launch_input":"fixture-approved-scope"}}`, name, os.Args[0], marker)
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
	if got := set.Enabled(); len(got) != 1 || got[0].Name != name {
		t.Fatalf("selected fixture daemon missing: %+v", got)
	}
	if got := set.Enabled()[0].HostDaemon.LaunchInput; got != "fixture-approved-scope" {
		t.Fatalf("loaded daemon launch_input = %q", got)
	}

	workspace := t.TempDir()
	stateDir := filepath.Join(t.TempDir(), "private state;'$(false)")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	argv := assembledPrivateFixtureMounts(t, workspace, "yolo-launch-input-test")
	// The launcher assembles the full mount argv and freezes its effective/user-only
	// tables in the keeper plan; the keeper, not a test-only Options field, consumes it.
	user := newConfig()
	userServers, err := jsonx.Decode([]byte(`{"linear":{"url":"https://user.example/mcp"}}`))
	if err != nil {
		t.Fatal(err)
	}
	user.Set("mcp_servers", userServers)
	effective := newConfig()
	effectiveServers, err := jsonx.Decode([]byte(`{"linear":{"url":"https://user.example/mcp","requires_env":["X"]},"private":{"url":"https://workspace.example/mcp"}}`))
	if err != nil {
		t.Fatal(err)
	}
	effective.Set("mcp_servers", effectiveServers)
	selectedPack := &packload.Pack{Name: "declared-user-pack", Decl: &packdecl.Manifest{Contributes: []packdecl.Contribution{
		{Kind: packdecl.KindMCP, Name: "packed", Raw: json.RawMessage(`{"url":"https://pack.example/mcp"}`)},
	}}}
	frozen := frozenMCPForLaunch(effective, user, []*packload.Pack{selectedPack}, []*packload.Pack{selectedPack})
	plan := &keeperPlan{Workspace: workspace, Cname: "yolo-launch-input-test", Runtime: "podman", RunCmd: argv, LaunchInputMCP: &frozen,
		ApprovedScopes: map[string][]config.ScopeRepo{"scope-source": {{Repo: "approved/repo"}}}}
	encoded, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	var transferred keeperPlan
	if err := json.Unmarshal(encoded, &transferred); err != nil {
		t.Fatal(err)
	}
	// A later config edit cannot change the request the keeper receives.
	user.Set("mcp_servers", nil)
	effective.Set("mcp_servers", nil)
	selectedPack.Decl.Contributes[0].Raw = json.RawMessage(`{"url":"https://changed.example/mcp"}`)
	k := newKeeper(&transferred, KeeperSeams{}, nil, nil, nil, nil)
	o := k.o
	o.launchInputProducers = map[string]LaunchInputProducer{
		"fixture-approved-scope": {StateDir: func(LaunchInputContext) string { return stateDir }, Produce: func(in LaunchInputContext) ([]byte, error) {
			if !in.MCPProven || in.MCPScopes["linear"].Level != "user" || in.MCPScopes["private"].Level != "workspace" ||
				in.MCPScopes["packed"].Level != "user" ||
				in.EffectiveMCPServers == nil || in.UserOnlyMCPServers == nil {
				return nil, fmt.Errorf("missing frozen MCP composition")
			}
			return []byte("approved-input-bytes"), nil
		}},
	}
	o.Stdout, o.Stderr = &output, &output
	o.ServiceReadyTimeout = 2 * time.Second
	o.ServiceTermGrace = time.Second
	o.PathExists = func(string) bool { return false }
	o.recordApprovedScopes(map[string][]config.ScopeRepo{"scope-source": {{Repo: "approved/repo"}}}, nil, nil)
	t.Setenv(launchInputChildEnv, "1")
	cname := "yolo-launch-input-test"
	// These are selected host daemons, not just standalone predicate calls.
	// The complete launcher argv is transferred through the keeper plan before
	// the selected consumer is allowed to invoke Produce.
	for _, tc := range []struct{ name, source string }{
		{"parent", stateDir},
		{"alias", func() string {
			alias := filepath.Join(t.TempDir(), "state-alias")
			if err := os.Symlink(stateDir, alias); err != nil {
				t.Fatal(err)
			}
			return alias
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refused := newKeeper(&keeperPlan{Workspace: workspace, Cname: cname, Runtime: "podman",
				RunCmd:         append(append([]string(nil), argv...), "--volume", tc.source+":/shadow:ro"),
				LaunchInputMCP: &frozen}, KeeperSeams{}, nil, nil, nil, nil).o
			calls := 0
			refused.launchInputProducers = map[string]LaunchInputProducer{"fixture-approved-scope": {
				StateDir: func(LaunchInputContext) string { return stateDir },
				Produce:  func(LaunchInputContext) ([]byte, error) { calls++; return []byte("secret"), nil },
			}}
			refused.Stdout, refused.Stderr = &output, &output
			refused.PathExists = func(string) bool { return false }
			if handles := refused.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true }); len(handles) != 0 || calls != 0 {
				t.Fatalf("selected mounted input reached producer or daemon: handles=%d calls=%d", len(handles), calls)
			}
		})
	}
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(socketsDir) })
	handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true })
	if len(handles) != 1 {
		t.Fatalf("selected daemon did not start: handles=%d output=%s", len(handles), output.String())
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			o.stopLoopholes(handles, socketsDir, cname, "podman")
		}
	})

	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("selected daemon did not receive the producer's file: %v\n%s", err, output.String())
	}
	parts := strings.SplitN(string(data), "\n", 3)
	if len(parts) != 3 || parts[0] != "600" || parts[2] != "approved-input-bytes" {
		t.Fatalf("daemon input marker = %q; want mode 600 and exact producer bytes", data)
	}
	inputPath := parts[1]
	if strings.HasPrefix(inputPath, socketsDir+string(os.PathSeparator)) {
		t.Fatalf("producer input is exposed by the jail's services mount: %s", inputPath)
	}
	if _, err := os.Stat(inputPath); err != nil {
		t.Fatalf("daemon input path was not live while daemon ran: %v", err)
	}
	if got := o.launchInputFiles[name]; got != inputPath {
		t.Fatalf("tracked input %q, daemon received %q", got, inputPath)
	}
	if err := o.checkSpawnLaunchInputMounts(append([]string(nil), argv...)); err != nil {
		t.Fatalf("the exact assembled spawn topology should still be private: %v", err)
	}
	if err := o.checkSpawnLaunchInputMounts(append(append([]string(nil), argv...), "--volume", stateDir+":/late:ro")); err == nil {
		t.Fatal("a mount inserted after the selected producer was not refused before spawn")
	}
	// Exercise the keeper's actual pre-spawn guard, not just a standalone
	// map-copy helper. Cleanup owns this same mutex after the daemon exits.
	launchInputMu.Lock()
	guardDone := make(chan error, 1)
	go func() { guardDone <- o.checkSpawnLaunchInputMounts(argv) }()
	select {
	case err := <-guardDone:
		launchInputMu.Unlock()
		t.Fatalf("pre-spawn guard read input ownership without its mutex: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	launchInputMu.Unlock()
	if err := <-guardDone; err != nil {
		t.Fatalf("guard refused unchanged topology: %v", err)
	}
	stopGuard := make(chan struct{})
	go func() {
		defer close(stopGuard)
		for i := 0; i < 200; i++ {
			_ = o.checkSpawnLaunchInputMounts(argv)
		}
	}()
	o.stopLoopholes(handles, socketsDir, cname, "podman")
	<-stopGuard
	stopped = true
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(inputPath); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("input file survived observed daemon teardown")
		}
		time.Sleep(10 * time.Millisecond)
	}
	launchInputMu.Lock()
	remaining := len(o.launchInputFiles)
	launchInputMu.Unlock()
	if remaining != 0 {
		t.Fatalf("launch input ownership was not cleared: %d", remaining)
	}
	alias := filepath.Join(t.TempDir(), "mounted-state-alias")
	if err := os.Symlink(stateDir, alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	o.launchInputMountArgv = append(o.launchInputMountArgv, "-v", alias+":/visible:ro")
	if got := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true }); len(got) != 0 {
		t.Fatalf("jail-visible alias spawned daemon: %d", len(got))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("jail-visible alias executed daemon: %v", err)
	}
	if _, err := os.Stat(inputPath); !os.IsNotExist(err) {
		t.Fatalf("jail-visible alias published input: %v", err)
	}
}

func TestLaunchInputDirectArgvIsPathData(t *testing.T) {
	path := "/private home/a 'b;$(false).json"
	o := &Options{launchInputFiles: map[string]string{"fixture": path}}
	resolved, ok := o.launchInputTokenArg("fixture", "--share={launch_input}")
	if !ok || resolved != "--share="+path {
		t.Fatalf("direct argv lost path data: %q, %v", resolved, ok)
	}
}

func TestUnknownLaunchInputProducerDoesNotStartDaemon(t *testing.T) {
	redirectState(t)
	const name = "launch-input-unknown"
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	manifestBytes, err := json.Marshal(map[string]any{
		"name": name, "description": "synthetic", "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": map[string]any{
			"cmd":       []string{"/bin/sh", "-c", fmt.Sprintf("touch %q; exec sleep 60", marker), "daemon", "{launch_input}", "{socket}"},
			"publishes": "socket", "launch_input": "not-registered",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	emptyMarker := filepath.Join(t.TempDir(), "empty-must-not-exist")
	emptyName := "launch-input-empty"
	emptyManifest, err := json.Marshal(map[string]any{
		"name": emptyName, "description": "synthetic", "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": map[string]any{
			"cmd":       []string{"/bin/sh", "-c", fmt.Sprintf("touch %q; exec sleep 60", emptyMarker), "daemon", "{launch_input}", "{socket}"},
			"publishes": "socket", "launch_input": "empty-fixture-input",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	emptyModule := filepath.Join(t.TempDir(), emptyName)
	if err := os.MkdirAll(emptyModule, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(emptyModule, "manifest.jsonc"), emptyManifest, 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{
		{Dir: module, HostExecApproved: true}, {Dir: emptyModule, HostExecApproved: true},
	}})
	var output strings.Builder
	o := &Options{Workspace: t.TempDir(), launchInputProducers: map[string]LaunchInputProducer{
		"empty-fixture-input": {Produce: func(LaunchInputContext) ([]byte, error) { return nil, nil }, StateDir: func(LaunchInputContext) string { return t.TempDir() }},
	}}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	o.launchInputMountArgv = []string{"podman", "run", "-v", o.Workspace + ":/workspace"}
	cname := "yolo-launch-input-unknown-" + strconv.Itoa(os.Getpid())
	handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true })
	if len(handles) != 0 {
		t.Fatalf("unknown producer unexpectedly started a daemon: %d", len(handles))
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("unknown producer executed daemon: %v", err)
	}
	if _, err := os.Stat(emptyMarker); !os.IsNotExist(err) {
		t.Fatalf("empty producer executed daemon: %v", err)
	}
	if !strings.Contains(output.String(), "unknown launch input producer") || !strings.Contains(output.String(), "not started") || !strings.Contains(output.String(), "produced an empty launch input") {
		t.Fatalf("unknown producer refusal lacks actionable explanation: %s", output.String())
	}
}

// TestDetachedLaunchInputOutlivesSignalUntilObservedExit exercises the real
// selected-daemon spawn, not a stand-in stop callback.
//
// The fixture's shell EXECS the daemon. A detached stop signals the bare pid
// (detachService), and Debian's and Ubuntu's dash do not replace `sh -c` with
// its last command, so without the exec the SIGTERM killed the shell, the
// daemon behind it never saw it, and the stop read the shell's death as the
// daemon exiting unsuccessfully. Every wait below ends on an event: the
// daemon's marker, or the stop returning because the daemon exited.
func TestDetachedLaunchInputOutlivesSignalUntilObservedExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix daemon")
	}
	redirectState(t)
	const name = "detached input fixture"
	state := filepath.Join(t.TempDir(), "private state ' $home;printf INJECTED")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	marker := filepath.Join(t.TempDir(), "shutdown.marker")
	command := fmt.Sprintf("exec %q -test.run=^TestLaunchInputFixtureDaemon$ -- \"$1\" \"$2\" %q", os.Args[0], marker)
	manifest := fmt.Sprintf(`{"name":%q,"description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["/bin/sh","-c",%q,"--","{launch_input}","{socket}"],"publishes":"socket","detach":true,"launch_input":"fixture-approved-scope"}}`, name, command)
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
	var output strings.Builder
	o := &Options{Workspace: workspace, launchInputProducers: map[string]LaunchInputProducer{
		"fixture-approved-scope": {Produce: func(LaunchInputContext) ([]byte, error) { return []byte("shutdown-secret"), nil }, StateDir: func(LaunchInputContext) string { return state }},
	}}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	o.ServiceReadyTimeout = time.Minute // readiness returns when the socket accepts; this bounds only a failure
	o.PathExists = func(string) bool { return false }
	cname := "yolo-detached-launch-input"
	sockets := hostServiceSocketsDir(cname, false)
	o.launchInputMountArgv = assembledPrivateFixtureMounts(t, workspace, cname)
	t.Setenv(launchInputChildEnv, "1")
	t.Setenv("YOLO_TEST_LAUNCH_INPUT_DETACH", "1")
	t.Cleanup(func() { _ = os.WriteFile(marker+".release", nil, 0o600); _ = os.RemoveAll(sockets) })
	handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true })
	if len(handles) != 1 {
		t.Fatalf("daemon failed: %s", output.String())
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			o.stopLoopholes(handles, sockets, cname, "podman")
		}
	})
	input := o.launchInputFiles[name]
	// The stop waits for the daemon's exit, and the daemon exits only once this
	// test releases it, so the stop runs beside the test. Its return is the
	// event that says the daemon is gone.
	stopped = true
	stopDone := make(chan bool)
	go func() { stopDone <- o.stopLoopholes(handles, sockets, cname, "podman") }()
	for shutdownSeen := false; !shutdownSeen; {
		select {
		case failed := <-stopDone:
			t.Fatalf("the daemon exited (stop failed=%v) without reading its input during shutdown: %s", failed, output.String())
		case <-time.After(10 * time.Millisecond):
			_, err := os.Stat(marker + ".shutdown")
			shutdownSeen = err == nil
		}
	}
	if bytes, err := os.ReadFile(marker + ".shutdown"); err != nil || string(bytes) != "shutdown-secret" {
		t.Fatalf("shutdown read: %q %v", bytes, err)
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("input removed before observed exit: %v", err)
	}
	if err := os.WriteFile(marker+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if failed := <-stopDone; failed {
		t.Fatalf("the released daemon's exit was not confirmed as its final harvest: %s", output.String())
	}
	// Removal follows the observed exit on a goroutine of its own, so it is
	// awaited, not bounded: a removal that never comes fails at the suite's timeout.
	for {
		if _, err := os.Stat(input); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestRestartedDetachedLaunchInputWaitsForCurrentProcessExit covers teardown
// while the restarted process is in its readiness wait, after the first exited.
func TestRestartedDetachedLaunchInputWaitsForCurrentProcessExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix daemon")
	}
	redirectState(t)
	const name = "restart input fixture"
	state := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	marker := filepath.Join(t.TempDir(), "restart.marker")
	manifest := fmt.Sprintf(`{"name":%q,"description":"synthetic","transport":"none","lifecycle":"spawned","host_daemon":{"cmd":[%q,"-test.run=^TestLaunchInputFixtureDaemon$","--","{launch_input}","{socket}",%q],"publishes":"socket","detach":true,"launch_input":"fixture-approved-scope"}}`, name, os.Args[0], marker)
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
	var output strings.Builder
	o := &Options{Workspace: workspace, launchInputProducers: map[string]LaunchInputProducer{
		"fixture-approved-scope": {Produce: func(LaunchInputContext) ([]byte, error) { return []byte("restart-secret"), nil }, StateDir: func(LaunchInputContext) string { return state }},
	}}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	// Far longer than the test: supervision must leave its restart-readiness wait
	// because of the stop, not its deadline, while the detached process stays live.
	o.ServiceReadyTimeout = time.Hour
	o.ServiceTermGrace = time.Second
	o.PathExists = func(string) bool { return false }
	cname := "yolo-restarted-detached-input"
	sockets := hostServiceSocketsDir(cname, false)
	o.launchInputMountArgv = assembledPrivateFixtureMounts(t, workspace, cname)
	t.Setenv(launchInputChildEnv, "1")
	t.Setenv("YOLO_TEST_LAUNCH_INPUT_DETACH", "1")
	t.Setenv("YOLO_TEST_LAUNCH_INPUT_RESTART", "1")
	t.Cleanup(func() { _ = os.WriteFile(marker+".release", nil, 0o600); _ = os.RemoveAll(sockets) })
	handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true })
	if len(handles) != 1 {
		t.Fatalf("daemon failed: %s", output.String())
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			o.stopLoopholes(handles, sockets, cname, "podman")
		}
	})
	input := filepath.Join(state, "replicas", cname+".json")
	// Every wait below is on an event the daemon or the supervisor produces;
	// the go test timeout is the only clock.
	wait := func(suffix string) {
		t.Helper()
		for {
			if _, err := os.Stat(marker + suffix); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	wait(".first")
	if err := os.WriteFile(marker+".crash", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wait(".restarted")
	// The restarted process is still blocked before listening; teardown ends
	// supervision but must not remove its input until that process exits.
	// The stop waits for that exit, which only .release allows, so it runs
	// beside the checks rather than sitting out its final-exit timer.
	stopped = true
	stopDone := make(chan bool, 1)
	go func() { stopDone <- o.stopLoopholes(handles, sockets, cname, "podman") }()
	wait(".shutdown")
	body, err := os.ReadFile(marker + ".shutdown")
	if err != nil || string(body) != "restart-secret" {
		t.Fatalf("restarted daemon's shutdown read: %q %v", body, err)
	}
	// The non-fronted selected daemon exposes the supervisor's end directly.
	// Its restart-readiness wait ends on teardown before the process exits; a
	// wait that ignored the stop would hold this for the hour above.
	select {
	case <-handles[0].end.done:
	case failed := <-stopDone:
		t.Fatalf("stop returned (failed=%v) before the restarted process exited", failed)
	}
	select {
	case <-handles[0].inputExited:
		t.Fatal("input cleanup signaled before the restarted process's exit")
	case failed := <-stopDone:
		t.Fatalf("stop returned (failed=%v) before the restarted process exited", failed)
	default:
	}
	if _, err := os.Stat(input); err != nil {
		t.Fatalf("input removed before restarted process exited: %v", err)
	}
	if err := os.WriteFile(marker+".release", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	<-handles[0].inputExited
	if failed := <-stopDone; failed {
		t.Fatalf("stop reported a failure after the restarted process exited: %s", output.String())
	}
	for {
		if _, err := os.Stat(input); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPrivateLaunchInputRefusesMountAliasAndUnknownTopology(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "host state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(state, "replicas", "container.json")
	if err := os.MkdirAll(filepath.Dir(dest), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("synthetic sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{nil, {"podman", "run", "-v", root + ":/host:ro"}, {"podman", "run", "--mount", "type=bind,src=" + alias + ",destination=/alias,readonly"}, {"podman", "run", "-v", dest + ":/file:ro"}, {"podman", "run", "--mount", "type=bind,source=unresolved-relative,destination=/unsafe"}} {
		if err := launchInputOutsideJail(dest, argv); err == nil {
			t.Fatalf("accepted jail-visible source: %v", argv)
		}
	}
	if err := launchInputOutsideJail(dest, []string{"podman", "run", "-v", t.TempDir() + ":/workspace"}); err != nil {
		t.Fatalf("private source refused: %v", err)
	}
}

func TestOldLaunchInputExitCannotRemoveAtomicReplacement(t *testing.T) {
	state := filepath.Join(t.TempDir(), "private state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	cname := "yolo-replacement-fixture"
	makeOptions := func(data string) *Options {
		o := &Options{Workspace: workspace, launchInputProducers: map[string]LaunchInputProducer{
			"fixture": {StateDir: func(LaunchInputContext) string { return state }, Produce: func(LaunchInputContext) ([]byte, error) { return []byte(data), nil }},
		}}
		fillDefaults(o)
		o.launchInputMountArgv = []string{"podman", "run", "-v", workspace + ":/workspace"}
		return o
	}
	lp := &loopholes.Loophole{Name: "fixture", HostDaemon: &loopholes.HostDaemon{LaunchInput: "fixture"}}
	old, next := makeOptions("old"), makeOptions("new")
	if !old.writeLaunchInput(lp, cname) {
		t.Fatal("old publication failed")
	}
	path := old.launchInputFiles[lp.Name]
	owner := old.launchInputOwners[lp.Name]
	if !next.writeLaunchInput(lp, cname) {
		t.Fatal("replacement failed")
	}
	old.removeLaunchInput(lp.Name, path, owner)
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "new" {
		t.Fatalf("old cleanup deleted/replaced new input: %q %v", body, err)
	}
	next.removeLaunchInput(lp.Name, path, next.launchInputOwners[lp.Name])
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("new cleanup retained input: %v", err)
	}
}

func TestUnsupportedLaunchInputShellFormRefusesBeforeExec(t *testing.T) {
	var output strings.Builder
	o := &Options{Workspace: t.TempDir(), Stdout: &output, Stderr: &output, launchInputFiles: map[string]string{"fixture": "/private home/secret.json"}}
	fillDefaults(o)
	for _, cmd := range [][]any{
		{"/bin/sh", "-ec", "printf '%s' {launch_input}"},
		{"/bin/sh", "-ec", "--", "printf '%s' {launch_input}"},
		{"/bin/sh", "--", "-c", "printf '%s' {launch_input}"},
		{"/bin/bash", "-ce", "printf '%s' {launch_input}"},
		{"/bin/zsh", "-c", "printf '%s' {launch_input}"},
	} {
		if argv, ok := o.resolveDaemonArgv("fixture", newConfig("command", cmd), "/tmp/socket"); ok {
			t.Fatalf("unsupported shell form executed: %v", argv)
		}
	}
	if !strings.Contains(output.String(), "unsupported launch-input shell context") || !strings.Contains(output.String(), "as direct daemon argv") {
		t.Fatalf("refusal lacks action: %s", output.String())
	}
}

func TestDaemonizingLaunchInputWrapperRetainsInputWithExitAdvice(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("fixture uses a Unix host process group")
	}
	redirectState(t)
	t.Setenv(launchInputChildEnv, "1")
	t.Setenv("YOLO_TEST_LAUNCH_INPUT_WRAPPER", "1")
	const name = "daemonizing-input-fixture"
	marker := filepath.Join(t.TempDir(), "child-marker")
	command := fmt.Sprintf("%q -test.run=^TestLaunchInputFixtureDaemon$ -- \"$1\" \"$2\" %q", os.Args[0], marker)
	manifest := fmt.Sprintf(`{"name":%q,"description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["/bin/sh","-c",%q,"--","{launch_input}","{socket}"],"publishes":"socket","launch_input":"fixture"}}`, name, command)
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
	workspace, state := t.TempDir(), filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	var output lockedBuffer
	o := &Options{Workspace: workspace, Stderr: &output, Stdout: &output,
		launchInputMountArgv: []string{"podman", "run", "-v", workspace + ":/workspace"},
		launchInputProducers: map[string]LaunchInputProducer{"fixture": {
			StateDir: func(LaunchInputContext) string { return state },
			Produce:  func(LaunchInputContext) ([]byte, error) { return []byte("synthetic sentinel"), nil },
		}},
	}
	fillDefaults(o)
	o.ServiceReadyTimeout = 2 * time.Second
	o.ServiceTermGrace = time.Second
	cname := "yolo-daemonizing-input"
	sockets := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true })
	if len(handles) != 1 {
		t.Fatalf("wrapper not ready: %s", output.String())
	}
	stopped := false
	t.Cleanup(func() {
		if !stopped {
			o.stopLoopholes(handles, sockets, cname, "podman")
		}
	})
	path := o.launchInputFiles[name]
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(output.String(), "exit cannot be observed") {
		time.Sleep(10 * time.Millisecond)
	}
	if !strings.Contains(output.String(), "matching input file") || !strings.Contains(output.String(), "confirmed stopped") {
		t.Fatalf("wrapper exit did not advise retained input: %s", output.String())
	}
	select {
	case <-handles[0].inputUnknown:
	case <-time.After(2 * time.Second):
		t.Fatal("unobservable wrapper did not retire the cleanup observer")
	}
	if body, err := os.ReadFile(path); err != nil || string(body) != "synthetic sentinel" {
		t.Fatalf("wrapper child lost its live input: %q %v", body, err)
	}
	select {
	case <-handles[0].end.done:
		t.Fatal("wrapper exit closed service lifecycle prematurely")
	default:
	}
	o.stopLoopholes(handles, sockets, cname, "podman")
	stopped = true
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("unobserved child exit removed input: %v", err)
	}
	launchInputMu.Lock()
	owner := o.launchInputOwners[name]
	launchInputMu.Unlock()
	o.removeLaunchInput(name, path, owner) // Fixture's child has now been stopped.
}

func TestSelectedDaemonRefusesUnprovenLaunchInputBeforeProduction(t *testing.T) {
	redirectState(t)
	for _, tc := range []struct {
		name    string
		command []string
	}{
		{"nested shell", []string{"/bin/sh", "-c", `printf '%s' "$(printf '%s' {launch_input})"`}},
		{"split eval", []string{"/bin/sh", "-c", `e''val "printf '%s' {launch_input}"`}},
		{"combined shell option", []string{"/bin/sh", "-ec", "--", `printf '%s' {launch_input}`}},
		{"env wrapper", []string{"/usr/bin/env", "sh", "-ec", "--", `printf '%s' {launch_input}`}},
		{"xargs wrapper", []string{"/bin/xargs", "-a", "/dev/null", "/bin/sh", "-c", `printf '%s' {launch_input}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const name = "nested-launch-input-fixture"
			command, err := json.Marshal(tc.command)
			if err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf(`{"name":%q,"description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":%s,"publishes":"socket","launch_input":"fixture"}}`, name, command)
			module := filepath.Join(t.TempDir(), name)
			if err := os.MkdirAll(module, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
			if len(set.Enabled()) != 1 {
				t.Fatalf("selected fixture missing: %+v", set.Enabled())
			}
			state := filepath.Join(t.TempDir(), "private path;printf INJECTED")
			if err := os.MkdirAll(state, 0o700); err != nil {
				t.Fatal(err)
			}
			workspace := t.TempDir()
			var output strings.Builder
			produced := 0
			o := &Options{Workspace: workspace, Stderr: &output, Stdout: &output,
				launchInputMountArgv: []string{"podman", "run", "-v", workspace + ":/workspace"},
				launchInputProducers: map[string]LaunchInputProducer{"fixture": {
					StateDir: func(LaunchInputContext) string { return state },
					Produce:  func(LaunchInputContext) ([]byte, error) { produced++; return []byte("synthetic sentinel"), nil },
				}},
			}
			fillDefaults(o)
			cname := "yolo-nested-input-test"
			sockets := hostServiceSocketsDir(cname, false)
			t.Cleanup(func() { _ = os.RemoveAll(sockets) })
			if handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true }); len(handles) != 0 {
				t.Fatalf("unproven input started host service: %d", len(handles))
			}
			if produced != 0 {
				t.Fatalf("refusal called producer %d times", produced)
			}
			if _, err := os.Stat(filepath.Join(state, "replicas", cname+".json")); !os.IsNotExist(err) {
				t.Fatalf("refusal published input: %v", err)
			}
			if !strings.Contains(output.String(), "unsupported launch-input shell context") || !strings.Contains(output.String(), "as direct daemon argv") {
				t.Fatalf("missing safe refusal and remedy: %s", output.String())
			}
		})
	}
}

func TestLaunchInputShellOptionAndRawArgvContexts(t *testing.T) {
	var output strings.Builder
	path := "/private home/a;printf INJECTED.json"
	o := &Options{Workspace: t.TempDir(), Stderr: &output, Stdout: &output, launchInputFiles: map[string]string{"fixture": path}}
	fillDefaults(o)
	for _, cmd := range [][]any{
		{"/bin/sh", "-ec", "--", "printf '%s' {launch_input}"},
		{"/bin/sh", "--", "-c", "printf '%s' {launch_input}"},
		{"/bin/bash", "-ce", "printf '%s' {launch_input}"},
		{"/bin/dash", "-c", "printf '%s' {launch_input}"},
		{"/usr/bin/env", "sh", "-ec", "--", "printf '%s' {launch_input}"},
		{"/usr/bin/env", "-u", "LC_ALL", "sh", "-c", "printf '%s' {launch_input}"},
		{"/usr/bin/env", "NAME=value", "sh", "-c", "printf '%s' {launch_input}"},
		{"/usr/bin/timeout", "5", "sh", "-c", "printf '%s' {launch_input}"},
		{"/usr/bin/python3", "-c", "print({launch_input})"},
	} {
		if argv, ok := o.resolveDaemonArgv("fixture", newConfig("command", cmd), "/tmp/socket"); ok {
			t.Fatalf("shell form resolved without a proven context: %v", argv)
		}
	}
	for _, script := range []string{
		`printf '%s' {launch_input}`,
		`printf '%s' "{launch_input}"`,
		`printf '%s' '{launch_input}'`,
		`printf '%s' --config {launch_input}`,
		`printf '%s' "$(printf '%s' {launch_input})"`,
		"printf '%s' `{launch_input}`",
		`printf '%s' ${launch_input}`,
		`printf '%s' $'{launch_input}'`,
		"cat <<EOF\n{launch_input}\nEOF",
		`eval "printf '%s' {launch_input}"`,
		`e''val "printf '%s' {launch_input}"`,
		`sh -c "printf '%s' {launch_input}"`,
	} {
		if argv, ok := o.resolveDaemonArgv("fixture", newConfig("command", []any{"/bin/sh", "-c", script}), "/tmp/socket"); ok {
			t.Fatalf("nested script resolved: %v", argv)
		}
	}
	if !strings.Contains(output.String(), "as direct daemon argv") {
		t.Fatalf("refusal has no direct-argv remedy: %s", output.String())
	}
	argv, ok := o.resolveDaemonArgv("fixture", newConfig("command", []any{"/usr/bin/printf", "%s", "{launch_input}"}), "/tmp/socket")
	if !ok || len(argv) != 3 || argv[2] != path {
		t.Fatalf("direct argv was not preserved: %v, %v", argv, ok)
	}
	argv, ok = o.resolveDaemonArgv("fixture", newConfig("command", []any{"/usr/bin/printf", "--config", "{launch_input}"}), "/tmp/socket")
	if !ok || len(argv) != 3 || argv[2] != path {
		t.Fatalf("ordinary --config argv was not preserved: %v, %v", argv, ok)
	}
	argv, ok = o.resolveDaemonArgv("fixture", newConfig("command", []any{"/bin/sh", "-c", `printf '%s' --config "$1"`, "--", "{launch_input}"}), "/tmp/socket")
	if !ok || argv[4] != path {
		t.Fatalf("positional --config script was falsely refused: %v, %v", argv, ok)
	}
	argv, ok = o.resolveDaemonArgv("fixture", newConfig("command", []any{"/bin/sh", "-c", `printf '%s' "$1"`, "--", "{launch_input}"}), "/tmp/socket")
	if !ok || len(argv) != 5 || argv[4] != path {
		t.Fatalf("shell positional argv was not preserved: %v, %v", argv, ok)
	}
	got, err := exec.Command(argv[0], argv[1:]...).Output()
	if err != nil || string(got) != path {
		t.Fatalf("shell positional path lost data semantics: %q, %v", got, err)
	}
}

func TestLaunchInputCleanupMissingPrivateDirReportsWithoutPayload(t *testing.T) {
	var output strings.Builder
	o := &Options{Stderr: &output}
	fillDefaults(o)
	o.removeLaunchInput("fixture", filepath.Join(t.TempDir(), "missing", "instance.json"), nil)
	if !strings.Contains(output.String(), "confirmed stopped") || !strings.Contains(output.String(), "matching input file") {
		t.Fatalf("missing private directory left no safe next step: %s", output.String())
	}
}

func TestLaunchInputCleanupLockFailureReportsWithoutPayload(t *testing.T) {
	state := filepath.Join(t.TempDir(), "private")
	if err := os.MkdirAll(filepath.Join(state, "replicas"), 0o700); err != nil {
		t.Fatal(err)
	}
	const payload = "synthetic sentinel never disclose"
	path := filepath.Join(state, "replicas", "instance.json")
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	owner, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(state, "replicas", ".launch-input.lock")
	if err := os.WriteFile(lockPath, []byte("lock"), 0o640); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	o := &Options{Stderr: &output}
	fillDefaults(o)
	o.removeLaunchInput("fixture", path, owner)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file changed after lock refusal: %v", err)
	}
	if strings.Contains(output.String(), payload) || !strings.Contains(output.String(), "confirmed stopped") || !strings.Contains(output.String(), "matching input file") {
		t.Fatalf("cleanup warning unsafe or absent: %s", output.String())
	}
}

func assembledPrivateFixtureMounts(t *testing.T, workspace, cname string) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	o := goldenOptions(workspace, home)
	input := &assembleInput{cfg: newConfig(), rt: "podman", cname: cname,
		wsState: filepath.Join(workspace, ".yolo", "home"), miseStore: filepath.Join(home, "mise-store"),
		homeSkeleton: filepath.Join(home, "skeleton"), agentsPath: filepath.Join(home, "agents"),
		imageRef: goldenImageRef, jailPrefix: jailPrefix{binDir: filepath.Join(home, "prefix-bin"), shareDir: filepath.Join(home, "prefix-share")}, mountTargets: map[string]struct{}{}}
	argv := o.assembleRunCmd(input)
	if input.assemblyErr != nil {
		t.Fatal(input.assemblyErr)
	}
	for _, dir := range []string{input.homeSkeleton, input.miseStore, input.agentsPath, input.wsState} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for _, mount := range mountSources(argv) {
		if mount.src == "" || namedVolume(mount.src) {
			continue // tmpfs or a named volume has no host directory
		}
		if mount.src == hostServiceSocketsDir(cname, false) {
			mkdirHostServicesDir(mount.src)
			continue
		}
		if _, err := os.Lstat(mount.src); os.IsNotExist(err) {
			if !strings.HasPrefix(mount.src, home+string(os.PathSeparator)) && !strings.HasPrefix(mount.src, workspace+string(os.PathSeparator)) {
				t.Fatalf("assembler referenced missing source outside fixture: %s", mount.src)
			}
			if err := os.MkdirAll(mount.src, 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	return argv
}

func TestRealAssembledMountTopologyExcludesPrivateInput(t *testing.T) {
	workspace := t.TempDir()
	cname := "yolo-private-assembled"
	argv := assembledPrivateFixtureMounts(t, workspace, cname)
	state := filepath.Join(t.TempDir(), "private state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(state, "replicas", cname+".json")
	if err := launchInputOutsideJail(dest, argv); err != nil {
		t.Fatalf("full assembled argv did not prove private location: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(state, "replicas"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dest, []byte("synthetic sentinel"), 0o600); err != nil {
		t.Fatal(err)
	}
	argv = append(argv, "--volume", dest+":/leak:ro")
	if err := launchInputOutsideJail(dest, argv); err == nil {
		t.Fatal("assembled file-level alias became jail-visible")
	}
}

func TestKnownProducerWithoutMountProofNeverRuns(t *testing.T) {
	state := filepath.Join(t.TempDir(), "private state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	called := false
	o := &Options{Workspace: t.TempDir(), launchInputProducers: map[string]LaunchInputProducer{
		"fixture": {StateDir: func(LaunchInputContext) string { return state }, Produce: func(LaunchInputContext) ([]byte, error) { called = true; return []byte("should-not-publish"), nil }},
	}}
	fillDefaults(o)
	lp := &loopholes.Loophole{Name: "fixture", HostDaemon: &loopholes.HostDaemon{LaunchInput: "fixture"}}
	if o.writeLaunchInput(lp, "yolo-no-topology") || called {
		t.Fatal("producer ran without complete mount proof")
	}
	if _, err := os.Lstat(filepath.Join(state, "replicas")); !os.IsNotExist(err) {
		t.Fatalf("refusal made private state: %v", err)
	}
}

func TestFailedDaemonStartRemovesInputOnlyAfterObservedExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix daemon")
	}
	redirectState(t)
	const name = "failing input fixture"
	state := filepath.Join(t.TempDir(), "private state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{"name":%q,"description":"synthetic","transport":"loopback-tls","lifecycle":"spawned","host_daemon":{"cmd":["/bin/sh","-c","exit 17","daemon","{launch_input}","{socket}"],"publishes":"socket","detach":true,"launch_input":"fixture"}}`, name)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}}})
	workspace := t.TempDir()
	cname := "yolo-failed-launch-input"
	var output strings.Builder
	o := &Options{Workspace: workspace, launchInputProducers: map[string]LaunchInputProducer{
		"fixture": {StateDir: func(LaunchInputContext) string { return state }, Produce: func(LaunchInputContext) ([]byte, error) { return []byte("synthetic"), nil }},
	}}
	fillDefaults(o)
	o.Stderr, o.Stdout = &output, &output
	o.ServiceReadyTimeout = 300 * time.Millisecond
	o.launchInputMountArgv = assembledPrivateFixtureMounts(t, workspace, cname)
	sockets := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() { _ = os.RemoveAll(sockets) })
	if handles := o.startLoopholesMatching(set, cname, "podman", nil, func(string) bool { return true }); len(handles) != 0 {
		t.Fatalf("failed daemon started: %d", len(handles))
	}
	path := filepath.Join(state, "replicas", cname+".json")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("failed daemon input survived exit: %s", output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLaunchInputCleanupLockOrder pins the actual cleanup callsite: the
// destination flock must be taken before the map mutex, just as publication
// does. A reversed acquisition used to deadlock two simultaneous lifetimes.
func TestLaunchInputCleanupLockOrder(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no source path")
	}
	tree, err := parser.ParseFile(token.NewFileSet(), filepath.Join(filepath.Dir(file), "launchinput.go"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var flock, mutex token.Pos
	for _, decl := range tree.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "removeLaunchInput" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			obj, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if obj.Name == "syscall" && sel.Sel.Name == "Flock" && flock == token.NoPos {
				flock = call.Pos()
			}
			if obj.Name == "launchInputMu" && sel.Sel.Name == "Lock" && mutex == token.NoPos {
				mutex = call.Pos()
			}
			return true
		})
	}
	if flock == token.NoPos || mutex == token.NoPos || mutex < flock {
		t.Fatal("cleanup must acquire destination flock before map mutex")
	}
}

func TestConcurrentLaunchInputCleanupAndReplacement(t *testing.T) {
	state := filepath.Join(t.TempDir(), "private state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	cname := "yolo-concurrent-publication"
	makeOptions := func(data string) *Options {
		o := &Options{Workspace: workspace, launchInputProducers: map[string]LaunchInputProducer{
			"fixture": {StateDir: func(LaunchInputContext) string { return state }, Produce: func(LaunchInputContext) ([]byte, error) { return []byte(data), nil }},
		}, launchInputMountArgv: []string{"podman", "run", "-v", workspace + ":/workspace"}}
		fillDefaults(o)
		return o
	}
	lp := &loopholes.Loophole{Name: "fixture", HostDaemon: &loopholes.HostDaemon{LaunchInput: "fixture"}}
	old, next := makeOptions("old"), makeOptions("new")
	if !old.writeLaunchInput(lp, cname) {
		t.Fatal("first publication")
	}
	path := old.launchInputFiles[lp.Name]
	owner := old.launchInputOwners[lp.Name]
	lock, err := os.OpenFile(filepath.Join(state, "replicas", ".launch-input.lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	held := true
	t.Cleanup(func() {
		if held {
			_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		}
		_ = lock.Close()
	})
	cleaned := make(chan struct{})
	go func() { old.removeLaunchInput(lp.Name, path, owner); close(cleaned) }()
	// With the destination lock held, cleanup may NOT hold the map mutex.
	// The old inverted order held the mutex while waiting for our flock.
	mapAvailable := make(chan string, 1)
	go func() {
		launchInputMu.Lock()
		trackedPath := old.launchInputFiles[lp.Name]
		launchInputMu.Unlock()
		mapAvailable <- trackedPath
	}()
	select {
	case trackedPath := <-mapAvailable:
		if trackedPath != path {
			t.Fatalf("cleanup changed tracked path while waiting for destination flock: %q", trackedPath)
		}
	case <-time.After(time.Second):
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		held = false
		<-cleaned
		t.Fatal("cleanup waited on destination flock while holding map mutex")
	}
	published := make(chan bool, 1)
	go func() { published <- next.writeLaunchInput(lp, cname) }()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	held = false
	select {
	case <-cleaned:
	case <-time.After(2 * time.Second):
		t.Fatal("old cleanup deadlocked")
	}
	select {
	case ok := <-published:
		if !ok {
			t.Fatal("replacement failed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("publisher deadlocked")
	}
	body, err := os.ReadFile(path)
	if err != nil || string(body) != "new" {
		t.Fatalf("new publication not intact: %q %v", body, err)
	}
	next.removeLaunchInput(lp.Name, path, next.launchInputOwners[lp.Name])
}

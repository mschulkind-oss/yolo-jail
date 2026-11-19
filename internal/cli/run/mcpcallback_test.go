package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauth"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpauthdaemon"
	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The helper starts successfully and either emits a real drained bind refusal
// before BootReadyLine, or emits readiness and waits on its signal event. It
// never invokes a runtime, agent, installer, hook, listener or provider.
func TestManagedMCPCallbackRuntimeChild(t *testing.T) {
	root := os.Getenv("YOLO_TEST_CALLBACK_RUNTIME_ROOT")
	if root == "" {
		return
	}
	at := slices.Index(os.Args, "--")
	if at < 0 || at+1 >= len(os.Args) {
		os.Exit(2)
	}
	args := os.Args[at+1:]
	if args[0] == "exec" {
		os.Exit(0)
	}
	if args[0] != "run" {
		os.Exit(2)
	}
	file, err := os.OpenFile(filepath.Join(root, "attempts"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		os.Exit(3)
	}
	fmt.Fprintln(file, strings.Join(args, " "))
	file.Close()
	if _, err := os.Stat(filepath.Join(root, "conflict")); err == nil && slices.Contains(args, "127.0.0.1:3118:3118/tcp") {
		if _, err := os.Stat(filepath.Join(root, "partial")); err == nil {
			if err := os.WriteFile(filepath.Join(root, "created"), []byte("partial"), 0600); err != nil {
				os.Exit(3)
			}
		}
		fmt.Fprintln(os.Stderr, "Error: rootlessport listen tcp 127.0.0.1:3118: bind: address already in use")
		os.Exit(125)
	}
	if _, err := os.Stat(filepath.Join(root, "unrelated")); err == nil {
		fmt.Fprintln(os.Stderr, "Error: image fixture unavailable")
		os.Exit(125)
	}
	if err := os.WriteFile(filepath.Join(root, "created"), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		os.Exit(3)
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM)
	fmt.Fprintln(os.Stderr, entrypoint.BootReadyLine)
	<-signals
	os.Exit(0)
}

type callbackRun struct {
	rc                int
	plan              *keeperPlan
	options           *Options
	root              string
	retain            bool
	stopped           bool
	pidSeen           string
	liveness          string
	pending           string
	acquires          int
	releases          int
	stops             int
	rms               int
	cleanupFail       bool
	replaced          bool
	unknownAfterStop  bool
	replacedAfterStop bool
	out, errs         bytes.Buffer
}

type callbackFixture struct {
	t            *testing.T
	home, bundle string
	mu           sync.Mutex
	runs         map[string]*callbackRun
	foreign      bool
	spawns       int
	probes       int
	tune         func(*callbackRun, *Options)
	beforeSpawn  func(*callbackRun)
}

func callbackMCPLaunchFixture(t *testing.T) *callbackFixture {
	t.Helper()
	home := packHome(t)
	root := t.TempDir()
	files := map[string]string{
		"pack.toml": `name="callback-fixture"
skills_tier="namespaced"
[[skills]]
from="skills"
[[mcp_sign_in_bundle]]
bundle="skills/original"
format="claude-plugin"
origin="https://example.invalid/original"
revision="0123456789abcdef0123456789abcdef01234567"
license="skills/original/LICENSE"
callback_route=true
`,
		"skills/original/LICENSE":                    "MIT License",
		"skills/original/.claude-plugin/plugin.json": `{"name":"original"}`,
		"skills/original/.mcp.json":                  `{"mcpServers":{"native-key":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"public-fixture","callbackPort":3118}}}}`,
	}
	for relative, body := range files {
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(home, ".config/yolo-jail"), 0755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("packs=[\"claude\",%q]\npack_sources={fixture={path=%q,bare=true}}\n[mcp_servers.configured-alias]\nurl=\"https://mcp.fixture.invalid/mcp\"\nsign_in=\"once\"\n[allow.cgroup-delegate]\noff=true\n[allow.claude-auth]\noff=true\n[allow.mcp-auth]\noff=true\n", "fixture/"+filepath.Base(root), filepath.Dir(root))
	if err := os.WriteFile(filepath.Join(home, ".config/yolo-jail/config.toml"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	fixture := &callbackFixture{t: t, home: home, bundle: root, runs: map[string]*callbackRun{}}
	previous := defaultKeeperSpawner
	t.Cleanup(func() { defaultKeeperSpawner = previous })
	defaultKeeperSpawner = func(o *Options, path string, progress, lifeline, lock *os.File, reserved []*os.File) (func() int, error) {
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		plan := &keeperPlan{}
		if err := json.Unmarshal(body, plan); err != nil {
			return nil, err
		}
		fixture.mu.Lock()
		fixture.spawns++
		var record *callbackRun
		for _, candidate := range fixture.runs {
			if candidate.options.Workspace == o.Workspace {
				record = candidate
			}
		}
		if record == nil {
			fixture.mu.Unlock()
			return nil, fmt.Errorf("fixture has no launch observation")
		}
		record.plan = plan
		record.options = o
		fixture.mu.Unlock()
		record.pending = fixture.statusRecord(record)
		if fixture.beforeSpawn != nil {
			fixture.beforeSpawn(record)
		}
		// The fixture user disabled external services. The real keeper still
		// validates its plan and owns actual process Start/ready/drain/teardown.
		binary, err := os.Executable()
		if err != nil {
			return nil, err
		}
		helper := filepath.Join(record.root, "runtime")
		script := "#!/bin/sh\nYOLO_TEST_CALLBACK_RUNTIME_ROOT=" + shquote.Quote(record.root) + " exec " + shquote.Quote(binary) + " -test.run='^TestManagedMCPCallbackRuntimeChild$' -- \"$@\"\n"
		if err := os.WriteFile(helper, []byte(script), 0755); err != nil {
			return nil, err
		}
		plan.RunCmd[0] = helper
		if err := writeReplacementKeeperFixturePlan(path, plan); err != nil {
			return nil, err
		}
		return inProcessKeeper(o, path, progress, lifeline, lock, reserved)
	}
	return fixture
}

func writeReplacementKeeperFixturePlan(path string, plan *keeperPlan) error {
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	return os.WriteFile(path, body, 0600)
}

func (f *callbackFixture) Start(name string, foreignRace, unrelated, retain bool) *callbackRun {
	f.t.Helper()
	record := &callbackRun{root: f.t.TempDir(), retain: retain, liveness: "live"}
	workspace := f.t.TempDir()
	o := dispatchOptions(f.t, workspace, "podman", &record.out, &record.errs, nil)
	record.options = o
	f.mu.Lock()
	f.runs[name] = record
	f.mu.Unlock()
	o.mcpCallbackLeaseEvent = func(operation string, _ mcpsignin.CallbackLease) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if operation == "acquire" {
			record.acquires++
		} else {
			record.releases++
		}
	}
	o.mcpCallbackRoot = filepath.Join(f.home, ".local/share/yolo-jail/state/mcp-callback")
	o.mcpCallbackPortFree = func(int) (bool, bool) { f.mu.Lock(); defer f.mu.Unlock(); return !f.foreign, true }
	o.AcceptConfigChanges, o.NeverAttach = true, true
	repo, _ := o.RepoRoot()
	o.PathExists = func(path string) bool { return path == filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint") }
	o.Exec = f.exec
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.CapturesDir = func() string { return "" }
	o.AutoCapture = func([]string, string, io.Writer, bool) { f.t.Error("unexpected auto-capture") }
	if foreignRace {
		if err := os.WriteFile(filepath.Join(record.root, "conflict"), nil, 0600); err != nil {
			f.t.Fatal(err)
		}
	}
	if unrelated {
		if err := os.WriteFile(filepath.Join(record.root, "unrelated"), nil, 0600); err != nil {
			f.t.Fatal(err)
		}
	}
	// The first-session client is also the inert helper, never real podman.
	binary, err := os.Executable()
	if err != nil {
		f.t.Fatal(err)
	}
	bin := f.t.TempDir()
	script := "#!/bin/sh\nYOLO_TEST_CALLBACK_RUNTIME_ROOT=" + shquote.Quote(record.root) + " exec " + shquote.Quote(binary) + " -test.run='^TestManagedMCPCallbackRuntimeChild$' -- \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte(script), 0755); err != nil {
		f.t.Fatal(err)
	}
	f.t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if f.tune != nil {
		f.tune(record, o)
	}
	record.rc = Run(*o)
	return record
}

func (f *callbackFixture) exec(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
	f.mu.Lock()
	defer f.mu.Unlock()
	text := strings.Join(argv, " ")
	if strings.Contains(text, " ps ") {
		f.probes++
	}
	for _, record := range f.runs {
		if record.plan == nil || !strings.Contains(text, record.plan.Cname) {
			continue
		}
		pidBytes, exists := os.ReadFile(filepath.Join(record.root, "created"))
		if exists == nil && record.pidSeen != string(pidBytes) {
			record.pidSeen = string(pidBytes)
			record.stopped = false
		}
		switch {
		case strings.Contains(text, " inspect "):
			wire, _ := envArgValue(record.plan.RunCmd, mcpsignin.WireEnv)
			if record.replaced {
				wire = strings.ReplaceAll(wire, record.plan.LaunchInputMCP.SignIn.CallbackGeneration, "replacement-generation")
			}
			return ExecResult{Ran: true, Stdout: mcpsignin.WireEnv + "=" + wire + "\n"}
		case strings.Contains(text, " stop "):
			record.stops++
			if record.unknownAfterStop {
				record.liveness = "unknown"
				return ExecResult{Ran: true}
			}
			if record.cleanupFail {
				return ExecResult{Ran: true, RC: 125}
			}
			record.stopped = true
			if exists == nil {
				if pid, err := strconv.Atoi(string(pidBytes)); err == nil && pid > 0 {
					_ = syscall.Kill(pid, syscall.SIGTERM)
				}
			}
			if record.replacedAfterStop {
				_ = os.WriteFile(filepath.Join(record.root, "created"), []byte("replacement"), 0600)
				return ExecResult{Ran: true}
			}
			if !record.retain {
				_ = os.Remove(filepath.Join(record.root, "created"))
			}
			return ExecResult{Ran: true}
		case strings.Contains(text, " rm "):
			record.rms++
			if record.cleanupFail || record.unknownAfterStop {
				return ExecResult{Ran: true, RC: 125}
			}
			if !record.retain || string(pidBytes) == "partial" {
				_ = os.Remove(filepath.Join(record.root, "created"))
			}
			return ExecResult{Ran: true}
		case strings.Contains(text, " ps "):
			if record.stopped && strings.Contains(text, "ps -q") {
				return ExecResult{Ran: true}
			}
			if record.liveness == "unknown" {
				return ExecResult{Ran: true, RC: 125}
			}
			if record.liveness != "gone" && exists == nil {
				id := "fixture-id-" + record.plan.Cname
				if string(pidBytes) == "replacement" {
					id = "replacement-id-" + record.plan.Cname
				}
				return ExecResult{Ran: true, Stdout: id + "\n"}
			}
			return ExecResult{Ran: true}
		}
	}
	return ExecResult{Ran: true}
}

func (f *callbackFixture) Status(name string) string {
	f.t.Helper()
	record := f.runs[name]
	if record.plan == nil || record.plan.LaunchInputMCP == nil {
		f.t.Fatal("no produced plan")
	}
	return f.statusRecord(record)
}

func (f *callbackFixture) statusRecord(record *callbackRun) string {
	share := mcpauth.Share{Version: 1, Once: []mcpauth.OnceServer{{Name: "configured-alias", URL: "https://mcp.fixture.invalid/mcp", Transport: "http"}}, SignIn: record.plan.LaunchInputMCP.SignIn}
	rows := mcpauthdaemon.ReadHostStatus(filepath.Join(f.home, ".local/share/yolo-jail/state/mcp-auth-broker"), share)
	if len(rows) != 1 {
		f.t.Fatal(rows)
	}
	return fmt.Sprint(rows[0]["callback"]) + " " + fmt.Sprint(rows[0]["next_step"])
}

func TestManagedMCPCallbackHeldLockDeadlineKeepsUsableLaunchWithoutOwnershipMutation(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	var before []byte
	f.tune = func(_ *callbackRun, o *Options) {
		callbackHeldLock(t, o.callbackRoot())
		lease := mcpsignin.CallbackLease{Version: 1, Port: 3118, Launch: "previous-owner", Runtime: "podman", Generation: "previous-generation"}
		if err := writeCallbackJSON(mcpsignin.CallbackLeasePath(o.callbackRoot(), 3118), lease); err != nil {
			t.Fatal(err)
		}
		before, _ = os.ReadFile(mcpsignin.CallbackLeasePath(o.callbackRoot(), 3118))
		now := time.Unix(100, 0)
		o.mcpCallbackLockNow = func() time.Time { return now }
		o.mcpCallbackLockWait = func(context.Context, time.Duration) error { now = now.Add(mcpCallbackLeaseWait); return nil }
	}
	record := f.Start("locked", false, false, false)
	after, err := os.ReadFile(mcpsignin.CallbackLeasePath(record.options.callbackRoot(), 3118))
	if record.rc != 0 || len(record.plan.CallbackLeases) != 0 || err != nil || !bytes.Equal(before, after) || !strings.Contains(f.Status("locked"), "unavailable") || !strings.Contains(f.Status("locked"), "full callback URL") {
		t.Fatal("optional wait deadline refused launch/mutated owner", record.rc, f.Status("locked"), err)
	}
}

func TestManagedMCPCallbackUnavailableStateKeepsUsableLaunchAndHonestStatus(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	f.tune = func(_ *callbackRun, o *Options) {
		if err := os.MkdirAll(o.callbackRoot(), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(o.callbackRoot(), 0755); err != nil {
			t.Fatal(err)
		}
	}
	record := f.Start("unavailable", false, false, false)
	if record.rc != 0 || len(record.plan.CallbackLeases) != 0 || !strings.Contains(f.Status("unavailable"), "unavailable") || !strings.Contains(f.Status("unavailable"), "full callback URL") {
		t.Fatal("unavailable state refused launch or invented holder", record.rc, f.Status("unavailable"))
	}
}

func TestManagedMCPCallbackStatusPlainReadsProducedFactsWithoutProbeOrMutation(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	record := f.Start("status", false, false, true)
	if record.rc != 0 {
		t.Fatal(record.rc, record.errs.String())
	}
	before := callbackStateSnapshot(t, record.options.callbackRoot())
	f.mu.Lock()
	probes := f.probes
	f.mu.Unlock()
	status := f.Status("status")
	f.mu.Lock()
	afterProbes := f.probes
	f.mu.Unlock()
	if !strings.HasPrefix(status, "owner ") || probes != afterProbes || !reflect.DeepEqual(before, callbackStateSnapshot(t, record.options.callbackRoot())) {
		t.Fatal("status probed/mutated callback state", status)
	}
}

func callbackStateSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	rows := map[string]string{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rows[path] = fmt.Sprint(info.Mode()) + string(body)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestManagedMCPCallbackForeignRaceRetriesOnlyFailedAttempt(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	path := filepath.Join(f.home, ".config/yolo-jail/config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config = append(config, []byte("\n[machine]\nports=[{host=8999,jail=9000}]\n")...)
	if err := os.WriteFile(path, config, 0644); err != nil {
		t.Fatal(err)
	}
	record := f.Start("foreign-race", true, false, false)
	if record.rc != 0 {
		t.Fatalf("optional early refusal failed launch: %d %s %s", record.rc, record.out.String(), record.errs.String())
	}
	attempts, err := os.ReadFile(filepath.Join(record.root, "attempts"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(attempts)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "127.0.0.1:3118:3118/tcp") || strings.Contains(lines[1], "127.0.0.1:3118:3118/tcp") || !strings.Contains(lines[0], "8999:9000") || !strings.Contains(lines[1], "8999:9000") {
		t.Fatalf("actual retry lost declared ports or did not remove only callback: %s", attempts)
	}
	if strings.Contains(lines[1], "3118/tcp") {
		t.Fatalf("callback DNAT input survived: %s", lines[1])
	}
	if f.spawns != 1 || !strings.Contains(f.Status("foreign-race"), "port busy") || !strings.Contains(f.Status("foreign-race"), "full callback URL") {
		t.Fatal("Run/producer repeated or observed failure was hidden", f.spawns, f.Status("foreign-race"))
	}
	if _, err := mcpsignin.ReadCallbackLease(record.options.callbackRoot(), 3118); !os.IsNotExist(err) {
		t.Fatal("failed optional lease leaked", err)
	}
	failure := mcpsignin.CallbackReceiptPath(record.options.callbackRoot(), record.plan.LaunchInputMCP.SignIn.CallbackGeneration) + ".failure-3118"
	if _, err := os.Stat(failure); err != nil {
		t.Fatal("actual early failure receipt missing", err)
	}
}

func TestManagedMCPCallbackOwnPartialCreateIsStoppedConfirmedGoneThenRetried(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	path := filepath.Join(f.home, ".config/yolo-jail/config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config = append(config, []byte("\n[machine]\nports=[{host=8999,jail=9000}]\n")...)
	if err := os.WriteFile(path, config, 0644); err != nil {
		t.Fatal(err)
	}
	f.tune = func(record *callbackRun, _ *Options) {
		if err := os.WriteFile(filepath.Join(record.root, "partial"), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	record := f.Start("own-partial", true, false, false)
	attempts, err := os.ReadFile(filepath.Join(record.root, "attempts"))
	if err != nil {
		t.Fatal(err)
	}
	if record.rc != 0 || len(strings.Split(strings.TrimSpace(string(attempts)), "\n")) != 2 || record.stops != 2 || f.spawns != 1 {
		t.Fatalf("own partial optional failure did not become usable same-keeper launch: rc=%d stops=%d spawns=%d %s", record.rc, record.stops, f.spawns, attempts)
	}
	lines := strings.Split(strings.TrimSpace(string(attempts)), "\n")
	if !strings.Contains(lines[0], "8999:9000") || !strings.Contains(lines[1], "8999:9000") || strings.Contains(lines[1], "127.0.0.1:3118:3118/tcp") {
		t.Fatal("partial retry changed declared publications", string(attempts))
	}
	if !strings.Contains(f.Status("own-partial"), "port busy") {
		t.Fatal(f.Status("own-partial"))
	}
}

func TestManagedMCPCallbackPartialUnsafeCleanupNeverBlindlyRetriesOrDeletes(t *testing.T) {
	for _, name := range []string{"cleanup failed", "replacement", "replacement after stop", "unknown after stop", "unknown before stop"} {
		t.Run(name, func(t *testing.T) {
			f := callbackMCPLaunchFixture(t)
			f.tune = func(record *callbackRun, _ *Options) {
				if err := os.WriteFile(filepath.Join(record.root, "partial"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				record.cleanupFail = name == "cleanup failed"
				record.replaced = name == "replacement"
				record.replacedAfterStop = name == "replacement after stop"
				record.unknownAfterStop = name == "unknown after stop"
			}
			if name == "unknown before stop" {
				f.beforeSpawn = func(record *callbackRun) { f.mu.Lock(); record.liveness = "unknown"; f.mu.Unlock() }
			}
			record := f.Start("unsafe-partial", true, false, true)
			attempts, err := os.ReadFile(filepath.Join(record.root, "attempts"))
			if err != nil {
				t.Fatal(err)
			}
			if record.rc != 125 || len(strings.Split(strings.TrimSpace(string(attempts)), "\n")) != 1 || record.rms != 0 {
				t.Fatal("unsafe partial blindly retried/deleted", record.rc, record.stops, record.rms)
			}
			if (name == "replacement" || name == "unknown before stop") && record.stops != 0 {
				t.Fatal("unproven partial was stopped", record.stops)
			}
			if lease, err := mcpsignin.ReadCallbackLease(record.options.callbackRoot(), 3118); err != nil || lease.Generation != record.options.mcpCallbackGeneration {
				t.Fatal("unsafe partial released lease", lease, err)
			}
		})
	}
}

func TestManagedMCPCallbackSynchronousOptionalCreateErrorRetriesSameKeeper(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	previous := keeperStartJailMain
	t.Cleanup(func() { keeperStartJailMain = previous })
	starts := 0
	keeperStartJailMain = func(argv []string, stdout, stderr io.Writer, onExit func()) (*jailMain, error) {
		starts++
		if starts == 1 {
			return nil, fmt.Errorf("rootlessport listen tcp 127.0.0.1:3118: bind: address already in use")
		}
		return startJailMain(argv, stdout, stderr, onExit)
	}
	record := f.Start("sync-refusal", false, false, false)
	if record.rc != 0 || starts != 2 || f.spawns != 1 || !strings.Contains(f.Status("sync-refusal"), "port busy") {
		t.Fatal("sync refusal reentered Run/failed", record.rc, starts, f.spawns, f.Status("sync-refusal"))
	}
}

func TestManagedMCPCallbackMultiplePortsRetryPreservesUnaffectedPublication(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	path := filepath.Join(f.bundle, "skills/original/.mcp.json")
	body := `{"mcpServers":{"native-key":{"type":"http","url":"https://mcp.fixture.invalid/mcp","oauth":{"clientId":"public-fixture","callbackPort":3118}},"second-key":{"type":"http","url":"https://second.fixture.invalid/mcp","oauth":{"clientId":"second-public","callbackPort":3119}}}}`
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(f.home, ".config/yolo-jail/config.toml")
	config, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	config = append(config, []byte("\n[mcp_servers.second-alias]\nurl=\"https://second.fixture.invalid/mcp\"\nsign_in=\"once\"\n")...)
	if err := os.WriteFile(path, config, 0644); err != nil {
		t.Fatal(err)
	}
	record := f.Start("multiple", true, false, true)
	attempts, err := os.ReadFile(filepath.Join(record.root, "attempts"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(attempts)), "\n")
	if record.rc != 0 || len(lines) != 2 || !strings.Contains(lines[0], "127.0.0.1:3118:3118/tcp") || strings.Contains(lines[1], "127.0.0.1:3118:3118/tcp") || !strings.Contains(lines[0], "127.0.0.1:3119:3119/tcp") || !strings.Contains(lines[1], "127.0.0.1:3119:3119/tcp") {
		t.Fatalf("retry dropped unaffected optional port: %d %s", record.rc, attempts)
	}
	lease, err := mcpsignin.ReadCallbackLease(record.options.callbackRoot(), 3119)
	if err != nil || lease.Generation != record.plan.LaunchInputMCP.SignIn.CallbackGeneration {
		t.Fatal("unaffected lease lost", lease, err)
	}
	row := mcpsignin.ObserveCallback(record.options.callbackRoot(), record.plan.LaunchInputMCP.SignIn, "second-alias")
	if row.Disposition != "owner" || row.Port != 3119 {
		t.Fatal("remaining publication not observed", row)
	}
	if _, err := mcpsignin.ReadCallbackLease(record.options.callbackRoot(), 3118); !os.IsNotExist(err) {
		t.Fatal("affected lease retained", err)
	}
}

func TestManagedMCPCallbackUnrelatedEarlyFailureDoesNotRetry(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	record := f.Start("unrelated", false, true, false)
	attempts, err := os.ReadFile(filepath.Join(record.root, "attempts"))
	if err != nil {
		t.Fatal(err)
	}
	if record.rc != 125 || len(strings.Split(strings.TrimSpace(string(attempts)), "\n")) != 1 {
		t.Fatalf("unrelated runtime failure became success/retry: rc=%d %s", record.rc, attempts)
	}
	if _, err := mcpsignin.ReadCallbackLease(record.options.callbackRoot(), 3118); !os.IsNotExist(err) {
		t.Fatal("confirmed absent failed create retained lease", err)
	}
}

func TestManagedMCPCallbackUnknownRuntimeDoesNotBlindlyRetryOrRelease(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	f.beforeSpawn = func(record *callbackRun) { f.mu.Lock(); record.liveness = "unknown"; f.mu.Unlock() }
	record := f.Start("unknown", true, false, false)
	attempts, err := os.ReadFile(filepath.Join(record.root, "attempts"))
	if err != nil {
		t.Fatal(err)
	}
	if record.rc != 125 || len(strings.Split(strings.TrimSpace(string(attempts)), "\n")) != 1 {
		t.Fatal("unknown runtime blindly retried", record.rc, string(attempts))
	}
	if lease, err := mcpsignin.ReadCallbackLease(record.options.callbackRoot(), 3118); err != nil || lease.Generation != record.options.mcpCallbackGeneration {
		t.Fatal("unknown runtime lease removed", lease, err)
	}
}

func TestManagedMCPCallbackDisabledOrUndeliveredNeverAcquires(t *testing.T) {
	for _, name := range []string{"default false", "no port", "workspace redirect", "workspace-only bundle", "user publication", "dry run", "host network"} {
		t.Run(name, func(t *testing.T) {
			f := callbackMCPLaunchFixture(t)
			path := filepath.Join(f.bundle, "pack.toml")
			if name == "default false" {
				body, _ := os.ReadFile(path)
				body = []byte(strings.ReplaceAll(string(body), "callback_route=true", "callback_route=false"))
				if err := os.WriteFile(path, body, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if name == "no port" {
				path = filepath.Join(f.bundle, "skills/original/.mcp.json")
				body, _ := os.ReadFile(path)
				body = []byte(strings.ReplaceAll(string(body), `,"callbackPort":3118`, ""))
				if err := os.WriteFile(path, body, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if name == "user publication" {
				path = filepath.Join(f.home, ".config/yolo-jail/config.toml")
				body, _ := os.ReadFile(path)
				body = append(body, []byte("\n[machine]\nports=[{host=3118,jail=9999}]\n")...)
				if err := os.WriteFile(path, body, 0644); err != nil {
					t.Fatal(err)
				}
			}
			if name == "workspace-only bundle" {
				path = filepath.Join(f.home, ".config/yolo-jail/config.toml")
				body, _ := os.ReadFile(path)
				body = []byte(strings.ReplaceAll(string(body), ",\"fixture/"+filepath.Base(f.bundle)+"\"", ""))
				if err := os.WriteFile(path, body, 0644); err != nil {
					t.Fatal(err)
				}
			}
			f.tune = func(_ *callbackRun, o *Options) {
				if name == "workspace-only bundle" {
					body := fmt.Sprintf("packs=[%q]\n", "fixture/"+filepath.Base(f.bundle))
					if err := os.WriteFile(filepath.Join(o.Workspace, "yolo-jail.toml"), []byte(body), 0644); err != nil {
						t.Fatal(err)
					}
				}
				if name == "dry run" {
					o.DryRun = true
				}
				if name == "host network" {
					o.Network = "host"
				}
				if name == "workspace redirect" {
					path := filepath.Join(o.Workspace, "yolo-jail.toml")
					if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte("[mcp_servers.configured-alias]\nurl=\"https://redirect.invalid/mcp\"\n"), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			record := f.Start("without-callback", false, false, false)
			if record.acquires != 0 || record.releases != 0 {
				t.Fatal("noneligible launch attempted lease operation", record.acquires, record.releases)
			}
			if name == "dry run" {
				if f.spawns != 0 {
					t.Fatal("dry run created runtime")
				}
				if _, err := os.Stat(record.options.callbackRoot()); !os.IsNotExist(err) {
					t.Fatal("dry run wrote callback state", err)
				}
				return
			}
			if name == "workspace-only bundle" {
				if record.rc != 1 || f.spawns != 0 || !strings.Contains(record.errs.String(), "user-scope only") {
					t.Fatal("workspace selection bypassed existing authority refusal", record.rc, record.errs.String())
				}
				return
			}
			if record.rc != 0 {
				t.Fatalf("plain convenience branch failed: %d %s %s", record.rc, record.out.String(), record.errs.String())
			}
			if slices.Contains(record.plan.RunCmd, "127.0.0.1:3118:3118/tcp") || len(record.plan.CallbackLeases) != 0 {
				t.Fatal("unauthorized optional bind", record.plan.CallbackLeases)
			}
			if _, err := os.Stat(mcpsignin.CallbackLeasePath(record.options.callbackRoot(), 3118)); !os.IsNotExist(err) {
				t.Fatal("unexpected acquisition", err)
			}
		})
	}
}

func TestManagedMCPCallbackIndependentForgetFindsOnlyConfirmedGoneOwnedLeases(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	record := f.Start("retire", false, false, true)
	if record.rc != 0 {
		t.Fatal(record.rc, record.errs.String())
	}
	reaper := callbackLeaseOptions(t, record.options.callbackRoot(), true, "")
	reaper.Exec = f.exec
	other := mcpsignin.CallbackLease{Version: 1, Port: 3119, Launch: "other-owner", Runtime: "podman", Generation: "other-generation"}
	if disposition, _ := reaper.acquireCallbackLease(other); disposition != "publication pending" {
		t.Fatal(disposition)
	}
	for _, state := range []string{"live", "unknown"} {
		f.mu.Lock()
		record.liveness = state
		f.mu.Unlock()
		reaper.forgetGoneContainer(record.plan.Cname, "podman", "")
		if _, err := mcpsignin.ReadCallbackLease(reaper.callbackRoot(), 3118); err != nil {
			t.Fatal("independent live/unknown retirement", state, err)
		}
	}
	f.mu.Lock()
	record.liveness = "gone"
	f.mu.Unlock()
	reaper.forgetGoneContainer(record.plan.Cname, "podman", "")
	if _, err := mcpsignin.ReadCallbackLease(reaper.callbackRoot(), 3118); !os.IsNotExist(err) {
		t.Fatal("independent known-gone retained lease", err)
	}
	if lease, err := mcpsignin.ReadCallbackLease(reaper.callbackRoot(), 3119); err != nil || lease != other {
		t.Fatal("unrelated owner retired", lease, err)
	}
}

func TestManagedMCPCallbackIsOptionalExclusiveAndLoopback(t *testing.T) {
	f := callbackMCPLaunchFixture(t)
	first := f.Start("first", false, false, true)
	if first.rc != 0 || !slices.Contains(first.plan.RunCmd, "127.0.0.1:3118:3118/tcp") {
		t.Fatalf("owner publication missing: rc=%d %s %s", first.rc, first.out.String(), first.errs.String())
	}
	for i, arg := range first.plan.RunCmd {
		if arg == "-p" && (i+1 >= len(first.plan.RunCmd) || first.plan.RunCmd[i+1] != "127.0.0.1:3118:3118/tcp") {
			t.Fatal("callback widened binding", first.plan.RunCmd)
		}
	}
	if input, _ := envArgValue(first.plan.RunCmd, "YOLO_PUBLISHED_PORTS"); input != `["3118/tcp"]` {
		t.Fatal("owner DNAT input missing")
	}
	if !strings.HasPrefix(first.pending, "publication pending ") || !strings.Contains(first.pending, "full callback URL") {
		t.Fatal("start has not proved publication", first.pending)
	}
	if !strings.HasPrefix(f.Status("first"), "owner ") {
		t.Fatal(f.Status("first"))
	}
	second := f.Start("second", false, false, false)
	if second.rc != 0 || slices.Contains(second.plan.RunCmd, "127.0.0.1:3118:3118/tcp") || !strings.Contains(second.errs.String(), "held by "+first.plan.Cname) || !strings.Contains(f.Status("second"), "held by "+first.plan.Cname) {
		t.Fatalf("second stole callback: %d %s", second.rc, f.Status("second"))
	}
	lease, err := mcpsignin.ReadCallbackLease(first.options.callbackRoot(), 3118)
	if err != nil {
		t.Fatal(err)
	}
	before := lease
	f.mu.Lock()
	first.liveness = "unknown"
	f.mu.Unlock()
	first.options.forgetGoneContainer(first.plan.Cname, "podman", "")
	lease, err = mcpsignin.ReadCallbackLease(first.options.callbackRoot(), 3118)
	if err != nil || !reflect.DeepEqual(before, lease) {
		t.Fatal("unknown runtime released lease")
	}
	f.mu.Lock()
	first.liveness = "gone"
	f.mu.Unlock()
	first.options.forgetGoneContainer(first.plan.Cname, "podman", "")
	if _, err := mcpsignin.ReadCallbackLease(first.options.callbackRoot(), 3118); !os.IsNotExist(err) {
		t.Fatal("confirmed gone retained lease", err)
	}
	f.foreign = true
	third := f.Start("third", false, false, false)
	if third.rc != 0 || slices.Contains(third.plan.RunCmd, "127.0.0.1:3118:3118/tcp") || !strings.Contains(f.Status("third"), "port busy") || !strings.Contains(f.Status("third"), "full callback URL") {
		t.Fatalf("foreign busy convenience failed launch: %d %s", third.rc, f.Status("third"))
	}
}

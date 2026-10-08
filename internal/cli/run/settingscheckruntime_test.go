package run

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/image"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func settingsCheckChildMain(settingsPath, markerPath, marker string) int {
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "settings fixture could not read snapshot")
		return 2
	}
	if err := os.WriteFile(markerPath, []byte(marker+"|"+settingsPath+"|"+string(data)), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "settings fixture could not write marker")
		return 2
	}
	return 0
}

func settingsRewriteCheckChildMain(settingsPath, markerPath string) int {
	if code := settingsCheckChildMain(settingsPath, markerPath, "validator-before-rewrite"); code != 0 {
		return code
	}
	if err := os.WriteFile(settingsPath, []byte(`{"profile":"validator-mutated"}`), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "settings fixture could not mutate its private input")
		return 3
	}
	return 0
}

func settingsSnapshotDaemonChildMain(socketPath, settingsPath, markerPath string) int {
	if code := settingsCheckChildMain(settingsPath, markerPath, "daemon"); code != 0 {
		return code
	}
	return frontUpstreamChildMain("line", socketPath)
}

func settingsInterleaveDaemonChildMain(socketPath, settingsPath, coordDir string) int {
	hash := sha256.Sum256([]byte(settingsPath))
	prefix := filepath.Join(coordDir, hex.EncodeToString(hash[:8]))
	startedPath, releasePath, resultPath := prefix+".started", prefix+".release", prefix+".result"
	if err := os.WriteFile(startedPath, []byte(settingsPath), 0o600); err != nil {
		return 2
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(releasePath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return 3
		}
		time.Sleep(10 * time.Millisecond)
	}
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		return 4
	}
	if err := os.WriteFile(resultPath, append([]byte(settingsPath+"\n"), data...), 0o600); err != nil {
		return 5
	}
	return frontUpstreamChildMain("line", socketPath)
}

func settingsRefusalChildMain(settingsPath string) int {
	if _, err := os.ReadFile(settingsPath); err != nil {
		fmt.Fprintln(os.Stderr, `{"reason":"settings snapshot unreadable","remedy":"check the profile"}`)
		return 2
	}
	fmt.Fprintln(os.Stdout, `{"reason":"The AWS profile is not usable.","remedy":"Run aws sso login --profile <profile>."}`)
	return 1
}

func TestInvalidSingletonCandidatePreservesLiveDaemonAndExistingFront(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	oldSingletonDir := paths.HostSingletonDir
	shortSingletonDir, err := os.MkdirTemp("/tmp", "yhs-")
	if err != nil {
		t.Fatal(err)
	}
	paths.HostSingletonDir = shortSingletonDir
	t.Cleanup(func() {
		paths.HostSingletonDir = oldSingletonDir
		_ = os.RemoveAll(shortSingletonDir)
	})
	module := filepath.Join(t.TempDir(), "live-singleton")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{
  "name": "live-singleton",
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "good"}},
  "host_daemon": {
    "cmd": [%q, "-front-upstream-child", "line", "{socket}", "--settings", "{settings}"],
    "settings_check": ["/bin/sh", "-c", "if grep -q invalid \"$1\"; then printf '%%s' '{\"reason\":\"candidate rejected\",\"remedy\":\"correct user settings\"}'; exit 1; fi; exit 0", "validator", "{settings}"],
    "publishes": "socket",
    "scope": "host"
  }
}`, os.Args[0])
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	allow := func(string) bool { return true }

	deps := broker.SingletonDeps("live-singleton", nil)
	valid := settingsConfig("live-singleton", "valid-profile")
	first := &Options{Workspace: t.TempDir()}
	var output strings.Builder
	first.Stdout, first.Stderr = &output, &output
	fillDefaults(first)
	first.ServiceReadyTimeout = 3 * time.Second
	first.PathExists = func(string) bool { return false }
	first.discloseSettingsCheckHostExec(nil, set, valid, allow)
	if !first.prepareLoopholeSettingsForStart(set, valid, allow) {
		t.Fatalf("valid initial settings refused: %v", first.startupRefusal)
	}
	cname := "yolo-singleton-preserve-" + fmt.Sprint(time.Now().UnixNano())
	handles := first.startLoopholesMatching(set, cname, "podman", valid, allow)
	if len(handles) != 1 {
		logData, _ := os.ReadFile(deps.LogPath)
		t.Fatalf("initial host singleton did not start: handles=%d refusal=%v output=%s log=%s", len(handles), first.startupRefusal, output.String(), logData)
	}
	defer first.stopLoopholes(handles, hostServiceSocketsDir(cname, false), cname, "podman")
	t.Cleanup(func() { _ = broker.BrokerKill(deps, syscall.SIGTERM, 2*time.Second) })
	pid, ok := broker.BrokerReadPID(deps)
	if !ok || !broker.BrokerIsAlive(deps) {
		t.Fatalf("initial host singleton is not live: pid=%d ok=%v", pid, ok)
	}
	stable := loopholes.SettingsFileFor("live-singleton")
	before, err := os.ReadFile(stable)
	if err != nil {
		t.Fatal(err)
	}
	frontPath := handles[0].hostPath
	frontBefore, err := os.ReadFile(frontPath)
	if err != nil {
		t.Fatalf("initial jail front endpoint is absent: %v", err)
	}

	invalid := settingsConfig("live-singleton", "invalid-profile")
	plan := &launchservice.Plan{Declared: launchservice.Declared{Service: "live-singleton", Pack: "fixture"}}
	doorways := &HostDoorways{plans: []*launchservice.Plan{plan}, set: set}
	startCalled := false
	_, stop, _, refusal := doorways.Start(invalid, t.TempDir(), "pi", &output,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			startCalled = true
			return nil, fmt.Errorf("fixture doorway should not start after refusal")
		})
	if stop != nil {
		stop()
	}
	if refusal == nil || startCalled || !strings.Contains(refusal.Error(), "candidate rejected") {
		t.Fatalf("host doorway did not propagate settings refusal before starting: refusal=%v startCalled=%v", refusal, startCalled)
	}
	if after, err := os.ReadFile(stable); err != nil || string(after) != string(before) {
		t.Fatalf("invalid candidate changed singleton settings: before=%q after=%q err=%v", before, after, err)
	}
	if after, ok := broker.BrokerReadPID(deps); !ok || after != pid || !broker.BrokerIsAlive(deps) {
		t.Fatalf("invalid candidate disturbed live singleton: prior pid=%d current=%d ok=%v", pid, after, ok)
	}
	if !socketConnectable(paths.HostSingletonSocket("live-singleton"), time.Second) || !broker.BrokerIsAlive(deps) {
		t.Fatal("invalid candidate made the existing singleton socket unreachable")
	}
	if frontAfter, err := os.ReadFile(frontPath); err != nil || string(frontAfter) != string(frontBefore) {
		t.Fatalf("invalid candidate disturbed the existing jail front: before=%q after=%q err=%v", frontBefore, frontAfter, err)
	}
}

func TestManifestSettingsRefusalPreservesHostSingletonSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	t.Setenv("HOME", t.TempDir())
	module := filepath.Join(t.TempDir(), "refused")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := fmt.Sprintf(`{
  "name": "refused",
  "description": "refusal fixture",
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "default": "old"}},
  "host_daemon": {
    "cmd": [%q, "-front-upstream-child", "line", "{socket}", "--settings", "{settings}"],
    "settings_check": [%q, "-settings-refusal-child", "{settings}"],
    "publishes": "socket",
    "scope": "host"
  }
}`, os.Args[0], os.Args[0])
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	stable := loopholes.SettingsFileFor("refused")
	before := []byte("{\"profile\": \"previous-valid\"}\n")
	if err := loopholes.WriteSettingsBytes(stable, before); err != nil {
		t.Fatal(err)
	}
	settings := jsonx.NewOrderedMap()
	settings.Set("profile", "invalid-candidate")
	entry := jsonx.NewOrderedMap()
	entry.Set("settings", settings)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set("refused", entry)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("loopholes", loopCfg)
	var output strings.Builder
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	o.PathExists = func(string) bool { return false }

	handles := o.startLoopholesMatching(set, "yolo-refusal", "podman", cfg,
		func(string) bool { return true })
	if len(handles) != 0 || o.startupRefusal == nil {
		t.Fatalf("invalid candidate was not refused before startup: handles=%v refusal=%v output=%s",
			handles, o.startupRefusal, output.String())
	}
	got, err := os.ReadFile(stable)
	if err != nil || string(got) != string(before) {
		t.Fatalf("stable singleton settings changed on refusal: got=%q err=%v", got, err)
	}
	if strings.Contains(output.String(), "invalid-candidate") || strings.Contains(output.String(), "<profile>") {
		t.Fatalf("refusal leaked profile or placeholder: %s", output.String())
	}
}

func TestPerJailSettingsSnapshotsRemainIsolatedAcrossInterleavedStarts(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns host processes")
	}
	t.Setenv("HOME", t.TempDir())
	module := filepath.Join(t.TempDir(), "interleaved")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	coordDir := t.TempDir()
	manifest := fmt.Sprintf(`{
  "name": "interleaved",
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": [%q, "-settings-interleave-child", "{socket}", "{settings}", %q],
    "settings_check": [%q, "-settings-check-child", "{settings}", %q],
    "publishes": "socket"
  }
}`, os.Args[0], coordDir, os.Args[0], filepath.Join(t.TempDir(), "validator.marker"))
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	// Each workspace uses the same declaration and pack code but a different settings input.
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != "interleaved" {
		t.Fatalf("enabled fixture loopholes = %+v", enabled)
	}
	type startResult struct {
		o       *Options
		handles []loopholeDaemon
		refusal *hostStartupRefusal
	}
	startedResult := make([]chan startResult, 2)
	cnames := []string{"yolo-interleave-one-" + fmt.Sprint(time.Now().UnixNano()),
		"yolo-interleave-two-" + fmt.Sprint(time.Now().UnixNano())}
	for i := range startedResult {
		startedResult[i] = make(chan startResult, 1)
	}
	received := [2]bool{}
	var results [2]startResult
	cleanup := func() {
		entries, _ := os.ReadDir(coordDir)
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".started") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(coordDir, entry.Name()))
			if err != nil {
				continue
			}
			hash := sha256.Sum256(data)
			_ = os.WriteFile(filepath.Join(coordDir, hex.EncodeToString(hash[:8])+".release"), []byte("go"), 0o600)
		}
		for i := range startedResult {
			if !received[i] {
				select {
				case results[i] = <-startedResult[i]:
				case <-time.After(12 * time.Second):
					continue
				}
			}
			if results[i].o != nil && len(results[i].handles) > 0 {
				results[i].o.stopLoopholes(results[i].handles, hostServiceSocketsDir(cnames[i], false), cnames[i], "podman")
			}
		}
	}
	t.Cleanup(cleanup)
	startOne := func(i int) {
		cfg := jsonx.NewOrderedMap()
		settings := jsonx.NewOrderedMap()
		profile := []string{"profile-workspace-one", "profile-workspace-two"}[i]
		settings.Set("profile", profile)
		entry := jsonx.NewOrderedMap()
		entry.Set("settings", settings)
		loopCfg := jsonx.NewOrderedMap()
		loopCfg.Set("interleaved", entry)
		cfg.Set("loopholes", loopCfg)
		o := &Options{Workspace: t.TempDir()}
		fillDefaults(o)
		o.ServiceReadyTimeout = 3 * time.Second
		o.PathExists = func(string) bool { return false }
		handles := o.startLoopholesMatching(set, cnames[i], "podman", cfg, func(string) bool { return true })
		startedResult[i] <- startResult{o: o, handles: handles, refusal: o.startupRefusal}
	}
	go startOne(0)
	waitForStartedCount(t, coordDir, 1)
	go startOne(1)
	waitForStartedCount(t, coordDir, 2)
	started, err := os.ReadDir(coordDir)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for _, entry := range started {
		if !strings.HasSuffix(entry.Name(), ".started") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(coordDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, string(data))
	}
	if len(paths) != 2 {
		t.Fatalf("started daemons = %d, want 2: %v", len(paths), started)
	}
	if paths[0] == paths[1] || paths[0] == loopholes.SettingsFileFor("interleaved") ||
		paths[1] == loopholes.SettingsFileFor("interleaved") {
		t.Fatalf("two starts did not own distinct private settings paths: %q, %q", paths[0], paths[1])
	}
	for _, path := range paths {
		hash := sha256.Sum256([]byte(path))
		releasePath := filepath.Join(coordDir, hex.EncodeToString(hash[:8])+".release")
		if err := os.WriteFile(releasePath, []byte("go"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := range startedResult {
		out := <-startedResult[i]
		received[i], results[i] = true, out
		if out.refusal != nil || len(out.handles) != 1 {
			t.Fatalf("start %d refused or did not start: handles=%d refusal=%v", i, len(out.handles), out.refusal)
		}
	}
	entries, err := os.ReadDir(coordDir)
	if err != nil {
		t.Fatal(err)
	}
	seenPaths, seenProfiles := map[string]bool{}, map[string]bool{}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".result") {
			continue
		}
		got, err := os.ReadFile(filepath.Join(coordDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		parts := strings.SplitN(string(got), "\n", 2)
		if len(parts) != 2 || parts[0] == loopholes.SettingsFileFor("interleaved") || seenPaths[parts[0]] {
			t.Fatalf("daemon attempts shared or misidentified a snapshot path: %q", got)
		}
		seenPaths[parts[0]] = true
		for _, profile := range []string{"profile-workspace-one", "profile-workspace-two"} {
			if strings.Contains(parts[1], `"profile": "`+profile+`"`) {
				seenProfiles[profile] = true
			}
		}
	}
	if len(seenPaths) != 2 || len(seenProfiles) != 2 {
		t.Fatalf("interleaved daemon snapshots did not remain isolated: paths=%v profiles=%v", seenPaths, seenProfiles)
	}
}

func waitForStartedCount(t *testing.T, dir string, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		if err == nil {
			count := 0
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".started") {
					count++
				}
			}
			if count == want {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d fixture daemons to pause", want)
}

func TestKeeperStartsWithTheLaunchValidatedSettingsSnapshot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	emptyLoopholeDirs(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	module := filepath.Join(t.TempDir(), "keeper-settings")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	validatorMarker := filepath.Join(t.TempDir(), "validator.marker")
	daemonMarker := filepath.Join(t.TempDir(), "daemon.marker")
	manifest := fmt.Sprintf(`{
  "name": "keeper-settings",
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": [%q, "-settings-snapshot-daemon", "{socket}", "{settings}", %q],
    "settings_check": [%q, "-settings-check-child", "{settings}", %q],
    "publishes": "socket"
  }
}`, os.Args[0], daemonMarker, os.Args[0], validatorMarker)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, HostExecApproved: true}})
	original := settingsConfig("keeper-settings", "validated-profile")
	set := loopholes.NewHostSet(cfgMap(original, "loopholes"))
	launch := &Options{Workspace: t.TempDir()}
	fillDefaults(launch)
	launch.discloseSettingsCheckHostExec(nil, set, original, func(string) bool { return true })
	if !launch.prepareLoopholeSettingsForStart(set, original, func(string) bool { return true }) {
		t.Fatalf("initial settings preflight refused: %v", launch.startupRefusal)
	}
	frozen := launch.frozenLoopholeSettings()
	if len(frozen["keeper-settings"]) == 0 {
		t.Fatal("launch did not freeze settings for keeper transport")
	}

	// The keeper receives a later decoded config value, but its preflight result must
	// remain the exact immutable bytes whose validator already passed in the launch.
	changed := settingsConfig("keeper-settings", "changed-after-preflight")
	plan := &keeperPlan{Workspace: t.TempDir(), Cname: "yolo-keeper-settings-" + fmt.Sprint(time.Now().UnixNano()),
		Runtime: "podman", Settings: frozen, SettingsPrepared: true}
	k := newKeeper(plan, KeeperSeams{}, nil, nil, nil, nil)
	k.o.ServiceReadyTimeout = 3 * time.Second
	k.o.PathExists = func(string) bool { return false }
	handles, refused := k.o.startPlannedLoopholes(plan.Cname, "podman", changed, nil)
	if refused != nil || len(handles) != 1 {
		t.Fatalf("keeper start refused or missed its daemon: handles=%d refusal=%v", len(handles), refused)
	}
	t.Cleanup(func() {
		k.o.stopLoopholes(handles, hostServiceSocketsDir(plan.Cname, false), plan.Cname, "podman")
	})
	validatorResult, err := os.ReadFile(validatorMarker)
	if err != nil || !strings.Contains(string(validatorResult), `"profile": "validated-profile"`) ||
		strings.Contains(string(validatorResult), "changed-after-preflight") {
		t.Fatalf("keeper reran validation on changed config: result=%q err=%v", validatorResult, err)
	}
	daemonResult, err := os.ReadFile(daemonMarker)
	if err != nil || !strings.Contains(string(daemonResult), `"profile": "validated-profile"`) ||
		strings.Contains(string(daemonResult), "changed-after-preflight") {
		t.Fatalf("keeper did not publish its frozen validated settings: result=%q err=%v", daemonResult, err)
	}
}

func settingsConfig(name, profile string) *jsonx.OrderedMap {
	settings := jsonx.NewOrderedMap()
	settings.Set("profile", profile)
	entry := jsonx.NewOrderedMap()
	entry.Set("settings", settings)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set(name, entry)
	return newConfig("loopholes", loopCfg)
}

func TestManifestSettingsCheckUsesAndRetainsLaunchPrivateSnapshot(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	module := filepath.Join(t.TempDir(), "checked")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	validatorMarker := filepath.Join(t.TempDir(), "validator.marker")
	daemonMarker := filepath.Join(t.TempDir(), "daemon.marker")
	manifest := fmt.Sprintf(`{
  "name": "checked",
  "description": "private settings fixture",
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "default": "old"}},
  "host_daemon": {
    "cmd": [%q, "-settings-snapshot-daemon", "{socket}", "{settings}", %q],
    "settings_check": [%q, "-settings-rewrite-check-child", "{settings}", %q],
    "publishes": "socket"
  }
}`, os.Args[0], daemonMarker, os.Args[0], validatorMarker)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != "checked" {
		t.Fatalf("enabled = %+v, want checked fixture", enabled)
	}
	settings := jsonx.NewOrderedMap()
	settings.Set("profile", "candidate")
	entry := jsonx.NewOrderedMap()
	entry.Set("settings", settings)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set("checked", entry)
	cfg := jsonx.NewOrderedMap()
	cfg.Set("loopholes", loopCfg)

	cname := "yolo-settings-check-" + fmt.Sprintf("%d", time.Now().UnixNano())
	var output strings.Builder
	o := &Options{Workspace: t.TempDir(), IsMacOS: false}
	fillDefaults(o)
	o.Stdout = &output
	o.Stderr = &output
	o.ServiceReadyTimeout = 3 * time.Second
	o.PathExists = func(string) bool { return false }
	handles := o.startLoopholesMatching(set, cname, "podman", cfg, func(string) bool { return true })
	if len(handles) != 1 {
		t.Fatalf("started %d services, want one: %s", len(handles), output.String())
	}
	t.Cleanup(func() {
		o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), cname, "podman")
	})

	validatorResult, err := os.ReadFile(validatorMarker)
	if err != nil {
		t.Fatalf("validator was not invoked: %v; output=%s", err, output.String())
	}
	if !strings.Contains(string(validatorResult), `"profile": "candidate"`) || strings.Contains(string(validatorResult), `"profile": "old"`) {
		t.Fatalf("validator saw unexpected settings snapshot: %q", validatorResult)
	}
	if !strings.Contains(string(validatorResult), "validator-before-rewrite") {
		t.Fatalf("validator fixture did not report its input mutation: %q", validatorResult)
	}
	daemonResult, err := os.ReadFile(daemonMarker)
	if err != nil {
		t.Fatalf("daemon did not receive a settings file: %v; output=%s", err, output.String())
	}
	parts := strings.SplitN(string(daemonResult), "|", 3)
	if len(parts) != 3 || parts[0] != "daemon" {
		t.Fatalf("daemon marker = %q", daemonResult)
	}
	stable := loopholes.SettingsFileFor("checked")
	if parts[1] == stable {
		t.Fatalf("per-jail daemon was handed shared stable settings path %q", parts[1])
	}
	if !strings.Contains(parts[2], `"profile": "candidate"`) ||
		strings.Contains(parts[2], `"profile": "validator-mutated"`) {
		t.Fatalf("daemon received validator-mutated or stale settings: %q", parts[2])
	}
	if _, err := os.Stat(parts[1]); err != nil {
		t.Fatalf("owned private snapshot was removed before service teardown: %v", err)
	}
	o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), cname, "podman")
	handles = nil
	if _, err := os.Stat(parts[1]); !os.IsNotExist(err) {
		t.Fatalf("private settings snapshot survived service teardown: %v", err)
	}
}

func TestPrepareSettingsValidatorAdmissionAtLaunchCaller(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a Linux shell validator")
	}
	for _, tc := range []struct {
		name        string
		approved    bool
		disabled    bool
		platform    string
		superseded  bool
		inWorkspace bool
		allow       string
		admit       bool
	}{
		{name: "selected fresh launch", approved: true, allow: "podman", admit: true},
		{name: "unselected service", approved: true, allow: "none"},
		{name: "unresolved pack approval", approved: false, allow: "podman"},
		{name: "disabled", approved: true, disabled: true, allow: "podman"},
		{name: "unsupported platform", approved: true, platform: "darwin", allow: "podman"},
		{name: "superseded", approved: true, superseded: true, allow: "podman"},
		{name: "agent-editable module", approved: true, inWorkspace: true, allow: "podman"},
		{name: "unsupported container backend", approved: true, allow: "container"},
		{name: "excluded allow selection", approved: true, allow: "exclude"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			marker := filepath.Join(t.TempDir(), "validator-marker")
			workspace := t.TempDir()
			module := filepath.Join(t.TempDir(), "admission-fixture")
			if tc.inWorkspace {
				module = filepath.Join(workspace, "packs", "fixture", "loopholes", "admission-fixture")
			}
			if err := os.MkdirAll(module, 0o700); err != nil {
				t.Fatal(err)
			}
			platforms := ""
			if tc.platform != "" {
				platforms = fmt.Sprintf(`,"platforms":[%q]`, tc.platform)
			}
			serves := ""
			var claims []loopholes.PackSupersession
			if tc.superseded {
				serves = `,"serves":["admission-capability"]`
				claims = []loopholes.PackSupersession{{Pack: "replacement", Capability: "admission-capability"}}
			}
			validator := `grep -q current-profile "$1" && printf x >> ` + shquote.Quote(marker)
			manifest := fmt.Sprintf(`{
  "name":"admission-fixture", "default_enabled":true, "transport":"none"%s%s,
  "settings":{"profile":{"type":"string","default":"default-profile"}},
  "host_daemon":{
    "cmd":[%q,"-settings-snapshot-daemon","{socket}","{settings}",%q],
    "settings_check":["/bin/sh","-c",%q,"validator","{settings}"],
    "publishes":"socket"
  }
}`, platforms, serves, os.Args[0], marker, validator)
			if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
				t.Fatal(err)
			}
			config := settingsConfig("admission-fixture", "current-profile")
			var moduleConfig *jsonx.OrderedMap
			if tc.disabled {
				moduleConfig = newConfig("admission-fixture", newConfig("enabled", false))
			}
			set := loopholes.NewSet(loopholes.DiscoverOptions{
				LoopholesConfig:   moduleConfig,
				PackModules:       []loopholes.PackModule{{Dir: module, HostExecApproved: tc.approved}},
				PackSupersessions: claims,
			})
			allow := func(name string) bool {
				switch tc.allow {
				case "podman":
					return true
				case "container":
					return name == openAIAuthBrokerName
				default:
					return false
				}
			}
			o := &Options{Workspace: workspace}
			fillDefaults(o)
			if !o.prepareLoopholeSettingsForStart(set, config, allow) {
				t.Fatalf("settings preparation unexpectedly refused: %v", o.startupRefusal)
			}
			o.cleanupSettingsSnapshots()
			data, err := os.ReadFile(marker)
			if tc.admit {
				if err != nil || string(data) != "x" {
					t.Fatalf("admitted validator did not run exactly once against current settings: marker=%q err=%v", data, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("inert validator ran despite its launch admission gate: marker=%q err=%v", data, err)
			}
		})
	}
}

func writeSettingsValidatorModePack(t *testing.T, home, marker string) {
	t.Helper()
	validator := `grep -q current-profile "$1" && printf x >> ` + shquote.Quote(marker)
	daemonMarker := filepath.Join(t.TempDir(), "settings-daemon-ran")
	manifest := fmt.Sprintf(`{
  "name":"settings-mode",
  "description":"settings validation mode fixture",
  "transport":"loopback-tls",
  "default_enabled":true,
  "settings":{"profile":{"type":"string","scope":"user","default":"old-profile"}},
  "host_daemon":{
    "cmd":[%q,"-settings-snapshot-daemon","{socket}","{settings}",%q],
    "settings_check":["/bin/sh","-c",%q,"validator","{settings}"],
    "publishes":"socket"
  }
}`, os.Args[0], daemonMarker, validator)
	writeLocalLoopholePack(t, home, "settings-mode", manifest)
	writeUserConfigJSON(t, home, `{
  "packs": [],
  "loopholes": {"settings-mode":{"enabled":true,"settings":{"profile":"current-profile"}}}
}`)
}

func TestAttachRunDoesNotRevalidateChangedSelectedSettings(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the attach fixture uses a fake podman runtime")
	}
	home := packHome(t)
	workspace := t.TempDir()
	marker := filepath.Join(t.TempDir(), "settings-validator-ran")
	writeSettingsValidatorModePack(t, home, marker)
	writeUserConfigJSON(t, home, `{
  "packs": [],
  "loopholes": {"settings-mode":{"enabled":true,"settings":{"profile":"prior-profile"}}}
}`)

	cname := yoloruntime.FromWorkspace(workspace)
	var stagingOutput bytes.Buffer
	staging := dispatchOptions(t, workspace, "podman", &stagingOutput, &stagingOutput, nil)
	cfg, ok := staging.loadAndValidateConfig()
	if !ok {
		t.Fatalf("initial config did not validate:\n%s", stagingOutput.String())
	}
	staging.stagingCfg = cfg
	staged, ok := staging.stageRunPacks(cname)
	if !ok {
		t.Fatalf("initial selected pack did not stage:\n%s", stagingOutput.String())
	}
	if err := writeLivePackTree(cname, staged.root); err != nil {
		t.Fatalf("record running jail's pack tree: %v", err)
	}

	// This is a config edit made while the existing jail is running. It would be a
	// validator input on a fresh launch; attach must keep the running service and must
	// not validate the replacement settings against that jail.
	writeUserConfigJSON(t, home, `{
  "packs": [],
  "loopholes": {"settings-mode":{"enabled":true,"settings":{"profile":"current-profile"}}}
}`)
	r := attachThroughRun(t, workspace, func(o *Options) { o.AcceptConfigChanges = true })
	if !strings.Contains(r.execArgv, "exec") {
		t.Fatalf("Run did not reach the existing jail's attach command: argv=%q\n%s\n%s", r.execArgv, r.stdout, r.stderr)
	}
	if data, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("attach revalidated changed settings for the running jail: marker=%q err=%v\n%s", data, err, r.stdout+r.stderr)
	}
}

func TestSealedRunDoesNotValidateOrStartSelectedHostService(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a fake podman runtime and Linux shell validator")
	}
	marker := filepath.Join(t.TempDir(), "settings-validator-ran")
	daemonMarker := filepath.Join(t.TempDir(), "settings-daemon-ran")
	manifest := fmt.Sprintf(`{
  "name":"sealed-settings", "description":"sealed validator fixture",
  "transport":"loopback-tls", "default_enabled":true,
  "settings":{"profile":{"type":"string","scope":"user","default":"old-profile"}},
  "host_daemon":{
    "cmd":[%q,"-settings-snapshot-daemon","{socket}","{settings}",%q],
    "settings_check":["/bin/sh","-c",%q,"validator","{settings}"],
    "publishes":"socket"
  }
}`, os.Args[0], daemonMarker,
		`grep -q current-profile "$1" && printf x >> `+shquote.Quote(marker))
	fixture := sealFixture{
		files: map[string]string{
			".config/yolo-jail/local/pack.json":                                `{"contributes":[{"kind":"loophole","from":"loopholes/sealed-settings"}]}`,
			".config/yolo-jail/local/loopholes/sealed-settings/manifest.jsonc": manifest,
		},
		config: `{"packs":[],"loopholes":{"sealed-settings":{"enabled":true,"settings":{"profile":"current-profile"}}}}`,
	}
	argv, _, _, printed := fixture.launch(t, true)
	if len(argv) == 0 {
		t.Fatalf("sealed Run did not reach the fake container runtime:\n%s", printed)
	}
	if !strings.Contains(strings.Join(argv, " "), entrypoint.SealedBuildEnv+"=1") {
		t.Fatalf("Run did not take the sealed build path:\nargv=%q\n%s", argv, printed)
	}
	if data, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("sealed build executed a selected host-service settings validator: marker=%q err=%v\n%s", data, err, printed)
	}
	if data, err := os.ReadFile(daemonMarker); !os.IsNotExist(err) {
		t.Fatalf("sealed build started the selected host service: marker=%q err=%v\n%s", data, err, printed)
	}
	for _, value := range argvValues(argv, "-e") {
		if strings.HasPrefix(value, hostServiceEnvVar("sealed-settings")+"=") {
			t.Fatalf("sealed build handed the jail a host-service endpoint: %q\n%s", value, printed)
		}
	}
}

func TestRunAdmissionNegativeCasesDoNotExecuteSelectedSettingsValidator(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the admission pack uses a Linux shell validator")
	}
	for _, tc := range []struct {
		name          string
		selected      bool
		disabled      bool
		unsupportedOS bool
		superseded    bool
		agentEditable bool
	}{
		{name: "unselected pack"},
		{name: "disabled selected service", selected: true, disabled: true},
		{name: "unsupported platform", selected: true, unsupportedOS: true},
		{name: "superseded service", selected: true, superseded: true},
		{name: "agent-editable pack placement", selected: true, agentEditable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			workspace := t.TempDir()
			marker := filepath.Join(t.TempDir(), "settings-validator-ran")
			packDir := t.TempDir()
			if tc.agentEditable {
				packDir = filepath.Join(workspace, "packs", "settings-pack")
			}
			editableValidator := ""
			if tc.agentEditable {
				editableValidator = filepath.Join(workspace, "validator.sh")
			}
			writeSettingsValidatorFilePack(t, packDir, marker, tc.unsupportedOS, tc.superseded, editableValidator)

			packs := `[]`
			if tc.selected {
				packs = `[{"source":"file://` + packDir + `","name":"settings-pack"}]`
			}
			if tc.superseded {
				replacementDir := filepath.Join(t.TempDir(), "replacement")
				if err := os.MkdirAll(replacementDir, 0o755); err != nil {
					t.Fatal(err)
				}
				writePack(t, replacementDir, `{"name":"replacement","supersedes":[{"capability":"settings-capability","because":"fixture replacement"}]}`)
				packs = `[{"source":"file://` + packDir + `","name":"settings-pack"},` +
					`{"source":"file://` + replacementDir + `","name":"replacement"}]`
			}
			enabled := `true`
			if tc.disabled {
				enabled = `false`
			}
			writeUserConfigJSON(t, home, `{"packs":`+packs+`,"loopholes":{"settings-mode":{"enabled":`+enabled+
				`,"settings":{"profile":"current-profile"}}}}`)

			got := macosUserLaunch(t, workspace)
			if got.rc != 0 || got.env == nil {
				t.Fatalf("actual Run did not complete through the macos-user backend: rc=%d env=%v\n%s", got.rc, got.env != nil, got.out)
			}
			if data, err := os.ReadFile(marker); !os.IsNotExist(err) {
				t.Fatalf("actual Run executed a validator behind the %s admission boundary: marker=%q err=%v\n%s", tc.name, data, err, got.out)
			}
			if tc.agentEditable && (!strings.Contains(got.out, "host_daemon.settings_check[1]") ||
				!strings.Contains(got.out, "inside the workspace this launch bind-mounts :rw")) {
				t.Fatalf("actual Run did not report the workspace placement that withheld the validator:\n%s", got.out)
			}
		})
	}
}

func writeSettingsValidatorFilePack(t *testing.T, root, marker string, unsupportedOS, servesCapability bool,
	editableValidator string) {
	t.Helper()
	module := filepath.Join(root, "loopholes", "settings-mode")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	platforms, serves := "", ""
	if unsupportedOS {
		platforms = `,"platforms":["darwin"]`
	}
	if servesCapability {
		serves = `,"serves":["settings-capability"]`
	}
	validator := `grep -q current-profile "$1" && printf x >> ` + shquote.Quote(marker)
	settingsCheck := fmt.Sprintf(`"settings_check":["/bin/sh","-c",%q,"validator","{settings}"]`, validator)
	if editableValidator != "" {
		if err := os.WriteFile(editableValidator, []byte("#!/bin/sh\n"+validator+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		settingsCheck = fmt.Sprintf(`"settings_check":["/bin/sh",%q,"{settings}"]`, editableValidator)
	}
	manifest := fmt.Sprintf(`{
  "name":"settings-mode", "description":"admission boundary fixture",
  "transport":"loopback-tls", "default_enabled":true%s%s,
  "settings":{"profile":{"type":"string","scope":"user","default":"old-profile"}},
  "host_daemon":{
    "cmd":["/bin/false","{settings}"],
    %s,
    "publishes":"socket"
  }
}`, platforms, serves, settingsCheck)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	writePack(t, root, `{"name":"settings-pack","contributes":[{"kind":"loophole","from":"loopholes/settings-mode"}]}`)
}

func TestRunAppleContainerExcludesUnsupportedSettingsValidator(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses an injected Apple Container runtime on Linux")
	}
	home := packHome(t)
	workspace := t.TempDir()
	marker := filepath.Join(t.TempDir(), "settings-validator-ran")
	packDir := t.TempDir()
	writeSettingsValidatorFilePack(t, packDir, marker, false, false, "")
	writeUserConfigJSON(t, home, `{"packs":[{"source":"file://`+packDir+`","name":"settings-pack"}],`+
		`"loopholes":{"settings-mode":{"enabled":true,"settings":{"profile":"current-profile"}}}}`)

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, workspace, "container", &stdout, &stderr, nil)
	// Run and pack/config selection are real. Only the Apple host/runtime boundary is
	// substituted: an in-process keeper plus a harmless container stub, never a VM.
	o.IsMacOS, o.IsLinux = true, false
	o.NeverAttach = true
	o.AcceptConfigChanges = true
	repo, ok := o.RepoRoot()
	if !ok {
		t.Fatal("dispatch fixture has no repository root")
	}
	entrypointPath := filepath.Join(prebuiltBinDir(repo.Root), "yolo-entrypoint")
	o.PathExists = func(path string) bool { return path == entrypointPath }
	o.BuildJailPrefix = func(string) (string, []string) { return "/opt/yolo-jail", nil }
	o.autoLoad = func(image.AutoLoadOptions) image.LoadResult { return image.LoadResult{OK: true, Ref: goldenImageRef} }
	o.CapturesDir = func() string { return t.TempDir() }
	defaultLookPath := o.LookPath
	o.LookPath = func(name string) (string, bool) {
		if name == "container" {
			return "/usr/bin/container", true
		}
		return defaultLookPath(name)
	}
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		if len(argv) >= 2 && argv[1] == "--version" {
			return ExecResult{Ran: true, RC: 0, Stdout: "Apple container CLI version test\n"}
		}
		if len(argv) >= 3 && argv[0] == "container" && argv[1] == "system" && argv[2] == "status" {
			return ExecResult{Ran: true, RC: 0, Stdout: "running\n"}
		}
		return ExecResult{Ran: true, RC: 0}
	}
	rc := Run(*o)
	output := stdout.String() + stderr.String()
	if rc != 1 || !strings.Contains(output, "Configured runtime 'container' not found on PATH") {
		t.Fatalf("Run() = %d, want the stub runtime's expected exec refusal after admission; output:\n%s", rc, output)
	}
	if !strings.Contains(output, "settings-mode is inert on this backend") || !strings.Contains(output, "keeper: started") {
		t.Fatalf("Run did not reach Apple Container's selected-backend launch boundary:\n%s", output)
	}
	if data, err := os.ReadFile(marker); !os.IsNotExist(err) {
		t.Fatalf("actual Run executed the excluded pack validator on Apple Container: marker=%q err=%v\n%s",
			data, err, output)
	}
}

func TestMacosUserRunChecksSelectedSettingsOnlyForFreshLaunch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		dryRun   bool
		admitted bool
	}{
		{name: "fresh selected native launch", admitted: true},
		{name: "dry-run selected native launch", dryRun: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := packHome(t)
			workspace := t.TempDir()
			marker := filepath.Join(t.TempDir(), "settings-validator-ran")
			writeSettingsValidatorModePack(t, home, marker)
			var got macosUserLaunchResult
			if tc.dryRun {
				got = macosUserLaunchWithOptions(t, workspace, nil, func(o *Options) { o.DryRun = true })
			} else {
				got = macosUserLaunch(t, workspace)
			}
			if got.rc != 0 {
				t.Fatalf("Run() = %d\n%s", got.rc, got.out)
			}
			if got.dryRun != tc.dryRun {
				t.Fatalf("backend received dryRun=%v, want %v\n%s", got.dryRun, tc.dryRun, got.out)
			}
			data, err := os.ReadFile(marker)
			if tc.admitted {
				if err != nil || string(data) != "x" {
					t.Fatalf("fresh native launch did not validate current settings exactly once: marker=%q err=%v\n%s", data, err, got.out)
				}
				for _, want := range []string{"settings validator", "settings-mode", "This launch runs pack code on your machine"} {
					if !strings.Contains(got.out, want) {
						t.Errorf("fresh native launch lacks %q:\n%s", want, got.out)
					}
				}
			} else {
				if !os.IsNotExist(err) {
					t.Fatalf("dry run executed selected settings validator: marker=%q err=%v\n%s", data, err, got.out)
				}
				if strings.Contains(got.out, "settings validator") {
					t.Errorf("dry run disclosed validator execution that did not happen:\n%s", got.out)
				}
			}
		})
	}
}

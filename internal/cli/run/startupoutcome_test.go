package run

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestSelectedLoopCollectsActualPerJailReasonBeforeFalseBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	const name = "o1-per-jail-outcome-fixture"
	script := `import json, os, socket, struct
record = {"version":1,"service":os.environ["YOLO_HOST_SERVICE_NAME"],"attempt":os.environ["YOLO_HOST_SERVICE_ATTEMPT"],"class":"dependency","reason":"The selected fixture dependency is absent.","remedy":"Install the fixture dependency and retry."}
data = json.dumps(record, separators=(",", ":")).encode()
s = socket.socket(fileno=3)
s.sendall(struct.pack(">I", len(data)) + data)
`
	manifest := fmt.Sprintf(`{
		"name":%q,"default_enabled":true,"transport":"none",
		"host_daemon":{"cmd":["python3","{loophole_dir}/daemon.py","--socket","{socket}"],
			"publishes":"socket","startup_reason":true}
	}`, name)
	p := writeRealLoopholePack(t, "o1", name, manifest)
	if err := os.WriteFile(filepath.Join(p.Root, "loopholes", name, "daemon.py"), []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	startingLoopholePacks(p)

	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set(name, entry)
	cfg := newConfig()
	cfg.Set("loopholes", loopCfg)
	set := loopholes.NewHostSet(loopCfg)

	var output bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: &output, Stderr: &output, ServiceReadyTimeout: time.Second}
	fillDefaults(o)
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != name {
		t.Fatalf("selected per-jail fixture set = %+v", enabled)
	}
	_, refused := o.startLoopholesDisclosed("o1-per-jail-outcome", "podman", cfg, []*packload.Pack{p}, nil)
	if len(o.startupOutcomes) != 1 {
		t.Fatalf("selected loop collected %d outcomes, want the failed selected start; refusal=%+v startupRefusal=%+v hosts=%v output=%s",
			len(o.startupOutcomes), refused, o.startupRefusal, o.hostServiceNames("podman", cfg), output.String())
	}
	got := o.startupOutcomes[0]
	if got.Owner != hostservice.StartupOwnerLaunch || got.Service != name || got.Attempt == 0 {
		t.Fatalf("per-jail result lacks owner/service/current-attempt attribution: %+v", got)
	}
	if got.Kind != hostservice.StartupKindCooperativeRefusal || got.Readiness != hostservice.StartupReadinessNotReady ||
		got.Process != hostservice.StartupProcessExited || got.ReasonRead.Kind != hostservice.StartupReasonReadRecord {
		t.Fatalf("selected per-jail start did not consume its actual failed readiness record: %+v", got)
	}
	if got.ReasonClass != "dependency" || got.Reason != "The selected fixture dependency is absent." ||
		got.Remedy != "Install the fixture dependency and retry." {
		t.Fatalf("per-jail outcome lost the safe current-attempt reason: %+v", got)
	}
	if strings.Contains(output.String(), "YOLO_HOST_SERVICE_ATTEMPT") || strings.Contains(output.String(), "opaque") {
		t.Fatalf("the private attempt token entered launch output: %s", output.String())
	}
}

func TestSelectedLoopCollectsSingletonPreparationOutcomeBeforeFalseBranch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)
	oldSingletonDir := paths.HostSingletonDir
	singletonDir := fmt.Sprintf("/tmp/o1s-%d", os.Getpid())
	if err := os.MkdirAll(singletonDir, 0o700); err != nil {
		t.Fatal(err)
	}
	paths.HostSingletonDir = singletonDir
	t.Cleanup(func() {
		paths.HostSingletonDir = oldSingletonDir
		_ = os.RemoveAll(singletonDir)
	})

	const name = "o1-singleton-outcome-fixture"
	listener, err := net.Listen("unix", paths.HostSingletonSocket(name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	manifest := fmt.Sprintf(`{
		"name":%q,"default_enabled":true,"transport":"loopback-tls",
		"host_daemon":{"cmd":[%q,"-test.run=^$","{socket}"],
			"scope":"host","publishes":"socket"}
	}`, name, os.Args[0])
	p := writeRealLoopholePack(t, "o1", name, manifest)
	startingLoopholePacks(p)
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set(name, entry)
	cfg := newConfig()
	cfg.Set("loopholes", loopCfg)
	set := loopholes.NewHostSet(loopCfg)

	var output bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: &output, Stderr: &output, ServiceReadyTimeout: time.Second}
	fillDefaults(o)
	o.singletonDepsForStart = func(name string, argv []string) broker.Deps {
		deps := broker.SingletonDeps(name, argv)
		deps.Spawn = func([]string, string) (int, func() bool, error) {
			t.Error("spawn was invoked after preparation failed")
			return 77, func() bool { return false }, nil
		}
		deps.PrepareLocked = func() (func() error, error) {
			return nil, errors.New("injected preparation failure")
		}
		return deps
	}
	cname := "o1-singleton-outcome"
	if enabled := set.Enabled(); len(enabled) != 1 || enabled[0].Name != name {
		t.Fatalf("selected singleton fixture set = %+v", enabled)
	}
	handles, refused := o.startLoopholesDisclosed(cname, "podman", cfg, []*packload.Pack{p}, nil)
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), cname, "podman") })
	if refused != nil {
		t.Fatalf("preparation failure should remain a non-fatal outcome, got refusal: %+v", refused)
	}
	if len(o.startupOutcomes) != 1 {
		t.Fatalf("selected loop collected %d singleton outcomes, want the failed owner result; output=%s", len(o.startupOutcomes), output.String())
	}
	got := o.startupOutcomes[0]
	if got.Owner != hostservice.StartupOwnerSingleton || got.Service != name || got.Attempt == 0 ||
		got.Kind != hostservice.StartupKindPreparationFailed || got.Phase != hostservice.StartupPhasePreparation || got.Spawned {
		t.Fatalf("selected singleton loop did not retain the actual preparation outcome: %+v", got)
	}
	frontPath := filepath.Join(hostServiceSocketsDir(cname, false), name+paths.ServiceEndpointExt)
	if fileExists(frontPath) {
		t.Fatalf("terminal preparation failure was hidden by an accepting old singleton; a new front exists at %s", frontPath)
	}
}

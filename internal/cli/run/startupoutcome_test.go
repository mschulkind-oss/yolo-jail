package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
		"name":%q,"transport":"none",
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
	o := &Options{Workspace: t.TempDir(), Stdout: &output, Stderr: &output, ServiceReadyTimeout: readyFixtureBudget(t)}
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

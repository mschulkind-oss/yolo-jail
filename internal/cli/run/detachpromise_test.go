package run

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// detachpromise_test.go pins that `host_daemon.detach` alone does not make a daemon's endpoint
// variable the assembler's to promise. `detach` says how a jail's end stops the daemon; the
// assembler's optimistic `-e YOLO_SERVICE_<NAME>_ENDPOINT=/run/yolo-services/<name>.endpoint` is
// a promise only a loopback-tls service keeps, since only one gets an endpoint file. A detaching
// daemon on `transport: "none"` binds a plain socket: promised an endpoint, its jail would be
// handed a variable naming a file that never appears (which the reachability witness refuses
// the launch over), and its real `_SOCKET` variable would be blanked from the handle.

// socketDaemonFixture records one pack module declaring a detaching daemon on transport "none"
// that binds a plain AF_UNIX socket.
func socketDaemonFixture(t *testing.T, name string) {
	t.Helper()
	lp := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(lp, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd, err := json.Marshal([]string{os.Args[0], "-front-upstream-child", "line", "{socket}"})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "` + name + `", "description": "fixture", "version": 1,
	  "transport": "` + loopholes.TransportNone + `",
	  "lifecycle": "spawned",
	  "host_daemon": {"cmd": ` + string(cmd) + `, "publishes": "socket", "detach": true}}`
	if err := os.WriteFile(filepath.Join(lp, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.SetPackModuleResolver(nil)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: lp, HostExecApproved: true}})
	t.Cleanup(func() {
		loopholes.ResetPackModules()
		loopholes.SetPackModuleResolver(resolvePackLoopholeModules)
	})
}

func TestADetachingDaemonWithoutLoopbackTLSIsPromisedNoEndpoint(t *testing.T) {
	const name = "yjtest-sockd"
	socketDaemonFixture(t, name)
	cfg := jsonx.NewOrderedMap()
	if got := promisedEndpoints("podman", cfg); slices.Contains(got, name) {
		t.Errorf("promisedEndpoints = %v: a transport-none daemon was promised an endpoint file "+
			"it never publishes, because it declares `detach`", got)
	}
	o := &Options{}
	fillDefaults(o)
	args := strings.Join(o.hostServicesMountArgs("podman", "yolo-sockd-test", cfg), " ")
	if strings.Contains(args, hostServiceEnvVar(name)) {
		t.Errorf("the argv carries %s for a daemon with no endpoint file: %s", hostServiceEnvVar(name), args)
	}
}

// THE SPAWN HALF: the handle keeps its own `_SOCKET` variable, the one the jail reaches the
// daemon by, because the assembler promised it nothing.
func TestADetachingSocketDaemonKeepsItsSocketVariable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host process")
	}
	const name = "yjtest-sockd"
	socketDaemonFixture(t, name)
	var buf strings.Builder
	o := &Options{}
	fillDefaults(o)
	o.Stdout = &buf
	cname := "yolo-sockd-test"
	handles := o.startLoopholes(cname, "podman", jsonx.NewOrderedMap())
	socketsDir := hostServiceSocketsDir(cname, false)
	t.Cleanup(func() {
		o.stopLoopholes(handles, socketsDir, "", "")
		_ = os.RemoveAll(socketsDir)
	})
	for _, h := range handles {
		if h.name == name {
			if h.envVarName != hostServiceSocketEnvVar(name) {
				t.Errorf("the handle's variable = %q, want %s: the jail is left with no way to "+
					"reach the daemon", h.envVarName, hostServiceSocketEnvVar(name))
			}
			return
		}
	}
	t.Fatalf("no handle for the daemon; output:\n%s", buf.String())
}

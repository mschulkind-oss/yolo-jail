package callbroker

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func TestSelectedCLIForwarderReachesAuthenticatedCoreHandlerWithoutWriteAuthority(t *testing.T) {
	h, _, marker, store := testCallHandler(t)
	root := filepath.Dir(marker)
	shortSocketDir, err := os.MkdirTemp(os.TempDir(), "cbf-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shortSocketDir) })
	socket := filepath.Join(shortSocketDir, "upstream.sock")
	if len(socket) > 103 {
		t.Fatalf("fixture Unix socket path is too long (%d bytes): %q", len(socket), socket)
	}
	t.Logf("fixture upstream Unix socket path is %d bytes", len(socket))
	endpointDir := filepath.Join(root, "launch")
	if err := os.Mkdir(endpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(endpointDir, "tasks.endpoint")
	stop := make(chan struct{})
	server := make(chan error, 1)
	front := make(chan error, 1)
	serverDone, frontStarted, frontDone := false, false, false
	go func() { server <- hostservice.ServeFrontedUnix(h.Handle, socket, stop) }()
	t.Cleanup(func() {
		close(stop)
		if !serverDone {
			select {
			case <-server:
			case <-time.After(5 * time.Second):
				t.Error("upstream server did not stop")
			}
		}
		if frontStarted && !frontDone {
			select {
			case <-front:
			case <-time.After(5 * time.Second):
				t.Error("front server did not stop")
			}
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		select {
		case err := <-server:
			serverDone = true
			t.Fatalf("upstream socket %q (%d bytes) failed before readiness: %v", socket, len(socket), err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("upstream socket %q (%d bytes) was not ready before timeout", socket, len(socket))
		}
		time.Sleep(time.Millisecond)
	}
	go func() { front <- svcendpoint.ServeFront(endpoint, "127.0.0.1", socket, stop) }()
	frontStarted = true
	for !svcendpoint.Probe(endpoint) {
		select {
		case err := <-front:
			frontDone = true
			t.Fatalf("front failed before readiness: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("front not ready")
		}
		time.Sleep(time.Millisecond)
	}
	key := paths.ServiceEnvVarPrefix + paths.ServiceEnvSlug("tasks") + paths.ServiceEnvVarSuffix
	// The fixture pack's intercept uses the ordinary entrypoint-generated shell
	// content and the shipped yolo binary, not a private handler test adapter.
	binary := filepath.Join(root, "bin", "yolo")
	goTool, err := callBrokerTestGoTool()
	if err != nil {
		t.Fatalf("locate Go compiler for fixture forwarder: %v", err)
	}
	build := exec.Command(goTool, "build", "-o", binary, "../../cmd/yolo")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture's real forwarder binary: %v %s", err, output)
	}
	shimRoot := filepath.Join(root, "packroot", "test-pack")
	if err := os.MkdirAll(shimRoot, 0700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"test-pack","contributes":[{"kind":"intercept","bin":"tasks","forward":["yolo","internal","call-broker-forward","tasks","--"]}]}`
	if err := os.WriteFile(filepath.Join(shimRoot, "pack.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
	jailHome := filepath.Join(root, "jail-home")
	if err := os.Mkdir(jailHome, 0700); err != nil {
		t.Fatal(err)
	}
	boot := entrypoint.NewEnv(map[string]string{"JAIL_HOME": jailHome, "YOLO_PACK_ROOT": filepath.Dir(shimRoot)})
	if err := entrypoint.GenerateShims(boot); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(boot.BlockDir(), "tasks")
	if _, err := os.Stat(shim); err != nil {
		t.Fatalf("selected pack intercept missing: %v", err)
	}
	invoke := func(argv ...string) (int, string) {
		t.Helper()
		cmd := exec.Command(shim, argv...)
		cmd.Env = append(os.Environ(), key+"="+endpoint, "PATH="+filepath.Dir(binary)+":"+os.Getenv("PATH"))
		output, err := cmd.CombinedOutput()
		if err == nil {
			return 0, string(output)
		}
		if exit, ok := err.(*exec.ExitError); ok {
			return exit.ExitCode(), string(output)
		}
		t.Fatalf("fixture intercept failed to launch: %v", err)
		return -1, ""
	}
	if rc, output := invoke("read"); rc != 0 || !strings.Contains(output, `"ok":true`) {
		t.Fatalf("intercept read %d %q", rc, output)
	}
	if rc, _ := invoke("update", "a1"); rc != 75 {
		t.Fatalf("intercept write without live authority: %d", rc)
	}
	forward := func(op string, argv ...string) (int, string) {
		t.Helper()
		var out, errors bytes.Buffer
		rc := ForwardCLI("tasks", op, argv, nil, &out, &errors, func(k string) string {
			if k == key {
				return endpoint
			}
			return ""
		})
		return rc, out.String() + errors.String()
	}
	if rc, output := forward("act", "read"); rc != 0 || !strings.Contains(output, `"ok":true`) {
		t.Fatalf("selected read %d %q", rc, output)
	}
	if rc, output := forward("evaluate", "update", "a1"); rc != 0 || !strings.Contains(output, `"verdict":"asks"`) {
		t.Fatalf("prediction %d %q", rc, output)
	}
	if rc, _ := forward("ask", "update", "a1"); rc != 0 {
		t.Fatalf("ask: %d", rc)
	}
	if rc, _ := forward("act", "update", "a1"); rc != 75 {
		t.Fatalf("write without live authority: %d", rc)
	}
	if rc, _ := forward("act", "update", "a1", "b1"); rc != 64 {
		t.Fatalf("whole refused multiwrite: %d", rc)
	}
	if rc := ForwardCLI("tasks", "act", []string{"read"}, nil, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" }); rc != 69 {
		t.Fatalf("unselected forward: %d", rc)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("forwarder ran host write: %v", err)
	}
	requests, grants, err := store.List()
	if err != nil || len(requests) != 0 || len(grants) != 0 {
		t.Fatalf("inert pack mutated store: %v %v %v", requests, grants, err)
	}
}

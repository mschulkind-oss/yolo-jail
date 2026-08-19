package hostprocesses

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// Main's own suite. The black-box suite drives BuildHandler + hostservice
// directly and never touches Main, so until this file the flag layer — the half
// that decides WHICH TRANSPORT the manifest asked for — had no coverage at all.
//
// That layer is where the expensive mistake lives. `--socket` used to be an alias
// for `--endpoint`, so a manifest spawning `--socket {socket}` produced a
// token-bearing regular FILE at the path yolo's readiness probe dials as a
// socket: the probe fails, waitServiceReady SIGKILLs the process group, and the
// user gets one yellow warning line with nothing in it about transports. The
// assertions below are on the artifact's TYPE for exactly that reason — an
// assertion that "something appeared at the path" would have passed throughout.

// serveInBackground starts Main and returns a channel that carries its exit code
// if it gives up. The daemon is deliberately LEAKED: Main owns its own stop
// channel and installs process-wide SIGTERM/SIGINT handlers, so there is no way
// to shut it down from here that would not also kill the test binary. It holds a
// socket or an endpoint file in a temp dir and nothing else.
func serveInBackground(t *testing.T, argv ...string) <-chan int {
	t.Helper()
	rc := make(chan int, 1)
	go func() { rc <- Main(argv) }()
	return rc
}

// privateDir is 0700, which svcendpoint requires of a directory it publishes a
// credential into. t.TempDir() is 0755 and is correctly REFUSED.
func privateDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "yj-hp-main-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// waitArtifact waits for path to exist, failing fast (with Main's exit code) if
// the daemon gave up instead of serving.
func waitArtifact(t *testing.T, path string, rc <-chan int) os.FileMode {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case code := <-rc:
			t.Fatalf("Main exited rc=%d instead of serving at %s", code, path)
		default:
		}
		if fi, err := os.Lstat(path); err == nil {
			return fi.Mode()
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("nothing appeared at %s within 5s", path)
	return 0
}

// TestMainRequiresExactlyOneTransport pins the flag contract that replaced the
// alias: neither flag is an error, and BOTH is an error. The second half is the
// one worth a test — while --socket folded into --endpoint, passing both was a
// silent win for whichever the code happened to prefer.
func TestMainRequiresExactlyOneTransport(t *testing.T) {
	dir := privateDir(t)
	cases := []struct {
		name string
		argv []string
	}{
		{"neither", []string{}},
		{"both", []string{"--socket", filepath.Join(dir, "a.sock"), "--endpoint", filepath.Join(dir, "b.endpoint")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if rc := Main(tc.argv); rc != 2 {
				t.Errorf("Main(%v) = %d, want 2", tc.argv, rc)
			}
			// And it refused before binding anything.
			for _, name := range []string{"a.sock", "b.endpoint"} {
				if _, err := os.Lstat(filepath.Join(dir, name)); err == nil {
					t.Errorf("a refused invocation still created %s", name)
				}
			}
		})
	}
}

// TestMainRefusesTheRetiredConfigFlag pins the deletion-shaped half of the settings
// move: --config named a yolo-jail.jsonc this daemon parsed itself, and it is gone.
//
// A REFUSAL rather than an alias, and the difference is measurable rather than
// stylistic. The two flags name files with different SHAPES — a settings file says
// {"visible": …}, a config file says {"host_processes": {"visible": …}} — so quietly
// treating one as the other reads the outer object, finds no `visible`, and starts a
// daemon with an EMPTY allowlist that looks entirely healthy. Refusing at flag-parse
// time is the only outcome that says what happened.
//
// It also refuses BEFORE binding anything, so a mis-spawned daemon leaves no artifact
// for the run pipeline's readiness probe to succeed against.
func TestMainRefusesTheRetiredConfigFlag(t *testing.T) {
	dir := privateDir(t)
	sock := filepath.Join(dir, "hp.sock")
	if rc := Main([]string{"--socket", sock, "--config", filepath.Join(dir, "yolo-jail.jsonc")}); rc != 2 {
		t.Errorf("Main with --config = %d, want 2 — a retired flag must refuse and name "+
			"its replacement, never fall back", rc)
	}
	if _, err := os.Lstat(sock); err == nil {
		t.Error("a refused invocation still bound the socket")
	}
}

// TestMainSocketBindsASocket is the anti-alias assertion. `--socket` must produce
// a SOCKET — the artifact yolo's front dials and its readiness probe connects to
// — and must not produce the endpoint file the alias produced.
func TestMainSocketBindsASocket(t *testing.T) {
	dir := privateDir(t)
	sock := filepath.Join(dir, "hp.sock")
	rc := serveInBackground(t, "--socket", sock, "--settings", filepath.Join(dir, "settings.json"))

	if mode := waitArtifact(t, sock, rc); mode&os.ModeSocket == 0 {
		t.Fatalf("--socket produced mode %v, want a socket; an alias for --endpoint "+
			"publishes a regular file here and the run pipeline's connect probe then "+
			"SIGKILLs the daemon with no diagnosis", mode)
	}
	if svcendpoint.Probe(sock) {
		t.Error("--socket published a usable ENDPOINT (a bearer-token file) at the socket path")
	}
}

// TestMainEndpointPublishesAnEndpointFile is the other half: --endpoint keeps its
// meaning exactly, so the flag split cannot be half-applied without one of these
// two tests going red.
func TestMainEndpointPublishesAnEndpointFile(t *testing.T) {
	t.Setenv(svcendpoint.AdvertiseHostEnv, "127.0.0.1")
	dir := privateDir(t)
	ep := filepath.Join(dir, "hp.endpoint")
	rc := serveInBackground(t, "--endpoint", ep, "--settings", filepath.Join(dir, "settings.json"))

	if mode := waitArtifact(t, ep, rc); mode&os.ModeSocket != 0 {
		t.Fatalf("--endpoint bound a socket (mode %v); it must publish an endpoint file", mode)
	}
	// Probe, not existence: a truncated file exists but names nothing dialable.
	deadline := time.Now().Add(5 * time.Second)
	for !svcendpoint.Probe(ep) {
		if time.Now().After(deadline) {
			t.Fatal("--endpoint never published a USABLE endpoint")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

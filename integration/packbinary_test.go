package integration

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAJailRunsAPackBinaryItDownloaded is a loophole's DOWNLOADED BINARY end to end in a real
// container (docs/design/broker-as-a-pack.md BP-D1 to BP-D6): the one thing the unit tests
// cannot show is that the cached build, mounted as a single read-only file bind, is executable
// in the jail and run by the supervisor.
//
// Three steps, each a surface the design names:
//
//  1. A launch BEFORE `yolo pack install` runs nothing and says so, naming the command: a launch
//     never downloads (BP-D5).
//  2. `yolo pack install` fetches the build over https from a local server, verifies its sha256
//     and caches it (the server's certificate reaches the child through SSL_CERT_FILE, which is
//     why this runs on Linux only: darwin's root pool ignores it).
//  3. The next launch mounts it at /etc/yolo-jail/loophole-binaries/<loophole>/<name> and the
//     jail's supervisor runs it, which is the line in its log.
//
// The pack is configured by path; its own tree carries no executable at all, so the program
// that runs can only have come from the download.
func TestAJailRunsAPackBinaryItDownloaded(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" {
		t.Skip("the download's TLS server is trusted through SSL_CERT_FILE, which darwin's root pool ignores")
	}

	script := []byte("#!/bin/sh\necho \"hello from $0\"\n")
	sum := sha256.Sum256(script)
	digest := hex.EncodeToString(sum[:])
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(script)
	}))
	t.Cleanup(srv.Close)
	certFile := filepath.Join(t.TempDir(), "server.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE",
		Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}

	pack := t.TempDir()
	mod := filepath.Join(pack, "loopholes", "hello-bin")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name": "hello-bin", "transport": "none", "default_enabled": true,
	  "binaries": {"hello": {"linux/` + runtime.GOARCH + `": {"url": "` + srv.URL + `/hello",
	    "sha256": "` + digest + `"}}},
	  "jail_daemon": {"cmd": ["{jail_binary:hello}"], "restart": "no"}}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(
		`{"name": "hello-bin", "contributes": [{"kind": "loophole", "from": "loopholes/hello-bin"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"name": "hello-bin", "source": "file://`+pack+`"}]}`)
	dir := writeProject(t, `{}`)

	const logPath = "$HOME/.local/state/yolo-jail-daemons/hello-bin.log"
	readLog := `for i in $(seq 1 100); do [ -s ` + logPath + ` ] && break; sleep 0.1; done; ` +
		`cat ` + logPath + ` 2>/dev/null; true`

	before := runYolo(t, dir, readLog)
	if before.rc != 0 {
		t.Fatalf("the launch before install failed: rc %d\n%s", before.rc, before.combined())
	}
	if !strings.Contains(before.combined(), "loophole hello-bin is waiting for its binary hello") ||
		!strings.Contains(before.combined(), "yolo pack install") {
		t.Errorf("the launch before install did not say the loophole waits for `yolo pack install`:\n%s",
			before.combined())
	}
	if strings.Contains(before.stdout, "hello from") {
		t.Errorf("a program ran before it was downloaded:\n%s", before.combined())
	}

	install := runCommand(t, dir, []string{"pack", "install"}, withEnv("SSL_CERT_FILE="+certFile))
	if install.rc != 0 || !strings.Contains(install.stdout, "binary hello for linux/"+runtime.GOARCH+
		": fetched and verified") {
		t.Fatalf("yolo pack install did not fetch the build: rc %d\n%s", install.rc, install.combined())
	}

	after := runYolo(t, dir, readLog+`; ls -l /etc/yolo-jail/loophole-binaries/hello-bin/hello`)
	if after.rc != 0 {
		t.Fatalf("the launch after install failed: rc %d\n%s", after.rc, after.combined())
	}
	if !strings.Contains(after.stdout, "hello from /etc/yolo-jail/loophole-binaries/hello-bin/hello") {
		t.Errorf("the jail's supervisor did not run the downloaded program:\n%s", after.combined())
	}
}

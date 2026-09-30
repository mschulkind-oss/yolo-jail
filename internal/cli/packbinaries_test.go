package cli

// `yolo pack install`'s download step (packbinaries.go; docs/design/broker-as-a-pack.md BP-D1).
// The builds are served by a local TLS server, and the pack is a local one under a temp HOME, so
// nothing here touches the network or the real home.
//
// Mutation check: delete the installPackBinaries call in packInstall and
// TestPackInstallFetchesADeclaredBinary fails — nothing else fetches.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packbin"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// binaryPackHome writes a temp HOME whose user config selects one local pack; its loophole's
// host daemon runs toold, declared for this machine at url and pinned by sum. It returns the
// pack-binary cache under that HOME.
func binaryPackHome(t *testing.T, url, sum string) string {
	t.Helper()
	return binaryPackHomeWith(t, url, sum, "")
}

// binaryPackHomeWith is binaryPackHome with extra manifest keys, written as `"key": value, `.
func binaryPackHomeWith(t *testing.T, url, sum, extra string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	pack := filepath.Join(home, "packs", "toolpack")
	mod := filepath.Join(pack, "loopholes", "tool")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	manifest := `{"name": "tool", "transport": "none", ` + extra + `
	  "binaries": {"toold": {"` + platform + `": {"url": "` + url + `", "sha256": "` + sum + `"}}},
	  "doctor_cmd": ["{binary:toold}", "--self-check"]}`
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pj := `{"name": "toolpack", "contributes": [{"kind": "loophole", "from": "loopholes/tool"}]}`
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(pj), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := `{"packs": [{"source": "file://` + pack + `", "name": "toolpack"}]}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	return paths.PackBinariesDir()
}

// serveBinary serves body at /toold over TLS, and points `yolo pack install`'s fetcher at it.
func serveBinary(t *testing.T, body []byte) string {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	prev := packBinaryFetcher
	packBinaryFetcher = func() packbin.Fetcher {
		return packbin.Fetcher{Dir: paths.PackBinariesDir(), Client: srv.Client()}
	}
	t.Cleanup(func() { packBinaryFetcher = prev })
	return srv.URL + "/toold"
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestPackInstallFetchesADeclaredBinary(t *testing.T) {
	body := []byte("#!/bin/sh\necho toold\n")
	url := serveBinary(t, body)
	sum := sha256Hex(body)
	cache := binaryPackHome(t, url, sum)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "binary toold for "+runtime.GOOS+"/"+runtime.GOARCH+": fetched and verified") {
		t.Errorf("install did not report the fetch:\n%s", out.String())
	}
	path := packbin.Path(cache, sum, "toold")
	if !packbin.Present(path) {
		t.Fatalf("no executable build at %s after install", path)
	}
	if got, _ := os.ReadFile(path); string(got) != string(body) {
		t.Errorf("cached bytes = %q", got)
	}

	// A second install of the same pin is answered by the cache and says so.
	out.Reset()
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("second install rc = %d: %s", rc, errw.String())
	}
	if !strings.Contains(out.String(), "already fetched") {
		t.Errorf("second install did not report the cached build:\n%s", out.String())
	}
}

// A mismatch fails the install, names both digests, and caches nothing (§9: an integrity
// failure, not a fetch failure).
func TestPackInstallRefusesABinaryWhoseDigestDoesNotMatch(t *testing.T) {
	body := []byte("not what was pinned")
	url := serveBinary(t, body)
	pinned := sha256Hex([]byte("what was pinned"))
	cache := binaryPackHome(t, url, pinned)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc == 0 {
		t.Fatalf("install succeeded over a digest mismatch:\n%s", out.String())
	}
	for _, want := range []string{pinned, sha256Hex(body), "refused"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the refusal lacks %q:\n%s", want, errw.String())
		}
	}
	if packbin.Present(packbin.Path(cache, pinned, "toold")) {
		t.Error("a mismatched download was cached")
	}
}

// A loophole whose `platforms` leaves this machine out runs nothing here, so install fetches none
// of its builds, even one declared for this machine: BP-D5 fetches what this machine NEEDS, and a
// loophole it cannot run needs nothing (the launch reports it on the platform axis). Deleting the
// SupportsPlatform skip in fetchPackBinaries fails this.
func TestPackInstallFetchesNothingForALoopholeThisMachineCannotRun(t *testing.T) {
	body := []byte("#!/bin/sh\necho toold\n")
	var hits int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	prev := packBinaryFetcher
	packBinaryFetcher = func() packbin.Fetcher {
		return packbin.Fetcher{Dir: paths.PackBinariesDir(), Client: srv.Client()}
	}
	t.Cleanup(func() { packBinaryFetcher = prev })
	other := "plan9"
	if runtime.GOOS == other {
		other = "linux"
	}
	sum := sha256Hex(body)
	cache := binaryPackHomeWith(t, srv.URL+"/toold", sum, `"platforms": ["`+other+`"], `)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"install"}, &out, &errw, false); rc != 0 {
		t.Fatalf("install rc = %d\n%s%s", rc, out.String(), errw.String())
	}
	if hits != 0 || packbin.Present(packbin.Path(cache, sum, "toold")) {
		t.Errorf("install fetched a build for a loophole this machine cannot run (%d requests):\n%s",
			hits, out.String())
	}
	if !strings.Contains(out.String(), "does not run on "+runtime.GOOS+"/"+runtime.GOARCH) {
		t.Errorf("install did not say why it fetched nothing:\n%s", out.String())
	}
}

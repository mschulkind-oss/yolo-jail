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
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
)

// packtreeprogram_test.go is the container-level cell for a PACK-TREE PROGRAM
// (docs/design/pack-buildable-programs.md, a `program` delivered `via: "pack"`): a launch builds
// the program from its pack's own tree in a sealed build jail of its own, delivers it into the jail,
// rebuilds it once when the tree changes, and builds nothing when it has not — the design's done
// steps 1 to 3.
//
// HERMETIC: the pack is a `file://` directory whose tools/x holds a VERSION file, and its build line
// writes a shell script that prints that version — a real build step with no network. The capture
// store is a private fixture one (withPrivateFixtureYoloStore), so nothing here touches the machine's.

const (
	packTreeFixturePack = "packtreefx"
	packTreeFixtureBin  = "packtreefx-x"
	packTreeBuiltLine   = "built program " + packTreeFixturePack + "/" + packTreeFixtureBin
)

func TestPackTreeProgramBuildsOnceAndRebuildsOnChange(t *testing.T) {
	requireJail(t)
	pack := t.TempDir()
	src := filepath.Join(pack, "tools", "x")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	setVersion := func(v string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(src, "VERSION"), []byte(v+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	setVersion("1")
	// The build: a script whose output names the VERSION it was built from, at `run` ("x").
	build := `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"`+packTreeFixturePack+
		`","contributes":[{"kind":"program","bin":"`+packTreeFixtureBin+`","via":"pack","from":"tools/x",`+
		`"build":"`+buildJSON+`","run":"x"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+packTreeFixturePack+`"}]}`)
	withPrivateFixtureYoloStore(t)
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, packTreeFixtureBin) })

	launch := func(what string) string {
		t.Helper()
		r := runCommand(t, t.TempDir(), append(jailRunArgs(), "--", packTreeFixtureBin, "--version"), withHostSemantics())
		out := r.combined()
		if r.rc != 0 {
			t.Fatalf("%s: rc %d\n%s", what, r.rc, out)
		}
		t.Logf("%s:\n%s\nstore entries of %s since the test began: %v", what, out, packTreeFixtureBin,
			newCaptureEntries(t, store, before, packTreeFixtureBin))
		return out
	}

	// 1. A LAUNCH INSTALLS IT: one build, and `<bin> --version` reports the built tree.
	out := launch("the first launch")
	if !strings.Contains(out, "x-1") || strings.Count(out, packTreeBuiltLine) != 1 {
		t.Fatalf("the first launch did not build once and run the built tree (want x-1 and one %q):\n%s",
			packTreeBuiltLine, out)
	}
	if added := newCaptureEntries(t, store, before, packTreeFixtureBin); len(added) != 1 {
		t.Fatalf("the first launch added %d entries, want 1: %v", len(added), added)
	}

	// 2. EDIT THE SOURCE, RELAUNCH: it rebuilds once and serves the new bytes.
	setVersion("2")
	out = launch("the launch after the edit")
	if !strings.Contains(out, "x-2") || strings.Count(out, packTreeBuiltLine) != 1 {
		t.Fatalf("the launch after the edit did not rebuild once and serve x-2:\n%s", out)
	}
	if added := newCaptureEntries(t, store, before, packTreeFixtureBin); len(added) != 2 {
		t.Errorf("after the edit there are %d new entries, want 2: %v", len(added), added)
	}

	// 3. RELAUNCH WITH NO CHANGE: no rebuild, and the hit says nothing.
	out = launch("the launch with no change")
	if !strings.Contains(out, "x-2") || strings.Contains(out, packTreeBuiltLine) || strings.Contains(out, "program builds") {
		t.Errorf("the launch with no change rebuilt, or did not serve x-2:\n%s", out)
	}
	if added := newCaptureEntries(t, store, before, packTreeFixtureBin); len(added) != 2 {
		t.Errorf("the launch with no change added an entry: %v", added)
	}
}

// TestPackTreeProgramBuildFailureRefusesOnlyIt is the design's done step 6 ("Break the build: only
// that program is refused, and the log path is named"), under OQ-BG1's leaning: a pack-tree program
// whose build fails stops the launch by the readiness act's rule (OQ-JR1), naming that program
// alone, its build log, the retry command and the hatch; with YOLO_ALLOW_MISSING_PROGRAMS=1 the
// jail starts and the pack's other program works. The readiness act is ON for both launches
// (withReadiness): without it nothing is refused, and the test would pass vacuously.
func TestPackTreeProgramBuildFailureRefusesOnlyIt(t *testing.T) {
	requireJail(t)
	const (
		failPack  = "packtreefail"
		brokenBin = "packtreefail-broken"
		otherBin  = "packtreefail-other"
	)
	pack := t.TempDir()
	src := filepath.Join(pack, "tools", "broken")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "main.sh"), []byte("echo never built\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// The pack's other program: an installer in the pack itself, so nothing reaches the network.
	installer := "#!/bin/bash\nset -euo pipefail\nmkdir -p \"$HOME/.local/bin\"\n" +
		"printf '#!/bin/sh\\necho OTHER-RAN\\n' > \"$HOME/.local/bin/" + otherBin + "\"\n" +
		"chmod +x \"$HOME/.local/bin/" + otherBin + "\"\n"
	if err := os.WriteFile(filepath.Join(pack, "install.sh"), []byte(installer), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"`+failPack+`","contributes":[`+
		`{"kind":"program","bin":"`+brokenBin+`","via":"pack","from":"tools/broken","build":"echo building; exit 3","run":"x"},`+
		`{"kind":"program","bin":"`+otherBin+`","via":"installer","url":"file:///ctx/packs/`+failPack+`/install.sh"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+failPack+`"}]}`)
	withPrivateFixtureYoloStore(t)
	t.Setenv(paths.AllowMissingProgramsEnv, "")
	dir := t.TempDir()

	// 1. THE LAUNCH STOPS, naming only the broken program, its log, the retry and the hatch.
	r := runCommand(t, dir, append(jailRunArgs(), "--", otherBin), withHostSemantics(), withReadiness())
	all := r.combined()
	t.Logf("the launch with the broken build:\n%s", all)
	if r.rc != provision.StageRefusedStatus {
		t.Fatalf("rc = %d, want the readiness refusal's %d", r.rc, provision.StageRefusedStatus)
	}
	if strings.Contains(all, "OTHER-RAN") {
		t.Errorf("the command ran after the refusal")
	}
	for what, want := range map[string]string{
		"the program":   "program " + brokenBin + " (pack " + failPack + ")",
		"the build log": filepath.Join(".yolo", "logs", "build-"),
		"the retry":     "yolo pack build " + failPack,
		"the hatch":     paths.AllowMissingProgramsEnv + "=1",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("the refusal does not name %s (%q)", what, want)
		}
	}
	if strings.Contains(all, "program "+otherBin+" (pack "+failPack+"): its install exited") {
		t.Errorf("the refusal names the pack's other program too, whose install worked")
	}
	if strings.Contains(all, "fork "+failPack) || strings.Contains(all, "this launch continues without") {
		t.Errorf("the refusal speaks of a fork, or says the launch continues, for a pack-tree program")
	}

	// 2. THE HATCH STARTS IT, and the pack's other program works.
	r = runCommand(t, dir, append(jailRunArgs(), "--", otherBin), withHostSemantics(), withReadiness(),
		withEnv(paths.AllowMissingProgramsEnv+"=1"))
	all = r.combined()
	t.Logf("the launch with the hatch:\n%s", all)
	if r.rc != 0 || !strings.Contains(all, "OTHER-RAN") {
		t.Fatalf("rc = %d: %s must start the jail and run %s", r.rc, paths.AllowMissingProgramsEnv, otherBin)
	}
	if !strings.Contains(all, "program "+brokenBin+" (pack "+failPack+")") {
		t.Errorf("the hatch's launch does not list the program it starts without")
	}
}

// TestPackTreeProgramUsesThePrebuiltWithoutSources is the design's done step 5 ("Remove the pack's
// sources: it uses the declared fallback artifact and reports the checksum it verified"): a pack
// whose manifest names tools/x but ships no such directory, with a `prebuilt` for this jail's
// platform served over TLS by a server inside this test (trusted through SSL_CERT_FILE, so no
// network and Linux only). The launch fetches it, verifies it, delivers it, and prints one line
// with the first 12 hex digits of its sha256; the next launch is a store hit that sends no request
// and still says the line. The readiness act is on, so an undelivered program would refuse.
func TestPackTreeProgramUsesThePrebuiltWithoutSources(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" {
		t.Skip("the artifact's TLS server is trusted through SSL_CERT_FILE, which darwin's root pool ignores")
	}
	const (
		pbPack = "packtreepb"
		pbBin  = "packtreepb-x"
	)
	script := []byte("#!/bin/sh\necho prebuilt-ran\n")
	sum := sha256.Sum256(script)
	digest := hex.EncodeToString(sum[:])
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(script)
	}))
	t.Cleanup(srv.Close)
	certFile := filepath.Join(t.TempDir(), "server.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE",
		Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/" + pbBin

	pack := t.TempDir() // no tools/x: the pack ships no source for the program
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"`+pbPack+`","contributes":[`+
		`{"kind":"program","bin":"`+pbBin+`","via":"pack","from":"tools/x","build":"make","run":"x",`+
		`"prebuilt":{"linux/`+runtime.GOARCH+`":{"url":"`+url+`","sha256":"`+digest+`"}}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+pbPack+`"}]}`)
	withPrivateFixtureYoloStore(t)
	t.Setenv(paths.AllowMissingProgramsEnv, "")
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, pbBin) })
	dir := t.TempDir()
	line := pbBin + ": prebuilt artifact from pack " + pbPack + " (no tools/x in the pack) — sha256 " + digest[:12]

	launch := func(what string) string {
		t.Helper()
		r := runCommand(t, dir, append(jailRunArgs(), "--", pbBin), withHostSemantics(), withReadiness(),
			withEnv("SSL_CERT_FILE="+certFile))
		out := r.combined()
		t.Logf("%s:\n%s", what, out)
		if r.rc != 0 {
			t.Fatalf("%s: rc %d", what, r.rc)
		}
		return out
	}

	out := launch("the first launch")
	if !strings.Contains(out, "prebuilt-ran") {
		t.Errorf("the jail did not run the prebuilt artifact")
	}
	if strings.Count(out, line) != 1 || !strings.Contains(out, "verified, "+url) {
		t.Errorf("the launch did not say once that it used the prebuilt and the sha256 it verified (%q)", line)
	}
	if strings.Contains(out, "program builds") || strings.Contains(out, "built program") {
		t.Errorf("the launch announced a build for a program it fetched")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("%d requests for the artifact on the first launch, want 1", n)
	}
	if added := newCaptureEntries(t, store, before, pbBin); len(added) != 1 {
		t.Errorf("the first launch added %d store entries, want 1: %v", len(added), added)
	}

	out = launch("the second launch")
	if !strings.Contains(out, "prebuilt-ran") || strings.Count(out, line) != 1 {
		t.Errorf("the second launch did not run the prebuilt and say its line")
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("%d requests after the second launch, want 1: a stored artifact is a hit", n)
	}
}

// TestPackTreeProgramUsesThePrebuiltOverItsSourceOnlyWithTheOptIn is OQ-BG2's ruling end to end: a
// pack that ships its source (whose build line fails, so any build would refuse the launch) and a
// `prebuilt` for this jail's platform. With the user's opt-in, `programs.overrides.<bin>:
// {"prebuilt": true}` (BG-D18), the launch fetches and runs the verified prebuilt and its line names
// the setting; no build runs.
func TestPackTreeProgramUsesThePrebuiltOverItsSourceOnlyWithTheOptIn(t *testing.T) {
	requireJail(t)
	if runtime.GOOS != "linux" {
		t.Skip("the artifact's TLS server is trusted through SSL_CERT_FILE, which darwin's root pool ignores")
	}
	const (
		pbPack = "packtreeoptin"
		pbBin  = "packtreeoptin-x"
	)
	script := []byte("#!/bin/sh\necho opted-in-prebuilt-ran\n")
	sum := sha256.Sum256(script)
	digest := hex.EncodeToString(sum[:])
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write(script)
	}))
	t.Cleanup(srv.Close)
	certFile := filepath.Join(t.TempDir(), "server.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE",
		Bytes: srv.Certificate().Raw}), 0o644); err != nil {
		t.Fatal(err)
	}
	url := srv.URL + "/" + pbBin

	pack := t.TempDir()
	if err := os.MkdirAll(filepath.Join(pack, "tools", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "tools", "x", "VERSION"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"`+pbPack+`","contributes":[`+
		`{"kind":"program","bin":"`+pbBin+`","via":"pack","from":"tools/x","build":"exit 7","run":"x",`+
		`"prebuilt":{"linux/`+runtime.GOARCH+`":{"url":"`+url+`","sha256":"`+digest+`"}}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	packHome(t, `{"packs": [{"source": "file://`+pack+`", "name": "`+pbPack+`"}], `+
		`"programs": {"overrides": {"`+pbBin+`": {"prebuilt": true}}}}`)
	withPrivateFixtureYoloStore(t)
	t.Setenv(paths.AllowMissingProgramsEnv, "")
	store := filepath.Join(os.Getenv("HOME"), ".local", "share", "yolo-jail", "captures")
	before := captureEntryNames(t, store)
	t.Cleanup(func() { removeNewCaptureEntries(t, store, before, pbBin) })

	r := runCommand(t, t.TempDir(), append(jailRunArgs(), "--", pbBin), withHostSemantics(), withReadiness(),
		withEnv("SSL_CERT_FILE="+certFile))
	out := r.combined()
	t.Logf("the launch:\n%s", out)
	if r.rc != 0 || !strings.Contains(out, "opted-in-prebuilt-ran") {
		t.Fatalf("rc %d: the opted-in launch did not run the prebuilt", r.rc)
	}
	line := pbBin + ": prebuilt artifact from pack " + pbPack + " (programs.overrides." + pbBin +
		".prebuilt is set) — sha256 " + digest[:12]
	if strings.Count(out, line) != 1 {
		t.Errorf("the launch did not say once that the opt-in chose the prebuilt (%q)", line)
	}
	if n := hits.Load(); n != 1 {
		t.Errorf("%d requests for the artifact, want 1", n)
	}
}

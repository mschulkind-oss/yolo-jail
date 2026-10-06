package integration

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capture"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE MAC'S HOST FLOOR, from an installer's capture (docs/design/host-tool-provisioning.md HP-D2):
// on a Mac the floor's copy of an installer agent is the macos-user capture act's entry — the
// vendor installer run once as the sandbox account under the capture's Seatbelt profile, in a
// throwaway home — materialized into the floor's own prefix. The recording half of that act was
// measured on hardware on 2026-09-11 (macosuser/capture.go); the floor reading its entry, relocating
// it out of the neutral staging home, and `yolo host` running it, had no run at all.
//
// A FIXTURE INSTALLER, never a vendor's: a file:// script under /private/tmp, where the sandbox
// account can read it, that lays the shape claude's installer leaves — the program in a versions
// directory and ~/.local/bin/<bin> an absolute link to it — so the relocation has a link to rewrite.
// No agent starts; the program it installs prints its argv.
//
// ⚠ Mac-only, behind requireMacosUser: the sandbox account and passwordless sudo. A nested jail, and
// every Linux runner, cannot reach it.
func TestMacosUserHostFloorMaterializesAFixtureInstallerCapture(t *testing.T) {
	requireMacosUser(t)
	const bin = "yolohostprobe"
	root, err := os.MkdirTemp("/private/tmp", "yolo-hostcapture-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(root, "install.sh")
	body := "#!/bin/sh\nset -eu\n" +
		`mkdir -p "$HOME/.local/share/` + bin + `/v1" "$HOME/.local/bin"` + "\n" +
		`printf '#!/bin/sh\necho ` + bin + ` 1.0 "$@"\n' > "$HOME/.local/share/` + bin + `/v1/` + bin + `"` + "\n" +
		`chmod 755 "$HOME/.local/share/` + bin + `/v1/` + bin + `"` + "\n" +
		`ln -sf "$HOME/.local/share/` + bin + `/v1/` + bin + `" "$HOME/.local/bin/` + bin + `"` + "\n"
	if err := os.WriteFile(script, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := filepath.Join(root, "pack")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(`{"name":"hostcapturepack","contributes":[`+
		`{"kind":"program","bin":"`+bin+`","via":"installer","url":"file://`+script+`"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// `host_management: own`: an unset key is `none` since the `assert` retirement (OQ-CO14), and
	// `yolo host apply --assert` refuses under it before the floor stage this test reads.
	packHome(t, `{"packs":[{"source":"file://`+pack+`","name":"hostcapturepack"}],"host_management":"own"}`)
	dir := t.TempDir()

	a := runCommand(t, dir, []string{"host", "apply", "--assert"}, withEnv("YOLO_VERSION="))
	if a.rc != 0 {
		t.Fatalf("yolo host apply --assert: rc %d\nstdout:\n%s\nstderr:\n%s", a.rc, a.stdout, a.stderr)
	}
	if want := "materialized " + bin + " from capture"; !strings.Contains(a.stdout+a.stderr, want) {
		t.Errorf("the apply does not say it materialized the capture (%q):\nstdout:\n%s\nstderr:\n%s", want,
			a.stdout, a.stderr)
	}

	r := runCommand(t, dir, []string{"host", "--", bin, "--version"}, withEnv("YOLO_VERSION="))
	if r.rc != 0 || !strings.Contains(r.stdout, bin+" 1.0 --version") {
		t.Fatalf("yolo host -- %s --version: rc %d\nstdout:\n%s\nstderr:\n%s", bin, r.rc, r.stdout, r.stderr)
	}
	if !strings.Contains(r.stderr, "yolo's floor copy") {
		t.Errorf("the launch did not run the floor's copy:\n%s", r.stderr)
	}

	home := os.Getenv("HOME")
	floor := paths.HostFloorDirUnder(home)
	raw, err := os.ReadFile(filepath.Join(floor, "records", bin+".json"))
	if err != nil {
		t.Fatalf("the floor kept no record of %s: %v", bin, err)
	}
	var rec struct {
		Capture, Entry string
	}
	if err := json.Unmarshal(raw, &rec); err != nil || rec.Capture == "" {
		t.Fatalf("the floor's record %s names no capture (%v)", raw, err)
	}
	entry := filepath.Join(paths.CapturesDirUnder(home), "entries", rec.Capture)
	m, err := capture.ReadManifest(entry)
	if err != nil {
		t.Fatal(err)
	}
	if want := "darwin/" + runtime.GOARCH; m.Platform != want || !m.Relocatable {
		t.Errorf("the capture is %s relocatable=%v, want a relocatable %s capture", m.Platform, m.Relocatable, want)
	}
	receipts, err := entrypoint.ReadCaptureReceipts(capture.ReceiptsPath(entry))
	if err != nil || len(receipts) == 0 || receipts[0].Platform != "darwin/"+runtime.GOARCH {
		t.Errorf("the capture's record receipt is %+v (%v), want one for darwin/%s with no host mark", receipts, err,
			runtime.GOARCH)
	}
	if link, err := os.Readlink(rec.Entry); err != nil || !strings.HasPrefix(link, floor) {
		t.Errorf("~/.local/bin/%s in the floor links to %q (%v), not into the floor", bin, link, err)
	}
}

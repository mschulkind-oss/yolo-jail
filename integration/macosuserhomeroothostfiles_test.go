package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// macosUserHomeRootProbeName is the home-root host_files destination both tests below use. A
// FIXED NAME, NOT A NONCE, on purpose: the link the Mac test leaves in /Users/_yolojail is the
// same relative string on every run, so the next run reuses it rather than adding one more
// dangling link to the account home per run. No other test writes the name, so no real file
// can be sitting there for the layout to refuse.
const macosUserHomeRootProbeName = ".yolo-it-homeroot-probe"

// macosUserHomeRootProbe prints what the session reads through ~/<name>, and the link itself.
func macosUserHomeRootProbe() string {
	return strings.Join([]string{
		`echo "=== READ ==="; cat "$HOME/` + macosUserHomeRootProbeName + `" 2>&1`,
		`echo "=== LINK ==="; readlink "$HOME/` + macosUserHomeRootProbeName + `" 2>&1 || echo NOT-A-SYMLINK`,
		`echo "=== END ==="`,
	}, "\n")
}

// macosUserHomeRootProbeLink is the probe's LINK fence, trimmed.
func macosUserHomeRootProbeLink(stdout string) string {
	return strings.TrimSpace(section(stdout, "=== LINK ===", "=== END ==="))
}

// macosUserHomeRootProbeRead is the probe's READ fence.
func macosUserHomeRootProbeRead(stdout string) string {
	return section(stdout, "=== READ ===", "=== LINK ===")
}

// A HOME-ROOT host_files FILE IS PER-WORKSPACE ON macos-user, AS IT IS ON PODMAN.
//
// WHAT IT SETTLES. On podman a home-root entry (`~/.npmrc`) is a relative link in each jail's
// home skeleton, `~/<name> -> .config/yolo-home/<slug>`, and `~/.config` is the workspace's own
// sidecar, so each workspace has its own file. macos-user has ONE account home, and its layout
// redirected only core's three files, so the same entry was a real file every workspace shared:
// a `once` seeded by the first workspace was never seeded for the second at all. The layout now
// lays the podman link in the account home (entrypoint.DarwinHomeLayout.WithHostFileRedirects),
// and the host_files step writes the file through it.
//
// WHY AN INTEGRATION TEST. The Linux unit gate (internal/entrypoint/hostfileredirect_test.go)
// drives the real bootstrap against a real filesystem, two workspaces sharing one home included.
// What it cannot reach is the Seatbelt half: that the sandboxed session READS the file through
// two links (`~/<name>`, then `~/.config`) into its own workspace's sidecar, which the profile
// allows, while another workspace's sidecar, which it denies, is never on the path.
func TestMacosUserHomeRootHostFilesArePerWorkspace(t *testing.T) {
	requireMacosUser(t)
	name := macosUserHomeRootProbeName
	wantLink := (config.HostFileEntry{Path: name}).SymlinkTarget()
	entry := func(body string) string {
		return `{"host_files": [{"path": "~/` + name + `", "content": ` + strconv.Quote(body) +
			`, "mode": "once"}]}`
	}
	nonce := acParityNonce()
	bodyA, bodyB := "workspace-A-"+nonce+"\n", "workspace-B-"+nonce+"\n"
	wsA := macosUserWorkspace(t, entry(bodyA))
	wsB := macosUserWorkspace(t, entry(bodyB))

	rA := macosUserRunProbe(t, "home-root host_files (A)", wsA, macosUserHomeRootProbe())
	if got := macosUserHomeRootProbeLink(rA.stdout); got != wantLink {
		t.Errorf("in workspace A's session ~/%s is %q, want the link %q podman's skeleton lays: "+
			"anything else is a file every workspace on this Mac shares", name, got, wantLink)
	}
	if got := macosUserHomeRootProbeRead(rA.stdout); !strings.Contains(got, bodyA) {
		t.Errorf("workspace A's session reads %q through ~/%s, want its own entry's bytes %q",
			got, name, bodyA)
	}

	rB := macosUserRunProbe(t, "home-root host_files (B)", wsB, macosUserHomeRootProbe())
	if got := macosUserHomeRootProbeLink(rB.stdout); got != wantLink {
		t.Errorf("in workspace B's session ~/%s is %q, want the same link %q", name, got, wantLink)
	}
	readB := macosUserHomeRootProbeRead(rB.stdout)
	if !strings.Contains(readB, bodyB) {
		t.Errorf("workspace B's session reads %q through ~/%s, want its own entry's bytes %q. "+
			"With mode `once`, a file workspace A seeded in a SHARED home is never re-seeded "+
			"for B — the defect this test exists for", readB, name, bodyB)
	}
	if strings.Contains(readB, bodyA) {
		t.Errorf("workspace B's session reads workspace A's file through ~/%s: %q", name, readB)
	}

	// SEPARATED, NOT ERASED: A's file is still in A's sidecar after B ran, read on the host.
	inA := filepath.Join(wsA, ".yolo", "home", "config", "yolo-home", name)
	if got, err := os.ReadFile(inA); err != nil || string(got) != bodyA {
		t.Errorf("after workspace B's launch, %s holds %q (err %v), want A's own %q. `no such "+
			"file` means the layout separated the workspaces by losing one; `permission denied` "+
			"means this check could not be made from the host account", inA, got, err, bodyA)
	}
}

// THE PROBE ABOVE, RUN ON LINUX against a layout the REAL deriver laid, so a quoting slip in the
// script or a disagreement between wantLink and the layout surfaces here rather than on a Mac,
// where each run builds a native floor first. Not gated: it launches nothing.
func TestMacosUserHomeRootHostFilesProbeReadsARealLayout(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("no bash on PATH, so the probe script cannot be exercised here")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(base, "home")
	sidecar := filepath.Join(base, "ws", ".yolo", "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	entry := config.HostFileEntry{Path: macosUserHomeRootProbeName, Codec: "raw", HasContent: true, Mode: config.HostFileModeOnce}
	layout := entrypoint.DeriveDarwinHomeLayout(home, sidecar, nil, nil).
		WithHostFileRedirects([]config.HostFileEntry{entry}, nil)
	if err := layout.Apply(); err != nil {
		t.Fatalf("applying the real layout: %v", err)
	}
	// What the host_files step writes, at the physical path the layout's links lead to.
	written := filepath.Join(sidecar, "config", "yolo-home", entry.Slug())
	if err := os.MkdirAll(filepath.Dir(written), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(written, []byte("preflight-bytes\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "-c", macosUserHomeRootProbe())
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the probe script did not run cleanly: %v\n%s", err, out)
	}
	if got, want := macosUserHomeRootProbeLink(string(out)), entry.SymlinkTarget(); got != want {
		t.Errorf("the probe reads the link as %q, want %q:\n%s", got, want, out)
	}
	if got := macosUserHomeRootProbeRead(string(out)); !strings.Contains(got, "preflight-bytes") {
		t.Errorf("the probe does not read the file through the real layout's links: %q", got)
	}
}

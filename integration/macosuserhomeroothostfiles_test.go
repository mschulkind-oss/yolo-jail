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
// FIXED NAME, NOT A NONCE: no other test writes it, so no real file can be sitting there for
// the layout to refuse. (Before slice 12a the Mac test's link stayed behind in the shared
// /Users/_yolojail, and a fixed name kept that to one link; since MMP-D25 it is laid in each
// workspace's own home and goes with the workspace.)
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
// sidecar, so each workspace has its own file. Before slice 12a macos-user's account mode ran
// every workspace in ONE account home, where such an entry was a real file every workspace
// shared: a `once` seeded by the first workspace was never seeded for the second at all.
//
// THE GEOMETRY NOW (slice 12a, MMP-D25 in docs/design/macos-user-model-plan.md). Account mode
// runs with HOME = <ws>/.yolo/home, laid by entrypoint.DeriveDarwinWorkspaceHome with dotted
// names: `~/.config` is a REAL directory in that home, and the layout lays podman's same
// relative link `~/<name> -> .config/yolo-home/<slug>` in it
// (entrypoint.DarwinHomeLayout.WithHostFileRedirects). So the file is at
// <ws>/.yolo/home/.config/yolo-home/<slug> — not under the undotted `config/` the pre-12a
// account layout linked ~/.config to, which a workspace used before 12a may still hold, unused
// (OQ-MM4).
//
// WHY AN INTEGRATION TEST. The Linux unit gate (internal/entrypoint/hostfileredirect_test.go)
// drives the real bootstrap against a real filesystem. What it cannot reach is the Seatbelt
// half: that the sandboxed session READS the file through `~/<name>` inside its own workspace's
// home, which the profile allows, while another workspace's home, which it denies, is never on
// the path — and that two workspaces run as the same account each get their own bytes.
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

	// SEPARATED, NOT ERASED: A's file is still in A's home after B ran, read on the host, at the
	// path A's own link names relative to that home (MMP-D25: HOME is <ws>/.yolo/home).
	inA := filepath.Join(wsA, ".yolo", "home", filepath.FromSlash(wantLink))
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
	// Account mode's homes since slice 12a (MMP-D25): HOME is the workspace home itself, and one
	// machine root serves as both Machine and Shared.
	machine := filepath.Join(base, "machine")
	home := filepath.Join(base, "ws", ".yolo", "home")
	entry := config.HostFileEntry{Path: macosUserHomeRootProbeName, Codec: "raw", HasContent: true, Mode: config.HostFileModeOnce}
	layout := entrypoint.DeriveDarwinWorkspaceHome(
		entrypoint.DarwinHomes{Home: home, Machine: machine, Shared: machine}, nil, nil).
		WithHostFileRedirects([]config.HostFileEntry{entry}, nil)
	if err := layout.Apply(); err != nil {
		t.Fatalf("applying the real layout: %v", err)
	}
	if len(layout.HostFileRedirects) != 1 {
		t.Fatalf("the real layout lays %d host_files links for one home-root entry, want 1: %v",
			len(layout.HostFileRedirects), layout.HostFileRedirects)
	}
	// What the host_files step writes, at the path the Mac test reads A's file from on the host:
	// the link's own relative target under HOME, which is the dotted `.config` real directory.
	written := filepath.Join(home, filepath.FromSlash(entry.SymlinkTarget()))
	if want := filepath.Join(home, ".config", "yolo-home", entry.Slug()); written != want {
		t.Fatalf("the link's target resolves to %s, want %s", written, want)
	}
	if fi, err := os.Lstat(filepath.Join(home, ".config")); err != nil || !fi.IsDir() {
		t.Fatalf("~/.config is not a real directory in the workspace home (%v, %v), so the file "+
			"the Mac test reads would not be where it looks", fi, err)
	}
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

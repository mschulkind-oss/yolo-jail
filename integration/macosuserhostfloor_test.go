package integration

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// THE HOST FLOOR IS THE HOST USER'S ALONE, measured on a Mac (docs/design/host-tool-provisioning.md
// §3 and §8, HP-D8): the floor is a tree of executables `yolo host` runs with the user's full
// authority, and the macos-user sandbox account is another uid on the same machine. What keeps that
// account from reading or replacing them is the prefix's 0700 mode, which only a Mac can confirm:
// the account exists only there.
//
// THE FLOOR IS PUT WHERE ONLY ITS OWN MODE CAN STOP THE ACCOUNT. A real floor sits under the user's
// home, behind two more layers: the launch's Seatbelt profile denies reads under /Users outside the
// workspace and the account's own home (seatbelt.go, users-read-deny), and `yolo macos-setup` takes
// the account out of `staff` (macosuser.CreateUserCommands), the home's group. The suite's isolated
// home is under the per-user temp dir, whose own mode can stop the account before the prefix's is
// asked. So this test's HOME is minted under /private/tmp with every directory down to the prefix
// traversable, a control proves the account reaches the prefix's parent, and only then is the
// prefix itself probed. Every parent's `ls -led` is logged, so a failure says which layer answered.
//
// The floor is made by the floor's npm cell (installPinnedNpmOnTheFloor, hostfloor_test.go): the
// shipped Node tarball fetched and checked against yolo's compiled-in digest, and the pinned npm
// specimen installed with it. On the macos-user workflow's runner that is darwin-arm64, whose digest
// no other job checks; the macOS nightly runs the same cell on darwin-x64.
//
// ⚠ Mac-only, behind requireMacosUser: the sandbox account and passwordless sudo.
func TestMacosUserHostFloorIsTheHostUsersAlone(t *testing.T) {
	requireMacosUser(t)
	root, err := os.MkdirTemp("/private/tmp", "yolo-hostfloor-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeWorkspaceTree(t, root) })
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0o755); err != nil {
		t.Fatal(err)
	}
	packDir := filepath.Join(root, "pack")
	if err := os.Mkdir(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// requireMacosUser isolated a home already (hostHome is the machine's); this one stands in for it,
	// seeded the same way.
	if err := seedPackHome(home, hostHome, pinnedNpmFloorConfig(t, packDir)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	privateStateEntries(t, home, filepath.Base(paths.HostFloorDirUnder(home)))

	made := installPinnedNpmOnTheFloor(t, t.TempDir(), home)
	floor, err := filepath.EvalSymlinks(made)
	if err != nil {
		t.Fatal(err)
	}
	logParentModes(t, floor)
	if hostHome != "" {
		// The machine's own home, the layer a real floor has in front of its own mode: logged rather
		// than relied on.
		logLs(t, hostHome)
	}

	// THE CONTROL: the account reaches the prefix's parent and lists it, so whatever stops it below is
	// the prefix itself, and sudo can run as the account at all.
	parent := filepath.Dir(floor)
	if out, err := asSandboxUser("/bin/ls", parent); err != nil || !strings.Contains(out, filepath.Base(floor)) {
		t.Fatalf("control: %s cannot list %s, which every mode down to it should allow, so the probes below "+
			"would not be measuring the floor's own mode: %v\n%s", macosuser.SandboxUser, parent, err, out)
	}
	for _, probe := range [][]string{
		{"/bin/ls", floor},
		{"/bin/cat", filepath.Join(floor, "bin", pinnedNpmBin)},
	} {
		out, err := asSandboxUser(probe...)
		if err == nil || !strings.Contains(out, "Permission denied") {
			t.Errorf("%s ran `%s` on this user's floor: %v\n%s — the floor's 0700 must stop another uid",
				macosuser.SandboxUser, strings.Join(probe, " "), err, out)
		}
	}

	// END TO END: from inside a macos-user launch, the agent's own view. The config is emptied first,
	// so the launch stages no pack; the floor stays, since only `yolo host apply --assert` removes an
	// entry. /bin/ls and /bin/cat by path, so neither the sandbox's PATH nor the generated rc's `ls`
	// alias decides what runs.
	if err := os.WriteFile(filepath.Join(home, ".config", "yolo-jail", "config.jsonc"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := macosUserWorkspace(t, `{}`)
	r := runMacosUser(t, ws, `echo "=== FLOOR ==="; /bin/ls '`+floor+`' 2>&1; echo "LS_RC=$?"; `+
		`/bin/cat '`+filepath.Join(floor, "bin", pinnedNpmBin)+`' 2>&1; echo "CAT_RC=$?"; echo "=== END ==="`)
	if r.rc != 0 {
		t.Fatalf("the macos-user launch failed (rc %d) before its probe could answer:\nstdout:\n%s\nstderr:\n%s",
			r.rc, r.stdout, r.stderr)
	}
	got := section(r.stdout, "=== FLOOR ===", "=== END ===")
	t.Logf("inside the sandbox:\n%s", got)
	if strings.Contains(got, "LS_RC=0") || strings.Contains(got, "CAT_RC=0") || strings.Contains(got, "host agent floor") ||
		!strings.Contains(got, "LS_RC=") {
		t.Errorf("a macos-user sandbox read this user's floor at %s:\n%s", floor, got)
	}
}

// asSandboxUser runs argv as the sandbox account, without a password prompt, and returns its
// combined output.
func asSandboxUser(argv ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", "-u", macosuser.SandboxUser}, argv...)...)
	cmd.Stdin = nil
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// logParentModes logs `ls -led` of path and of every directory above it, the modes and ACLs that
// decide whether another uid reaches it.
func logParentModes(t *testing.T, path string) {
	t.Helper()
	for p := path; ; p = filepath.Dir(p) {
		logLs(t, p)
		if p == filepath.Dir(p) {
			return
		}
	}
}

// logLs logs `ls -led` of one directory: its mode, owner, group and ACL entries.
func logLs(t *testing.T, dir string) {
	t.Helper()
	out, err := exec.Command("/bin/ls", "-led", dir).CombinedOutput()
	if err != nil {
		t.Logf("ls -led %s: %v %s", dir, err, out)
		return
	}
	t.Logf("%s", strings.TrimSpace(string(out)))
}

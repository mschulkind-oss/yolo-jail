package integration

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
)

// THE INSTALL-CAPTURE PROFILE'S DENIAL PROBE (docs/plans/install-capture.md, slice 6's hardware
// item 3): the profile a capture runs a vendor installer under (macosuser.SeatbeltCaptureProfile),
// loaded by the kernel as the sandbox account, and the kernel's answer for each path that profile
// names. The recording half of a capture was measured on hardware on 2026-09-11; that a capture
// cannot write the shared sandbox home, the one claim that tells a confined capture from one that
// silently wrote there, had no run at all.
//
// THE PROFILE AND THE TREE ARE THE CAPTURE'S OWN. macosuser.BuildCapturePlan builds both, with a
// capture root of the test's own under /Users/Shared, CapturePlanInvariants must pass on that plan
// first, and the plan's PrepareCommands make the staging tree under sudo, as RunCapturePlan does.
// Nothing else of a capture runs: no binary is staged, no bootstrap, no installer.
//
// EVERY CASE RUNS BARE FIRST, as the sandbox account under the same sudo and environment, where it
// must succeed. A refusal is the profile's only when the same command, as the same account, works
// without it (macosuserseatbelt_test.go's rule, for its reason), so a failed control fails the test
// rather than being counted.
//
// ⚠ Not the session profile's suite. That one (macosuserseatbelt_test.go) loads the session
// profile as the invoking user; a capture runs as the sandbox account against a staging HOME, and
// the shared home it must not touch is that account's own, so only a run as that account measures
// it. Hence requireMacosUser's gate (the account and passwordless sudo), not the seatbelt one.

// captureSeatbeltFixture is the tree the capture cases are written against.
type captureSeatbeltFixture struct {
	plan macosuser.CapturePlan
	// root is the capture root the plan was built with: traversal only, as the capture root
	// under /Users/Shared is on a Mac (macosuser.CaptureRootDefault).
	root string
	// sharedHome is the sandbox account's home, which the capture must neither write nor read;
	// a stand-in directory in the Linux twin.
	sharedHome string
	// sibling is a directory beside the staging tree under root, holding a file: another
	// program's staging tree, as far as the profile can tell.
	sibling string
	// neutral is a path outside the writable set that the account could write without the
	// profile: under /private/var/tmp on a Mac, so the write deny is what refuses it and not the
	// /Users read deny.
	neutral string
	// probe is the file name every write case creates and removes, unique to the run.
	probe string
}

// captureSeatbeltTree builds the plan and the fixture paths over root, sharedHome and neutralDir,
// and creates the sibling. It does NOT make the staging tree: on a Mac that is the plan's
// PrepareCommands under sudo, and in the twin a plain MkdirAll.
func captureSeatbeltTree(t *testing.T, root, sharedHome, neutralDir string) captureSeatbeltFixture {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The invoking user owns the staging tree, as at `yolo capture`. Only the Mac case runs the
	// chown that names it; a twin on a uid with no passwd entry still gets a plan.
	hostUser := os.Getenv("USER")
	if me, err := user.Current(); err == nil {
		hostUser = me.Username
	}
	// SelfExe is any non-empty path: the plan's StageCommands, which copy it, are never run here,
	// and CapturePlanInvariants refuses a plan with no binary to stage. The launch env is empty
	// rather than nil, as `yolo capture` always composes one (the bootstrap env reads it).
	plan := macosuser.BuildCapturePlan(macosuser.CaptureOptions{
		Bin: "probetool", Config: jsonx.NewOrderedMap(), SandboxEnv: jsonx.NewOrderedMap(),
		SelfExe: self, HostUser: hostUser, CaptureRoot: root,
	})
	if problems := macosuser.CapturePlanInvariants(plan); len(problems) > 0 {
		t.Fatalf("the capture plan over %s is not viable, so the profile it carries is not a "+
			"capture's:\n  %s", root, strings.Join(problems, "\n  "))
	}
	stamp := fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixNano())
	f := captureSeatbeltFixture{
		plan:       plan,
		root:       root,
		sharedHome: sharedHome,
		sibling:    filepath.Join(root, "sibling"),
		neutral:    filepath.Join(neutralDir, "yolo-capsb-write-"+stamp),
		probe:      ".yolo-capsb-probe-" + stamp,
	}
	if err := os.MkdirAll(f.sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.sibling, "secret"), []byte("another capture's file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

// captureSeatbeltCase is one probe: the script, run as the sandbox account, bare and then under
// the capture profile, and what the sandboxed run must show.
type captureSeatbeltCase struct {
	name string
	why  string
	// want is wantRefused or wantAllowed (macosuserseatbelt_test.go's vocabulary).
	want seatbeltWant
	// refusal is text a refused run's output must carry, when the shell reports the errno.
	refusal string
	script  func(captureSeatbeltFixture) string
}

func captureSeatbeltCases() []captureSeatbeltCase {
	return []captureSeatbeltCase{
		{
			name: "shared_home_write_refused",
			why: "the shared sandbox home is the machine's one credential store for every " +
				"workspace; a capture records what a vendor installer leaves behind, not what it did there",
			want: wantRefused, refusal: "Operation not permitted",
			script: func(f captureSeatbeltFixture) string {
				p := sh(filepath.Join(f.sharedHome, f.probe))
				return "touch " + p + " && rm -f " + p
			},
		},
		{
			name: "shared_home_read_refused",
			why: "reads under /Users are denied with the sandbox home NOT re-allowed: an installer " +
				"that could read the credential store could also ship it somewhere",
			want: wantRefused,
			script: func(f captureSeatbeltFixture) string {
				return "ls " + sh(f.sharedHome)
			},
		},
		{
			name: "staging_home_and_out_writable",
			why:  "the staging tree is the one writable subtree: the installer's home and the out dir the delta moves into",
			want: wantAllowed,
			script: func(f captureSeatbeltFixture) string {
				h, o := sh(filepath.Join(f.plan.StagingHome, f.probe)), sh(filepath.Join(f.plan.OutDir, f.probe))
				return "touch " + h + " && rm -f " + h + " && touch " + o + " && rm -f " + o + " && echo " + seatbeltOK
			},
		},
		{
			name: "capture_root_traversable",
			why:  "the capture root is granted as a literal, so the staging tree under it can be reached",
			want: wantAllowed,
			script: func(f captureSeatbeltFixture) string {
				return "test -d " + sh(f.root) + " && echo " + seatbeltOK
			},
		},
		{
			name: "sibling_under_the_root_refused",
			why: "the capture root is traversal only, never a subpath: a subpath grant there would " +
				"re-allow reads of every other program's staging tree beside this one",
			want: wantRefused,
			script: func(f captureSeatbeltFixture) string {
				return "ls " + sh(f.sibling) + " && cat " + sh(filepath.Join(f.sibling, "secret"))
			},
		},
		{
			name: "neutral_write_refused",
			why: "writes are denied everywhere but the staging tree and the OS scratch dirs; " +
				"/private/var/tmp is neither, and is readable, so only the write deny can refuse this",
			want: wantRefused,
			script: func(f captureSeatbeltFixture) string {
				p := sh(f.neutral)
				return "touch " + p + " && rm -f " + p
			},
		},
	}
}

// captureSeatbeltRootPrefix is what every capture root this file mints starts with, which is what
// the one root `rm -rf` below checks before it runs.
const captureSeatbeltRootPrefix = sharedUsersDir + "/yolo-capsb-"

// TestMacosUserCaptureSeatbeltProfileDeniesTheSharedHome loads the capture profile with
// sandbox-exec as the sandbox account and asks the kernel about every case above.
func TestMacosUserCaptureSeatbeltProfileDeniesTheSharedHome(t *testing.T) {
	requireMacosUser(t)
	dir, err := os.MkdirTemp(sharedUsersDir, "yolo-capsb-")
	if err != nil {
		t.Fatalf("creating the capture root under %s: %v", sharedUsersDir, err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { removeCaptureSeatbeltRoot(t, root) })
	// 1777, as /Users/Shared is: the plan's PrepareCommands make the staging tree under it as
	// root, and the profile's directory below is the test's own.
	if err := os.Chmod(root, 0o777|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	f := captureSeatbeltTree(t, root, macosuser.SandboxHome(), "/private/var/tmp")
	t.Cleanup(func() {
		for _, p := range []string{filepath.Join(f.sharedHome, f.probe), f.neutral} {
			if _, err := os.Lstat(p); err == nil {
				if !runQuiet(time.Minute, "sudo", "-n", "/bin/rm", "-f", p) {
					t.Logf("could not remove the probe file %s; remove it by hand", p)
				}
			}
		}
	})
	for _, cmd := range f.plan.PrepareCommands {
		if out, err := exec.Command("sudo", append([]string{"-n"}, cmd...)...).CombinedOutput(); err != nil {
			t.Fatalf("the capture plan's prepare step `sudo %s` failed: %v\n%s", strings.Join(cmd, " "), err, out)
		}
	}
	// The profile where the sandbox account can read it: a 0755 directory of the test's under the
	// root, never t.TempDir(), which is the invoking user's 0700 /var/folders.
	profileDir := filepath.Join(root, "profile")
	if err := os.Mkdir(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(profileDir, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := filepath.Join(profileDir, "capture.sb")
	if err := os.WriteFile(profile, []byte(f.plan.Seatbelt), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("capture Seatbelt profile under test (%s):\n%s", profile, f.plan.Seatbelt)

	bare := []string{"sudo", "-n", "--user=" + macosuser.SandboxUser, "/usr/bin/env", "-i",
		"HOME=" + f.plan.StagingHome, "PATH=/usr/bin:/bin"}
	sandboxed := append(append([]string{}, bare...), "/usr/bin/sandbox-exec", "-f", profile, "--")
	for _, tc := range captureSeatbeltCases() {
		t.Run(tc.name, func(t *testing.T) {
			script := tc.script(f)
			bareOut, bareRC := runScript(t, script, bare)
			if bareRC != 0 || (tc.want == wantAllowed && !strings.Contains(bareOut, seatbeltOK)) {
				t.Fatalf("the CONTROL failed: `%s` exits %d as %s without the profile, so this case "+
					"cannot tell a refusal by the profile from one by the machine.\nwhy: %s\noutput:\n%s",
					script, bareRC, macosuser.SandboxUser, tc.why, bareOut)
			}
			sbOut, sbRC := runScript(t, script, sandboxed)
			switch tc.want {
			case wantRefused:
				if sbRC == 0 {
					t.Errorf("NOT REFUSED. `%s` succeeded under the capture profile.\nwhy: %s\noutput:\n%s",
						script, tc.why, sbOut)
				} else if tc.refusal != "" && !strings.Contains(sbOut, tc.refusal) {
					t.Errorf("REFUSED, but not with %q, so something other than the profile may have "+
						"refused it.\nwhy: %s\noutput:\n%s", tc.refusal, tc.why, sbOut)
				}
			case wantAllowed:
				if !strings.Contains(sbOut, seatbeltOK) {
					t.Errorf("REFUSED, and should not have been: `%s` did not print %s under the "+
						"capture profile (rc %d).\nwhy: %s\noutput:\n%s", script, seatbeltOK, sbRC, tc.why, sbOut)
				}
			}
		})
	}
	if t.Failed() {
		logSandboxDenials(t)
	}
}

// logSandboxDenials logs the kernel's own record of what Seatbelt refused in the last few
// minutes, for a failure message: the evidence the hardware checklist asks for beside the
// refusal itself. Best effort and bounded; `log show` can be slow.
func logSandboxDenials(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "log", "show", "--last", "5m", "--style", "compact",
		"--predicate", `sender == "Sandbox"`).CombinedOutput()
	text := string(out)
	if len(text) > 8000 {
		text = "…" + text[len(text)-8000:]
	}
	t.Logf("`log show` Sandbox lines (err %v):\n%s", err, text)
}

// removeCaptureSeatbeltRoot deletes a capture root this file minted. The staging tree in it is
// root's and the sandbox account's, so the removal is `sudo rm -rf`, the one in this file, and it
// refuses any path but a direct child of /Users/Shared with this file's prefix.
func removeCaptureSeatbeltRoot(t *testing.T, root string) {
	t.Helper()
	if err := os.RemoveAll(root); err == nil {
		return
	}
	if !strings.HasPrefix(root, captureSeatbeltRootPrefix) || strings.Contains(root, "..") ||
		filepath.Dir(root) != sharedUsersDir {
		t.Fatalf("refusing to escalate removal of %s: it is not a capture root this test minted "+
			"under %s", root, sharedUsersDir)
	}
	if !runQuiet(2*time.Minute, "sudo", "-n", "/bin/rm", "-rf", root) {
		t.Logf("could not remove the capture root %s even with sudo; remove it by hand", root)
	}
}

// TestMacosUserCaptureSeatbeltControlsRunUnsandboxed runs every capture case's BARE CONTROL on the
// machine that develops this repo, over stand-ins for the shared home and the neutral directory,
// and checks the plan is viable and each control leaves nothing behind. It asserts nothing about
// Seatbelt, for TestMacosUserSeatbeltContentControlsRunUnsandboxed's reason: the scripts are built
// by concatenation, and nobody who writes them can run the case above, which skips everywhere but
// a Mac with the sandbox account. Not behind a gate, so not in the vacuity ledger.
func TestMacosUserCaptureSeatbeltControlsRunUnsandboxed(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on PATH")
	}
	root := resolvedTempDir(t)
	f := captureSeatbeltTree(t, root, filepath.Join(resolvedTempDir(t), "shared-home"), resolvedTempDir(t))
	for _, d := range []string{f.sharedHome, f.plan.StagingHome, f.plan.OutDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The plan must be over the root it was handed: its staging tree under it, and the profile
	// granting that tree, or the case above would measure a profile for some other tree.
	if !strings.HasPrefix(f.plan.StagingRoot, root+"/") ||
		!strings.Contains(f.plan.Seatbelt, `(subpath "`+f.plan.StagingRoot+`")`) {
		t.Fatalf("the plan's staging tree %s is not under the capture root %s, or its profile does "+
			"not grant it:\n%s", f.plan.StagingRoot, root, f.plan.Seatbelt)
	}
	for _, tc := range captureSeatbeltCases() {
		out, rc := runScript(t, tc.script(f), nil)
		if rc != 0 {
			t.Errorf("%s: the bare control exits %d, so on a Mac this case would report a broken "+
				"control instead of measuring the profile:\n%s", tc.name, rc, out)
		}
		if tc.want == wantAllowed && !strings.Contains(out, seatbeltOK) {
			t.Errorf("%s: the bare control does not print %s:\n%s", tc.name, seatbeltOK, out)
		}
		for _, p := range []string{filepath.Join(f.sharedHome, f.probe), filepath.Join(f.plan.StagingHome, f.probe),
			filepath.Join(f.plan.OutDir, f.probe), f.neutral} {
			if _, err := os.Lstat(p); err == nil {
				t.Errorf("after %s's control, %s is left behind", tc.name, p)
			}
		}
	}
}

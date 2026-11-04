package integration

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// AN UNCHANGED RELAUNCH ON A MAC KEEPS EVERY DELIVERED FILE, AND A DELIVERED FILE THAT WAS EDITED IS
// REPLACED (MMP-D35, docs/design/macos-user-model-plan.md), in both macos_user_mode values.
//
// The home overlay delivers COPIES of the staged files and never tries a hard link from the staging
// root, so the deny-by-default Seatbelt profile needs no file-link grant. An unchanged relaunch
// compares each delivered copy with the staged file by mode and bytes and rewrites nothing, so the
// inode stays; a copy whose bytes differ is replaced.
//
// The Linux gate pins every hop with fakes: the host overlay reused on an unchanged digest
// (run.TestAnUnchangedMacosUserRelaunchReusesTheHomeOverlay), no stage command for a current sealed
// copy (macosuser.TestALaunchStagesTheOverlayOnlyWhenItsStagedCopyIsStale), and the guest's content
// comparison (entrypoint.TestOverlayInstallOfAnUnchangedStagedTreeReplacesNothing,
// entrypoint.TestOverlayInstallReplacesADestinationChangedBetweenSessions). What only a Mac can say:
// that the real bootstrap on APFS, as the real sandbox account, keeps the inode, delivers the staged
// bytes, replaces an edited copy, and no longer logs the "delivered as a copy … refused" line W1
// saw on every launch.
//
// THE EDIT is made between launches, outside the session profile (which write-protects every
// destination, G14), as the account that owns the delivered copy: the sandbox account in account
// mode, the test runner itself in run-as-you mode.
func TestMacosUserUnchangedRelaunchKeepsTheDeliveredFiles(t *testing.T) {
	requireMacosUser(t)
	for _, mode := range []string{"account", "self"} {
		t.Run(mode, func(t *testing.T) {
			packHome(t, `{"macos_user_mode": "`+mode+`", "packs": ["claude"]}`)
			ws := macosUserWorkspace(t, `{}`)
			probe := `
skill=$(ls -d "$HOME"/.claude/skills/*/SKILL.md 2>/dev/null | head -n 1)
echo "=== DELIVERED ==="
printf 'rel|%s\n' "${skill#"$HOME"/}"
printf 'physical|%s\n' "$(cd "$(dirname "$skill")" 2>/dev/null && printf '%s/%s' "$(pwd -P)" "$(basename "$skill")" || echo NONE)"
printf 'inode|%s\n' "$(stat -f %i "$skill" 2>/dev/null || echo NONE)"
echo "=== END DELIVERED ==="
`
			launch := func(n int) map[string]string {
				t.Helper()
				r := runMacosUserAs(t, mode, ws, probe)
				if r.rc != 0 {
					t.Fatalf("launch %d failed (rc %d)\nstdout:\n%s\nstderr:\n%s", n, r.rc, r.stdout, r.stderr)
				}
				got := macosUserHomeProbeFields(t, r.stdout, "DELIVERED")
				if got["rel"] == "" || got["physical"] == "NONE" || got["inode"] == "NONE" {
					t.Fatalf("launch %d delivered no skill under ~/.claude/skills:\n%s", n, r.stdout)
				}
				return got
			}
			first := launch(1)
			staged := filepath.Join(macosuser.StagedHomeOverlay(naming.FromWorkspace(ws), ""), filepath.FromSlash(first["rel"]))
			requireStagedBytes := func(n int, got map[string]string) {
				t.Helper()
				want, err := os.ReadFile(staged)
				if err != nil {
					t.Fatalf("reading the staged skill %s: %v (the staging root moved?)", staged, err)
				}
				have, err := os.ReadFile(got["physical"])
				if err != nil {
					t.Fatalf("reading the delivered skill %s after launch %d: %v", got["physical"], n, err)
				}
				if !bytes.Equal(have, want) {
					t.Errorf("after launch %d the delivered skill %s is not the staged file's bytes:\n%q\nwant %q",
						n, got["physical"], have, want)
				}
			}

			requireStagedBytes(1, first)
			second := launch(2)
			requireStagedBytes(2, second)
			if first["inode"] != second["inode"] {
				t.Errorf("the unchanged relaunch replaced %s (inode %s, then %s): the guest re-delivered "+
					"an unchanged overlay", second["rel"], first["inode"], second["inode"])
			}
			if log, err := os.ReadFile(entrypoint.BootLogPath(ws)); err == nil &&
				strings.Contains(string(log), "delivered as a copy") {
				t.Errorf("the boot log still reports a copy as a refused link (MMP-D35):\n%s", log)
			}

			// THE EDIT, between sessions, as the copy's owner.
			edit := `chmod u+w "$1" && printf '\nan edit\n' >> "$1"`
			if mode == "account" {
				if out, err := runAsSandboxAccount(t, "/bin/sh", "-c", edit, "sh", second["physical"]); err != nil {
					t.Fatalf("the sandbox account could not edit its delivered copy %s: %v\n%s", second["physical"], err, out)
				}
			} else {
				if err := os.Chmod(second["physical"], 0o644); err != nil {
					t.Fatal(err)
				}
				f, err := os.OpenFile(second["physical"], os.O_WRONLY|os.O_APPEND, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteString("\nan edit\n")
				if cerr := f.Close(); err == nil {
					err = cerr
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			third := launch(3)
			requireStagedBytes(3, third)
			if third["inode"] == second["inode"] {
				t.Logf("the edited copy was rewritten under the same inode number %s (APFS reused it); the bytes "+
					"above are the assertion", third["inode"])
			}
		})
	}
}

package integration

import (
	"strings"
	"testing"
)

// G36 ON A MAC — THE OVERLAY INSTALL KEEPS THE AGENT STATE BESIDE ITS DESTINATIONS.
//
// WHAT IT SETTLES. The skills and briefing the host stages for the sandbox account are
// copied over its home at every boot (entrypoint.InstallHomeOverlay). The copy used to
// replace the first REAL directory below the home's layout symlinks, which for a pack whose
// skills live at `.<agent>/agent/skills` is `~/.<agent>/agent` — the agent's whole state
// dir. So every launch deleted the state the previous session had written, and even the
// config the same boot had just generated.
//
// The Linux gate reproduces the wipe with the real layout code for every shipped pack
// (internal/entrypoint/darwinoverlaystate_test.go). What it cannot reach, and this can: the
// destination list surviving the root-owned `sudo cp -R` staging, the bootstrap reading it
// as the sandbox user from /var/yolo-jail, and the install's symlink containment check
// resolving the REAL /Users/_yolojail and /Users/Shared paths without refusing them.
//
// THE PACK IS omp, not pi, on purpose: it has pi's layout — a `scope: workspace` state dir
// with the destinations one level below it — and no `needs`, so the launch does not also
// bring up openai-auth's loophole and this test measures one thing.
func TestMacosUserOverlayInstallKeepsTheAgentStateBesideItsSkills(t *testing.T) {
	requireMacosUser(t)
	packHome(t, `{"packs": ["omp"]}`)
	ws := macosUserWorkspace(t, `{}`)

	// Launch 1 writes agent state where omp keeps it, AS THE SANDBOX USER — so the state
	// has the owner and the ACL a real session's would, rather than the test runner's.
	r1 := runMacosUser(t, ws, `
mkdir -p "$HOME/.oh-omp/agent/sessions"
printf 'omp sign-in' > "$HOME/.oh-omp/agent/auth.json"
printf 'a session' > "$HOME/.oh-omp/agent/sessions/it.jsonl"
echo "=== PLANTED ==="
printf 'auth|%s\n' "$(cat "$HOME/.oh-omp/agent/auth.json" 2>/dev/null || echo ABSENT)"
echo "=== END PLANTED ==="
`)
	if r1.rc != 0 {
		t.Fatalf("the first macos-user launch failed (rc %d), so nothing below is a statement "+
			"about the overlay install.\nstdout:\n%s\nstderr:\n%s", r1.rc, r1.stdout, r1.stderr)
	}
	if got := macosUserHomeProbeFields(t, r1.stdout, "PLANTED")["auth"]; got != "omp sign-in" {
		t.Fatalf("the first launch could not plant omp's state (read back %q), so its survival "+
			"below would prove nothing", got)
	}

	// Launch 2: its boot installs the overlay again. Everything planted must still be there,
	// the config this boot generated must be there, and the skills must have arrived.
	r2 := runMacosUser(t, ws, `
echo "=== STATE ==="
printf 'auth|%s\n' "$(cat "$HOME/.oh-omp/agent/auth.json" 2>/dev/null || echo GONE)"
printf 'session|%s\n' "$(cat "$HOME/.oh-omp/agent/sessions/it.jsonl" 2>/dev/null || echo GONE)"
if [ -f "$HOME/.oh-omp/agent/models.yml" ]; then g=PRESENT; else g=GONE; fi
printf 'generated|%s\n' "$g"
if [ -f "$HOME/.oh-omp/agent/skills/configuring-the-jail/SKILL.md" ]; then s=PRESENT; else s=ABSENT; fi
printf 'skills|%s\n' "$s"
if [ -f "$HOME/.oh-omp/agent/AGENTS.md" ]; then b=PRESENT; else b=ABSENT; fi
printf 'briefing|%s\n' "$b"
if [ -L "$HOME/.oh-omp" ]; then l=SYMLINK; else l=NOT-A-SYMLINK; fi
printf 'layout|%s\n' "$l"
echo "=== END STATE ==="
`)
	if r2.rc != 0 {
		t.Fatalf("the second macos-user launch failed (rc %d). If stderr names "+
			"install_home_overlay, read its message: the install refuses a destination that is "+
			"a symbolic link, or whose directory resolves outside the sandbox home and the "+
			"workspace sidecar — and on a real Mac that refusal would be a false one.\n"+
			"stdout:\n%s\nstderr:\n%s", r2.rc, r2.stdout, r2.stderr)
	}
	state := macosUserHomeProbeFields(t, r2.stdout, "STATE")
	for key, want := range map[string]string{
		"auth":      "omp sign-in",
		"session":   "a session",
		"generated": "PRESENT",
		"skills":    "PRESENT",
		"briefing":  "PRESENT",
		"layout":    "SYMLINK",
	} {
		if got := state[key]; got != want {
			t.Errorf("%s = %q after the second launch, want %q.\n"+
				"GONE for auth, session or generated is G36 itself: the overlay install replaced "+
				"~/.oh-omp/agent — the directory ABOVE its destinations — instead of only "+
				"~/.oh-omp/agent/skills and ~/.oh-omp/agent/AGENTS.md "+
				"(docs/reference/macos-user-home-tiers.md#the-overlay-replaces-its-destinations-and-nothing-else).",
				key, got, want)
		}
	}
	if t.Failed() {
		t.Logf("second launch stderr:\n%s", strings.TrimSpace(r2.stderr))
	}
}

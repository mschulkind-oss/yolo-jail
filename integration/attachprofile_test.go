package integration

// attachprofile_test.go is the end-to-end acceptance of PER-ENTRY channel delivery
// (docs/design/agent-auth-modes.md §4.3, implemented as the yolo-user-env.sh channel
// section): a jail launched with NO profile selected carries no provider environment
// in its frozen container env, and an attach that selects one DELIVERS it — same
// running jail, per-session env. Until per-entry delivery shipped, 'yolo -p zai --
// claude' against a running jail parsed, validated, composed the channel, and
// dropped it silently.
//
// The unit tier pins the write, the pre-flight and the refusals
// (internal/cli/run attachchannel_test.go, userenv_test.go; internal/entrypoint
// boot_test.go pins the hydrate override). Only a real jail proves the whole chain:
// the host write into the live-mounted file, the exec'd boot's re-hydration, and
// the shell sourcing what this entry composed.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAttachDeliversTheSelectedProfile: launch unprofiled, attach with
// '-p claude=zai', and the attached session's environment carries z.ai's pair —
// while the FIRST session's environment provably carried none (the frozen-env half
// of the assertion, read from the first session's own output).
func TestAttachDeliversTheSelectedProfile(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "zai"]}`)
	t.Setenv("ZAI_API_KEY", "integration-probe-not-a-real-key")
	// The invoking environment may carry inherited ANTHROPIC_* (the session running
	// this suite exports them); pin sentinels so the assertion reads what this ATTACH
	// composed, not what the host shell had.
	t.Setenv("ANTHROPIC_BASE_URL", "inherited")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "inherited")

	// The first launch holds its jail open via the live /workspace bind, and reports
	// whether ITS session carried the provider pair (it must not: nothing selected).
	const releaseName = "release-attach-profile"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-HAS-$(env | grep -c '^ANTHROPIC_BASE_URL=' || true); `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	t.Cleanup(func() {
		_ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644)
	})

	// Wait for the first session's COMMAND to have run — the FIRST-HAS marker prints
	// only after the boot's provisioning finishes, which is well after the container
	// exists. (runningContainers>0 would race the marker; the marker IS the sync point.)
	deadline := time.Now().Add(jailTimeout())
	for !strings.Contains(first.combined(), "FIRST-HAS-") {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first session never reported its env within %s:\n%s",
				jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if up := first.combined(); !strings.Contains(up, "FIRST-HAS-0") {
		t.Errorf("the UNPROFILED first session must carry no provider pair "+
			"(the frozen env this test needs to be empty):\n%s", up)
	}

	// The attach: same workspace, same running jail, one profile on the flag.
	r := runCommand(t, dir, append(jailRunArgs(),
		"-p", "claude=zai", "--", "bash", "-lc",
		`env | grep -E '^ANTHROPIC_(BASE_URL|AUTH_TOKEN)=' | sort`))
	if r.rc != 0 {
		t.Fatalf("profiled attach failed: rc %d\n%s", r.rc, r.combined())
	}
	if got := r.combined(); !strings.Contains(got, "Attaching to existing jail") {
		t.Errorf("the launch must report attaching to the running jail "+
			"(otherwise this tested a fresh launch):\n%s", got)
	}
	// The attach banner is stdout's first line (the attach arm prints it there), so the
	// exact-match discipline applies to everything AFTER it.
	if !strings.HasPrefix(r.stdout, "Attaching to existing jail") {
		t.Errorf("the attach report is not where the pair should follow it:\n%s", r.stdout)
	}
	tail := r.stdout[strings.IndexByte(r.stdout, '\n')+1:]
	want := "ANTHROPIC_AUTH_TOKEN=integration-probe-not-a-real-key\n" +
		"ANTHROPIC_BASE_URL=https://api.z.ai/api/anthropic\n"
	if tail != want {
		t.Errorf("the attached session's provider env =\n%s\nwant exactly the composed pair "+
			"(z.ai, delivered per-entry into a jail that launched without it):\n%s", tail, want)
	}
	// The disclosure belongs to the delivery: an attach that delivers a profile says
	// which packs declared the name it carried.
	if !strings.Contains(r.stderr, "Profile zai: declared: zai") {
		t.Errorf("the attach must print where the selection landed:\n%s", r.stderr)
	}
}

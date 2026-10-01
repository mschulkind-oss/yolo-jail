package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// APPLE CONTAINER HOME FACTS BUILT ON LINUX AND NEVER BOOTED, ASKED AS EXPERIMENTS — a third
// batch under the exception applecontainer_test.go's header names, kept by
// applecontainerparity_test.go's rule: BOTH ANSWERS PASS, each is recorded on one
// `AC-PARITY <fix> VERDICT:` line, and only an experiment not conducted is red.
//
//	login-seed       TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed   docs/design/base-home-legacy-state.md §3, OQ-BH12
//
// They run in apple-container.yml, on the maintainer's SELF-HOSTED Mac (the only Apple Container
// instrument there is; that workflow's header says why no hosted runner can run the backend).
// None adds a job or a trigger there: the job selects every TestAppleContainer… test by name.

// TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed asks OQ-BH12's fix on the hardware.
//
// Apple Container binds the workspace overlay WHOLE at /home/agent, so its ~/.claude.json is
// <ws>/.yolo/home/.claude.json. Until 2026-09-25 the launch synced the Claude login seed with
// podman's dot-stripped path instead, so the seed never reached an Apple Container jail, and it
// created podman's undotted bind sources (`~/claude`, `~/npm-global`) in that home for nothing.
// The fix is runtime-aware paths in prepareWsState (claudeJSONInWsState, and no
// preparePodmanBindSources on this backend), unit-tested in internal/cli/run/acseed_test.go.
// What only a boot shows is that a FRESH workspace's jail then reads the seed's login at
// ~/.claude.json and has the selected pack's dotted dir and none of the undotted ones.
//
// THE SEED IS THIS TEST'S, never the machine's: the isolated home's link to the machine's state
// dir is replaced by a private dir linking back everything except `home` (privateStateEntries),
// the store the seed lives in, and a seed carrying a per-run fake login is written there. No
// agent starts, so the fake login is only bytes in a file.
func TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed(t *testing.T) {
	const fix = "login-seed"
	dir := appleContainerWorkspace(t)
	privateStateEntries(t, os.Getenv("HOME"), hostHome, filepath.Base(paths.GlobalHome()))
	nonce := acParityNonce()
	email := "yolo-it-" + nonce + "@example.invalid"
	seed := filepath.Join(paths.GlobalHome(), ".claude", "claude.json")
	if err := os.MkdirAll(filepath.Dir(seed), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seed, []byte(`{"oauthAccount": {"emailAddress": "`+email+`"}, `+
		`"hasCompletedOnboarding": true}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res := acParityRun(t, fix, dir, strings.Join([]string{
		`echo "=== SEED ==="; cat ~/.claude.json 2>&1`,
		`echo "=== DIRS ==="`,
		`for d in .claude claude npm-global; do if [ -d "$HOME/$d" ]; then echo "$d|DIR"; else echo "$d|ABSENT"; fi; done`,
		`echo "=== END ==="`,
	}, "\n"))
	jailSeed := section(res.stdout, "=== SEED ===", "=== DIRS ===")
	dirs := map[string]string{}
	for _, line := range strings.Split(section(res.stdout, "=== DIRS ===", "=== END ==="), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "|"); ok {
			dirs[k] = v
		}
	}
	if len(dirs) == 0 {
		t.Fatalf("%s %s: the probe printed no directory facts, so NOTHING WAS MEASURED:\n%s",
			acParityTag, fix, lastLines(res.stdout, 30))
	}
	hostCopy, _ := os.ReadFile(filepath.Join(dir, ".yolo", "home", ".claude.json"))

	var wrong []string
	if !strings.Contains(jailSeed, email) {
		wrong = append(wrong, "the jail's ~/.claude.json lacks the seed's login")
	}
	for d, want := range map[string]string{".claude": "DIR", "claude": "ABSENT", "npm-global": "ABSENT"} {
		if dirs[d] != want {
			wrong = append(wrong, fmt.Sprintf("~/%s is %s, want %s", d, dirs[d], want))
		}
	}
	evidence := fmt.Sprintf("host <ws>/.yolo/home/.claude.json carries the seed's login: %v\n"+
		"jail ~/.claude.json:\n%s\ndirs: %v", strings.Contains(string(hostCopy), email),
		lastLines(jailSeed, 12), dirs)
	if len(wrong) > 0 {
		acParityRecord(t, fix, false, "a fresh workspace's jail does not boot the way OQ-BH12's fix "+
			"says: "+strings.Join(wrong, "; "), evidence)
		return
	}
	acParityRecord(t, fix, true, "a fresh workspace's jail reads the seed's login at "+
		"~/.claude.json, has ~/.claude, and has neither undotted podman bind source", evidence)
}

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// ONE APPLE CONTAINER HOME FACT IS STILL ASKED AS AN EXPERIMENT — the third batch under the
// exception applecontainer_test.go's header names, kept by applecontainerparity_test.go's rule:
// both answers pass, are recorded on one `AC-PARITY <fix> VERDICT:` line, and only an experiment
// not conducted is red.
//
//	storage-classes  TestAppleContainerBriefingCarriesItsStorageClasses       docs/design/durable-scratch-space.md, DS-P1
//
// The login-seed boot check is promoted to a hard regression assertion; see
// TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed and its offline assertion controls in
// applecontainerseedassert_test.go. The storage-classes experiment runs in apple-container.yml,
// on the maintainer's SELF-HOSTED Mac (the only Apple Container instrument there is; that
// workflow's header says why no hosted runner can run the backend). The job selects every
// TestAppleContainer… test by name.

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
// THE SEED IS THIS TEST'S, never the machine's: the isolated home's link to the shared state
// dir (this run's own, or the machine's) is replaced by a private dir linking back everything
// except `home` (privateStateEntries), the store the seed lives in, and a seed carrying a per-run
// fake login is written there. No agent starts, so the fake login is only bytes in a file.
func TestAppleContainerFreshWorkspaceBootsWithTheLoginSeed(t *testing.T) {
	const fix = "login-seed"
	dir := appleContainerWorkspace(t)
	privateStateEntries(t, os.Getenv("HOME"), filepath.Base(paths.GlobalHome()))
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
	probe, err := parseACLoginSeedProbe(res.stdout)
	if err != nil {
		t.Fatalf("%s %s: %v, so NOTHING WAS MEASURED:\n%s",
			acParityTag, fix, err, lastLines(res.stdout, 30))
	}
	hostCopy, hostCopyErr := os.ReadFile(filepath.Join(dir, ".yolo", "home", ".claude.json"))

	evidence := fmt.Sprintf("host <ws>/.yolo/home/.claude.json carries the seed's login: %v (read error: %v)\n"+
		"jail ~/.claude.json:\n%s\ndirs: %v", hostCopyErr == nil && strings.Contains(string(hostCopy), email),
		hostCopyErr, lastLines(probe.jailSeed, 12), probe.dirs)
	if err := acLoginSeedFailures(email, probe, string(hostCopy), hostCopyErr); err != nil {
		t.Fatalf("%s %s VERDICT: DOES NOT HOLD — a fresh workspace's jail does not boot the way "+
			"OQ-BH12's fix says: %v\n\nevidence:\n%s", acParityTag, fix, err, evidence)
	}
	t.Logf("%s %s VERDICT: HOLDS — a fresh workspace's jail reads the seed's login at "+
		"~/.claude.json, has ~/.claude, has neither undotted podman bind source, and the host copy "+
		"is readable\n\nevidence:\n%s", acParityTag, fix, evidence)
}

// TestAppleContainerBriefingCarriesItsStorageClasses asks the storage-classes section's Apple
// Container wording on the hardware.
//
// The section is rendered from the launch's own persistence map (DS-P1), and Apple Container's
// map differs from podman's in exactly two facts: the whole home is one per-workspace bind, and
// the per-launch paths are tmpfs. So its section says the per-launch set is in RAM, puts all of
// /home/agent in the per-workspace class, and names what yolo rewrites at each launch instead of
// a read-only rest (internal/jailcontent/persistencesection.go). Linux pins that text from a
// hand-built map; only a boot shows the map the Apple Container launch really builds reaching
// the briefing that ARRIVES, and $YOLO_DURABLE_DIR naming a directory the jail can write.
func TestAppleContainerBriefingCarriesItsStorageClasses(t *testing.T) {
	const fix = "storage-classes"
	dir := appleContainerWorkspace(t)
	res := acParityRun(t, fix, dir, strings.Join([]string{
		`echo "=== BRIEFING ==="; cat ~/.claude/CLAUDE.md 2>&1`,
		`echo "=== DURABLE ==="`,
		`echo "dir|${` + durable.EnvVar + `-UNSET}"`,
		`if [ -n "${` + durable.EnvVar + `-}" ] && touch "$` + durable.EnvVar + `/.yolo-it-probe" 2>/dev/null; then rm -f "$` + durable.EnvVar + `/.yolo-it-probe"; echo "write|ALLOWED"; else echo "write|DENIED"; fi`,
		`echo "=== END ==="`,
	}, "\n"))
	briefing := section(res.stdout, "=== BRIEFING ===", "=== DURABLE ===")
	if !strings.Contains(briefing, "# YOLO Jail Environment") {
		t.Fatalf("%s %s: no yolo briefing arrived at ~/.claude/CLAUDE.md, so there is no section "+
			"to read — NOTHING WAS MEASURED:\n%s", acParityTag, fix, lastLines(briefing, 30))
	}
	facts := map[string]string{}
	for _, line := range strings.Split(section(res.stdout, "=== DURABLE ===", "=== END ==="), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "|"); ok {
			facts[k] = v
		}
	}

	var wrong []string
	for _, want := range []string{
		"## Storage classes: what survives a restart",
		"- **Per launch** (in RAM)",
		"all of `/home/agent` outside the other classes",
		"- **Rewritten at each launch**",
		"`$" + durable.EnvVar + "`",
	} {
		if !strings.Contains(briefing, want) {
			wrong = append(wrong, fmt.Sprintf("the section lacks %q", want))
		}
	}
	for _, unwanted := range []string{"- **Read-only**: the rest of", "- **Per launch** (on disk)"} {
		if strings.Contains(briefing, unwanted) {
			wrong = append(wrong, fmt.Sprintf("the section carries podman's %q", unwanted))
		}
	}
	if facts["dir"] != durable.ContainerJailPath {
		wrong = append(wrong, fmt.Sprintf("$%s is %q, want %s", durable.EnvVar, facts["dir"], durable.ContainerJailPath))
	}
	if facts["write"] != "ALLOWED" {
		wrong = append(wrong, "the jail cannot write in $"+durable.EnvVar)
	}
	evidence := fmt.Sprintf("durable facts: %v\nsection:\n%s", facts,
		lastLines(section(briefing, "## Storage classes", "\n## "), 20))
	if len(wrong) > 0 {
		acParityRecord(t, fix, false, "the delivered storage-classes section is not Apple "+
			"Container's: "+strings.Join(wrong, "; "), evidence)
		return
	}
	acParityRecord(t, fix, true, "the delivered briefing carries Apple Container's storage "+
		"classes, and $"+durable.EnvVar+" is a writable "+durable.ContainerJailPath, evidence)
}

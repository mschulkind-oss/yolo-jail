package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// THE GITHUB PACK TELLS EVERY AGENT HOW `gh` BEHAVES IN A JAIL (docs/design/boundary-broker.md
// BB-D68), so an agent does not learn each rule by failing once: `gh` is a forwarder at
// ~/.yolo/bin/block/gh and not a blocker, filters work, a write exits 77 at once, raw GraphQL
// is refused while gh's own commands work, and how a branch with a slash goes into a `gh api`
// path. Driven through the production staging and briefing refresh with the REAL official
// packs, so deleting the file or the convention that delivers it fails here.
func TestTheGitHubPackBriefsEveryAgentOnGHInTheJail(t *testing.T) {
	shipped, err := officialpacks.FS.ReadFile("github/briefing/gh.md")
	if err != nil {
		t.Fatalf("the github pack ships no briefing: %v", err)
	}
	prose := string(shipped)
	flat := strings.Join(strings.Fields(prose), " ")
	for _, want := range []string{
		"`gh` here is `~/.yolo/bin/block/gh`, the github pack's forwarder, not a blocker.",
		"`--jq` and `--template` work as in `gh`",
		"exits 77 at once",
		"Ask the user to run it on the host.",
		"Raw `gh api graphql` is always refused; `gh`'s own commands",
		"`repos/OWNER/REPO/branches/MS%2Fmain`",
		"`gh auth status` names the login",
		// The workspace entry (docs/design/workspace-widening.md WW-D6): where to add a
		// repository, that the user must restart and approve it, and that nothing changes
		// before then.
		"A repository this workspace has no remote for stays out of scope until the user approves it.",
		"Add it to the `repos` list under `brokered.github` in the workspace config file your environment briefing names",
		"(create the key only if the file has none)",
		"or in the local file beside it for what the project should not commit, or when the config file is read-only here",
		"Then run `yolo check --no-build`, and ask the user to restart the jail and answer y to the repository-scope prompt.",
		"Nothing changes before that restart.",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("the github briefing lacks %q:\n%s", want, prose)
		}
	}

	home := packHome(t)
	writeUserPacks(t, home, `["claude", "pi", "github"]`)
	pinLauncherInJail(t, false)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	_, packs, proses, err := o.stagePacks("yolo-ws-abcd1234")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	cfg, _ := persistenceFixture(t, "")
	o.ensureDurableDir("podman", cfg)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, "podman",
		stagedPacks{packs: packs, briefings: proses}, ioprio.Normal)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	// Broadcast: every agent's briefing carries it once.
	for _, dest := range []string{".claude/CLAUDE.md", ".pi/agent/AGENTS.md"} {
		raw, err := os.ReadFile(filepath.Join(staging, briefingStagingName(dest)))
		if err != nil {
			t.Fatalf("no %s briefing written: %v", dest, err)
		}
		if n := strings.Count(string(raw), strings.TrimRight(prose, "\n")); n != 1 {
			t.Errorf("%s carries the github briefing %d times, want 1:\n%s", dest, n, raw)
		}
	}
}

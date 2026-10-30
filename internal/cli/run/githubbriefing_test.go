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
// ~/.yolo/bin/block/gh and not a blocker, filters work, an in-scope write waits for one exact
// host Allow-once decision, raw GraphQL is refused while gh's own commands work, and a branch
// with a slash goes into a `gh api` path. Driven through the production staging and briefing
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
		"files a pending request and waits briefly for the host",
		"exact host approval",
		"joins this workspace's **working set**",
		"A merge always asks.",
		"`yolo approve <id> --add` to add the PR or issue it writes to the working set",
		"A configured `on_request` hook is optional; its output is never consent.",
		"Raw `gh api graphql` is always refused; `gh`'s own commands",
		"`repos/OWNER/REPO/branches/MS%2Fmain`",
		"`gh auth status` names the login",
		// The workspace entry (docs/design/workspace-widening.md WW-D6): where to add a
		// repository, that the user approves it with `yolo config apply`, and that nothing changes
		// before then.
		"A repository this workspace has no remote for stays out of scope until the user approves it.",
		"Run `yolo request github repos OWNER/REPO`, which writes this project's config",
		"ask the user to run the same command on the host, in this workspace",
		"Then run `yolo check --no-build`, and ask the user to run `yolo config apply` on the host, in this workspace, and answer y to the repository-scope prompt.",
		"In a jail started by this yolo, the running broker takes the repository at its next call; an older one needs a restart. Nothing changes before that y.",
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

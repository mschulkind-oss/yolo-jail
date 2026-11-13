package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
)

// THE USER BLOCK COMES FIRST (docs/design/briefing-diet.md §7, the owner's ruling BD-D11). Each
// destination's briefing is two blocks: the user's (the host file `after` names, then
// agents_md_extra, then each non-shipped pack's prose in config order), then yolo's (any alert,
// the core, then each shipped pack's pointer), with one `---` rule between them when both are
// non-empty. So an agent that cuts a long file from the end loses yolo's text before any of the
// user's. These tests drive the launch's own staging and briefing write, so they fail if the call
// site stops composing in two blocks.

// stagedClaudeBriefing stages the packs a user config at home selects and returns the Claude
// briefing refreshJailBriefings writes for cfg.
func stagedClaudeBriefing(t *testing.T, home string, configure func(cfg map[string]any)) string {
	t.Helper()
	pinLauncherInJail(t, false)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	_, packs, proses, err := o.stagePacks("yolo-ws-abcd1234")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	cfg, _ := persistenceFixture(t, "")
	extra := map[string]any{}
	if configure != nil {
		configure(extra)
	}
	for k, v := range extra {
		cfg.Set(k, v)
	}
	o.ensureDurableDir("podman", cfg)
	// The launch's fact sheet, so the topics are staged into the skill as a launch stages them,
	// and the briefing is the core rather than the inline fallback.
	staged := stagedPacks{packs: packs, briefings: proses}
	facts := o.mechanicsFactSheet("yolo-ws-abcd1234", cfg, "podman", staged, storePackagesPlan{}, nil)
	staging, err := o.refreshJailBriefings("yolo-ws-abcd1234", cfg, "podman", staged, ioprio.Normal, facts)
	if err != nil {
		t.Fatalf("refreshJailBriefings: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(staging, briefingStagingName(claudeBriefingDest)))
	if err != nil {
		t.Fatalf("no Claude briefing written: %v", err)
	}
	return string(raw)
}

// localProsePack writes a local pack named name whose one briefing file is prose.
func localProsePack(t *testing.T, name, prose string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "briefing"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, dir, `{"name":"`+name+`"}`)
	if err := os.WriteFile(filepath.Join(dir, "briefing", "rules.md"), []byte(prose+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// inOrder fails unless each marker occurs in body, each after the one before it.
func inOrder(t *testing.T, body string, markers ...string) {
	t.Helper()
	at := -1
	for _, m := range markers {
		i := strings.Index(body, m)
		if i < 0 {
			t.Fatalf("%q is not in the composed briefing:\n%s", m, body)
		}
		if i <= at {
			t.Fatalf("%q is out of order: the composed briefing must read %q in that order:\n%s",
				m, markers, body)
		}
		at = i
	}
}

// The user's host file, agents_md_extra and local pack precede yolo's header, core and shipped
// pointers in the jail's composed file, with one rule between the two blocks.
func TestTheUsersProsePrecedesYolosCoreInTheJail(t *testing.T) {
	home := packHome(t)
	// The host file `after` names: the claude pack's names ~/.claude/CLAUDE.md, which on a host
	// that never ran `yolo host apply` is the user's own file.
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "CLAUDE.md"), []byte("HOST FILE RULE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	local := localProsePack(t, "mine", "LOCAL PACK RULE")
	writeUserPacks(t, home, `["claude", "github", {"name":"mine","source":"file://`+local+`"}]`)
	body := stagedClaudeBriefing(t, home, func(cfg map[string]any) {
		cfg["agents_md_extra"] = "EXTRA RULE"
	})
	inOrder(t, body, "HOST FILE RULE", "EXTRA RULE", "LOCAL PACK RULE", "\n---\n",
		"# YOLO Environment", "**yolo-gh**")
	if n := strings.Count(body, "\n---\n"); n != 1 {
		t.Errorf("the two blocks are separated by exactly one rule, found %d:\n%s", n, body)
	}
}

// A shipped pack the user has forked into a local copy is no longer shipped: its prose is the
// user's, and composes in the user block ahead of yolo's core.
func TestAForkedShippedPackIsUserProse(t *testing.T) {
	home := packHome(t)
	fork := localProsePack(t, "github", "FORKED GITHUB RULE")
	writeUserPacks(t, home, `["claude", {"name":"github","source":"file://`+fork+`"}]`)
	body := stagedClaudeBriefing(t, home, nil)
	inOrder(t, body, "FORKED GITHUB RULE", "\n---\n", "# YOLO Environment")
}

// yoloBlockBudget is the bound on yolo's block for Claude with claude, git, github and guardrails
// selected (docs/design/briefing-diet.md §8, BD-D4): 9.7% of the 26,867 bytes it replaced.
const yoloBlockBudget = 2600

// With no user prose the file is yolo's block alone, with no rule, and that block fits its budget.
// Measured through the launch's own composer over the reference selection, with the facts that add
// lines: a forwarded port, the durable dir, the guardrails blocks.
func TestYolosBlockAloneHasNoRuleAndFitsItsBudget(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "git", "github", "guardrails"]`)
	body := stagedClaudeBriefing(t, home, func(cfg map[string]any) {
		cfg["network"] = newConfig("forward_host_ports", []any{8919})
	})
	if !strings.HasPrefix(body, "# YOLO Environment") {
		t.Errorf("with no user prose the file opens with yolo's block:\n%s", body)
	}
	if strings.Contains(body, "\n---\n") {
		t.Errorf("with no user prose there is no rule:\n%s", body)
	}
	t.Logf("yolo's block: %d bytes", len(body))
	if len(body) > yoloBlockBudget {
		lines := strings.Split(body, "\n")
		longest := append([]string{}, lines...)
		for i := 0; i < len(longest); i++ {
			for j := i + 1; j < len(longest); j++ {
				if len(longest[j]) > len(longest[i]) {
					longest[i], longest[j] = longest[j], longest[i]
				}
			}
		}
		if len(longest) > 3 {
			longest = longest[:3]
		}
		t.Errorf("yolo's composed block is %d bytes, %d over its %d-byte budget. Move text into a "+
			"topic file of the jail-mechanics skill or into the pack's skill; do not raise the "+
			"number. Its longest lines:\n%s\n\nThe block:\n%s",
			len(body), len(body)-yoloBlockBudget, yoloBlockBudget, strings.Join(longest, "\n"), body)
	}
}

// AN ATTACH KEEPS THE ORIGIN. An attach composes over the running jail's tree (loadPackTree), not
// the launch's loaded packs, so a shipped pack read back from that tree must still be shipped, or
// every attach would move yolo's pointers into the user block.
func TestAnAttachReadsEachPacksOriginBackFromTheTree(t *testing.T) {
	home := packHome(t)
	local := localProsePack(t, "mine", "LOCAL PACK RULE")
	writeUserPacks(t, home, `["claude", "github", {"name":"mine","source":"file://`+local+`"}]`)
	pinLauncherInJail(t, false)
	emptyLoopholeDirs(t)
	o := goldenOptions(t.TempDir(), home)
	o.Stdout, o.Stderr = discardBuf(), discardBuf()
	root, _, _, err := o.stagePacks("yolo-ws-abcd1234")
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	booted, err := loadPackTree(root)
	if err != nil {
		t.Fatalf("loadPackTree: %v", err)
	}
	shipped := map[string]bool{}
	for _, b := range quietPackBriefings(booted) {
		shipped[b.Name] = b.Shipped
	}
	if !shipped["github"] || shipped["mine"] {
		t.Errorf("read back from the tree, github is shipped and mine is not: %v", shipped)
	}
}

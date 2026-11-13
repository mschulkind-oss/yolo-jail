package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE PACK BUDGET BINDS SHIPPED PACKS ONLY (docs/design/briefing-diet.md BD-D4, §6). A user's
// local pack and a fetched pack are user prose: yolo does not cap, trim or summarize it, however
// far past the 250 bytes a shipped pack may spend it runs (packload's
// TestEveryShippedPacksProseFitsItsBudgetPerDestination is the shipped half). Driven through the
// jail's production staging and briefing write, and the host notch's composer, with prose many
// times the budget, so a cap added to either composer fails here.
func TestAUserPacksLongProseIsComposedWhole(t *testing.T) {
	home := packHome(t)
	long := func(who string) string {
		var b strings.Builder
		b.WriteString("## " + who + " rules\n\n")
		for i := 0; b.Len() < 2000; i++ {
			b.WriteString("- " + who + " rule: every line of this is the user's own and reaches the agent whole.\n")
		}
		return strings.TrimRight(b.String(), "\n")
	}
	localProse, fetchedProse := long("local"), long("fetched")

	local := t.TempDir()
	if err := os.MkdirAll(filepath.Join(local, "briefing"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, local, `{"name":"mine"}`)
	if err := os.WriteFile(filepath.Join(local, "briefing", "rules.md"), []byte(localProse+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fetched := fetchedPackSource(t, map[string]string{
		"pack.json":         `{"name":"far"}`,
		"briefing/rules.md": fetchedProse + "\n",
	})
	writeUserPacks(t, home, `["claude", {"name":"mine","source":"file://`+local+`"},`+
		`{"name":"far","source":"`+fetched+`"}]`)
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
	raw, err := os.ReadFile(filepath.Join(staging, briefingStagingName(".claude/CLAUDE.md")))
	if err != nil {
		t.Fatalf("no briefing written: %v", err)
	}
	resolved, _ := packload.ResolveDestinations(packs)
	hostHome := t.TempDir()
	var host string
	for _, d := range entrypoint.ComposeHostBriefings(resolved, hostHome, "", false) {
		if d.Path == filepath.Join(hostHome, ".claude", "CLAUDE.md") {
			host = d.Content
		}
	}
	for notch, body := range map[string]string{"jail": string(raw), "host": host} {
		for who, prose := range map[string]string{"local": localProse, "fetched": fetchedProse} {
			if strings.Count(body, prose) != 1 {
				t.Errorf("%s: the %s pack's %d-byte prose is not composed whole, once:\n%s",
					notch, who, len(prose), body)
			}
		}
	}
}

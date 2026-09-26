package integration

// attachpacktree_test.go is the end-to-end acceptance of ONE IMMUTABLE PACK TREE PER LAUNCH,
// the maintainer's OQ-PK2 ruling, option (c) (docs/reference/pack-system.md#oq-pk2): a running
// jail keeps the pack tree it booted with, an attach after a config change neither re-stages it
// nor hands the jail the configured packs, and the attach says what differs and that a restart
// picks it up. The unit tier (internal/cli/run packtree_test.go, concurrentstaging_test.go)
// drives the same attach against a faked runtime; only a real jail shows what the jail itself
// sees at /ctx/packs, through a real bind of the host tree, and that the tree goes when the jail
// does.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	naming "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// firstPacksAnswer is the first session's sync point: the ANSWER to its /ctx/packs listing, the
// marker followed by at least one character, and never the boot's echo of the command, which
// carries the marker with `$(` after it.
var firstPacksAnswer = regexp.MustCompile(`FIRST-PACKS-[a-z,]+`)

// TestAnAttachKeepsThePackTreeTheJailBootedWith: launch with the zai pack selected, drop it from
// the config, and attach. The attached session still sees zai at /ctx/packs — the tree its jail
// booted with — and the attach says zai was removed and names the restart. Once the first
// launch's jail is gone, its tree goes too, and the attach left none of its own behind.
func TestAnAttachKeepsThePackTreeTheJailBootedWith(t *testing.T) {
	requireJail(t)

	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["zai"]}`)
	cname := naming.FromWorkspace(dir)

	const releaseName = "release-pack-tree"
	first := startYoloBackground(t, "first", dir,
		`echo FIRST-PACKS-$(ls /ctx/packs/_official | tr '\n' ,); `+
			`for _ in $(seq 1 600); do [ -f /workspace/`+releaseName+` ] && break; sleep 0.5; done`)
	release := func() { _ = os.WriteFile(filepath.Join(dir, releaseName), []byte("go\n"), 0o644) }
	t.Cleanup(release)

	deadline := time.Now().Add(jailTimeout())
	for !firstPacksAnswer.MatchString(first.combined()) {
		select {
		case err := <-first.done:
			t.Fatalf("first launch exited (%v) before its session ran:\n%s", err, first.combined())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("first session never listed its packs within %s:\n%s", jailTimeout(), first.combined())
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got := firstPacksAnswer.FindString(first.combined()); !strings.Contains(got, "zai") {
		t.Fatalf("the first session's /ctx/packs does not hold zai (%q), so this test cannot tell a "+
			"kept tree from a re-staged one:\n%s", got, first.combined())
	}

	// The config drops zai. `packs` is user scope, so the user config is what changes.
	userConfig := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail", "config.jsonc")
	if err := os.WriteFile(userConfig, []byte(`{"packs": []}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := runCommand(t, dir, append(jailRunArgs(), "--", "bash", "-lc",
		`echo ATTACH-PACKS-$(ls /ctx/packs/_official | tr '\n' ,)`))
	if r.rc != 0 {
		t.Fatalf("the attach failed: rc %d\n%s", r.rc, r.combined())
	}
	if !strings.Contains(r.stdout, "Attaching to existing jail") {
		t.Fatalf("the launch did not attach, so it tested a fresh launch:\n%s", r.combined())
	}
	if !strings.Contains(r.stdout, "ATTACH-PACKS-zai") {
		t.Errorf("the attached session no longer sees zai at /ctx/packs: the attach re-staged the "+
			"tree the running jail binds\n%s", r.combined())
	}
	for _, want := range []string{"configured packs differ", "removed zai", "yolo stop"} {
		if !strings.Contains(r.stderr, want) {
			t.Errorf("the attach's notice does not say %q:\n%s", want, r.stderr)
		}
	}
	if strings.Contains(r.stderr, "could not find the pack tree") {
		t.Errorf("the attach could not find the tree the jail booted from — the fresh launch "+
			"did not record it:\n%s", r.stderr)
	}

	release()
	if rc := first.wait(t, jailTimeout()); rc != 0 {
		t.Errorf("first launch rc = %d, want 0:\n%s", rc, first.combined())
	}
	// The first launch saw its container go and collected the tree it had handed it; the attach
	// discarded the one it staged to compare. Nothing is left under the workspace's root.
	entries, err := os.ReadDir(paths.PackTreeRoot(cname))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	if len(left) != 0 {
		t.Errorf("after the jail ended, %s still holds %v", paths.PackTreeRoot(cname), left)
	}
}

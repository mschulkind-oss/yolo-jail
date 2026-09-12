package cli

// adoptionarchiveslot_test.go is OQ-CO7's D1 end to end, across the two packages that own the
// halves of it (docs/design/config-ownership-and-promotion.md §6.3.3): `yolo config reset`
// lives here, the adoption archive lives in internal/entrypoint, and the defect was the
// sentence between them — reset destroyed the one record saying the file on disk was yolo's
// own output, and the next render therefore read the file it had just written as the user's.
//
// It cannot be stated in either package alone, which is why it is a test of its own rather
// than an assertion added to hostadoptionarchive_test.go: entrypoint cannot call reset, and a
// unit test of reset can only pin the sidecar it leaves, not what the next apply then does
// with it. What makes the slot load-bearing is that there is exactly ONE per surface, forever,
// so a false copy is not a wasted file — it is the net for the real adoption, already spent.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// ownHomeWithNoAgentFile is a real home under `host_management: own` whose agent file does not
// exist yet — the only state in which this defect is reachable, because a home that HAD a file
// spends the slot legitimately on the first apply and the idempotency check covers it from
// then on.
//
// YOLO_VERSION is cleared for hostResetFixture's reason: `reset` must take the host branch
// here, which is the branch a user at their own machine takes, and the suite runs inside a
// jail where the ambient environment says otherwise.
func ownHomeWithNoAgentFile(t *testing.T) (settings, archive, baseline string) {
	t.Helper()
	home := t.TempDir()
	selectPacks(t, home, `"claude"`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":["claude"],"host_management":"own"}`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")

	settings = filepath.Join(home, ".claude", "settings.json")
	archive = filepath.Join(paths.GlobalStorageUnder(home), "archive", "config",
		"claude-settings", "settings.json")
	baseline = filepath.Join(render.Host(home, nil, render.OwnershipOwn).SidecarDir(),
		"claude-settings.last_render")
	return settings, archive, baseline
}

// RESET MUST NOT SPEND THE ADOPTION ARCHIVE, and the slot must still be there for the real
// thing afterwards. Four steps, and the last one is the half that makes this a data-loss test
// rather than a tidiness test.
//
//  1. an apply over an ABSENT file: yolo creates it, archives nothing, slot untouched.
//  2. `yolo config reset`: discards the overlay and truncates the file to its pure render.
//  3. the next apply: this is where the defect lived. With no baseline over a non-empty file
//     the render took the first-migration branch, adopted, and copied YOLO'S OWN OUTPUT into
//     the slot — announcing it as "the file as yolo found it".
//  4. a genuine adoption afterwards — a hand-edited file whose baseline is gone — must still
//     get the copy. Under the defect it got `Archived: ""` and nothing on disk, because step 3
//     had already spent the one slot this surface will ever have.
func TestResetDoesNotSpendTheAdoptionArchiveOnYolosOwnOutput(t *testing.T) {
	settings, archive, baseline := ownHomeWithNoAgentFile(t)

	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("first apply rc=%d\n%s", rc, report)
	}
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("the `own` apply did not render %s: %v", settings, err)
	}
	if _, err := os.Stat(archive); err == nil {
		t.Fatalf("an apply over an ABSENT file archived something at %s", archive)
	}

	var out, errw bytes.Buffer
	if rc := configReset([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("reset rc=%d\n%s%s", rc, out.String(), errw.String())
	}

	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("post-reset apply rc=%d\n%s", rc, report)
	}
	if data, err := os.ReadFile(archive); err == nil {
		t.Fatalf("the post-reset apply spent this surface's one-time adoption archive on "+
			"yolo's own output:\n%s\n\nThe copy exists to hold the file as yolo FOUND it, and "+
			"there is one per surface forever — so this is not a stray file, it is the net for "+
			"the next real adoption, already spent (OQ-CO7 D1).", data)
	}
	if strings.Contains(report, archive) {
		t.Errorf("the apply named an archive it did not (and must not) write:\n%s", report)
	}

	// The genuine adoption: the user's own file, and a baseline that has gone the way a
	// restored or half-wiped capture store loses one.
	const userSeed = `{"apiKeyHelper":"/usr/local/bin/acme-key.sh","permissions":{"ask":["Bash(rm:*)"]}}`
	writeFile(t, settings, userSeed)
	if err := os.Remove(baseline); err != nil {
		t.Fatal(err)
	}
	if rc, report := applyWith(t, true, nil); rc != 0 {
		t.Fatalf("adopting apply rc=%d\n%s", rc, report)
	}
	got, err := os.ReadFile(archive)
	if err != nil {
		t.Fatalf("a genuine adoption of %s got no archive (%v) — the slot was spent earlier "+
			"and the user's file is the thing that is now unrecoverable", settings, err)
	}
	if string(got) != userSeed {
		t.Errorf("the archive holds:\n%s\nwant the user's own file:\n%s\n\nA copy of an "+
			"earlier render in this slot is worse than none: the report names it as the "+
			"original, so the user finds a file that is not theirs.", got, userSeed)
	}
}

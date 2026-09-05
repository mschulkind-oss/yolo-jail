package check

// needspack_test.go pins `yolo check`'s half of the `needs` closure
// (docs/design/wire-bridge.md §3.1, WB-D12): the additions print beside the pack
// list, and a closure refusal is a FAIL — the launch refuses both, so a check
// that passed on either would pass on a config that cannot start a jail.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// needsPackDir writes a local pack declaring one needs entry, with enough content
// to stage (pack.json alone stages fine), and returns its dir. The dir basename
// is the pack name, which is what a bare-string file:// config entry names it.
func needsPackDir(t *testing.T, name, needsJSON string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"` + name + `","needs":` + needsJSON + `}`
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestSectionPacksPrintsNeedsAdditions: a selected pack whose live need names an
// embedded official pack shows the addition in check's output (WB-D12 — the same
// cause string the launch banner carries) and does not fail: the launch will join
// the pack, so passing here is the truth.
func TestSectionPacksPrintsNeedsAdditions(t *testing.T) {
	needy := needsPackDir(t, "needy", `[{"pack":"zai","when_bins":["claude"]}]`)
	packsFixture(t, `{"packs": ["claude", "file://`+needy+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)

	if r.failed != 0 {
		t.Fatalf("a live need must not fail check:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "+ zai (needed by needy: claude selected)") {
		t.Errorf("check must print the addition (WB-D12), naming the added pack, the "+
			"needing pack and the bin that fired:\n%s", buf.String())
	}
}

// TestSectionPacksFailsOnANonEmbeddedNeed: a need naming a pack outside the
// embedded official set is a FAIL naming the target — the launch refuses it, so a
// passing check would pass on a config that cannot start a jail.
func TestSectionPacksFailsOnANonEmbeddedNeed(t *testing.T) {
	needy := needsPackDir(t, "needy", `[{"pack":"ghost"}]`)
	packsFixture(t, `{"packs": ["claude", "file://`+needy+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)

	if r.failed == 0 {
		t.Fatalf("a need naming a non-embedded pack must FAIL check — the launch refuses "+
			"it, so a passing check would be a lie:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), "ghost") {
		t.Errorf("the failure must name the target the need asked for:\n%s", buf.String())
	}
}

// TestSectionPacksPrintsNothingWhenNoNeedIsLive: the control — an unmet
// condition adds no line, so a reader can trust the additions it does see.
func TestSectionPacksPrintsNothingWhenNoNeedIsLive(t *testing.T) {
	needy := needsPackDir(t, "needy", `[{"pack":"zai","when_bins":["codex"]}]`)
	packsFixture(t, `{"packs": ["claude", "file://`+needy+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{}).sectionPacks(r)

	if r.failed != 0 {
		t.Fatalf("an unmet need must not fail check:\n%s", buf.String())
	}
	if got := buf.String(); strings.Contains(got, "+ zai") {
		t.Errorf("an unmet condition must print no addition:\n%s", got)
	}
}

package cli

// packlintdescribes_test.go pins the `describes` half of `yolo pack lint`'s delivery listing
// (docs/design/boundary-broker.md BB-D69): a gated briefing names its gate, and the notch that
// leaves it out, on its own delivery line. Through packMain, so the call site is pinned too.

import (
	"path/filepath"
	"strings"
	"testing"

	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

func TestPackLintNamesTheDescribesGateOnTheDeliveryLine(t *testing.T) {
	// The SHIPPED github pack, copied out of the embed, so the line is the one its author sees.
	dir := filepath.Join(t.TempDir(), "github")
	for _, rel := range []string{"pack.json", "briefing/gh.md",
		"loopholes/github-broker/manifest.jsonc"} {
		raw, err := officialpacks.FS.ReadFile("github/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(dir, filepath.FromSlash(rel)), string(raw))
	}
	rc, report := lintReport(t, dir)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	if !hasLine(report, "briefing/gh.md", "every agent (declared broadcast)", "contributes[1]",
		"only where intercept applies — not at the host") {
		t.Errorf("lint does not name the gate on briefing/gh.md's delivery:\n%s", report)
	}

	// A kind the host delivers gates nothing there, and the note says only the gate.
	other := t.TempDir()
	writeFile(t, filepath.Join(other, "briefing", "env.md"), "About the env.\n")
	writeFile(t, filepath.Join(other, "pack.json"), `{"name":"p","contributes":[`+
		`{"kind":"briefing","from":"briefing/env.md","describes":["env"]},`+
		`{"kind":"env","vars":{"A":"1"}}]}`)
	rc, report = lintReport(t, other)
	if rc != 0 {
		t.Fatalf("lint rc=%d:\n%s", rc, report)
	}
	if !hasLine(report, "briefing/env.md", "(only where env applies)") ||
		strings.Contains(report, "not at the host") {
		t.Errorf("an env-gated briefing holds at the host, and lint must not say otherwise:\n%s", report)
	}
}

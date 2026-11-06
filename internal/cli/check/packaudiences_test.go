package check

// packaudiences_test.go pins `yolo check`'s FS-D6 rows (docs/design/pack-files-for-agent-state.md#FS-D6,
// §4.2 and §10): content addressed to an agent no selected pack provides is a [SKIP] row naming
// the pack, the kind and the agent as written, with `yolo pack add <pack>` beneath it when a
// pack yolo ships provides that agent. A skip is not a failure and not a pass.
//
// The section's own call in Check() is pinned by TestEverySectionIsWired; these run it over the
// selection sectionPacks resolved, as Check() does.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func runPackAudiences(t *testing.T, addressed string) (*reporter, string) {
	t.Helper()
	agent := programPackDir(t, "alphacli",
		`{"kind":"program","bin":"alphacli","via":"npm","package":"alphacli"},`+
			`{"kind":"briefing","into":".alpha/AGENTS.md","agent":"alphacli"}`)
	house := programPackDir(t, "house",
		`{"kind":"files","from":"agents","agents":[`+addressed+`]}`)
	packsFixture(t, `{"packs": ["file://`+agent+`", "file://`+house+`"]}`)

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	o := &Options{}
	o.sectionPacks(r, jsonx.NewOrderedMap())
	if r.failed != 0 {
		t.Fatalf("the Packs section failed before the audience section ran:\n%s", buf.String())
	}
	before := r.skipped
	o.sectionPackAudiences(r)
	r.skipped -= before
	return r, buf.String()
}

func TestCheckSkipsAnAudienceNoSelectedPackProvides(t *testing.T) {
	r, out := runPackAudiences(t, `"codex","agyy"`)
	if r.failed != 0 {
		t.Fatalf("a skipped audience must never fail check (FS-D6):\n%s", out)
	}
	if r.skipped != 2 {
		t.Fatalf("want 2 [SKIP] rows (codex and agyy), got %d:\n%s", r.skipped, out)
	}
	for _, want := range []string{
		"[SKIP] pack house: files for codex not delivered — no selected pack provides codex",
		"-> If you want it here:  yolo pack add codex",
		"[SKIP] pack house: files for agyy not delivered — no selected pack provides agyy",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
	// A misspelling reads as written, and no shipped pack provides it, so it gets no next step.
	if strings.Contains(out, "yolo pack add agyy") {
		t.Errorf("a name no shipped pack provides must not be offered as a pack to add:\n%s", out)
	}
}

func TestCheckPassesAnAudienceTheSelectionHas(t *testing.T) {
	r, out := runPackAudiences(t, `"alphacli"`)
	if r.skipped != 0 || r.failed != 0 {
		t.Fatalf("an addressed agent the selection provides must not skip or fail:\n%s", out)
	}
	if !strings.Contains(out, "Pack audiences") {
		t.Errorf("a selection with addressed content must say the section ran:\n%s", out)
	}
}

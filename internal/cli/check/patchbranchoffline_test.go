package check

// patchbranchoffline_test.go pins that `yolo check`, which reaches no network, says when a selected pack's
// patch branch has never been resolved on this machine — no check record holds its commit — instead of a
// bare pass, naming `yolo pack lint --online`; and says nothing of it once a record does.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func TestCheckSaysWhenAPatchBranchsRefWasNeverResolved(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "acme")
	const source = "git+https://github.com/upstream/pi-subagents?ref=main"
	const branch = "git+https://github.com/you/pi-subagents?ref=mine"
	manifest := `{"name": "acme", "contributes": [{"kind": "files", "into": ".pi/agent/yolo-patched/pi-subagents",
	  "source": "` + source + `", "patch_branch": "` + branch + `", "build": "npm install"}]}`
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	packsFixture(t, `{"packs": ["file://`+dir+`"]}`)
	run := func() (string, *reporter) {
		var buf bytes.Buffer
		r := &reporter{w: &buf}
		(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r, jsonx.NewOrderedMap())
		return buf.String(), r
	}

	report, r := run()
	want := "extension acme/pi-subagents: ref not resolved (offline) — `yolo pack lint --online " + shquote.Quote(dir) + "` checks it"
	if r.failed != 0 || !strings.Contains(report, want) {
		t.Fatalf("check of a never-resolved patch branch: failed=%d, want %q\n%s", r.failed, want, report)
	}

	// A RECORD THAT RESOLVED IT, as a launch's check leaves one: nothing to say.
	rec := map[string]any{"schema": packsrc.CheckRecordSchema, "owner": "acme/pi-subagents",
		"patch_branch": map[string]any{"source": branch, "upstream": packsrc.CheckRepo(source),
			"commit": strings.Repeat("a", 40), "base": strings.Repeat("b", 40)}}
	data, _ := json.Marshal(rec)
	p := (&packsrc.Store{Dir: paths.PacksDir()}).CheckRecordPath("acme/pi-subagents")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if report, _ := run(); strings.Contains(report, "not resolved (offline)") {
		t.Errorf("check of a patch branch a record resolved still says it is unresolved:\n%s", report)
	}

	// A RELATIVE patch branch read where it does not resolve (in a jail, the delivered copy of a host pack) names
	// the lint of the pack's own directory, never one of the copy that would refuse.
	relative := strings.Replace(manifest, branch, "../..?ref=mine", 1)
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(relative), 0o644); err != nil {
		t.Fatal(err)
	}
	report, r = run()
	if r.failed != 0 || !strings.Contains(report, "ref not resolved (offline) — run `yolo pack lint --online <dir>` on the pack's own directory") {
		t.Errorf("check of a relative patch branch it cannot resolve: failed=%d\n%s", r.failed, report)
	}
}

package cli

// applyhostuserfiles_test.go pins OQ-NC8 (docs/plans/notch-convergence.md, ruled 2026-09-28 by
// parity, A) through the real `yolo host apply`: a SOURCE-LESS host_files entry renders into the
// real home through the surface engine the jail uses, under `host_management`; removing the
// entry has the next apply remove what the first one wrote, on the provenance record it kept;
// and a source-bearing entry and `mise_tools` are NAMED as inert, never silently skipped. Each
// test drives hostMain, so deleting the render, the retirement or the notch line from
// applyHostSurveyed fails it.
//
// Every config that renders declares `host_management: "own"`, the one contract that writes
// since the `assert` retirement (OQ-CO14): the unset key is `none`, and `yolo host apply`
// refuses under it before reading a single entry.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const userFileRel = ".config/mytool/config.json"

// hostApplyRun runs `yolo host apply` (assert when write) and returns the rc and the report.
func hostApplyRun(t *testing.T, write bool) (int, string) {
	t.Helper()
	args := []string{"apply"}
	if write {
		args = append(args, "--assert")
	}
	var out, errw bytes.Buffer
	rc := hostMain(args, &out, &errw, false, strings.NewReader("y\n"))
	return rc, out.String() + errw.String()
}

// userFileRecord is the provenance record host apply keeps for the entry above.
func userFileRecord(home string) string {
	return filepath.Join(paths.GlobalStorageUnder(home), "host-provenance",
		"user-.config_2fmytool_2fconfig.json.provenance")
}

func TestHostApplyWritesASourceLessHostFileAndRemovesItWhenTheEntryGoes(t *testing.T) {
	// Under `own`, the one contract that writes (it was also run under the retired `assert`,
	// then the default), and with packs and without: host_files is a config key, not a pack
	// kind, so an empty `packs` renders it too.
	for _, tc := range []struct{ packs, contract string }{
		{`["pi"]`, `"host_management":"own",`}, {`[]`, `"host_management":"own",`},
	} {
		packs := tc.packs
		t.Run("packs "+packs+" "+tc.contract, func(t *testing.T) {
			home := hostComputedHome(t, `{"packs":`+packs+`,`+tc.contract+`
				"host_files":[{"path":"~/`+userFileRel+`",
				               "content":"{\"theme\": \"dark\"}",
				               "managed":{"telemetry": false}}]}`)
			// The user's own key, which the entry never declares: it must survive both the
			// write and the removal.
			writeFile(t, filepath.Join(home, userFileRel), `{"mine": 1}`)

			if rc, report := hostApplyRun(t, false); rc != 0 ||
				!strings.Contains(report, "user/") {
				t.Fatalf("dry run rc=%d, and the entry has no row:\n%s", rc, report)
			}
			if got := readJSONAt(t, home, userFileRel); got["theme"] != nil {
				t.Fatalf("the dry run wrote the file: %v", got)
			}

			rc, report := hostApplyRun(t, true)
			if rc != 0 {
				t.Fatalf("assert rc=%d\n%s", rc, report)
			}
			got := readJSONAt(t, home, userFileRel)
			if got["theme"] != "dark" || got["telemetry"] != false || got["mine"] != float64(1) {
				t.Fatalf("the source-less entry did not render into the real home "+
					"(want theme, telemetry and your own key): %v\n%s", got, report)
			}
			if _, err := os.Stat(userFileRecord(home)); err != nil {
				t.Fatalf("no provenance record for the entry, so no later apply can remove "+
					"what this one wrote: %v", err)
			}

			// The entry goes.
			writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
				`{`+tc.contract+`"packs":`+packs+`}`)
			if rc, report := hostApplyRun(t, false); rc != 0 ||
				!strings.Contains(report, "would remove") {
				t.Fatalf("the dry run after the drop does not say what it would remove "+
					"(rc=%d):\n%s", rc, report)
			}
			if got := readJSONAt(t, home, userFileRel); got["theme"] != "dark" {
				t.Fatalf("the dry run removed a key: %v", got)
			}
			rc, report = hostApplyRun(t, true)
			if rc != 0 {
				t.Fatalf("assert after the drop rc=%d\n%s", rc, report)
			}
			got = readJSONAt(t, home, userFileRel)
			if _, ok := got["theme"]; ok {
				t.Errorf("the dropped entry's content is still in the file: %v\n%s", got, report)
			}
			if _, ok := got["telemetry"]; ok {
				t.Errorf("the dropped entry's managed key is still in the file: %v\n%s", got, report)
			}
			if got["mine"] != float64(1) {
				t.Errorf("the removal took your own key: %v", got)
			}
			if !strings.Contains(report, "telemetry") || !strings.Contains(report, "removed") {
				t.Errorf("the removal is not named key by key:\n%s", report)
			}
			if _, err := os.Stat(userFileRecord(home)); !os.IsNotExist(err) {
				t.Errorf("the dropped entry's record survived its removal (err=%v)", err)
			}
		})
	}
}

func TestHostApplyNamesSourceBearingHostFilesAndMiseToolsInert(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["pi"],"host_management":"own",
		"mise_tools":{"node":"22"},
		"host_files":[{"path":"~/.config/other/token.json","source":"~/secret.json"}]}`)
	writeFile(t, filepath.Join(home, "secret.json"), `{"k": 1}`)
	for _, write := range []bool{false, true} {
		rc, report := hostApplyRun(t, write)
		if rc != 0 {
			t.Fatalf("assert=%v rc=%d\n%s", write, rc, report)
		}
		for _, want := range []string{"host_files with a source", "mise_tools"} {
			if !strings.Contains(report, want) {
				t.Errorf("assert=%v: %q is not named inert at the host:\n%s", write, want, report)
			}
		}
		if !strings.Contains(report, "~/.config/other/token.json") {
			t.Errorf("assert=%v: the inert entry is not named by its destination:\n%s", write, report)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "other", "token.json")); !os.IsNotExist(err) {
		t.Errorf("a source-bearing entry wrote into the real home (err=%v)", err)
	}
}

// `yolo config render --at host` previews the entry host apply writes (item 23's byte-for-byte
// rule, extended to the user's surfaces): the preview holds the entry's content and managed key.
// Under `own`, since under the unset key (`none`) host apply writes nothing to preview.
func TestConfigRenderAtHostPreviewsASourceLessHostFile(t *testing.T) {
	hostComputedHome(t, `{"packs":[],"host_management":"own",
		"host_files":[{"path":"~/`+userFileRel+`","content":"{\"theme\": \"dark\"}",
		               "managed":{"telemetry": false}}]}`)
	rc, out, errs := runConfigVerb(t, "render", "user", "--at", "host")
	if rc != 0 || !strings.Contains(out, `"theme": "dark"`) || !strings.Contains(out, `"telemetry": false`) {
		t.Fatalf("config render user --at host rc=%d does not preview the entry:\n%s%s", rc, out, errs)
	}
}

// `yolo host apply --revert` withdraws a host_files entry's keys by the same record and walk a
// pack surface's go by, even while the entry is still declared: a revert is the user taking yolo
// out of their files.
//
// The apply runs under `own` and the revert under `none`, the order the ruling gives (OQ-CO14):
// a revert is refused under `own`, whose next apply would compose the keys straight back, and
// runs under `none` on the record the owned apply left.
func TestHostApplyRevertWithdrawsAHostFile(t *testing.T) {
	const entry = `"host_files":[{"path":"~/` + userFileRel + `","managed":{"telemetry": false}}]`
	home := hostComputedHome(t, `{"packs":[],"host_management":"own",`+entry+`}`)
	writeFile(t, filepath.Join(home, userFileRel), `{"mine": 1}`)
	if rc, report := hostApplyRun(t, true); rc != 0 {
		t.Fatalf("assert rc=%d\n%s", rc, report)
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[],"host_management":"none",`+entry+`}`)
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--revert", "--assert"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("revert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	got := readJSONAt(t, home, userFileRel)
	if _, ok := got["telemetry"]; ok || got["mine"] != float64(1) {
		t.Errorf("the revert did not withdraw exactly yolo's key: %v\n%s", got, out.String())
	}
	if _, err := os.Stat(userFileRecord(home)); !os.IsNotExist(err) {
		t.Errorf("the revert left the entry's record (err=%v)", err)
	}
}

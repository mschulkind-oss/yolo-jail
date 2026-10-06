package cli

// hostapplyjsonrefused_test.go pins `yolo host apply --format json` where the host_management
// gate refuses (hostmanagementgate.go): under "none", set or unset, and under the retired
// "assert". The dry run is a state report ([OQ-RO4]), and since the `assert` retirement (OQ-CO14)
// a fresh home is one where the gate refuses, so a consumer asking for the document must still
// get one — outcome `refused`, the stage that refused it named — rather than an empty stdout and
// prose it cannot parse.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// hostManagementRefusedFixture is a scratch home whose user config says mode for
// host_management, or nothing at all when mode is "".
func hostManagementRefusedFixture(t *testing.T, mode string) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(t.TempDir())
	if mode != "" {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
			`{"host_management": "`+mode+`"}`)
	}
	return home
}

// THE DOCUMENT AT BOTH SPELLINGS, for every value under which the gate refuses: exit 1, the
// prose refusal on stderr, and on stdout exactly one document with outcome `refused`,
// failed_stages ["host_management"], every list `[]` and every count zero. Through hostMain and
// applyMain, so deleting the emission at either call site fails here.
func TestHostApplyDryRunEmitsARefusedDocumentWhenHostManagementRefuses(t *testing.T) {
	type spelling struct {
		name string
		run  func(out, errw *bytes.Buffer) int
	}
	spellings := []spelling{
		{"host apply", func(out, errw *bytes.Buffer) int {
			return hostMain([]string{"apply", "--format", "json"}, out, errw, false, nil)
		}},
		{"apply --at host", func(out, errw *bytes.Buffer) int {
			return applyMain([]string{"--at", "host", "--format", "json"}, out, errw, false, nil)
		}},
	}
	lists := []string{"inapplicable_kinds", "at_launch_kinds", "failed_packs", "failed_stages",
		"unresolved_packs", "destinations", "groups", "host_floor"}
	for _, mode := range []string{"", "none", "assert"} {
		for _, sp := range spellings {
			t.Run("host_management="+mode+"/"+sp.name, func(t *testing.T) {
				home := hostManagementRefusedFixture(t, mode)
				var out, errw bytes.Buffer
				if rc := sp.run(&out, &errw); rc != 1 {
					t.Errorf("rc=%d, want 1: the configuration declined it\nstdout:\n%s\nstderr:\n%s",
						rc, out.String(), errw.String())
				}
				stderr := errw.String()
				if !strings.Contains(stderr, "yolo host apply: ") || !strings.Contains(stderr, "host_management") {
					t.Errorf("the prose refusal is not on stderr:\n%s", stderr)
				}
				if mode == "assert" && !strings.Contains(stderr, `"assert" is RETIRED`) {
					t.Errorf("the retired value's refusal is not its own:\n%s", stderr)
				}
				if strings.Contains(out.String(), "\x1b[") {
					t.Errorf("the document carries ANSI escapes:\n%s", out.String())
				}

				// PARSED, the whole stream: a stray human line before or after fails here.
				var raw map[string]any
				if err := json.Unmarshal(out.Bytes(), &raw); err != nil {
					t.Fatalf("stdout is not one JSON document (%v):\n%s", err, out.String())
				}
				var doc hostApplyDoc
				if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
					t.Fatal(err)
				}
				if doc.Posture != "dry-run" || doc.Home != home || doc.Outcome != "refused" {
					t.Errorf("posture %q, home %q, outcome %q; want dry-run, %s, refused",
						doc.Posture, doc.Home, doc.Outcome, home)
				}
				if len(doc.FailedStages) != 1 || doc.FailedStages[0] != "host_management" {
					t.Errorf("failed_stages = %v, want [host_management]", doc.FailedStages)
				}
				if !strings.HasPrefix(doc.Verdict, "An --assert would REFUSE: ") ||
					!strings.HasSuffix(doc.Verdict, "so nothing would be written.") ||
					!strings.Contains(doc.Verdict, "host_management") || !strings.Contains(doc.Verdict, `"own"`) {
					t.Errorf("the verdict is not the refused verdict naming host_management and \"own\": %q",
						doc.Verdict)
				}
				for _, key := range lists {
					v, present := raw[key]
					list, isList := v.([]any)
					if !present || !isList {
						t.Errorf("%s = %#v, want a list ([] never null)", key, v)
						continue
					}
					if key != "failed_stages" && len(list) != 0 {
						t.Errorf("%s = %v, want [] — nothing was surveyed", key, list)
					}
				}
				counts, _ := raw["counts"].(map[string]any)
				if len(counts) == 0 {
					t.Errorf("counts = %#v, want every count present", raw["counts"])
				}
				for k, v := range counts {
					if n, _ := v.(float64); n != 0 {
						t.Errorf("counts.%s = %v, want 0 — nothing was surveyed", k, v)
					}
				}
			})
		}
	}
}

// THE ACTING POSTURE STILL REFUSES THE FLAG, at both spellings and before the host_management
// gate: an --assert has no document ([OQ-RO4]), so exit 2 with an empty stdout, never a refused
// document for an act.
func TestHostApplyAssertWithJSONStillRefusesTheFlagWhereHostManagementRefuses(t *testing.T) {
	for _, mode := range []string{"", "none", "assert"} {
		for _, argv := range [][]string{
			{"host", "apply", "--assert", "--format", "json"},
			{"apply", "--at", "host", "--assert", "--format", "json"},
		} {
			hostManagementRefusedFixture(t, mode)
			var out, errw bytes.Buffer
			var rc int
			if argv[0] == "host" {
				rc = hostMain(argv[1:], &out, &errw, false, nil)
			} else {
				rc = applyMain(argv[1:], &out, &errw, false, nil)
			}
			if rc != 2 || out.Len() != 0 || !strings.Contains(errw.String(), "--format json") {
				t.Errorf("host_management %q, %v: rc=%d stdout %q, want 2, empty stdout and the flag named:\n%s",
					mode, argv, rc, out.String(), errw.String())
			}
		}
	}
}

// promoteClause is the clause of msg that names `yolo config promote` — from the boundary before
// it (an opening parenthesis, a semicolon, a sentence end or a line break) to the one after —
// and the text of msg before that clause.
func promoteClause(t *testing.T, msg string) (before, clause string) {
	t.Helper()
	p := strings.Index(msg, "yolo config promote")
	if p < 0 {
		t.Fatalf("the message does not name `yolo config promote`:\n%s", msg)
	}
	start := strings.LastIndexAny(msg[:p], "(;\n")
	if s := strings.LastIndex(msg[:p], ". "); s > start {
		start = s
	}
	end := len(msg)
	if e := strings.IndexAny(msg[p:], ")\n"); e >= 0 {
		end = p + e
	}
	if e := strings.Index(msg[p:], ". "); e >= 0 && p+e < end {
		end = p + e
	}
	return msg[:start+1], msg[start+1 : end]
}

// THE ORDER THE REFUSAL NAMES (FIX 6 of the retirement's review): `yolo config promote` lifts a
// CAPTURED key into the local pack, and only an owned apply captures one — under "none" there is
// no capture store at the host, and promote answers "Nothing to promote". So the refusal names
// "own" before promote and never tells the user to promote "first".
func TestTheNoneRefusalNamesOwnBeforePromote(t *testing.T) {
	hostManagementRefusedFixture(t, "")
	for _, declared := range []bool{false, true} {
		msg := hostManagementRefusal(config.HostManagementNone, declared)
		before, clause := promoteClause(t, msg)
		if !strings.Contains(before, `"own"`) {
			t.Errorf("declared=%v: \"own\" is not named before `yolo config promote`:\n%s", declared, msg)
		}
		if strings.Contains(clause, "first") {
			t.Errorf("declared=%v: the refusal still says to promote first, which finds nothing under "+
				"\"none\": %q", declared, clause)
		}
	}
}

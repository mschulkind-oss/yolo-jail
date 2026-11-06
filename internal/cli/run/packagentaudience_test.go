package run

// packagentaudience_test.go pins the launch's FS-D6 skip (docs/design/pack-files-for-agent-state.md#FS-D6):
// an `agents` selector naming an agent this jail does not have delivers nothing, and says so.
//
// Through stagePacks, not packload.AgentAudienceSkips — that function has its own unit tests,
// and a test of it alone would stay green with the disclosure deleted.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// audienceLaunch configures one agent pack (`alphacli`) plus a content pack addressing
// `addressed`, and returns Options ready for stagePacks.
func audienceLaunch(t *testing.T, addressed string) *Options {
	t.Helper()
	home := packHome(t)
	base := t.TempDir()
	agent := filepath.Join(base, "alphacli")
	if err := os.MkdirAll(agent, 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, agent, `{"name":"alphacli","contributes":[`+
		`{"kind":"program","bin":"alphacli","via":"npm","package":"alphacli"},`+
		`{"kind":"briefing","into":".alpha/AGENTS.md","agent":"alphacli"}]}`)

	house := filepath.Join(base, "house")
	if err := os.MkdirAll(filepath.Join(house, "prose"), 0o755); err != nil {
		t.Fatal(err)
	}
	writePack(t, house, `{"name":"house","contributes":[`+
		`{"kind":"briefing","from":"prose/x.md","agents":["`+addressed+`"]}]}`)
	if err := os.WriteFile(filepath.Join(house, "prose", "x.md"),
		[]byte("Addressed prose.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	writeUserPacks(t, home,
		`[{"source":"file://`+agent+`","name":"alphacli"},`+
			`{"source":"file://`+house+`","name":"house"}]`)
	return &Options{Workspace: t.TempDir(), Stdout: &strings.Builder{}}
}

// THE CALL-SITE TEST, and FS-D6's headline (docs/design/pack-files-for-agent-state.md#FS-D6): a
// misspelling and a real agent nobody selected are SKIPPED identically, the launch runs, and the
// skip is disclosed on stderr naming the pack, the kind and the agent as written. Only a name a
// shipped pack provides earns the `yolo pack add` next step.
func TestLaunchSkipsAnAudienceThisJailDoesNotHave(t *testing.T) {
	for _, tc := range []struct{ addressed, next string }{
		{"alphaclu", ""},
		{"codex", "yolo pack add codex"},
	} {
		t.Run(tc.addressed, func(t *testing.T) {
			o := audienceLaunch(t, tc.addressed)
			stderr := &strings.Builder{}
			o.Stderr = stderr
			_, _, proses, err := o.stagePacks("yolo-test-audience")
			if err != nil {
				t.Fatalf("stagePacks refused `agents: [%q]`; FS-D6 rules it skipped, and the "+
					"launch runs: %v", tc.addressed, err)
			}
			want := "pack house: briefing for " + tc.addressed + " not delivered — no selected " +
				"pack provides " + tc.addressed
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("the launch did not disclose the skip %q; stderr:\n%s", want, stderr)
			}
			if tc.next != "" && !strings.Contains(stderr.String(), tc.next) {
				t.Errorf("the skip of a shipped agent must name %q; stderr:\n%s", tc.next, stderr)
			}
			if tc.next == "" && strings.Contains(stderr.String(), "yolo pack add") {
				t.Errorf("a name no shipped pack provides must carry no next step; stderr:\n%s", stderr)
			}
			// ONE VOICE: the R1 "no destination declares a matching `agent`" warning is for a
			// name the selection HAS, and must not repeat a skip with the wrong remedy.
			if strings.Contains(stderr.String(), "declares a matching `agent`") {
				t.Errorf("a skipped name was also reported as an unmatched audience; stderr:\n%s", stderr)
			}
			for _, p := range proses {
				if strings.Contains(p.Text, "Addressed prose.") && len(p.Agents) == 0 {
					t.Errorf("prose addressed to %q lost its audience: %+v", tc.addressed, p)
				}
			}
		})
	}
}

// A BUILD JAIL'S NARROWED SELECTION (selectionNarrowed, PPX-D39) still discloses the skip, but
// names no `yolo pack add`: the narrowing dropped the agent, not the user's `packs`, so the step
// would be wrong advice there.
func TestANarrowedLaunchSkipsWithoutAPackAddStep(t *testing.T) {
	o := audienceLaunch(t, "codex")
	stderr := &strings.Builder{}
	o.Stderr = stderr
	o.OnlyPacks = []string{"house"}
	if _, _, _, err := o.stagePacks("yolo-test-audience-narrowed"); err != nil {
		t.Fatalf("a narrowed selection refused a skipped audience: %v", err)
	}
	if !strings.Contains(stderr.String(), "pack house: briefing for codex not delivered") {
		t.Errorf("the narrowed launch did not disclose the skip; stderr:\n%s", stderr)
	}
	if strings.Contains(stderr.String(), "yolo pack add") {
		t.Errorf("a narrowed launch offered `yolo pack add`; stderr:\n%s", stderr)
	}
}

// The HAPPY PATH: an enabled audience launches, and the addressed prose is collected.
func TestLaunchAcceptsAnAudienceThisJailHas(t *testing.T) {
	o := audienceLaunch(t, "alphacli")
	_, _, proses, err := o.stagePacks("yolo-test-audience-ok")
	if err != nil {
		t.Fatalf("addressing an enabled agent must launch; got: %v", err)
	}
	var found bool
	for _, p := range proses {
		if strings.Contains(p.Text, "Addressed prose.") && strings.Join(p.Agents, ",") == "alphacli" {
			found = true
		}
	}
	if !found {
		t.Errorf("the addressed prose did not reach the composition input carrying its "+
			"audience; got %+v", proses)
	}
}

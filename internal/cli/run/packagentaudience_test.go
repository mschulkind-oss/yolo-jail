package run

// packagentaudience_test.go is the EIGHTH launch pre-flight (briefing-audiences.md P3,
// OQ-BA3): an `agents` selector naming an agent this jail does not have.
//
// Through stagePacks, not packload.AgentAudienceProblems — that function has its own unit
// tests, and a test of it alone would stay green with the pre-flight deleted.

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

// THE CALL-SITE TEST, and P3's headline: a typo and a real agent nobody selected are refused
// identically, because from the jail's point of view they are the same mistake.
func TestLaunchRefusesAnAudienceThisJailDoesNotHave(t *testing.T) {
	for _, addressed := range []string{"alphaclu", "codex"} {
		t.Run(addressed, func(t *testing.T) {
			o := audienceLaunch(t, addressed)
			_, _, _, err := o.stagePacks("yolo-test-audience")
			if err == nil {
				t.Fatalf("stagePacks accepted `agents: [%q]` in a jail that has no such agent — "+
					"prose addressed to nobody is worse than prose addressed to everybody, "+
					"because the author believes it was delivered", addressed)
			}
			msg := err.Error()
			for _, want := range []string{
				addressed,
				"pack house",
				"Agents your `packs` provide: alphacli",
				"Fix the name, or add the pack",
			} {
				if !strings.Contains(msg, want) {
					t.Errorf("refusal missing %q; got:\n%s", want, msg)
				}
			}
		})
	}
	// And the near miss carries a did-you-mean, which is the half of R3 a candidate list alone
	// does not provide.
	o := audienceLaunch(t, "alphaclu")
	_, _, _, err := o.stagePacks("yolo-test-audience-guess")
	if err == nil || !strings.Contains(err.Error(), `did you mean "alphacli"`) {
		t.Errorf("a one-character typo must earn a did-you-mean; got: %v", err)
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

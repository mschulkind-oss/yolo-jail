package cli

// hostapplygaterelated_test.go pins [OQ-HS17] (host-apply-staleness.md): a failure stops a
// wrapped launch only when it is in the launched program's own configuration.
//
// Measured on the maintainer's host 2026-09-28: `yolo host -p bedrock -- claude` refused —
// "the host apply did not complete (rc=1) — claude was not launched" — because pi's
// settings.json could not be written. claude reads no pi file.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// relatedGateFixture is brokenLinkHome with the launch gate on and no terminal.
func relatedGateFixture(t *testing.T) string {
	t.Helper()
	home, _ := brokenLinkHome(t, `,"host_apply_on_launch":true`)
	setGateTTY(t, false)
	return home
}

// THE MAINTAINER'S LAUNCH: an unrelated broken link is reported with its fix, the rest of the
// home is synchronized, and claude launches — on a home never applied (the gate auto-applies)
// and on one already applied (nothing else to change).
func TestAnUnrelatedPacksFailureDoesNotStopTheLaunch(t *testing.T) {
	home := relatedGateFixture(t)
	for _, phase := range []string{"stale home", "settled home"} {
		var errw bytes.Buffer
		if !hostApplyGate(&errw, nil, "claude") {
			t.Fatalf("%s: claude was refused over pi's broken link:\n%s", phase, errw.String())
		}
		for _, want := range []string{
			"~/.pi/agent/settings.json is a symlink to ~/.dotfiles/pi/settings.json",
			"rm ~/.pi/agent/settings.json",
			"claude reads none of it — launching claude.",
		} {
			if !strings.Contains(errw.String(), want) {
				t.Errorf("%s: the launch did not say %q:\n%s", phase, want, errw.String())
			}
		}
		if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); err != nil {
			t.Errorf("%s: claude's own configuration was not applied: %v", phase, err)
		}
	}
}

// THE CONTROL: the same failure in the launched program's own configuration refuses it.
func TestAFailureInTheLaunchedProgramsOwnConfigRefusesIt(t *testing.T) {
	relatedGateFixture(t)
	var errw bytes.Buffer
	if hostApplyGate(&errw, nil, "pi") {
		t.Fatalf("pi launched although its own settings.json cannot be written:\n%s", errw.String())
	}
	if !strings.Contains(errw.String(), "refusing to launch pi") ||
		!strings.Contains(errw.String(), "~/.pi/agent/settings.json is a symlink") {
		t.Errorf("the refusal does not name the failure:\n%s", errw.String())
	}
}

// A RENDER ERROR follows the same rule, and a failure the gate cannot attribute to a pack
// still refuses: it is not provably unrelated.
func TestARenderErrorIsJudgedByItsPackAndAnUnattributedOneRefuses(t *testing.T) {
	cases := []struct {
		name       string
		fail       func(s *hostApplySurvey)
		bin        string
		wantLaunch bool
	}{
		{"pi failed, launching claude", func(s *hostApplySurvey) { s.noteRenderFailure("pi", "boom") },
			"claude", true},
		{"pi failed, launching pi", func(s *hostApplySurvey) { s.noteRenderFailure("pi", "boom") },
			"pi", false},
		{"claude failed, launching claude", func(s *hostApplySurvey) {
			s.noteRenderFailure("claude", "boom")
		}, "claude", false},
		{"a failed stage, launching claude", func(s *hostApplySurvey) { s.noteStageFailure(stageSkills) },
			"claude", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := relatedGateFixture(t)
			// Remove the broken link: this case is about the injected failure alone.
			if err := os.Remove(filepath.Join(home, ".pi", "agent", "settings.json")); err != nil {
				t.Fatal(err)
			}
			prev := hostApplyGateWrite
			hostApplyGateWrite = func(out, errw io.Writer, color, write bool, stdin io.Reader,
				s *hostApplySurvey) int {
				applyHostSurveyed(out, errw, color, write, stdin, s)
				c.fail(s)
				return 1
			}
			t.Cleanup(func() { hostApplyGateWrite = prev })
			var errw bytes.Buffer
			if got := hostApplyGate(&errw, nil, c.bin); got != c.wantLaunch {
				t.Errorf("launch=%v, want %v:\n%s", got, c.wantLaunch, errw.String())
			}
			if c.wantLaunch && !strings.Contains(errw.String(), "pi failed to render: boom") {
				t.Errorf("the unrelated failure was not reported:\n%s", errw.String())
			}
		})
	}
}

// WHICH PACKS ARE RELATED, stated as the rule: the installer, the packs declaring its agent's
// files, the packs configuring its surfaces, and their needs — and nothing for a program no pack
// installs.
func TestLaunchRelatedPacksIsTheProgramsOwnConfiguration(t *testing.T) {
	relatedGateFixture(t)
	var out, errw bytes.Buffer
	s := &hostApplySurvey{}
	applyHostSurveyed(&out, &errw, false, false, nil, s)
	rel := launchRelatedPacks(s.loaded, "claude")
	if !rel["claude"] {
		t.Errorf("the installing pack is not related: %v", rel)
	}
	if rel["pi"] {
		t.Errorf("pi is related to a claude launch: %v", rel)
	}
	if got := launchRelatedPacks(s.loaded, "bash"); len(got) != 0 {
		t.Errorf("a program no pack installs has related packs: %v", got)
	}
}

// THE INTERACTIVE FIRST APPLY takes the same rule: on a TTY, a first apply that would replace a
// hand-added MCP server asks, the user says yes, and an unrelated broken link does not then stop
// claude.
func TestTheInteractiveFirstApplyLetsAnUnrelatedFailureThrough(t *testing.T) {
	home := t.TempDir()
	packDir := filepath.Join(home, "packs", "matt-mcp")
	writeFile(t, filepath.Join(packDir, "pack.json"), mcpContributorPackJSON)
	brokenLinkHomeAt(t, home, `"claude","pi",{"source":"file://`+packDir+`","name":"matt-mcp"}`,
		`,"host_apply_on_launch":true`)
	setGateTTY(t, true)
	writeFile(t, filepath.Join(home, ".claude.json"),
		`{"mcpServers":{"tavily":{"type":"http","url":"https://x?k=SECRET"}}}`)

	var out, e bytes.Buffer
	probe := &hostApplySurvey{}
	applyHostSurveyed(&out, &e, false, false, nil, probe)
	if !surveyNeedsPrompt(probe) {
		t.Fatalf("fixture premise: this home must reach the interactive first-apply path:\n%s", out.String())
	}

	var errw bytes.Buffer
	if !hostApplyGate(&errw, strings.NewReader("y\n"), "claude") {
		t.Fatalf("claude was refused over pi's broken link after the user answered y:\n%s",
			errw.String())
	}
	if !strings.Contains(errw.String(), "~/.pi/agent/settings.json is a symlink") ||
		!strings.Contains(errw.String(), "launching claude") {
		t.Errorf("the launch did not name the unrelated failure and go ahead:\n%s", errw.String())
	}
}

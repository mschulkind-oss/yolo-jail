package jailcontent

import (
	"strings"
	"testing"
)

// TestTheAttachSkewSectionIsAnAcknowledgedAttachsAlone pins SK-D15's section: absent from every
// briefing that carries no skew, and, for an attach that went ahead under the acknowledgment,
// present near the top (after the confinement header, before the environment) and naming the
// version the jail was launched with, each contract tag it lacks with what that withholds, every
// other difference, the withheld names and the host's remedy.
func TestTheAttachSkewSectionIsAnAcknowledgedAttachsAlone(t *testing.T) {
	plain := BriefingContent(BriefingInput{Workspace: "/ws"})
	if strings.Contains(plain, "could not take what started it") {
		t.Fatalf("a briefing with no skew carries the section:\n%s", plain)
	}
	skewed := BriefingContent(BriefingInput{Workspace: "/ws", AttachSkew: &AttachSkew{
		JailVersion:     "0.10.0",
		LauncherVersion: "0.11.0+5.gabc",
		Jail:            "was launched by an older yolo and cannot receive what this entry delivers",
		MissingTags: []AttachSkewTag{{Tag: "agent-env-files",
			Lacks: "its launchers read no per-agent env file, so a value scoped to one agent cannot reach that agent"}},
		Differences: []string{"Added to your config since it launched: zai."},
		Withheld:    []string{"claude (profile zai): ANTHROPIC_AUTH_TOKEN, ZAI_API_KEY"},
		Remedy:      "`yolo stop` from this workspace on the host",
	}})
	head := strings.Index(skewed, "## ⚠ This session runs in a jail that could not take what started it")
	env := strings.Index(skewed, "## Environment")
	if head < 0 || env < 0 || head > env {
		t.Fatalf("the section is missing or below the environment (at %d, environment at %d):\n%s", head, env, skewed)
	}
	section := skewed[head:env]
	for _, want := range []string{
		"this jail was launched by an older yolo and cannot receive what this entry delivers",
		"`YOLO_ALLOW_ATTACH_SKEW` was set",
		"**The jail was launched with**: yolo 0.10.0 (the attach was yolo 0.11.0+5.gabc).",
		"**It lacks the `agent-env-files` contract**: its launchers read no per-agent env file",
		"- Added to your config since it launched: zai.",
		"  - claude (profile zai): ANTHROPIC_AUTH_TOKEN, ZAI_API_KEY",
		"`yolo stop` from this workspace on the host, then a new launch",
		"tell the user",
	} {
		if !strings.Contains(section, want) {
			t.Errorf("the section must say %q:\n%s", want, section)
		}
	}
	unknown := BriefingContent(BriefingInput{Workspace: "/ws", AttachSkew: &AttachSkew{Jail: "x"}})
	if !strings.Contains(unknown, "an older yolo, whose version this jail does not record") ||
		!strings.Contains(unknown, "a restart on the host") {
		t.Errorf("a jail with no recorded version, and no remedy, must still be described:\n%s", unknown)
	}
}

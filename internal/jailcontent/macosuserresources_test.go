package jailcontent

import (
	"strings"
	"testing"
)

func TestBriefingPointsToRenderedMechanicsInsteadOfLifecycleRules(t *testing.T) {
	for _, in := range []BriefingInput{
		{Workspace: "/Users/Shared/yolo/proj", Mechanism: "macos-user"},
		{Workspace: "/workspace", Mechanism: "podman"},
	} {
		briefing := reachable(in)
		if !strings.Contains(briefing, "Read the **jail-mechanics** skill") {
			t.Errorf("briefing lacks mechanics pointer: %s", briefing)
		}
		for _, removed := range []string{"## Packages", "container-limit change", "restart the jail"} {
			if strings.Contains(briefing, removed) {
				t.Errorf("briefing still contains lifecycle guidance %q", removed)
			}
		}
	}
}

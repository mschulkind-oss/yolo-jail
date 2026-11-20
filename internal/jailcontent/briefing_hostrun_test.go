package jailcontent

import (
	"regexp"
	"strings"
	"testing"
)

func TestTheJailBriefingFrontLoadsHostOnlyWork(t *testing.T) {
	for name, in := range referenceFactSheets() {
		t.Run(name, func(t *testing.T) {
			core := BriefingContent(in)
			var hostOnly string
			for _, line := range strings.Split(core, "\n") {
				if strings.HasPrefix(line, "- **Host-only work**:") {
					hostOnly = line
					break
				}
			}
			if hostOnly == "" {
				t.Fatal("core has no dedicated Host-only work line")
			}
			for _, want := range []string{
				"Need host environment/capability? Request `yolo host-run`",
				"no confinement bypass or human handoff",
				"Relay full refusal menu; one command per code block",
				"Background `yolo wait <id>`; retry once on exit 0",
				"Human runs `yolo approve`, `yolo config apply`, `yolo allow`",
			} {
				if !strings.Contains(hostOnly, want) {
					t.Errorf("Host-only work line lacks %q: %s", want, hostOnly)
				}
			}
			if strings.Index(hostOnly, "yolo host-run") > strings.Index(hostOnly, "yolo approve") {
				t.Errorf("host-run is not front-loaded before approval instructions: %s", hostOnly)
			}
			if !strings.Contains(core, "Its topic files: storage, network, mounts, host requests,") {
				t.Errorf("core does not keep the host-requests topic discoverable: %s", core)
			}
		})
	}
}

func TestRenderBuiltinSkillsKeepsHostRunProcedureInHostRequestsTopic(t *testing.T) {
	files, err := RenderBuiltinSkills(FactSheet{
		WorkspacePath: "/workspace",
		Briefing:      BriefingInput{Workspace: "/home/test/project"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const path = "jail-mechanics/topics/host-requests.md"
	topic := string(files[path])
	if topic == "" {
		t.Fatalf("generated skill has no %s", path)
	}
	for _, want := range []string{
		"Approvals are not host-run.",
		"never through `yolo host-run`",
		"Exit 0: retry the refused command exactly once",
		"yolo host-run -C <value of $YOLO_HOST_DIR> --name <fresh-label> -- <command>",
		"Then start `yolo wait <fresh-label>` in the background",
		"`yolo host-run show --name <fresh-label>`",
	} {
		if !strings.Contains(topic, want) {
			t.Errorf("generated %s lacks %q", path, want)
		}
	}
	if routed := regexp.MustCompile("yolo host-run[^`\\n]*--\\s+yolo\\s+(approve|config\\s+apply|allow)").FindString(topic); routed != "" {
		t.Errorf("generated %s routes a human approval through host-run: %q", path, routed)
	}
}

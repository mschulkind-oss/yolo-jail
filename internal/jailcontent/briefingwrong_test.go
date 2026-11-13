package jailcontent

import (
	"os"
	"strings"
	"testing"
)

// The "Provisioning failed" section names the startup log where this backend has it. On
// macos-user there is no /workspace, so it named a file the agent could not open.
func TestProvisioningFailedNamesTheLogWhereThisBackendHasIt(t *testing.T) {
	native := BriefingContent(BriefingInput{Workspace: "/Users/Shared/yolo/proj", Mechanism: "macos-user",
		Home: "/Users/_yolojail", ProvisioningFailed: true})
	if !strings.Contains(native, "Read `/Users/Shared/yolo/proj/.yolo/logs/startup.log`") {
		t.Errorf("the macos-user section does not name the workspace's own log:\n%s", native)
	}
	if strings.Contains(native, "/workspace/.yolo/logs/startup.log") {
		t.Errorf("the macos-user section names /workspace, which that backend does not have:\n%s", native)
	}
	container := BriefingContent(BriefingInput{Workspace: "/host/proj", Mechanism: "podman", ProvisioningFailed: true})
	if !strings.Contains(container, "Read `/workspace/.yolo/logs/startup.log`") {
		t.Errorf("the container section lost /workspace's log:\n%s", container)
	}
}

// The Limitations section said authenticated git fails without jail-local credentials, which is
// false when a broker runs git or gh with the host's credentials.
func TestLimitationsDoNotDenyTheBrokers(t *testing.T) {
	out := topicText(BriefingInput{Workspace: "/host/proj", Mechanism: "podman"}, "limits.md")
	if strings.Contains(out, "Only without") {
		t.Errorf("the Limitations section still says only jail-local credentials authenticate:\n%s", out)
	}
	if !strings.Contains(out, "runs through the host's own credentials") {
		t.Errorf("the Limitations section does not name the broker case:\n%s", out)
	}
}

// The skills an agent reads name the home this launch stores, and do not describe loopholes as
// switched on and off: a loophole runs whenever its pack is selected.
func TestBuiltinSkillsNameTheCurrentHomeAndNoLoopholeSwitch(t *testing.T) {
	configure, err := os.ReadFile("builtinskills/configuring-the-jail/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(configure), ".yolo/home/pi") ||
		!strings.Contains(string(configure), "<workspace>/.yolo/setups/<setup>/home") {
		t.Errorf("configuring-the-jail does not name the per-setup home:\n%s", configure)
	}
	diagnose, err := os.ReadFile("builtinskills/diagnosing-the-jail/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(diagnose), "what's enabled") {
		t.Errorf("diagnosing-the-jail still says loopholes are enabled and disabled")
	}
}

// The packs-and-loopholes recipe pointed at a host-side loophole switch that does not exist,
// and the tools recipe wrapped a whole backticked sentence in another pair of backticks.
func TestMechanicsRecipesNameNoLoopholeSwitchAndNestNoBackticks(t *testing.T) {
	for _, name := range []string{"rootless-container", "copy-container", "native-session"} {
		golden, err := os.ReadFile("testdata/mechanics-" + name + ".golden")
		if err != nil {
			t.Fatal(err)
		}
		text := string(golden)
		for _, gone := range []string{"host-side switch", "exact switch command", "`Please run", "edit `packages`"} {
			if strings.Contains(text, gone) {
				t.Errorf("%s: the rendered recipes still say %q", name, gone)
			}
		}
	}
}

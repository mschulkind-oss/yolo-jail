package jailcontent

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func TestBriefingHeaderNamesAndDescribesTheSetup(t *testing.T) {
	cases := []struct {
		name, mechanism, want string
		setup.Setup
	}{
		{name: "seatbelt-self", mechanism: "macos-user", want: "You are running under setup **seatbelt-self**: Seatbelt, deny-by-default, as you", Setup: setup.MustBuiltIn("seatbelt-self")},
		{name: "podman-self", mechanism: "podman", want: "You are running under setup **podman-self**: Podman, as you", Setup: setup.MustBuiltIn("podman-self")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := BriefingContent(BriefingInput{
				Workspace: "/w", Setup: tc.Setup, SetupDescription: setup.Describe(tc.Setup),
				Mechanism: tc.mechanism, IsMacOS: tc.mechanism == "macos-user",
			})
			header := body
			if i := strings.Index(body, "## Environment"); i >= 0 {
				header = body[:i]
			}
			if !strings.Contains(header, tc.want) {
				t.Errorf("briefing header omits setup name or its fields %q:\n%s", tc.want, header)
			}
			if strings.Contains(header, "sandboxed container") || strings.Contains(header, "confinement level") {
				t.Errorf("briefing describes the old confinement notch instead of the setup:\n%s", header)
			}
		})
	}
}

func TestBriefingSetupDescriptionUsesTheAppliedNetwork(t *testing.T) {
	s := setup.MustBuiltIn("podman-self")
	body := BriefingContent(BriefingInput{
		Workspace: "/w", Setup: s, SetupDescription: setup.LaunchDescription(s, "host"),
		Mechanism: "podman", NetMode: "host", AppliedNetMode: "host",
	})
	if !strings.Contains(body, "You are running under setup **podman-self**: Podman, as you; shares the host's network.") {
		t.Fatalf("briefing does not spell the network the launch applied:\n%s", body)
	}
}
func TestHostBriefingHeaderNamesTheHostSetup(t *testing.T) {
	s := setup.MustBuiltIn("host-self")
	body := BriefingContent(BriefingInput{Setup: s, SetupDescription: setup.Describe(s)})
	if strings.Contains(body, "## Environment") || !strings.Contains(body, "setup **host-self**") {
		t.Fatalf("host briefing must stay a header-only briefing and name its setup:\n%s", body)
	}
}

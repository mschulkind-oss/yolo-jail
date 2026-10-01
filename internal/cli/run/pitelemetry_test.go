package run

import (
	"strings"
	"testing"
)

// PI_TELEMETRY IS PI'S OWN SWITCH, SO THE PI PACK SETS IT (docs/design/agent-directory-map.md
// Appendix B; AGENTS.md: core knows no agent). Core used to export PI_TELEMETRY=0 on every
// container launch, as an `-e` in commonEnvBlock and again in the generated .bashrc, whether or
// not pi was selected.
//
// Asserted on what the jail receives: the argv's env block, and the shared file the entrypoint
// exports into every process of the jail (yolo-user-env.sh), which is what reaches a
// non-interactive `yolo -- pi` that never sources .bashrc — the reason the `-e` existed.
func TestPiTelemetryComesFromThePiPackNotCore(t *testing.T) {
	without := assembleWithProfilesAssembled(t, newConfig(), packsFixture(t, "claude", "bedrock"), nil)
	if got := envArgValues(without.argv, "PI_TELEMETRY"); len(got) != 0 {
		t.Errorf("a launch that selects no pi pack still exports pi's variable on the argv: %v", got)
	}
	if got := without.channelEnv(t, "PI_TELEMETRY"); len(got) != 0 {
		t.Errorf("a launch that selects no pi pack still delivers pi's variable: %v", got)
	}

	with := assembleWithProfilesAssembled(t, newConfig(), packsFixture(t, "pi", "bedrock", "openai-auth"), nil)
	if got := envArgValues(with.argv, "PI_TELEMETRY"); len(got) != 0 {
		t.Errorf("pi's variable is still frozen onto the argv by core: %v", got)
	}
	shared, _ := deliveredFiles(t, with.in.envChannel(with.o))
	if !strings.Contains(shared, "\nexport PI_TELEMETRY='0'\n") {
		t.Errorf("the pi pack's PI_TELEMETRY=0 is not in the shared file every jail process gets:\n%s",
			shared)
	}
}

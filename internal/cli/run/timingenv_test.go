package run

// timingenv_test.go pins the CALL SITE of the timing flag's only argv-side effect.
// Options.Timing is read in two places: here (the env pair the launch carries into
// the jail) and run.go's host-side total. The parse half is pinned in internal/cli
// (TestParseRunArgsFlags); this proves the assembled container argv actually carries
// the pair when --timing is set, and that a launch without it carries nothing — the
// same callee-pinned/call-site-unpinned gap providershapeenv_test.go closes for the
// provider env.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// assembleWithTiming is the minimal podman/linux launch with the timing flag set or
// unset, so the assertion below is about that one flag and nothing else.
func assembleWithTiming(t *testing.T, timing bool) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	o.Timing = timing

	sec := jsonx.NewOrderedMap()
	sec.Set("blocked_tools", []any{})
	in := &assembleInput{
		cfg: newConfig(
			"agents", []any{"claude"},
			"security", sec,
		),
		rt:           "podman",
		cname:        "yolo-ws-abcd1234",
		imageRef:     goldenImageRef,
		packs:        claudePackFixture(t),
		agentsPath:   "/agents/yolo-ws-abcd1234",
		wsState:      "/ws/.yolo/home",
		miseStore:    "/mise-store",
		yoloVersion:  "9.9.9-test",
		mountTargets: map[string]struct{}{},
	}
	return o.assembleRunCmd(in)
}

// timingEnvName is the env var the timing launch sets. It still spells "PROFILE": it
// was named for the flag that used to own this meaning (--profile, before
// docs/reference/providers.md OQ-PT5 renamed it --timing), nothing in the image reads
// it, and a rename is a host→jail contract change no step here owns. If it ever moves,
// this constant and the assemble.go site are the two places to change together.
const timingEnvName = "YOLO_PROFILE"

// TestAssembleCarriesTheTimingEnvOnlyForATimingLaunch: `--timing` puts exactly one
// extra variable on the argv and an ordinary launch puts none — a timing launch that
// silently looked unprofiled would report nothing, and an ordinary launch that grew
// the pair would hand every jail a marker nothing consumes.
func TestAssembleCarriesTheTimingEnvOnlyForATimingLaunch(t *testing.T) {
	timed := envArgValues(assembleWithTiming(t, true), timingEnvName)
	if len(timed) != 1 || timed[0] != timingEnvName+"=1" {
		t.Errorf("timing launch env args = %q, want [%s=1]", timed, timingEnvName)
	}

	plain := envArgValues(assembleWithTiming(t, false), timingEnvName)
	if len(plain) != 0 {
		t.Errorf("non-timing launch carried timing env: %q", plain)
	}
}

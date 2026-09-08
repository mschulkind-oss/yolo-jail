package run

// timingenv_test.go pins the CALL SITE of the timing flag's only argv-side effect.
// The gate there is timingReporting() — the --timing flag or the global
// --verbose/-v, the two EXPLICIT spellings (D12) — because the pair below is what
// makes the JAIL half print; a silently-recording launch must not carry it. The
// collector in Run is gated on timingRecording(), which every opt-in turns on;
// the host-side report those spans feed is pinned by timingspans_test.go. The
// parse half is pinned in internal/cli (TestParseRunArgsFlags); this proves the
// assembled container argv actually carries the pair when --timing is set, and
// that a launch without it carries nothing — the same
// callee-pinned/call-site-unpinned gap providershapeenv_test.go closes for the
// provider env.

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// assembleWithTiming is the minimal podman/linux launch with the timing flag set or
// unset, so the assertion below is about that one flag and nothing else.
func assembleWithTiming(t *testing.T, timing bool) []string {
	t.Helper()
	return assembleTimed(t, func(o *Options) { o.Timing = timing })
}

// assembleTimed is the same launch with the timing surface configured by tweak —
// the seam the quiet-recording case needs, since it turns on an opt-in that is
// NOT the flag.
func assembleTimed(t *testing.T, tweak func(*Options)) []string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)
	o := goldenOptions("/ws", home)
	tweak(o)

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
		jailPrefix:   goldenJailPrefix,
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

// D12's half of the same pair: a launch that RECORDS silently — the persistent
// `perf_logging` opt-in, and the env spellings a shell profile exports — must
// NOT carry the pair, because this variable is what makes the in-container half
// PRINT its own block (the jail records to ~/.yolo-perf.log either way). Flip
// the assemble.go gate back to the recording answer and every quiet launch
// starts printing an in-jail table again, which is most of the noise D12 removed.
func TestAssembleWithholdsTheTimingEnvFromAQuietLaunch(t *testing.T) {
	quiet := map[string]func(*Options){
		"perf_logging": func(o *Options) { o.perfLoggingOn = true },
		"YOLO_TIMING": func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{paths.TimingEnv: "1"}[k] }
		},
		"YOLO_VERBOSE": func(o *Options) {
			o.Getenv = func(k string) string { return map[string]string{paths.VerboseEnv: "1"}[k] }
		},
	}
	for name, tweak := range quiet {
		t.Run(name, func(t *testing.T) {
			argv := assembleTimed(t, tweak)
			if got := envArgValues(argv, timingEnvName); len(got) != 0 {
				t.Errorf("a silently-recording launch (%s) carried %s: %q", name, timingEnvName, got)
			}
			// The gate it must not have been confused with: this launch IS recording.
			o := goldenOptions("/ws", t.TempDir())
			tweak(o)
			if !o.timingRecording() {
				t.Fatalf("%s did not turn recording on; the case proves nothing", name)
			}
		})
	}
}

// The explicit --verbose / -v flag is the OTHER printing spelling, and it
// reaches the in-container half exactly as --timing does: the front door's strip
// sets Options.Verbose, and this is what that field buys on the argv.
func TestAssembleCarriesTheTimingEnvForAVerboseLaunch(t *testing.T) {
	argv := assembleTimed(t, func(o *Options) { o.Verbose = true })
	if got := envArgValues(argv, timingEnvName); len(got) != 1 || got[0] != timingEnvName+"=1" {
		t.Errorf("verbose launch env args = %q, want [%s=1]", got, timingEnvName)
	}
}

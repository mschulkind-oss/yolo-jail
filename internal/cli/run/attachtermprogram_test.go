package run

import (
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// termProgramPairs is every `-e TERM_PROGRAM=…` value in argv, in order.
func termProgramPairs(env []string) []string {
	var got []string
	for _, e := range env {
		if strings.HasPrefix(e, termProgramVar+"=") {
			got = append(got, e)
		}
	}
	return got
}

// TestAssembleForwardsTermProgram: the fresh launch carries the launching terminal's
// TERM_PROGRAM, so an agent TUI that gates OSC 8 links on the terminal it identifies (pi-tui)
// finds one behind a generic TERM — herdr's `xterm-256color`. Only when set: an empty pair
// would be a terminal named "".
func TestAssembleForwardsTermProgram(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	emptyLoopholeDirs(t)

	for _, tc := range []struct {
		value string
		want  []string
	}{{"herdr", []string{"TERM_PROGRAM=herdr"}}, {"ghostty", []string{"TERM_PROGRAM=ghostty"}}, {"", nil}} {
		o := goldenOptions("/ws", home)
		o.Getenv = func(k string) string {
			if k == termProgramVar {
				return tc.value
			}
			return ""
		}
		sec := jsonx.NewOrderedMap()
		sec.Set("blocked_tools", []any{})
		got := o.assembleRunCmd(&assembleInput{
			cfg:          newConfig("agents", []any{"claude"}, "security", sec),
			rt:           "podman",
			cname:        "yolo-ws-abcd1234",
			packs:        claudePackFixture(t),
			agentsPath:   "/agents/yolo-ws-abcd1234",
			wsState:      "/ws/.yolo/home",
			miseStore:    "/mise-store",
			yoloVersion:  "9.9.9-test",
			mountTargets: map[string]struct{}{},
		})
		var env []string
		for i := 0; i+1 < len(got); i++ {
			if got[i] == "-e" {
				env = append(env, got[i+1])
			}
		}
		if pairs := termProgramPairs(env); !slices.Equal(pairs, tc.want) {
			t.Errorf("TERM_PROGRAM=%q at launch: the run sets %q, want %q; argv: %q", tc.value, pairs, tc.want, got)
		}
	}
}

// TestAttachExecCarriesTheAttachingTermProgram: the agents in an attached session render to
// the ATTACHING terminal, so its TERM_PROGRAM rides the exec, and one the launch froze is
// cleared (empty: `podman exec` cannot unset) when this invocation has none — a jail launched
// from Ghostty and attached from a terminal that swallows OSC 8 must not keep claiming Ghostty.
// Driven through the real attachExisting, not read from its source.
func TestAttachExecCarriesTheAttachingTermProgram(t *testing.T) {
	for _, tc := range []struct {
		name       string
		invocation string
		frozenEnv  string
		want       []string
	}{
		{"set at attach, never frozen", "ghostty", podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n", []string{"TERM_PROGRAM=ghostty"}},
		{"set at attach overrides frozen", "herdr", podmanSetupNetworkEnv + "TERM_PROGRAM=ghostty\n", []string{"TERM_PROGRAM=herdr"}},
		{"frozen, unset at attach", "", podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\nTERM_PROGRAM=ghostty\n", []string{"TERM_PROGRAM="}},
		{"frozen empty", "", podmanSetupNetworkEnv + "TERM_PROGRAM=\n", nil},
		{"never frozen", "", podmanSetupNetworkEnv + "YOLO_VERSION=9.9.9-test\n", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			argv := attachExecArgv(t, map[string]string{termProgramVar: tc.invocation}, tc.frozenEnv)
			if got := termProgramPairs(execEnvBeforeContainer(t, argv)); !slices.Equal(got, tc.want) {
				t.Errorf("the exec sets %q, want %q; argv: %q", got, tc.want, argv)
			}
		})
	}
}

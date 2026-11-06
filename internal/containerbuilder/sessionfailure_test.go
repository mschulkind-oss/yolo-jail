package containerbuilder

import (
	"strings"
	"testing"
)

// TestSessionStartSaysWhyItDidNotStart: each way Start gives up leaves its reason in Failure, so
// the image build's failure report can quote it. The report quotes the build's returned tail
// and nothing printed before it, and on the 2026-11-05 macOS nightly that tail read only
// "container builder did not start".
func TestSessionStartSaysWhyItDidNotStart(t *testing.T) {
	for _, tc := range []struct {
		name      string
		run       func([]string) int
		reachable bool
		want      string
	}{
		{"pull", func([]string) int { return 1 }, true, "could not pull the Linux builder image"},
		{"run", func(argv []string) int {
			if len(argv) > 1 && argv[1] == "run" {
				return 125
			}
			return 0
		}, true, "could not start the Linux builder container"},
		{"sshd", func([]string) int { return 0 }, false, "sshd never answered"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := &fakeClock{}
			s := &Session{
				Runtime: "podman",
				Deps: Deps{
					Run:         tc.run,
					Output:      okOutput(""),
					Reachable:   func(string, int) bool { return tc.reachable },
					Sleep:       func(float64) { clk.t += 100 },
					Now:         clk.now,
					InContainer: func() bool { return false },
				},
			}
			if _, _, ok := s.Start(); ok {
				t.Fatal("Start succeeded")
			}
			if got := s.Failure(); !strings.Contains(got, tc.want) {
				t.Errorf("Failure() = %q, want it to contain %q", got, tc.want)
			}
		})
	}
	if got := (&Session{}).Failure(); got != "" {
		t.Errorf("a Session that never started says %q, want \"\"", got)
	}
}

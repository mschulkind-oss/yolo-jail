package check

// servedprediction_test.go pins `yolo check` predicting PER RUNTIME what is served at the
// launch's notch (docs/plans/notch-convergence.md §4 item 2): a bridged profile passes the
// pairing gate on a container runtime, where the wire bridge runs, and is predicted REFUSED on
// macos-user, which runs no jail daemon — the refusal that launch now makes. Through
// sectionPacks, so deleting predictedServed's use there fails it.

import (
	"bytes"
	"strings"
	"testing"
)

func TestCheckPredictsTheBridgedPairingPerRuntime(t *testing.T) {
	for _, tc := range []struct {
		runtime string
		refused bool
	}{
		{"", false},
		{"podman", false},
		{"macos-user", true},
	} {
		t.Run("runtime="+tc.runtime, func(t *testing.T) {
			packsFixture(t, `{"packs": ["claude", "cerebras"]}`)
			merged := useProfiles("claude", "cerebras")
			if tc.runtime != "" {
				merged.Set("runtime", tc.runtime)
			}
			var buf bytes.Buffer
			r := &reporter{w: &buf}
			(&Options{}).sectionPacks(r, merged)
			out := buf.String()
			refused := strings.Contains(out, "REFUSED") && strings.Contains(out, "nothing serves it here")
			if refused != tc.refused {
				t.Errorf("runtime %q: predicted the bridged pairing refused = %v, want %v:\n%s",
					tc.runtime, refused, tc.refused, out)
			}
		})
	}
}

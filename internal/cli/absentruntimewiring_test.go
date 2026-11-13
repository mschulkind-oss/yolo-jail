package cli

import (
	"strings"
	"testing"
)

// TestEveryListingVerbIsWiredToNameAnAbsentRuntime pins the call site of listingDetect: `yolo
// prune` (pruneOptions) hands its engine the detector that names the next step when the selected
// setup's runtime is not installed, and fails when its front door goes back to the bare
// detectListingRuntime. `yolo ps` and `yolo stores` ask every source (named-setups-plan.md slices
// 11d and 11f), and their source lines say which technology is not installed.
func TestEveryListingVerbIsWiredToNameAnAbsentRuntime(t *testing.T) {
	for _, tc := range []struct {
		verb string
		run  func()
	}{
		{"prune", func() { pruneOptions([]string{"prune"}).DetectRuntime() }},
	} {
		t.Run(tc.verb, func(t *testing.T) {
			withHomeAndCwd(t)
			onPlatform(t, false)
			t.Setenv("PATH", t.TempDir()) // no runtime installed
			t.Setenv("YOLO_SETUP", "podman-self")
			t.Setenv("YOLO_RUNTIME", "")
			_, stderr := captureStdio(t, tc.run)
			if want := "yolo " + tc.verb + ": Podman, the runtime of setup podman-self, is not installed here"; !strings.Contains(stderr, want) {
				t.Errorf("yolo %s with no Podman: stderr %q lacks %q", tc.verb, stderr, want)
			}
		})
	}
}

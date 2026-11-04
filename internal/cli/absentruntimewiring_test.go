package cli

import (
	"strings"
	"testing"
)

// TestEveryListingVerbIsWiredToNameAnAbsentRuntime pins the three call sites of listingDetect:
// `yolo ps` (runPs), `yolo stores` (storesOptions) and `yolo prune` (pruneOptions) each hand
// their engine the detector that names the next step when the selected setup's runtime is not
// installed. Each case fails when its front door goes back to the bare detectListingRuntime.
func TestEveryListingVerbIsWiredToNameAnAbsentRuntime(t *testing.T) {
	for _, tc := range []struct {
		verb string
		run  func()
	}{
		{"ps", func() { runPs([]string{"ps"}) }},
		{"stores", func() { storesOptions([]string{"stores"}).DetectRuntime() }},
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

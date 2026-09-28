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

// THE PREDICTION CARRIES EACH DAEMON'S DECLARED LISTEN ADDRESS (docs/plans/notch-convergence.md
// NC-D41): a pack env pointer naming {listen} composes from it, and a prediction binds nothing,
// so it names the address a private namespace serves. Without it every such pointer reads as one
// whose daemon declares no address and is withheld from the prediction's gate. Deleting
// predictedServed's WithListen fails this.
func TestCheckPredictsEachDaemonAtItsDeclaredListenAddress(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	writeLoopholeManifest(t, moduleRoot, "acme-adapter",
		`"name":"acme-adapter","description":"d","transport":"none","default_enabled":true,`+
			`"jail_daemon":{"cmd":["yolo-jaild","acme","--listen","{listen}"],"listen":"127.0.0.1:1999"}`)
	served := (&Options{}).predictedServed(useProfiles("claude", "cerebras"), nil)
	if got := served.Listen("acme-adapter"); got != "127.0.0.1:1999" {
		t.Errorf("predicted listen address = %q, want the declared 127.0.0.1:1999", got)
	}
}

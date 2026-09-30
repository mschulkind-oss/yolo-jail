package packload

import "testing"

// TestTheShippedAgentsSayWhoseBackgroundModelsAreOffTheList pins which shipped agents the wire
// bridge's model allowlist admits every model for (docs/design/wire-bridge-gateway.md WG-I41,
// model-lists-and-pickers.md §14.3): codex, whose review and memories models bypass the picker
// and are not yet pointed at listed ids (MM-D9), and copilot, whose background ids are
// unmeasured. Every other agent is refused an off-list model on the bridge path: claude because
// yolo pins every tier to the list (MM-D2), and pi, oh-omp and opencode because the design names
// no background model of theirs. A pack that drops or gains the declaration fails here, where the
// change is a ruling's to make.
func TestTheShippedAgentsSayWhoseBackgroundModelsAreOffTheList(t *testing.T) {
	want := map[string]bool{"codex": true, "copilot": true, "claude": false, "pi": false, "oh-omp": false, "opencode": false}
	seen := map[string]bool{}
	for _, p := range Embedded() {
		for _, bin := range p.InstallBins() {
			if w, ok := want[bin]; ok {
				seen[bin] = true
				if got := p.Decl.SendsUnlistedModels(bin); got != w {
					t.Errorf("%s (pack %s) declares unlisted_background_models = %v, want %v", bin, p.Name, got, w)
				}
			}
		}
	}
	for bin := range want {
		if !seen[bin] {
			t.Errorf("no shipped pack installs %s, so this checks nothing for it", bin)
		}
	}
}

package cli

// confighostlayer_test.go pins WHOSE BYTES `yolo config render` composes as the `host`
// layer ([OQ-CR6](docs/reference/config-target-resolution.md#oq-cr6), ruled (a)).
//
// Three files answer to "the host layer" and they do not hold the same bytes: the copy the
// LAUNCH stages under /ctx (what the boot render reads), the destination inside the jail
// (after one boot, yolo's own output), and the destination on the host (on a managed home,
// likewise yolo's own output). The preview read the destination, so it fed yolo's previous
// output back in as the user's input while the bytes the boot render uses came from neither
// (§2.3 F4). That is the circularity SkillTarget.HostSource was DELETED for.
//
// Every test here drives configRender with a TARGET, which is the call-site shape this
// repo's callee-pinned failures argue for: delete the resolution from renderSurface and the
// notes below stop appearing.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// withStagedHostLayer writes content where a LAUNCH would stage this surface's host copy,
// and returns the target of a jail that owns its workspace — the one target for which the
// staged tree is reachable.
//
// It resolves the destination through entrypoint.StagedHostLayer rather than joining
// "/ctx/host-<pack>/<basename>" by hand: packload.CtxPath decides that layout, the launcher
// emits it and the entrypoint opens it, so a fixture that invented its own would pass while
// the real halves disagreed — which is the bug that shipped in 2026-09-05.
func withStagedHostLayer(t *testing.T, agent, name, content string) configTarget {
	t.Helper()
	s, ok := surfaceManifest().Lookup(agent, name)
	if !ok {
		t.Fatalf("missing %s/%s in the surface manifest", agent, name)
	}
	if !s.HasHostLayer() {
		t.Fatalf("%s/%s declares no host layer, so it cannot stand in for one", agent, name)
	}
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	// The SECOND ambient fact a bare runner cannot supply, and unlike the ctx root it is
	// supplied wrongly rather than not at all when the suite runs INSIDE a jail. yolo exports
	// YOLO_HOST_LAYERS describing ITS OWN boot — "I have already rendered pi/settings" — and
	// entrypoint.StagedHostLayer reads the process environment (os.Getenv) because its two
	// contemplated callers, the boot render and the host CLI, both own the report they read.
	// A unit test owns neither: inherited, the report resolves to HostLayerRender, the staged
	// bytes this fixture just wrote are treated as yolo's own previous output and skipped, and
	// every host-layer assertion below fails for a reason that has nothing to do with the code
	// under test. Empty parses as UNKNOWN, which composes the layer — the behaviour that
	// shipped before the variable existed, and the one these tests are about.
	t.Setenv(packload.HostLayerEnvVar, "")
	staged, _ := entrypoint.StagedHostLayer(s)
	if staged == "" {
		t.Fatalf("no /ctx source derived for %s/%s", agent, name)
	}
	if err := os.MkdirAll(filepath.Dir(staged), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, staged, content)

	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tgt := jailConfigTarget(ws, "the cwd")
	// The one ambient fact a bare runner cannot supply: this process IS the jail that owns
	// the workspace, which is what makes /ctx ours to read.
	tgt.local = true
	return tgt
}

// THE STAGED COPY IS THE LAYER, AND THE DESTINATION IS NOT. Both files exist here and they
// disagree; the preview must show the staged one. Point renderSurface back at the
// destination and this fails on the value.
func TestRenderComposesTheStagedHostCopyNotTheDestination(t *testing.T) {
	withHomeAndCwd(t)
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"staged-by-the-launch"}`)
	// The destination, holding what a previous render left there. Reading THIS is the defect.
	writeFile(t, expandHome("~/.pi/agent/settings.json"), `{"theme":"yolos-own-previous-output"}`)

	var out, errw bytes.Buffer
	if rc := configRender(tgt, []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if !strings.Contains(got, "staged-by-the-launch") {
		t.Errorf("the preview did not compose the copy the launch stages — which is the "+
			"only one the boot render reads:\n%s", got)
	}
	if strings.Contains(got, "yolos-own-previous-output") {
		t.Errorf("the preview read the DESTINATION as the host layer. After one boot that "+
			"file is yolo's own output, so every key yolo wrote comes back labelled `host` "+
			"and a removed pack overlay's value is pinned there forever:\n%s", got)
	}
}

// HOST-SIDE THE LAYER IS UNAVAILABLE, AND SAYS SO. There is no /ctx on the host, and the
// invoking human's own dotfile is a different file — substituting it silently is the error
// the ruling names. The same holds for a jail asked about ANOTHER workspace, whose /ctx
// belongs to this jail rather than to that workspace.
func TestRenderReportsAnUnavailableHostLayerRatherThanSubstituting(t *testing.T) {
	home, _ := withHomeAndCwd(t)
	// The invoking human's own file: present, and not this preview's business.
	writeFile(t, filepath.Join(home, ".pi/agent/settings.json"), `{"theme":"the humans own"}`)

	var out, errw bytes.Buffer
	if rc := configRender(hostTargetForTest(), []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "the humans own") {
		t.Errorf("host-side the preview read the invoking user's real dotfile as the jail's "+
			"`host` layer — a different file, presented as the one the boot render uses:\n%s", got)
	}
	if !strings.Contains(got, "unavailable here") {
		t.Errorf("an unreachable host layer must be REPORTED, not silently dropped — a "+
			"preview missing a layer reads exactly as correct as one that has it:\n%s", got)
	}
}

// A DELIVERY LABELLED A RENDER IS A BASELINE, NOT A LAYER. The bytes are staged and
// readable; what stops them composing is the launcher's label, and the preview says which.
func TestRenderDropsAHostLayerTheLaunchLabelledARender(t *testing.T) {
	withHomeAndCwd(t)
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"yolos-own-host-render"}`)
	s, _ := surfaceManifest().Lookup("pi", "settings")
	wire, err := entrypoint.HostLayerWire{
		HostLayerReport: packload.HostLayerReport{
			Delivery:  packload.HostLayersSupported,
			Delivered: []string{s.HostSource},
		},
		Rendered: []string{s.HostSource},
	}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(packload.HostLayerEnvVar, wire)

	var out, errw bytes.Buffer
	if rc := configRender(tgt, []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	got := out.String()
	if strings.Contains(got, "yolos-own-host-render") {
		t.Errorf("the preview composed a delivery the launch labelled yolo's OWN render. "+
			"Folding it in makes a key yolo wrote indistinguishable from the user's, and "+
			"pins a removed pack overlay's value in the file forever ([P6]):\n%s", got)
	}
	if !strings.Contains(got, "baseline and not a layer") {
		t.Errorf("the preview dropped the layer and did not say why:\n%s", got)
	}
}

// WITHOUT THE LABEL THE SAME BYTES COMPOSE, which is what makes the label load-bearing
// rather than decorative: delete the launcher's Rendered list and this test's twin above
// stops failing, so this one pins that the difference is the label and nothing else.
func TestRenderComposesAnUnlabelledDeliveryAsALayer(t *testing.T) {
	withHomeAndCwd(t)
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"the users own bytes"}`)
	s, _ := surfaceManifest().Lookup("pi", "settings")
	wire, err := entrypoint.HostLayerWire{
		HostLayerReport: packload.HostLayerReport{
			Delivery:  packload.HostLayersSupported,
			Delivered: []string{s.HostSource},
		},
	}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(packload.HostLayerEnvVar, wire)

	var out, errw bytes.Buffer
	if rc := configRender(tgt, []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	if got := out.String(); !strings.Contains(got, "the users own bytes") {
		t.Errorf("an unlabelled delivery is the user's own file and must compose — this is "+
			"the onboarding path [P7] keeps frictionless:\n%s", got)
	}
}

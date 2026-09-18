package entrypoint

// hostlayerlabel_test.go pins the FIFTH disposition
// ([OQ-CR6](docs/design/config-target-resolution.md#oq-cr6)): a delivery the launcher
// labelled yolo's OWN render is a BASELINE, so the boot composes without it.
//
// Every case below differs from its twin in hostlayer_test.go by ONE list in ONE
// environment variable, with the same pack, the same staged bytes and the same home. That
// is the whole mechanism: the jail cannot derive what the bytes ARE — `host_management` is
// deliberately not inherited into a container — so the launch says, and the jail witnesses.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// claudeHostSource is the /ctx destination the two halves agree on for claude/settings,
// spelled out so these fixtures fail if that layout ever moves.
const claudeHostSource = "/ctx/host-claude/settings.json"

// A LABELLED DELIVERY IS NOT COMPOSED. The bytes are staged and readable; what keeps them
// out of the fold is the label, and nothing else about the jail differs from the delivered
// case in hostlayer_test.go. Delete the Rendered check from hostSurfaceBytes and this fails
// on the key.
func TestBootDoesNotComposeAHostLayerLabelledARender(t *testing.T) {
	e, ctx := newClaudePrismEnv(t, map[string]string{
		packload.HostLayerEnvVar: `{"delivery":"supported","delivered":["` + claudeHostSource +
			`"],"rendered":["` + claudeHostSource + `"]}`,
	})
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"),
		[]byte(`{"verbose":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("ConfigurePackByName: %v", err)
	}
	got := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json"))
	if _, present := got["verbose"]; present {
		t.Errorf("the boot composed bytes the launch labelled yolo's own render. A key yolo "+
			"wrote would come back indistinguishable from the user's, so a pack overlay that "+
			"later changes or is removed leaves its old value in place forever ([P6]):\n%v", got)
	}
}

// SAME BYTES, NO LABEL, COMPOSED. This is the twin that makes the test above about the
// label rather than about the file: drop the Rendered list from the launcher and the
// composition comes back.
func TestBootComposesTheSameBytesWithoutTheLabel(t *testing.T) {
	e, ctx := newClaudePrismEnv(t, map[string]string{
		packload.HostLayerEnvVar: `{"delivery":"supported","delivered":["` + claudeHostSource + `"]}`,
	})
	if err := os.WriteFile(filepath.Join(ctx, "settings.json"),
		[]byte(`{"verbose":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("ConfigurePackByName: %v", err)
	}
	if got := decodeJSONFile(t, filepath.Join(e.ClaudeDir(), "settings.json")); got["verbose"] != true {
		t.Errorf("verbose = %v, want true — an UNLABELLED delivery is the user's own file, "+
			"and composing it is the onboarding path [P7] keeps frictionless", got["verbose"])
	}
}

// A LABELLED DELIVERY NEVER REFUSES, even when it cannot be read. The fail-closed refusal
// exists to stop a composition that silently drops the user's own keys; a render composed
// from packs alone drops none, so refusing over bytes the render discards would be the
// over-refusal the `rmw` arm declines to make for the same reason.
func TestALabelledHostLayerNeverRefusesTheLaunch(t *testing.T) {
	e, _ := newClaudePrismEnv(t, map[string]string{
		packload.HostLayerEnvVar: `{"delivery":"supported","delivered":["` + claudeHostSource +
			`"],"rendered":["` + claudeHostSource + `"]}`,
	})
	// No file staged at all: the same input the refusal test uses, minus the label.
	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("the boot refused over a layer it was never going to compose: %v", err)
	}
}

// AND IT SAYS SO IN THE BOOT LOG. The composition looks exactly as correct either way, so
// the one observable difference between "the user has no host file" and "the host file is
// yolo's own render" has to be written down — this is the record a reader of boot.log needs
// and a user watching a healthy launch does not (Env.note).
func TestALabelledHostLayerIsRecordedInTheBootLog(t *testing.T) {
	// A temp ctx root, so the read this test proves does NOT happen could not have found a
	// real /ctx mount if it did: in the jail this repo is developed inside, that path is the
	// developer's own settings.json.
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	var log strings.Builder
	e := &Env{Home: t.TempDir(), Vars: map[string]string{
		packload.HostLayerEnvVar: `{"delivery":"supported","delivered":["` + claudeHostSource +
			`"],"rendered":["` + claudeHostSource + `"]}`,
	}, LogOnly: &log}

	data, err := hostSurfaceBytes(e, manifest.Surface{
		Agent: "claude", Name: "settings", Path: "~/.claude/settings.json",
		Codec: "json", ReadsHost: true, HostSource: claudeHostSource,
	})
	if err != nil || data != nil {
		t.Fatalf("hostSurfaceBytes = (%q, %v), want no layer and no refusal", data, err)
	}
	if !strings.Contains(log.String(), "baseline and not a layer") {
		t.Errorf("the boot dropped a host layer and left no record of why:\n%s", log.String())
	}
}

// THE FIFTH IS READ BEFORE THE FILE, and the ordering is what makes it a label rather than
// a fallback: a rendered path is DELIVERED too, so a reader that consulted the report only
// after a failed read would compose exactly the bytes this exists to keep out.
func TestTheRenderLabelOutranksTheDeliveredAnswer(t *testing.T) {
	wire, err := HostLayerWire{
		HostLayerReport: packload.HostLayerReport{
			Delivery:  packload.HostLayersSupported,
			Delivered: []string{claudeHostSource},
		},
		Rendered: []string{claudeHostSource},
	}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if got := HostLayerDispositionIn(wire, claudeHostSource); got != HostLayerRender {
		t.Errorf("disposition = %q, want %q for a path that is both delivered and labelled",
			got, HostLayerRender)
	}
	// And packload's own reader still reads the same wire, unchanged: the label is an
	// ADDITION to the report, so a half that does not know about it (macosuser's plan
	// builder, an older jail) sees exactly what it saw before.
	r, ok := packload.ParseHostLayerReport(wire)
	if !ok || r.DispositionFor(claudeHostSource) != packload.HostLayerDelivered {
		t.Errorf("the four-disposition reader no longer reads this wire (ok=%v): %+v", ok, r)
	}
}

// AN UNLABELLED, UNPARSEABLE OR ABSENT REPORT IS UNKNOWN — packload's contract, unchanged.
// A jail that read a garbled variable as an empty delivery list would refuse every host
// layer on the machine.
func TestAGarbledReportIsUnknownNotALabel(t *testing.T) {
	for _, wire := range []string{"", "not json", `{"delivered":["/ctx/x"]}`} {
		if got := HostLayerDispositionIn(wire, claudeHostSource); got != packload.HostLayerUnknown {
			t.Errorf("HostLayerDispositionIn(%q) = %q, want %q", wire, got, packload.HostLayerUnknown)
		}
	}
}

// THE DISCRIMINATOR IS THE MARK, NOT THE POSTURE, and this is the case that forces it: an
// ABSENT `host_management` resolves to `assert` (OQ-CO2), so a posture test would label
// every default machine's settings.json "a render" and stop every jail composing the file
// its user already has — exactly the onboarding path [P7] protects. The provenance record
// is what a host render actually leaves behind, so it is what the launcher labels from.
func TestHostSurfaceRenderedReadsTheProvenanceMark(t *testing.T) {
	home := t.TempDir()
	s := manifest.Surface{Agent: "claude", Name: "settings", Path: "~/.claude/settings.json"}
	if HostSurfaceRendered(home, s) {
		t.Fatal("a home yolo has never rendered into reports a render — every default " +
			"install would stop composing the user's own settings file")
	}
	// The one trace a host render leaves, written through the Target that decides where it
	// goes rather than a hand-joined path.
	mark := render.Host(home, nil, render.OwnershipUnstated).ProvenancePath(s.Agent, s.Name)
	if err := os.MkdirAll(filepath.Dir(mark), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mark, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if !HostSurfaceRendered(home, s) {
		t.Errorf("yolo has rendered %s/%s into this home and the label says otherwise — the "+
			"jail would fold yolo's own output back in as the user's", s.Agent, s.Name)
	}
}

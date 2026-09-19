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
//
// # And every test here NAMES the launch report it composes under
//
// hostLayerFor switches on what the LAUNCH said about the staged bytes
// (entrypoint.StagedHostLayer → os.Getenv(YOLO_HOST_LAYERS)), which is right for both
// processes that function was written for — the boot render and the host CLI each own the
// report they read. A unit test owns neither, and this repo is DEVELOPED FROM INSIDE ITS OWN
// JAIL, so a bare `go test` inherits a report describing THAT jail's boot: pi/settings
// delivered and labelled yolo's own render. The disposition resolves to HostLayerRender, the
// staged bytes the fixture just wrote are skipped as yolo's previous output, and four tests
// fail naming the surfaces rather than the cause (MEASURED 2026-09-19:
// TestConfigRenderExplain, TestConfigRenderExplainColor, TestConfigRenderMergesThenEnforces,
// TestRenderComposesTheStagedHostCopyNotTheDestination).
//
// This is the SECOND member of a class — the first, YOLO_VERSION, ran the other way and made
// CI red while every local run stayed green (internal/config/hostscope_test.go). The fixture
// shape is that file's: a NAMED fixture per arm, never a blanket scrub, because forcing every
// test onto one arm leaves the other with no test at all. hostLayerDelivery below is the pair,
// isolateTheStagedTree is the package-wide default beneath it, and
// TestTheStagedFixtureOutranksTheJailsOwnReport is the guard that fails if either is dropped.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// hostLayerDelivery is what a LAUNCH said about the bytes withStagedHostLayer stages: the two
// dispositions hostLayerFor switches on, plus the absence that predates the variable. Every
// call site names one, so no test's outcome is a property of the machine running it.
type hostLayerDelivery int

const (
	// usersOwnBytes — delivered and UNLABELLED. The file is the user's own, so it composes as
	// the `host` layer: the onboarding path [P7] keeps frictionless, and the arm an absent
	// `host_management` (which resolves to `assert`) must NOT take.
	usersOwnBytes hostLayerDelivery = iota
	// yolosOwnRender — delivered AND labelled (entrypoint.HostLayerRender). Yolo's own output,
	// so it is the baseline the jail reports divergence against and never a layer ([P6]).
	yolosOwnRender
	// noReportAtAll — nothing in the environment, which is a launcher older than the variable.
	// UNKNOWN, which composes; the behaviour that shipped before the label existed, and the one
	// every host-side `go test` on a CI runner takes by default.
	noReportAtAll
)

// pinHostLayerReport makes YOLO_HOST_LAYERS say one definite thing about this surface for the
// duration of one test, whatever the ambient environment.
//
// It BUILDS the report through entrypoint.HostLayerWire rather than writing the JSON out by
// hand, for the reason withStagedHostLayer resolves its path through StagedHostLayer: the
// launcher marshals that struct (run/packhostgrants.go) and the readers parse it, so a fixture
// carrying its own spelling of the wire would keep passing while the two halves disagreed.
func pinHostLayerReport(t *testing.T, s manifest.Surface, d hostLayerDelivery) {
	t.Helper()
	if d == noReportAtAll {
		t.Setenv(packload.HostLayerEnvVar, "")
		return
	}
	w := entrypoint.HostLayerWire{HostLayerReport: packload.HostLayerReport{
		Delivery:  packload.HostLayersSupported,
		Delivered: []string{s.HostSource},
	}}
	if d == yolosOwnRender {
		// A labelled path is in Delivered TOO — the label says what arrived, not whether
		// anything did — so this is a subset and not a substitution.
		w.Rendered = []string{s.HostSource}
	}
	wire, err := w.Marshal()
	if err != nil {
		t.Fatalf("marshal the host-layer report: %v", err)
	}
	t.Setenv(packload.HostLayerEnvVar, wire)
}

// isolateTheStagedTree is the package's DEFAULT for the two ambient facts about the staged
// tree, installed by TestMain (hostdepstub_test.go) and returning its own cleanup.
//
// It is the guard rather than the fix: the four tests above are fixed by naming their arm, and
// this is what stops the FIFTH — a test someone writes tomorrow that drives configRender over
// claude/settings with a local target and never thinks about /ctx. In this development jail
// that test would read the developer's real ~/.claude/settings.json through the real
// /ctx/host-claude/settings.json, and today only the inherited `rendered` label happens to
// stop it: the two ambient facts cancel, and a jail whose home carries no host provenance for
// that surface would uncancel them. hostdepstub_test.go's TestMain already makes this argument
// for the install runner — "overriding the seam per test would protect the tests someone
// remembered to write it into".
//
// The ctx root is pointed at an EMPTY REAL DIRECTORY rather than unset, because unset resolves
// to the live /ctx. Empty is the honest answer for a process that is not a jail boot: every
// unowned read gets os.IsNotExist, which is the silent no-layer path a host takes.
func isolateTheStagedTree() func() {
	root, err := os.MkdirTemp("", "yolo-cli-staged-tree-")
	if err != nil {
		panic("isolate the staged tree: " + err.Error())
	}
	_ = os.Setenv("YOLO_CTX_ROOT", root)
	_ = os.Unsetenv(packload.HostLayerEnvVar)
	return func() { _ = os.RemoveAll(root) }
}

// withStagedHostLayer writes content where a LAUNCH would stage this surface's host copy,
// and returns the target of a jail that owns its workspace — the one target for which the
// staged tree is reachable.
//
// It resolves the destination through entrypoint.StagedHostLayer rather than joining
// "/ctx/host-<pack>/<basename>" by hand: packload.CtxPath decides that layout, the launcher
// emits it and the entrypoint opens it, so a fixture that invented its own would pass while
// the real halves disagreed — which is the bug that shipped in 2026-09-05.
// The `delivered` argument is not optional and has no default: it is the ambient fact a bare
// runner cannot supply, and unlike the ctx root it is supplied WRONGLY rather than not at all
// when the suite runs inside a jail (see this file's header). Naming it at the call site is
// what makes each test's arm the fixture's answer instead of the machine's.
func withStagedHostLayer(t *testing.T, agent, name, content string, delivered hostLayerDelivery) configTarget {
	t.Helper()
	s, ok := surfaceManifest().Lookup(agent, name)
	if !ok {
		t.Fatalf("missing %s/%s in the surface manifest", agent, name)
	}
	if !s.HasHostLayer() {
		t.Fatalf("%s/%s declares no host layer, so it cannot stand in for one", agent, name)
	}
	t.Setenv("YOLO_CTX_ROOT", t.TempDir())
	pinHostLayerReport(t, s, delivered)
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
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"staged-by-the-launch"}`, usersOwnBytes)
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
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"yolos-own-host-render"}`,
		yolosOwnRender)

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
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"the users own bytes"}`,
		usersOwnBytes)

	var out, errw bytes.Buffer
	if rc := configRender(tgt, []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	if got := out.String(); !strings.Contains(got, "the users own bytes") {
		t.Errorf("an unlabelled delivery is the user's own file and must compose — this is "+
			"the onboarding path [P7] keeps frictionless:\n%s", got)
	}
}

// AND WITH NO REPORT AT ALL THE SAME BYTES COMPOSE, which is the third arm and the only one a
// host-side `go test` reaches by default: an absent variable means "launcher older than the
// variable", never "nothing was delivered". Make ParseHostLayerWire fail CLOSED on an empty
// value and this test is what says so — every host layer on every older launcher would drop.
func TestRenderComposesAHostLayerNoReportMentions(t *testing.T) {
	withHomeAndCwd(t)
	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"no report was emitted"}`,
		noReportAtAll)

	var out, errw bytes.Buffer
	if rc := configRender(tgt, []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	if got := out.String(); !strings.Contains(got, "no report was emitted") {
		t.Errorf("an absent report is UNKNOWN, which composes the layer:\n%s", got)
	}
}

// THE FIXTURE OWNS THE REPORT AND THE AMBIENT JAIL DOES NOT. This is the regression test for
// the defect the fixture exists to remove, written in the one shape that cannot be satisfied by
// a comment: it PLANTS the exact report this development jail exports — pi/settings delivered
// and labelled yolo's own render — in the position a developer's process environment occupies,
// and then asserts the staged bytes still compose.
//
// Delete pinHostLayerReport's call from withStagedHostLayer (or give `delivered` a default that
// falls through to the environment) and this fails, as do the four tests that failed for
// everyone running the unit suite in a jail. It is not a test of the fixture alone: it drives
// configRender end to end, so pointing renderSurface back at the destination fails it too.
func TestTheStagedFixtureOutranksTheJailsOwnReport(t *testing.T) {
	withHomeAndCwd(t)
	s, ok := surfaceManifest().Lookup("pi", "settings")
	if !ok {
		t.Fatal("missing pi/settings in the surface manifest")
	}
	ambient, err := entrypoint.HostLayerWire{
		HostLayerReport: packload.HostLayerReport{
			Delivery:  packload.HostLayersSupported,
			Delivered: []string{s.HostSource},
		},
		Rendered: []string{s.HostSource},
	}.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(packload.HostLayerEnvVar, ambient)

	tgt := withStagedHostLayer(t, "pi", "settings", `{"theme":"staged-by-this-test"}`,
		usersOwnBytes)

	var out, errw bytes.Buffer
	if rc := configRender(tgt, []string{"pi/settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("rc=%d, stderr=%s", rc, errw.String())
	}
	if got := out.String(); !strings.Contains(got, "staged-by-this-test") {
		t.Errorf("the fixture's own report was outranked by the ambient one, so this test's "+
			"outcome is a property of the machine running it — which is the whole defect:\n%s",
			got)
	}
}

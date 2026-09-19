package entrypoint

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// piSettingsHome lays out a home holding one pi settings file, the state a render that
// happened before the pack was fixed left behind.
func piSettingsHome(t *testing.T, content string) (string, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".pi", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

// renderPiHost runs the SHIPPED pi pack at the host notch — what `yolo host apply` (observe)
// and `--assert` (write) do — and returns the pi/settings result.
//
// The shipped pack rather than a fixture surface, deliberately: guard 2 only lets the repair
// fire when the pack's own `defaults` has moved OFF the rejected value, so running the real
// manifest is what pins that packs/pi/pack.json is actually fixed. A revert there turns these
// tests red instead of leaving a silent no-op behind.
func renderPiHost(t *testing.T, home string, observe bool) HostRenderResult {
	t.Helper()
	pi, err := embeddedPack("pi")
	if err != nil {
		t.Fatalf("embedded pi: %v", err)
	}
	results, rerr := RenderHostPack(pi, home, render.OwnershipAssert, observe, nil)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	for _, r := range results {
		if r.Surface == "pi/settings" {
			return r
		}
	}
	t.Fatalf("no pi/settings result: %+v", results)
	return HostRenderResult{}
}

// TestHostRenderRepairsTheRejectedValue is CASE 1 at the host notch, which is where the value
// is frozen HARDEST: an rmw render reads the file back as its `host` layer, so from the next
// apply on the value yolo wrote as a default reads as the user's and no later default can
// displace it.
//
// It pins the ORDER as well as the repair. The provenance assertion is what fails if the
// repair moves below `present := obj.Keys()`: the record would read `host` — yolo's own output
// laundered into "the user set this" — instead of `defaults`, where a fill-if-absent key
// belongs.
//
// FAILS IF THE CALL SITE IN renderSurfaceRMWSurface IS DELETED.
func TestHostRenderRepairsTheRejectedValue(t *testing.T) {
	home, path := piSettingsHome(t, `{"theme":"system","defaultModel":"sonnet"}`)

	got := renderPiHost(t, home, false)
	if got.Action != "rendered" {
		t.Fatalf("pi/settings action = %q, want rendered", got.Action)
	}
	after := decodeJSONFile(t, path)
	if after["theme"] != "light/dark" {
		t.Errorf("theme = %v, want light/dark (the pack's current default, filled by absence)",
			after["theme"])
	}
	// The repair deletes ONE key. Everything else in the user's file is untouched — an rmw
	// render that lost a neighbouring key while fixing one would be a far worse bug.
	if after["defaultModel"] != "sonnet" {
		t.Errorf("defaultModel = %v, want sonnet left alone", after["defaultModel"])
	}
	if len(got.Repaired) != 1 {
		t.Fatalf("Repaired = %v, want one sentence", got.Repaired)
	}
	for _, want := range []string{"removed", "pi/settings", `theme = "system"`, `"light/dark"`} {
		if !strings.Contains(got.Repaired[0], want) {
			t.Errorf("Repaired[0] = %q, missing %q", got.Repaired[0], want)
		}
	}
	prov, found := hostProvenance(t, home, "pi", "settings")
	if !found {
		t.Fatal("the host notch wrote no provenance record")
	}
	if prov["theme"] != "defaults" {
		t.Errorf("provenance[theme] = %q, want defaults. `host` means the repair ran AFTER "+
			"the pre-render snapshot, which re-freezes the key one value later", prov["theme"])
	}
}

// TestHostRenderLeavesAChosenThemeAlone is CASE 2: a theme the user picked survives, even
// though the default has moved. `dracula` is not a builtin either — it comes from
// ~/.pi/agent/themes/ — so this pins that an entry matches ONE VALUE and is not a predicate
// over "themes yolo cannot verify", which yolo has no way to evaluate.
func TestHostRenderLeavesAChosenThemeAlone(t *testing.T) {
	home, path := piSettingsHome(t, `{"theme":"dracula"}`)

	got := renderPiHost(t, home, false)
	if after := decodeJSONFile(t, path); after["theme"] != "dracula" {
		t.Errorf("theme = %v, want dracula — a chosen value is not yolo's to repair", after["theme"])
	}
	if len(got.Repaired) != 0 {
		t.Errorf("Repaired = %v, want none", got.Repaired)
	}
}

// TestHostRenderRepairsNothingWhenTheKeyIsAbsent is CASE 3: a home that never got the bad
// value gets the ordinary fill and NO notice. A repair sentence about a file yolo did not
// change is the mechanism's worst failure mode — it is a claim to have edited the user's
// config.
func TestHostRenderRepairsNothingWhenTheKeyIsAbsent(t *testing.T) {
	home, path := piSettingsHome(t, `{"defaultModel":"sonnet"}`)

	got := renderPiHost(t, home, false)
	if len(got.Repaired) != 0 {
		t.Errorf("Repaired = %v, want none for a key that was never set", got.Repaired)
	}
	if after := decodeJSONFile(t, path); after["theme"] != "light/dark" {
		t.Errorf("theme = %v, want the default filled in", after["theme"])
	}
}

// TestHostRenderRepairIsIdempotent is CASE 4: the second apply repairs nothing and says
// nothing, because the mechanism keeps no state — "already done" and "never needed" are the
// same observation. That is what lets an entry sit on the per-render path with no sentinel,
// and what makes retiring one a plain deletion.
func TestHostRenderRepairIsIdempotent(t *testing.T) {
	home, path := piSettingsHome(t, `{"theme":"system"}`)

	if first := renderPiHost(t, home, false); len(first.Repaired) != 1 {
		t.Fatalf("first apply Repaired = %v, want one", first.Repaired)
	}
	afterFirst, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	second := renderPiHost(t, home, false)
	if len(second.Repaired) != 0 {
		t.Errorf("second apply Repaired = %v, want none", second.Repaired)
	}
	afterSecond, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(afterFirst, afterSecond) {
		t.Errorf("second apply changed the file:\n %s\nwant %s", afterSecond, afterFirst)
	}
	if second.WouldChange {
		t.Error("WouldChange = true on a second apply that changed nothing")
	}
}

// TestHostRenderObservePreviewsTheRepairWithoutMakingIt is the DRY RUN — `yolo host apply`
// with no --assert, the posture the surrounding facility already offers, which is why the
// repair needed no flag of its own.
//
// The preview says "would remove" — a dry run may not claim a mutation it did not make — and
// the file is untouched. The predicate half is measured separately, below, because at the
// SHIPPED pi surface it cannot be: the autonomy posture patch adds a managed key
// (`defaultProjectTrust`) the fixture file never has, so `WouldChange` is true for every input
// here and an assertion on it would pin nothing.
func TestHostRenderObservePreviewsTheRepairWithoutMakingIt(t *testing.T) {
	home, path := piSettingsHome(t, `{"theme":"system"}`)

	got := renderPiHost(t, home, true)
	if len(got.Repaired) != 1 {
		t.Fatalf("Repaired = %v, want one previewed repair", got.Repaired)
	}
	if !strings.Contains(got.Repaired[0], "would remove") {
		t.Errorf("Repaired[0] = %q, want the dry-run tense: a preview may not claim a "+
			"mutation it did not make", got.Repaired[0])
	}
	if after := decodeJSONFile(t, path); after["theme"] != "system" {
		t.Errorf("theme = %v, an observe run must write nothing", after["theme"])
	}
}

// TestHostChangePredicateCountsTheRepair pins the CHANGE PREDICATE against the writer, over a
// surface whose only pending change IS the repair.
//
// The divergence it guards is not cosmetic. `WouldChange` decides the `unchanged` verdict and
// the report tier, so a predicate that skipped the repair would have `yolo host apply` print
// `unchanged` for the very surface whose repair the line below it announces — and, worse, an
// `--assert` would then edit a file a dry run had called settled.
//
// FAILS IF THE agentcfg.RepairRejected CALL IN hostSurfaceWouldChange IS DELETED.
func TestHostChangePredicateCountsTheRepair(t *testing.T) {
	surface := piFixedSurfaceDecl(manifest.ModeRMW)
	for _, tc := range []struct {
		file string
		want bool
	}{
		{`{"theme":"system"}`, true},      // the repair, and nothing else, is the change
		{`{"theme":"dracula"}`, false},    // a chosen value: nothing to do
		{`{"theme":"light/dark"}`, false}, // already what the default says
	} {
		home, _ := piSettingsHome(t, tc.file)
		e := &Env{Home: home, Vars: map[string]string{},
			hostTarget: true, hostOwnership: render.OwnershipAssert}
		if got := hostSurfaceWouldChange(e, surface, filepath.Join(home, ".pi", "agent",
			"settings.json"), nil, nil); got != tc.want {
			t.Errorf("wouldChange(%s) = %v, want %v", tc.file, got, tc.want)
		}
	}
}

// piFixedSurfaceDecl is the corrected pi settings surface as a bare declaration, for the two
// jail-notch tests below — which render into a temp home rather than through a pack.
func piFixedSurfaceDecl(mode string) manifest.Surface {
	return manifest.Surface{
		Agent: "pi", Name: "settings", Codec: "json", Mode: mode,
		Path:     "~/.pi/agent/settings.json",
		Defaults: map[string]any{"theme": "light/dark"},
	}
}

// TestJailRMWRepairSaysSoOnStderr pins the LOUDNESS of the rmw notch, which is the whole
// reason the repair returns a report instead of quietly mutating: yolo is editing a key in the
// user's own config file, once, and a mutation nobody announced is the defect rather than the
// mutation.
//
// FAILS IF THE noteRepairs CALL IN renderSurfaceRMWSurface IS DELETED.
func TestJailRMWRepairSaysSoOnStderr(t *testing.T) {
	home, _ := piSettingsHome(t, `{"theme":"system"}`)
	var errw bytes.Buffer
	e := &Env{Home: home, Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}

	if err := renderSurfaceRMWSurface(e, piFixedSurfaceDecl(manifest.ModeRMW), nil, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := errw.String()
	for _, want := range []string{"repaired", "pi/settings", `theme = "system"`, "Theme not found"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr = %q, missing %q", out, want)
		}
	}
}

// TestJailStatefulRepairSaysSoOnStderr is the same claim for the stateful notch, whose repair
// lands in the capture overlay rather than in the file — the state a jail that has already
// migrated carries the value in.
//
// Announced from the PERSIST half, so the sentence is only ever spoken about a write that
// happened: a compose that fails to persist has repaired nothing the user can see.
//
// FAILS IF THE noteRepairedValues CALL IN persistStatefulSurface IS DELETED.
func TestJailStatefulRepairSaysSoOnStderr(t *testing.T) {
	home, path := piSettingsHome(t, `{"theme":"system"}`)
	var errw bytes.Buffer
	e := &Env{Home: home, Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}

	if _, err := renderSurfaceStatefulDetail(e, piFixedSurfaceDecl(manifest.ModeStateful),
		nil, nil, nil); err != nil {
		t.Fatalf("render: %v", err)
	}
	if out := errw.String(); !strings.Contains(out, "repaired") ||
		!strings.Contains(out, `theme = "system"`) {
		t.Errorf("stderr = %q, want the repair announced", out)
	}
	if after := decodeJSONFile(t, path); after["theme"] != "light/dark" {
		t.Errorf("theme = %v, want light/dark", after["theme"])
	}
}

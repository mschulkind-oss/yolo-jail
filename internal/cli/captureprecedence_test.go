package cli

// captureprecedence_test.go pins ONE sentence — the ceiling on a capture overlay — in the
// three places a user reads it, plus the fold that makes the sentence true.
//
// The three strings said "outrank the host layer" (`config ls`, `config diff`) and
// "outranking the definition" (`apply --sealed`) until 2026-09-14, which is an
// unconditional win the fold does not grant: `computed` and `managed` both beat the
// capture (configls.go's header states the whole stack, and TestTheCeilingTheStringsName
// below measures it). For mise/config, which declares no `host` layer and whose only
// yolo-owned layer IS `computed`, the old wording was wrong in both halves at once.
//
// ⚠ EVERY ASSERTION HERE READS EMITTED BYTES, never a source file. Each of these files
// also EXPLAINS the retired claim in a comment, so a grep for the old wording matches the
// correction that removed it — the trap that has produced four bogus greens in this repo.
// Driving the commands is also what makes the test fail if a call site is deleted: the
// clause is not asserted against a formatter, it is asserted against `configLs`,
// `configDiff` and `applyMain --sealed`.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// captureCeilingClause is the clause all three reports share. P8's closed vocabulary is
// the reason it is ONE spelling rather than three paraphrases: the reader learns the
// ceiling once and recognises it wherever it appears.
const captureCeilingClause = "every layer but `computed` and `managed`"

// retiredCaptureClaims are the overstatements this file replaced. Asserted ABSENT from the
// emitted report so a revert fails rather than merely losing the correction.
var retiredCaptureClaims = []string{
	"outrank the host layer",
	"outranks the host layer",
	"outranking the definition",
	"outrank every declared layer",
}

// assertCaptureCeiling checks one command's OUTPUT: the ceiling is stated, and no retired
// claim survives anywhere in it.
func assertCaptureCeiling(t *testing.T, command, got string) {
	t.Helper()
	if !strings.Contains(got, captureCeilingClause) {
		t.Errorf("%s does not state the ceiling on a capture (%q):\n%s",
			command, captureCeilingClause, got)
	}
	for _, retired := range retiredCaptureClaims {
		if strings.Contains(got, retired) {
			t.Errorf("%s claims %q, which the fold does not grant — `computed` and "+
				"`managed` beat a capture:\n%s", command, retired, got)
		}
	}
}

// TestConfigLsStatesTheCaptureCeiling: the ⚠ footer names the two layers a capture does
// NOT beat, by the spelling the LAYERS column above it uses, so the reader can tell which
// of their own surfaces the warning actually applies to.
func TestConfigLsStatesTheCaptureCeiling(t *testing.T) {
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configLs([]string{"--all"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configLs rc=%d, stderr=%s", rc, errw.String())
	}
	assertCaptureCeiling(t, "yolo config ls", out.String())
}

// TestConfigDiffStatesTheCaptureCeiling: the same clause closes the diff, where the user
// is looking at the captured values themselves.
func TestConfigDiffStatesTheCaptureCeiling(t *testing.T) {
	dir := withSidecarDir(t)
	writeSidecar(t, dir, "claude", "settings", `{"theme":"dark"}`, `{"theme":"light"}`)

	var out, errw bytes.Buffer
	if rc := configDiff([]string{"claude", "--surface", "settings"}, &out, &errw, false); rc != 0 {
		t.Fatalf("configDiff rc=%d, stderr=%s", rc, errw.String())
	}
	assertCaptureCeiling(t, "yolo config diff", out.String())
}

// TestApplySealedStatesTheCaptureCeiling: the refusal is the one of the three that carries
// a REMEDY, so the fact it justifies the refusal with has to be the true one.
//
// Scratch home + cwd so workspaceRoot() resolves here rather than at /workspace, whose
// sidecars are real (TestApplySealedClosure's rule, and the reason `reset` needs the same
// seam).
func TestApplySealedStatesTheCaptureCeiling(t *testing.T) {
	home, repo := withHomeAndCwd(t)
	writeFile(t, filepath.Join(repo, "yolo-jail.jsonc"), `{"packs":["claude"]}`)
	writeFile(t, filepath.Join(repo, ".yolo", "keep"), "x")
	// Declared, so the unset-`host_management` refusal cannot supply the output this test
	// reads (it names no capture at all, and the assertion would pass vacuously).
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"host_management":"assert"}`)

	s, ok := surfaceManifest().Lookup("claude", "settings")
	if !ok {
		t.Fatal("missing claude/settings")
	}
	writeFile(t, prismOverlayPath(s.Agent, s.Name), `{"myEdit":"present"}`)

	var out, errw bytes.Buffer
	if rc := applyMain([]string{"--sealed"}, &out, &errw, false, nil); rc != 1 {
		t.Fatalf("an outstanding capture overlay should refuse (rc 1), got %d: %s%s",
			rc, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "claude/settings") {
		t.Fatalf("the refusal did not come from the capture scan:\n%s", out.String())
	}
	assertCaptureCeiling(t, "yolo apply --sealed", out.String())
}

// TestTheCeilingTheStringsName measures the claim the three reports now make, so a reorder
// of the fold fails HERE — beside the wording — instead of turning three true sentences
// false in silence.
//
// One surface, one key per layer, and the capture overlay claiming all three: the `host`
// key it wins (the layer the retired wording named), the `computed` and `managed` keys it
// loses. agentcfg's own suite pins the fold from the inside
// (TestComposeComputedLayer, TestComposeManagedWinsOverComputed); this asserts the
// sentence the CLI prints about it.
func TestTheCeilingTheStringsName(t *testing.T) {
	res, err := agentcfg.Compose(agentcfg.Inputs{
		Surface: manifest.Surface{
			Agent: "example", Name: "settings", Path: "~/.example/settings.json",
			Codec:   "json",
			Managed: map[string]any{"managedKey": "from-managed"},
		},
		HostBytes: []byte(`{"hostKey":"from-host"}`),
		Overlay: map[string]any{
			"hostKey":     "from-capture",
			"computedKey": "from-capture",
			"managedKey":  "from-capture",
		},
		Computed: map[string]any{"computedKey": "from-computed"},
	})
	if err != nil {
		t.Fatalf("Compose: %v", err)
	}
	got := res.ConfigMap()
	if got["hostKey"] != "from-capture" {
		t.Errorf("hostKey = %v, want from-capture — a capture outranks the `host` layer, "+
			"which is why the reports say \"every layer BUT\" rather than dropping the "+
			"claim", got["hostKey"])
	}
	if got["computedKey"] != "from-computed" {
		t.Errorf("computedKey = %v, want from-computed — the reports name `computed` as a "+
			"layer a capture does not beat", got["computedKey"])
	}
	if got["managedKey"] != "from-managed" {
		t.Errorf("managedKey = %v, want from-managed — the reports name `managed` as a "+
			"layer a capture does not beat", got["managedKey"])
	}
	if res.Provenance["computedKey"] != agentcfg.LayerComputed ||
		res.Provenance["hostKey"] == agentcfg.LayerHost {
		t.Errorf("provenance disagrees with the values: %v", res.Provenance)
	}
}

// TestMiseConfigHasNoHostLayerToOutrank is the CONTROL on why the old wording was wrong
// rather than merely imprecise: `config ls` prints the ⚠ footer for whichever surfaces
// carry a capture, and mise/config — the one the launch banner flags most often — has no
// `host` layer for a capture to outrank at all. Its only yolo-owned layer is the
// `computed` [tools] table, which BEATS the capture.
//
// If mise/config ever grows a host layer this test fails, and the fix is to re-check the
// footer's wording rather than to delete the case: the footer would still be printed for
// surfaces that have none.
func TestMiseConfigHasNoHostLayerToOutrank(t *testing.T) {
	s, ok := surfaceManifest().Lookup("mise", "config")
	if !ok {
		t.Fatal("missing mise/config")
	}
	if s.HasHostLayer() {
		t.Errorf("mise/config now declares a host layer (%q) — re-check the ⚠ footer's "+
			"wording against the surface set it is printed for", s.HostSource)
	}
	if surfaceMode(s) != "capture" {
		t.Fatalf("mise/config mode = %q, want capture — it is only in scope for the "+
			"footer while it carries a capture overlay", surfaceMode(s))
	}
	var computed bool
	for _, k := range agentcfg.CoreComputedSurfaces() {
		if k == s.Key() {
			computed = true
		}
	}
	if !computed {
		t.Error("mise/config is no longer a core computed surface — the footer's " +
			"`computed` exception was written for exactly this case")
	}
}

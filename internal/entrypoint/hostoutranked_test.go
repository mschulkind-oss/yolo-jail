package entrypoint

// hostoutranked_test.go covers finding F4: an overlay key the OWNER outranks must be named,
// with its cause, and must not be reported as though the overlay won.
//
// The mechanism under test is deliberately NOT changed by any of this — config-overlay folds
// below the owner's managed layer (§5), and at the host notch the guarded autonomy posture
// folds into that layer, which is the jail-bypass-leak fix. What these pin is the REPORT: the
// loss is stated policy rather than a silent one, and — the case that catches an over-broad
// fix — an overlay key that actually wins is still reported as winning.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// outrankedFor returns the Outranked lines for one surface, joined for substring assertions.
func outrankedFor(t *testing.T, results []HostRenderResult, surface string) string {
	t.Helper()
	for _, r := range results {
		if r.Surface == surface {
			return strings.Join(r.Outranked, "\n")
		}
	}
	t.Fatalf("no result for surface %q: %+v", surface, results)
	return ""
}

// THE FIELD-REPORT CASE, verbatim: the shipped `claude` pack plus TWO overlay packs both
// setting permissions.defaultMode: acceptEdits. The host render lands "default" (the guarded
// posture owns the key), and the report must name the key, name both contributors, and name
// the posture as the cause.
func TestHostRenderNamesOutrankedAutonomyKey(t *testing.T) {
	home := t.TempDir()
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	p1 := claudeSettingsOverlay(t, "p1", map[string]any{
		"permissions": map[string]any{"defaultMode": "acceptEdits"},
	})
	p2 := claudeSettingsOverlay(t, "p2", map[string]any{
		"permissions": map[string]any{"defaultMode": "acceptEdits"},
	})
	// autonomy=false, matching applyHost: the host notch renders the guarded posture.
	overlays := packoverlay.Collect([]*packload.Pack{claude, p1, p2}, false, nil)

	results, rerr := RenderHostPack(claude, home, false, overlays)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	got := outrankedFor(t, results, "claude/settings")
	for _, want := range []string{
		"permissions.defaultMode", // the key
		"IGNORED",                 // that it did not take effect
		"p1", "p2",                // both contributors
		"autonomy", // and the cause: the guarded posture, not a bug
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the outranked report is missing %q — a user cannot tell a policy loss "+
				"from a bug without it:\n%s", want, got)
		}
	}

	// And the mechanism is unchanged: the file still carries the guarded value.
	data, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read host settings: %v", err)
	}
	if !strings.Contains(string(data), `"default"`) || strings.Contains(string(data), "acceptEdits") {
		t.Errorf("the host render must still land the GUARDED value — this fix is reporting "+
			"only:\n%s", data)
	}
}

// THE MISLEADING WARNING, which is the half of F4 that made the output worse than silent: the
// ⚠ overwrite line must NOT attribute the clobber to an overlay whose value never reached the
// file. The managed layer that actually wrote it is still reported, unlabelled.
func TestHostRenderDoesNotBlameAnOutrankedOverlayForTheOverwrite(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	// The user already has a THIRD value, so the managed write is a genuine overwrite.
	if err := os.WriteFile(settings,
		[]byte(`{"permissions":{"defaultMode":"plan"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatalf("embedded claude: %v", err)
	}
	pushy := claudeSettingsOverlay(t, "pushy", map[string]any{
		"permissions": map[string]any{"defaultMode": "acceptEdits"},
	})
	overlays := packoverlay.Collect([]*packload.Pack{claude, pushy}, false, nil)

	// Observe: the report must be honest BEFORE anything is written.
	results, rerr := RenderHostPack(claude, home, true, overlays)
	if rerr != nil {
		t.Fatalf("RenderHostPack observe: %v", rerr)
	}
	var overwrites string
	for _, r := range results {
		if r.Surface == "claude/settings" {
			overwrites = strings.Join(r.Overwrites, "; ")
		}
	}
	if strings.Contains(overwrites, "pushy") {
		t.Errorf("the overwrite warning blames the outranked overlay `pushy` for a value it "+
			"never wrote — that is what made the loss read as a WIN: %q", overwrites)
	}
	if !strings.Contains(overwrites, "permissions.defaultMode") {
		t.Errorf("the managed layer DID overwrite the user's value and must still be "+
			"reported: %q", overwrites)
	}
}

// THE NEGATIVE CASE, which is the one that catches an over-broad fix: an overlay key the
// owner does not manage still WINS, must not appear as IGNORED, and must still show up in the
// overwrite warning attributed to its pack.
func TestHostRenderDoesNotCallAWinningOverlayKeyIgnored(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".acme")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"fileSuggestion":"my-own-choice"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	owner := overlayOwnerPack(t, "")
	// fileSuggestion: the owner manages nothing of the sort, so the overlay wins.
	// telemetry: the owner's own managed key, so the overlay loses.
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{
		"fileSuggestion": "run-fzf", "telemetry": true,
	})
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false, nil)

	results, err := RenderHostPack(owner, home, false, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	outranked := outrankedFor(t, results, "acme/settings")
	if strings.Contains(outranked, "fileSuggestion") {
		t.Errorf("a WINNING overlay key was reported as IGNORED — the fix is over-broad and "+
			"now cries wolf on every contribution:\n%s", outranked)
	}
	if !strings.Contains(outranked, "telemetry") {
		t.Errorf("the contested key `telemetry` must still be named as outranked:\n%s", outranked)
	}
	// The winning key is still warned about, and still attributed to its pack.
	var overwrites string
	for _, r := range results {
		if r.Surface == "acme/settings" {
			overwrites = strings.Join(r.Overwrites, "; ")
		}
	}
	if !strings.Contains(overwrites, "fileSuggestion") || !strings.Contains(overwrites, "acme-fzf") {
		t.Errorf("a winning overlay that clobbers a user value must still warn, naming the "+
			"pack: %q", overwrites)
	}
	// And the file proves who won each key.
	got := readRenderedJSON(t, home, ".acme/settings.json")
	if got["fileSuggestion"] != "run-fzf" || got["telemetry"] != false {
		t.Errorf("precedence changed — this fix is reporting only: %#v", got)
	}
}

// A REDUNDANT overlay — declaring exactly the value the managed layer asserts — is not
// IGNORED: the key lands on what the pack asked for. Calling it a loss would send someone
// hunting a problem that does not exist.
func TestHostRenderRedundantOverlayKeyIsNotReportedAsIgnored(t *testing.T) {
	home := t.TempDir()
	owner := overlayOwnerPack(t, "") // manages telemetry=false
	agreeable := overlayContributorPack(t, "agreeable", map[string]any{"telemetry": false})
	overlays := packoverlay.Collect([]*packload.Pack{owner, agreeable}, false, nil)

	results, err := RenderHostPack(owner, home, false, overlays)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	if got := outrankedFor(t, results, "acme/settings"); got != "" {
		t.Errorf("an overlay agreeing with the managed value is redundant, not ignored:\n%s", got)
	}
}

// A NESTED SIBLING is not collateral damage: an overlay contributing a sibling key under a
// parent the owner also manages keeps its sibling, so only the contested LEAF is reported.
func TestHostRenderOutrankedIsPerLeafNotPerBranch(t *testing.T) {
	home := t.TempDir()
	surface, err := json.Marshal([]any{map[string]any{
		"agent": "acme", "name": "settings", "codec": "json",
		"path": "~/.acme/settings.json", "mode": "rmw",
		"managed": map[string]any{"perms": map[string]any{"deny": []any{"owners-value"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	owner := &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{{Kind: packdecl.KindConfig, Raw: surface}},
	}}
	contributor := overlayContributorPack(t, "acme-fzf", map[string]any{
		"perms": map[string]any{
			"allow": []any{"contributed"}, // a sibling the owner never names — wins
			"deny":  []any{"CONTESTED"},   // the owner's key — loses
		},
	})
	overlays := packoverlay.Collect([]*packload.Pack{owner, contributor}, false, nil)

	results, rerr := RenderHostPack(owner, home, false, overlays)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	got := outrankedFor(t, results, "acme/settings")
	if !strings.Contains(got, "perms.deny") {
		t.Errorf("the contested LEAF must be named by its dotted path:\n%s", got)
	}
	if strings.Contains(got, "perms.allow") {
		t.Errorf("the overlay's surviving sibling was reported as ignored:\n%s", got)
	}
}

// No overlays anywhere: the field stays empty, so the quiet-by-default half of R3 is not
// traded away for this line.
func TestHostRenderWithNoOverlaysReportsNothingOutranked(t *testing.T) {
	home := t.TempDir()
	results, err := RenderHostPack(overlayOwnerPack(t, ""), home, false, nil)
	if err != nil {
		t.Fatalf("RenderHostPack: %v", err)
	}
	if got := outrankedFor(t, results, "acme/settings"); got != "" {
		t.Errorf("a pack set with no overlays must report nothing outranked:\n%s", got)
	}
}

// claudeSettingsOverlay is an overlay pack targeting the SHIPPED claude/settings surface —
// the field report's exact shape, so the autonomy interaction is exercised rather than
// simulated with a synthetic posture.
func claudeSettingsOverlay(t *testing.T, name string, managed map[string]any) *packload.Pack {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"managed": managed})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: name, Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfigOverlay, Surface: "claude/settings", Raw: raw},
		},
	}}
}

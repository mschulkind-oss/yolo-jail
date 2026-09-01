package entrypoint

// packfoldnote_test.go pins the REPORT at both render paths, which is the part a packload
// unit test cannot see: a note that packload computes and no render path prints is the
// finding again, one layer down. So these drive the production entries — the boot loop and
// the host render — and assert on what the user is told, which is what fails if the emission
// site is deleted (the unpinned-callee class this review's finding #2 is about).
//
// The bug they guard: `foldPostureManaged` computed which patches named no base surface and
// threw that away, so a patch written for a claude surface and moved into a pack owning no
// claude surface (the OQ-Z5 shape) folded nowhere and looked to its author like a patch that
// had worked.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// foldNotePack is a pack that installs `claude`, declares ONE surface, and carries a config
// patch under the named profile — targeting `claude/setings` when typo is true (the review's
// verification, letter for letter) and the real surface when it is false.
func foldNotePack(t *testing.T, typo bool) *packload.Pack {
	t.Helper()
	name := "settings"
	if typo {
		name = "setings"
	}
	patch, err := json.Marshal([]any{map[string]any{
		"agent": "claude", "name": name, "codec": "json",
		"path": "~/.claude/settings.json", "managed": map[string]any{"profile": "yes"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	base, err := json.Marshal([]any{map[string]any{
		"agent": "claude", "name": "settings", "codec": "json",
		"path": "~/.claude/settings.json", "managed": map[string]any{"base": "surface"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindProgram, Bin: "claude", Via: "npm", Package: "@acme/claude"},
			{Kind: packdecl.KindConfig, Raw: base},
			{Kind: packdecl.KindProfile, Name: "bedrock", Raw: patch},
		},
	}}
}

// THE JAIL PATH: a selected variant whose patch names nothing the pack declares is reported
// on the boot's stderr, by target and by what the pack does declare — and never as a
// generator failure, because the render succeeded and the patch was merely inert.
func TestJailRenderWarnsOnProfilePatchNamingNoSurface(t *testing.T) {
	for _, c := range []struct {
		typo  bool
		want  bool
		label string
	}{
		{true, true, "a dead patch"},
		{false, false, "a patch that folds"},
	} {
		e, errw := overlayRenderEnv(t)
		e.Vars["YOLO_PACK_PROFILES"] = `{"claude":"bedrock"}`
		ConfigurePackSurfaces(e, []*packload.Pack{foldNotePack(t, c.typo)})
		if fails := e.GenFailures(); len(fails) != 0 {
			t.Fatalf("%s: an inert patch is not a render failure: %v", c.label, fails)
		}
		if got := errw.String(); strings.Contains(got, "claude/setings") != c.want {
			t.Errorf("%s: want warning=%v, boot said:\n%s", c.label, c.want, got)
		}
	}
}

// THE HOST PATH: `yolo host apply` passes no profile table — a variant is a launch decision
// — so the dead patch it CAN see is a posture's, and it reaches the user through the result
// (the host Env has no Stderr; see tableLosses). A row that says nothing was written, for an
// identity the pack does not declare.
func TestHostRenderReportsPatchNamingNoSurface(t *testing.T) {
	for _, c := range []struct {
		typo  bool
		want  bool
		label string
	}{
		{true, true, "a dead patch"},
		{false, false, "a patch that folds"},
	} {
		p := posturePatchPack(t, c.typo)
		results, err := RenderHostPack(p, t.TempDir(), true, nil)
		if err != nil {
			t.Fatalf("%s: RenderHostPack: %v", c.label, err)
		}
		var found bool
		for _, r := range results {
			if r.Surface != "claude/setings" {
				continue
			}
			found = true
			if !strings.HasPrefix(r.Action, "ignored:") {
				t.Errorf("%s: the row must say nothing was written, got %q", c.label, r.Action)
			}
		}
		if found != c.want {
			t.Errorf("%s: want a result row for the unmatchable identity=%v, got %+v",
				c.label, c.want, results)
		}
	}
}

// posturePatchPack is a pack whose GUARDED posture patches `claude/setings` (typo) or the
// surface it really declares. The host notch renders the guarded posture, which is what makes
// this the case a host apply can see.
func posturePatchPack(t *testing.T, typo bool) *packload.Pack {
	t.Helper()
	name := "settings"
	if typo {
		name = "setings"
	}
	patch := func(agent, name string) json.RawMessage {
		raw, err := json.Marshal([]any{map[string]any{
			"agent": agent, "name": name, "codec": "json",
			"path": "~/.claude/settings.json", "managed": map[string]any{"auto": "yes"},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	return &packload.Pack{Name: "acme", Decl: &packdecl.Manifest{
		Contributes: []packdecl.Contribution{
			{Kind: packdecl.KindConfig, Raw: patch("claude", "settings")},
			{Kind: packdecl.KindAutonomy,
				Autonomous: &packdecl.AutonomyPosture{Config: patch("claude", "settings")},
				Guarded:    &packdecl.AutonomyPosture{Config: patch("claude", name)}},
		},
	}}
}

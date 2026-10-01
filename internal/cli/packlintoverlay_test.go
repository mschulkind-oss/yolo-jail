package cli

// packlintoverlay_test.go pins `yolo pack lint` to the RENDER's overlay decode
// (docs/design/synced-skill-trees-plan.md, "Two defects this measurement found"). Lint used to
// check an overlay only for packdecl's shape (a surface named, a body present), so a body the
// render refuses by name lint-passed with "✓ pack ok" and a claim line saying it "contributes
// keys", then contributed nothing at the next apply. The overlay is the remedy yolo's own loss
// message tells a user to write, so a green lint on a misspelled one is the loss anyway.

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
)

// Every overlay the render's collector refuses, lint refuses, printing each of the collector's
// own lines exactly once. The first four cases are a body whose ONLY content is `defaults` —
// the spelling the reviewer reproduced against the claude/settings remedy — in each of the
// declarations the collector decodes as an overlay: a config-overlay, a profile-gated one
// (decoded whatever profile is active, so lint holding no profile table must not skip it), and
// an autonomy posture's `config` entry on a surface its pack does not declare (a posture
// overlay), under each posture.
//
// The two "beside a surface of its own" cases are the ones only BOTH postures catch. A posture
// overlay that is not a well-formed surface entry at all, in a pack that declares a surface of
// its own, is left by the collector to the posture fold at the posture it selects, so one
// collection at that posture reports nothing and lint has to ask at the other one too.
//
// The last two are a target that is no "agent/name" identity, which packdecl checks only for
// presence: the collector refuses it for a config-overlay and a config-list alike.
func TestPackLintRefusesAnOverlayBodyTheRenderRefuses(t *testing.T) {
	const defaultsRule = `may not set "defaults"`
	const strictRule = `unknown field "manged"`
	const targetRule = `is not an "agent/name" identity`
	posture := func(which, body string) string {
		return `{"kind":"autonomy","` + which + `":{"config":[{"agent":"pi","name":"settings",` +
			`"codec":"json","path":"~/.pi/agent/settings.json",` + body + `}]}}`
	}
	const ownSurface = `{"kind":"config","config":[{"agent":"local","name":"own","codec":"json",` +
		`"path":"~/.local-own.json","managed":{"a":1}}]},`
	for _, c := range []struct{ name, contributions, rule string }{
		{"config-overlay", `{"kind":"config-overlay","surface":"claude/settings",` +
			`"config":{"defaults":{"enabledPlugins":{"my-own-plugin@mkt":true}}}}`, defaultsRule},
		{"profile-gated config-overlay", `{"kind":"config-overlay","surface":"claude/settings",` +
			`"profile":"zai","config":{"defaults":{"env":{"K":"v"}}}}`, defaultsRule},
		{"guarded posture overlay", posture("guarded", `"defaults":{"x":1}`), defaultsRule},
		{"autonomous posture overlay", posture("autonomous", `"defaults":{"x":1}`), defaultsRule},
		{"guarded posture overlay beside a surface of its own",
			ownSurface + posture("guarded", `"manged":{"x":1}`), strictRule},
		{"autonomous posture overlay beside a surface of its own",
			ownSurface + posture("autonomous", `"manged":{"x":1}`), strictRule},
		{"config-overlay on no identity", `{"kind":"config-overlay","surface":"claudesettings",` +
			`"config":{"managed":{"x":1}}}`, targetRule},
		{"config-list on no identity", `{"kind":"config-list","surface":"claudesettings",` +
			`"path":"/permissions/allow","add":["Bash(ls)"]}`, targetRule},
	} {
		t.Run(c.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "local")
			writeFile(t, filepath.Join(dir, "pack.json"),
				`{"name":"local","contributes":[`+c.contributions+`]}`)

			// What the render refuses, from the render's own collector over this pack at both
			// notches' postures. Asserted non-empty and naming the rule, so this case cannot
			// pass vacuously against a collector that stopped refusing the body.
			p, problems := packload.LoadDir(dir, "local")
			if len(problems) != 0 {
				t.Fatalf("fixture manifest does not decode clean: %v", problems)
			}
			var refused []string
			seen := map[string]bool{}
			for _, autonomy := range []bool{false, true} {
				for _, prob := range packoverlay.Collect([]*packload.Pack{p}, autonomy, nil).Problems {
					if !seen[prob] {
						seen[prob] = true
						refused = append(refused, prob)
					}
				}
			}
			if !strings.Contains(strings.Join(refused, "\n"), c.rule) {
				t.Fatalf("the render's collector no longer refuses this body with %q, so the "+
					"case pins nothing: %v", c.rule, refused)
			}

			var out, errw bytes.Buffer
			rc := packMain([]string{"lint", dir}, &out, &errw, false)
			got := out.String()
			if rc == 0 || strings.Contains(got, "pack ok") {
				t.Fatalf("lint passed an overlay the render refuses (rc=%d):\n%s", rc, got)
			}
			// Exactly once: a refusal both postures report is one mistake, not two lines.
			for _, want := range refused {
				if n := strings.Count(got, want); n != 1 {
					t.Errorf("lint printed the render's refusal %q %d times, want once:\n%s",
						want, n, got)
				}
			}
		})
	}
}

// The other direction, so the fix cannot be "fail every overlay": a well-formed overlay, a
// profile-gated one and a posture overlay, all on surfaces this pack does not own, lint clean.
// From a one-pack view each is OWNERLESS, which the render reports as inert rather than as a
// problem (R2: a pack the user did not select is not an error), and lint knows no selection.
func TestPackLintPassesAnOverlayTheRenderAccepts(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "local")
	writeFile(t, filepath.Join(dir, "pack.json"), `{"name":"local","contributes":[`+
		`{"kind":"config-overlay","surface":"claude/settings",`+
		`"config":{"managed":{"enabledPlugins":{"my-own-plugin@mkt":true}}}},`+
		`{"kind":"config-overlay","surface":"claude/settings","profile":"zai",`+
		`"config":{"managed":{"env":{"K":"v"}}}},`+
		`{"kind":"autonomy","guarded":{"config":[{"agent":"pi","name":"settings",`+
		`"codec":"json","path":"~/.pi/agent/settings.json","managed":{"x":1}}]}}]}`)

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc != 0 {
		t.Fatalf("lint refused overlays the render accepts (rc=%d):\n%s%s",
			rc, out.String(), errw.String())
	}
}

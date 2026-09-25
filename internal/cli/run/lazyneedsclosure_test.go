package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// lazyNeedsFixture points HOME at a scratch config selecting only `claude` — whose manifest
// NEEDS openai-auth — plus a conventional local pack superseding openai-auth's capability,
// and clears both process-wide records so the LAZY resolvers are what answer.
func lazyNeedsFixture(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME"} {
		t.Setenv(v, "")
	}
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	local := filepath.Join(cfgDir, "local")
	if err := os.MkdirAll(local, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.jsonc"),
		[]byte(`{"packs": ["claude"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "pack.json"), []byte(
		`{"name":"local","supersedes":[{"capability":"openai-oauth-refresh",`+
			`"because":"this fixture ships its own OpenAI refresh path"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	loopholes.ResetPackModules()
	loopholes.ResetPackSupersessions()
	t.Cleanup(loopholes.ResetPackModules)
	t.Cleanup(loopholes.ResetPackSupersessions)
}

// THE LAZY RESOLVERS MUST APPLY THE NEEDS CLOSURE, or `yolo check` contradicts the launch it
// describes. stagePacks extends the selection with packload.ResolveNeeds before recording
// the pack modules and supersessions; the lazy fallbacks behind `yolo check`, `yolo loopholes
// list` and config validation used to iterate config.LoadPacks alone.
//
// Measured before the fix, with `"packs": ["claude"]` and a local pack superseding
// openai-oauth-refresh: the Loopholes section listed only claude-oauth-broker, and the
// supersession — which the launch honors, because the staged set includes openai-auth — was
// graded `[WARN] pack supersession matched no served capability`, sending the user to fix a
// pack that was right.
func TestLazyResolversApplyTheNeedsClosure(t *testing.T) {
	lazyNeedsFixture(t)

	found := false
	for _, m := range resolvePackLoopholeModules() {
		if filepath.Base(m.Dir) == "openai-auth-broker" {
			found = true
		}
	}
	if !found {
		t.Error("the lazy module resolver omitted openai-auth-broker, which packs/claude's " +
			"`needs` pulls into every launch selecting claude")
	}

	_, set := loopholes.ValidateSet()
	if probs := set.SupersessionProblems(); len(probs) != 0 {
		t.Errorf("a supersession of a capability served by a needs-pulled pack was reported "+
			"as matching nothing — the launch honors it:\n%s", strings.Join(probs, "\n"))
	}
	if lp, ok := set.Lookup("openai-auth-broker"); !ok || !lp.Superseded() {
		t.Error("openai-auth-broker is not superseded in the lazily resolved set, but the " +
			"launch supersedes it")
	}
}

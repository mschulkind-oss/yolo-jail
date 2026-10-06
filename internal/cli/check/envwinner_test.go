package check

// envwinner_test.go pins the env-override prediction's reading of THE ONE ORDERED COMPOSITION
// (packload's envcompose.go): `yolo check` asks the composition which source delivers a name, as
// the launch's deliverySource does, so an env_sources null that takes a pack's value out of every
// process leaves nothing to override, and an env_sources value over a pack's names env_sources.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// tokenPack is a second local pack whose static env sets the overriding WIDGET_TOKEN for every
// process.
func tokenPack(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(`{"name": "tokenpack", "contributes": [
    {"kind": "env", "vars": {"`+widgetToken+`": "pack-token"}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSectionPacksReadsTheCompositionsWinner(t *testing.T) {
	packsFixture(t, `{"packs": ["file://`+overriddenPack(t, "someagent", "gatedprofile")+`", "file://`+tokenPack(t)+`"]}`)
	predict := func(entry *jsonx.OrderedMap) (int, string) {
		merged := useProfiles("someagent", "gatedprofile")
		if entry != nil {
			merged.Set("env_sources", []any{entry})
		}
		var buf bytes.Buffer
		r := &reporter{w: &buf}
		(&Options{Workspace: t.TempDir(), Getenv: func(string) string { return "" }}).sectionPacks(r, merged)
		return r.failed, buf.String()
	}

	// The pack's token beside the pointer it overrides: refused, naming the pack env.
	if failed, out := predict(nil); failed == 0 ||
		!strings.Contains(out, widgetToken+" is delivered by "+packload.FromPackEnv) {
		t.Errorf("a pack's token beside the pointer must FAIL naming the pack env (failed=%d):\n%s", failed, out)
	}
	// A dotenv value of the same name beats the pack's, and the prediction names env_sources.
	assigned := jsonx.NewOrderedMap()
	assigned.Set(widgetToken, "es-token")
	if failed, out := predict(assigned); failed == 0 ||
		!strings.Contains(out, widgetToken+" is delivered by "+packload.FromEnvSources) {
		t.Errorf("env_sources beats the pack env, so the prediction must name it (failed=%d):\n%s", failed, out)
	}
	// A null removes the pack's token from every process, so nothing overrides the pointer.
	removed := jsonx.NewOrderedMap()
	removed.Set(widgetToken, nil)
	if failed, out := predict(removed); failed != 0 || strings.Contains(out, widgetToken) {
		t.Errorf("a null takes the pack's token out, so `check` must predict no override (failed=%d):\n%s", failed, out)
	}
}

// THE PREDICTION AND THE LAUNCH'S SHADOW LINE NAME ONE WINNER (notch-convergence NC-D76).
// `yolo check` prints no shadow line, since that disclosure is a launch's and check cannot see a
// profile's half of it, but where it predicts an override on a name env_sources shadows, it names
// env_sources: the winner the launch's line names, read off the composition check composes
// (overrideScope).
func TestThePredictionNamesTheWinnerTheShadowLineNames(t *testing.T) {
	widget, token := overriddenPack(t, "someagent", "gatedprofile"), tokenPack(t)
	packsFixture(t, `{"packs": ["file://`+widget+`", "file://`+token+`"]}`)
	merged := useProfiles("someagent", "gatedprofile")
	assigned := jsonx.NewOrderedMap()
	assigned.Set(widgetToken, "es-token")
	merged.Set("env_sources", []any{assigned})
	ws := t.TempDir()

	var buf bytes.Buffer
	r := &reporter{w: &buf}
	(&Options{Workspace: ws, Getenv: func(string) string { return "" }}).sectionPacks(r, merged)
	out := buf.String()
	if !strings.Contains(out, widgetToken+" is delivered by "+packload.FromEnvSources) {
		t.Errorf("the prediction must name env_sources, the source that wins WIDGET_TOKEN:\n%s", out)
	}
	if strings.Contains(out, "Shadowed") {
		t.Errorf("`yolo check` printed a launch's shadow line:\n%s", out)
	}

	var packs []*packload.Pack
	for _, f := range []struct{ name, dir string }{{"widgetpack", widget}, {"tokenpack", token}} {
		p, problems := packload.LoadDir(f.dir, f.name)
		if len(problems) != 0 {
			t.Fatalf("loading %s: %v", f.name, problems)
		}
		packs = append(packs, p)
	}
	scope := overrideScope(packs, merged, packload.ServedDaemons{}, ws, func(string) {},
		func() (map[string]packload.UserProfile, error) { return nil, nil })
	want := "Shadowed " + widgetToken + ": your env_sources value wins over the tokenpack pack's value"
	if lines := scope.ShadowLines(nil); len(lines) != 1 || lines[0] != want {
		t.Errorf("the launch's line over check's composition = %q, want %q", lines, want)
	}
	if e, ok := scope.Delivered(widgetToken); !ok || e.Origin != packload.FromEnvSources {
		t.Errorf("check's composition must deliver WIDGET_TOKEN from env_sources: %+v %v", e, ok)
	}
}

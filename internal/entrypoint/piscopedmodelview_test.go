package entrypoint

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// pi named in the audience must render exactly what an unscoped only renders (design 3.4).
// A gateway provider pi has no built-in for; its `default` alias names an id the only drops.
func renderPiScopedDefault(t *testing.T, scope string) (settings, models []byte, env map[string]string) {
	t.Helper()
	packs := testPacksForAgent(t, "pi")
	const provider = `{"kind":"provider","name":"gw",
	   "endpoints":{"openai":{"base_url":"https://gw.example/v1","wire_api":"openai-chat-completions"}},
	   "models":{"default":"beta-2","alpha-1":"alpha-1","beta-2":"beta-2"}}`
	only := `"only":["alpha-1"]`
	contributes := provider + `,{"kind":"models","provider":"gw",` + only + `}`
	if scope == "pi" {
		contributes = provider + `,{"kind":"models","provider":"gw",` + only + `,"agents":["pi"]}`
	}
	packs = append(packs, companyModelsPack(t, contributes))
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveShippedProfiles(packs, map[string]packload.UserProfile{"menu": {Provider: "gw"}}, providers)
	if err != nil {
		t.Fatal(err)
	}
	use := map[string]string{"pi": "menu"}
	errw := &bytes.Buffer{}
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_USE_PROFILES": useJSON(t, use),
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	withCtxRoot(t, t.TempDir(), "pi")
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("pi boot surface render failed (%s): %v\n%s", scope, fails, errw.String())
	}
	vars, err := packload.AgentEnv(packs, providers, use, "pi", "menu", func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	env = map[string]string{}
	for _, v := range vars {
		if !v.Unset {
			env[v.Key] = v.Value
		}
	}
	return readRenderedBytes(t, filepath.Join(e.Home, ".pi", "agent", "settings.json")),
		readRenderedBytes(t, piModelsPath(e.Home)), env
}

func TestPiScopedOnlyRendersLikeUnscopedWithExcludedDefault(t *testing.T) {
	ss, sm, se := renderPiScopedDefault(t, "pi")
	us, um, ue := renderPiScopedDefault(t, "all")
	if se["YOLO_MODEL_DEFAULT"] != ue["YOLO_MODEL_DEFAULT"] {
		t.Errorf("host role differs: scoped=%v unscoped=%v", se, ue)
	}
	if !bytes.Equal(ss, us) {
		t.Errorf("pi/settings differ:\nscoped=%s\nunscoped=%s", ss, us)
	}
	if !bytes.Equal(sm, um) {
		t.Errorf("pi/models differ:\nscoped=%s\nunscoped=%s", sm, um)
	}
}

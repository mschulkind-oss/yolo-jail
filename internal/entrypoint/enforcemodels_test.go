package entrypoint

// enforcemodels_test.go pins the profile's model-list ENFORCEMENT SWITCH
// (docs/design/model-lists-and-pickers.md MM-D5; `enforce_models`, OQ-WG3's switch) from the
// user's config to what both derive paths see as ctx.enforce_models: lowered by
// config.LoadProfiles into packload.UserProfile, resolved user-over-pack by
// ResolveProfiles, carried across as YOLO_PROFILES (ProfilesWireTable, then LoadProfiles
// here), and handed to the surface derive (ConfigurePackSurfaces) and the env derive
// (packload.AgentEnv). Each hop is a call site that can be deleted, so the fixture derive
// reports the value on both paths and the test reads what it reported.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

const enforceDeriveLua = `yolo.derive("acme", "settings", function(ctx)
  return { enforce_models = tostring(ctx.enforce_models) }
end)
yolo.env("acme", function(ctx)
  return { ACME_ENFORCE_MODELS = tostring(ctx.enforce_models) }
end)
`

func enforceAcmePack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"provider","name":"gw","endpoints":{"openai":{"base_url":"https://gw.example/v1"}},
	   "models":{"m-1":"m-1"}},
	  {"kind":"profile","name":"gw","provider":"gw"},
	  {"kind":"profile","name":"gw-open","provider":"gw","enforce_models":false},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	   "path":"~/.acme/settings.json","mode":"computed"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"), enforceDeriveLua)
	p, problems := packload.LoadDir(dir, "acme")
	if p == nil || len(problems) != 0 {
		t.Fatalf("the acme fixture did not load: %v", problems)
	}
	return p
}

// renderEnforce resolves the profiles (the pack's, then user's), renders the surface through
// the boot loop and composes the env, with `profile` active for acme.
func renderEnforce(t *testing.T, user map[string]packload.UserProfile, profile string) (surface, env string) {
	t.Helper()
	packs := []*packload.Pack{enforceAcmePack(t)}
	providers, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, user, providers)
	if err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Stderr: &errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_USE_PROFILES": `{"acme":"` + profile + `"}`,
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
	}}
	withCtxRoot(t, t.TempDir(), "acme")
	ConfigurePackSurfaces(e, packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	data, err := os.ReadFile(filepath.Join(e.Home, ".acme", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	surface, _ = got["enforce_models"].(string)
	vars, err := packload.AgentEnv(packs, providers, map[string]string{"acme": profile}, "acme", profile,
		func(string) (string, bool) { return "", false }, packload.WithResolvedProfiles(resolved))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range vars {
		if v.Key == "ACME_ENFORCE_MODELS" {
			env = v.Value
		}
	}
	return surface, env
}

func TestTheModelEnforcementSwitchReachesBothDerivePaths(t *testing.T) {
	off, on := false, true
	for _, tc := range []struct {
		name    string
		user    map[string]packload.UserProfile
		profile string
		want    string
	}{
		{"a profile that says nothing is enforced", nil, "gw", "true"},
		{"a pack's profile switches it off", nil, "gw-open", "false"},
		{"a user's profile switches it off", map[string]packload.UserProfile{
			"mine": {Provider: "gw", EnforceModels: &off}}, "mine", "false"},
		{"the user's value wins over the pack's", map[string]packload.UserProfile{
			"gw-open": {Provider: "gw", EnforceModels: &on}}, "gw-open", "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			surface, env := renderEnforce(t, tc.user, tc.profile)
			if surface != tc.want || env != tc.want {
				t.Errorf("ctx.enforce_models = %q on the surface path and %q on the env path, want %q on both",
					surface, env, tc.want)
			}
		})
	}
}

package entrypoint

// profilesctx_test.go pins the JAIL half of YOLO_PROFILES: the parse (LoadProfiles) and
// the SURFACE derive path's exposure of it as ctx.profile.
//
// Same trap surfacederiveselection_test.go names, one field over: the exposure lives at a
// call site (deriveComputedLayer handing activeProfileOptions' map to the derive ctx), so
// nothing else fails if that hand-off is deleted — the derive still runs, ctx.profile
// reads as nil in Lua, and every derive that indexes it writes nothing, silently. So the
// fixture derive reports what its ctx held, and the assertions are on the values.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// profileDeriveLua reports the resolved option map exactly as the ctx carried it: the two
// option spellings a provider declares (one defaulted, one not), whether the field is a
// table at all, and how many keys it held. The count is the assertion that catches a map
// that arrived non-nil but empty when it should have held something.
const profileDeriveLua = `yolo.derive("acme", "settings", function(ctx)
  local n = 0
  if type(ctx.profile) == "table" then
    for _ in pairs(ctx.profile) do n = n + 1 end
  end
  return {
    profile_model = (ctx.profile and ctx.profile.model) or "",
    profile_thinking = (ctx.profile and ctx.profile.thinking) or "",
    profile_is_table = type(ctx.profile) == "table" and "yes" or "no",
    profile_keys = tostring(n),
  }
end)
`

// profileAcmePack installs the acme bin and owns one computed surface, so the boot loop
// renders it and the derive's report is the file's whole content.
func profileAcmePack(t *testing.T) *packload.Pack {
	t.Helper()
	dir := t.TempDir()
	writeHostFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","description":"d","contributes":[
	  {"kind":"program","bin":"acme","via":"npm","package":"acme"},
	  {"kind":"config","config":[{"agent":"acme","name":"settings","codec":"json",
	   "path":"~/.acme/settings.json","mode":"computed"}]}]}`)
	writeHostFile(t, filepath.Join(dir, "derive.lua"), profileDeriveLua)
	p, problems := packload.LoadDir(dir, "acme", false)
	if p == nil {
		t.Fatalf("the acme fixture did not load: %v", problems)
	}
	return p
}

// renderAcmeProfile drives the boot loop with the given wire tables and parses the
// rendered settings file.
func renderAcmeProfile(t *testing.T, useProfiles, profiles string) map[string]any {
	t.Helper()
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{
		"YOLO_USE_PROFILES": useProfiles,
		"YOLO_PROFILES":     profiles,
	}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "acme")

	ConfigurePackSurfaces(e, []*packload.Pack{profileAcmePack(t)})
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot render failed: %v\n%s", fails, errw.String())
	}
	data, err := os.ReadFile(filepath.Join(e.Home, ".acme", "settings.json"))
	if err != nil {
		t.Fatalf("read rendered surface: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse rendered surface: %v\n%s", err, data)
	}
	return got
}

// The active profile's RESOLVED options reach the derive: the launcher composed them, the
// jail read them off the shared table, and the derive indexes ctx.profile with no nil
// guard — which is only safe if the field is always a table.
func TestSurfaceDeriveReadsTheResolvedProfileOptions(t *testing.T) {
	got := renderAcmeProfile(t, `{"acme":"aws"}`,
		`{"aws": {"provider": "aws-bedrock", "model": "fast", "thinking": "low"}}`)
	if got["profile_model"] != "fast" || got["profile_thinking"] != "low" {
		t.Errorf("ctx.profile carried %v / %v, want the resolved options fast / low",
			got["profile_model"], got["profile_thinking"])
	}
	if got["profile_keys"] != "2" {
		t.Errorf("ctx.profile held %s keys, want 2", got["profile_keys"])
	}
	if got["profile_is_table"] != "yes" {
		t.Errorf("ctx.profile = %v, want a table the derive can index unguarded", got["profile_is_table"])
	}
}

// No profile active, or the active name absent from the resolved table: ctx.profile is
// EMPTY. The table-ness is structural — the ctx builder always writes a `profile` table
// and fills it from the (possibly nil) map — so what these assertions hold the line on is
// the CONTENT: nothing leaks in from another agent's selection or from a name the table
// does not hold, which is the negative space of the case above (OQ-CS2's no-selection
// case: a derive reads "no profile" as an empty map, not as somebody else's).
func TestSurfaceDeriveSeesAnEmptyProfileTableWhenNothingResolves(t *testing.T) {
	for _, tc := range []struct{ use, resolved string }{
		{"", ``},
		{"", `{}`},
		{`{"acme":"aws"}`, ``},
		{`{"acme":"ghost"}`, `{"aws": {"provider": "aws-bedrock", "model": "fast"}}`},
		{`{"claude":"aws"}`, `{"aws": {"provider": "aws-bedrock", "model": "fast"}}`},
	} {
		got := renderAcmeProfile(t, tc.use, tc.resolved)
		if got["profile_model"] != "" || got["profile_thinking"] != "" || got["profile_keys"] != "0" {
			t.Errorf("use=%s resolved=%s: ctx.profile carried model=%v thinking=%v keys=%s, want empty",
				tc.use, tc.resolved, got["profile_model"], got["profile_thinking"], got["profile_keys"])
		}
		if got["profile_is_table"] != "yes" {
			t.Errorf("use=%s resolved=%s: ctx.profile = %v, want an empty table rather than nil",
				tc.use, tc.resolved, got["profile_is_table"])
		}
	}
}

// Two profiles in the table, two agents selecting different ones: the surface reads ITS
// agent's entry. Sharing one table is the point of keying it by profile NAME; reading the
// first entry instead would hand one agent another agent's model.
func TestSurfaceDeriveReadsThisAgentsOwnProfileEntry(t *testing.T) {
	got := renderAcmeProfile(t, `{"acme":"slow","claude":"fast"}`,
		`{"fast": {"provider": "p1", "model": "quick"}, "slow": {"provider": "p2", "model": "steady"}}`)
	if got["profile_model"] != "steady" {
		t.Errorf("ctx.profile.model = %v, want acme's own profile's steady", got["profile_model"])
	}
}

// THE PARSE half: the wire table the launcher serialized comes back as the map the boot
// loop reads, with `provider` lifted out of the options.
func TestLoadProfilesParsesTheResolvedTable(t *testing.T) {
	e := &Env{Vars: map[string]string{
		"YOLO_PROFILES": `{"aws": {"provider": "aws-bedrock", "model": "fast"}, "z": {"provider": "p"}}`,
	}}
	got := e.LoadProfiles()
	if len(got) != 2 {
		t.Fatalf("LoadProfiles = %v, want two entries", got)
	}
	aws := got["aws"]
	if aws.Provider != "aws-bedrock" {
		t.Errorf("aws.Provider = %q, want the entry's provider", aws.Provider)
	}
	if aws.Options["model"] != "fast" || len(aws.Options) != 1 {
		t.Errorf("aws.Options = %v, want exactly {model: fast} — provider is lifted out", aws.Options)
	}
	if p := got["z"]; p.Provider != "p" || len(p.Options) != 0 {
		t.Errorf("z = %+v, want a provider and no options", p)
	}
}

// Absent, empty, undecodable, or not an object: an EMPTY map, never an error and never a
// nil map — an older launcher that emitted no variable composes no profiles, which is the
// same world as a launch that selected none. Junk entries inside a good table are skipped
// rather than fatal: the launcher composed this table, so the jail's job is to read it.
func TestLoadProfilesIsEmptyWhenTheVariableSaysNothing(t *testing.T) {
	for _, raw := range []string{"", `not json`, `[1, 2]`, `{"a": "scalar"}`,
		`{"a": {"provider": "p", "junk": [1]}}`} {
		e := &Env{Vars: map[string]string{"YOLO_PROFILES": raw}}
		got := e.LoadProfiles()
		if got == nil {
			t.Fatalf("YOLO_PROFILES=%q: LoadProfiles returned nil, want an empty map", raw)
		}
		if len(got) > 1 {
			t.Errorf("YOLO_PROFILES=%q: LoadProfiles = %v, want at most one readable entry", raw, got)
		}
	}
	if got := (&Env{}).LoadProfiles(); len(got) != 0 {
		t.Errorf("no variable at all: LoadProfiles = %v, want empty", got)
	}
}

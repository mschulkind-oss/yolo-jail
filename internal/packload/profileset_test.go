package packload

// profileset_test.go pins the ACTIVE SET's core half (docs/design/active-provider-sets.md, OQ-AP1
// to OQ-AP3): the lowering of a list to its primary and its whole set, the set's own refusals,
// and the credential gate over a set (ScopeCredentials, the function both notches call). The
// notches' call sites are pinned where they compose: internal/cli/run (the jail and macos-user),
// internal/cli (the host), internal/entrypoint (the boot render of pi's derive).

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func decodedTable(t *testing.T, body string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := v.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("%s is not an object", body)
	}
	return m
}

// A list is its PRIMARY to every reader written before sets (AP-D1), and its whole set to
// ProfileSets; a null, an empty list or a malformed one selects nothing to both.
func TestAListLowersToItsPrimaryAndItsWholeSet(t *testing.T) {
	m := decodedTable(t, `{"pi":["zai","openrouter"],"claude":"bedrock","codex":null,
		"opencode":[],"copilot":["x",3]}`)
	table := ProfileTable(m)
	if table["pi"] != "zai" || table["claude"] != "bedrock" || len(table) != 2 {
		t.Errorf("ProfileTable = %v, want pi→zai (the primary) and claude→bedrock only", table)
	}
	sets := ProfileSets(m)
	if strings.Join(sets["pi"], ",") != "zai,openrouter" || strings.Join(sets["claude"], ",") != "bedrock" ||
		len(sets) != 2 {
		t.Errorf("ProfileSets = %v, want pi's whole list in order and claude's one entry", sets)
	}
	if got := ProfileSetWire([]string{"zai"}); got != "zai" {
		t.Errorf("a set of one must cross as the plain string (AP-D8), got %#v", got)
	}
	if got, ok := ProfileSetWire([]string{"zai", "openrouter"}).([]any); !ok || len(got) != 2 {
		t.Errorf("a set of two must cross as a list, got %#v", got)
	}
}

// setPack is an agent pack whose program declares provider_sets (or not).
func setPack(t *testing.T, bin string, holdsSets bool) *Pack {
	t.Helper()
	sets := ""
	if holdsSets {
		sets = `,"provider_sets":true`
	}
	return scopePack(t, bin, `{"name":"`+bin+`","contributes":[{"kind":"program","bin":"`+bin+
		`","via":"npm","package":"@acme/`+bin+`","protocols":["openai"]`+sets+`}]}`, "")
}

// The set's own refusals (AP-D3, OQ-AP2, AP-D9), each naming what to change, and a clean set
// of a set-capable agent refused by none of them.
func TestProfileSetProblemsRefuseWhatASetCannotMean(t *testing.T) {
	packs := []*Pack{setPack(t, "pi", true), setPack(t, "claude", false)}
	resolved := map[string]ResolvedProfile{
		"zai": {Provider: "zai"}, "zai-fast": {Provider: "zai"}, "openrouter": {Provider: "openrouter"},
		"pz": {Provider: "zai", Via: "wire-bridge"},
	}
	cases := []struct {
		name string
		sets map[string][]string
		says string
	}{
		{"a clean set", map[string][]string{"pi": {"zai", "openrouter"}}, ""},
		{"a set of one at a single-provider agent", map[string][]string{"claude": {"zai"}}, ""},
		{"an agent this launch does not install", map[string][]string{"codex": {"zai", "openrouter"}}, ""},
		{"a list at a single-provider agent (OQ-AP2)", map[string][]string{"claude": {"zai", "openrouter"}},
			"whose pack does not declare provider_sets, so yolo cannot hand it a list"},
		{"a name listed twice", map[string][]string{"pi": {"zai", "openrouter", "zai"}}, `"zai" is listed twice`},
		{"two entries on one provider", map[string][]string{"pi": {"zai", "zai-fast"}},
			`both resolve to provider "zai"`},
		{"a via entry after the first (AP-D9)", map[string][]string{"pi": {"openrouter", "pz"}},
			"a via profile can sit in a set only as its first entry"},
		{"a via entry first", map[string][]string{"pi": {"pz", "openrouter"}}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			problems := ProfileSetProblems(packs, nil, tc.sets, resolved)
			if tc.says == "" {
				if len(problems) != 0 {
					t.Errorf("refused %v: %v", tc.sets, problems)
				}
				return
			}
			if len(problems) != 1 || !strings.Contains(problems[0], tc.says) {
				t.Errorf("problems = %v, want one saying %q", problems, tc.says)
			}
		})
	}
	// The single-provider refusal names the one-entry spellings that work.
	msg := ProfileSetProblems(packs, nil, map[string][]string{"claude": {"zai", "openrouter"}}, resolved)[0]
	for _, want := range []string{"`-p claude=zai`", `"profile": {"claude": "zai"}`, "drop openrouter"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal must name %q:\n%s", want, msg)
		}
	}
}

// A bare list goes whole to a set-capable agent and its first entry to every other (OQ-AP3,
// config.FoldProfiles), and the one launch line names exactly the agents narrowed and what they
// ignore, with the per-agent spelling of the list where it was written.
func TestABareListNarrowsForSingleProviderAgentsAndSaysSo(t *testing.T) {
	list := []string{"zai", "openrouter"}
	note := BareListNote(list, []string{"pi"}, []string{"codex", "claude"}, false)
	for _, want := range []string{"claude and codex take one profile (their packs do not declare provider_sets)", "on zai alone",
		"ignore openrouter", "pi takes the whole list"} {
		if !strings.Contains(note, want) {
			t.Errorf("the bare-list line must say %q:\n%s", want, note)
		}
	}
	if note := BareListNote(list, []string{"pi"}, nil, false); note != "" {
		t.Errorf("no agent narrowed must say nothing, got %q", note)
	}
	for keyed, want := range map[bool]string{
		false: "(a bare -p, naming no agent)",
		true:  `(the profile key's list, naming no agent)`,
	} {
		if note := BareListNote(list, nil, []string{"claude"}, keyed); !strings.Contains(note, want) {
			t.Errorf("keyed=%v: the line must name its source %q:\n%s", keyed, want, note)
		}
	}
	if note := BareListNote(list, nil, []string{"claude"}, true); !strings.Contains(note,
		`"profile": {"<agent>": ["zai", "openrouter"]}`) {
		t.Errorf("the key's line must spell the per-agent list in the key:\n%s", note)
	}
}

// THE GATE OVER A SET (§4.5): each entry's claimed key reaches the agent holding the set and no
// other process, the agent's derive sees every entry's key, the pre-flight demands them all, and
// the disclosure names them in set order whatever order env_sources hydrated them in.
func TestTheGateDeliversEveryEntrysKeyToItsAgentAlone(t *testing.T) {
	probe := scopePack(t, "probe", `{"name":"probe","contributes":[
	  {"kind":"program","bin":"probe","via":"npm","package":"@acme/probe","protocols":["openai"],
	   "provider_sets":true}]}`,
		`yolo.env("probe", function(ctx)
  local out = {}
  for name, p in pairs(ctx.providers) do
    if p.api_key then out["SAW_" .. string.upper(name)] = p.api_key end
  end
  local names = {}
  for _, e in ipairs(ctx.active_set) do table.insert(names, e.provider) end
  out.SET = table.concat(names, ",")
  return out
end)`)
	scope, err := ScopeCredentials(ScopeInput{
		Packs:     []*Pack{probe},
		Providers: twoProviders(t),
		Profiles:  map[string]string{"probe": "zai-profile"},
		Sets:      map[string][]string{"probe": {"zai-profile", "cerebras-profile"}},
		Resolved: map[string]ResolvedProfile{
			"zai-profile": {Provider: "zai"}, "cerebras-profile": {Provider: "cerebras"},
		},
		// CEREBRAS hydrated first: the disclosure must still name zai's key first.
		EnvSources: hydrated("CEREBRAS_API_KEY", "c", "ZAI_API_KEY", "z", "GH_TOKEN", "g"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.SharedEnvSources().Keys(); strings.Join(got, ",") != "GH_TOKEN" {
		t.Errorf("shared env_sources = %v, want only GH_TOKEN: a set's keys reach its agent alone", got)
	}
	if got := scope.Agent("probe").EnvSources.Keys(); strings.Join(got, ",") != "CEREBRAS_API_KEY,ZAI_API_KEY" {
		t.Errorf("probe's own env_sources = %v, want both entries' keys", got)
	}
	var seen []string
	for _, v := range scope.Agent("probe").Shape {
		seen = append(seen, v.Key+"="+v.Value)
	}
	if strings.Join(seen, ",") != "SAW_CEREBRAS=c,SAW_ZAI=z,SET=zai,cerebras" {
		t.Errorf("probe's env derive saw %v, want both entries' keys and ctx.active_set in order", seen)
	}
	if got := scope.SelectedProviders(); strings.Join(got, ",") != "cerebras,zai" {
		t.Errorf("SelectedProviders = %v, want every entry of the set", got)
	}
	lines := scope.DisclosureWith(DisclosureNotes{})
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "  ZAI_API_KEY (provider zai): probe only") ||
		!strings.HasPrefix(lines[2], "  CEREBRAS_API_KEY (provider cerebras): probe only") {
		t.Errorf("the disclosure must name each key, in set order:\n%s", strings.Join(lines, "\n"))
	}
}

// The credential pre-flight demands every entry's key and names the missing one's position, so
// a set is never started on the entries that happen to have keys (AP-D3).
func TestTheCredentialPreflightNamesTheEntrysPosition(t *testing.T) {
	scope, err := ScopeCredentials(ScopeInput{
		Providers: twoProviders(t),
		Profiles:  map[string]string{"pi": "zai-profile"},
		Sets:      map[string][]string{"pi": {"zai-profile", "cerebras-profile"}},
		Resolved: map[string]ResolvedProfile{
			"zai-profile": {Provider: "zai"}, "cerebras-profile": {Provider: "cerebras"},
		},
		EnvSources: hydrated("ZAI_API_KEY", "z"),
	})
	if err != nil {
		t.Fatal(err)
	}
	facts := ProviderCredentialGapsIn(nil, twoProviders(t), scope, func(string) (string, bool) {
		return "", false
	}, []string{"env_sources"})
	joined := strings.Join(facts, "\n")
	for _, want := range []string{`provider "cerebras"`, "CEREBRAS_API_KEY",
		"profile cerebras-profile is entry 2 of pi's profiles (zai-profile, cerebras-profile)"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the pre-flight must name %q:\n%s", want, joined)
		}
	}
}

// A `platform` gate fires for an agent whose set holds a provider of that platform anywhere, not
// only first (AP-P1): the CLI-less pointer reaches pi on [zai, bedrock-like].
func TestAPlatformGateFiresForAnyEntryOfTheSet(t *testing.T) {
	agent := setPack(t, "pi", true)
	pointer := scopePack(t, "pointer", `{"name":"pointer","contributes":[
	  {"kind":"env","platform":"aws-bedrock","vars":{"POINTER":"yes"}}]}`, "")
	providers := userProviders(t, `{
	  "zai":{"endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  "bed":{"platform":"aws-bedrock","endpoints":{"openai":{"base_url":"https://bedrock.example/v1"}}}}`)
	resolved := map[string]ResolvedProfile{"zai": {Provider: "zai"}, "bed": {Provider: "bed"}}
	scope, err := ScopeCredentials(ScopeInput{
		Packs: []*Pack{agent, pointer}, Providers: providers, Resolved: resolved,
		Profiles: map[string]string{"pi": "zai"},
		Sets:     map[string][]string{"pi": {"zai", "bed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.Agent("pi").PackEnv["POINTER"]; got != "yes" {
		t.Errorf("pi's set holds a Bedrock provider second, so its platform gate must fire: PackEnv = %v",
			scope.Agent("pi").PackEnv)
	}
	// And the set of one on zai alone does not.
	scope, err = ScopeCredentials(ScopeInput{
		Packs: []*Pack{agent, pointer}, Providers: providers, Resolved: resolved,
		Profiles: map[string]string{"pi": "zai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, fired := scope.Agent("pi").PackEnv["POINTER"]; fired {
		t.Error("pi on zai alone must not receive a Bedrock-platform pointer")
	}
}

// A `profile` gate fires for an agent whose set names that profile anywhere, not only first
// (AP-P1), through the gate both notches call: pi on [zai, fast] receives the env gated on
// `fast`. Reading only the primary (profileSelected's set branch cut to set[0]) withholds it.
func TestAProfileGateFiresForAnyEntryOfTheSet(t *testing.T) {
	agent := setPack(t, "pi", true)
	gated := scopePack(t, "gated", `{"name":"gated","contributes":[
	  {"kind":"env","profile":"fast","vars":{"FAST_MODE":"on"}}]}`, "")
	providers := userProviders(t, `{
	  "zai":{"endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  "router":{"endpoints":{"openai":{"base_url":"https://router.example/v1"}}}}`)
	resolved := map[string]ResolvedProfile{"zai": {Provider: "zai"}, "fast": {Provider: "router"}}
	scope, err := ScopeCredentials(ScopeInput{
		Packs: []*Pack{agent, gated}, Providers: providers, Resolved: resolved,
		Profiles: map[string]string{"pi": "zai"},
		Sets:     map[string][]string{"pi": {"zai", "fast"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := scope.Agent("pi").PackEnv["FAST_MODE"]; got != "on" {
		t.Errorf("pi's set names `fast` second, so its profile gate must fire: PackEnv = %v",
			scope.Agent("pi").PackEnv)
	}
}

// Every later entry is asked the protocol gate's question (AP-P1): an entry the agent cannot
// speak refuses the whole launch, naming its position, rather than rendering the rest.
func TestALaterEntryTheAgentCannotSpeakRefuses(t *testing.T) {
	probe := setPack(t, "probe", true)
	providers := userProviders(t, `{
	  "zai":{"endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  "anthro":{"endpoints":{"anthropic":{"base_url":"https://api.anthropic.example"}}}}`)
	_, err := ScopeCredentials(ScopeInput{
		Packs: []*Pack{probe}, Providers: providers,
		Profiles: map[string]string{"probe": "zai"},
		Sets:     map[string][]string{"probe": {"zai", "anthro"}},
		Resolved: map[string]ResolvedProfile{"zai": {Provider: "zai"}, "anthro": {Provider: "anthro"}},
	})
	if err == nil || !strings.Contains(err.Error(), `profile "anthro", entry 2 of probe's profiles (zai, anthro)`) {
		t.Fatalf("err = %v, want the pairing refusal naming entry 2", err)
	}
	// The same refusal, predicted from configuration (yolo check's reader).
	refusals := SetEntryPairingRefusals([]*Pack{probe}, providers,
		map[string]ResolvedProfile{"zai": {Provider: "zai"}, "anthro": {Provider: "anthro"}},
		map[string][]string{"probe": {"zai", "anthro"}}, nil)
	if len(refusals) != 1 || !strings.Contains(refusals[0].Error(), "entry 2 of probe's profiles") {
		t.Errorf("SetEntryPairingRefusals = %v, want the launch's refusal", refusals)
	}
}

// The SHIPPED declarations: pi holds a set (the first slice, §8 step 2) and opencode does (§8
// step 3, AP-D15), and the three agents OQ-AP2 names as running one provider per session do not.
// Read off the embedded packs, so dropping `provider_sets` from packs/pi/pack.json or
// packs/opencode/pack.json fails here.
func TestTheShippedPacksDeclareWhichAgentsHoldASet(t *testing.T) {
	var packs []*Pack
	for _, name := range []string{"pi", "opencode", "claude", "codex", "copilot"} {
		packs = append(packs, shippedPack(t, name))
	}
	for _, agent := range []string{"pi", "opencode"} {
		if !HoldsProviderSets(packs, agent) {
			t.Errorf("packs/%s must declare provider_sets on its program", agent)
		}
	}
	for _, agent := range []string{"claude", "codex", "copilot"} {
		if HoldsProviderSets(packs, agent) {
			t.Errorf("%s runs one provider per session (OQ-AP2) and must not declare provider_sets", agent)
		}
	}
}

// pi's env derive pre-launches the shared OpenAI login when ANY entry of its set is
// openai-codex, since pi can switch to it mid-session; on zai alone it pre-launches nothing.
// The shipped pi pack, through the gate that runs it.
func TestPiPreLaunchesTheCodexLoginForAnyEntry(t *testing.T) {
	pi := shippedPack(t, "pi")
	providers := userProviders(t, `{
	  "zai":{"endpoints":{"openai":{"base_url":"https://api.z.ai/v4"}}},
	  "openai-codex":{"endpoints":{"openai-responses":{"base_url":"https://chatgpt.example/codex"}}}}`)
	resolved := map[string]ResolvedProfile{"zai": {Provider: "zai"}, "codex": {Provider: "openai-codex"}}
	flag := func(sets map[string][]string) string {
		t.Helper()
		scope, err := ScopeCredentials(ScopeInput{Packs: []*Pack{pi}, Providers: providers,
			Resolved: resolved, Profiles: map[string]string{"pi": "zai"}, Sets: sets})
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range scope.Agent("pi").Shape {
			if v.Key == "YOLO_AUTH_PRELAUNCH_PI_FLAG" {
				return v.Value
			}
		}
		return ""
	}
	if got := flag(map[string][]string{"pi": {"zai", "codex"}}); got != "--pi-auth" {
		t.Errorf("pi on [zai, codex] must pre-launch the OpenAI login, got %q", got)
	}
	if got := flag(nil); got != "" {
		t.Errorf("pi on zai alone must pre-launch nothing, got %q", got)
	}
}

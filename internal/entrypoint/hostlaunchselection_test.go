package entrypoint

// hostlaunchselection_test.go pins the composition half of a program's LAUNCH SELECTION at `yolo
// host --` (packdecl.LaunchSelection; docs/design/model-lists-and-pickers.md MM-D30): each shipped
// pack's own derive, run over one launch's wire tables, gives the selection a jail's boot would
// write into the program's file, spelled in the pack's words and writing nothing; and pi's two
// extensions read the lists a launch hands them before their files. The call site, hostExec, is
// pinned in internal/cli (hostmodelmenu_test.go, hostactiveset_test.go).

import (
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// selectionFixture is one agent's shipped pack, its launch selection and the inputs of launches
// over the packs it needs and extra, with user providers and profiles merged into the table.
type selectionFixture struct {
	pack  *packload.Pack
	spec  packdecl.LaunchSelection
	packs []*packload.Pack
	table *jsonx.OrderedMap
	res   map[string]packload.ResolvedProfile
}

func newSelectionFixture(t *testing.T, agent string, userProviders *jsonx.OrderedMap,
	userProfiles map[string]packload.UserProfile, extra ...string) selectionFixture {
	t.Helper()
	packs := testPacksForAgent(t, agent, extra...)
	table, err := packload.ComposeProviders(userProviders, packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := packload.ResolveProfiles(packs, userProfiles, table)
	if err != nil {
		t.Fatal(err)
	}
	f := selectionFixture{packs: packs, table: table, res: resolved}
	for _, p := range packs {
		installs, _ := p.HonoredInstalls()
		for _, in := range installs {
			if in.LaunchSelection != nil && p.Name == packNameFor(agent) {
				f.pack, f.spec = p, *in.LaunchSelection
			}
		}
	}
	if f.pack == nil {
		t.Fatalf("the shipped %s pack declares no launch_selection", agent)
	}
	return f
}

// packNameFor is the shipped pack installing agent.
func packNameFor(agent string) string {
	if agent == "oh-omp" {
		return "omp"
	}
	return agent
}

// compose is the launch selection of one launch selecting use ({"<agent>": ...}), in a fresh home
// it asserts stays empty.
func (f selectionFixture) compose(t *testing.T, use string) *LaunchSelection {
	t.Helper()
	home := t.TempDir()
	in := &HostInputs{Packs: f.packs, Vars: map[string]string{
		ProvidersWireEnv:   mustCompactJSON(t, f.table),
		UseProfilesWireEnv: use,
		ProfilesWireEnv:    mustCompactJSON(t, packload.ProfilesWireTable(f.res)),
	}}
	sel, err := HostLaunchSelection(f.pack, f.spec, home, in, nil)
	if err != nil {
		t.Fatalf("composing %s's launch selection over %s: %v", f.pack.Name, use, err)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("composing the launch selection wrote into the home: %v", entries)
	}
	return sel
}

// acmeProvider is a user provider codex can reach, with a default model, and the profile over it.
func acmeProvider(t *testing.T) (*jsonx.OrderedMap, map[string]packload.UserProfile) {
	t.Helper()
	m, err := jsonx.Decode([]byte(`{"acme": {"endpoints": {"openai": {"base_url": "https://acme.example/v1"}},
	  "api_key_env_name": "ACME_KEY", "models": {"default": "acme-1"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	return m.(*jsonx.OrderedMap), map[string]packload.UserProfile{"acme": {Provider: "acme"}}
}

// CODEX: the subscription's selection names a model and no provider, so the declared default
// stands for the clear a jail's render makes; a provider of the user's own is handed with its row;
// one codex cannot reach composes nothing; and a selection is the same as itself, never as
// another's.
func TestHostLaunchSelectionSpellsCodexsSelectionAsItsDashC(t *testing.T) {
	providers, profiles := acmeProvider(t)
	f := newSelectionFixture(t, "codex", providers, profiles, "zai")

	sub := f.compose(t, `{"codex":"codex"}`)
	argv, err := sub.Argv()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"-c", `model="gpt-6.1-sol"`, "-c", `model_provider="openai"`}; !reflect.DeepEqual(argv, want) {
		t.Errorf("-p codex hands codex %q, want %q: the first declared id, and the built-in provider", argv, want)
	}
	if sub.Rows != nil {
		t.Errorf("the subscription names no row of model_providers, but %v was handed", sub.Rows)
	}

	acme := f.compose(t, `{"codex":"acme"}`)
	argv, err = acme.Argv()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]string{{"-c", `model_provider="acme"`}, {"-c", `model="acme-1"`},
		{"-c", `model_providers.acme.base_url="https://acme.example/v1"`},
		{"-c", `model_providers.acme.env_key="ACME_KEY"`}, {"-c", `model_providers.acme.wire_api="responses"`}} {
		if !containsRun(argv, want) {
			t.Errorf("-p acme hands codex %q, which lacks %q", argv, want)
		}
	}
	for _, w := range argv {
		if strings.Contains(w, "model_providers.zai") {
			t.Errorf("-p acme handed a row the selection does not name: %q", w)
		}
	}

	if zai := f.compose(t, `{"codex":"zai"}`); !zai.Empty() {
		t.Errorf("codex cannot reach zai (chat completions only), yet -p zai composed %v", zai.Selection)
	}
	if !sub.Same(f.compose(t, `{"codex":"codex"}`)) || sub.Same(acme) {
		t.Error("a selection must be the same as itself and differ from another's")
	}
	if none := f.compose(t, `{}`); !none.Empty() || !none.Same(f.compose(t, `{"codex":"zai"}`)) {
		t.Error("no profile composes nothing, the same nothing an unreachable provider composes")
	}
}

// OPENCODE: one document in its variable, the selection's keys and the rows enabled_providers
// names, merged over a value the user already set there, whose other keys are kept; a value that is
// not a document is refused, never overwritten. A provider opencode has built in is named by its
// own id and gets no row (pi-codex-provider-shadowing.md OQ-3): zai is its `zai-coding-plan`, and
// llamacpp, which it has not built in, carries the row the merge is about.
func TestHostLaunchSelectionHandsOpencodeOneDocumentMergedOverTheUsersOwn(t *testing.T) {
	f := newSelectionFixture(t, "opencode", nil, nil, "llamacpp", "zai")
	sel := f.compose(t, `{"opencode":["llamacpp","zai"]}`)
	if argv, _ := sel.Argv(); argv != nil {
		t.Errorf("the env form hands no argv, got %q", argv)
	}
	vars, err := sel.Vars(func(string) (string, bool) { return "", false })
	if err != nil || len(vars) != 1 || vars[0].Name != f.spec.Env || vars[0].Merged {
		t.Fatalf("vars = %+v (%v), want one unmerged %s", vars, err, f.spec.Env)
	}
	doc := decodeObject(t, vars[0].Value)
	if got := doc["enabled_providers"]; !reflect.DeepEqual(got, []any{"llamacpp", "zai-coding-plan"}) {
		t.Errorf("enabled_providers = %v, want the set in order, zai by opencode's own id", got)
	}
	rows, _ := doc["provider"].(map[string]any)
	if _, ok := rows["llamacpp"]; !ok || len(rows) != 1 {
		t.Errorf("provider rows = %v, want llamacpp's alone (zai is opencode's own)", rows)
	}

	user := `{"theme": "mine", "enabled_providers": ["anthropic"], "provider": {"llamacpp": {"name": "My llama"}}}`
	vars, err = sel.Vars(func(name string) (string, bool) { return user, name == f.spec.Env })
	if err != nil || len(vars) != 1 || !vars[0].Merged {
		t.Fatalf("vars over the user's own = %+v (%v), want one merged", vars, err)
	}
	doc = decodeObject(t, vars[0].Value)
	llama, _ := doc["provider"].(map[string]any)["llamacpp"].(map[string]any)
	if doc["theme"] != "mine" || llama["name"] != "My llama" || llama["options"] == nil ||
		!reflect.DeepEqual(doc["enabled_providers"], []any{"llamacpp", "zai-coding-plan"}) {
		t.Errorf("the merge kept %v; want the user's theme and row name kept, the selection's keys winning", doc)
	}
	if _, err := sel.Vars(func(string) (string, bool) { return "not json", true }); err == nil {
		t.Error("a value of the user's that is no document was merged into")
	}
}

// PI: its own flags, the set's scope as one --models word; and the two list files its extensions
// read, each in the variable its declaration names, as this launch composes them.
func TestHostLaunchSelectionHandsPiItsFlagsAndItsLists(t *testing.T) {
	f := newSelectionFixture(t, "pi", nil, nil, "zai", "openrouter")
	sel := f.compose(t, `{"pi":["zai","openrouter"]}`)
	argv, err := sel.Argv()
	if err != nil {
		t.Fatal(err)
	}
	if len(argv) < 6 || argv[0] != "--provider" || argv[1] != "zai" || argv[2] != "--model" ||
		argv[4] != "--models" {
		t.Fatalf("pi on [zai, openrouter] is handed %q, want --provider zai --model <id> --models <scope>", argv)
	}
	scope := strings.Split(argv[5], ",")
	if !strings.HasPrefix(scope[0], "zai/") || !slices.Contains(scope, "openrouter/*") {
		t.Errorf("--models %q must lead with zai's run and span openrouter", argv[5])
	}
	vars, err := sel.Vars(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	named := map[string]string{}
	for _, v := range vars {
		named[v.Name] = v.Value
	}
	codexVar := f.spec.Surfaces[piCodexModelsRel(t)]
	if codexVar == "" || named[codexVar] == "" || f.spec.Surfaces[piModelListsRel(t)] == "" {
		t.Fatalf("pi's declaration must name a variable for both list files; vars = %v", named)
	}
	if models, _ := decodeObject(t, named[codexVar])["models"].([]any); len(models) == 0 {
		t.Errorf("%s carries no openai-codex list: %s", codexVar, named[codexVar])
	}
}

// AN ARGV FORM REFUSES WHAT IT CANNOT SPELL, rather than handing a word the program would misread:
// a key holding a dot for a dotted {key}, an item holding the comma that joins a list.
func TestALaunchSelectionRefusesWhatItsWordsCannotSpell(t *testing.T) {
	each := &LaunchSelection{Spec: packdecl.LaunchSelection{Each: []string{"-c", "{key}={value}"},
		Rows: &packdecl.LaunchSelectionRows{Table: "rows", NamedBy: []string{"p"}}}, codec: "toml",
		Selection: map[string]any{"p": "a.b"}, Rows: map[string]any{"a.b": map[string]any{"url": "x"}}}
	if _, err := each.Argv(); err == nil || !strings.Contains(err.Error(), `"a.b"`) {
		t.Errorf("a dotted row key was spelled (%v)", err)
	}
	flags := &LaunchSelection{Spec: packdecl.LaunchSelection{Flags: []packdecl.LaunchSelectionFlag{
		{Key: "scope", Argv: []string{"--models", "{value}"}}}}, Selection: map[string]any{"scope": []any{"a,b"}}}
	if _, err := flags.Argv(); err == nil || !strings.Contains(err.Error(), "comma") {
		t.Errorf("an item holding a comma was joined (%v)", err)
	}
}

// THE TWO PI EXTENSIONS READ THE LAUNCH'S LISTS BEFORE THEIR FILES: with the variable pi's
// declaration names set to what a launch composed, the delivered extension registers that list
// and not the file's, on both of yolo-openai-auth.js's routes; unset, the file's. The variable's
// name is read off the shipped declaration, so the pack and each extension cannot disagree on it.
func TestPiExtensionsReadTheLaunchsListsBeforeTheirFiles(t *testing.T) {
	f := newSelectionFixture(t, "pi", nil, nil)
	sel := f.compose(t, `{"pi":"codex"}`)
	vars, err := sel.Vars(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	codexVar := f.spec.Surfaces[piCodexModelsRel(t)]
	var launchList string
	for _, v := range vars {
		if v.Name == codexVar {
			launchList = v.Value
		}
	}
	var composed struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(launchList), &composed); err != nil || len(composed.Models) == 0 {
		t.Fatalf("the launch composed no openai-codex list in %s: %q (%v)", codexVar, launchList, err)
	}
	var want []any
	for _, m := range composed.Models {
		want = append(want, m.ID)
	}
	for _, route := range piRoutes {
		fx := newPiExtensionFixture(t)
		fx.builtinStub(t)
		fx.modelsFile(t, `{"models":[{"id":"gpt-6-from-the-file"}]}`)
		if ids := registeredCodexIDs(t, fx.output(t, route, piRegisteredIDsHarness, codexVar+"="+launchList)); !reflect.DeepEqual(ids, want) {
			t.Errorf("on the %s with %s set the extension registers %v, want the launch's %v", route.name, codexVar, ids, want)
		}
		if ids := registeredCodexIDs(t, fx.output(t, route, piRegisteredIDsHarness, codexVar+"=")); !reflect.DeepEqual(ids, []any{"gpt-6-from-the-file"}) {
			t.Errorf("on the %s with %s unset the extension registers %v, want the file's", route.name, codexVar, ids)
		}
	}

	listsVar := f.spec.Surfaces[piModelListsRel(t)]
	t.Setenv(listsVar, "")
	fromFile := runPiModelListsExtension(t, renderedZaiLists(t, false), piZaiCatalogStub, piCompatStub)
	if len(fromFile.Registrations) == 0 {
		t.Fatal("the rendered zai list registers nothing from its file")
	}
	t.Setenv(listsVar, `{"providers":{}}`)
	if fromLaunch := runPiModelListsExtension(t, renderedZaiLists(t, false), piZaiCatalogStub, piCompatStub); len(fromLaunch.Registrations) != 0 {
		t.Errorf("with %s naming no list the extension still registers the file's: %+v", listsVar, fromLaunch.Registrations)
	}
}

// containsRun reports whether run appears in argv as consecutive words.
func containsRun(argv, run []string) bool {
	for i := 0; i+len(run) <= len(argv); i++ {
		if slices.Equal(argv[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

func decodeObject(t *testing.T, text string) map[string]any {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("not a JSON object: %q (%v)", text, err)
	}
	return doc
}

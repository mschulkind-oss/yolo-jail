package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func callRootsWorkspace(t *testing.T, body string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("YOLO_VERSION", "")
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, WorkspaceTOMLConfigName), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return ws
}
func TestReadWorkspaceForGateLowersTOMLCallRoots(t *testing.T) {
	ws := callRootsWorkspace(t, "[call_roots]\nlocal-tasks = [\"task://one\", \"task://two\"]\n")
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []EntryRepo{{Repo: "task://one", Files: []string{WorkspaceTOMLConfigName}}, {Repo: "task://two", Files: []string{WorkspaceTOMLConfigName}}}
	if got := read.CallRoots("local-tasks"); !reflect.DeepEqual(got, want) {
		t.Fatalf("roots=%+v", got)
	}
	if _, present := read.Config.Get("call_roots"); present {
		t.Fatal("format spelling leaked into canonical config")
	}
	check := WithCallBrokerDeclarations(nil, ws, []SelectedCallBrokerScope{{Source: "local-tasks", Pack: "tasks", Declaration: callBrokerCellFixture(t)}}, read)
	if len(check.Sources) != 1 || check.Sources[0].EntryKey != "brokered.local-tasks.roots" || !reflect.DeepEqual(check.Sources[0].Entry, want) {
		t.Fatalf("gate=%+v", check)
	}
	if approvalConfigPart(read.Config).Len() != 0 {
		t.Fatal("roots leaked into config approval part")
	}
}
func TestValidateConfigTOMLCallRoots(t *testing.T) {
	for _, tc := range []struct{ name, roots, mode, source, want string }{
		{"valid", `["task://one"]`, "declared", "local-tasks", ""},
		{"empty", `[]`, "declared", "local-tasks", ""},
		{"bad", `["task://../escape"]`, "declared", "local-tasks", "not a declared root"},
		{"duplicate", `["task://one","task://one"]`, "declared", "local-tasks", "duplicate root"},
		{"unselected", `["task://one"]`, "declared", "other", "no selected pack"},
		{"remote", `["task://one"]`, "remotes", "local-tasks", "git remotes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := callRootsWorkspace(t, "[call_roots]\n"+tc.source+" = "+tc.roots+"\n")
			cfg, src, err := LoadReadableConfigWithSources(ws, true, func(string) {})
			if err != nil {
				t.Fatal(err)
			}
			decl := callBrokerCellFixture(t)
			decl.Scope.Roots = tc.mode
			resolver := callBrokerFenceResolver{"tasks": {Name: "tasks", CallBroker: decl}}
			errs, _ := ValidateConfig(cfg, ws, resolver)
			text := strings.Join(src.Annotate(errs), "\n")
			if tc.want == "" {
				if len(errs) != 0 {
					t.Fatalf("errors=%s", text)
				}
			} else if !strings.Contains(text, tc.want) || !strings.Contains(text, "call_roots."+tc.source) {
				t.Fatalf("diagnostic=%s", text)
			}
		})
	}
}
func TestTOMLCallRootsRefusesRawBrokeredUserAndOutside(t *testing.T) {
	t.Run("brokered", func(t *testing.T) {
		ws := callRootsWorkspace(t, "[brokered.local-tasks]\nroots=[\"task://one\"]\n")
		_, err := ReadWorkspaceForGate(ws)
		if err == nil || !strings.Contains(err.Error(), "call_roots") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("user", func(t *testing.T) {
		ws := callRootsWorkspace(t, "")
		dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("[call_roots]\nlocal-tasks=[]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadReadableConfigWithSources(ws, true, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "workspace-only") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("legacy-user", func(t *testing.T) {
		ws := callRootsWorkspace(t, "")
		dir := filepath.Join(os.Getenv("HOME"), ".config", "yolo-jail")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"call_roots":{"local-tasks":["task://one"]}}`), 0600); err != nil {
			t.Fatal(err)
		}
		_, _, err := LoadReadableConfigWithSources(ws, true, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "workspace-only") {
			t.Fatalf("error=%v", err)
		}
	})
	t.Run("outside", func(t *testing.T) {
		ws := callRootsWorkspace(t, "include_if_found=[\"outside.toml\"]\n")
		outside := filepath.Join(t.TempDir(), "roots.toml")
		if err := os.WriteFile(outside, []byte("[call_roots]\nlocal-tasks=[\"task://one\"]\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(ws, "outside.toml")); err != nil {
			t.Fatal(err)
		}
		_, err := ReadWorkspaceForGate(ws)
		if err == nil || !strings.Contains(err.Error(), "outside this workspace") || !strings.Contains(err.Error(), "call_roots") {
			t.Fatalf("error=%v", err)
		}
	})
}
func TestCallRootsNeverInheritIntoUserAuthority(t *testing.T) {
	raw := decode(t, `{"call_roots":{"local-tasks":["task://one"]},"brokered":{"local-tasks":{"roots":["task://one"]}}}`)
	for _, scope := range []InheritScope{InheritPreflight, InheritNested} {
		inherited, unknown, err := FilterInheritErr(raw, scope)
		if err != nil {
			t.Fatal(err)
		}
		if len(unknown) != 0 {
			t.Fatalf("unclassified roots=%v", unknown)
		}
		for _, key := range []string{"call_roots", "brokered"} {
			if _, present := inherited.Get(key); present {
				t.Fatalf("%s inherited at scope %v", key, scope)
			}
		}
	}
}

func TestCallRootsCanonicalApprovalKeepsDeclaringFileBinding(t *testing.T) {
	ws := callRootsWorkspace(t, "[call_roots]\nlocal-tasks=[\"task://one\",\"task://two\"]\n")
	toml, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	selected := []SelectedCallBrokerScope{{Source: "local-tasks", Pack: "tasks", Declaration: callBrokerCellFixture(t)}}
	a := WithCallBrokerDeclarations(nil, ws, selected, toml)
	if err := os.Remove(filepath.Join(ws, WorkspaceTOMLConfigName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, WorkspaceConfigName), []byte(`{"brokered":{"local-tasks":{"roots":["task://one","task://two"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	legacy, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	b := WithCallBrokerDeclarations(nil, ws, selected, legacy)
	ar := compareScope(a, scopeRecord{}, sourcesRecord{})
	br := compareScope(b, scopeRecord{}, sourcesRecord{})
	if !reflect.DeepEqual(ar.next, br.next) || a.Sources[0].EntryKey != b.Sources[0].EntryKey {
		t.Fatal("canonical approval roots/declaration changed across formats")
	}
	if reflect.DeepEqual(ar.nextSources, br.nextSources) {
		t.Fatal("declaring filename binding was lost")
	}
	if !reflect.DeepEqual(approvalConfigPart(toml.Config), approvalConfigPart(legacy.Config)) {
		t.Fatal("config approval part changed")
	}
}

func TestTOMLCallRootsDoesNotTakeValuedGrantNamespace(t *testing.T) {
	ws := callRootsWorkspace(t, "[requests.fixture]\nroots=[\"value-one\"]\n")
	useGrantFixturePacks(t, map[string]string{"fixture": `[[grants]]
kind="roots"
text="fixture roots values"
valued=true
value_noun="root values"
`})
	cfg, err := LoadReadableConfig(ws, true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	dir := writeGrantFixturePack(t, t.TempDir(), "fixture", `[[grants]]
kind="roots"
text="fixture roots values"
valued=true
value_noun="root values"
`)
	declarations := grantFixtureDeclarations(t, dir, "fixture")
	plan, err := ResolveReadableAllowance(ReadableAllowanceInput{User: decode(t, `{"allow":{"fixture":{"on_request":["roots"]}}}`), Workspace: cfg, SelectedPacks: []string{"fixture"}, PackSettings: declarations})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.LoopholeGrants.Packs) != 1 || !reflect.DeepEqual(plan.LoopholeGrants.Packs[0].Kinds[0].Values, []string{"value-one"}) {
		t.Fatalf("valued grant=%+v", plan.LoopholeGrants)
	}
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.CallRoots("fixture")) != 0 {
		t.Fatal("grant became scope context")
	}
}

func TestCallRootsMixedSourcesKeepContainedIncludeOrigins(t *testing.T) {
	ws := callRootsWorkspace(t, "include_if_found=[\"roots.toml\"]\n[call_roots]\nlocal-tasks=[\"task://one\"]\n")
	if err := os.WriteFile(filepath.Join(ws, "roots.toml"), []byte("[call_roots]\nlocal-tasks=[\"task://two\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, WorkspaceLocalLegacyConfigName), []byte(`{"brokered":{"other":{"roots":["other://three"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	want := []EntryRepo{{Repo: "task://one", Files: []string{WorkspaceTOMLConfigName}}, {Repo: "task://two", Files: []string{"roots.toml"}}}
	if !reflect.DeepEqual(read.CallRoots("local-tasks"), want) {
		t.Fatalf("include roots=%+v", read.CallRoots("local-tasks"))
	}
	if got := read.CallRoots("other"); len(got) != 1 || !reflect.DeepEqual(got[0].Files, []string{WorkspaceLocalLegacyConfigName}) {
		t.Fatalf("other source=%+v", got)
	}
}

func TestCallRootsMixedSpellingsAndReadableListMerge(t *testing.T) {
	ws := callRootsWorkspace(t, "[call_roots]\nlocal-tasks=[\"task://one\"]\n")
	local := filepath.Join(ws, WorkspaceLocalTOMLConfigName)
	if err := os.WriteFile(local, []byte("[call_roots]\nlocal-tasks=[\"task://one\",\"task://two\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read, err := ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	got := read.CallRoots("local-tasks")
	if len(got) != 2 || len(got[0].Files) != 2 {
		t.Fatalf("merge=%+v", got)
	}
	if err := os.WriteFile(local, []byte("[call_roots]\nlocal-tasks=[]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	read, err = ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.CallRoots("local-tasks")) != 0 {
		t.Fatal("empty local list did not reset roots")
	}
	if err := os.Remove(local); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, WorkspaceLocalLegacyConfigName), []byte(`{"brokered":{"local-tasks":{"roots":["task://two"]}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = ReadWorkspaceForGate(ws)
	if err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("mixed spellings error=%v", err)
	}
}

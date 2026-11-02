package config

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

func editReadableMap(t *testing.T, text string) *jsonx.OrderedMap {
	t.Helper()
	value, err := json5.Decode([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	mapping, ok := value.(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("decoded value %T, want object", value)
	}
	return mapping
}

func selectedEmbeddedPackForReadableDeclarations(t *testing.T, name string) *packload.Pack {
	t.Helper()
	selected, problems, err := packload.ReadSelectedPackFS(packs.FS, name, "/selected/"+name, name, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(problems) != 0 {
		t.Fatalf("load selected embedded pack %s: %v", name, problems)
	}
	return selected
}

func pathBackedOfficialPackForReadableDeclarations(t *testing.T, name string) *packload.Pack {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	root, err := filepath.Abs(filepath.Join(filepath.Dir(file), "..", "..", "packs", name))
	if err != nil {
		t.Fatal(err)
	}
	loaded, problems := packload.LoadDir(root, name)
	if len(problems) != 0 {
		t.Fatalf("load path-backed official pack %s: %v", name, problems)
	}
	loaded.Official = true
	return loaded
}

func TestReadableDeclarationsForOfficialPackReadsEmbeddedModuleFiles(t *testing.T) {
	pack := selectedEmbeddedPackForReadableDeclarations(t, "aws-auth")
	if pack == nil {
		t.Fatal("selected embedded aws-auth pack is missing")
	}
	brokers, settings, err := ReadableDeclarationsForPacks([]*packload.Pack{pack})
	if err != nil {
		t.Fatal(err)
	}
	if len(settings) == 0 || settings[0].Pack != "aws-auth" {
		t.Fatalf("embedded module metadata not projected: brokers=%#v settings=%#v", brokers, settings)
	}
}

func TestReadableDeclarationsForOfficialPathPackReadsItsOwnModuleFiles(t *testing.T) {
	pack := pathBackedOfficialPackForReadableDeclarations(t, "aws-auth")
	if _, _, err := ReadableDeclarationsForPacks([]*packload.Pack{pack}); err != nil {
		t.Fatal(err)
	}
}

func TestReadableDeclarationsForPacksRejectsUnboundOfficialDeclaration(t *testing.T) {
	raw, err := packs.FS.ReadFile("aws-auth/pack.toml")
	if err != nil {
		t.Fatal(err)
	}
	decl, problems := packdecl.DecodeTOML(raw)
	if len(problems) != 0 {
		t.Fatalf("decode fixture declaration: %v", problems)
	}
	pack := &packload.Pack{Name: "aws-auth", Root: "/stale/tree/aws-auth", Decl: decl, Official: true}
	if _, _, err := ReadableDeclarationsForPacks([]*packload.Pack{pack}); err == nil {
		t.Fatal("an official declaration without loaded source binding was paired with the current embedded FS by name")
	}
}

// TestReadableDeclarationsForPacksPreservesExplicitEmptyStringListDefault: a string_list
// setting declared `default = []` keeps an explicit, non-nil empty default. Its shipped
// instance was host-processes' `visible`, which became the `view` grant (plan F3), so the
// declaration is a fixture pack's.
func TestReadableDeclarationsForPacksPreservesExplicitEmptyStringListDefault(t *testing.T) {
	dir := writeGrantFixturePack(t, t.TempDir(), "lister", grantFixtureSerial+`
[settings.names]
type = "string_list"
scope = "user"
default = []
description = "names"
`)
	for _, declaration := range grantFixtureDeclarations(t, dir, "lister") {
		if declaration.Pack != "lister" {
			continue
		}
		for _, setting := range declaration.Settings {
			if setting.Key != "names" {
				continue
			}
			values, ok := setting.Default.([]string)
			if !ok || values == nil || len(values) != 0 || !setting.DefaultSet {
				t.Fatalf("names default = %#v (%T), defaultSet=%t; want explicit non-nil empty []string", setting.Default, setting.Default, setting.DefaultSet)
			}
			return
		}
	}
	t.Fatal("the fixture's names setting is missing from readable declarations")
}

func TestReadableBrokerDeclarationClonesSettingsDefaultLists(t *testing.T) {
	settings := []loopholedecl.Setting{{Key: "visible", Type: loopholedecl.SettingTypeStringList, Scope: loopholedecl.SettingScopeWorkspace, Default: []string{"alpha", "beta"}}}
	declaration := readableBrokerDeclaration("fixture", "fixture-broker", loopholedecl.Brokered{}, settings)

	settings[0].Default.([]string)[0] = "source-mutated"
	got := declaration.Settings[0].Default.([]string)
	if !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("declaration setting default aliases source: got %#v", got)
	}
	got[1] = "declaration-mutated"
	if !reflect.DeepEqual(settings[0].Default, []string{"source-mutated", "beta"}) {
		t.Fatalf("source setting default aliases declaration: got %#v", settings[0].Default)
	}
}

func TestReadableBrokerDeclarationClonesExplicitPendingMetadata(t *testing.T) {
	original := loopholedecl.Brokered{
		Source: "github", RemoteHost: "github.com",
		ReadCapability:      "read",
		KindCapabilities:    map[string]string{"create-pr": "create_pr", "create-issue": "create_issue"},
		PendingCapabilities: []string{"create_pr", "create_issue"},
	}
	before := loopholedecl.Brokered{
		Source: original.Source, RemoteHost: original.RemoteHost, ReadCapability: original.ReadCapability,
		KindCapabilities: cloneStringMap(original.KindCapabilities), PendingCapabilities: append([]string(nil), original.PendingCapabilities...),
	}
	declaration := readableBrokerDeclaration("github", "github-broker", original, nil)
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("adapter mutated source declaration: got %#v, want %#v", original, before)
	}
	if !reflect.DeepEqual(declaration.Brokered.Grants, []string{"create-issue", "create-pr"}) {
		t.Fatalf("pending-only mapped grant kinds = %#v", declaration.Brokered.Grants)
	}
	if !reflect.DeepEqual(declaration.PendingCapabilities, original.PendingCapabilities) {
		t.Fatalf("pending capabilities = %#v", declaration.PendingCapabilities)
	}

	plan, err := ResolveReadableAllowance(ReadableAllowanceInput{
		User:          editReadableMap(t, `{"allow":{"github":{"always":["read"],"on_request":["create_pr","create_issue"]}}}`),
		Workspace:     editReadableMap(t, `{"requests":{"github":{"create_pr":true,"create_issue":true}}}`),
		SelectedPacks: []string{"github"}, Declarations: []ReadableBrokerDeclaration{declaration},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Requests) != 2 {
		t.Fatalf("requests = %#v, want two pending requests", plan.Requests)
	}
	for _, request := range plan.Requests {
		if request.Status != ReadableRequestPending {
			t.Errorf("request %s status %q, want pending-runtime", request.Path, request.Status)
		}
	}
	if !reflect.DeepEqual(original, before) {
		t.Fatalf("allowance projection mutated source declaration: got %#v, want %#v", original, before)
	}
}

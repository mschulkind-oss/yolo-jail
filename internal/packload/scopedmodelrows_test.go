package packload

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/modelview"
)

func composedModelIDs(t *testing.T, table *jsonx.OrderedMap, name string) []string {
	t.Helper()
	entry := providerEntry(table, name)
	models := subOrderedOrNil(entry, "models")
	seen := map[string]bool{}
	var out []string
	if models == nil {
		return nil
	}
	for _, a := range models.Keys() {
		id, _ := stringAt(models, a)
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}

func TestUserModelRowsSurviveScopedOnlyLikeUnscoped(t *testing.T) {
	for _, tc := range []struct{ name, user string }{
		{"legacy-string-form", `{"gw":{"models":{"custom":"user-row-9"}}}`},
		{"legacy-object-form", `{"gw":{"models":{"custom":{"id":"user-row-9","vendor":"acme"}}}}`},
		{"readable-model-form", `{"gw":{"models":{"custom":{"model":"user-row-9","vendor":"acme"}}}}`},
		{"readable-row-form", `{"gw":{"models":{"user-row-9":{"vendor":"acme"}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := userProviders(t, tc.user)
			var notesU, notesS []string
			u, err := ComposeProviders(user, []*Pack{gatewayPack(t),
				modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["beta-2"]}`)},
				WithModelNotes(func(n string) { notesU = append(notesU, n) }))
			if err != nil {
				t.Fatal(err)
			}
			s, err := ComposeProviders(user, []*Pack{gatewayPack(t),
				modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["beta-2"],"agents":["claude"]}`)},
				WithModelNotes(func(n string) { notesS = append(notesS, n) }))
			if err != nil {
				t.Fatal(err)
			}
			unscoped := composedModelIDs(t, u, "gw")
			if !reflect.DeepEqual(unscoped, []string{"beta-2", "user-row-9"}) {
				t.Fatalf("user-last id lost in baseline: %v", unscoped)
			}
			projected := modelview.ProjectProviderTable(s, "claude").(*jsonx.OrderedMap)
			scoped := composedModelIDs(t, projected, "gw")
			t.Logf("unscoped ids=%v notes=%v", unscoped, notesU)
			t.Logf("scoped   ids=%v notes=%v view=%s", scoped, notesS, dump(t, providerEntry(s, "gw")))
			if !reflect.DeepEqual(unscoped, scoped) {
				t.Errorf("claude's scoped view %v != unscoped only list %v (user-declared id must survive)", scoped, unscoped)
			}
		})
	}
}

func TestOnlyNamingUserModelRowsDoesNotWarn(t *testing.T) {
	for _, model := range []string{`"user-row-9":{"vendor":"acme"}`, `"custom":{"model":"user-row-9","vendor":"acme"}`, `"custom":{"id":"user-row-9","vendor":"acme"}`, `"custom":"user-row-9"`} {
		user := userProviders(t, `{"gw":{"models":{`+model+`}}}`)
		for _, audience := range []string{"", `,"agents":["claude"]`} {
			var notes []string
			got, err := ComposeProviders(user, []*Pack{gatewayPack(t), modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["user-row-9"]`+audience+`}`)}, WithModelNotes(func(n string) { notes = append(notes, n) }))
			if err != nil {
				t.Fatal(err)
			}
			if len(notes) != 0 {
				t.Errorf("%s %s: false model notes: %v", model, audience, notes)
			}
			if ids := composedModelIDs(t, modelview.ProjectProviderTable(got, "claude").(*jsonx.OrderedMap), "gw"); !reflect.DeepEqual(ids, []string{"user-row-9"}) {
				t.Errorf("model ids = %v", ids)
			}
		}
	}
}

func TestScopedOnlyUnlistedIDsReportsEmptyView(t *testing.T) {
	var notes []string
	got, err := ComposeProviders(nil, []*Pack{gatewayPack(t),
		modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["nope"],"agents":["claude"]}`)},
		WithModelNotes(func(n string) { notes = append(notes, n) }))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(notes, "\n"), "keep no models for agent") {
		t.Errorf("missing empty-view note: %v", notes)
	}
	t.Logf("notes=%q claude=%v", notes, composedModelIDs(t, modelview.ProjectProviderTable(got, "claude").(*jsonx.OrderedMap), "gw"))
}

func TestScopedModelNotesSortProvidersAndKeepUserRows(t *testing.T) {
	for i := 0; i < 32; i++ {
		var notes []string
		packs := []*Pack{modelsPack(t, "policy", `{"kind":"models","provider":"z-last","only":["absent"],"agents":["claude"]},{"kind":"models","provider":"a-first","only":["absent"],"agents":["claude"]}`)}
		_, err := ComposeProviders(nil, packs, WithModelNotes(func(note string) { notes = append(notes, note) }))
		if err != nil {
			t.Fatal(err)
		}
		if len(notes) != 2 || !strings.Contains(notes[0], `"a-first"`) || !strings.Contains(notes[1], `"z-last"`) {
			t.Fatalf("provider notes not sorted: %v", notes)
		}
	}
	user := userProviders(t, `{"gw":{"models":{"user-row":{"vendor":"acme"}}}}`)
	var notes []string
	_, err := ComposeProviders(user, []*Pack{gatewayPack(t), modelsPack(t, "policy", `{"kind":"models","provider":"gw","only":["absent"],"agents":["claude"]}`)}, WithModelNotes(func(note string) { notes = append(notes, note) }))
	if err != nil {
		t.Fatal(err)
	}
	for _, note := range notes {
		if strings.Contains(note, "keep no models") {
			t.Fatalf("user-last row prevents an empty view: %v", notes)
		}
	}
}

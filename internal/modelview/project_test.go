package modelview

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestProjectProviderTableFiltersOnlyTheNamedAgentWithoutMutation(t *testing.T) {
	models := jsonx.NewOrderedMap()
	models.Set("fable", "fable-id")
	models.Set("opus", "opus-id")
	models.Set("custom", "custom-id")
	options := jsonx.NewOrderedMap()
	options.Set("fable", jsonx.NewOrderedMap())
	options.Set("opus", jsonx.NewOrderedMap())
	options.Set("custom", jsonx.NewOrderedMap())
	views := jsonx.NewOrderedMap()
	views.Set("claude", []any{"fable-id", "opus-id"})
	views.Set("pi", []any{"custom-id"})
	entry := jsonx.NewOrderedMap()
	entry.Set("models", models)
	entry.Set("model_options", options)
	entry.Set(ViewsKey, views)
	providers := jsonx.NewOrderedMap()
	providers.Set("bedrock", entry)
	before := jsonx.DeepCopy(providers)

	projected, ok := ProjectProviderTable(providers, "claude").(*jsonx.OrderedMap)
	if !ok {
		t.Fatalf("projected table has type %T", ProjectProviderTable(providers, "claude"))
	}
	projectedEntry, _ := projected.Get("bedrock")
	gotModels, _ := objectGet(projectedEntry, "models")
	if got, want := objectKeys(gotModels), []string{"fable", "opus"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Claude aliases = %v, want %v", got, want)
	}
	if _, ok := objectGet(projectedEntry, ViewsKey); ok {
		t.Error("composer view key reached Claude's derive")
	}
	if marked, _ := objectGet(projectedEntry, ModelsOnlyKey); marked != true {
		t.Errorf("models_only = %v, want true", marked)
	}
	if !reflect.DeepEqual(providers, before) {
		t.Errorf("projection mutated shared input: got %s, want %s", providers, before)
	}

	pi := ProjectProviderTable(providers, "pi").(*jsonx.OrderedMap)
	piEntry, _ := pi.Get("bedrock")
	piModels, _ := objectGet(piEntry, "models")
	if got, want := objectKeys(piModels), []string{"custom"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Pi aliases = %v, want %v", got, want)
	}
}

func TestProjectProviderTableStripsUnmatchedViewsAndPreservesIdentityWithoutViews(t *testing.T) {
	providers := map[string]any{"bedrock": map[string]any{
		"models": map[string]any{"a": "id-a", "b": "id-b"},
		ViewsKey: map[string]any{"claude": []any{"id-a"}},
	}}
	projected := ProjectProviderTable(providers, "copilot").(map[string]any)
	entry := projected["bedrock"].(map[string]any)
	if _, ok := entry[ViewsKey]; ok {
		t.Fatal("unmatched composer view key reached derive")
	}
	if got := entry["models"].(map[string]any); !reflect.DeepEqual(got, map[string]any{"a": "id-a", "b": "id-b"}) {
		t.Errorf("unmatched view changed models: %v", got)
	}
	if _, ok := entry[ModelsOnlyKey]; ok {
		t.Error("unmatched agent was marked narrowed")
	}
	plain := map[string]any{"bedrock": map[string]any{"models": map[string]any{"a": "id-a"}}}
	if got := ProjectProviderTable(plain, "claude"); !reflect.DeepEqual(got, plain) {
		t.Errorf("table without views changed: %#v", got)
	}
}

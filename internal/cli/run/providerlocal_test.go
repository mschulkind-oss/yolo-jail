package run

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

func TestLocalProviderForwardsFindsOnlyUserLoopbackURLs(t *testing.T) {
	decoded, err := jsonx.Decode([]byte(`{
  "local": {
    "base_url": "http://localhost:8080/v1",
    "endpoints": {
      "anthropic": {"base_url": "https://[::1]/api"},
      "openai": {"base_url": "http://127.0.0.1:8080/v1"}
    }
  },
  "remote": {"base_url": "https://api.example.test/v1"},
  "not-an-object": null
}`))
	if err != nil {
		t.Fatal(err)
	}
	providers := decoded.(*jsonx.OrderedMap)
	if got, want := localProviderForwards(providers), []any{8080, 443}; !reflect.DeepEqual(got, want) {
		t.Errorf("localProviderForwards = %#v, want %#v", got, want)
	}
}

func TestMergeHostForwardsPreservesExplicitRemaps(t *testing.T) {
	got := mergeHostForwards([]any{"8080:9090", 3000}, []any{8080, 11434})
	if want := []any{"8080:9090", 3000, 11434}; !reflect.DeepEqual(got, want) {
		t.Errorf("mergeHostForwards = %#v, want %#v", got, want)
	}
}

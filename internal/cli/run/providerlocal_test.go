package run

import (
	"reflect"
	"strings"
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

// TestLocalProviderForwardSourcesAttributesEachPort pins OQ-PC2's attribution half: the
// disclosure has to name WHO asked for a port, and first-writer-wins on a duplicate must
// match the port-only projection so the two cannot disagree about how many forwards exist.
func TestLocalProviderForwardSourcesAttributesEachPort(t *testing.T) {
	decoded, err := jsonx.Decode([]byte(`{
  "ollama": {"base_url": "http://localhost:11434/v1"},
  "lmstudio": {
    "base_url": "http://127.0.0.1:1234/v1",
    "endpoints": {"openai": {"base_url": "http://localhost:11434/v1"}}
  },
  "remote": {"base_url": "https://api.example.test/v1"}
}`))
	if err != nil {
		t.Fatal(err)
	}
	providers := decoded.(*jsonx.OrderedMap)

	got := localProviderForwardSources(providers)
	want := []providerForward{{Port: 11434, Provider: "ollama"}, {Port: 1234, Provider: "lmstudio"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("localProviderForwardSources = %#v, want %#v", got, want)
	}
	// The two projections must describe the same set, or the disclosure and the transport
	// would report different numbers of holes.
	if ports := localProviderForwards(providers); len(ports) != len(got) {
		t.Errorf("port-only projection has %d entries, attributed has %d — they must agree",
			len(ports), len(got))
	}
}

// TestDiscloseImplicitForwardsNamesPortAndProvider pins the line's content, and that it
// stays silent about a port the user declared themselves — that hole is already in their
// config and in the briefing, so naming it here would report their own decision back to them.
func TestDiscloseImplicitForwardsNamesPortAndProvider(t *testing.T) {
	var lines []string
	warn := func(msg string) { lines = append(lines, msg) }

	discloseImplicitProviderForwards(warn, []any{1234}, []providerForward{
		{Port: 11434, Provider: "ollama"},
		{Port: 1234, Provider: "lmstudio"},
	})

	if len(lines) != 1 {
		t.Fatalf("want exactly one line — the implicit port only; got %d: %v", len(lines), lines)
	}
	for _, want := range []string{"11434", "ollama"} {
		if !strings.Contains(lines[0], want) {
			t.Errorf("the disclosure must name %q; got: %s", want, lines[0])
		}
	}
	if strings.Contains(lines[0], "lmstudio") {
		t.Errorf("a port the user declared must not be disclosed as implicit; got: %s", lines[0])
	}
}

// TestNoProvidersDisclosesNothing keeps the quiet path quiet: the line reports an action, so
// no action means no line.
func TestNoProvidersDisclosesNothing(t *testing.T) {
	var lines []string
	discloseImplicitProviderForwards(func(m string) { lines = append(lines, m) }, []any{8080}, nil)
	if len(lines) != 0 {
		t.Errorf("no implicit forwards must produce no output, got: %v", lines)
	}
}

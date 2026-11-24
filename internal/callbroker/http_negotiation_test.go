package callbroker

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

func recordedHTTPInputs(t *testing.T, path string) []httpClassifierInput {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []httpClassifierInput
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var input httpClassifierInput
		if err := json.Unmarshal([]byte(line), &input); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func TestHTTPClassifierNegotiationUsesActualHTTPFrontShape(t *testing.T) {
	call := fixtureHTTPCall([]byte("original fixture body"), true)
	old := fixtureHTTPAnswer(true, digestHTTPBody(true, call.Body), "", "original")
	advertisement := strings.Replace(old, "classifier/1", "classifier/2", 1)
	typed := strings.Replace(advertisement, `"reads":[[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]]`, `"reads":[{"cell":"list","chain":[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]}]`, 1)
	for _, tc := range []struct {
		name    string
		cells   []loopholedecl.CallBrokerCell
		answers []string
		paths   []string
		calls   int
		valid   bool
	}{
		{"N2-zero-stays-old", nil, []string{advertisement}, []string{"/graphql"}, 1, true},
		{"N3-first-typed-shape-refused", nil, []string{typed}, []string{"/graphql"}, 1, false},
		{"N4-one-old", explicitReadCells()[:1], []string{old}, []string{"/graphql"}, 1, true},
		{"N5-many-upgrades", explicitReadCells(), []string{advertisement, typed}, []string{"/graphql"}, 2, true},
		{"N5a-method-floor", explicitReadCells(), []string{advertisement}, nil, 1, false},
		{"N5b-second-unknown", explicitReadCells(), []string{advertisement, strings.Replace(typed, `"cell":"list"`, `"cell":"foreign"`, 1)}, []string{"/graphql"}, 2, false},
		{"N6-many-old", explicitReadCells(), []string{old}, []string{"/graphql"}, 1, false},
		{"N9-downgrade", explicitReadCells(), []string{advertisement, old}, []string{"/graphql"}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, log := countingClassifierScript(t, tc.answers)
			classifier := fixtureHTTPClassifier(t, script)
			classifier.Cells, classifier.ReadOverPost = tc.cells, tc.paths
			result, err := classifier.ClassifyHTTP(context.Background(), call, []string{"content-type"})
			if (err == nil) != tc.valid {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			inputs := recordedHTTPInputs(t, log)
			if len(inputs) != tc.calls || inputs[0].Version != legacyClassifierVersion || inputs[0].Front != "http" {
				t.Fatalf("inputs=%+v", inputs)
			}
			if len(inputs) == 2 {
				if inputs[1].Version != typedClassifierVersion || len(inputs[1].Facts) != 0 {
					t.Fatal("HTTP upgrade missed /2 or added facts")
				}
				inputs[1].Version = inputs[0].Version
				if !reflect.DeepEqual(inputs[0], inputs[1]) {
					t.Fatal("HTTP upgrade changed request, body, roots or facts")
				}
			}
			if !tc.valid && (len(result.Operations) != 0 || result.Canonical.Method != "") {
				t.Fatal("invalid protocol reached a canonical call")
			}
		})
	}
}

func TestHTTPClassifierNegotiationDiscardsProvisionalLookupBeforeReader(t *testing.T) {
	call := fixtureHTTPCall(nil, false)
	lookup := `{"version":"yolo.call-broker-classifier/2","lookups":[{"name":"parent","arguments":["discard"]}]}`
	refusal := `{"version":"yolo.call-broker-classifier/2","refused":{"class":"unsupported","reason":"no data","next_step":"update the pack"}}`
	script, log := countingClassifierScript(t, []string{lookup, refusal})
	classifier := fixtureHTTPClassifier(t, script)
	classifier.Cells = explicitReadCells()
	classifier.Lookups = []loopholedecl.CallBrokerLookup{{Name: "parent", HTTPResource: "task", HTTPMethod: "GET", HTTPPath: "/tasks/{id}"}}
	reads := 0
	result, err := classifier.ClassifyHTTPWithLookups(context.Background(), call, []string{"content-type"}, func(context.Context, loopholedecl.CallBrokerLookup, []string) (json.RawMessage, error) {
		reads++
		return json.RawMessage(`{}`), nil
	})
	if err != nil || result.Refused == nil || reads != 0 || len(recordedHTTPInputs(t, log)) != 2 {
		t.Fatalf("provisional lookup leaked: result=%+v err=%v reads=%d", result, err, reads)
	}
}

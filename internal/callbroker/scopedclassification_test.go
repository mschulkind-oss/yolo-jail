package callbroker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func TestScopedClassificationCLINamedReadProtocol(t *testing.T) {
	raw := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/2","classification":{"argv":["list"],"stdin_sha256":"%x","reads":[{"cell":"list","chain":[{"type":"project","id":"team:alpha"}],"description":"list project"}]}}`, sha256.Sum256(nil))
	classifier := fixtureClassifier(t, typedAnswerScript(t, raw, ""))
	classifier.Cells = []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}}
	answer, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, nil)
	if err != nil || answer.Classification == nil || len(answer.Classification.Reads) != 1 {
		t.Fatalf("typed named read refused: answer=%+v err=%v", answer, err)
	}
}

func TestScopedClassificationReadCellCannotBecomeWrite(t *testing.T) {
	raw := strings.Replace(fixtureAnswer(nil, "team:alpha"), `"cell":"update"`, `"cell":"list"`, 1)
	classifier := fixtureClassifier(t, "cat >/dev/null; printf '%s' '"+raw+"'")
	classifier.Cells = []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}}
	if _, err := classifier.ClassifyCLI(context.Background(), []string{"update", "a1"}, nil, nil); err == nil {
		t.Fatal("read declaration was accepted as a write cell")
	}
}

func TestScopedClassificationReadCardinalityAtCLIDecoder(t *testing.T) {
	for _, tc := range []struct {
		name, read, version string
		cells               []loopholedecl.CallBrokerCell
		grant, valid        bool
	}{
		{"zero-granted-old", `[{"type":"project","id":"team:alpha"}]`, "1", nil, true, true},
		{"zero-without-grant", `[{"type":"project","id":"team:alpha"}]`, "1", nil, false, false},
		{"zero-named", `{"cell":"list","chain":[{"type":"project","id":"team:alpha"}]}`, "2", nil, true, false},
		{"one-old", `[{"type":"project","id":"team:alpha"}]`, "1", []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}}, false, true},
		{"many-old-ambiguous", `[{"type":"project","id":"team:alpha"}]`, "1", []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}, {Name: "get", Kind: "read"}}, true, false},
		{"many-named", `{"cell":"get","chain":[{"type":"project","id":"team:alpha"}]}`, "2", []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}, {Name: "get", Kind: "read"}}, false, true},
		{"write-named-read", `{"cell":"update","chain":[{"type":"project","id":"team:alpha"}]}`, "2", []loopholedecl.CallBrokerCell{{Name: "update", Tables: []string{"all"}}}, true, false},
		{"wrong-kind", `{"kind":"write","chain":[{"type":"project","id":"team:alpha"}]}`, "2", nil, true, false},
		{"null-kind", `{"kind":null,"chain":[{"type":"project","id":"team:alpha"}]}`, "2", nil, true, false},
		{"duplicate-cell", `{"cell":"list","cell":"get","chain":[{"type":"project","id":"team:alpha"}]}`, "2", nil, true, false},
		{"missing-chain", `{"cell":"list"}`, "2", nil, true, false},
		{"outside-root", `[{"type":"project","id":"team:beta"}]`, "1", nil, true, false},
		{"root-sibling", `[{"type":"project","id":"team:alpha/"}]`, "1", nil, true, false},
		{"repeated-type", `[{"type":"project","id":"team:alpha"},{"type":"project","id":"team:alpha"}]`, "1", nil, true, false},
		{"wrong-parent", `[{"type":"task","id":"a1"},{"type":"task","id":"team:alpha"}]`, "1", nil, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/%s","classification":{"argv":["list"],"stdin_sha256":"%x","reads":[%s]}}`, tc.version, sha256.Sum256(nil), tc.read)
			script := "cat >/dev/null; printf '%s' '" + raw + "'"
			if tc.version == "2" {
				for _, cell := range tc.cells {
					if cell.IsRead() {
						script = typedAnswerScript(t, raw, "")
						break
					}
				}
			}
			classifier := fixtureClassifier(t, script)
			classifier.Cells, classifier.ReadGrant = tc.cells, tc.grant
			answer, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, nil)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v answer=%+v err=%v", tc.valid, answer, err)
			}
			if !tc.valid {
				return
			}
			ops := answer.Classification.Operations
			if len(ops) != 1 || ops[0].Kind != brokerpolicy.Read || ops[0].Description == "" {
				t.Fatalf("typed normalization missing: %+v", ops)
			}
			if len(tc.cells) == 1 && ops[0].Cell != tc.cells[0].Name {
				t.Fatal("one read cell did not absorb old uncelled read")
			}
			matcher, _ := brokerpolicy.NewScopeMatcher("exact", "", nil)
			var cells []brokerpolicy.Cell
			for _, cell := range tc.cells {
				cells = append(cells, brokerpolicy.Cell{Name: cell.Name, Kind: brokerpolicy.Read})
			}
			policy, err := brokerpolicy.NewPolicy(classifier.Source, matcher, cells, tc.grant, false, []brokerpolicy.Rule{{Source: classifier.Source, Kind: brokerpolicy.Read, Cell: ops[0].Cell, Value: brokerpolicy.OnRequest, Origin: "synthetic user ceiling"}}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if decision := policy.Decide(ops, "", nil); decision.Decision != brokerpolicy.Hold || len(decision.Operations) != 1 {
				t.Fatalf("validated producer did not reach shared decision: %+v", decision)
			}
		})
	}
}

func TestScopedClassificationHTTPMethodFloorAndTypedReport(t *testing.T) {
	call := fixtureHTTPCall(nil, false)
	raw := fixtureHTTPAnswer(false, digestHTTPBody(false, nil), "", "original")
	raw = strings.Replace(raw, "classifier/1", "classifier/2", 1)
	raw = strings.Replace(raw, `"reads":[[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]]`, `"reads":[{"cell":"list","chain":[{"type":"task","id":"a1"},{"type":"project","id":"team:alpha"}]}]`, 1)
	for _, tc := range []struct {
		name  string
		paths []string
		valid bool
	}{{"declared-post", []string{"/graphql"}, true}, {"undeclared-post", nil, false}} {
		t.Run(tc.name, func(t *testing.T) {
			classifier := fixtureHTTPClassifier(t, typedAnswerScript(t, raw, ""))
			classifier.Cells = []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}}
			classifier.ReadOverPost = tc.paths
			result, err := classifier.ClassifyHTTP(context.Background(), call, []string{"content-type"})
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v result=%+v err=%v", tc.valid, result, err)
			}
			if tc.valid && (len(result.Operations) != 1 || result.Operations[0].Cell != "list") {
				t.Fatal("HTTP typed normalization missing")
			}
		})
	}
}

func TestScopedClassificationInvalidCellRefusesBeforeActualCLIHandlerEffect(t *testing.T) {
	h, _, marker, store := testCallHandler(t)
	raw := strings.Replace(fixtureAnswer(nil, "team:alpha"), `"cell":"update"`, `"cell":"list"`, 1)
	classifier := fixtureClassifier(t, "cat >/dev/null; printf '%s' '"+raw+"'")
	classifier.Cells = []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}}
	h.classifier = classifier
	_, _, err := h.serveCLI(context.Background(), "act", "jail", []string{"update", "a1"}, nil)
	if err == nil {
		t.Fatal("read-as-write reached handler")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("service effect occurred: %v", err)
	}
	requests, _, err := store.List()
	if err != nil || len(requests) != 0 {
		t.Fatalf("invalid protocol filed requests: %v %v", requests, err)
	}
}

// typedAnswerScript advertises with an old-shaped answer, then emits a /2
// body only after observing a /2 request at the actual process boundary.
func typedAnswerScript(t *testing.T, typed, inputLog string) string {
	t.Helper()
	var legacy map[string]any
	if err := json.Unmarshal([]byte(typed), &legacy); err != nil {
		t.Fatal(err)
	}
	if classification, ok := legacy["classification"].(map[string]any); ok {
		if reads, ok := classification["reads"].([]any); ok {
			for i, read := range reads {
				if descriptor, ok := read.(map[string]any); ok {
					reads[i] = descriptor["chain"]
				}
			}
		}
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	log := ""
	if inputLog != "" {
		log = "printf '%s\\n' \"$request\" >> " + shquote.Quote(inputLog) + "\n"
	}
	return "request=$(cat)\n" + log + "case \"$request\" in *yolo.call-broker-classifier/2*) printf '%s' " + shquote.Quote(typed) + " ;; *) printf '%s' " + shquote.Quote(string(raw)) + " ;; esac\n"
}

func TestScopedClassificationCLIDuplicateTopFieldRefuses(t *testing.T) {
	raw := `{"version":"yolo.call-broker-classifier/1","version":"yolo.call-broker-classifier/1","local":true}`
	classifier := fixtureClassifier(t, "printf '%s' '"+raw+"'")
	if _, err := classifier.ClassifyCLI(context.Background(), []string{"--version"}, nil, nil); err == nil {
		t.Fatal("duplicate top-level protocol field accepted")
	}
}

package callbroker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// BSD wc (macOS) pads its count with leading spaces; the counting fixture must
// still select its numbered answer. A padding wc first on PATH reproduces it.
func TestCountingClassifierScriptToleratesPaddedLineCounts(t *testing.T) {
	bin := canonicalTempDir(t)
	realWC, err := exec.LookPath("wc")
	if err != nil {
		t.Fatal(err)
	}
	shim := "#!/bin/sh\nn=$(" + shquote.Quote(realWC) + " \"$@\" | tr -d ' ')\nprintf '%8s\\n' \"$n\"\n"
	if err := os.WriteFile(filepath.Join(bin, "wc"), []byte(shim), 0o755); err != nil {
		t.Fatal(err)
	}
	script, _ := countingClassifierScript(t, []string{"first", "second"})
	cmd := exec.Command("/bin/sh", "-c", script)
	cmd.Env = testtmp.ChildEnv([]string{"PATH=" + bin + ":" + os.Getenv("PATH")})
	cmd.Stdin = strings.NewReader("{}")
	out, err := cmd.CombinedOutput()
	if err != nil || string(out) != "first" {
		t.Fatalf("padded wc broke the counting fixture: out=%q err=%v", out, err)
	}
}

func countingClassifierScript(t *testing.T, answers []string) (string, string) {
	t.Helper()
	log := filepath.Join(canonicalTempDir(t), "requests.jsonl")
	script := "request=$(cat)\nprintf '%s\\n' \"$request\" >> " + shquote.Quote(log) + "\ncount=$(( $(wc -l < " + shquote.Quote(log) + ") ))\ncase \"$count\" in\n"
	for i, answer := range answers {
		script += fmt.Sprintf("%d) printf '%%s' %s ;;\n", i+1, shquote.Quote(answer))
	}
	script += "*) exit 13 ;;\nesac\n"
	return script, log
}

func recordedCLIInputs(t *testing.T, path string) []CLIInput {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []CLIInput
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		var input CLIInput
		if err := json.Unmarshal([]byte(line), &input); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
	}
	return inputs
}

func negotiationRead(label string, typed bool, cell, root string) string {
	read := fmt.Sprintf(`[{"type":"project","id":%q}]`, root)
	if typed {
		name := ""
		if cell != "" {
			name = fmt.Sprintf(`"cell":%q,`, cell)
		}
		read = "{" + name + `"chain":` + read + "}"
	}
	return fmt.Sprintf(`{"version":%q,"classification":{"argv":["list"],"stdin_sha256":"%x","reads":[%s]}}`, label, sha256.Sum256(nil), read)
}

func explicitReadCells() []loopholedecl.CallBrokerCell {
	return []loopholedecl.CallBrokerCell{{Name: "list", Kind: "read"}, {Name: "get", Kind: "read"}}
}

func TestClassifierNegotiationRealProcessMatrix(t *testing.T) {
	old := negotiationRead(legacyClassifierVersion, false, "", "team:alpha")
	advertisement := negotiationRead(typedClassifierVersion, false, "", "team:alpha")
	typed := negotiationRead(typedClassifierVersion, true, "list", "team:alpha")
	for _, tc := range []struct {
		name      string
		cells     []loopholedecl.CallBrokerCell
		grant     bool
		answers   []string
		wantCalls int
		wantError bool
	}{
		{"N1-old-zero", nil, true, []string{old}, 1, false},
		{"N2-advertising-zero", nil, true, []string{advertisement}, 1, false},
		{"N3-typed-body-on-first", nil, true, []string{typed}, 1, true},
		{"N4-one-old-absorbs", explicitReadCells()[:1], false, []string{old}, 1, false},
		{"N5-many-upgrades", explicitReadCells(), false, []string{advertisement, typed}, 2, false},
		{"N5a-outside-root", explicitReadCells(), true, []string{negotiationRead(typedClassifierVersion, false, "", "team:beta")}, 1, true},
		{"N5a-wrong-hash", explicitReadCells(), true, []string{strings.Replace(advertisement, fmt.Sprintf("%x", sha256.Sum256(nil)), strings.Repeat("0", 64), 1)}, 1, true},
		{"N5a-invalid-argv", explicitReadCells(), true, []string{strings.Replace(advertisement, `["list"]`, `[]`, 1)}, 1, true},
		{"N5b-typed-uncelled", explicitReadCells(), true, []string{advertisement, negotiationRead(typedClassifierVersion, true, "", "team:alpha")}, 2, true},
		{"N5b-typed-unknown", explicitReadCells(), true, []string{advertisement, negotiationRead(typedClassifierVersion, true, "foreign", "team:alpha")}, 2, true},
		{"N6-old-many-no-upgrade", explicitReadCells(), true, []string{old}, 1, true},
		{"N7-refusal-final", explicitReadCells(), true, []string{`{"version":"yolo.call-broker-classifier/2","refused":{"class":"unsupported","reason":"not supported","next_step":"update the pack"}}`}, 1, false},
		{"N9-downgrade", explicitReadCells(), true, []string{advertisement, old}, 2, true},
		{"zero-no-grant", nil, false, []string{advertisement}, 1, true},
		{"unexpected-label", explicitReadCells(), true, []string{strings.Replace(advertisement, "classifier/2", "classifier/3", 1)}, 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, log := countingClassifierScript(t, tc.answers)
			classifier := fixtureClassifier(t, script)
			classifier.Cells, classifier.ReadGrant = tc.cells, tc.grant
			answer, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("answer=%+v err=%v", answer, err)
			}
			inputs := recordedCLIInputs(t, log)
			if len(inputs) != tc.wantCalls || inputs[0].Version != legacyClassifierVersion {
				t.Fatalf("requests=%+v", inputs)
			}
			if len(inputs) == 2 {
				if inputs[1].Version != typedClassifierVersion || len(inputs[1].Facts) != 0 {
					t.Fatal("upgrade added facts or missed /2")
				}
				inputs[1].Version = inputs[0].Version
				if !reflect.DeepEqual(inputs[0], inputs[1]) {
					t.Fatalf("upgrade changed metadata: %+v", inputs)
				}
			}
			if tc.name == "N6-old-many-no-upgrade" && !strings.Contains(err.Error(), "update the pack's classifier") {
				t.Fatal("many-cell refusal lost next step")
			}
		})
	}
}

func TestClassifierNegotiationReadFreeAndLocalFirstAnswersDoNotUpgrade(t *testing.T) {
	for _, local := range []bool{false, true} {
		argv := []string{"update", "a1"}
		answer := strings.Replace(fixtureAnswer(nil, "team:alpha"), "classifier/1", "classifier/2", 1)
		if local {
			argv = []string{"--version"}
			answer = `{"version":"yolo.call-broker-classifier/2","local":true}`
		}
		script, log := countingClassifierScript(t, []string{answer})
		classifier := fixtureClassifier(t, script)
		classifier.Cells = append(explicitReadCells(), loopholedecl.CallBrokerCell{Name: "update", Tables: []string{"all"}})
		result, err := classifier.ClassifyCLI(context.Background(), argv, nil, nil)
		if err != nil || result.Local != local || len(recordedCLIInputs(t, log)) != 1 {
			t.Fatalf("N7 read-free/local answer was upgraded: %+v %v", result, err)
		}
	}
}

func TestClassifierNegotiationProvisionalLookupIsNeverUsed(t *testing.T) {
	lookup := `{"version":"yolo.call-broker-classifier/2","lookups":[{"name":"task-project","arguments":["provisional"]}]}`
	for _, second := range []string{negotiationRead(typedClassifierVersion, true, "list", "team:alpha"), negotiationRead(typedClassifierVersion, true, "foreign", "team:alpha")} {
		script, log := countingClassifierScript(t, []string{lookup, second})
		classifier := fixtureClassifier(t, script)
		classifier.Cells = explicitReadCells()
		calls := 0
		answer, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, func(context.Context, loopholedecl.CallBrokerLookup, []string) (json.RawMessage, error) {
			calls++
			return json.RawMessage(`{}`), nil
		})
		if calls != 0 || len(recordedCLIInputs(t, log)) != 2 {
			t.Fatal("N8 provisional lookup was used")
		}
		if strings.Contains(second, "foreign") && err == nil {
			t.Fatal("N9 invalid typed successor accepted")
		}
		if err == nil && len(answer.Classification.Operations) != 1 {
			t.Fatal("typed successor did not normalize")
		}
	}
}

func TestClassifierNegotiationLookupAndFactBudgetsAtRealBoundary(t *testing.T) {
	lookup := func(label, arg string) string {
		return fmt.Sprintf(`{"version":%q,"lookups":[{"name":"task-project","arguments":[%q]}]}`, label, arg)
	}
	for _, tc := range []struct {
		name                 string
		answers              []string
		wantCalls, wantReads int
		valid                bool
	}{
		{"N12-three-rounds", []string{lookup(typedClassifierVersion, "discard"), lookup(typedClassifierVersion, "one"), lookup(typedClassifierVersion, "two"), lookup(typedClassifierVersion, "three"), negotiationRead(typedClassifierVersion, true, "list", "team:alpha")}, 5, 3, true},
		{"N12-no-sixth-process", []string{lookup(typedClassifierVersion, "discard"), lookup(typedClassifierVersion, "one"), lookup(typedClassifierVersion, "two"), lookup(typedClassifierVersion, "three"), lookup(typedClassifierVersion, "four")}, 5, 3, false},
		{"N10-flipped-label", []string{lookup(legacyClassifierVersion, "one"), negotiationRead(typedClassifierVersion, false, "", "team:alpha")}, 2, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script, log := countingClassifierScript(t, tc.answers)
			classifier := fixtureClassifier(t, script)
			if !strings.HasPrefix(tc.name, "N10") {
				classifier.Cells = explicitReadCells()
			}
			reads := 0
			_, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, func(_ context.Context, _ loopholedecl.CallBrokerLookup, args []string) (json.RawMessage, error) {
				if args[0] == "discard" {
					t.Fatal("provisional lookup was performed")
				}
				reads++
				return json.RawMessage(`{"project":"team:alpha"}`), nil
			})
			inputs := recordedCLIInputs(t, log)
			if (err == nil) != tc.valid || len(inputs) != tc.wantCalls || reads != tc.wantReads {
				t.Fatalf("err=%v inputs=%+v reads=%d", err, inputs, reads)
			}
			for _, input := range inputs[1:] {
				if !strings.HasPrefix(tc.name, "N10") && input.Version != typedClassifierVersion {
					t.Fatal("typed upgrade did not persist")
				}
			}
		})
	}
}

func TestClassifierNegotiationFourFactCapIsIndependentOfLookupRounds(t *testing.T) {
	answer := `{"version":"yolo.call-broker-classifier/1","lookups":[`
	for i := 0; i < 5; i++ {
		if i != 0 {
			answer += ","
		}
		answer += fmt.Sprintf(`{"name":"task-project","arguments":[%q]}`, fmt.Sprint(i))
	}
	answer += `]}`
	script, log := countingClassifierScript(t, []string{answer})
	classifier := fixtureClassifier(t, script)
	reads := 0
	_, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, func(context.Context, loopholedecl.CallBrokerLookup, []string) (json.RawMessage, error) {
		reads++
		return json.RawMessage(`{}`), nil
	})
	if err == nil || reads != 4 || len(recordedCLIInputs(t, log)) != 1 {
		t.Fatalf("four-fact bound changed: err=%v reads=%d", err, reads)
	}
}

func TestClassifierNegotiationProvisionalReadCannotReachActualHandler(t *testing.T) {
	h, _, marker, store := testCallHandler(t)
	// This fixture program records every service invocation, not a real provider.
	program := filepath.Join(filepath.Dir(marker), "bin", "tasks")
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf 'effect\\n' >> "+shquote.Quote(marker)+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	first := strings.Replace(negotiationRead(typedClassifierVersion, false, "", "team:alpha"), `["list"]`, `["read"]`, 1)
	second := strings.Replace(negotiationRead(typedClassifierVersion, true, "foreign", "team:alpha"), `["list"]`, `["read"]`, 1)
	script, log := countingClassifierScript(t, []string{first, second})
	classifier := fixtureClassifier(t, script)
	classifier.Cells = explicitReadCells()[:1]
	h.classifier = classifier
	_, _, err := h.serveCLI(context.Background(), "act", "jail", []string{"read"}, nil)
	if err == nil {
		t.Fatal("N5c provisional answer or invalid successor reached handler decision")
	}
	if len(recordedCLIInputs(t, log)) != 2 {
		t.Fatal("N5c upgrade did not discard the provisional answer")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("N5c provisional answer executed provider: %v", err)
	}
	requests, _, err := store.List()
	if err != nil || len(requests) != 0 {
		t.Fatalf("N5c provisional answer filed request: %v %v", requests, err)
	}
}

func TestClassifierNegotiationOldProducerCannotContinueWithDeniedLookup(t *testing.T) {
	lookup := `{"version":"yolo.call-broker-classifier/1","lookups":[{"name":"task-project","arguments":["one"]}]}`
	script, log := countingClassifierScript(t, []string{lookup})
	classifier := fixtureClassifier(t, script)
	reads := 0
	_, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, func(context.Context, loopholedecl.CallBrokerLookup, []string) (json.RawMessage, error) {
		reads++
		return nil, fmt.Errorf("read line denies this lookup")
	})
	if err == nil || !strings.Contains(err.Error(), "check read access") || reads != 1 || len(recordedCLIInputs(t, log)) != 1 {
		t.Fatalf("N11 old producer was continued with denied lookup: %v reads=%d", err, reads)
	}
}

func TestClassifierNegotiationNoCapabilityCacheOnReplay(t *testing.T) {
	advertisement := negotiationRead(typedClassifierVersion, false, "", "team:alpha")
	typed := negotiationRead(typedClassifierVersion, true, "list", "team:alpha")
	script, log := countingClassifierScript(t, []string{advertisement, typed, advertisement, typed})
	classifier := fixtureClassifier(t, script)
	classifier.Cells = explicitReadCells()
	for i := 0; i < 2; i++ {
		if _, err := classifier.ClassifyCLI(context.Background(), []string{"list"}, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	inputs := recordedCLIInputs(t, log)
	if len(inputs) != 4 || inputs[2].Version != legacyClassifierVersion {
		t.Fatal("replay reused a capability")
	}
}

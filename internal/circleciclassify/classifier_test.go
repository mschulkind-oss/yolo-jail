package circleciclassify

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

func classifierInput(argv ...string) callbroker.CLIInput {
	return callbroker.CLIInput{
		Version:     protocolVersion,
		Front:       "cli",
		Argv:        argv,
		StdinSHA256: strings.Repeat("a", 64),
		Roots:       []string{"gh/acme/app"},
	}
}

func TestCircleCIClassifierAllowsOnlyNamedScopedReadGrammar(t *testing.T) {
	for _, argv := range [][]string{
		{"run", "list", "--project", "gh/acme/app"},
		{"run", "list", "--project=gh/acme/app", "--branch", "main", "--json"},
		{"run", "list", "-b", "release/1", "--project", "gh/acme/app"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			answer := Classify(classifierInput(argv...))
			if answer.Refused != nil || answer.Classification == nil {
				t.Fatalf("scoped run-list was not classified: %+v", answer)
			}
			got := answer.Classification
			if !equalStrings(got.Argv, argv) || got.StdinSHA256 != strings.Repeat("a", 64) || len(got.Reads) != 1 || len(got.Writes) != 0 {
				t.Fatalf("classification changed call or is not a read: %+v", got)
			}
			if len(got.Reads[0]) != 1 || got.Reads[0][0] != (callbroker.Resource{Type: "project", ID: "gh/acme/app"}) {
				t.Fatalf("read resource is not the approved project root: %+v", got.Reads)
			}
		})
	}
}

func TestCircleCIClassifierRefusesUnscopedUnknownAndWriteGrammar(t *testing.T) {
	for _, argv := range [][]string{
		{"run", "list"},
		{"run", "list", "--project", "gh/elsewhere/app"},
		{"api", "/me"},
		{"auth", "login"},
		{"run", "list", "--host", "https://example.invalid"},
		{"run", "list", "--token", "synthetic"},
		{"run", "list", "-o", "result.json"},
		{"run", "list", "--output", "result.json"},
		{"run", "list", "--unknown"},
		{"run", "list", "--current-branch"},
		{"workflow", "rerun", "wf1"},
		{"run", "approve", "job1"},
		{"pipeline", "create"},
		{"future", "subcommand"},
	} {
		t.Run(strings.Join(argv, " "), func(t *testing.T) {
			answer := Classify(classifierInput(argv...))
			if answer.Refused == nil || answer.Classification != nil || len(answer.Lookups) != 0 {
				t.Fatalf("unsupported grammar was not refused before execution: %+v", answer)
			}
			if answer.Refused.NextStep == "" {
				t.Fatalf("refusal omitted a next step: %+v", answer.Refused)
			}
		})
	}
}

func TestCircleCIClassifierUsesActualProtocolDispatch(t *testing.T) {
	input := classifierInput("run", "list", "--project", "gh/acme/app")
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if rc := Run([]string{"circleci"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
		t.Fatalf("classifier dispatch rc=%d stderr=%s", rc, stderr.String())
	}
	var answer callbroker.Answer
	if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
		t.Fatalf("classifier dispatch emitted invalid protocol: %v (%q)", err, stdout.String())
	}
	if answer.Classification == nil || answer.Classification.Reads[0][0].ID != "gh/acme/app" {
		t.Fatalf("actual dispatch did not classify the scoped read: %+v", answer)
	}
}

func TestCircleCIClassifierRejectsMalformedProtocol(t *testing.T) {
	for _, raw := range []string{
		`{"version":"yolo.call-broker-classifier/1","front":"cli","argv":["run","list","--project","gh/acme/app"],"roots":["gh/acme/app"],"extra":true}`,
		`{"version":"yolo.call-broker-classifier/1","front":"cli","argv":["run","list","--project","gh/acme/app"],"roots":["gh/acme/app"]}{}`,
	} {
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{"circleci"}, strings.NewReader(raw), &stdout, &stderr); rc != 2 || stdout.Len() != 0 {
			t.Fatalf("malformed input rc=%d stdout=%q stderr=%q", rc, stdout.String(), stderr.String())
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

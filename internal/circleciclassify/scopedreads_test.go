package circleciclassify

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

func TestCircleCIRunAdvertisesThenNamesOnlyExistingListing(t *testing.T) {
	input := callbroker.CLIInput{Front: "cli", Argv: []string{"run", "list", "--project", "gh/acme/repo", "--json"}, Roots: []string{"gh/acme/repo"}, StdinSHA256: "fixture-digest"}
	var answers []callbroker.Answer
	for _, version := range []string{protocolVersion, typedProtocolVersion} {
		input.Version = version
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{"circleci"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
			t.Fatalf("rc=%d stderr=%s", rc, stderr.String())
		}
		var answer callbroker.Answer
		if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil {
			t.Fatal(err)
		}
		if answer.Version != typedProtocolVersion || answer.Classification == nil {
			t.Fatalf("not advertised/classified: %+v", answer)
		}
		answers = append(answers, answer)
	}
	legacy, typed := answers[0].Classification, answers[1].Classification
	if len(legacy.ReadDescriptors) != 0 || len(typed.ReadDescriptors) != 1 || typed.ReadDescriptors[0].Cell != "list_runs" {
		t.Fatalf("bad version-dispatched shape: %+v %+v", legacy, typed)
	}
	if !reflect.DeepEqual(legacy.Argv, typed.Argv) || !reflect.DeepEqual(legacy.Reads, typed.Reads) || legacy.StdinSHA256 != typed.StdinSHA256 {
		t.Fatal("listing labels changed grammar or admission")
	}
	for _, argv := range [][]string{{"workflow", "rerun", "id"}, {"run", "list", "--project", "gh/acme/other"}, {"run", "list", "--project", "gh/acme/repo", "--token", "secret"}} {
		input.Argv = argv
		raw, _ := json.Marshal(input)
		var stdout, stderr bytes.Buffer
		if rc := Run([]string{"circleci"}, bytes.NewReader(raw), &stdout, &stderr); rc != 0 {
			t.Fatalf("unexpected dispatch failure: %s", stderr.String())
		}
		var answer callbroker.Answer
		if err := json.Unmarshal(stdout.Bytes(), &answer); err != nil || answer.Refused == nil || answer.Classification != nil {
			t.Fatalf("unbuilt grammar or foreign root accepted: %+v %v", answer, err)
		}
	}
}

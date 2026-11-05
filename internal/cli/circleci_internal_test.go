package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

func TestRunInternalDispatchesBuiltinCircleCIClassifier(t *testing.T) {
	input := callbroker.CLIInput{
		Version: "yolo.call-broker-classifier/1", Front: "cli",
		Argv:        []string{"run", "list", "--project", "gh/acme/app"},
		StdinSHA256: strings.Repeat("a", 64), Roots: []string{"gh/acme/app"},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/classifier-input.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	oldStdin := os.Stdin
	os.Stdin = in
	t.Cleanup(func() { os.Stdin = oldStdin })

	rc, stdout, stderr := runMainCaptured(t, "internal", "call-broker-classify", "circleci")
	if rc != 0 {
		t.Fatalf("internal classifier rc=%d stderr=%q", rc, stderr)
	}
	var answer callbroker.Answer
	if err := json.Unmarshal([]byte(stdout), &answer); err != nil {
		t.Fatalf("internal classifier emitted invalid protocol: %v (%q)", err, stdout)
	}
	if answer.Classification == nil || len(answer.Classification.Reads) != 1 ||
		answer.Classification.Reads[0][0].ID != "gh/acme/app" {
		t.Fatalf("internal command did not reach the actual scoped classifier: %+v", answer)
	}
}

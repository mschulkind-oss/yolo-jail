package cli

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
)

func pipeStdin(t *testing.T, data []byte) {
	t.Helper()
	path := t.TempDir() + "/classifier-input.json"
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { in.Close() })
	oldStdin := os.Stdin
	os.Stdin = in
	t.Cleanup(func() { os.Stdin = oldStdin })
}

func TestRunInternalDispatchesBuiltinS3Classifier(t *testing.T) {
	input := callbroker.CLIInput{
		Version: "yolo.call-broker-classifier/1", Front: "cli",
		Argv:        []string{"s3", "ls", "s3://bkt/x"},
		StdinSHA256: strings.Repeat("a", 64), Roots: []string{"s3://bkt"},
	}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	pipeStdin(t, data)

	rc, stdout, stderr := runMainCaptured(t, "internal", "call-broker-classify", "aws-s3")
	if rc != 0 {
		t.Fatalf("internal classifier rc=%d stderr=%q", rc, stderr)
	}
	var answer callbroker.Answer
	if err := json.Unmarshal([]byte(stdout), &answer); err != nil {
		t.Fatalf("internal classifier emitted invalid protocol: %v (%q)", err, stdout)
	}
	if answer.Classification == nil || len(answer.Classification.Reads) != 1 ||
		answer.Classification.Reads[0][0].ID != "s3://bkt/x" {
		t.Fatalf("internal command did not reach the S3 classifier: %+v", answer)
	}
}

func TestRunInternalClassifierDispatchRefusesUnknownSource(t *testing.T) {
	for _, args := range [][]string{
		{"internal", "call-broker-classify"},
		{"internal", "call-broker-classify", "nonesuch"},
		{"internal", "call-broker-classify", "aws-s3", "extra"},
	} {
		pipeStdin(t, []byte("{}"))
		rc, stdout, stderr := runMainCaptured(t, args...)
		if rc != 64 || stdout != "" || stderr == "" {
			t.Fatalf("%q rc=%d stdout=%q stderr=%q", args, rc, stdout, stderr)
		}
	}
}

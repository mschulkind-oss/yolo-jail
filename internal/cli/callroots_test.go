package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/callbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

func TestConfigCheckAndAddTOMLCallRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_IN_JAIL", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("packs=[\"aws\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ws := t.TempDir()
	t.Chdir(ws)
	file := filepath.Join(ws, config.WorkspaceTOMLConfigName)
	if err := os.WriteFile(file, []byte("[call_roots]\naws=[\"s3://bkt\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rc, out, errs := runMainCaptured(t, "config", "check")
	if rc != 0 {
		t.Fatalf("check rc=%d out=%s err=%s", rc, out, errs)
	}
	rc, out, errs = runMainCaptured(t, "config", "add", "call_roots.aws", "ce://host-login", "--project")
	if rc != 0 {
		t.Fatalf("add rc=%d out=%s err=%s", rc, out, errs)
	}
	read, err := config.ReadWorkspaceForGate(ws)
	if err != nil {
		t.Fatal(err)
	}
	roots := read.CallRoots("aws")
	if len(roots) != 2 || roots[1].Repo != "ce://host-login" {
		t.Fatalf("roots=%+v", roots)
	}
	if err := os.WriteFile(file, []byte("[brokered.aws]\nroots=[\"ce://host-login\"]\n"), 0600); err != nil {
		t.Fatal(err)
	}
	rc, out, errs = runMainCaptured(t, "config", "check")
	if rc == 0 || !strings.Contains(out+errs, "call_roots") {
		t.Fatalf("old spelling rc=%d out=%s err=%s", rc, out, errs)
	}
}
func TestRunInternalUnknownAWSServiceAdvice(t *testing.T) {
	for _, argv := range [][]string{{"ec2", "describe-instances"}, {"--version"}} {
		input := callbroker.CLIInput{Version: "yolo.call-broker-classifier/1", Front: "cli", Argv: argv, StdinSHA256: strings.Repeat("a", 64), Roots: []string{"ce://host-login"}}
		data, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		pipeStdin(t, data)
		rc, out, errs := runMainCaptured(t, "internal", "call-broker-classify", "aws")
		if rc != 0 {
			t.Fatalf("rc=%d err=%s", rc, errs)
		}
		var answer callbroker.Answer
		if err := json.Unmarshal([]byte(out), &answer); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(answer)
		if strings.Contains(string(encoded), "S3 broker") || (argv[0] != "--version" && (answer.Refused == nil || !strings.Contains(answer.Refused.NextStep, "ce") || !strings.Contains(answer.Refused.NextStep, "s3api"))) {
			t.Fatalf("advice=%s", encoded)
		}
	}
}

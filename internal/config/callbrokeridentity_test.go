package config

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestExplicitPackResolverCarriesSelectedProgramIdentityToGate(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "loopholes", "tasks")
	programDir := filepath.Join(root, "tools", "classifier")
	for _, dir := range []string{module, programDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	program := filepath.Join(programDir, "classifier")
	contents := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(program, contents, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestText := `{"name":"tasks","call_broker":{"source":"tasks","front":"cli","program":"tasks","classifier":["{program:classifier}"],"scope":{"roots":"declared"},"resource":[{"type":"project"}],"cell":[{"name":"tasks","tables":["working_set"],"default":{"working_set":false}}]}}`
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifestText), 0o644); err != nil {
		t.Fatal(err)
	}
	packText := `name = "fixture"
[[program]]
bin = "classifier"
via = "pack"
from = "tools/classifier"
run = "classifier"

[[loophole]]
from = "loopholes/tasks"
`
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte(packText), 0o644); err != nil {
		t.Fatal(err)
	}
	decl, problems := packdecl.DecodeTOML([]byte(packText))
	if len(problems) != 0 {
		t.Fatalf("fixture pack declaration: %v", problems)
	}
	pack := &packload.Pack{Name: "fixture", Root: root, Decl: decl}
	known, ok := (explicitPackLoopholeResolver{packs: []*packload.Pack{pack}}).Known()
	if !ok || known["tasks"].CallBroker == nil {
		t.Fatalf("explicit selected-pack resolver: ok=%v known=%+v", ok, known)
	}
	identity, found := known["tasks"].CallBroker.ProgramIdentities["classifier"]
	if !found || identity.Pack != "fixture" || identity.Path != program || identity.SHA256 != fmt.Sprintf("%x", sha256.Sum256(contents)) {
		t.Fatalf("selected staged program identity = %+v, found=%v", identity, found)
	}
	joined := strings.Join(CallBrokerDisclosure(known["tasks"].CallBroker), "\n")
	if !strings.Contains(joined, "host program=fixture/classifier sha256="+identity.SHA256) {
		t.Fatalf("explicit selected-pack gate disclosure omitted identity:\n%s", joined)
	}
	if err := os.Chmod(program, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_VERSION", "")
	errs, _ := ValidateConfig(decode(t, `{}`), t.TempDir(), explicitPackLoopholeResolver{packs: []*packload.Pack{pack}})
	if !strings.Contains(strings.Join(errs, "\n"), "program identity") {
		t.Fatalf("invalid selected program did not refuse config validation: %v", errs)
	}
}

func TestCallBrokerDisclosureIncludesResolvedProgramChecksum(t *testing.T) {
	broker := &loopholedecl.CallBroker{
		Source: "tasks", Front: "cli", Program: "tasks", Classifier: []string{"{program:classifier}"}, Executor: "core",
		ProgramIdentities: map[string]loopholedecl.CallBrokerProgramIdentity{
			"classifier": {Pack: "tasks-pack", Bin: "classifier", Path: "/secret/staged/tree/classifier", SHA256: strings.Repeat("a", 64)},
		},
	}
	joined := strings.Join(CallBrokerDisclosure(broker), "\n")
	if !strings.Contains(joined, "host program=tasks-pack/classifier sha256="+strings.Repeat("a", 64)) {
		t.Fatalf("gate omitted resolved pack program identity and checksum:\n%s", joined)
	}
	if strings.Contains(joined, "/secret/staged/tree") {
		t.Fatalf("gate disclosed a host staged-tree path:\n%s", joined)
	}
}

package run

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// A pre-stage discovery pass knows that the selected pack declares the broker, but it does
// not have the launch's staged root from which to attest the external classifier. Keep the
// declaration visible to config validation without manufacturing an identity or making it
// eligible for the launch-input carrier.
func TestLazyPackResolverKeepsUnresolvedCallBrokerMetadataOnly(t *testing.T) {
	home := packHome(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(root, "loopholes", "tasks")
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `name = "tasks"
transport = "loopback-tls"
access = "read tasks"
[call_broker]
source = "tasks"
front = "cli"
program = "tasks"
classifier = ["{program:classifier}"]
[call_broker.scope]
roots = "declared"
[[call_broker.resource]]
type = "project"
[[call_broker.cell]]
name = "update"
tables = ["all"]
default = { all = false }
[[grants]]
kind = "read"
text = "read tasks"
`
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	pack := "name = \"fixture\"\n[[program]]\nbin = \"classifier\"\nvia = \"pack\"\nfrom = \"tools/classifier\"\nrun = \"classifier\"\n[[loophole]]\nfrom = \"loopholes/tasks\"\n"
	if err := os.WriteFile(filepath.Join(root, "pack.toml"), []byte(pack), 0o644); err != nil {
		t.Fatal(err)
	}
	classifier := filepath.Join(root, "tools", "classifier", "classifier")
	if err := os.MkdirAll(filepath.Dir(classifier), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(classifier, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeUserPacks(t, home, `[ {"source":"file://`+root+`","name":"fixture"} ]`)

	var set loopholes.Set
	stderr := capturePackFallbackStderr(t, func() {
		modules := resolvePackLoopholeModules()
		var selected []loopholes.PackModule
		for _, module := range modules {
			if module.Pack == "fixture" {
				selected = append(selected, module)
			}
		}
		if len(selected) != 1 || selected[0].PackRoot != root {
			t.Fatalf("pre-stage fixture modules = %+v; want selected source root", selected)
		}
		// The live selected resolver carries a verified root. Exercise the typed
		// no-root fallback explicitly, without weakening the live selected path.
		selected[0].PackRoot = ""
		set = loopholes.NewSet(loopholes.DiscoverOptions{PackModules: selected})
	})
	if strings.Contains(stderr, "call-broker declaration is NOT active") || strings.Contains(stderr, "select that source's pack") {
		t.Fatalf("pre-stage selection emitted a false inactive/selection diagnostic: %s", stderr)
	}
	lp, ok := set.Lookup("tasks")
	if !ok || lp.CallBroker == nil {
		t.Fatalf("selected call-broker metadata was dropped before config validation: found=%v, loophole=%+v", ok, lp)
	}
	if lp.CallBroker.ProgramIdentityError == "" || !lp.CallBroker.ProgramIdentityPending || len(lp.CallBroker.ProgramIdentities) != 0 {
		t.Fatalf("pre-stage discovery must retain a pending identity failure without inventing identities: %+v", lp.CallBroker)
	}
	if _, err := selectedCallBrokerLaunchInput(lp, nil, nil, nil); err == nil {
		t.Fatal("unresolved pre-stage metadata must not publish a launch input")
	}

	badClassifier := filepath.Join(root, "tools", "classifier", "classifier")
	if err = os.Chmod(badClassifier, 0o644); err != nil {
		t.Fatal(err)
	}
	var invalidSet loopholes.Set
	invalidStderr := capturePackFallbackStderr(t, func() {
		invalidSet = loopholes.NewSet(loopholes.DiscoverOptions{PackModules: []loopholes.PackModule{{
			Dir: module, Pack: "fixture", PackRoot: root, HostExecApproved: true,
		}}})
	})
	_, invalidFound := invalidSet.Lookup("tasks")
	if invalidFound || !strings.Contains(invalidStderr, "call-broker declaration is NOT active") {
		t.Fatalf("invalid selected program was not diagnosed and dropped: found=%v stderr=%s", invalidFound, invalidStderr)
	}
}

func capturePackFallbackStderr(t *testing.T, fn func()) string {
	t.Helper()
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	previous := os.Stderr
	os.Stderr = writer
	defer func() { os.Stderr = previous }()
	fn()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data)
}

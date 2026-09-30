package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
)

// internalnodefloor_test.go pins `yolo internal node-floor-satisfied`'s one output: when it
// answers "no", stdout carries the interpreters that ARE available, because OQ-AR3's refusal
// (docs/reference/agent-program-runtimes.md) must name them and the generated bootstrap quotes
// this line rather than walking the mise store a second time in shell.
//
// Driven through runInternal, not runNodeFloorSatisfied, so the dispatcher's own argument — the
// stream it hands the function — is under test too: passing io.Discard there would pass a
// callee-only test and leave every refusal saying "none".

// A floor nothing on any machine satisfies: exit 1, and exactly one non-empty line on stdout.
// Its content is machine-dependent (whatever node this host has, or "none (…)"), so the test
// asserts the shape; entrypoint.TestAvailableNodes* pin the content against a fixture store.
func TestNodeFloorSatisfiedNamesWhatIsAvailableWhenItSaysNo(t *testing.T) {
	var rc int
	out := captureStdout(t, func() { rc = runInternal([]string{"node-floor-satisfied", "999"}) })
	if rc != 1 {
		t.Fatalf("rc = %d, want 1 (not satisfied)", rc)
	}
	if strings.TrimSpace(out) == "" {
		t.Error("an unsatisfied floor printed nothing on stdout, so the bootstrap's refusal " +
			"cannot say what IS available")
	}
	if strings.Count(out, "\n") != 1 || !strings.HasSuffix(out, "\n") {
		t.Errorf("want exactly one line on stdout for the refusal to quote, got %q", out)
	}
}

// `yolo internal node-floor-launchers` (AR-L5, docs/design/agent-program-runtimes.md) is the
// bootstrap's regeneration of a met floor's launchers. Driven through runInternal, so the verb's
// dispatch is under test: a floor-pending record for floor 99, a mise store holding a node 99.0.0
// (MISE_DATA_DIR, the variable the stage carries), and the launcher the verb writes is the
// record's segments joined with that interpreter.
func TestNodeFloorLaunchersFinishesAPendingLauncher(t *testing.T) {
	data := t.TempDir()
	node := filepath.Join(data, "installs", "node", "99.0.0", "bin", "node")
	if err := os.MkdirAll(filepath.Dir(node), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MISE_DATA_DIR", data)
	t.Setenv(entrypoint.DarwinLoginPathEnv, "")
	pending, launch := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(pending, "thing"),
		[]byte(`{"floor": "99", "segments": ["exec ", "\"$REAL_BIN\"\n"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if rc := runInternal([]string{"node-floor-launchers", "--pending=" + pending, "--launch=" + launch,
		"thing"}); rc != 0 {
		t.Fatalf("rc = %d, want 0", rc)
	}
	got, err := os.ReadFile(filepath.Join(launch, "thing"))
	if err != nil {
		t.Fatal(err)
	}
	if want := "exec " + node + " \"$REAL_BIN\"\n"; string(got) != want {
		t.Errorf("launcher = %q, want %q", got, want)
	}
	for _, args := range [][]string{
		{"node-floor-launchers"},
		{"node-floor-launchers", "--launch=" + launch, "thing"},
		{"node-floor-launchers", "--pending=" + pending, "--launch=" + launch},
		{"node-floor-launchers", "--pending=" + pending, "--launch=" + launch, "--bogus", "thing"},
	} {
		if rc := runInternal(args); rc != 2 {
			t.Errorf("%v: rc = %d, want 2 (misuse)", args, rc)
		}
	}
}

// Misuse keeps stdout empty: its diagnostics are stderr's, and a line on stdout would be
// quoted into a refusal as though it were a list of interpreters.
func TestNodeFloorSatisfiedMisuseLeavesStdoutEmpty(t *testing.T) {
	for _, args := range [][]string{
		{"node-floor-satisfied"},
		{"node-floor-satisfied", ">=22.19"},
	} {
		var rc int
		out := captureStdout(t, func() { rc = runInternal(args) })
		if rc != 2 {
			t.Errorf("%v: rc = %d, want 2 (misuse)", args, rc)
		}
		if out != "" {
			t.Errorf("%v: misuse wrote to stdout: %q", args, out)
		}
	}
}

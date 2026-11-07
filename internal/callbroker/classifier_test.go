package callbroker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// canonicalTempDir mints t.TempDir() with its symlinks resolved. A selected
// classifier's identity path must equal its EvalSymlinks form, and on macOS
// the temp root is under /var, a link to /private/var.
func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func fixtureClassifier(t *testing.T, body string) Classifier {
	t.Helper()
	path := filepath.Join(canonicalTempDir(t), "classifier")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return Classifier{Source: "local-tasks", Argv: []string{"{program:classifier}"},
		Identities: map[string]loopholedecl.CallBrokerProgramIdentity{"classifier": {
			Bin: "classifier", Path: path, SHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}},
		Roots: []string{"team:alpha"}, Resources: []loopholedecl.CallBrokerResource{{Type: "project"}, {Type: "task", Parent: "project"}},
		Cells:   []loopholedecl.CallBrokerCell{{Name: "update", Tables: []string{"all"}}},
		Lookups: []loopholedecl.CallBrokerLookup{{Name: "task-project", CLI: []string{"task", "get", "{id}"}}}}
}

func fixtureAnswer(stdin []byte, resource string) string {
	return fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"argv":["update","a1"],"stdin_sha256":"%x","writes":[{"cell":"update","chain":[{"type":"task","id":"a1"},{"type":"project","id":%q}],"description":"update a1"}]}}`, sha256.Sum256(stdin), resource)
}

func TestPackCLIClassifierNeverStartsPreCancelledCall(t *testing.T) {
	marker := filepath.Join(canonicalTempDir(t), "started")
	classifier := fixtureClassifier(t, fmt.Sprintf("printf started > %q", marker))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started := false
	originalStart := startOwnedCommand
	startOwnedCommand = func(cmd *exec.Cmd) error { started = true; return cmd.Start() }
	t.Cleanup(func() { startOwnedCommand = originalStart })
	_, err := classifier.ClassifyCLI(ctx, []string{"write"}, nil, nil)
	if started {
		t.Fatal("pre-cancelled classifier reached cmd.Start")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled classifier: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("pre-cancelled classifier executed: %v", err)
	}
}

func TestPackCLIClassifierValidatesCanonicalResourceAndDoesNotExecute(t *testing.T) {
	stdin := []byte("request input")
	calls := filepath.Join(canonicalTempDir(t), "calls")
	inputFile := filepath.Join(canonicalTempDir(t), "classifier-input")
	body := fmt.Sprintf("test -z \"$TOKEN_SHOULD_NOT_REACH_CLASSIFIER\" || exit 9\ncat > %q\nprintf '%%s' '%s'\n", inputFile, fixtureAnswer(stdin, "team:alpha"))
	classifier := fixtureClassifier(t, body)
	t.Setenv("TOKEN_SHOULD_NOT_REACH_CLASSIFIER", "secret")
	answer, err := classifier.ClassifyCLI(context.Background(), []string{"update", "a1", "--jail-flag"}, stdin, nil)
	if err != nil || answer.Classification == nil || len(answer.Classification.Writes) != 1 {
		t.Fatalf("validated answer=%+v err=%v", answer, err)
	}
	if _, err := os.Stat(calls); !os.IsNotExist(err) {
		t.Fatalf("classifying executed service call: %v", err)
	}
	input, err := os.ReadFile(inputFile)
	if err != nil || strings.Contains(string(input), "secret") || strings.Contains(string(input), string(stdin)) {
		t.Fatalf("classifier received credential or original stdin: %v", err)
	}
	for name, verdict := range map[string]string{
		"outside root":       fixtureAnswer(stdin, "team:beta"),
		"changed stdin":      strings.Replace(fixtureAnswer(stdin, "team:alpha"), fmt.Sprintf("%x", sha256.Sum256(stdin)), strings.Repeat("0", 64), 1),
		"unknown cell":       strings.Replace(fixtureAnswer(stdin, "team:alpha"), `"cell":"update"`, `"cell":"delete"`, 1),
		"wrong parent":       strings.Replace(fixtureAnswer(stdin, "team:alpha"), `"type":"project"`, `"type":"task"`, 1),
		"unsafe description": strings.Replace(fixtureAnswer(stdin, "team:alpha"), `"description":"update a1"`, `"description":"update\u001b[31m"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			broken := fixtureClassifier(t, fmt.Sprintf("cat >/dev/null\nprintf '%%s' '%s'", verdict))
			_, err := broken.ClassifyCLI(context.Background(), []string{"update", "a1"}, stdin, nil)
			if err == nil || !strings.Contains(err.Error(), "check the selected pack") {
				t.Fatalf("unsafe classification accepted: %v", err)
			}
		})
	}
}

func TestPackCLIClassifierInputRemainsBoundedTo64KiB(t *testing.T) {
	marker := filepath.Join(canonicalTempDir(t), "started")
	classifier := fixtureClassifier(t, fmt.Sprintf("touch %q; printf '%%s' '{}'", marker))
	_, err := classifier.ClassifyCLI(context.Background(), []string{strings.Repeat("x", classifierLimit)}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "input exceeds 64 KiB") {
		t.Fatalf("oversized CLI classifier input was not rejected at 64 KiB: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("oversized CLI input started the classifier: %v", err)
	}
}

func TestPackCLIClassifierFailsClosed(t *testing.T) {
	stdin := []byte("request input")
	cases := []struct{ name, script string }{
		{"crash", "exit 2"},
		{"malformed", `printf '{invalid'`},
		{"unknown field", `printf '%s' '{"version":"yolo.call-broker-classifier/1","local":true,"extra":true}'`},
		{"oversized", `head -c 65537 /dev/zero | tr '\\000' x`},
		{"timeout", "sleep 30"},
		{"unknown lookup", `printf '%s' '{"version":"yolo.call-broker-classifier/1","lookups":[{"name":"undeclared","arguments":["a1"]}]}'`},
		{"unsafe refusal", `printf '%s' '{"version":"yolo.call-broker-classifier/1","refused":{"class":"unknown","reason":"no","next_step":"retry\u001b[31m"}}'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			classifier := fixtureClassifier(t, tc.script)
			if tc.name == "timeout" {
				classifier.deadline = 200 * time.Millisecond
			}
			start := time.Now()
			_, err := classifier.ClassifyCLI(context.Background(), []string{"update", "a1"}, stdin, nil)
			if err == nil || !strings.Contains(err.Error(), "check the selected pack") {
				t.Fatalf("classifier failure accepted: %v", err)
			}
			if tc.name == "oversized" && !strings.Contains(err.Error(), "64 KiB") {
				t.Fatalf("oversized classifier answer was not bounded: %v", err)
			}
			if tc.name == "timeout" && (!strings.Contains(err.Error(), "timed out") || time.Since(start) > 20*time.Second) {
				t.Fatalf("classifier deadline was not enforced: %v after %v", err, time.Since(start))
			}
		})
	}
}

func TestPackCLIClassifierRejectsChangedSelectedExecutable(t *testing.T) {
	classifier := fixtureClassifier(t, `printf '%s' '{"version":"yolo.call-broker-classifier/1","local":true}'`)
	path := classifier.Identities["classifier"].Path
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho replaced\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := classifier.ClassifyCLI(context.Background(), []string{"--help"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "checksum changed") {
		t.Fatalf("modified selected classifier was executed: %v", err)
	}
}

func TestPackCLIClassifierLookupRoundsAreBoundedAndFactsStayUncredentialed(t *testing.T) {
	// This classifier asks for the same declared fact on every invocation. A fourth
	// lookup request is rejected, even when a trusted reader returns facts.
	classifier := fixtureClassifier(t, `cat >/dev/null; printf '%s' '{"version":"yolo.call-broker-classifier/1","lookups":[{"name":"task-project","arguments":["a1"]}]}'`)
	count := 0
	reader := func(_ context.Context, lookup loopholedecl.CallBrokerLookup, args []string) (json.RawMessage, error) {
		count++
		if lookup.Name != "task-project" || len(args) != 1 || args[0] != "a1" {
			t.Fatalf("unapproved lookup: %v %v", lookup, args)
		}
		return json.RawMessage(`{"project":"team:alpha"}`), nil
	}
	_, err := classifier.ClassifyCLI(context.Background(), []string{"update", "a1"}, nil, reader)
	if err == nil || !strings.Contains(err.Error(), "three lookup rounds") || count != 3 {
		t.Fatalf("fourth lookup was not refused: calls=%d err=%v", count, err)
	}
	_, err = classifier.ClassifyCLI(context.Background(), []string{"update", "a1"}, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "read grant") {
		t.Fatalf("lookup without core read grant accepted: %v", err)
	}
}

// The descendant keeps the classifier's stdout and blocks reading hold, so a runner that leaves it
// alive either fails the call or leaves a reader on hold; nothing waits on a clock.
func TestPackCLIClassifierStopsOwnedDescendantAfterSuccessfulWrapperExit(t *testing.T) {
	hold := fixtureFIFO(t, "hold")
	marker := filepath.Join(canonicalTempDir(t), "survived")
	body := fmt.Sprintf("(read x < %q; printf alive > %q) &\nprintf '%%s' '%s'", hold, marker,
		`{"version":"yolo.call-broker-classifier/1","local":true}`)
	classifier := fixtureClassifier(t, body)
	answer, err := classifier.ClassifyCLI(context.Background(), []string{"--help"}, nil, nil)
	if err != nil || !answer.Local {
		t.Fatalf("normal wrapper exit: answer=%+v err=%v", answer, err)
	}
	if fifoHasReader(t, hold) {
		t.Fatal("owned child survived normal exit: it still reads hold")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("owned child survived normal exit: %v", err)
	}
}

// The classifier deadline bounds each classifier process, not the lookups core
// runs between rounds: a host lookup that outlasts one deadline must not refuse
// a classification whose classifier answers promptly every round.
func TestPackCLIClassifierDeadlineBoundsEachInvocationNotLookups(t *testing.T) {
	classifier := fixtureClassifier(t, `if grep -q '"facts"'; then printf '%s' '`+fixtureAnswer(nil, "team:alpha")+`'; else printf '%s' '{"version":"yolo.call-broker-classifier/1","lookups":[{"name":"task-project","arguments":["a1"]}]}'; fi`)
	classifier.deadline = time.Second
	reader := func(ctx context.Context, _ loopholedecl.CallBrokerLookup, _ []string) (json.RawMessage, error) {
		// Outlast one classifier deadline; a reader whose context the
		// classifier's deadline cuts short returns that error instead.
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(classifier.deadline + 200*time.Millisecond):
			return json.RawMessage(`{"project":"team:alpha"}`), nil
		}
	}
	answer, err := classifier.ClassifyCLI(context.Background(), []string{"update", "a1"}, nil, reader)
	if err != nil || answer.Classification == nil || len(answer.Classification.Writes) != 1 {
		t.Fatalf("slow lookup consumed the classifier deadline: answer=%+v err=%v", answer, err)
	}
}

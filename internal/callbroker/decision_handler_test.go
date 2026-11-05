package callbroker

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	gobuild "go/build"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/hostservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

// TestMain builds a small portable Go classifier fixture once. It remains the selected,
// checksum-pinned executable invoked through the production Classifier path.
var testClassifierPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "callbroker-classifier-test-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "create classifier fixture directory:", err)
		os.Exit(2)
	}
	resolvedDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve classifier fixture directory:", err)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}
	dir = resolvedDir
	testClassifierPath = filepath.Join(dir, "classifier")
	goTool, err := callBrokerTestGoTool()
	if err != nil {
		fmt.Fprintln(os.Stderr, "locate Go compiler for portable classifier fixture:", err)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}
	build := exec.Command(goTool, "build", "-o", testClassifierPath, "./testdata/classifier")
	output, err := build.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "build portable classifier fixture: %v\n%s", err, output)
		_ = os.RemoveAll(dir)
		os.Exit(2)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

func callBrokerTestGoTool() (string, error) {
	goTool := filepath.Join(gobuild.Default.GOROOT, "bin", "go")
	if _, err := os.Stat(goTool); err == nil {
		return goTool, nil
	}
	return exec.LookPath("go")
}

func shortCallBrokerTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(os.TempDir(), "cb-")
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return resolved
}

func testCallHandler(t *testing.T) (*CLIHandler, string, string, *brokerstore.Store) {
	t.Helper()
	root := shortCallBrokerTempDir(t)
	t.Setenv("HOME", root)
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(root, "writes")
	lookupMarker := filepath.Join(root, "lookup-executions")
	program := "#!/bin/sh\ncase \"$1\" in\n lookup) printf 'ran\\n' >> '" + lookupMarker + "'; case \"$2\" in nonzero) printf '{\"ok\":true}'; exit 13 ;; invalid) printf 'not-json' ;; oversized) head -c 65537 /dev/zero | tr '\\000' x ;; *) printf '{\"project\":\"team:alpha\"}' ;; esac ;;\n read) case \"$2\" in overflow) head -c 33554433 /dev/zero | tr '\\000' x ;; expand) head -c 33554432 /dev/zero | tr '\\000' x ;; *) printf '{\"ok\":true}' ;; esac ;;\n update) printf 'WRITE\\n' >> '" + marker + "' ;;\n error) exit 13 ;;\n *) exit 13 ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(bin, "tasks"), []byte(program), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	helper, err := os.ReadFile(testClassifierPath)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(helper)
	input := run.CallBrokerLaunchInput{Version: "yolo.call-broker-launch/2", Loophole: "tasks", Source: "local-tasks", Front: "cli", Program: "tasks", Classifier: []string{"{program:classifier}"},
		ProgramIdentities: map[string]loopholedecl.CallBrokerProgramIdentity{"classifier": {Pack: "test-pack", Bin: "classifier", Path: testClassifierPath, SHA256: hex.EncodeToString(sum[:])}},
		Roots:             []string{"team:alpha", "team:beta"}, Resources: []loopholedecl.CallBrokerResource{{Type: "project"}, {Type: "task", Parent: "project"}},
		Cells: []loopholedecl.CallBrokerCell{{Name: "change", Tables: []string{"working_set", "all"}}, {Name: "blocked", Tables: []string{"all"}}},
		EffectiveCells: map[string]config.CallBrokerCellPolicy{
			"change":  {Tables: map[string]config.CallBrokerTablePolicy{"all": {Request: "ask", Ceiling: "off", Decision: "ask"}, "working_set": {Request: "ask", Ceiling: "off", Decision: "ask"}}},
			"blocked": {Tables: map[string]config.CallBrokerTablePolicy{"all": {Request: "false", Ceiling: "off", Decision: "refuse"}}},
		},
		Lookups: []loopholedecl.CallBrokerLookup{{Name: "task-project", CLI: []string{"lookup", "{id}"}}}, ReadStanding: true, ReadGrantStanding: "granted"}
	data, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	inputFile := filepath.Join(root, "selected.json")
	if err := os.WriteFile(inputFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "store"), 0700); err != nil {
		t.Fatal(err)
	}
	store := &brokerstore.Store{Dir: filepath.Join(root, "store"), Source: input.Source}
	if _, err := store.Working(); err != nil {
		t.Fatal(err)
	}
	audit := brokeraudit.Open(filepath.Join(root, "audit.jsonl"), nil)
	h, err := newCLIHandler(inputFile, root, store, audit)
	if err != nil {
		t.Fatal(err)
	}
	return h, inputFile, marker, store
}

func TestPackLookupAuditTracksCompletedExecutionBeforeValidatingResult(t *testing.T) {
	h, _, marker, _ := testCallHandler(t)
	counter := filepath.Join(filepath.Dir(marker), "lookup-executions")
	lookup := h.lookupWithClassifier("launch", h.classifier)
	declared := h.input.Lookups[0]
	for _, tc := range []struct {
		name, id, wantOutcome, wantReason string
		wantErr                           bool
	}{
		{name: "valid", id: "valid", wantOutcome: "ran"},
		{name: "nonzero exit", id: "nonzero", wantOutcome: "ran", wantReason: "host-cli-failed", wantErr: true},
		{name: "invalid JSON", id: "invalid", wantOutcome: "ran", wantReason: "host-cli-response-rejected", wantErr: true},
		{name: "oversized JSON", id: "oversized", wantOutcome: "ran", wantReason: "host-cli-response-rejected", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := countLines(t, counter)
			fact, err := lookup(context.Background(), declared, []string{tc.id})
			if (err != nil) != tc.wantErr {
				t.Fatalf("lookup error = %v, wantErr %v", err, tc.wantErr)
			}
			if err == nil && !json.Valid(fact) {
				t.Fatalf("successful lookup returned invalid JSON: %q", fact)
			}
			if tc.name == "oversized JSON" && len(fact) != 0 {
				t.Fatalf("oversized lookup returned %d bytes", len(fact))
			}
			if got := countLines(t, counter); got != before+1 {
				t.Fatalf("host CLI executions = %d, want %d", got-before, 1)
			}
			events := readPackAuditEvents(t, filepath.Join(filepath.Dir(marker), "audit.jsonl"))
			got := events[len(events)-1]
			if got.Event != "lookup" || got.Set != "read" || got.Outcome != tc.wantOutcome || got.Reason != tc.wantReason {
				t.Fatalf("persisted lookup audit = %+v, want outcome=%q reason=%q", got, tc.wantOutcome, tc.wantReason)
			}
			if got.CommandDigest == "" || len(got.Argv) != 0 || got.Stdin != nil || got.Workspace != "" {
				t.Fatalf("lookup audit contains missing digest or private fields: %+v", got)
			}
		})
	}
	auditPath := filepath.Join(filepath.Dir(marker), "audit.jsonl")
	log, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"nonzero", "invalid", "oversized", "team:alpha", filepath.Dir(marker)} {
		if strings.Contains(string(log), private) {
			t.Errorf("lookup audit leaked private value %q", private)
		}
	}
}

func TestPackLookupPreExecutionRefusalsDoNotRunHostProgram(t *testing.T) {
	t.Run("invalid arguments", func(t *testing.T) {
		h, _, marker, _ := testCallHandler(t)
		counter := filepath.Join(filepath.Dir(marker), "lookup-executions")
		_, err := h.lookupWithClassifier("launch", h.classifier)(context.Background(), h.input.Lookups[0], nil)
		if err == nil || countLines(t, counter) != 0 {
			t.Fatalf("invalid lookup arguments err=%v host executions=%d", err, countLines(t, counter))
		}
		event := lastPackAuditEvent(t, filepath.Join(filepath.Dir(marker), "audit.jsonl"))
		if event.Outcome != "refused" || event.Reason != "request-invalid" {
			t.Fatalf("invalid-argument audit = %+v", event)
		}
	})
	t.Run("classifier refusal", func(t *testing.T) {
		h, _, marker, _ := testCallHandler(t)
		counter := filepath.Join(filepath.Dir(marker), "lookup-executions")
		declared := h.input.Lookups[0]
		declared.CLI = []string{"unknown", "{id}"}
		_, err := h.lookupWithClassifier("launch", h.classifier)(context.Background(), declared, []string{"a1"})
		if err == nil || countLines(t, counter) != 0 {
			t.Fatalf("classifier refusal err=%v host executions=%d", err, countLines(t, counter))
		}
		event := lastPackAuditEvent(t, filepath.Join(filepath.Dir(marker), "audit.jsonl"))
		if event.Outcome != "refused" || event.Reason != "classifier-refused" {
			t.Fatalf("classifier-refusal audit = %+v", event)
		}
	})
	t.Run("selected input changed", func(t *testing.T) {
		h, inputFile, marker, _ := testCallHandler(t)
		counter := filepath.Join(filepath.Dir(marker), "lookup-executions")
		if err := os.WriteFile(inputFile, []byte(`{}`), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := h.lookupWithClassifier("launch", h.classifier)(context.Background(), h.input.Lookups[0], []string{"a1"})
		if err == nil || countLines(t, counter) != 0 {
			t.Fatalf("changed selected input err=%v host executions=%d", err, countLines(t, counter))
		}
		event := lastPackAuditEvent(t, filepath.Join(filepath.Dir(marker), "audit.jsonl"))
		if event.Outcome != "refused" || event.Reason != "selected-input-changed" {
			t.Fatalf("identity-refusal audit = %+v", event)
		}
	})
	t.Run("read standing missing", func(t *testing.T) {
		h, _, marker, _ := testCallHandler(t)
		counter := filepath.Join(filepath.Dir(marker), "lookup-executions")
		h.input.ReadStanding = false
		verdict, _, err := h.serveCLI(context.Background(), "act", "launch", []string{"read"}, nil)
		if err != nil || verdict.Verdict != "refused" || countLines(t, counter) != 0 {
			t.Fatalf("read without standing verdict=%+v err=%v host executions=%d", verdict, err, countLines(t, counter))
		}
		event := lastPackAuditEvent(t, filepath.Join(filepath.Dir(marker), "audit.jsonl"))
		if event.Event != "call" || event.Outcome != "refused" || event.Reason != "read-standing-missing" {
			t.Fatalf("policy-refusal audit = %+v", event)
		}
	})
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		return 0
	}
	return strings.Count(string(data), "\n")
}

func lastPackAuditEvent(t *testing.T, path string) brokeraudit.Event {
	t.Helper()
	events := readPackAuditEvents(t, path)
	if len(events) == 0 {
		t.Fatal("no persisted audit events")
	}
	return events[len(events)-1]
}

// The real hostservice handler, not a stand-in for the classifier or executor,
// owns this request boundary. A refused second write cannot run the first.
func TestPackCLIHandlerAuditsReadLookupsAndKeepsMultiwriteInert(t *testing.T) {
	h, _, marker, store := testCallHandler(t)
	root := filepath.Dir(marker)
	sock := filepath.Join(root, "d.sock")
	endpointDir := filepath.Join(root, "launch")
	if err := os.Mkdir(endpointDir, 0700); err != nil {
		t.Fatal(err)
	}
	endpoint := filepath.Join(endpointDir, "tasks.endpoint")
	stop := make(chan struct{})
	server := make(chan error, 1)
	front := make(chan error, 1)
	go func() { server <- hostservice.ServeFrontedUnix(h.Handle, sock, stop) }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	go func() { front <- svcendpoint.ServeFront(endpoint, "127.0.0.1", sock, stop) }()
	t.Cleanup(func() { close(stop); <-server; <-front })
	for !svcendpoint.Probe(endpoint) {
		if time.Now().After(deadline) {
			t.Fatal("front unavailable")
		}
		time.Sleep(time.Millisecond)
	}
	request := func(op string, argv ...string) (int, string) {
		t.Helper()
		conn, err := svcendpoint.DialLocal(endpoint, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
		body, err := json.Marshal(map[string]any{"op": op, "argv": argv})
		if err != nil {
			t.Fatal(err)
		}
		if err := frameproto.WriteRequest(conn, body); err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for {
			frame, err := frameproto.ReadFrame(conn)
			if err != nil {
				t.Fatal(err)
			}
			if frame.StreamID == frameproto.StreamExit {
				return int(binary.BigEndian.Uint32(frame.Payload)), text.String()
			}
			text.Write(frame.Payload)
		}
	}
	if rc, out := request("act", "read"); rc != 0 || !strings.Contains(out, "ok") {
		t.Fatalf("read rc=%d %q", rc, out)
	}
	if rc, out := request("evaluate", "update", "a1"); rc != 0 || !strings.Contains(out, `"verdict":"asks"`) {
		t.Fatalf("evaluate rc=%d %q", rc, out)
	}
	if rc, out := request("ask", "update", "a1"); rc != 0 || !strings.Contains(out, "cannot be filed") {
		t.Fatalf("ask rc=%d %q", rc, out)
	}
	if rc, out := request("ask", "update", "a1", "b1"); rc != 0 || !strings.Contains(out, `"verdict":"refused"`) || strings.Contains(out, "cannot be filed") {
		t.Fatalf("ask blocked multiwrite rc=%d output=%q", rc, out)
	}
	if rc, _ := request("act", "update", "a1", "b1"); rc != 64 {
		t.Fatalf("refused multiwrite rc=%d", rc)
	}
	if rc, _ := request("act", "update", "a1"); rc != 75 {
		t.Fatalf("write without approval policy rc=%d", rc)
	}
	if rc, _ := request("act", "error"); rc != 13 {
		t.Fatalf("host command exit rc=%d", rc)
	}
	if rc, _ := request("act", "classifier-error"); rc != 64 {
		t.Fatalf("classifier error rc=%d", rc)
	}
	h.program.secrets = [][]byte{[]byte("x")}
	for _, arg := range []string{"overflow", "expand"} {
		if rc, out := request("act", "read", arg); rc == 0 || len(out) > 1024 {
			t.Fatalf("unsafe/incomplete %s result: rc=%d output length=%d", arg, rc, len(out))
		}
	}
	if err := os.Remove(h.program.path); err != nil {
		t.Fatal(err)
	}
	if rc, _ := request("act", "read"); rc != 64 {
		t.Fatalf("unavailable host program rc=%d", rc)
	}
	if err := os.Remove(h.inputFile); err != nil {
		t.Fatal(err)
	}
	if rc, _ := request("act", "read"); rc != 64 {
		t.Fatalf("unavailable selected input rc=%d", rc)
	}
	if err := os.WriteFile(h.inputFile, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	if rc, _ := request("act", "read"); rc != 64 {
		t.Fatalf("changed selected input rc=%d", rc)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("host write ran: %v", err)
	}
	requests, grants, err := store.List()
	if err != nil || len(requests) != 0 || len(grants) != 0 {
		t.Fatalf("store mutated: requests %d grants %d %v", len(requests), len(grants), err)
	}
	events := readPackAuditEvents(t, filepath.Join(root, "audit.jsonl"))
	var calls []brokeraudit.Event
	var sawLookup bool
	for _, event := range events {
		if event.Event == "lookup" && event.Set == "read" && event.Outcome == "ran" {
			sawLookup = true
		}
		if event.Event == "call" {
			calls = append(calls, event)
		}
	}
	want := []struct{ set, outcome, reason string }{
		{"read", "ran", ""},
		{"write", "refused", "evaluation-only"},
		{"write", "refused", "approval-inactive"},
		{"write", "refused", "decision-refused"},
		{"write", "refused", "decision-refused"},
		{"write", "refused", "approval-inactive"},
		{"read", "ran", ""},
		{"refused", "refused", "classifier-failed"},
		{"read", "ran", "host-cli-response-rejected"},
		{"read", "ran", "host-cli-response-rejected"},
		{"read", "refused", "host-cli-failed"},
		{"refused", "refused", "selected-input-unavailable"},
		{"refused", "refused", "selected-input-changed"},
	}
	if !sawLookup || len(calls) != len(want) {
		t.Fatalf("audited lookups=%v calls=%+v", sawLookup, calls)
	}
	for i, expected := range want {
		got := calls[i]
		if got.Set != expected.set || got.Outcome != expected.outcome || got.Reason != expected.reason {
			t.Errorf("call audit %d = set %q outcome %q reason %q, want %q %q %q", i, got.Set, got.Outcome, got.Reason, expected.set, expected.outcome, expected.reason)
		}
		if got.Event != "call" || got.Service != "local-tasks" || got.Workspace != "" || len(got.Argv) != 0 || got.Stdin != nil {
			t.Errorf("call audit %d contains invalid or private fields: %+v", i, got)
		}
	}
	log, err := os.ReadFile(filepath.Join(root, "audit.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"classifier-error", "team:alpha", "team:beta", root, "update"} {
		if strings.Contains(string(log), private) {
			t.Errorf("audit leaked private value %q", private)
		}
	}
}

func readPackAuditEvents(t *testing.T, path string) []brokeraudit.Event {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var events []brokeraudit.Event
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var event brokeraudit.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestPackCLIEvaluateDigestBindsEveryWriteAndLiveSelection(t *testing.T) {
	h, inputFile, marker, store := testCallHandler(t)
	ctx := context.Background()
	a, _, err := h.serveCLI(ctx, "evaluate", "launch", []string{"update", "a1"}, nil)
	if err != nil || a.Verdict != "asks" {
		t.Fatalf("first %+v %v", a, err)
	}
	b, _, err := h.serveCLI(ctx, "evaluate", "launch", []string{"update", "a1", "a2"}, nil)
	if err != nil || b.Digest == a.Digest {
		t.Fatalf("second %+v %v", b, err)
	}
	if _, _, err := h.serveCLI(ctx, "act", "launch", []string{"update", "a1"}, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(inputFile); err != nil {
		t.Fatal(err)
	}
	c, _, err := h.serveCLI(ctx, "act", "launch", []string{"read"}, nil)
	if err == nil || c.Verdict != "refused" {
		t.Fatalf("removed selected input: %+v %v", c, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("host write ran: %v", err)
	}
	qs, gs, err := store.List()
	if err != nil || len(qs) != 0 || len(gs) != 0 {
		t.Fatalf("store modified %d %d %v", len(qs), len(gs), err)
	}
}

func TestPackCLIReadDeniedWithoutGateStandingAndOutsideRoot(t *testing.T) {
	h, inputFile, marker, store := testCallHandler(t)
	data, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}
	var selected run.CallBrokerLaunchInput
	if err := json.Unmarshal(data, &selected); err != nil {
		t.Fatal(err)
	}
	selected.ReadStanding = false
	selected.ReadGrantStanding = "withheld"
	write := func() {
		t.Helper()
		data, err := json.Marshal(selected)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(inputFile, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write()
	withoutRead, err := newCLIHandler(inputFile, h.workspace, store, h.audit)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err := withoutRead.serveCLI(context.Background(), "act", "launch", []string{"read"}, nil)
	if err != nil || v.Verdict != "refused" || v.Rule != "read grant missing" {
		t.Fatalf("read without standing: %+v %v", v, err)
	}
	selected.ReadStanding = true
	selected.ReadGrantStanding = "granted"
	selected.Roots = []string{"team:beta"}
	write()
	outside, err := newCLIHandler(inputFile, h.workspace, store, h.audit)
	if err != nil {
		t.Fatal(err)
	}
	v, _, err = outside.serveCLI(context.Background(), "act", "launch", []string{"read"}, nil)
	if err == nil || v.Verdict != "refused" {
		t.Fatalf("read outside gate-approved root: %+v %v", v, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("write executed without standing or root: %v", err)
	}
}

func TestPackCLIPreCancelledClassificationNeverStartsHostOperation(t *testing.T) {
	h, _, marker, store := testCallHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v, _, err := h.serveCLI(ctx, "act", "launch", []string{"update", "a1"}, nil)
	if err == nil || v.Verdict != "refused" {
		t.Fatalf("cancelled invocation %+v %v", v, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("cancelled write executed: %v", err)
	}
	qs, gs, err := store.List()
	if err != nil || len(qs) != 0 || len(gs) != 0 {
		t.Fatalf("cancelled call changed store: %d %d %v", len(qs), len(gs), err)
	}
}
func TestPackCLIPredictionCannotClaimAnInactiveWriteRuns(t *testing.T) {
	h, inputFile, marker, store := testCallHandler(t)
	data, err := os.ReadFile(inputFile)
	if err != nil {
		t.Fatal(err)
	}
	var selected run.CallBrokerLaunchInput
	if err := json.Unmarshal(data, &selected); err != nil {
		t.Fatal(err)
	}
	policy := selected.EffectiveCells["change"]
	policy.Tables["all"] = config.CallBrokerTablePolicy{Request: "true", Ceiling: "always", Decision: "run"}
	selected.EffectiveCells["change"] = policy
	data, err = json.Marshal(selected)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	active, err := newCLIHandler(inputFile, h.workspace, store, h.audit)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"evaluate", "act", "ask"} {
		v, _, err := active.serveCLI(context.Background(), op, "launch", []string{"update", "a1"}, nil)
		if err != nil || v.Verdict != "refused" || v.Rule != "pack approval effects are not active" {
			t.Fatalf("%s predicted/performed an inactive write: %+v %v", op, v, err)
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("inactive write executed: %v", err)
	}
}

func TestPackCoreDecisionHonorsFalseAndEveryWrite(t *testing.T) {
	policy := map[string]config.CallBrokerCellPolicy{"change": {Tables: map[string]config.CallBrokerTablePolicy{"all": {Request: "false", Ceiling: "always", Decision: "refuse"}, "working_set": {Request: "ask", Ceiling: "always", Decision: "ask"}}}}
	call := Classification{Writes: []Write{{Cell: "change", Chain: []Resource{{Type: "task", ID: "a1"}, {Type: "project", ID: "team:alpha"}}}, {Cell: "change", Chain: []Resource{{Type: "task", ID: "a2"}, {Type: "project", ID: "team:alpha"}}}}}
	working := []brokerstore.WorkingEntry{{Workspace: "/workspace", Repo: "local-tasks", Kind: "task", Resource: "a1"}}
	if v := decideCall(call, policy, working, "/workspace", "local-tasks", time.Now()); v.Verdict != "refused" {
		t.Fatalf("second uncovered false write was lost: %+v", v)
	}
	q := callRequest("local-tasks", "/workspace", "jail", "start", "launch", call, nil)
	if len(q.CallWrites) != 2 || len(q.CallWrites[0].Chain) != 2 || len(q.CallWrites[1].Chain) != 2 || q.Digest != brokerstore.CommandDigest(q) {
		t.Fatalf("write set not complete: %+v", q.CallWrites)
	}
}

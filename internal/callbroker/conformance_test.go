package callbroker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	kit "github.com/mschulkind-oss/yolo-jail/internal/callbrokerconformance"
)

type conformanceAdapter struct {
	call        func(context.Context, string, kit.Call) (kit.Observation, error)
	approve     func(context.Context) (kit.Observation, error)
	read, write kit.Call
	readOutput  string
	secrets     []string
}

func conformanceCounts(t *testing.T, store *brokerstore.Store, writes int) kit.Counts {
	t.Helper()
	snap, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	got := kit.Counts{Writes: writes}
	for _, r := range snap.Requests {
		if r.State == "pending" {
			got.Pending++
		}
	}
	for _, g := range snap.Grants {
		got.GrantUses += g.Uses
	}
	return got
}

func conformanceHandler(ctx context.Context, h *CLIHandler, op string, call kit.Call) (kit.Observation, error) {
	v, r, err := h.serveCLI(ctx, op, h.input.FrontJailID, call.Argv, call.Stdin)
	return kit.Observation{Verdict: v.Verdict, Output: string(r.Stdout) + string(r.Stderr) + v.Next}, err
}

func conformanceClassify(c Classifier, lookup LookupReader) kit.Classify {
	return func(ctx context.Context, call kit.Call) (json.RawMessage, error) {
		answer, err := c.ClassifyCLI(ctx, call.Argv, call.Stdin, lookup)
		if err != nil {
			return nil, err
		}
		return json.Marshal(answer)
	}
}

func conformanceWant(t *testing.T, answer Answer) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(answer)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func conformanceAnswer(argv []string, stdin []byte) Answer {
	return Answer{Version: "yolo.call-broker-classifier/1", Classification: &Classification{Argv: argv, StdinSHA256: fmt.Sprintf("%x", sha256.Sum256(stdin))}}
}

func conformanceYolo(t *testing.T) string {
	t.Helper()
	if binary := os.Getenv("YOLO_TEST_MATCHED_YOLO"); binary != "" {
		return binary
	}
	binary := filepath.Join(shortCallBrokerTempDir(t), "yolo")
	goTool, err := callBrokerTestGoTool()
	if err != nil {
		t.Fatal(err)
	}
	if out, err := callBrokerTestBuild(goTool, binary, "../../cmd/yolo").CombinedOutput(); err != nil {
		t.Fatalf("build approver: %v %s", err, out)
	}
	return binary
}

func conformanceHost(ctx context.Context, binary, home, id string) (string, error) {
	cmd := exec.CommandContext(ctx, binary, "approve", id)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "XDG_CONFIG_HOME=", "PATH=" + filepath.Dir(binary) + ":" + os.Getenv("PATH"), "USER=fixture"}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func conformancePending(t *testing.T, store *brokerstore.Store) string {
	t.Helper()
	snap, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range snap.Requests {
		if r.State == "pending" {
			return r.ID
		}
	}
	t.Fatal("no actual pending request")
	return ""
}

func checkConformanceLifecycle(t *testing.T, a conformanceAdapter) {
	t.Helper()
	changed := kit.Call{Argv: a.write.Argv, Stdin: []byte("different\n")}
	steps := []kit.Step{}
	add := func(name, op string, call kit.Call, verdict string, counts kit.Counts, contains ...string) {
		steps = append(steps, kit.Step{Name: name, Run: func(ctx context.Context) (kit.Observation, error) { return a.call(ctx, op, call) }, WantVerdict: verdict, WantCounts: counts, OutputContains: contains, OutputExcludes: a.secrets})
	}
	add("read act", "act", a.read, "runs", kit.Counts{}, a.readOutput)
	add("write evaluate", "evaluate", a.write, "asks", kit.Counts{})
	add("ask", "ask", a.write, "asks", kit.Counts{Pending: 1})
	add("duplicate ask", "ask", a.write, "asks", kit.Counts{Pending: 1})
	steps = append(steps, kit.Step{Name: "host approve", Run: a.approve, WantVerdict: "approved", WantCounts: kit.Counts{GrantUses: 1}, OutputExcludes: a.secrets})
	add("approved evaluate", "evaluate", a.write, "runs", kit.Counts{GrantUses: 1})
	add("changed stdin", "act", changed, "asks", kit.Counts{Pending: 1, GrantUses: 1})
	add("exact act", "act", a.write, "runs", kit.Counts{Writes: 1, Pending: 1})
	add("replay", "act", a.write, "asks", kit.Counts{Writes: 1, Pending: 2})
	if err := kit.CheckSteps(context.Background(), steps); err != nil {
		t.Fatal(err)
	}
}

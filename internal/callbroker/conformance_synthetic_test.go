package callbroker

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	kit "github.com/mschulkind-oss/yolo-jail/internal/callbrokerconformance"
)

func TestConformanceSyntheticClassifications(t *testing.T) {
	f := selectedOnceFixture(t)
	cases := []kit.ClassificationCase{}
	for _, row := range []struct {
		name        string
		argv        []string
		stdin       []byte
		chain       []Resource
		description string
	}{
		{"read", []string{"read"}, nil, []Resource{{Type: "project", ID: "team:alpha"}}, ""},
		{"update-pr", []string{"update-pr"}, []byte("hello\n"), []Resource{{Type: "pr", ID: "#1"}, {Type: "project", ID: "team:alpha"}}, "update test PR"},
		{"declared read lookup", []string{"update", "a1"}, nil, []Resource{{Type: "task", ID: "a1"}, {Type: "project", ID: "team:alpha"}}, "update task"},
	} {
		a := conformanceAnswer(row.argv, row.stdin)
		if row.description == "" {
			a.Classification.Reads = [][]Resource{row.chain}
		} else {
			a.Classification.Writes = []Write{{Cell: "change", Chain: row.chain, Description: row.description}}
		}
		cases = append(cases, kit.ClassificationCase{Name: row.name, Call: kit.Call{Argv: row.argv, Stdin: row.stdin}, Want: conformanceWant(t, a)})
	}
	lookup := f.h.lookupWithClassifier(f.h.input.FrontJailID, f.h.classifier)
	if err := kit.CheckClassifications(context.Background(), conformanceClassify(f.h.classifier, lookup), cases); err != nil {
		t.Fatal(err)
	}
	data := string(mustRead(t, filepath.Join(f.home, "lookup-effects")))
	if strings.Count(data, "LOOKUP\n") != 2 || f.effects(t) != 0 || conformanceCounts(t, f.store, 0) != (kit.Counts{}) {
		t.Fatalf("lookup not exactly two standing reads: %q", data)
	}
	event := lastPackAuditEvent(t, filepath.Join(f.home, "audit.jsonl"))
	if event.Event != "lookup" || event.Set != "read" || event.Outcome != "ran" {
		t.Fatalf("lookup audit=%+v", event)
	}
}

func syntheticConformanceAdapter(t *testing.T, f *onceFixture, binary string) conformanceAdapter {
	observe := func(o kit.Observation) kit.Observation {
		if strings.Contains(o.Output, "synthetic-only-host-value") {
			t.Fatal("observation leaked synthetic host token")
		}
		o.Counts = conformanceCounts(t, f.store, f.effects(t))
		return o
	}
	a := conformanceAdapter{read: kit.Call{Argv: []string{"read"}}, write: kit.Call{Argv: []string{"update-pr"}, Stdin: []byte("hello\n")}, readOutput: `{"ok":true}`, secrets: []string{"synthetic-only-host-value"}}
	a.call = func(ctx context.Context, op string, call kit.Call) (kit.Observation, error) {
		o, err := conformanceHandler(ctx, f.h, op, call)
		return observe(o), err
	}
	a.approve = func(ctx context.Context) (kit.Observation, error) {
		out, err := conformanceHost(ctx, binary, f.home, conformancePending(t, f.store))
		o := kit.Observation{Output: out}
		if err == nil {
			o.Verdict = "approved"
		}
		return observe(o), err
	}
	return a
}

func TestConformanceSyntheticLifecycle(t *testing.T) {
	binary := conformanceYolo(t) // build under original Go environment, before execution HOME changes
	f := selectedOnceFixture(t)
	checkConformanceLifecycle(t, syntheticConformanceAdapter(t, f, binary))
	if got := string(mustRead(t, filepath.Join(f.home, "stdin-received"))); got != "hello\n" {
		t.Fatalf("executor stdin=%q", got)
	}
}

func TestConformanceSyntheticInvalidAnswers(t *testing.T) {
	stdin := []byte("hello\n")
	cases := []struct {
		name, contains string
		mutate         func(*Answer)
		script         string
	}{
		{name: "empty canonical argv", contains: "canonical call is empty", mutate: func(a *Answer) { a.Classification.Argv = nil }},
		{name: "canonical NUL", contains: "canonical argv contains NUL", mutate: func(a *Answer) { a.Classification.Argv = []string{"update-pr\x00"} }},
		{name: "changed stdin digest", contains: "changed its stdin", mutate: func(a *Answer) { a.Classification.StdinSHA256 = strings.Repeat("0", 64) }},
		{name: "undeclared resource", contains: "undeclared resource", mutate: func(a *Answer) { a.Classification.Writes[0].Chain[0].Type = "alien" }},
		{name: "repeated type", contains: "parent chain changed", mutate: func(a *Answer) {
			a.Classification.Writes[0].Chain[0].Type = "project"
		}},
		{name: "wrong parent", contains: "parent chain changed", mutate: func(a *Answer) { a.Classification.Writes[0].Chain[1].Type = "task" }},
		{name: "uncovered top root", contains: "outside gate-approved scope", mutate: func(a *Answer) { a.Classification.Writes[0].Chain[1].ID = "team:gamma" }},
		{name: "oversized classifier response", contains: "64 KiB", script: `cat >/dev/null; head -c 65537 /dev/zero | tr '\000' x`},
		{name: "invalid schema", contains: "not strict JSON", script: `cat >/dev/null; printf '%s' '{"version":"yolo.call-broker-classifier/1","extra":true}'`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := selectedOnceFixture(t)
			if tc.name == "uncovered top root" && slices.Contains(f.h.classifier.Roots, "team:gamma") {
				t.Fatal("negative control precondition: team:gamma is covered")
			}
			a := conformanceAnswer([]string{"update-pr"}, stdin)
			a.Classification.Writes = []Write{{Cell: "change", Chain: []Resource{{Type: "pr", ID: "#1"}, {Type: "project", ID: "team:alpha"}}, Description: "update test PR"}}
			script := tc.script
			if tc.mutate != nil {
				tc.mutate(&a)
				raw := conformanceWant(t, a)
				script = fmt.Sprintf("cat >/dev/null; printf '%%s' '%s'", raw)
			}
			pinned := fixtureClassifier(t, script)
			classifier := f.h.classifier
			classifier.Argv = pinned.Argv
			classifier.Identities = pinned.Identities
			call := kit.Call{Argv: []string{"update-pr"}, Stdin: stdin}
			if err := kit.CheckClassifications(context.Background(), conformanceClassify(classifier, nil), []kit.ClassificationCase{{Name: tc.name, Call: call, ErrorContains: tc.contains}}); err != nil {
				t.Fatal(err)
			}
			f.h.classifier = classifier
			o, err := conformanceHandler(context.Background(), f.h, "act", call)
			o.Counts = conformanceCounts(t, f.store, f.effects(t))
			if strings.Contains(o.Output, "synthetic-only-host-value") || (err != nil && strings.Contains(err.Error(), "synthetic-only-host-value")) {
				t.Fatal("negative answer leaked synthetic host token")
			}
			if err == nil || !strings.Contains(err.Error(), tc.contains) || o.Verdict != "refused" || o.Counts != (kit.Counts{}) {
				t.Fatalf("handler accepted malicious answer: observation=%+v error=%v", o, err)
			}
		})
	}
}

func TestConformanceSyntheticStaleApproval(t *testing.T) {
	binary := conformanceYolo(t)
	for _, name := range []string{"expired", "withdrawn"} {
		t.Run(name, func(t *testing.T) {
			f := selectedOnceFixture(t)
			a := syntheticConformanceAdapter(t, f, binary)
			ctx := context.Background()
			if o, err := a.call(ctx, "ask", a.write); err != nil || o.Verdict != "asks" {
				t.Fatalf("ask: %+v %v", o, err)
			}
			if o, err := a.approve(ctx); err != nil || o.Counts != (kit.Counts{GrantUses: 1}) {
				t.Fatalf("approve: %+v %v", o, err)
			}
			snap, err := f.store.Snapshot()
			if err != nil {
				t.Fatal(err)
			}
			if name == "expired" {
				f.store.Now = func() time.Time { return snap.Grants[0].Expires.Add(time.Second) }
			} else {
				data := string(mustRead(t, f.userPath))
				if !strings.Contains(data, `"all.change"`) {
					t.Fatal("withdrawal precondition missing")
				}
				if err := os.WriteFile(f.userPath, []byte(strings.Replace(data, `"all.change"`, `"all.blocked"`, 1)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			o, err := a.call(ctx, "act", a.write)
			if o.Verdict != "refused" || o.Counts != (kit.Counts{GrantUses: 1}) {
				t.Fatalf("stale approval executed/spent: %+v %v", o, err)
			}
			if name == "expired" {
				if _, ok := brokerstore.IsLapsedAllow(err); !ok || !strings.Contains(o.Output, "Allow once expired") {
					t.Fatalf("missing lapsed diagnostic: %+v %v", o, err)
				}
			}
		})
	}
	t.Run("host approve after withdrawal", func(t *testing.T) {
		f := selectedOnceFixture(t)
		a := syntheticConformanceAdapter(t, f, binary)
		ctx := context.Background()
		if _, err := a.call(ctx, "ask", a.write); err != nil {
			t.Fatal(err)
		}
		data := string(mustRead(t, f.userPath))
		if err := os.WriteFile(f.userPath, []byte(strings.Replace(data, `"all.change"`, `"all.blocked"`, 1)), 0600); err != nil {
			t.Fatal(err)
		}
		o, err := a.approve(ctx)
		if err == nil || o.Verdict == "approved" || o.Counts != (kit.Counts{Pending: 1}) {
			t.Fatalf("withdrawn host answer granted: %+v %v", o, err)
		}
	})
}

func TestConformanceSyntheticCompletedOversizedResponse(t *testing.T) {
	binary := conformanceYolo(t)
	f := selectedOnceFixture(t)
	a := syntheticConformanceAdapter(t, f, binary)
	ctx := context.Background()
	call := kit.Call{Argv: []string{"update-pr"}, Stdin: []byte("oversized-response\n")}
	if o, err := a.call(ctx, "ask", call); err != nil || o.Verdict != "asks" {
		t.Fatalf("ask: %+v %v", o, err)
	}
	id := conformancePending(t, f.store)
	if o, err := a.approve(ctx); err != nil || o.Counts != (kit.Counts{GrantUses: 1}) {
		t.Fatalf("approve: %+v %v", o, err)
	}
	o, err := a.call(ctx, "act", call)
	if !completedWriteResponse(err) || o.Verdict != "refused" || o.Counts != (kit.Counts{Writes: 1}) || !strings.Contains(o.Output, "inspect host state before retrying") {
		t.Fatalf("not a completed-write rejection: %+v %v", o, err)
	}
	for _, private := range []string{"OVER_CAP_PRIVATE_SENTINEL", "xxxxxxxxxx", "oversized-response\n", "synthetic-only-host-value"} {
		if strings.Contains(o.Output, private) || (err != nil && strings.Contains(err.Error(), private)) {
			t.Fatalf("partial/private response leaked %q", private)
		}
	}
	if got := string(mustRead(t, filepath.Join(f.home, "stdin-received"))); got != string(call.Stdin) {
		t.Fatalf("completed stdin changed: %q", got)
	}
	event := lastPackAuditEvent(t, filepath.Join(f.home, "audit.jsonl"))
	if event.Event != "call" || event.Set != "write" || event.Outcome != "ran" || event.Reason != "host-cli-response-rejected" || len(event.Argv) != 0 || event.Stdin != nil {
		t.Fatalf("completed audit=%+v", event)
	}
	audit := string(mustRead(t, filepath.Join(f.home, "audit.jsonl")))
	for _, private := range []string{string(call.Stdin), "OVER_CAP_PRIVATE_SENTINEL", "synthetic-only-host-value", f.home} {
		if strings.Contains(audit, private) {
			t.Fatalf("audit leaked %q", private)
		}
	}
	snap, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Grants) != 1 || snap.Grants[0].Request != id || snap.Grants[0].Uses != 0 || snap.Grants[0].EndedBy() != brokerstore.EndUsed {
		t.Fatalf("completed grant=%+v", snap.Grants)
	}
	var spent bool
	for _, r := range snap.Requests {
		if r.ID == id {
			spent = r.Spent
			if event.CommandDigest != r.Digest {
				t.Fatal("audit command digest differs")
			}
		}
	}
	if !spent {
		t.Fatal("completed request not spent")
	}
	if err := kit.CheckSteps(ctx, []kit.Step{{Name: "oversized replay", Run: func(ctx context.Context) (kit.Observation, error) { return a.call(ctx, "act", call) }, WantVerdict: "asks", WantCounts: kit.Counts{Writes: 1, Pending: 1}, OutputExcludes: []string{"OVER_CAP_PRIVATE_SENTINEL", "xxxxxxxxxx"}}}); err != nil {
		t.Fatal(err)
	}
}

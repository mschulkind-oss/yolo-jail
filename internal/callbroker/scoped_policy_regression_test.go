package callbroker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokercore"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

func republishScopedRegressionInput(t *testing.T, f *onceFixture) {
	t.Helper()
	raw, err := json.Marshal(f.h.input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	f.h.original = raw
}

func TestSelectedCLIExactAnswerSurvivesFreshLaunch(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("file: %+v %v", v, err)
	}
	if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	sf := f.scope
	var err error
	sf.LaunchID, err = brokerscope.NewLaunchID()
	if err != nil {
		t.Fatal(err)
	}
	sf.BrokerStart, sf.BrokerPID, sf.CallCLIPath, sf.CallCLIHash = "", 0, "", ""
	sf.CallInput = ""
	path, err := brokerscope.Write(sf)
	if err != nil {
		t.Fatal(err)
	}
	in := f.h.input
	in.ScopeFile, in.FrontJailID = path, "fresh-launch"
	_, block, err := loopholegrant.Read(in.GrantFile)
	if err != nil || block == nil {
		t.Fatalf("read fixture grant: %v", err)
	}
	block.LaunchID = sf.LaunchID
	grantRaw, err := loopholegrant.Merge([]byte("{}"), *block)
	if err != nil {
		t.Fatal(err)
	}
	in.GrantFile = filepath.Join(f.home, "fresh-grant.json")
	if err := os.WriteFile(in.GrantFile, grantRaw, 0600); err != nil {
		t.Fatal(err)
	}
	inputPath := filepath.Join(f.home, "fresh-selected.json")
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(inputPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := brokerscope.BindCallInput(path, sf, inputPath); err != nil {
		t.Fatal(err)
	}
	sf, err = brokerscope.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := newCLIHandler(inputPath, f.home, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	h.classifier.deadline = fixtureClassifierDeadline
	start, err := brokerstore.NewStartID()
	if err != nil {
		t.Fatal(err)
	}
	bound, lease, err := brokercore.BindLease(in.Source, path, sf, start)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := brokercore.CloseLease(in.Source, lease); err != nil {
			t.Error(err)
		}
	})
	if err := brokerscope.BindCallCLI(path, bound, h.program.path, h.program.sha256); err != nil {
		t.Fatal(err)
	}
	bound.CallCLIPath, bound.CallCLIHash = h.program.path, h.program.sha256
	h.scopePath, h.bound, h.start = path, bound, start
	f.h, f.store.Revalidate = h, h.revalidate
	if _, _, err := h.currentAuthority(); err != nil {
		t.Fatalf("fresh authority: %v", err)
	}
	v, _, err := f.call(t, "act", "update", "a1")
	snap, snapErr := f.store.Snapshot()
	if snapErr != nil {
		t.Fatal(snapErr)
	}
	t.Logf("fresh result=%+v error=%v effects=%d uses=%d", v, err, f.effects(t), snap.Grants[0].Uses)
	if err != nil || v.Verdict != "runs" || f.effects(t) != 1 || snap.Grants[0].Uses != 0 {
		t.Fatalf("workspace-held exact answer rejected by fresh live holder")
	}
}

func TestSelectedCLIWorkingSetOnlyCeilingRunsThroughTransaction(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	raw, err := os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.Replace(string(raw), `"all.change"`, `"working_set.change"`, 1))
	if err := os.WriteFile(f.userPath, raw, 0600); err != nil {
		t.Fatal(err)
	}
	p := f.h.input.EffectiveCells["change"]
	p.Tables["all"] = config.CallBrokerTablePolicy{Request: "false", Ceiling: "off", Decision: "refuse"}
	f.h.input.EffectiveCells["change"] = p
	republishScopedRegressionInput(t, f)
	if _, _, err := f.store.AddWorking(brokerstore.WorkingEntry{Workspace: f.home, Repo: f.h.input.Source, Kind: brokerstore.WorkingPR, Resource: "#1", By: "fixture"}); err != nil {
		t.Fatal(err)
	}
	answer, err := f.h.classifier.ClassifyCLI(context.Background(), []string{"update-pr", "1"}, nil, nil)
	if err != nil || answer.Classification == nil {
		t.Fatalf("classify: %v", err)
	}
	_, _, working, now, err := f.store.InspectContext(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	pure := decideCall(*answer.Classification, f.h.input.EffectiveCells, working, f.home, f.h.input.Source, now)
	if pure.Verdict != "runs" {
		t.Fatalf("fixture pure verdict: %+v", pure)
	}
	v, _, err := f.call(t, "act", "update-pr", "1")
	t.Logf("pure=%+v actual=%+v error=%v effects=%d", pure, v, err, f.effects(t))
	if err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatal("working-set-only authority rejected by all-table revalidation")
	}
}

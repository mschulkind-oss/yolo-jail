package callbroker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

func TestSelectedCLIReadsCurrentApprovedGrantAfterLiveAddition(t *testing.T) {
	f := selectedOnceFixtureWithGrant(t, false, false)
	if v, _, err := f.call(t, "act", "read"); err == nil || v.Verdict != "refused" {
		t.Fatalf("pending read grant ran before apply: %+v %v", v, err)
	}
	_, before, err := loopholegrant.Read(f.grantPath)
	if err != nil || before == nil {
		t.Fatalf("read grant fixture: %v", err)
	}
	after := *before
	after.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *before, after); err != nil {
		t.Fatal(err)
	}
	if v, _, err := f.call(t, "act", "read"); err != nil || v.Verdict != "runs" || f.effects(t) != 0 {
		t.Fatalf("live read grant did not reach next request: %+v %v", v, err)
	}
}

func TestSelectedCLILookupCannotRunAfterThisLaunchsReadGrantIsAppliedAway(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "evaluate", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("first lookup did not classify: %+v %v", v, err)
	}
	lookupPath := filepath.Join(f.home, "lookup-effects")
	before := mustRead(t, lookupPath)
	_, old, err := loopholegrant.Read(f.grantPath)
	if err != nil || old == nil {
		t.Fatalf("read the launch's grant: %v", err)
	}
	next := *old
	next.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *old, next); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.call(t, "act", "update", "a1")
	if err == nil || v.Verdict != "refused" || f.effects(t) != 0 {
		t.Fatalf("write after read withdrawal reached classifier/host: %+v %v", v, err)
	}
	if after := mustRead(t, lookupPath); string(after) != string(before) {
		t.Fatalf("withdrawn read grant still executed a lookup: %q to %q", before, after)
	}
}

func TestSelectedCLIReadStillRunsWhenOnlyWriteCeilingIsWithdrawn(t *testing.T) {
	f := selectedOnceFixture(t)
	user := strings.Replace(string(mustRead(t, f.userPath)), "all.change", "all.blocked", 1)
	if err := os.WriteFile(f.userPath, []byte(user), 0600); err != nil {
		t.Fatal(err)
	}
	v, _, err := f.call(t, "act", "read")
	if err != nil || v.Verdict != "runs" || f.effects(t) != 0 {
		t.Fatalf("read lost its own standing: %+v %v", v, err)
	}
	v, _, err = f.call(t, "evaluate", "update", "a1")
	if err == nil || v.Verdict != "refused" || f.effects(t) != 0 {
		t.Fatalf("withdrawn write ceiling authorized: %+v %v", v, err)
	}
}

func TestSelectedCLILookupReadsThisLaunchsCurrentGrant(t *testing.T) {
	f := selectedOnceFixtureWithGrant(t, false, false)
	lookup := f.h.lookupWithClassifier(f.h.input.FrontJailID, f.h.classifier)
	declared := f.h.input.Lookups[0]
	lookupPath := filepath.Join(f.home, "lookup-effects")
	if _, err := lookup(context.Background(), declared, []string{"a1"}); err == nil {
		t.Fatal("lookup executed before read addition")
	}
	if _, err := os.Stat(lookupPath); !os.IsNotExist(err) {
		t.Fatalf("lookup had an effect without standing: %v", err)
	}
	_, before, err := loopholegrant.Read(f.grantPath)
	if err != nil || before == nil {
		t.Fatalf("read grant fixture: %v", err)
	}
	granted := *before
	granted.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.Granted}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *before, granted); err != nil {
		t.Fatal(err)
	}
	if fact, err := lookup(context.Background(), declared, []string{"a1"}); err != nil || len(fact) == 0 {
		t.Fatalf("lookup after read addition: %q %v", fact, err)
	}
	observed := mustRead(t, lookupPath)
	_, current, err := loopholegrant.Read(f.grantPath)
	if err != nil || current == nil {
		t.Fatalf("read current grant: %v", err)
	}
	withdrawn := *current
	withdrawn.Kinds = map[string]loopholegrant.Kind{"read": {Standing: loopholegrant.NotAllowed}}
	if err := loopholegrant.Replace(context.Background(), f.grantPath, *current, withdrawn); err != nil {
		t.Fatal(err)
	}
	if _, err := lookup(context.Background(), declared, []string{"a1"}); err == nil {
		t.Fatal("lookup executed after read withdrawal")
	}
	if after := mustRead(t, lookupPath); string(after) != string(observed) {
		t.Fatalf("withdrawn lookup had an effect: %q to %q", observed, after)
	}
}

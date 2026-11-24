package callbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerlock"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
)

// This is a private authority-adapter fixture, NOT an approved gate producer or Main proof.
func legacySnapshotReaderFixture(t *testing.T) (*onceFixture, brokerscope.PolicySnapshot) {
	t.Helper()
	f := selectedOnceFixtureWithStanding(t, false)
	p := prepareLegacySnapshotReaderInput(t, f)
	h, err := newCLIHandler(f.inputPath, f.h.workspace, f.store, f.h.audit)
	if err != nil {
		t.Fatalf("actual /3 known-write reader cannot adopt complete authority: %v", err)
	}
	h.scopePath, h.bound, h.start = f.scopePath, f.scope, f.h.start
	f.h = h
	f.store.Revalidate = h.revalidate
	return f, p
}

func prepareLegacySnapshotReaderInput(t *testing.T, f *onceFixture) brokerscope.PolicySnapshot {
	t.Helper()
	in := f.h.input
	in.Version = "yolo.call-broker-launch/3"
	in.Roots, in.EffectiveCells = nil, nil
	in.ReadStanding, in.ReadWithheld, in.ReadGrantStanding = false, false, ""
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.inputPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	p := brokerscope.PolicySnapshot{Version: brokerscope.PolicySnapshotVersion,
		Binding:  brokerscope.PolicyBinding{Source: in.Source, Workspace: f.h.workspace, LaunchID: f.scope.LaunchID, DeclarationHash: in.DeclarationHash, InputDigest: snapshotInputDigest(data)},
		Sequence: 1, Model: "legacy/1", Roots: []string{"team:alpha", "team:beta"},
		Asks: brokerscope.WorkspaceAsks{SourceRead: "true", Requests: json.RawMessage(`{"all":{"change":"ask","blocked":false},"working_set":{"change":"ask"}}`), Scopes: []brokerscope.WorkspaceAskScope{}},
	}
	installFixtureSnapshot(t, f, p)
	return p
}

func installFixtureSnapshot(t *testing.T, f *onceFixture, p brokerscope.PolicySnapshot) {
	t.Helper()
	sf, err := brokerscope.ReadFile(f.scopePath)
	if err != nil {
		t.Fatal(err)
	}
	err = brokerlock.WithTransaction(f.store.Dir, func() error {
		ref, err := brokerscope.PreparePolicyLocked(sf, p)
		if err != nil {
			return err
		}
		sf.PolicySnapshot, sf.Repos = ref, append([]string{}, p.Roots...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// The fixture uses existing atomic scope Write, not public apply or approval proof.
	if _, err := brokerscope.Write(sf); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotKnownWriteUsesCurrentCellAndSourceFloorWithoutFrozenFallback(t *testing.T) {
	f, p := legacySnapshotReaderFixture(t)
	originalReader := readSourceGrant
	reads := 0
	readSourceGrant = func(path string) (map[string]any, *loopholegrant.Block, error) {
		reads++
		return originalReader(path)
	}
	t.Cleanup(func() { readSourceGrant = originalReader })
	if len(f.h.input.Roots) != 0 || len(f.h.input.EffectiveCells) != 0 {
		t.Fatal("/3 fixture retained frozen authority")
	}
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "asks" || f.effects(t) != 0 {
		t.Fatalf("snapshot asks did not file through actual handler: %+v %v", v, err)
	}
	q := f.pending(t)[0]
	p.Sequence++
	p.Asks.Requests = json.RawMessage(`{"all":{"change":false,"blocked":false},"working_set":{"change":false}}`)
	installFixtureSnapshot(t, f, p)
	if _, err := f.approvalSide().Decide(q.ID, "pending", brokerstore.Decision{}); err == nil {
		t.Fatal("standalone current request answered against stale cells")
	}
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict != "refused" && v.Verdict != "refuses" || f.effects(t) != 0 {
		t.Fatalf("changed snapshot cell did not refuse: %+v %v", v, err)
	}
	p.Sequence++
	p.Asks.SourceRead = "false"
	p.Asks.Requests = json.RawMessage(`{"all":{"change":true,"blocked":false},"working_set":{"change":true}}`)
	installFixtureSnapshot(t, f, p)
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict == "runs" || f.effects(t) != 0 {
		t.Fatalf("independent write without legacy read floor was activated: %+v %v", v, err)
	}
	if v, _, err := f.call(t, "act", "lookup", "a1"); err == nil && v.Verdict != "refused" && v.Verdict != "refuses" || f.effects(t) != 0 {
		t.Fatalf("snapshot read=false did not refuse stale granted source: %+v %v", v, err)
	}
	if reads != 0 {
		t.Fatalf("snapshot authorizers separately read grant file %d times", reads)
	}
}

func TestSnapshotKnownWriteLateCoverageReadsNewAsksAndCurrentHostCeiling(t *testing.T) {
	f, p := legacySnapshotReaderFixture(t)
	p.Sequence++
	p.Asks.Requests = json.RawMessage(`{"all":{"change":true,"blocked":false},"working_set":{"change":true}}`)
	installFixtureSnapshot(t, f, p)
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("current true request did not run supported write: %+v %v", v, err)
	}
	p.Sequence++
	p.Asks.Requests = json.RawMessage(`{"all":{"change":"ask","blocked":false},"working_set":{"change":"ask"}}`)
	installFixtureSnapshot(t, f, p)
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "asks" || f.effects(t) != 1 {
		t.Fatalf("snapshot ask did not replace previous true: %+v %v", v, err)
	}
	q := f.pending(t)[0]
	if _, err := f.approvalSide().Decide(q.ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	before, err := f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	p.Sequence++
	p.Asks.Requests = json.RawMessage(`{"all":{"change":false,"blocked":false},"working_set":{"change":false}}`)
	installFixtureSnapshot(t, f, p)
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict != "refused" && v.Verdict != "refuses" || f.effects(t) != 1 {
		t.Fatalf("late false request spent an earlier exact answer: %+v %v", v, err)
	}
	after, err := f.store.Snapshot()
	if err != nil || len(after.Grants) != 1 || after.Grants[0].Uses != before.Grants[0].Uses {
		t.Fatalf("withdrawn cell consumed answer: %+v %v", after, err)
	}
	p.Sequence++
	p.Asks.Requests = json.RawMessage(`{"all":{"change":true,"blocked":false},"working_set":{"change":true}}`)
	installFixtureSnapshot(t, f, p)
	data, err := os.ReadFile(f.userPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `on_request = ["all.change"]`) {
		t.Fatal("fixture did not carry the declared change-cell ceiling")
	}
	data = []byte(strings.ReplaceAll(string(data), `on_request = ["all.change"]`, `on_request = []`))
	if err := os.WriteFile(f.userPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict == "runs" || f.effects(t) != 1 {
		t.Fatalf("fresh host ceiling was replaced by snapshot permission: %+v %v", v, err)
	}
}

func TestSnapshotHTTPKnownWriteAndReadUseCurrentSnapshotNotGrantFile(t *testing.T) {
	const body = `{"operationName":"Rename","title":"ready"}`
	var sends atomic.Int32
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sends.Add(1)
		if r.URL.EscapedPath() == "/items/a1" {
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	h, f := selectedHTTPWriteFixture(t, origin, executor, []byte(body))
	f.h = h.CLIHandler
	p := prepareLegacySnapshotReaderInput(t, f)
	next, err := newHTTPHandler(f.inputPath, f.h.workspace, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	next.scopePath, next.bound, next.start = f.scopePath, f.scope, f.h.start
	next.httpClient = executor
	f.h = next.CLIHandler
	f.store.Revalidate = next.revalidateHTTP
	originalReader := readSourceGrant
	reads := 0
	readSourceGrant = func(path string) (map[string]any, *loopholegrant.Block, error) {
		reads++
		return originalReader(path)
	}
	t.Cleanup(func() { readSourceGrant = originalReader })
	request := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(body)}
	v, _, err := next.serveHTTP(context.Background(), "act", next.input.FrontJailID, request)
	if err != nil || v.Verdict != "asks" {
		t.Fatalf("snapshot HTTP ask: %+v %v", v, err)
	}
	q := f.pending(t)[0]
	p.Sequence++
	p.Asks.Requests = json.RawMessage(`{"all":{"change":false,"blocked":false},"working_set":{"change":false}}`)
	installFixtureSnapshot(t, f, p)
	if _, err := f.approvalSide().Decide(q.ID, "pending", brokerstore.Decision{}); err == nil {
		t.Fatal("standalone HTTP approval ignored snapshot cell withdrawal")
	}
	before := sends.Load()
	p.Sequence++
	p.Asks.SourceRead = "false"
	installFixtureSnapshot(t, f, p)
	if v, _, err := next.serveHTTP(context.Background(), "act", next.input.FrontJailID, request); err == nil && v.Verdict == "runs" || sends.Load() != before {
		t.Fatalf("withdrawn source floor sent HTTP request: %+v %v sends=%d", v, err, sends.Load())
	}
	if reads != 0 {
		t.Fatalf("HTTP snapshot callbacks read source grant %d times", reads)
	}
}

func TestSnapshotStandalonePublicApproveFindsCurrentRootAndCurrentAsk(t *testing.T) {
	f, binary := liveRootApprovalFixture(t)
	p := prepareLegacySnapshotReaderInput(t, f)
	h, err := newCLIHandler(f.inputPath, f.h.workspace, f.store, f.h.audit)
	if err != nil {
		t.Fatal(err)
	}
	h.scopePath, h.bound, h.start = f.scopePath, f.scope, f.h.start
	f.h = h
	f.store.Revalidate = h.revalidate
	// This private adapter installation is not a public apply producer/G6e claim.
	p.Sequence++
	p.Roots = []string{"team:beta"}
	installFixtureSnapshot(t, f, p)
	if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict == "runs" || len(f.pending(t)) != 0 || f.effects(t) != 0 {
		t.Fatalf("root outside current snapshot admitted: %+v %v", v, err)
	}
	p.Sequence++
	p.Roots = []string{"team:alpha", "team:beta"}
	installFixtureSnapshot(t, f, p)
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("current snapshot root did not file: %+v %v", v, err)
	}
	q := f.pending(t)[0]
	if out, err := publicLiveRootApprove(t, f, binary, q.ID); err != nil {
		t.Fatalf("public /3 answer could not resolve current snapshot: %v %s", err, out)
	}
	if v, _, err := f.call(t, "act", "update", "a1"); err != nil || v.Verdict != "runs" || f.effects(t) != 1 {
		t.Fatalf("public /3 answer did not spend once: %+v %v", v, err)
	}
}

func TestSnapshotPublicApplyDoesNotMutateAuthorityOrAncillaryGrantsBeforeCompleteApply(t *testing.T) {
	f, _ := liveRootApprovalFixture(t)
	p := prepareLegacySnapshotReaderInput(t, f)
	// No producer or consumer-proof claim: this is an owned private input/snapshot adapter.
	sf, err := brokerscope.ReadFile(f.scopePath)
	if err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(filepath.Dir(f.scopePath), sf.PolicySnapshot.Name)
	beforeScope, beforeGrant, beforePolicy := mustRead(t, f.scopePath), mustRead(t, f.grantPath), mustRead(t, policyPath)
	writeLiveRootRequests(t, f, []string{"team:alpha", "team:beta"})
	// Withdraw the grant too: source protection must precede every ancillary write.
	workspacePath := filepath.Join(f.h.workspace, "yolo-jail.toml")
	data := bytes.Replace(mustRead(t, workspacePath), []byte("read = true"), []byte("read = false"), 1)
	if err := os.WriteFile(workspacePath, data, 0600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if rc := run.ConfigApply([]string{"--accept-config-changes"}, &out, &out, false); rc == 0 {
		t.Fatalf("partial snapshot apply claimed success: %s", out.String())
	}
	if !strings.Contains(out.String(), "snapshot apply is not active") {
		t.Fatalf("pending snapshot source had no truthful disposition: %s", out.String())
	}
	if !bytes.Equal(beforeScope, mustRead(t, f.scopePath)) || !bytes.Equal(beforeGrant, mustRead(t, f.grantPath)) || !bytes.Equal(beforePolicy, mustRead(t, policyPath)) {
		t.Fatal("partial public apply changed snapshot source authority or ancillary grants")
	}
	if current, err := brokerscope.ReadPolicy(sf, p.Binding.DeclarationHash, p.Binding.InputDigest); err != nil || current.Asks.SourceRead != "true" {
		t.Fatal("public apply replaced or obscured old snapshot")
	}
}

func TestSnapshotReaderRefusesCorruptOrCopiedAuthorityBeforeAnyProviderLookup(t *testing.T) {
	for _, kind := range []string{"corrupt", "missing", "input", "lease", "scoped-ceiling"} {
		t.Run(kind, func(t *testing.T) {
			f, p := legacySnapshotReaderFixture(t)
			sf, err := brokerscope.ReadFile(f.scopePath)
			if err != nil {
				t.Fatal(err)
			}
			policyPath := filepath.Join(filepath.Dir(f.scopePath), sf.PolicySnapshot.Name)
			switch kind {
			case "corrupt":
				if err := os.WriteFile(policyPath, []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := os.Remove(policyPath); err != nil {
					t.Fatal(err)
				}
			case "input":
				p.Sequence++
				p.Binding.InputDigest = strings.Repeat("a", 64)
				installFixtureSnapshot(t, f, p)
			case "lease":
				sf.BrokerStart = strings.Repeat("c", 32)
				if _, err := brokerscope.Write(sf); err != nil {
					t.Fatal(err)
				}
			case "scoped-ceiling":
				data := mustRead(t, f.userPath)
				data = append(data, []byte("\n[scopes.narrow]\nsource=\"local-tasks\"\nresource=\"team:alpha\"\n[scopes.narrow.allow]\nnever=[\"all.change\"]\n")...)
				if err := os.WriteFile(f.userPath, data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict != "refused" && v.Verdict != "refuses" || len(f.pending(t)) != 0 || f.effects(t) != 0 {
				t.Fatalf("bad snapshot authority admitted or executed: %+v %v", v, err)
			}
			if data, err := os.ReadFile(filepath.Join(f.home, "lookup-effects")); err == nil && len(data) != 0 {
				t.Fatalf("bad snapshot authority performed provider lookup: %q", data)
			}
		})
	}
}

func TestSnapshotReaderRejectsUnsupportedModelAndScopeRatherThanActivateOrIgnore(t *testing.T) {
	for _, kind := range []string{"model", "scope-true", "scope-false"} {
		t.Run(kind, func(t *testing.T) {
			f, p := legacySnapshotReaderFixture(t)
			p.Sequence++
			if kind == "model" {
				p.Model = "scoped/1"
			} else {
				value := "true"
				if strings.HasSuffix(kind, "false") {
					value = "false"
				}
				p.Asks.Scopes = []brokerscope.WorkspaceAskScope{{Alias: "alpha", Resource: "team:alpha", Requests: json.RawMessage(`{"all":{"change":` + value + `}}`)}}
			}
			installFixtureSnapshot(t, f, p)
			if v, _, err := f.call(t, "act", "update", "a1"); err == nil && v.Verdict != "refused" && v.Verdict != "refuses" || len(f.pending(t)) != 0 || f.effects(t) != 0 {
				t.Fatalf("unsupported scoped asks activated or silently ignored: %+v %v", v, err)
			}
		})
	}
}

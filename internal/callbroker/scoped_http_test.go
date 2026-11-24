package callbroker

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerpolicy"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

const scopedHTTPBody = `{"operationName":"Rename"}`

// scopedHTTP is the selected HTTP front with a scoped decision leaf and a counted origin.
// Its classifier answers with the one read and one write the test names, both on a
// single-resource chain so the origin has no ancestry to verify.
type scopedHTTP struct {
	h              *HTTPHandler
	f              *onceFixture
	forward        func(string, HTTPCall) (int, string)
	reads, writes  atomic.Int32
	getProjectCall HTTPCall
	postCall       HTTPCall
}

func newScopedHTTP(t *testing.T, readAsk, readCeiling, changeAsk, changeCeiling brokerpolicy.Value, withRead, withWrite bool) *scopedHTTP {
	t.Helper()
	s := &scopedHTTP{}
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			s.writes.Add(1)
		} else {
			s.reads.Add(1)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	s.h, s.f = selectedHTTPReadFixture(t, origin, executor)
	policy := scopedLeaf(t, s.h.input.Source, readAsk, readCeiling, changeAsk, changeCeiling)
	s.h.livePolicy = func() (brokerpolicy.Policy, error) { return policy, nil }
	approvalLivePolicy = s.h.livePolicy
	t.Cleanup(func() { approvalLivePolicy = nil })
	s.f.store.Entries = RequestEntries
	s.f.store.Revalidate = s.h.revalidateHTTP
	s.getProjectCall = HTTPCall{Method: "GET", Path: "/project"}
	s.postCall = HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(scopedHTTPBody)}
	if withWrite {
		reads := ""
		if withRead {
			reads = `,"reads":[[{"type":"project","id":"team:alpha"}]]`
		}
		answer := fmt.Sprintf(`{"version":"yolo.call-broker-classifier/1","classification":{"canonical":{"method":"POST","path":"/graphql","query":"","headers":[],"body":{"mode":"original","present":true,"sha256":%q}}%s,"writes":[{"cell":"change","chain":[{"type":"project","id":"team:alpha"}],"description":"rename"}]}}`, digestHTTPBody(true, []byte(scopedHTTPBody)), reads)
		classifier := fixtureHTTPClassifier(t, "cat >/dev/null\ncat <<'ANSWER'\n"+answer+"\nANSWER")
		s.h.classifier.Identities, s.h.input.ProgramIdentities = classifier.Identities, classifier.Identities
	}
	s.forward = selectedHTTPHostFrame(t, s.h)
	return s
}

func (s *scopedHTTP) pending(t *testing.T) []brokerstore.Request {
	t.Helper()
	snap, err := s.f.store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	return snap.Requests
}

func (s *scopedHTTP) answerOnce(t *testing.T) {
	t.Helper()
	pending := s.pending(t)
	if len(pending) == 0 {
		t.Fatal("nothing pending to answer")
	}
	if _, err := s.f.store.Decide(pending[len(pending)-1].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
}

// The HTTP halves of the U7 rows: the same bundle decision as the CLI front, through the
// real selected HTTP front frame.
func TestScopedHTTPOperationApprovals(t *testing.T) {
	const (
		ask    = brokerpolicy.Ask
		onReq  = brokerpolicy.OnRequest
		always = brokerpolicy.Always
	)
	t.Run("C1", func(t *testing.T) {
		s := newScopedHTTP(t, ask, onReq, ask, onReq, false, false)
		if rc, msg := s.forward("ask", s.getProjectCall); rc != 0 || !strings.Contains(msg, `"verdict":"asks"`) || s.reads.Load() != 0 {
			t.Fatalf("an asking HTTP read did not ask: rc=%d %q reads=%d", rc, msg, s.reads.Load())
		}
		pending := s.pending(t)
		if len(pending) != 1 || len(pending[0].CallReads) != 1 || len(pending[0].CallWrites) != 0 || pending[0].HTTPOriginalDigest == "" || pending[0].CallReads[0].Chain[0].ID != "team:alpha" {
			t.Fatalf("durable HTTP read subject: %+v", pending)
		}
		if _, err := ApprovalAuthority(pending[0]); err != nil {
			t.Fatalf("host approval cannot revalidate the HTTP read request: %v", err)
		}
		s.answerOnce(t)
		if rc, msg := s.forward("act", s.getProjectCall); rc != 0 || s.reads.Load() != 1 {
			t.Fatalf("answered HTTP read: rc=%d %q reads=%d", rc, msg, s.reads.Load())
		}
		if rc, _ := s.forward("act", s.getProjectCall); rc != 75 || s.reads.Load() != 1 {
			t.Fatalf("a spent answer ran a second read: rc=%d reads=%d", rc, s.reads.Load())
		}
	})
	t.Run("C3", func(t *testing.T) {
		s := newScopedHTTP(t, ask, onReq, ask, onReq, false, false)
		s.f.store.TimedLimit = ApprovalTimedLimit
		if rc, _ := s.forward("ask", s.getProjectCall); rc != 0 {
			t.Fatalf("ask rc=%d", rc)
		}
		entries, err := s.f.store.DecideWorking(s.pending(t)[0].ID, brokerstore.Decision{For: 10 * time.Minute})
		if err != nil || len(entries) != 1 || !entries[0].Timed() {
			t.Fatalf("timed read answer: %+v %v", entries, err)
		}
		for i := 0; i < 2; i++ {
			if rc, msg := s.forward("act", s.getProjectCall); rc != 0 {
				t.Fatalf("run %d under the timed answer: rc=%d %q", i, rc, msg)
			}
		}
		if s.reads.Load() != 2 || len(s.pending(t)) != 1 {
			t.Fatalf("reads=%d requests=%+v", s.reads.Load(), s.pending(t))
		}
	})
	t.Run("C4", func(t *testing.T) {
		s := newScopedHTTP(t, ask, onReq, ask, onReq, true, true)
		if rc, msg := s.forward("ask", s.postCall); rc != 0 || !strings.Contains(msg, `"verdict":"asks"`) {
			t.Fatalf("ask: rc=%d %q", rc, msg)
		}
		pending := s.pending(t)
		if len(pending) != 1 || len(pending[0].CallReads) != 1 || len(pending[0].CallWrites) != 1 {
			t.Fatalf("one request must name both: %+v", pending)
		}
		unbound := pending[0]
		unbound.CallReads = nil
		if brokerstore.CommandDigest(unbound) == pending[0].Digest {
			t.Fatal("the digest does not bind the read")
		}
		if _, err := ApprovalAuthority(pending[0]); err != nil {
			t.Fatalf("host approval cannot revalidate the mixed HTTP request: %v", err)
		}
		s.answerOnce(t)
		if rc, msg := s.forward("act", s.postCall); rc != 0 || s.writes.Load() != 1 {
			t.Fatalf("one answer for both: rc=%d %q writes=%d", rc, msg, s.writes.Load())
		}
		snap, err := s.f.store.Snapshot()
		if err != nil || snap.Grants[0].Uses != 0 || snap.Grants[0].Issued != 1 {
			t.Fatalf("one answer spent more than once: %+v %v", snap.Grants, err)
		}
	})
	t.Run("C5", func(t *testing.T) {
		s := newScopedHTTP(t, brokerpolicy.False, onReq, ask, onReq, true, true)
		rc, msg := s.forward("act", s.postCall)
		if rc == 0 || !strings.Contains(msg, "fixture read ask") || len(s.pending(t)) != 0 || s.writes.Load() != 0 || s.reads.Load() != 0 {
			t.Fatalf("a refused read with an asking write: rc=%d %q requests=%d", rc, msg, len(s.pending(t)))
		}
	})
	t.Run("running read is not a subject", func(t *testing.T) {
		s := newScopedHTTP(t, brokerpolicy.True, always, ask, onReq, true, true)
		if rc, _ := s.forward("ask", s.postCall); rc != 0 {
			t.Fatalf("ask rc=%d", rc)
		}
		if pending := s.pending(t); len(pending) != 1 || len(pending[0].CallReads) != 0 || len(pending[0].CallWrites) != 1 {
			t.Fatalf("a read that runs under standing authority became a subject: %+v", pending)
		}
	})
	t.Run("C8", func(t *testing.T) {
		s := newScopedHTTP(t, ask, onReq, ask, onReq, true, true)
		for _, call := range []HTTPCall{s.postCall} {
			if rc, msg := s.forward("evaluate", call); rc != 0 || !strings.Contains(msg, `"verdict":"asks"`) {
				t.Fatalf("evaluate: rc=%d %q", rc, msg)
			}
		}
		if len(s.pending(t)) != 0 || s.reads.Load() != 0 || s.writes.Load() != 0 {
			t.Fatalf("a prediction filed or ran: requests=%d reads=%d writes=%d", len(s.pending(t)), s.reads.Load(), s.writes.Load())
		}
	})
	t.Run("G20", func(t *testing.T) {
		for _, withWrite := range []bool{false, true} {
			s := newScopedHTTP(t, brokerpolicy.True, brokerpolicy.Never, brokerpolicy.True, always, withWrite, withWrite)
			call := s.getProjectCall
			if withWrite {
				call = s.postCall
			}
			rc, msg := s.forward("act", call)
			if rc == 0 || !strings.Contains(msg, "fixture read ceiling") {
				t.Fatalf("write=%t: a never read was not refused naming its line: rc=%d %q", withWrite, rc, msg)
			}
			if s.reads.Load() != 0 || s.writes.Load() != 0 || len(s.pending(t)) != 0 {
				t.Fatalf("write=%t: a refused read sent or filed: reads=%d writes=%d requests=%d", withWrite, s.reads.Load(), s.writes.Load(), len(s.pending(t)))
			}
		}
	})
	t.Run("H27", func(t *testing.T) {
		s := newScopedHTTP(t, brokerpolicy.False, onReq, ask, onReq, false, false)
		rc, msg := s.forward("act", s.getProjectCall)
		if rc == 0 || s.reads.Load() != 0 || !strings.Contains(msg, "fixture read ask") {
			t.Fatalf("the scoped read line must govern an HTTP read: rc=%d %q", rc, msg)
		}
	})
}

// A stored bundle whose read the policy now refuses cannot be approved: the host approval
// adapter decides the stored operations again.
func TestScopedHTTPApprovalRefusesAWithdrawnRead(t *testing.T) {
	s := newScopedHTTP(t, brokerpolicy.Ask, brokerpolicy.OnRequest, brokerpolicy.Ask, brokerpolicy.OnRequest, false, false)
	if rc, _ := s.forward("ask", s.getProjectCall); rc != 0 {
		t.Fatalf("ask rc=%d", rc)
	}
	pending := s.pending(t)[0]
	withdrawn := scopedLeaf(t, s.h.input.Source, brokerpolicy.False, brokerpolicy.OnRequest, brokerpolicy.Ask, brokerpolicy.OnRequest)
	s.h.livePolicy = func() (brokerpolicy.Policy, error) { return withdrawn, nil }
	if _, err := s.h.revalidateHTTP(pending); err == nil || !strings.Contains(err.Error(), "yolo") {
		t.Fatalf("a request whose read was withdrawn revalidated: %v", err)
	}
}

// An ancestry lookup is a provider read. Under a read the policy refuses or asks, and no
// answer covers, the HTTP front sends no exploratory lookup: it refuses naming the read line.
func TestScopedHTTPAncestryLookupIsNotExploratory(t *testing.T) {
	const original = `{"operationName":"Rename"}`
	for _, readAsk := range []brokerpolicy.Value{brokerpolicy.False, brokerpolicy.Ask} {
		var origin atomic.Int32
		_, executor, url := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin.Add(1)
			_, _ = w.Write([]byte(`{"id":"a1","project":{"id":"team:alpha"}}`))
		}))
		h, f := selectedHTTPWriteFixture(t, url, executor, []byte(original))
		policy := scopedLeaf(t, h.input.Source, readAsk, brokerpolicy.OnRequest, brokerpolicy.True, brokerpolicy.Always)
		h.livePolicy = func() (brokerpolicy.Policy, error) { return policy, nil }
		forward := selectedHTTPHostFrame(t, h)
		call := HTTPCall{Method: "POST", Path: "/graphql", BodyPresent: true, Body: []byte(original)}
		rc, msg := forward("act", call)
		if rc == 0 || !strings.Contains(msg, "fixture read ask") || origin.Load() != 0 {
			t.Fatalf("read %s: an ancestry lookup ran under a read that does not run: rc=%d %q origin=%d", readAsk, rc, msg, origin.Load())
		}
		if snap, err := f.store.Snapshot(); err != nil || len(snap.Requests) != 0 {
			t.Fatalf("read %s: a refused lookup filed a request: %+v %v", readAsk, snap.Requests, err)
		}
	}
}

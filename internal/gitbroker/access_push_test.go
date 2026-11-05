package gitbroker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/frameproto"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/svcendpoint"
)

func pushAdvertisement(t *testing.T, ref, id string) []byte {
	t.Helper()
	var advertisement bytes.Buffer
	w := gitproto.NewWriter(&advertisement)
	format := ""
	if len(id) == 64 {
		format = " object-format=sha256"
	}
	if err := w.Data([]byte(id + " " + ref + "\x00report-status push-options" + format + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	return advertisement.Bytes()
}

func TestGitAccessPushRejectsStaleHostAdvertisement(t *testing.T) {
	id := strings.Repeat("a", 40)
	changed := strings.Repeat("b", 40)
	cmd := gitproto.Command{Old: id, New: changed, Ref: "refs/heads/main"}
	_, err := verifyProposedPush([]gitproto.Command{cmd}, []string{"ci.skip"}, bytes.NewReader(pushAdvertisement(t, cmd.Ref, changed)))
	if err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale ref accepted: %v", err)
	}
	for _, width := range []int{40, 64} {
		old, newID := strings.Repeat("a", width), strings.Repeat("b", width)
		refs := []gitproto.Command{{Old: old, New: newID, Ref: "refs/heads/main"}, {Old: strings.Repeat("0", width), New: newID, Ref: "refs/heads/new"}}
		if got, err := verifyProposedPush(refs, []string{"ci.skip", "priority=1"}, bytes.NewReader(pushAdvertisement(t, refs[0].Ref, old))); err != nil || got != widthName(width) {
			t.Fatalf("width %d: %s %v", width, got, err)
		}
		refs[1].Old = old
		if _, err := verifyProposedPush(refs, nil, bytes.NewReader(pushAdvertisement(t, refs[0].Ref, old))); err == nil {
			t.Fatal("changed creation accepted")
		}
	}
}

func hostProposal(t *testing.T, f *fixture, op string, p gitproto.PushProposal) map[string]any {
	t.Helper()
	conn, err := svcendpoint.Dial(f.endpoint, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	wire, err := json.Marshal(map[string]any{"access": map[string]any{"op": op, "version": 1, "proposal": p}})
	if err != nil {
		t.Fatal(err)
	}
	if err := frameproto.WriteRequest(conn, wire); err != nil {
		t.Fatal(err)
	}
	var out, stderr []byte
	for {
		frame, err := frameproto.ReadFrame(conn)
		if err != nil {
			t.Fatal(err)
		}
		switch frame.StreamID {
		case frameproto.StreamStdout:
			out = append(out, frame.Payload...)
		case frameproto.StreamStderr:
			stderr = append(stderr, frame.Payload...)
		case frameproto.StreamExit:
			rc, err := frameproto.ExitCode(frame.Payload)
			if err != nil || rc != 0 {
				t.Fatalf("host response %d %v: %s stderr=%s wire=%s", rc, err, out, stderr, wire)
			}
			var answer map[string]any
			if err := json.Unmarshal(out, &answer); err != nil {
				t.Fatalf("host response %s: %v", out, err)
			}
			return answer
		}
	}
}

func TestGitAccessPushAskNeverSendsCommands(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("proposal")
	head := mustGit(t, f.ws, "rev-parse", "HEAD")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: "git@fixture.test:/o/r.git", Commands: []gitproto.Command{{Old: old, New: head, Ref: "refs/heads/main"}}, PushOptions: []string{}}
	// The fixture's approved URL, not an inferred identity or another remote.
	sf := f.broker.bound
	p.URL = sf.Repos[0]
	repo, err := gitproto.Identity(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	a := hostProposal(t, f, "evaluate", p)
	if a["verdict"] != "asks" || a["command_digest"] != f.broker.pushRequest(repo, p.Commands, nil).Digest {
		t.Fatalf("evaluate: %+v", a)
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("evaluate wrote remote")
	}
	ask := hostProposal(t, f, "ask", p)
	if ask["verdict"] != "asks" || ask["command_digest"] != a["command_digest"] || ask["request_id"] == "" {
		t.Fatalf("ask: %+v", ask)
	}
	again := hostProposal(t, f, "ask", p)
	if again["request_id"] != ask["request_id"] {
		t.Fatalf("duplicate request: %+v vs %+v", ask, again)
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("ask wrote remote")
	}
	if sent, err := os.ReadFile(filepath.Join(f.record, "stdin")); err != nil || string(sent) != "0000" {
		t.Fatalf("host receive-pack got %q: %v", sent, err)
	}
}

func TestGitAccessPushAskExactGrantThenActSpendsOnce(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("approved")
	head := mustGit(t, f.ws, "rev-parse", "HEAD")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: head, Ref: "refs/heads/main"}}, PushOptions: []string{}}
	filed := hostProposal(t, f, "ask", p)
	id, ok := filed["request_id"].(string)
	if !ok || id == "" {
		t.Fatalf("no request: %+v", filed)
	}
	grant := allow(t, id)
	got := hostProposal(t, f, "ask", p)
	if got["verdict"] != "runs" || got["request_id"] != nil || got["covered_by"] != grant.ID || len(got["next"].([]any)) != 0 {
		t.Fatalf("pre-ask spent grant or refiled: %+v", got)
	}
	classified, ok := got["classified"].([]any)
	if !ok || len(classified) != 1 || classified[0].(map[string]any)["covered_by"] != grant.ID {
		t.Fatalf("exact grant missing from per-ref classification: %+v", got["classified"])
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("ask wrote upstream")
	}
	f.mustPush("origin", "HEAD:main")
	if f.upstreamRef("refs/heads/main") != head {
		t.Fatal("act did not write exact approved ref")
	}
	if got := hostProposal(t, f, "evaluate", p); got["verdict"] != "refused" {
		t.Fatalf("stale proposal accepted after act: %+v", got)
	}
}

func TestGitAccessPushRejectsMissingReadAndWrongURLBeforeUpstream(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{noRead: true})
	old := f.upstreamRef("refs/heads/main")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: strings.Repeat("a", 40), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	if got := hostProposal(t, f, "evaluate", p); got["verdict"] != "refused" || f.sshRan() {
		t.Fatalf("no read contacted upstream: %+v", got)
	}
	p.URL = "git@unapproved.test:o/r.git"
	if got := hostProposal(t, f, "ask", p); got["verdict"] != "refused" || f.sshRan() {
		t.Fatalf("wrong URL contacted upstream: %+v", got)
	}
}

func TestGitAccessPushOptionsOrderBindsDigest(t *testing.T) {
	f := newFixture(t, "tee")
	mustGit(t, f.bare, "config", "receive.advertisePushOptions", "true")
	old := f.upstreamRef("refs/heads/main")
	f.commit("options")
	cmd := gitproto.Command{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{cmd}, PushOptions: []string{"ci.skip", "priority=1"}}
	first := hostProposal(t, f, "evaluate", p)
	if first["verdict"] != "asks" {
		t.Fatalf("unexpected %v", first)
	}
	p.PushOptions[0], p.PushOptions[1] = p.PushOptions[1], p.PushOptions[0]
	second := hostProposal(t, f, "evaluate", p)
	if second["command_digest"] == first["command_digest"] {
		t.Fatalf("push options ignored: %v %v", first, second)
	}
}

func TestGitAccessPushHTTPSAdvertisementNeverPosts(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{})
	old := h.upstreamRef("refs/heads/main")
	h.commit("https-proposal")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: h.url, Commands: []gitproto.Command{{Old: old, New: mustGit(t, h.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	for _, op := range []string{"evaluate", "ask"} {
		got := hostProposal(t, h.fixture, op, p)
		if got["verdict"] != "asks" {
			t.Fatalf("%s: %+v; requests: %s", op, got, h.srv.log(t))
		}
	}
	for _, r := range h.srv.requests() {
		if r.Method != "GET" || !strings.HasSuffix(r.URI, "/info/refs?service=git-receive-pack") {
			t.Fatalf("prediction sent %s; requests: %s", r, h.srv.log(t))
		}
	}
	if h.upstreamRef("refs/heads/main") != old {
		t.Fatal("HTTPS evaluation changed remote")
	}
}

func TestGitAccessPushRefBindingAndWholePush(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("multi")
	head := mustGit(t, f.ws, "rev-parse", "HEAD")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: head, Ref: "refs/heads/main"}, {Old: strings.Repeat("0", 40), New: head, Ref: "refs/heads/new"}}, PushOptions: []string{}}
	got := hostProposal(t, f, "ask", p)
	if got["verdict"] != "asks" || got["request_id"] == nil {
		t.Fatalf("mixed push was partly authorized: %+v", got)
	}
	firstID, firstDigest := got["request_id"], got["command_digest"]
	if f.upstreamRef("refs/heads/main") != old || f.upstreamRef("refs/heads/new") != "" {
		t.Fatal("mixed pre-ask wrote refs")
	}
	p.Commands[1].Old = old
	if got := hostProposal(t, f, "ask", p); got["verdict"] != "refused" || got["request_id"] != nil {
		t.Fatalf("changed creation accepted: %+v", got)
	}
	p.Commands[1].Old = strings.Repeat("0", 40)
	p.Commands[1].New = old
	if got := hostProposal(t, f, "ask", p); got["request_id"] == nil || got["request_id"] == firstID || got["command_digest"] == firstDigest {
		t.Fatalf("new selected object ID reused the old exact request: %+v", got)
	}
}

func TestGitAccessPushHTTPSMissingReadNeverContactsRemote(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{public: true, noRead: true})
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: h.url, Commands: []gitproto.Command{{Old: h.upstreamRef("refs/heads/main"), New: strings.Repeat("a", 40), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	for _, op := range []string{"evaluate", "ask"} {
		if got := hostProposal(t, h.fixture, op, p); got["verdict"] != "refused" {
			t.Fatalf("%s: %+v", op, got)
		}
	}
	if len(h.srv.requests()) != 0 || len(h.credCalls()) != 0 {
		t.Fatalf("unapproved prediction contacted remote or credential helper: %s", h.srv.log(t))
	}
}

func TestGitAccessPushRejectsMalformedWireBeforeAdvertisement(t *testing.T) {
	f := newFixture(t, "tee")
	valid := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: f.upstreamRef("refs/heads/main"), New: strings.Repeat("b", 40), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	body, _ := json.Marshal(valid)
	for _, raw := range []string{
		`{"access":{"op":"ask","version":2,"proposal":` + string(body) + `}}`,
		`{"access":{"op":"ask","version":1,"proposal":` + string(body) + `},"service":"git-receive-pack"}`,
		`{"op":"ask","version":1,"proposal":` + string(body) + `}`,
		`{"access":{"op":"ask","version":1,"missing":true}}`,
		`{"access":{"op":"ask","version":1,"proposal":{"service":"git-receive-pack","url":"x","push_options":[],"extra":true}}}`,
		`{"access":{"op":"ask","version":1,"proposal":{"service":"git-receive-pack","url":"x","commands":[{"Old":"0000000000000000000000000000000000000000","New":"1111111111111111111111111111111111111111","Ref":"refs/heads/main"}],"extra":true}}}`,
		`{"access":{"op":"ask","version":1,"proposal":{"service":"git-receive-pack","url":"x","commands":[],"push_options":[],"unknown":true}}}`,
	} {
		conn, err := svcendpoint.Dial(f.endpoint, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err := frameproto.WriteRequest(conn, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		var rc int
		for {
			frame, err := frameproto.ReadFrame(conn)
			if err != nil {
				t.Fatal(err)
			}
			if frame.StreamID == frameproto.StreamStdout && len(frame.Payload) > 0 {
				t.Fatalf("malformed frame got output: %s", frame.Payload)
			}
			if frame.StreamID == frameproto.StreamExit {
				rc, err = frameproto.ExitCode(frame.Payload)
				if err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		_ = conn.Close()
		if rc != 64 || f.sshRan() {
			t.Fatalf("malformed frame rc=%d contacted SSH: %s", rc, raw)
		}
	}
}

func TestGitAccessPushHTTPSDoesNotSettleCredentialOnRead(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{})
	old := h.upstreamRef("refs/heads/main")
	h.commit("private")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: h.url, Commands: []gitproto.Command{{Old: old, New: mustGit(t, h.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	if got := hostProposal(t, h.fixture, "evaluate", p); got["verdict"] != "asks" {
		t.Fatalf("private GET: %+v", got)
	}
	calls := h.credCalls()
	if len(calls) != 1 || !strings.HasSuffix(calls[0], "-fill") {
		t.Fatalf("evaluate settled credential: %v", calls)
	}
	for _, r := range h.srv.requests() {
		if r.Method != "GET" {
			t.Fatalf("credentialed non-GET: %s", r)
		}
	}
}

func TestGitAccessPushDeniedCooldownDoesNotFileAgain(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("denied")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	filed := hostProposal(t, f, "ask", p)
	id, ok := filed["request_id"].(string)
	if !ok {
		t.Fatalf("not filed: %+v", filed)
	}
	if _, err := ApprovalStore().Decide(id, "pending", brokerstore.Decision{Deny: true}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"evaluate", "ask"} {
		got := hostProposal(t, f, op, p)
		if got["verdict"] != "refused" || got["request_id"] != nil {
			t.Fatalf("%s refiled denied push: %+v", op, got)
		}
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("denied push moved remote")
	}
}

func TestGitAccessPushInvalidCandidateNeverContactsSSH(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	base := gitproto.Command{Old: old, New: strings.Repeat("b", 40), Ref: "refs/heads/main"}
	for _, mutate := range []func(*gitproto.PushProposal){
		func(p *gitproto.PushProposal) { p.Commands[0].Old = "not-an-oid" },
		func(p *gitproto.PushProposal) { p.Commands[0].New = strings.Repeat("b", 64) },
		func(p *gitproto.PushProposal) { p.Commands[0].Ref = "refs/heads/main\nattack" },
		func(p *gitproto.PushProposal) { p.Commands[0].Ref = "refs/heads/" },
		func(p *gitproto.PushProposal) { p.Commands[0].Ref = "refs/heads/a..b" },
		func(p *gitproto.PushProposal) { p.Commands = append(p.Commands, base) },
		func(p *gitproto.PushProposal) { p.PushOptions = []string{"ci.skip\nattack"} },
	} {
		p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{base}, PushOptions: []string{}}
		mutate(&p)
		if got := hostProposal(t, f, "ask", p); got["verdict"] != "refused" || got["request_id"] != nil || f.sshRan() {
			t.Fatalf("invalid proposal contacted SSH or filed: %+v", got)
		}
	}
}

func TestGitAccessPushOptionsRequireAdvertisementCapability(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("option capability")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{"ci.skip"}}
	if got := hostProposal(t, f, "ask", p); got["verdict"] != "refused" || got["request_id"] != nil {
		t.Fatalf("unspendable option filed: %+v", got)
	}
	reqs, _, err := ApprovalStore().List()
	if err != nil || len(reqs) != 0 {
		t.Fatalf("unsupported option created requests: %+v %v", reqs, err)
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("option preflight wrote remote")
	}
}

func TestGitAccessPushExpiredUnspentOnceProjectsFirstRetry(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("window closed")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	filed := hostProposal(t, f, "ask", p)
	id := filed["request_id"].(string)
	g := allow(t, id)
	f.broker.store.Now = func() time.Time { return g.Expires.Add(time.Second) }
	for _, op := range []string{"evaluate", "ask"} {
		if err := os.WriteFile(filepath.Join(f.record, "argv"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		got := hostProposal(t, f, op, p)
		if got["verdict"] != "refused" || got["request_id"] != nil || !strings.Contains(strings.ToLower(got["rule"].(string)), "window closed") {
			t.Fatalf("%s missed unspent lapse: %+v", op, got)
		}
		next, ok := got["next"].([]any)
		wantAdvice := "run the same push once to record the expired Allow once refusal; it may contact the remote, but sends no ref updates upstream"
		if !ok || len(next) != 3 || !strings.Contains(strings.ToLower(next[1].(map[string]any)["detail"].(string)), strings.ToLower(wantAdvice)) || !strings.Contains(next[2].(map[string]any)["detail"].(string), "file a new approval request") || next[0].(map[string]any)["run"] != nil {
			t.Fatalf("%s gave advice that skips the first real refusal or misstates remote contact: %+v", op, got["next"])
		}
		if !f.sshRan() {
			t.Fatalf("%s did not contact the fake remote during the read-only preflight", op)
		}
	}
	if got := f.upstreamRef("refs/heads/main"); got != old {
		t.Fatalf("read-only lapse preflight changed the remote ref: got %s want %s", got, old)
	}
	reqs, _, err := ApprovalStore().List()
	if err != nil || len(reqs) != 1 || reqs[0].Lapsed {
		t.Fatalf("pre-ask mutated lapse: %+v %v", reqs, err)
	}
	// The atomic pre-ask path also leaves the lapsed marker untouched.
	q := f.broker.pushRequest(func() string { r, _ := gitproto.Identity(p.URL); return r }(), p.Commands, p.PushOptions)
	_, _, _, err = f.broker.store.FileIfStillAsksContext(context.Background(), q, f.broker.revalidate)
	if _, ok := brokerstore.IsLapsedAllow(err); !ok {
		t.Fatalf("atomic pre-ask did not refuse lapsed grant: %v", err)
	}
	reqs, _, err = ApprovalStore().List()
	if err != nil || len(reqs) != 1 || reqs[0].Lapsed {
		t.Fatalf("atomic pre-ask consumed lapse: %+v %v", reqs, err)
	}
	_, err = f.git("push", "origin", "HEAD:main")
	if err == nil {
		t.Fatal("real first retry should refuse the lapsed approval")
	}
	reqs, _, err = ApprovalStore().List()
	if err != nil || len(reqs) != 1 || !reqs[0].Lapsed {
		t.Fatalf("real retry did not consume lapse: %+v %v", reqs, err)
	}
	if got := hostProposal(t, f, "ask", p); got["request_id"] == nil {
		t.Fatalf("second retry did not file: %+v", got)
	}
}

func TestGitAccessPushLapsedAllowExpiryDuringFilingGivesFirstRetryAdvice(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("allow expires during pre-ask")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	filed := hostProposal(t, f, "ask", p)
	id := filed["request_id"].(string)
	current := time.Now()
	f.broker.store.Now = func() time.Time { return current }
	fired := false
	f.broker.store.AfterTransaction = func(error) {
		if !fired {
			fired = true
			g := allow(t, id)
			current = g.Expires.Add(time.Second)
		}
	}
	got := hostProposal(t, f, "ask", p)
	if !fired || got["verdict"] != "refused" || got["request_id"] != nil || !strings.Contains(got["rule"].(string), "window closed") {
		t.Fatalf("atomic expiry was not reported as lapsed: fired=%t response=%+v", fired, got)
	}
	next, ok := got["next"].([]any)
	if !ok || len(next) != 3 || !strings.Contains(next[1].(map[string]any)["detail"].(string), "record the expired Allow once refusal") || !strings.Contains(next[2].(map[string]any)["detail"].(string), "file a new approval request") {
		t.Fatalf("atomic expiry gave advice that skips the first real refusal: %+v", got["next"])
	}
	requests, _, err := ApprovalStore().List()
	if err != nil || len(requests) != 1 || requests[0].Lapsed {
		t.Fatalf("atomic pre-ask consumed the lapse or filed a request: %+v %v", requests, err)
	}
}

func TestGitAccessPushLegacyProposalKeyCannotRotateIntoAct(t *testing.T) {
	f := newFixture(t, "tee")
	f.commit("legacy frame control")
	old := f.upstreamRef("refs/heads/main")
	sendReserved := func(key string) {
		t.Helper()
		argvBefore, err := os.ReadFile(filepath.Join(f.record, "argv"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		conn, err := svcendpoint.Dial(f.endpoint, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		raw := `{"service":"git-receive-pack","url":"` + f.broker.bound.Repos[0] + `","` + key + `":null}`
		if err := frameproto.WriteRequest(conn, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		var rc int
		for {
			frame, err := frameproto.ReadFrame(conn)
			if err != nil {
				t.Fatal(err)
			}
			if frame.StreamID == frameproto.StreamExit {
				rc, err = frameproto.ExitCode(frame.Payload)
				if err != nil {
					t.Fatal(err)
				}
				break
			}
		}
		conn.Close()
		argvAfter, err := os.ReadFile(filepath.Join(f.record, "argv"))
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if rc != 64 || !bytes.Equal(argvBefore, argvAfter) {
			t.Fatalf("legacy frame with reserved %q reached ACT: %s rc=%d", key, raw, rc)
		}
	}
	for _, key := range []string{"proposal", "commands", "push_options"} {
		sendReserved(key)
	}
	out, err := f.git("push", "origin", "HEAD:main")
	id := refusedRequest(t, out, err)
	grant := allow(t, id)
	argvBefore, err := os.ReadFile(filepath.Join(f.record, "argv"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"proposal", "commands", "push_options"} {
		sendReserved(key)
	}
	argvAfter, err := os.ReadFile(filepath.Join(f.record, "argv"))
	if err != nil || !bytes.Equal(argvBefore, argvAfter) || grant.UsedBy != "" {
		t.Fatalf("reserved legacy frame contacted SSH or spent Allow once: err=%v grant=%+v", err, grant)
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("reserved legacy frame wrote the remote")
	}
	f.mustPush("origin", "HEAD:main")
	if f.upstreamRef("refs/heads/main") != mustGit(t, f.ws, "rev-parse", "HEAD") {
		t.Fatal("valid legacy frame failed to write fake bare remote")
	}
}

func TestGitAccessPushMalformedHTTPSNeverGetsOrSettles(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{})
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: h.url, Commands: []gitproto.Command{{Old: h.upstreamRef("refs/heads/main"), New: strings.Repeat("a", 40), Ref: "refs/heads/main"}}, PushOptions: []string{"ci.skip\x00malformed"}}
	for _, op := range []string{"evaluate", "ask"} {
		if got := hostProposal(t, h.fixture, op, p); got["verdict"] != "refused" {
			t.Fatalf("%s: %+v", op, got)
		}
	}
	if len(h.srv.requests()) != 0 || len(h.credCalls()) != 0 {
		t.Fatalf("invalid HTTPS proposal contacted host: %s calls=%v", h.srv.log(t), h.credCalls())
	}
}

func setFixturePushAdvertisement(t *testing.T, f *fixture, ref, id, caps string) {
	t.Helper()
	var advertisement bytes.Buffer
	w := gitproto.NewWriter(&advertisement)
	if err := w.Data([]byte(id + " " + ref + "\x00" + caps + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.root, "push-advertisement")
	if err := os.WriteFile(path, advertisement.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	writeExec(t, filepath.Join(f.bin, "ssh"), "#!/bin/sh\ncat "+strconv.Quote(path)+"\n")
}

func TestGitAccessPushOptionsAdvertisementIsExactAndDigestPreservesOrder(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("ordered push options")
	newID := mustGit(t, f.ws, "rev-parse", "HEAD")
	setFixturePushAdvertisement(t, f, "refs/heads/main", old, "report-status push-options")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0],
		Commands:    []gitproto.Command{{Old: old, New: newID, Ref: "refs/heads/main"}},
		PushOptions: []string{"ci.skip", "priority=1"}}
	first := hostProposal(t, f, "evaluate", p)
	if first["verdict"] != "asks" {
		t.Fatalf("bare push-options was not accepted: %+v", first)
	}
	repo, err := gitproto.Identity(p.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := f.broker.pushRequest(repo, p.Commands, p.PushOptions).Digest
	if first["command_digest"] != want {
		t.Fatalf("ordered push option digest %v, want %s", first["command_digest"], want)
	}
	p.PushOptions = []string{"priority=1", "ci.skip"}
	second := hostProposal(t, f, "evaluate", p)
	if second["verdict"] != "asks" || second["command_digest"] == first["command_digest"] {
		t.Fatalf("option order did not bind the evaluated command: first=%+v second=%+v", first, second)
	}
	requests, grants, err := ApprovalStore().List()
	if err != nil || len(requests) != 0 || len(grants) != 0 || f.upstreamRef("refs/heads/main") != old {
		t.Fatalf("evaluate filed, granted, or wrote: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
}

func TestGitAccessPushMalformedAdvertisementCapabilitiesRefuseAtEndpoint(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("malformed capabilities")
	newID := mustGit(t, f.ws, "rev-parse", "HEAD")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0],
		Commands: []gitproto.Command{{Old: old, New: newID, Ref: "refs/heads/main"}}, PushOptions: []string{"ci.skip"}}
	for _, caps := range []string{
		"report-status push-options=1",
		"report-status push-options=1 push-options=2",
		"report-status object-format",
		"report-status bad!cap",
		"report-status object-format=sha512",
		"report-status object-format=sha1 object-format=sha1",
		"report-status object-format=sha1 object-format=sha256",
	} {
		setFixturePushAdvertisement(t, f, "refs/heads/main", old, caps)
		got := hostProposal(t, f, "ask", p)
		if got["verdict"] != "refused" || got["request_id"] != nil {
			t.Fatalf("malformed capabilities %q reached approval: %+v", caps, got)
		}
	}
	requests, _, err := ApprovalStore().List()
	if err != nil || len(requests) != 0 || f.upstreamRef("refs/heads/main") != old {
		t.Fatalf("malformed advertisements filed or wrote: requests=%+v err=%v", requests, err)
	}
}

func TestGitAccessPushAdvertisementCapabilityAndWidths(t *testing.T) {
	for _, width := range []int{40, 64} {
		old, newID := strings.Repeat("a", width), strings.Repeat("b", width)
		cmd := gitproto.Command{Old: old, New: newID, Ref: "refs/heads/main"}
		format := ""
		if width == 64 {
			format = " object-format=sha256"
		}
		for _, capline := range []string{"report-status", "report-status push-options"} {
			var adv bytes.Buffer
			w := gitproto.NewWriter(&adv)
			if err := w.Data([]byte(old + " " + cmd.Ref + "\x00" + capline + format + "\n")); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			_, err := verifyProposedPush([]gitproto.Command{cmd}, []string{"ci.skip"}, &adv)
			if (err == nil) != (capline == "report-status push-options") {
				t.Fatalf("width %d caps %q: %v", width, capline+format, err)
			}
		}
		var empty bytes.Buffer
		w := gitproto.NewWriter(&empty)
		if err := w.Data([]byte(strings.Repeat("0", width) + " capabilities^{}\x00push-options" + format + "\n")); err != nil {
			t.Fatal(err)
		}
		if err := w.Flush(); err != nil {
			t.Fatal(err)
		}
		cmd.Old = strings.Repeat("0", width)
		if got, err := verifyProposedPush([]gitproto.Command{cmd}, []string{"ci.skip"}, &empty); err != nil || got != widthName(width) {
			t.Fatalf("empty %d: %s %v", width, got, err)
		}
	}
	old, newID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	cmd := gitproto.Command{Old: old, New: newID, Ref: "refs/heads/main"}
	var sha1 bytes.Buffer
	w := gitproto.NewWriter(&sha1)
	if err := w.Data([]byte(old + " " + cmd.Ref + "\x00push-options object-format=sha1\n")); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(); err != nil {
		t.Fatal(err)
	}
	if got, err := verifyProposedPush([]gitproto.Command{cmd}, []string{"ci.skip"}, &sha1); err != nil || got != "sha1" {
		t.Fatalf("explicit SHA-1 format rejected: %s %v", got, err)
	}
}

func TestGitAccessPushRejectsMalformedAdvertisementCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, id, caps string
	}{
		{name: "push-options value is not bare", id: strings.Repeat("a", 40), caps: "push-options=1"},
		{name: "duplicate capability name", id: strings.Repeat("a", 40), caps: "push-options=1 push-options=2"},
		{name: "unknown format", id: strings.Repeat("a", 40), caps: "object-format=sha512"},
		{name: "bare format", id: strings.Repeat("a", 40), caps: "object-format"},
		{name: "empty format", id: strings.Repeat("a", 40), caps: "object-format="},
		{name: "duplicate format", id: strings.Repeat("a", 40), caps: "object-format=sha1 object-format=sha1"},
		{name: "conflicting format", id: strings.Repeat("a", 40), caps: "object-format=sha1 object-format=sha256"},
		{name: "format width mismatch", id: strings.Repeat("a", 64), caps: "object-format=sha1"},
		{name: "sha256 defaults to sha1", id: strings.Repeat("a", 64), caps: "push-options"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := gitproto.Command{Old: tc.id, New: strings.Repeat("b", len(tc.id)), Ref: "refs/heads/main"}
			var adv bytes.Buffer
			w := gitproto.NewWriter(&adv)
			if err := w.Data([]byte(tc.id + " " + cmd.Ref + "\x00" + tc.caps + "\n")); err != nil {
				t.Fatal(err)
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if _, err := verifyProposedPush([]gitproto.Command{cmd}, []string{"ci.skip"}, &adv); err == nil {
				t.Fatalf("malformed/unsupported capability accepted: %q", tc.caps)
			}
		})
	}
}

func TestGitAccessPushLateExactGrantReturnsUpdatedClassification(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("late exact grant")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0],
		Commands: []gitproto.Command{{Old: old, New: mustGit(t, f.ws, "rev-parse", "HEAD"), Ref: "refs/heads/main"}}, PushOptions: []string{}}
	filed := hostProposal(t, f, "ask", p)
	id, ok := filed["request_id"].(string)
	if !ok {
		t.Fatalf("no exact-grant request filed: %+v", filed)
	}
	var grant brokerstore.Grant
	fired := false
	f.broker.store.AfterTransaction = func(error) {
		if !fired {
			fired = true
			grant = allow(t, id)
		}
	}
	got := hostProposal(t, f, "ask", p)
	if !fired || got["verdict"] != "runs" || got["request_id"] != nil || got["covered_by"] != grant.ID || got["eligibility_status"] != "exact-grant" || got["relies_on_until"] != grant.Expires.Format(time.RFC3339Nano) {
		t.Fatalf("late exact grant not recognized atomically: fired=%t response=%+v grant=%+v", fired, got, grant)
	}
	classified, ok := got["classified"].([]any)
	if !ok || len(classified) != 1 || classified[0].(map[string]any)["covered_by"] != grant.ID {
		t.Fatalf("late exact grant missing from classification: %+v", got["classified"])
	}
	requests, grants, err := ApprovalStore().List()
	if err != nil || len(requests) != 1 || len(grants) != 1 || grants[0].UsedBy != "" || f.upstreamRef("refs/heads/main") != old {
		t.Fatalf("late evaluate filed, spent, or wrote: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
}

func TestGitAccessPushLateMixedCoverageReturnsAtomicClassification(t *testing.T) {
	f := newFixtureOpts(t, fixtureOpts{grants: []brokerscope.Grant{{Kind: "branch", Repo: fixtureRepo, Branch: "config"}}})
	main := f.upstreamRef("refs/heads/main")
	mustGit(t, f.bare, "update-ref", "refs/heads/config", main)
	mustGit(t, f.bare, "update-ref", "refs/heads/work", main)
	f.commit("late mixed coverage")
	head := mustGit(t, f.ws, "rev-parse", "HEAD")
	workOld := f.upstreamRef("refs/heads/work")
	workProposal := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0],
		Commands: []gitproto.Command{{Old: workOld, New: head, Ref: "refs/heads/work"}}, PushOptions: []string{}}
	pending := hostProposal(t, f, "ask", workProposal)
	workRequest, ok := pending["request_id"].(string)
	if !ok {
		t.Fatalf("could not seed separate working-set request: %+v", pending)
	}

	configOld := f.upstreamRef("refs/heads/config")
	proposal := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{
		{Old: configOld, New: head, Ref: "refs/heads/config"},
		{Old: workOld, New: head, Ref: "refs/heads/work"},
	}, PushOptions: []string{}}
	var entry string
	fired := false
	f.broker.store.AfterTransaction = func(error) {
		if fired {
			return
		}
		fired = true
		entries, err := ApprovalStore().DecideWorking(workRequest, brokerstore.Decision{Add: true})
		if err != nil {
			t.Error(err)
			return
		}
		entry = entries[0].ID
		allowKinds(t, "branch")
	}
	got := hostProposal(t, f, "ask", proposal)
	if !fired || got["verdict"] != "runs" || got["request_id"] != nil || got["covered_by"] != nil {
		t.Fatalf("mixed atomic coverage was not summarized honestly: fired=%t response=%+v", fired, got)
	}
	classified, ok := got["classified"].([]any)
	if !ok || len(classified) != 2 {
		t.Fatalf("prediction did not return atomic per-ref classifications: %+v", got["classified"])
	}
	first, firstOK := classified[0].(map[string]any)
	second, secondOK := classified[1].(map[string]any)
	if !firstOK || !secondOK || first["ref"] != "refs/heads/config" || first["covered_by"] != "branch:config" ||
		second["ref"] != "refs/heads/work" || second["covered_by"] != entry {
		t.Fatalf("late ref coverage sources are stale or mislabeled: %+v", classified)
	}
	requests, grants, err := ApprovalStore().List()
	if err != nil || len(requests) != 1 || len(grants) != 0 || f.upstreamRef("refs/heads/config") != configOld || f.upstreamRef("refs/heads/work") != workOld {
		t.Fatalf("late evaluation filed/spent/wrote: requests=%+v grants=%+v err=%v", requests, grants, err)
	}
}

func TestGitAccessPushLateWorkingCoverageRunsWithoutOnceLabelOrSpend(t *testing.T) {
	f := newFixture(t, "tee")
	old := f.upstreamRef("refs/heads/main")
	f.commit("late coverage")
	head := mustGit(t, f.ws, "rev-parse", "HEAD")
	p := gitproto.PushProposal{Service: serviceReceivePack, URL: f.broker.bound.Repos[0], Commands: []gitproto.Command{{Old: old, New: head, Ref: "refs/heads/main"}}, PushOptions: []string{}}
	first := hostProposal(t, f, "ask", p)
	id := first["request_id"].(string)
	var entry string
	var expiry time.Time
	fired := false
	f.broker.store.AfterTransaction = func(error) {
		if fired {
			return
		}
		fired = true
		entries, err := ApprovalStore().DecideWorking(id, brokerstore.Decision{For: time.Hour})
		if err != nil {
			t.Error(err)
			return
		}
		entry, expiry = entries[0].ID, entries[0].Expires
	}
	got := hostProposal(t, f, "ask", p)
	if got["verdict"] != "runs" || got["covered_by"] != entry || got["eligibility_status"] != "not-covered" || got["relies_on_until"] != expiry.Format(time.RFC3339Nano) || got["request_id"] != nil || len(got["next"].([]any)) != 0 || strings.Contains(got["rule"].(string), "Allow once") {
		t.Fatalf("late working coverage mislabeled or filed: %+v entry=%s", got, entry)
	}
	reqs, grants, err := ApprovalStore().List()
	if err != nil || len(reqs) != 1 || len(grants) != 0 {
		t.Fatalf("late coverage mutated authority: %+v %+v %v", reqs, grants, err)
	}
	if f.upstreamRef("refs/heads/main") != old {
		t.Fatal("pre-ask wrote remote")
	}
	f.mustPush("origin", "HEAD:main")
	if f.upstreamRef("refs/heads/main") != head {
		t.Fatal("ACT did not recognize working coverage")
	}
}

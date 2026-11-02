package gitbroker

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// requestIDLen is the length of a request id, "r-" and 32 hex digits.
const requestIDLen = 34

// commandLines is the commands a refusal's menu puts alone on their own indented lines
// (approvalwait.Menu.Render), each trimmed.
func commandLines(out string) []string {
	var cmds []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "    ") && strings.TrimSpace(line) != "" {
			cmds = append(cmds, strings.TrimSpace(line))
		}
	}
	return cmds
}

// refusedRequest is the request a refused push's output names on its ref line, checking
// that the user is told the host command that approves it.
func refusedRequest(t *testing.T, out string, err error) string {
	t.Helper()
	i := strings.Index(out, "(needs approval: r-")
	if err == nil || i < 0 || !strings.Contains(out, "! [remote rejected]") {
		t.Fatalf("the push was not refused naming a request: %v\n%s", err, out)
	}
	id := out[i+len("(needs approval: ") : i+len("(needs approval: ")+requestIDLen]
	lines := commandLines(out)
	if !slices.Contains(lines, "yolo approve "+id) || !slices.Contains(lines, "yolo wait "+id) {
		t.Fatalf("the refusal does not put `yolo approve %s` and `yolo wait %s` each on its own line:\n%s", id, id, out)
	}
	return id
}

// allow decides request id Allow once, as `yolo approve <id>` does, and returns its grant.
func allow(t *testing.T, id string) brokerstore.Grant {
	t.Helper()
	g, err := ApprovalStore().Decide(id, "pending", brokerstore.Decision{})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// answerFirstPending decides the first request the store holds pending, as a human at the host
// would during a push's wait, and reports the decision's error on the returned channel.
func answerFirstPending(t *testing.T, d brokerstore.Decision) <-chan error {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			reqs, _, err := ApprovalStore().List()
			if err != nil {
				done <- err
				return
			}
			for _, q := range reqs {
				if q.State == "pending" {
					_, err := ApprovalStore().Decide(q.ID, "pending", d)
					done <- err
					return
				}
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- os.ErrDeadlineExceeded
	}()
	return done
}

func TestPushToMainAllowedOnceGoesThroughSSH(t *testing.T) {
	f := newFixture(t, "tee")
	f.commit("one")
	before, head := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD")

	// 1. Nothing covers main: the push files a request, waits, and is refused naming it.
	out, err := f.git("push", "origin", "HEAD:main")
	id := refusedRequest(t, out, err)
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved before approval")
	}
	if sent, _ := os.ReadFile(filepath.Join(f.record, "stdin")); string(sent) != "0000" {
		t.Fatalf("the upstream was sent %q, want an empty command list", sent)
	}

	// 2. The host allows it once; the same push goes through and spends it.
	g := allow(t, id)
	f.mustPush("origin", "HEAD:main")
	if got := f.upstreamRef("refs/heads/main"); got != head {
		t.Fatalf("upstream main %q, want %q", got, head)
	}
	p := lastPush(t)
	if p.Set != setPush || p.Outcome != "ran" || p.Grant != g.ID || p.Request != id || p.Refs[0].CoveredBy != g.ID {
		t.Fatalf("the allowed push's audit %+v", p)
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("an update of main joined the working set (%d entries)", n)
	}

	// 3. The same push again (main put back) is a new request: Allow once was one use.
	mustGit(t, f.bare, "update-ref", "refs/heads/main", before)
	out, err = f.git("push", "origin", "HEAD:main")
	if again := refusedRequest(t, out, err); again == id {
		t.Fatalf("the spent request %s was named again", id)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved on a spent Allow once")
	}
}

// Allowed while the push waits, the push goes through in the same call.
func TestPushAllowedDuringTheWaitGoesThrough(t *testing.T) {
	f := newFixture(t, "")
	f.broker.waitLimit = 15 * time.Second
	f.commit("one")
	answered := answerFirstPending(t, brokerstore.Decision{})
	f.mustPush("origin", "HEAD:main")
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream main %q, want %q", got, want)
	}
}

func TestPushToMainAllowedOnceGoesThroughHTTPS(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{})
	before := mustGit(t, h.bare, "rev-parse", "main")
	h.localCommit()
	out, err := h.git("push", "origin", "HEAD:main")
	id := refusedRequest(t, out, err)
	for _, r := range h.srv.requests() {
		if r.Method == http.MethodPost {
			t.Fatalf("a push waiting for approval reached the server with a POST:\n%s", h.srv.log(t))
		}
	}
	g := allow(t, id)
	if out, err := h.git("push", "origin", "HEAD:main"); err != nil {
		t.Fatalf("the allowed push: %v\n%s\n%s", err, out, h.srv.log(t))
	}
	if got, want := mustGit(t, h.bare, "rev-parse", "main"), mustGit(t, h.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("bare main %s, local HEAD %s", got, want)
	}
	if p := lastPush(t); p.Set != setPush || p.Grant != g.ID {
		t.Fatalf("the allowed push's audit %+v", p)
	}
	mustGit(t, h.bare, "update-ref", "refs/heads/main", before)
	out, err = h.git("push", "origin", "HEAD:main")
	if again := refusedRequest(t, out, err); again == id {
		t.Fatal("a spent Allow once covered the same push again")
	}
}

func TestPushDeniedNothingForwarded(t *testing.T) {
	f := newFixture(t, "tee")
	f.broker.waitLimit = 15 * time.Second
	f.commit("one")
	before := f.upstreamRef("refs/heads/main")
	answered := answerFirstPending(t, brokerstore.Decision{Deny: true})
	out, err := f.git("push", "origin", "HEAD:main")
	if aerr := <-answered; aerr != nil {
		t.Fatal(aerr)
	}
	if err == nil || !strings.Contains(out, "! [remote rejected]") || !strings.Contains(out, ", denied on the host)") ||
		!strings.Contains(strings.Join(strings.Fields(out), " "), "ask the user before pushing it again") {
		t.Fatalf("a denied push: %v\n%s", err, out)
	}
	if f.upstreamRef("refs/heads/main") != before {
		t.Fatal("main moved on a denied push")
	}
	if sent, _ := os.ReadFile(filepath.Join(f.record, "stdin")); string(sent) != "0000" {
		t.Fatalf("the upstream was sent %q, want an empty command list", sent)
	}
	// The same push again files nothing new for a while: the denial stands.
	f.broker.waitLimit = 50 * time.Millisecond
	out, err = f.git("push", "origin", "HEAD:main")
	if err == nil || !strings.Contains(out, "the host denied this same push") {
		t.Fatalf("a push the host just denied, again: %v\n%s", err, out)
	}
	reqs, _, lerr := ApprovalStore().List()
	if lerr != nil || len(reqs) != 1 {
		t.Fatalf("requests after a denial and a retry: %v %+v", lerr, reqs)
	}
}

// A new branch with no create-branch grant asks; allowed once, it joins the working set by that
// grant, and later pushes to it go through unasked.
func TestAllowOnceCreationJoinsTheWorkingSet(t *testing.T) {
	f := newFixture(t, "")
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:refs/heads/agent/x")
	g := allow(t, refusedRequest(t, out, err))
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	live := liveEntries(t)
	if len(live) != 1 || live[0].Resource != "refs/heads/agent/x" || live[0].By != g.ID {
		t.Fatalf("working set after an allowed creation: %+v", live)
	}
	f.commit("two")
	f.mustPush("origin", "HEAD:refs/heads/agent/x")
	if p := lastPush(t); p.Set != setWorkingSet || p.Refs[0].CoveredBy != live[0].ID {
		t.Fatalf("the next push's audit %+v", p)
	}
}

// A tag no grant covers asks too (GB-D18, GB-D25); allowed, it is pushed and never joins the
// working set.
func TestTagPushAsksForApproval(t *testing.T) {
	f := newFixture(t, "")
	mustGit(t, f.ws, "tag", "v1")
	out, err := f.git("push", "origin", "refs/tags/v1")
	if !strings.Contains(out, "no tag grant covers it, so this push asks for approval") {
		t.Fatalf("the tag's line does not say it asks:\n%s", out)
	}
	allow(t, refusedRequest(t, out, err))
	f.mustPush("origin", "refs/tags/v1")
	if f.upstreamRef("refs/tags/v1") == "" {
		t.Fatal("the allowed tag is not upstream")
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("a tag joined the working set (%d entries)", n)
	}
}

// A push holding a ref nothing approves files no request.
func TestPushWithARefOutsideHeadsAndTagsFilesNoRequest(t *testing.T) {
	f := newFixture(t, "")
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:main", "HEAD:refs/notes/x")
	if err == nil || !strings.Contains(out, reasonOutsideRefs) || strings.Contains(out, "needs approval: r-") {
		t.Fatalf("a push with a note: %v\n%s", err, out)
	}
	if reqs, _, err := ApprovalStore().List(); err != nil || len(reqs) != 0 {
		t.Fatalf("requests filed: %v %+v", err, reqs)
	}
}

// The daemon hands the core its store and lifetime, so the user's on_request runs for the
// requests this broker files, and its cleanup refuses those still pending.
func TestHandlerRunsOnRequestAndRefusesPendingAtStop(t *testing.T) {
	f := newFixture(t, "")
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	sf.BrokerStart, sf.BrokerPID = "", 0
	l, code := newHandler(context.Background(), f.scopeFile, sf, f.ws, &lockedBuffer{})
	if code != 0 || l.Handle == nil || l.Store == nil || l.Start == "" || l.Store.Revalidate == nil || l.Store.Audit == nil {
		t.Fatalf("launch %+v, code %d", l, code)
	}
	repo, _ := gitproto.Identity(origin)
	q := brokerstore.Request{Jail: paths.JailShortHash(sf.Container), Start: l.Start, Workspace: sf.Workspace,
		LaunchID: sf.LaunchID, Argv: pushArgv([]gitproto.Command{{Old: oldID, New: newID, Ref: "refs/heads/main"}}, nil),
		Repo: repo, Set: setPush, Policy: pushPolicy, StdinDigest: pushStdinDigest}
	filed, err := l.Store.File(q)
	if err != nil {
		t.Fatal(err)
	}
	l.Cleanup()
	got, err := l.Store.Status(q.Jail, l.Start, filed.ID)
	if err != nil || got.State != "refused" {
		t.Fatalf("a pending request after the broker stopped: %v %+v", err, got)
	}
}

// A push's options are part of what an Allow once approves (GB-D17): the request lists each
// one, and the same ref commands sent with other options, or with none, spend nothing.
func TestPushOptionsAreBoundIntoTheAllowOnce(t *testing.T) {
	f := newFixture(t, "")
	mustGit(t, f.bare, "config", "receive.advertisePushOptions", "true")
	f.commit("one")
	before, head := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD")

	out, err := f.git("push", "-o", "ci.skip", "origin", "HEAD:main")
	id := refusedRequest(t, out, err)
	reqs, _, err := ApprovalStore().List()
	if err != nil || len(reqs) != 1 || !slices.Contains(reqs[0].Argv, pushOptionPrefix+"ci.skip") {
		t.Fatalf("the request does not name its push option: %v %+v", err, reqs)
	}
	g := allow(t, id)

	unspent := func(step string) {
		t.Helper()
		_, gs, err := ApprovalStore().List()
		if err != nil || len(gs) != 1 || gs[0].ID != g.ID || gs[0].Uses != 1 {
			t.Fatalf("%s: the grant was spent: %v %+v", step, err, gs)
		}
		if f.upstreamRef("refs/heads/main") != before {
			t.Fatalf("%s: main moved", step)
		}
	}
	out, err = f.git("push", "-o", "merge_request.merge_when_pipeline_succeeds", "origin", "HEAD:main")
	if again := refusedRequest(t, out, err); again == id {
		t.Fatal("other push options were covered by the Allow once")
	}
	unspent("other options")
	out, err = f.git("push", "origin", "HEAD:main")
	if again := refusedRequest(t, out, err); again == id {
		t.Fatal("a push without the options was covered by the Allow once")
	}
	unspent("no options")

	f.mustPush("-o", "ci.skip", "origin", "HEAD:main")
	if got := f.upstreamRef("refs/heads/main"); got != head {
		t.Fatalf("the approved push with its option did not land: %q", got)
	}
}

// Over HTTPS too, a push allowed while it waits goes through in the same call.
func TestHTTPSPushAllowedDuringTheWaitGoesThrough(t *testing.T) {
	h := newHTTPSFixture(t, httpsOpts{})
	h.broker.waitLimit = 15 * time.Second
	h.localCommit()
	answered := answerFirstPending(t, brokerstore.Decision{})
	if out, err := h.git("push", "origin", "HEAD:main"); err != nil {
		t.Fatalf("the push allowed during its wait: %v\n%s\n%s", err, out, h.srv.log(t))
	}
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	if got, want := mustGit(t, h.bare, "rev-parse", "main"), mustGit(t, h.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("bare main %s, local HEAD %s", got, want)
	}
	if p := lastPush(t); p.Set != setPush || p.Grant == "" {
		t.Fatalf("the allowed push's audit %+v", p)
	}
}

package gitbroker

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// The two answers a push request takes besides Allow once and Deny (GB-D26): Allow push to the
// refs for a time, and Add the branches to the working set. Both are working-set entries per
// ref, decided with brokerstore.Store.DecideWorking, which is what `yolo approve <id> --for` and
// `--add` call through gitbroker.ApprovalStore.

// fakeClock is a clock a test moves by hand.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Now().UTC().Truncate(time.Second)}
}

// TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough: Allow push for 15 minutes records
// a timed entry on the refs that needed approval, and every push to them within it goes
// through, unspent, while a ref it does not name still asks.
func TestApproveGitForRecordsTimedGrantAndPushWithinItGoesThrough(t *testing.T) {
	f := newFixture(t, "")
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:main")
	id := refusedRequest(t, out, err)
	for _, want := range []string{"--for 15m"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the refusal does not name %q:\n%s", want, out)
		}
	}
	for _, removed := range []string{"--add", "--remember"} {
		if strings.Contains(out, removed) {
			t.Fatalf("the refusal still offers the removed %s (GB-D28):\n%s", removed, out)
		}
	}

	entries, err := ApprovalStore().DecideWorking(id, brokerstore.Decision{For: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Resource != "refs/heads/main" || !entries[0].Timed() ||
		entries[0].Workspace != f.broker.bound.Workspace || entries[0].By != "human" {
		t.Fatalf("entries %+v", entries)
	}
	if rs, gs, err := ApprovalStore().List(); err != nil || len(gs) != 0 || rs[0].State != "allowed" {
		t.Fatalf("a timed answer left grants or did not allow the request: %+v %+v %v", rs, gs, err)
	}

	for _, name := range []string{"two", "three"} {
		f.commit(name)
		f.mustPush("origin", "HEAD:main")
		if got, want := f.upstreamRef("refs/heads/main"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
			t.Fatalf("upstream main %q, want %q", got, want)
		}
		p := lastPush(t)
		if p.Set != setTimed || p.Outcome != "ran" || p.Refs[0].CoveredBy != entries[0].ID {
			t.Fatalf("the timed push's audit %+v", p)
		}
	}
	if n := len(liveEntries(t)); n != 1 {
		t.Fatalf("an update of main changed the working set: %d live entries", n)
	}

	// The grant is per ref: another branch update still asks.
	mustGit(t, f.bare, "update-ref", "refs/heads/other", mustGit(t, f.ws, "rev-parse", "HEAD~3"))
	out, err = f.git("push", "-f", "origin", "HEAD:other")
	if again := refusedRequest(t, out, err); again == id {
		t.Fatalf("the request %s was named again for another ref", id)
	}
}

// TestApproveGitAddJoinsWorkingSetAcrossRelaunch: the store's Add, what the removed
// `yolo approve --add` wrote (GB-D28), put the branch in the workspace's working set for good;
// such an entry keeps covering, so a new broker of a new launch, holding no grant, still covers it.
func TestApproveGitAddJoinsWorkingSetAcrossRelaunch(t *testing.T) {
	f := newFixture(t, "")
	mustGit(t, f.bare, "update-ref", "refs/heads/dev", f.upstreamRef("refs/heads/main"))
	f.commit("one")
	out, err := f.git("push", "-f", "origin", "HEAD:dev")
	id := refusedRequest(t, out, err)

	entries, err := ApprovalStore().DecideWorking(id, brokerstore.Decision{Add: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Resource != "refs/heads/dev" || entries[0].Timed() || entries[0].By != "human" {
		t.Fatalf("entries %+v", entries)
	}
	f.mustPush("-f", "origin", "HEAD:dev")

	// A new launch of the same workspace: a new broker, no grant but read.
	sf, err := brokerscope.ReadFile(f.scopeFile)
	if err != nil {
		t.Fatal(err)
	}
	first := f.broker.start
	sf.LaunchID, sf.Grants, sf.BrokerStart, sf.BrokerPID, sf.LiveScope = "fixture-launch-2", []brokerscope.Grant{readGrant}, "", 0, false
	f.launch(sf, nil)
	if f.broker.start == first {
		t.Fatal("the new broker kept the old start id")
	}
	f.commit("two")
	f.mustPush("-f", "origin", "HEAD:dev")
	if got, want := f.upstreamRef("refs/heads/dev"), mustGit(t, f.ws, "rev-parse", "HEAD"); got != want {
		t.Fatalf("upstream dev %q, want %q", got, want)
	}
	if p := lastPush(t); p.Set != setWorkingSet || p.Refs[0].CoveredBy != entries[0].ID {
		t.Fatalf("audit %+v", p)
	}
}

// TestAddIsRefusedForATagAndANewBranchNamingFor: the working set holds existing branches
// (GB-D11, GB-D18, BB-D90), so Add on a tag or a creation refuses, names `--for`, and changes
// nothing; the same request takes a timed answer.
func TestAddIsRefusedForATagAndANewBranchNamingFor(t *testing.T) {
	f := newFixture(t, "")
	f.commit("one")
	mustGit(t, f.ws, "tag", "v1")
	out, err := f.git("push", "origin", "v1")
	tag := refusedRequest(t, out, err)
	out, err = f.git("push", "origin", "HEAD:refs/heads/agent/new")
	created := refusedRequest(t, out, err)

	for _, id := range []string{tag, created} {
		_, err := ApprovalStore().DecideWorking(id, brokerstore.Decision{Add: true})
		if err == nil || !strings.Contains(err.Error(), "--for 15m") || !strings.Contains(err.Error(), "--add is not available") {
			t.Fatalf("--add on %s: %v", id, err)
		}
	}
	if n := len(liveEntries(t)); n != 0 {
		t.Fatalf("a refused Add recorded %d entries", n)
	}

	if _, err := ApprovalStore().DecideWorking(tag, brokerstore.Decision{For: time.Hour}); err != nil {
		t.Fatal(err)
	}
	f.mustPush("origin", "v1")
	if f.upstreamRef("refs/tags/v1") == "" {
		t.Fatal("the allowed tag did not reach the upstream")
	}
	if _, err := ApprovalStore().DecideWorking(created, brokerstore.Decision{For: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	f.mustPush("origin", "HEAD:refs/heads/agent/new")
	// The agent created the branch under the timed answer, so it is the workspace's for good.
	var lasting bool
	for _, e := range liveEntries(t) {
		if e.Resource == "refs/heads/agent/new" && !e.Timed() {
			lasting = true
		}
	}
	if !lasting {
		t.Fatalf("a branch created under a timed answer did not join the working set: %+v", liveEntries(t))
	}
}

// TestTimedGrantExpiresAndRefilesRequest: past its window the timed entry covers nothing, and
// the same push files a new request.
func TestTimedGrantExpiresAndRefilesRequest(t *testing.T) {
	clock := newClock()
	f := newFixture(t, "")
	f.broker.store.Now = clock.now
	f.commit("one")
	out, err := f.git("push", "origin", "HEAD:main")
	id := refusedRequest(t, out, err)

	s := ApprovalStore()
	s.Now = clock.now
	entries, err := s.DecideWorking(id, brokerstore.Decision{For: 15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if !entries[0].Expires.Equal(clock.now().Add(15 * time.Minute)) {
		t.Fatalf("expires %v, want 15 minutes after %v", entries[0].Expires, clock.now())
	}

	clock.advance(14 * time.Minute)
	f.mustPush("origin", "HEAD:main")

	clock.advance(2 * time.Minute)
	f.commit("two")
	out, err = f.git("push", "origin", "HEAD:main")
	if again := refusedRequest(t, out, err); again == id {
		t.Fatalf("the expired grant's request %s was named again", id)
	}
	if got := f.upstreamRef("refs/heads/main"); got == mustGit(t, f.ws, "rev-parse", "HEAD") {
		t.Fatal("a push past the window went through")
	}
}

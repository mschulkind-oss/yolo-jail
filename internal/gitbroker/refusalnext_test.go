package gitbroker

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// Every way a push that asked for approval is refused ends in a next step that works from
// where the user stands (the Happy Path Principle): a request already answered is never sent
// back to `yolo approve <its id>`, which would exit 3.
func TestEveryApprovalRefusalNamesAWorkingNextStep(t *testing.T) {
	const id = "r-0123456789abcdef0123456789abcdef"
	until := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	approveIt := "`yolo approve " + id + "`"
	for _, c := range []struct {
		name      string
		r         refusal
		want, not []string
	}{
		{"pending", pendingRefusal((&pushMenu{}).menu(id, []string{"refs/heads/main"})), []string{"\n    yolo approve " + id + "\n", "\n    yolo approve " + id + " --for 15m\n", "\n    yolo approve " + id + " --remember\n", "\n    yolo wait " + id + "\n", "within 15 minutes", "not through `yolo host-run`"}, nil},
		{"lapsed", lapsedRefusal(id), []string{"window closed", "no new request was filed", "push it from the host"}, []string{"\n    yolo approve " + id + "\n"}},
		{"allowed, grant gone", answeredRefusal(id, "allowed"),
			[]string{"allowed request " + id, "Run the same push again to file a new request"}, []string{approveIt}},
		{"denied", answeredRefusal(id, "denied"),
			[]string{"denied request " + id, "ask the user before pushing it again", "push it from the host"}, []string{approveIt}},
		{"refused at broker stop", answeredRefusal(id, "refused"),
			[]string{"broker that filed it stopped", "Run the same push again to file a new request"}, []string{approveIt, "denied"}},
		{"expired", answeredRefusal(id, "expired"),
			[]string{"expired", "push it from the host"}, []string{approveIt, "denied"}},
		{"another state", answeredRefusal(id, "mystery"),
			[]string{"mystery", "Run the same push again", "`yolo approve` on the host"}, []string{approveIt}},
		{"pending limit", filingRefusal(&brokerstore.PendingLimitError{Requests: []string{"r-a", "r-b", "r-c"}}),
			[]string{"r-a, r-b, r-c", "`yolo approve` on the host", "`yolo approve <id>`", "`yolo approve <id> --deny`", "run this push again"}, nil},
		{"denial cooldown", filingRefusal(&brokerstore.DenialCooldownError{Request: id, Until: until}),
			[]string{"The host denied this same push", id, until.Format(time.RFC3339), "push it from the host"}, []string{approveIt}},
		{"filing failed", filingRefusal(errors.New("disk full")),
			[]string{"disk full", "Run the push again", "`yolo approve` on the host"}, nil},
		{"grant check failed", storeRefusal(errors.New("lock timeout")),
			[]string{"lock timeout", "Run the push again", "`yolo approve` on the host"}, nil},
	} {
		if !strings.HasPrefix(c.r.reason, reasonNeedsApproval) || c.r.next == "" {
			t.Errorf("%s: reason %q, next %q", c.name, c.r.reason, c.r.next)
		}
		for _, w := range c.want {
			if !strings.Contains(c.r.next, w) {
				t.Errorf("%s: the next step does not say %q:\n%s", c.name, w, c.r.next)
			}
		}
		for _, n := range c.not {
			if strings.Contains(c.r.next, n) {
				t.Errorf("%s: the next step says %q:\n%s", c.name, n, c.r.next)
			}
		}
	}
}

// RefusalNextSteps hands the CLI's test every next step, so it can check each command named.
func TestRefusalNextStepsCoverEveryCase(t *testing.T) {
	if got := RefusalNextSteps(); len(got) < 9 {
		t.Fatalf("RefusalNextSteps returned %d steps", len(got))
	}
}

// At the pending limit, a real push is refused naming `yolo approve`, which lists the
// requests waiting and answers them.
func TestPushAtThePendingLimitNamesYoloApprove(t *testing.T) {
	f := newFixture(t, "")
	f.commit("one")
	for _, b := range []string{"a", "b", "c"} {
		out, err := f.git("push", "origin", "HEAD:refs/heads/agent/"+b)
		refusedRequest(t, out, err)
	}
	out, err := f.git("push", "origin", "HEAD:refs/heads/agent/d")
	flat := strings.Join(strings.Fields(out), " ")
	if err == nil || !strings.Contains(flat, "3 requests waiting") || !strings.Contains(flat, "`yolo approve` on the host") ||
		!strings.Contains(flat, "`yolo approve <id> --deny`") {
		t.Fatalf("a push at the pending limit: %v\n%s", err, out)
	}
}

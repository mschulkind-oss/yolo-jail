package cli

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// `yolo approve` records who answered (docs/design/jail-history.md JH-D11): the request's own
// on_request hook when its environment names that request (brokerhook.Env sets
// YOLO_REQUEST_ID), the human at the host otherwise, a hook for ANOTHER request included.
func TestApproveRecordsWhetherTheOnRequestHookAnswered(t *testing.T) {
	s := &brokerstore.Store{Dir: t.TempDir(), Source: "other", Revalidate: func(q brokerstore.Request) (brokerstore.Policy, error) { return q.Policy, nil }}
	file := func(n string) brokerstore.Request {
		q, err := s.File(brokerstore.Request{Jail: "j", Start: "s", Workspace: "/code/app", Argv: []string{"pr", "merge", n}, Repo: "o/r", StdinDigest: "stdin", Set: "read-write", Policy: brokerstore.Policy{Window: 15 * time.Minute, MaxWindow: time.Hour, Uses: 25}})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	hooked, other, timed := file("1"), file("2"), file("3")
	decide := func(env string, args ...string) {
		t.Helper()
		t.Setenv("YOLO_REQUEST_ID", env)
		var out, errs bytes.Buffer
		if rc := approveMain(context.Background(), args, false, s, &out, &errs); rc != 0 {
			t.Fatalf("approve %v: rc %d, err %q", args, rc, errs.String())
		}
	}
	decide(hooked.ID, hooked.ID)
	decide(hooked.ID, other.ID, "--deny")
	decide("", timed.ID, "--for", "15m")
	rs, _, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string][2]string{
		hooked.ID: {brokerstore.AnswerOnce, brokerstore.ViaOnRequest},
		other.ID:  {brokerstore.AnswerDeny, brokerstore.ViaHost},
		timed.ID:  {brokerstore.AnswerFor, brokerstore.ViaHost},
	}
	for _, q := range rs {
		if got := [2]string{q.Answer, q.Via}; got != want[q.ID] {
			t.Errorf("%s recorded %v, want %v", q.ID, got, want[q.ID])
		}
	}
}

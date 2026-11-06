package callbroker

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func TestSelectedCLIExactOnceExpiresOrIsRevokedWithoutHostEffect(t *testing.T) {
	for _, name := range []string{"expired", "revoked"} {
		t.Run(name, func(t *testing.T) {
			f := selectedOnceFixture(t)
			if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
				t.Fatalf("ask: %+v %v", v, err)
			}
			grant, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{})
			if err != nil {
				t.Fatal(err)
			}
			if name == "expired" {
				f.store.Now = func() time.Time { return grant.Expires.Add(time.Second) }
			} else {
				if err := f.store.Revoke(grant.ID); err != nil {
					t.Fatal(err)
				}
			}
			v, _, err := f.call(t, "act", "update", "a1")
			if name == "expired" {
				if _, ok := brokerstore.IsLapsedAllow(err); !ok || v.Verdict != "refused" || !strings.Contains(v.Next, "retry the same call") || f.effects(t) != 0 {
					t.Fatalf("expired once was not a named refusal: %+v %v", v, err)
				}
			} else if err != nil || v.Verdict != "asks" || f.effects(t) != 0 {
				t.Fatalf("revoked grant ran: %+v %v effects=%d", v, err, f.effects(t))
			}
		})
	}
}

func TestSelectedCLIReadAndEvaluateRefuseCurrentHostReadWithdrawal(t *testing.T) {
	f := selectedOnceFixture(t)
	before := mustRead(t, f.userPath)
	if err := os.WriteFile(f.userPath, []byte(strings.Replace(string(before), `on_request = ["read"]`, `on_request = []`, 1)), 0600); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"act", "evaluate", "ask"} {
		argv := []string{"update", "a1"}
		if op == "act" {
			argv = []string{"read"}
		}
		v, _, err := f.call(t, op, argv...)
		if err == nil || v.Verdict != "refused" || f.effects(t) != 0 {
			t.Fatalf("%s accepted withdrawn host read ceiling: %+v %v", op, v, err)
		}
	}
}

// A revalidation refusal names the classifier's own failure, not only the
// generic sentence, so a timeout is distinguishable from a changed answer.
func TestSelectedCLIRevalidationNamesTheClassifierError(t *testing.T) {
	f := selectedOnceFixture(t)
	if v, _, err := f.call(t, "ask", "update", "a1"); err != nil || v.Verdict != "asks" {
		t.Fatalf("ask: %+v %v", v, err)
	}
	f.h.classifier.deadline = time.Nanosecond
	_, err := f.h.revalidate(f.pending(t)[0])
	if err == nil || !strings.Contains(err.Error(), "current classifier refused this call") || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("revalidation hid the classifier's error: %v", err)
	}
}

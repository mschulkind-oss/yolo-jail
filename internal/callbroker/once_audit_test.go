package callbroker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokeraudit"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func TestSelectedCLIWriteAuditContainsNoRawArgumentsAndFailureCannotDispatch(t *testing.T) {
	f := selectedOnceFixture(t)
	token := "fixture-secret-argv"
	v, _, err := f.call(t, "ask", "update", token)
	if err != nil || v.Verdict != "asks" {
		t.Fatalf("ask: %+v %v", v, err)
	}
	if _, err := f.store.Decide(f.pending(t)[0].ID, "pending", brokerstore.Decision{}); err != nil {
		t.Fatal(err)
	}
	audit := mustRead(t, filepath.Join(f.home, "audit.jsonl"))
	if strings.Contains(string(audit), token) || strings.Contains(string(audit), f.home) || strings.Contains(string(audit), "team:alpha") {
		t.Fatal("pack authorization audit exposed original argv or host path")
	}
	// The request/decision evidence succeeded. Force the mandatory grant-used
	// audit to fail before the CLI can be dispatched, without touching user state.
	bad := filepath.Join(f.home, "audit-blocked")
	if err := os.Mkdir(bad, 0700); err != nil {
		t.Fatal(err)
	}
	log := brokeraudit.Open(bad, nil)
	f.h.audit = log
	f.store.Audit = StoreAudit(log, f.h.input.Source)
	v, _, err = f.call(t, "act", "update", token)
	if err == nil || v.Verdict != "refused" || f.effects(t) != 0 {
		t.Fatalf("audit failure dispatched: %+v %v effects=%d", v, err, f.effects(t))
	}
}

func TestSelectedCLIApprovalAdapterRejectsOldNILCallWritesWithoutGrant(t *testing.T) {
	f := selectedOnceFixture(t)
	v, _, err := f.call(t, "ask", "update", "a1")
	if err != nil || v.Verdict != "asks" {
		t.Fatalf("ask %+v %v", v, err)
	}
	q := f.pending(t)[0]
	q.CallWrites = nil
	q.Digest = brokerstore.CommandDigest(q)
	if _, err := ApprovalAuthority(q); err == nil {
		t.Fatal("legacy or missing write coverage accepted by pack adapter")
	}
	if f.effects(t) != 0 {
		t.Fatal("legacy request caused a host write")
	}
	// Preserve the stored parent exactly as it was; only the copied candidate
	// above is malformed.
	if len(f.pending(t)[0].CallWrites) == 0 {
		t.Fatal("test mutated the original request")
	}
}

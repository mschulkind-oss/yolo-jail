package ghbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// Call-site pin: a scoped or context-bound request must be refused by the legacy
// revalidation itself. Deleting the guard in Authority.Revalidate fails this.
func TestRevalidateRefusesScopedAndContextRequests(t *testing.T) {
	a, base := authorityFixture(t)
	for name, mutate := range map[string]func(*brokerstore.Request){
		"context name":   func(q *brokerstore.Request) { q.ContextName = "other" },
		"context digest": func(q *brokerstore.Request) { q.ContextDigest = strings.Repeat("ab", 32) },
		"scoped role": func(q *brokerstore.Request) {
			q.CallWrites = []brokerstore.CallWrite{{Cell: "c", Role: brokerstore.RoleWithheldWrite}}
		},
	} {
		q := base
		mutate(&q)
		if _, err := a.Revalidate(q); err == nil || !strings.Contains(err.Error(), "scoped operation approval is not active") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

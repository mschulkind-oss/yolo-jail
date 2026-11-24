package gitbroker

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

// Call-site pin: revalidatePush refuses a scoped or context-bound request before any
// holder check. Deleting the guard fails this, because the message changes.
func TestRevalidatePushRefusesScopedAndContextRequests(t *testing.T) {
	for name, q := range map[string]brokerstore.Request{
		"context name":   {ContextName: "other"},
		"context digest": {ContextDigest: strings.Repeat("ab", 32)},
		"scoped role":    {CallWrites: []brokerstore.CallWrite{{Cell: "c", Role: brokerstore.RoleWithheldWrite}}},
	} {
		if _, err := revalidatePush("", brokerscope.File{}, "", q); err == nil || !strings.Contains(err.Error(), "scoped operation approval is not active") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

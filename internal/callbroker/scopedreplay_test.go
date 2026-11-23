package callbroker

import (
	"net/http"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
)

func scopedReplayShapes() map[string]brokerstore.Request {
	return map[string]brokerstore.Request{
		"context name":   {ContextName: "other"},
		"context digest": {ContextDigest: strings.Repeat("ab", 32)},
		"scoped role":    {CallWrites: []brokerstore.CallWrite{{Cell: "c", Role: brokerstore.RoleWithheldWrite}}},
	}
}

// Call-site pins: the legacy revalidation of the CLI and HTTP paths refuses a scoped or
// context-bound request itself. Deleting either guard fails these, because without it the
// request is judged by the later holder checks and the message changes.
func TestCLIRevalidateRefusesScopedAndContextRequests(t *testing.T) {
	f := selectedOnceFixtureWithStanding(t, true)
	for name, q := range scopedReplayShapes() {
		if _, err := f.h.revalidate(q); err == nil || !strings.Contains(err.Error(), "scoped operation execution is not active") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

func TestHTTPRevalidateRefusesScopedAndContextRequests(t *testing.T) {
	_, executor, origin := fixturePinnedHTTPServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	h, _ := selectedHTTPWriteFixture(t, origin, executor, []byte(`{}`))
	for name, q := range scopedReplayShapes() {
		if _, err := h.revalidateHTTP(q); err == nil || !strings.Contains(err.Error(), "scoped HTTP operation execution is not active") {
			t.Fatalf("%s: err=%v", name, err)
		}
	}
}

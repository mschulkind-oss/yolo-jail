package run

import (
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/mcpsignin"
)

func TestManagedMCPCallbackBindConflictRequiresSameLoopbackEndpointLine(t *testing.T) {
	leases := []mcpsignin.CallbackLease{{Port: 3118}, {Port: 3119}}
	for _, tc := range []struct {
		text string
		want []int
	}{
		{"rootlessport listen tcp 127.0.0.1:3118: bind: address already in use", []int{3118}},
		{"Bind for 127.0.0.1:3119 failed: port is already allocated", []int{3119}},
		{"run -p 127.0.0.1:3118:3118/tcp\nError: listen tcp 127.0.0.1:8999: bind: address already in use", nil},
		{"127.0.0.1:3118 image unavailable", nil},
		{"listen tcp 127.0.0.1:31180: bind: address already in use", nil},
		{"listen tcp 0.0.0.0:3118: bind: address already in use", nil},
	} {
		if got := callbackBindConflicts(tc.text, leases); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("wrong classification for %q: %v", tc.text, got)
		}
	}
}

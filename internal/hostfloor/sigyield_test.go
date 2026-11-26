package hostfloor

import (
	"syscall"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/sigyield"
)

// TestTheRefreshHoldsTheSignalsItRecords: a refresh records SIGINT, SIGTERM and SIGHUP for the launch
// to decide on (signalsDuring), so it holds them from the front door's terminal arm, which would
// otherwise end the process on the same Ctrl-C (internal/sigyield), and lets go once it returns.
func TestTheRefreshHoldsTheSignalsItRecords(t *testing.T) {
	sigs := []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP}
	signalsDuring(func() {
		for _, s := range sigs {
			if !sigyield.Held(s) {
				t.Errorf("%v is not held while the refresh runs", s)
			}
		}
	})
	for _, s := range sigs {
		if sigyield.Held(s) {
			t.Errorf("%v is still held after the refresh", s)
		}
	}
}

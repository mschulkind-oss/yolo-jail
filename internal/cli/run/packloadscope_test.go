package run

import (
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// strictPackloadReads brackets a host-side test around the process-wide decoder switch that
// the in-jail entrypoint sets. Force strict reads at the test boundary and leave the run-package
// test process strict afterward, even when another test ran an entrypoint generator first.
func strictPackloadReads(t *testing.T) {
	t.Helper()
	packload.OverrideSkewTolerance(false)
	t.Cleanup(func() { packload.OverrideSkewTolerance(false) })
}

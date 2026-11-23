package perf

import (
	"fmt"
	"os"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/testtmp"
)

// The two variables that turn this test binary into the child process of
// TestTraceContextReachesAChildProcessThroughItsEnvironment: the trace root the child writes its
// lane under, and the context it is handed, in its string form. They are test-only: which variable
// carries a context in production is the host wiring's to name (context.go).
const (
	traceHelperRootEnv = "PERF_TEST_TRACE_HELPER_ROOT"
	traceHelperCtxEnv  = "PERF_TEST_TRACE_HELPER_CONTEXT"
)

func TestMain(m *testing.M) {
	if root := os.Getenv(traceHelperRootEnv); root != "" {
		os.Exit(runTraceHelper(root))
	}
	release := testtmp.Isolate()
	os.Exit(release(m.Run()))
}

// runTraceHelper is the child: it joins the trace its environment hands it, the way an in-jail
// daemon or a host service does, and records one span in its own lane.
func runTraceHelper(root string) int {
	ctx, ok := ParseContext(os.Getenv(traceHelperCtxEnv))
	if !ok {
		fmt.Fprintf(os.Stderr, "helper: no usable context in %s\n", traceHelperCtxEnv)
		return 3
	}
	tr := ctx.Join()
	l := NewTraced(nil, tr, TraceSink(root, "helper", tr, os.Stderr))
	l.Span("helper.work").End()
	return 0
}

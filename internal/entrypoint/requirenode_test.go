package entrypoint

import (
	"os/exec"
	"testing"
)

// requireNode returns node's path, or SKIPS the test, naming what did not run, when node is not
// on PATH. A skip and not a failure, for internal/footer's reason (its requireNode): node is a
// runtime of the pi extensions yolo ships, not of yolo's own build, so a from-source
// `go test -short ./...` on a machine without it must not go red over an absent interpreter.
// CI's short-suite jobs assert node is on PATH before the suite (.github/workflows/ci.yml, the
// "Node is on PATH" step), which is what keeps the skip from hiding a regression there.
//
// Call it where the test is about to run node, after the static assertions that need none.
func requireNode(t *testing.T, what string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is not on PATH (%v), so %s did not run; install node to run "+
			"this test — CI's short-suite jobs have it", err, what)
	}
	return node
}

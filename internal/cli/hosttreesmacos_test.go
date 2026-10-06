package cli

// hosttreesmacos_test.go pins PPX-D38 (docs/design/patched-extensions.md) at the host notch: on a
// host that builds no tree (macOS), a tree whose list entry is in a guarded posture list reaches no
// notch that has it, so neither the render's refusal nor a host launch's line sends the user to a
// jail, which would not load it either; both name moving the entry instead.

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestAMacOSHostNamesAStepThatWorksForAGuardedOnlyTree(t *testing.T) {
	fx := newTreeFixture(t, `"f.txt"`)
	fx.listTreeForAgentWith(t, treePosture("guarded"))
	fx.writeHostConfig(t, "")
	prev := hostTreesBuild
	hostTreesBuild = func() bool { return false }
	t.Cleanup(func() { hostTreesBuild = prev })
	stubBins(t, "tool")
	if out := fx.renderTrees(t, true); !strings.Contains(out, "this host builds no tree") ||
		strings.Contains(out, "YOLO_RUNTIME=podman") || !strings.Contains(out, packload.GuardedOnlyStep) {
		t.Errorf("the macOS render of a guarded-only tree:\n%s", out)
	}
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"tool"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d, execed %v\n%s", rc, got.execed, errw.String())
	}
	if s := errw.String(); !strings.Contains(s, "is not delivered on this host") || strings.Contains(s, "YOLO_RUNTIME=podman") ||
		!strings.Contains(s, packload.GuardedOnlyStep) {
		t.Errorf("the macOS launch's line for a guarded-only tree:\n%s", s)
	}
}

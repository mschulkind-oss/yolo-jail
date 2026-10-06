package run

// treenotch.go is a jail launch's half of PPX-D35 (docs/design/patched-extensions.md): a patched
// extension whose list entry reaches no jail — the owning agent pack's entry naming it is in a
// guarded posture list, which reaches the host alone — is built, copied and mounted in no jail. The
// launch's block still names it (patchedTreeLine), and the tree arm, the mount, YOLO_PATCHED_TREES,
// the delivery record and the lines of a notch that builds none all read the trees this returns, so
// none of them sees it.

import (
	goruntime "runtime"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// jailDeliveredTrees is trees without those a jail is not delivered (packload.Fork.DeliveredInJail),
// in their order.
func jailDeliveredTrees(trees []packload.Fork) []packload.Fork {
	var out []packload.Fork
	for _, f := range trees {
		if f.DeliveredInJail() {
			out = append(out, f)
		}
	}
	return out
}

// hostBuildsOwnTrees reports whether this host's own render builds patched extensions, as the host
// apply's does (a Linux host): on a macOS host a tree listed for the host alone is delivered nowhere,
// and the block says so (PPX-D38). A var so a test can be either host.
var hostBuildsOwnTrees = func() bool { return goruntime.GOOS == "linux" }

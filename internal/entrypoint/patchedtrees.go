package entrypoint

// patchedtrees.go is the jail's half of a PATCHED EXTENSION's delivery
// (docs/design/patched-extensions.md §8.1, §9; PPX-D8, PPX-D18): what the host handed this jail per
// extension key, read once at boot from YOLO_PATCHED_TREES, and the GATE it puts on the owning
// agent pack's launchers.
//
// THE HOST DECIDES, the jail obeys, as for a fork (forklauncher.go): which build serves is a
// question about the machine's check record and capture store, which no jail reads. The host
// mounts the build's per-launch copy read-only at `~/<into>` and says, per key, what it handed or
// why there is none, and whether the owning agent's launchers stop (PPX-D18: the good build
// serves; with none, the owning agent pack's launchers stop before exec and say why, at a notch
// that builds trees and that the agent's list entry reaches). The jail launch itself is never
// refused, and the shell stays up.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// PatchedTreesEnv names the host's per-extension decisions in the jail environment: a JSON
// object, extension key → TreeDelivery. A sibling of ForkBuildsEnv, not an overload of it: that
// one is keyed by bin, and an extension has none. A host↔jail contract in YOLO_PACK_ROOT's class.
const PatchedTreesEnv = "YOLO_PATCHED_TREES"

// TreeBuildEnv names, in a PATCHED EXTENSION's sealed build jail alone, the extension the jail builds
// (docs/design/patched-extensions.md §7.1, PPX-D30). That jail's seal selects the contributing pack
// and no other (PPX-D5), so the pack's own list entry naming `~/<into>` has no owner there by
// construction, and the boot names no orphaned overlay or list (reportOverlayResolution) rather than
// tell the user to check an identity that is correct. Additive: an entrypoint that predates it names
// the orphan, as before.
const TreeBuildEnv = "YOLO_TREE_BUILD"

// TreeDelivery is the host's answer for one patched extension this launch.
type TreeDelivery struct {
	// Into is the home-relative directory the tree is mounted at.
	Into string `json:"into"`
	// Build is the capture store entry the mounted copy was made from, "" when none was mounted.
	Build string `json:"build,omitempty"`
	// Label names the handed build as lines do ("v0.75.0 (6f1027f7) + 7 patches"), "" with none.
	Label string `json:"label,omitempty"`
	// Reason is why none was mounted, naming what to do; "" when Build is set.
	Reason string `json:"reason,omitempty"`
	// Owner is the owning agent pack (PPX-D4), "" when no list entry names the tree.
	Owner string `json:"owner,omitempty"`
	// Stop says the owner's launchers stop before exec (PPX-D18): nothing serves, at a notch that
	// builds trees and that the owner's list entry reaches.
	Stop bool `json:"stop,omitempty"`
}

// PatchedTreesWire renders the decisions for the environment, "" for none.
func PatchedTreesWire(d map[string]TreeDelivery) string {
	if len(d) == 0 {
		return ""
	}
	b, err := json.Marshal(d)
	if err != nil {
		return ""
	}
	return string(b)
}

// patchedTrees reads the host's decisions from the environment. An unreadable value decides
// nothing, and says so: no launcher is gated on a value this build cannot read.
func patchedTrees(e *Env) map[string]TreeDelivery {
	raw := e.Getenv(PatchedTreesEnv)
	if raw == "" {
		return nil
	}
	var d map[string]TreeDelivery
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		e.warn(fmt.Sprintf("yolo-entrypoint: %s is not a patched-extension delivery this build reads (%v) — "+
			"no launcher is stopped for one", PatchedTreesEnv, err))
		return nil
	}
	return d
}

// treeGateFor is the gate the launchers of pack carry: one line per patched extension that pack
// owns which the host says stops them, in key order, "" when nothing does.
func treeGateFor(d map[string]TreeDelivery, pack string) string {
	keys := make([]string, 0, len(d))
	for k, t := range d {
		if t.Stop && t.Owner == pack && pack != "" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var lines []string
	for _, k := range keys {
		t := d[k]
		why := t.Reason
		if why == "" {
			why = "the host handed this jail no build of it"
		}
		lines = append(lines, "  ⚠ extension "+k+" (~/"+strings.TrimSuffix(t.Into, "/")+"), which pack "+pack+
			" loads, has no build in this jail: "+why)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n  So this program does not start rather than start without it (the shell " +
		"is unaffected); once a fresh launch on the host has built it, launch again — or drop the list " +
		"entry naming it to run without it."
}

// treeGateShell is the gate every agent launcher carries, immediately before its exec: the gate's
// lines, baked by the generator from Install.Gate, and a stop when there are any.
const treeGateShell = `# --- a patched extension this agent loads, with no build (patched-extensions.md PPX-D18) ---
TREE_GATE=__YOLO_TREE_GATE__
if [ -n "$TREE_GATE" ]; then
    printf '%s\n' "$TREE_GATE" >&2
    exit 1
fi
`

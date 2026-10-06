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
// that builds trees and that the agent's list entry reaches), and the shell stays up. A fresh
// launch with such a tree refuses before booting (PPX-D40), so this gate is the backstop for an
// attach, a nested launch and a launch with YOLO_ALLOW_MISSING_PROGRAMS set.

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	// Cause is the build's cause in plain words, when its act found one (BuildCause), which the gate
	// says once for every extension that shares it; nil leaves Reason to say it.
	Cause *BuildCause `json:"cause,omitempty"`
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
		e.warnOnce(fmt.Sprintf("yolo-entrypoint: %s is not a patched-extension delivery this build reads (%v) — "+
			"no launcher is stopped for one", PatchedTreesEnv, err))
		return nil
	}
	return d
}

// withTreeFallbacks is packs with every UNMODIFIED EXTENSION's fallback taken that the host handed
// this jail no tree for (packload.ApplyTreeFallbacks; docs/design/pi-extension-store-builds.md XB-D7):
// a key the wire names with no build, a key it does not name, or every key when there is no wire at
// all — a notch that builds no tree (macos-user), or a launcher older than the wire (XB-D8: "an absent
// wire means nothing was handed"). The surfaces' list contributions are collected from what it
// returns, so the agent installs each such extension itself from its raw entry. The host's launch
// says each one, with its reason, where it decided it.
func withTreeFallbacks(e *Env, packs []*packload.Pack) []*packload.Pack {
	wire := patchedTrees(e)
	out, _ := packload.ApplyTreeFallbacks(packs, func(f packload.Fork) bool { return wire[f.Key()].Build != "" })
	return out
}

// treeGateFor is the gate the launchers of pack carry: "" when no patched extension that pack owns
// stops them, else ONE SHORT LINE PER EXTENSION, in key order, then why ONCE — the host's reason and
// its cause in plain words when every one shares a cause (BuildCause), as a launch that met one
// refusal hands each extension it left without a build — and the next step once. An extension
// whose why is its own says it on its line. The maintainer's first launch with patched extensions
// printed the whole relayed refusal on each extension's line, four times (PPX-D42).
func treeGateFor(d map[string]TreeDelivery, pack string) string {
	keys := make([]string, 0, len(d))
	for k, t := range d {
		if t.Stop && t.Owner == pack && pack != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)
	// One cause for all is said once, below them; otherwise each line says its own why.
	shared := d[keys[0]].Cause
	for _, k := range keys[1:] {
		if !d[k].Cause.Same(shared) {
			shared = nil
			break
		}
	}
	var lines []string
	// With no cause every extension shares, each cause is said under the first extension it left
	// without a build, and a later one it left names that one, so a cause is still said, and once.
	var said []string
	for _, k := range keys {
		t := d[k]
		line := "  ⚠ extension " + k + " (~/" + strings.TrimSuffix(t.Into, "/") + ") has no build in this jail"
		if shared != nil {
			lines = append(lines, line)
			continue
		}
		why := treeGateWhy(t)
		if !t.Cause.says() {
			lines = append(lines, line+": "+why)
			continue
		}
		if first := firstSaidWith(d, said, t.Cause); first != "" {
			head, _, _ := strings.Cut(why, " — ")
			lines = append(lines, line+": "+strings.TrimSuffix(head, ".")+", as extension "+first+"'s did")
			continue
		}
		said = append(said, k)
		c := causeSaid(why, t.Cause, "    ")
		lines = append(lines, line+": "+c[0])
		lines = append(lines, c[1:]...)
	}
	them, one := "them", "one"
	if len(keys) == 1 {
		them, one = "it", "it"
	}
	if shared != nil {
		// The reason's own next step ("— the next fresh launch tries again") is the gate's last line.
		why, _, _ := strings.Cut(treeGateWhy(d[keys[0]]), " — ")
		if len(keys) > 1 {
			why = strings.Replace(why, "its build jail", "their build jail", 1)
		}
		lines = append(lines, "    Why: "+strings.TrimSuffix(why, ".")+colonAfter(len(shared.Lines) > 0))
		for _, l := range shared.Lines {
			lines = append(lines, "      "+l)
		}
		if shared.YoloBug {
			lines = append(lines, "    This is a bug in yolo, not in the pack: report it at "+IssuesURL+".")
		}
	}
	return strings.Join(lines, "\n") + "\n  So pack " + pack + "'s program does not start rather than start without " +
		them + " (the shell is unaffected); once a fresh launch on the host has built " + them + ", launch " +
		"again — or drop the list entry naming " + one + " to run without it."
}

// firstSaidWith is the extension among said, the keys whose causes the gate has said, whose cause is
// c; "" when none is.
func firstSaidWith(d map[string]TreeDelivery, said []string, c *BuildCause) string {
	for _, k := range said {
		if d[k].Cause.Same(c) {
			return k
		}
	}
	return ""
}

// causeSaid is a why and its cause as the jail's lines say them: the why without the act that
// tries again (" — …", which the gate's own last line or the launcher's says), a colon when lines
// follow, then each of the cause's lines indented under it, then whose bug it is when it is
// yolo's. why alone when there is no cause to say (BuildCause.says).
func causeSaid(why string, c *BuildCause, indent string) []string {
	if !c.says() {
		return []string{why}
	}
	head, _, _ := strings.Cut(why, " — ")
	out := []string{strings.TrimSuffix(head, ".") + colonAfter(len(c.Lines) > 0)}
	for _, l := range c.Lines {
		out = append(out, indent+"  "+l)
	}
	if c.YoloBug {
		out = append(out, indent+"This is a bug in yolo, not in the pack: report it at "+IssuesURL+".")
	}
	return out
}

// colonAfter is ":" when lines follow.
func colonAfter(more bool) string {
	if more {
		return ":"
	}
	return ""
}

// treeGateWhy is one extension's why, the host's reason, or that the host handed nothing.
func treeGateWhy(t TreeDelivery) string {
	if t.Reason != "" {
		return t.Reason
	}
	return "the host handed this jail no build of it"
}

// treeGateShell is the gate every agent launcher carries FIRST, right after its update mode's exit
// and before any install, update or refresh (docs/design/pi-extension-store-builds.md XB-D25,
// amending PPX-D24's "immediately before the exec"), so a launch the gate stops pays for nothing:
// the gate's lines, baked by the generator from Install.Gate, and a stop when there are any. A
// version probe (XB-D24) is not stopped: it loads no extension. Nor is install-only mode
// (InstallOnlyEnv: the readiness act, `yolo capture`), which installs and runs no program, and which
// the gate's move to the top would otherwise have put behind it.
const treeGateShell = `# --- a patched extension this agent loads, with no build (patched-extensions.md PPX-D18) ---
# Checked FIRST (pi-extension-store-builds.md XB-D25): a launch this stops pays for no install,
# update or refresh.
TREE_GATE=__YOLO_TREE_GATE__
[ "${_YOLO_PROBE:-}" != "1" ] || TREE_GATE=""
[ "${` + InstallOnlyEnv + `:-}" != "1" ] || TREE_GATE=""
if [ -n "$TREE_GATE" ]; then
    printf '%s\n' "$TREE_GATE" >&2
    exit 1
fi
`

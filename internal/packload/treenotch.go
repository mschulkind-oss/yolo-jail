package packload

// treenotch.go says WHERE a patched extension's tree is delivered, and which list entries count as
// loading it (docs/design/patched-extensions.md PPX-D35, PPX-D36).
//
// A TREE IS BUILT AND MOUNTED ONLY AT A NOTCH ITS LIST ENTRY REACHES (PPX-D35). The owning agent
// pack's entry naming the tree says where the agent loads it: a `config-list` at every notch, an
// autonomous posture list in every jail, a guarded one at the host alone (Fork.ListedInJail,
// ListedAtHost). A launch builds, copies and mounts a tree nowhere else, and the host's render
// links it nowhere else, since nothing there would load it. A tree with NO owning agent pack says
// nothing about where it loads, so it is delivered at every notch, as before, and the lint says no
// agent loads it (LintPatchedTrees): the mechanism delivers any home-relative tree, and a tree no
// list names may be read by something that keeps no list.
//
// AN ENTRY UNDER `~/<into>/` LOADS THE TREE (PPX-D36): `~/<into>` itself, the same with a trailing
// slash, and any path inside it, such as one package of a monorepo's tree,
// `~/<into>/packages/session-name`. The entry is compared as a cleaned path, so `~/<into>/../x`
// names no part of the tree; core still reads none of an agent's package grammar beyond that.

import (
	"path"
	"strings"
)

// DeliveredInJail reports whether a jail launch builds, copies and mounts f (PPX-D35): its owning
// agent pack's list entry reaches a jail, or it has no owning agent pack. Always true for a fork of
// a program, which this rule does not reach.
func (f Fork) DeliveredInJail() bool { return !f.IsTree() || f.Owner == "" || f.ListedInJail }

// DeliveredAtHost reports whether the host's render builds and links f (PPX-D35): its owning agent
// pack's list entry reaches the host, or it has no owning agent pack. Always true for a fork of a
// program.
func (f Fork) DeliveredAtHost() bool { return !f.IsTree() || f.Owner == "" || f.ListedAtHost }

// NotDeliveredInJailNote is what a jail launch says of a tree it does not deliver
// (!DeliveredInJail): why, and where it is delivered instead. An owner's entry that reaches no jail
// is in a guarded posture list, since a `config-list` and an autonomous list both reach one.
const NotDeliveredInJailNote = "not built or mounted in a jail: the list entry that loads it is in a guarded " +
	"posture list, which reaches the host alone — `yolo host apply --assert` installs it there"

// loadsTree reports whether the list entry entry loads the tree whose own entry is want
// (TreeListEntry): entry, cleaned as a path, is want or lies inside it (PPX-D36).
func loadsTree(entry, want string) bool {
	if !strings.HasPrefix(entry, "~/") {
		return false
	}
	clean, want := path.Clean(entry), path.Clean(want)
	return clean == want || strings.HasPrefix(clean, want+"/")
}

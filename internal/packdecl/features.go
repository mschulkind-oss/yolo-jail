package packdecl

// features.go is the list `yolo features` prints: what this build of yolo can read in a pack, so
// a pack author or a script can ask a host whether a pack will work there before selecting it
// (docs/design/patched-forks.md PF-D71). It is a property of the build, not of the machine: a
// capability a backend does not support yet is still listed, and the launch on that backend says
// where it works instead.

import (
	"sort"
	"strings"
)

// Feature is one capability this build reads in a pack: a stable kebab-case NAME a script tests
// for, and one line saying what a pack uses it for.
type Feature struct {
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// namedFeatures are the capabilities that are neither a contribution kind nor a program `via`: a
// field or a mode of an existing kind, or a way this build reads a pack.
//
// THE RULE FOR ADDING ONE (PF-D71): a capability gets a name here when a pack can use it and a yolo
// without it would refuse or skip that use, and it is not already listed as a kind or a via, which
// are derived from their closed sets and never written here. The entry lands in the same commit as
// the capability, with a case in TestEveryNamedFeatureIsReadable proving this build reads it.
// Nothing older is backfilled: every build that has `yolo features` reads everything shipped
// before the command, so a yolo without the command is the answer "older than all of these".
var namedFeatures = []Feature{
	{Name: "patch-series", Summary: "`patches` (and `follow`) on a `program` with `via: \"source\"`: " +
		"a patched fork that follows its upstream while the series applies"},
	{Name: "patched-extensions", Summary: "`patches` on a `files` contribution: a patched pi " +
		"extension, built on the host and mounted read-only at `into`"},
	{Name: "skips-unreadable-contributions", Summary: "a launch skips, and names, a contribution " +
		"holding a kind, via or field this yolo does not know, instead of refusing the pack"},
}

// Features is every capability this build reads in a pack, sorted by name: the named ones, then
// `kind:<kind>` for each contribution kind in the closed set and `via:<via>` for each program
// delivery, both derived so a kind or a via added to its set is listed with no second edit.
func Features() []Feature {
	out := append([]Feature(nil), namedFeatures...)
	for _, k := range KnownKinds() {
		fp, _ := FootprintOf(k)
		out = append(out, Feature{Name: "kind:" + string(k), Summary: "the `" + string(k) +
			"` contribution kind: " + fp.Claims})
	}
	for _, v := range KnownVias() {
		out = append(out, Feature{Name: "via:" + v, Summary: "a `program` delivered `via: \"" + v + "\"`"})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.Compare(out[i].Name, out[j].Name) < 0 })
	return out
}

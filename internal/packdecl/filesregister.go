package packdecl

// filesregister.go is a `files` SLOT's two optional fields: `register`, which asks core to name
// every tree landing in the slot in a list inside a surface the slot's own pack declares, and
// `expects`, the top-level names a well-formed tree holds (docs/design/pack-pi-resources.md §3.1).
//
// pi is why they exist, and core never learns it. pi's `packages` setting loads a local directory
// laid out as a pi package — extensions/, themes/, prompts/ — with no list of its files, while its
// extensions folder loads a subdirectory only through a manifest naming every file
// (pack-pi-resources.md §1). So packs/pi declares a slot outside every folder pi scans by itself,
// with `register` naming its settings surface and `/packages`, and a content pack reaches pi with
// one entry, `{"kind": "files", "agents": ["pi"], "from": "<folder>"}`, and no pi path. Core knows
// a slot, a surface, a pointer and a template, and nothing about pi.
//
// The entry core appends is an ordinary config-list entry, attributed to the CONTRIBUTING pack
// (packoverlay.Collect places it; packload.Registrations computes it): it folds beside the user's
// own entries, is captured per entry in a jail, is recorded as inserted at the host, and leaves
// when its pack is dropped, by config-list's rules and no new ones (pack-pi-resources.md PR-D2).

import (
	"fmt"
	"regexp"
	"strings"
)

// TokenLanding is the one token a registration's `entry` may hold. It is replaced by the tree's
// landing, home-relative: `<slot>/<contributing pack>` (packload.SlotLanding).
const TokenLanding = "{landing}"

// DefaultRegisterEntry is what a registration appends when it names no `entry`: the landing under
// `~`. Home-relative rather than absolute because the jail's home and the host's are different
// paths, and the one settings file must name the tree at both (pack-pi-resources.md PR-D3). It is
// the default so the common slot spells only where the list is.
const DefaultRegisterEntry = "~/" + TokenLanding

// FilesRegister is a slot's `register`: where each landed tree is listed.
type FilesRegister struct {
	// Surface is the target surface identity, "agent/name". It must be one the SLOT'S OWN PACK
	// declares, which packoverlay.Collect checks, since only it sees a pack's surfaces: a slot
	// writing into another pack's settings would make the slot owner's delivery depend on a
	// pack it cannot name.
	Surface string `json:"surface"`
	// Path is the RFC 6901 pointer of the array inside the surface, with config-list's rules.
	Path string `json:"path"`
	// Entry is the string appended for each landed tree, holding TokenLanding exactly where the
	// landing goes and no other token. Absent means DefaultRegisterEntry.
	Entry string `json:"entry,omitempty"`
}

// EntryFor is the entry this registration appends for one tree landing at `landing`.
func (r FilesRegister) EntryFor(landing string) string {
	tmpl := r.Entry
	if tmpl == "" {
		tmpl = DefaultRegisterEntry
	}
	return strings.ReplaceAll(tmpl, TokenLanding, landing)
}

// registerToken finds every brace token in an entry template, so an unknown one is refused by name
// rather than written into an agent's settings verbatim.
var registerToken = regexp.MustCompile(`\{[^{}]*\}`)

// filesSlotProblems checks `register` and `expects`: refused on anything but a `files` slot, and
// each well-formed where it is allowed.
func filesSlotProblems(label string, c Contribution) []string {
	var problems []string
	slot := c.Kind == KindFiles && c.Agent != "" && !c.IsPatchedExtension()
	for _, f := range []struct {
		name string
		set  bool
	}{{"register", c.Register != nil}, {"expects", len(c.Expects) > 0}} {
		if f.set && !slot {
			problems = append(problems, fmt.Sprintf(
				"%s: %q is a files SLOT's field ({\"kind\":\"files\",\"agent\":\"<agent>\","+
					"\"into\":\"<dir>\"}) — it describes the trees other packs land there, so "+
					"a contribution that is not a slot has nothing for it to describe", label, f.name))
		}
	}
	if !slot {
		return problems
	}
	if r := c.Register; r != nil {
		for _, f := range []struct{ name, val string }{{"surface", r.Surface}, {"path", r.Path}} {
			if f.val == "" {
				problems = append(problems, fmt.Sprintf("%s.register: needs %q — the surface and "+
					"the array pointer are where each landed tree is listed", label, f.name))
			}
		}
		if r.Path != "" {
			problems = append(problems, configListPathProblems(label+".register.path", r.Path)...)
		}
		if r.Entry != "" {
			if !strings.Contains(r.Entry, TokenLanding) {
				problems = append(problems, fmt.Sprintf("%s.register.entry %q: names no %s, so "+
					"every tree would be listed as the same string — write %s where the tree's "+
					"home-relative path goes, or drop \"entry\" for the default %q",
					label, r.Entry, TokenLanding, TokenLanding, DefaultRegisterEntry))
			}
			for _, tok := range registerToken.FindAllString(r.Entry, -1) {
				if tok != TokenLanding {
					problems = append(problems, fmt.Sprintf("%s.register.entry %q: unknown token "+
						"%s — %s is the only one, and an unknown token would reach the agent's "+
						"settings verbatim", label, r.Entry, tok, TokenLanding))
				}
			}
		}
	}
	for i, name := range c.Expects {
		if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
			problems = append(problems, fmt.Sprintf("%s.expects[%d] %q: must be a bare top-level "+
				"name — it is compared against the entries directly inside a landed tree, so a "+
				"path could never match", label, i, name))
		}
	}
	return problems
}

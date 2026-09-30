package packdecl

// modelmenu.go is the `model_menu` field: how the launcher writes a PROGRAM's model menu from the
// program's own catalog before exec'ing it (docs/design/model-lists-and-pickers.md MM-D9, MM-D22).
// The type carries the grammar, modelMenuProblems the refusals, and internal/modelmenu the
// projection the launcher runs.

import (
	"fmt"
	"path"
	"strings"
)

// ModelMenuInto is the placeholder a ModelMenu.Flag word spells for the absolute path of the menu
// file the launcher wrote.
const ModelMenuInto = "{into}"

// ModelMenu declares a program's MODEL MENU — a term coined here (2026-09-30) for a menu file the
// generated launcher writes, at every launch and before the exec, from two inputs: the program's
// OWN catalog, which the program prints when run with Catalog, and yolo's list for the selected
// provider, which a derive renders to List. The file keeps the catalog's entries whose id the
// list names, in the list's order, and the launcher hands it to the program with Flag.
// packs/codex is the case that bought it: codex's `model_catalog_json` wants FULL catalog
// entries, whose prompt text only codex's own binary holds, so no derive can write the file
// (docs/design/model-lists-and-pickers.md MM-D9).
//
// Every key is the pack's, because every one is a fact about a release of the program: the argv
// that prints its catalog, where in that JSON the entries sit and what they call their id, order
// and display name, which of their fields yolo must clear (codex's `upgrade`, which would steer a
// user off the list) or set, and the flag that names a catalog file. Core reads none of them as a
// word it knows (AGENTS.md, "Core does not know what an agent is").
//
// WHAT THE LAUNCHER GUARANTEES around it, none of which the pack can turn off: it runs only when
// List names at least one model; an id the program's catalog lacks is left out with a warning,
// never written as a thin entry; when no listed id survives, or the catalog cannot be read, no
// file is written, Flag is not added and the program starts on its own catalog; `yolo`'s
// YOLO_NO_LAUNCH_FLAGS=1 skips it with the launch flags; and the file is rebuilt only when the
// program, the list or this declaration changed.
//
// `yolo host -- <bin>` RUNS THE SAME BUILD (docs/design/model-lists-and-pickers.md MM-D24 to
// MM-D28), with the same guarantees and two differences of place: no file sits at List, so the
// host runs the derive of the surface at that path over its own launch's tables; and the menu is
// kept in yolo's state, one file per cache key, rather than at Into, since two host launches of
// one program can hold different lists at once. It builds one only when the launch's `-p`, if
// any, selects the provider the configured profile does.
type ModelMenu struct {
	// Catalog is the program's own argv, bin omitted, that prints its catalog as ONE JSON
	// object on stdout, with no network and no credential: `["debug", "models", "--bundled"]`
	// for codex.
	Catalog []string `json:"catalog"`
	// List is the home-relative path of yolo's list, a file the pack's own derive renders (a
	// computed `config` surface): `{"models": [{"id": "...", "name": "..."}]}`, in menu order.
	// A file naming no model is "no menu this launch". At the host the path names the surface
	// whose derive the launch runs, and no file is read.
	List string `json:"list"`
	// Into is the home-relative path a jail's launcher writes the menu to. The host keeps its
	// menus in yolo's state instead (paths.HostModelMenusDir).
	Into string `json:"into"`
	// Flag is the argv words the launcher adds, ahead of the user's own, when it wrote a menu.
	// ModelMenuInto in a word becomes Into's absolute path: `["-c", "model_catalog_json={into}"]`
	// for codex, whose `-c` is global and whose later `-c` wins, so a user's own still does.
	Flag []string `json:"flag"`
	// Entries is the catalog object's key whose value is the array of entries.
	Entries string `json:"entries"`
	// ID is an entry's key whose string value is the model id the list names.
	ID string `json:"id"`
	// Order, when set, is an entry's key the menu renumbers 0, 1, 2, ... in the list's order.
	Order string `json:"order,omitempty"`
	// Name, when set, is an entry's key that takes the list entry's `name`, where it has one.
	Name string `json:"name,omitempty"`
	// Clear is entry keys deleted from every kept entry.
	Clear []string `json:"clear,omitempty"`
	// Set is entry keys given a string value on every kept entry.
	Set map[string]string `json:"set,omitempty"`
}

// modelMenuProblems refuses a `model_menu` that could never write a menu the program reads: on a
// kind with no program, with a missing or empty argv, a path that is not a clean home-relative
// file path, the list and the menu at one path, a flag that never names the file, or an entry key
// that is empty or claimed twice.
func modelMenuProblems(label string, c Contribution) []string {
	m := c.ModelMenu
	if m == nil {
		return nil
	}
	if c.Kind != KindProgram {
		return []string{fmt.Sprintf("%s: kind %q does not take \"model_menu\" — the launcher "+
			"writes it before exec'ing a PROGRAM, so only \"program\" has a launcher to run it",
			label, c.Kind)}
	}
	var problems []string
	add := func(format string, args ...any) {
		problems = append(problems, fmt.Sprintf("%s: \"model_menu\" "+format, append([]any{label}, args...)...))
	}
	if len(m.Catalog) == 0 {
		add("names no \"catalog\" argv, so nothing prints the program's catalog")
	}
	for _, w := range m.Catalog {
		if w == "" {
			add("\"catalog\" has an empty word")
		}
	}
	for _, p := range []struct{ key, val string }{{"list", m.List}, {"into", m.Into}} {
		if prob := homeRelativeFileProblem(p.val); prob != "" {
			add("%q %s", p.key, prob)
		}
	}
	if m.List != "" && m.List == m.Into {
		add("\"list\" and \"into\" are one path, so writing the menu would overwrite the list it reads")
	}
	named := false
	for _, w := range m.Flag {
		named = named || strings.Contains(w, ModelMenuInto)
	}
	if len(m.Flag) == 0 || !named {
		add("\"flag\" must name the menu file with %q in one of its words, or the program is "+
			"never told the file exists", ModelMenuInto)
	}
	keys := map[string]string{}
	claim := func(role, k string, required bool) {
		if k == "" {
			if required {
				add("names no %q key", role)
			}
			return
		}
		if prev, dup := keys[k]; dup {
			add("gives the entry key %q two roles, %s and %s", k, prev, role)
			return
		}
		keys[k] = role
	}
	claim("entries", m.Entries, true)
	claim("id", m.ID, true)
	claim("order", m.Order, false)
	claim("name", m.Name, false)
	for _, k := range m.Clear {
		if k == "" {
			add("\"clear\" has an empty key")
			continue
		}
		claim("clear", k, false)
	}
	for k := range m.Set {
		if k == "" {
			add("\"set\" has an empty key")
			continue
		}
		claim("set", k, false)
	}
	return problems
}

// homeRelativeFileProblem says why p could never name a file inside the home, "" when it could.
func homeRelativeFileProblem(p string) string {
	switch {
	case p == "":
		return "is empty"
	case strings.Contains(p, `\`):
		return "contains a backslash: paths are slash-separated"
	case strings.HasPrefix(p, "/") || strings.HasPrefix(p, "~"):
		return "is not home-relative: write it relative to the home, without a leading / or ~"
	case path.Clean(p) != p:
		return "is not clean (write it as " + fmt.Sprintf("%q", path.Clean(p)) + ")"
	case p == "." || p == ".." || strings.HasPrefix(p, "../"):
		return "leaves the home"
	}
	return ""
}

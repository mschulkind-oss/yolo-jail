package packdecl

// patchedext.go is the vocabulary of a PATCHED EXTENSION (docs/design/patched-extensions.md §4,
// PPX-D1 to PPX-D3, PPX-D6): a `files` contribution that names an upstream `source` and a
// `patches` series in place of `from`. Its bytes are the upstream at a commit with the series
// replayed, built in the sealed capture jail and admitted as a tree, which a fresh launch mounts
// read-only at `into` and the Linux host links at `~/<into>`.
//
// IT IS THE PATCHED-FORK MODE ON `files`, sharing one implementation through the OWNER KEY
// (patched-forks.md PF-D22): a patched fork's key is `<pack>/<bin>`, a patched extension's
// EXTENSION KEY is `<pack>/<name>`, `<name>` being the last segment of `into`. So the series, the
// ref, `follow` and the replay are a patched fork's (patchedForkProblems), and what is this file's
// own is the placement: which fields the kind takes, which it refuses beside `source`, the key's
// uniqueness, and the recipe, which a tree tags as one so it can never equal a program's.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TreeReservedRoot is the home-relative directory under the build jail's home a patched
// extension's build copies its checkout into, one directory per extension name (PPX-D5's fixed
// final step): under `.local`, which is a program surface the capture driver already walks, so the
// driver is unchanged and the admitted delta holds the tree. The spelling is this
// implementation's (patched-extensions.md §7.1 leaves it to the implementer).
const TreeReservedRoot = ".local/share/yolo-tree"

// IsPatchedExtension reports whether c is a PATCHED EXTENSION: a `files` contribution that
// declares `patches` (PPX-D1). Its `source` names the upstream, and `from` is refused beside it.
func (c Contribution) IsPatchedExtension() bool {
	return c.Kind == KindFiles && c.Patches != ""
}

// IsBuiltTree reports whether c declares a BUILT TREE (patched-extensions.md §1): a `files`
// contribution naming an upstream `source` or a `patches` series in place of `from` — a patched
// extension, or an UNMODIFIED one (docs/design/pi-extension-store-builds.md §4.1, XB-D1), which is a
// patched extension with no series. Both are built, admitted, delivered and followed by one
// implementation; only the series differs.
func (c Contribution) IsBuiltTree() bool {
	return c.Kind == KindFiles && (c.Source != "" || c.Patches != "")
}

// IsUnmodifiedExtension reports whether c is an UNMODIFIED EXTENSION: a built tree with no series,
// its bytes the upstream's at one commit or version (XB-D1).
func (c Contribution) IsUnmodifiedExtension() bool { return c.IsBuiltTree() && c.Patches == "" }

// ExtensionName is a patched extension's name: the last segment of its `into`, which is the name
// half of its extension key, `<pack>/<name>` (PPX-D2).
func ExtensionName(into string) string {
	clean := path.Clean(strings.TrimSuffix(into, "/"))
	if clean == "." || clean == "/" {
		return ""
	}
	return path.Base(clean)
}

// ExtensionName is c's extension name, "" for anything that is not a built tree.
func (c Contribution) ExtensionName() string {
	if !c.IsBuiltTree() {
		return ""
	}
	return ExtensionName(c.Into)
}

// UnmodifiedGitBuild is an unmodified git extension's build when it declares none
// (docs/design/pi-extension-store-builds.md XB-D3): npm's install of the checkout's own production
// dependencies, run whenever the checkout has a package.json and never otherwise — exactly the
// dependency step pi runs for the same `git:` entry, so the tree is what pi's own install would
// leave. A patched extension has no default (PPX-D3): its author writes the build its series needs.
const UnmodifiedGitBuild = "[ ! -f package.json ] || npm install --omit=dev --legacy-peer-deps"

// npmTreeVersion is where an npm tree's recipe line names the version, which every build of the
// tree fills in (NpmTreeInstall) and the recipe never does: one recipe serves every version.
const npmTreeVersion = "<version>"

// NpmTreeInstall is an npm tree's build line for version (XB-D6): pi's own install of an npm
// package, `npm install <name>@<version> --prefix <tree> --legacy-peer-deps`, into the build's
// empty checkout, whose copy is the tree. So the tree is an npm prefix, the shape pi's install
// leaves, and Node resolves the package's dependencies in it as it does under pi's.
func NpmTreeInstall(name, version string) string {
	return "npm install " + shquote.Quote(name+"@"+version) + " --prefix . --legacy-peer-deps"
}

// TreeBuild is the build line c's tree is built with, as its recipe reads it: a patched extension's
// own `build` (none is no command, PPX-D3); an unmodified git extension's `build`, or
// UnmodifiedGitBuild; an npm extension's install, its version left to each build (npmTreeVersion).
func (c Contribution) TreeBuild() string {
	return TreeBuildLine(c.Source, c.Patches, c.Build)
}

// TreeBuildLine is Contribution.TreeBuild from a built tree's source, series directory and `build`.
func TreeBuildLine(source, patches, build string) string {
	if patches != "" {
		return build
	}
	if n, err := packsrc.ParseNpm(source); err == nil {
		return NpmTreeInstall(n.Name, npmTreeVersion)
	}
	if strings.TrimSpace(build) == "" {
		return UnmodifiedGitBuild
	}
	return build
}

// TreeListEntry is the list entry an agent loads a built tree landing at into through
// (patched-extensions.md §8.2, XB-D6): `~/<into>`, with no trailing slash, or for an npm source the
// package inside the npm prefix the tree is, `~/<into>/node_modules/<name>`. The lint, the owning
// agent pack and the fallback compare that exact string; core reads none of the agent's grammar.
func TreeListEntry(source, into string) string {
	entry := "~/" + strings.TrimSuffix(into, "/")
	if n, err := packsrc.ParseNpm(source); err == nil {
		entry += "/node_modules/" + n.Name
	}
	return entry
}

// TreeListEntry is c's list entry (the package function).
func (c Contribution) TreeListEntry() string { return TreeListEntry(c.Source, c.Into) }

// TreeReservedDir is the home-relative directory the build of the patched extension named name
// leaves its tree in (TreeReservedRoot).
func TreeReservedDir(name string) string { return TreeReservedRoot + "/" + name }

// TreeRecipe is a patched extension's RECIPE HASH (patched-extensions.md §7.2, PPX-D6): the sha256,
// in hex, of the JSON array ["tree", build, sorted produces, subdir, series digest]. The leading
// "tree" tag means a tree's recipe can never equal a program's, whose arrays carry no tag
// (ForkRecipe, PatchedForkRecipe), so no lookup of one ever serves the other.
func TreeRecipe(build string, produces []string, subdir, series string) string {
	sorted := append([]string(nil), produces...)
	sort.Strings(sorted)
	canonical, _ := json.Marshal([]any{"tree", build, sorted, subdir, series})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// TreeSourceRecipe is TreeRecipe for a patched extension's declaration as the manifest spells it,
// the subdirectory read off the source address as ForkSourceRecipe reads it.
func TreeSourceRecipe(source, build string, produces []string, series string) string {
	sub := ""
	if a, err := packsrc.Parse(source); err == nil {
		sub = a.Path
	}
	return TreeRecipe(build, produces, sub, series)
}

// patchedExtensionProblems validates a patched extension (patched-extensions.md §4, PPX-D1 to
// PPX-D3): `source`, `patches` and `into` required; `from`, `fork_of`, `agent` and `agents`
// refused beside `source`, each naming why; `build` one command line when it is there; `produces`
// clean tree-relative paths; and the series, the ref and `follow` as a patched fork's
// (patchedForkProblems). What the series directory holds is read at each advance, never here.
func patchedExtensionProblems(label string, c Contribution) []string {
	var problems []string
	const what = "a patched extension (\"source\" and \"patches\" on \"files\")"
	if c.From != "" {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"from\" — \"from\" names a tree in "+
			"the pack itself, and a patched extension's bytes are its upstream's with the series "+
			"replayed and built; drop \"from\", or drop \"source\" and \"patches\" to ship the pack's "+
			"own tree", label, what))
	}
	if c.ForkOf != "" {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"fork_of\" — \"fork_of\" names the "+
			"pack whose PROGRAM a fork builds, and an extension is a tree with no bin and no base "+
			"program; a patched program is a \"program\" with via %q, \"fork_of\" and \"patches\"",
			label, what, ViaSource))
	}
	if c.Agent != "" {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"agent\" — \"agent\" makes a "+
			"\"files\" contribution an agent's DESTINATION, which ships nothing, and a patched "+
			"extension is content landing at its own \"into\"", label, what))
	}
	if len(c.Agents) > 0 {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"agents\" — an addressed tree lands "+
			"in a slot core picks, and a patched extension lands at the \"into\" it names, which "+
			"the agent's list entry names too (~/<into>); name \"into\"", label, what))
	}
	if c.Source == "" {
		problems = append(problems, label+": "+what+" needs \"source\" — the UPSTREAM's git address, "+
			"with ?ref= a branch to follow or a tag or a full commit to hold at, e.g. "+
			"git+https://github.com/<upstream>/<extension>?ref=main")
	} else if why := ForkSourceProblem(c.Source); why != "" {
		problems = append(problems, fmt.Sprintf("%s.source %q: %s", label, c.Source, why))
	}
	if c.Into == "" {
		problems = append(problems, label+": "+what+" needs \"into\" — the home-relative directory "+
			"its built tree lands at, e.g. \".pi/agent/yolo-patched/<name>\"; its last segment is "+
			"the extension's name")
	} else if name := ExtensionName(c.Into); !ValidBinName(name) {
		problems = append(problems, fmt.Sprintf("%s.into %q: its last segment, %q, is the "+
			"extension's name, and must be a bare name", label, c.Into, name))
	}
	if strings.ContainsAny(c.Build, "\r\n\x00") {
		problems = append(problems, label+".build: must be ONE command line — a newline makes it a "+
			"script, which belongs in the extension's repository where the command can call it")
	}
	problems = append(problems, treeProducesProblems(label, c.Produces)...)
	problems = append(problems, patchedForkProblems(label, c)...)
	return problems
}

// builtTreeProblems validates a built tree's declaration: a patched extension's
// (patchedExtensionProblems) or an unmodified one's (unmodifiedExtensionProblems).
func builtTreeProblems(label string, c Contribution) []string {
	if c.IsPatchedExtension() {
		problems := patchedExtensionProblems(label, c)
		if c.Fallback != "" {
			problems = append(problems, label+": a patched extension takes no \"fallback\" — a fallback is "+
				"the upstream's own entry, which the agent installs unpatched, and a series asks for its "+
				"patches; drop \"fallback\", or drop \"patches\" to deliver the upstream unmodified")
		}
		return problems
	}
	return unmodifiedExtensionProblems(label, c)
}

// unmodifiedExtensionProblems validates an UNMODIFIED EXTENSION (docs/design/pi-extension-store-builds.md
// §4.1, XB-D1 to XB-D7): `source` — a git address as a patched extension's, or `npm:<name>[@<spec>]` —
// and `into` required; `from`, `fork_of`, `agent` and `agents` refused as on a patched extension;
// `follow` a git source's alone, its default `head` (XB-D2); `build` one command line, a git source's
// alone, since an npm source's build is npm's own install (XB-D6); `produces` tree-relative; and
// `fallback`, when it is there, one line that is not the tree's own list entry.
func unmodifiedExtensionProblems(label string, c Contribution) []string {
	var problems []string
	const what = "an unmodified extension (\"source\" on \"files\")"
	if c.From != "" {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"from\" — \"from\" names a tree in "+
			"the pack itself, and an unmodified extension's bytes are its upstream's, built; drop "+
			"\"from\", or drop \"source\" to ship the pack's own tree", label, what))
	}
	if c.ForkOf != "" {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"fork_of\" — an extension is a tree "+
			"with no bin and no base program; a program built from source is a \"program\" with via %q "+
			"and \"fork_of\"", label, what, ViaSource))
	}
	if c.Agent != "" {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"agent\" — \"agent\" makes a "+
			"\"files\" contribution an agent's DESTINATION, which ships nothing", label, what))
	}
	if len(c.Agents) > 0 {
		problems = append(problems, fmt.Sprintf("%s: %s takes no \"agents\" — it lands at the \"into\" "+
			"it names, which the agent's list entry names too; name \"into\"", label, what))
	}
	npm := packsrc.IsNpmSource(c.Source)
	switch {
	case c.Source == "":
		problems = append(problems, label+": "+what+" needs \"source\"")
	case npm:
		if _, err := packsrc.ParseNpm(c.Source); err != nil {
			problems = append(problems, fmt.Sprintf("%s.source: %v", label, err))
		}
		if c.Follow != "" {
			problems = append(problems, fmt.Sprintf("%s: \"follow\" is a git source's — an npm source "+
				"follows its spec: no spec or a dist-tag follows that tag, a range its highest version, "+
				"and an exact version holds there (%s@1.2.3)", label, c.Source))
		}
		if c.Build != "" {
			problems = append(problems, fmt.Sprintf("%s: an npm source takes no \"build\" — its tree is "+
				"npm's own install of the package, which is all an unmodified npm extension is; an "+
				"extension that needs a build of its own is built from its git source", label))
		}
	default:
		if why := ForkSourceProblem(c.Source); why != "" {
			problems = append(problems, fmt.Sprintf("%s.source %q: %s — or an npm package, "+
				"npm:<name>[@<spec>]", label, c.Source, why))
		} else if a, err := packsrc.Parse(c.Source); err == nil && a.Ref == "HEAD" {
			problems = append(problems, fmt.Sprintf("%s.source %q: ?ref=HEAD moves with its branch "+
				"whenever the mirror is fetched — name the branch to follow (?ref=main), or a tag or a "+
				"full commit to hold at", label, c.Source))
		}
		if c.Follow != "" {
			if _, err := packsrc.ParseFollow(c.Follow); err != nil {
				problems = append(problems, fmt.Sprintf("%s.follow: %v", label, err))
			}
		}
		if strings.ContainsAny(c.Build, "\r\n\x00") {
			problems = append(problems, label+".build: must be ONE command line — a newline makes it a "+
				"script, which belongs in the extension's repository where the command can call it")
		}
	}
	if c.Into == "" {
		problems = append(problems, label+": "+what+" needs \"into\" — the home-relative directory "+
			"its built tree lands at, e.g. \".pi/agent/yolo-ext/<name>\"; its last segment is the "+
			"extension's name")
	} else if name := ExtensionName(c.Into); !ValidBinName(name) {
		problems = append(problems, fmt.Sprintf("%s.into %q: its last segment, %q, is the "+
			"extension's name, and must be a bare name", label, c.Into, name))
	}
	problems = append(problems, treeProducesProblems(label, c.Produces)...)
	switch {
	case c.Fallback == "":
	case strings.ContainsAny(c.Fallback, "\r\n\x00") || strings.TrimSpace(c.Fallback) != c.Fallback:
		problems = append(problems, label+".fallback: must be one list entry on one line, with no "+
			"leading or trailing space — the agent's own spelling of the extension, e.g. "+
			"\"npm:<name>\" or \"git:github.com/<owner>/<repo>@<commit>\"")
	case c.Into != "" && c.Fallback == c.TreeListEntry():
		problems = append(problems, fmt.Sprintf("%s.fallback %q: is the tree's own list entry — a "+
			"fallback is what the agent installs ITSELF where no tree is handed, the raw entry the "+
			"tree replaced", label, c.Fallback))
	}
	return problems
}

// fallbackPlacementProblems refuses `fallback` on everything but an unmodified extension, whose
// one consumer it is (builtTreeProblems refuses it beside `patches`, naming why).
func fallbackPlacementProblems(label string, c Contribution) []string {
	if c.Fallback == "" || c.IsBuiltTree() {
		return nil
	}
	return []string{fmt.Sprintf("%s: kind %q does not take \"fallback\" — it is an unmodified "+
		"extension's raw list entry (\"files\" with \"source\" and no \"patches\"), which only that "+
		"declaration reads", label, c.Kind)}
}

// treeProducesProblems validates a patched extension's `produces`: optional, and each entry a
// clean path RELATIVE TO THE BUILT TREE (never the home), listed once. The build's admit checks
// each one exists in the tree it leaves (PPX-D5).
func treeProducesProblems(label string, produces []string) []string {
	var problems []string
	seen := map[string]int{}
	for i, p := range produces {
		entry := fmt.Sprintf("%s.produces[%d]", label, i)
		switch {
		case p == "" || p == "." || strings.HasPrefix(p, "/") || path.Clean(p) != p:
			problems = append(problems, fmt.Sprintf("%s: %q must be a clean path relative to the "+
				"built tree (no leading /, ./, //, or trailing /), e.g. \"dist/index.js\"", entry, p))
			continue
		case strings.Contains(p, "\\"):
			problems = append(problems, fmt.Sprintf("%s: %q must use \"/\" as its separator", entry, p))
			continue
		}
		escapes := false
		for _, seg := range strings.Split(p, "/") {
			escapes = escapes || seg == ".."
		}
		if escapes {
			problems = append(problems, fmt.Sprintf("%s: %q must stay inside the built tree (no \"..\")", entry, p))
			continue
		}
		if first, dup := seen[p]; dup {
			problems = append(problems, fmt.Sprintf("%s: %q is already listed at [%d]", entry, p, first))
			continue
		}
		seen[p] = i
	}
	return problems
}

// validatePatchedOwnerKeys refuses two of one pack's patched declarations under one OWNER KEY
// (PPX-D2, PF-D22): a patched extension's `<pack>/<name>` against every other patched extension's
// and every patched fork's `<pack>/<bin>`. The key names the check record, its lock, the build's
// selection and every explicit act, so two declarations under one would share a record and read
// each other's good build. Strict path only, like every sibling here.
func (m *Manifest) validatePatchedOwnerKeys() []string {
	pack := m.Name
	if pack == "" {
		pack = "<pack>"
	}
	type first struct {
		at   int
		what string
	}
	seen := map[string]first{}
	var problems []string
	for i, c := range m.Contributes {
		var name, what string
		switch {
		case c.IsBuiltTree():
			name = ExtensionName(c.Into)
			what = fmt.Sprintf("the extension at %q", c.Into)
		case c.IsPatchedFork():
			name = c.Bin
			what = fmt.Sprintf("the patched fork of %q", c.Bin)
		default:
			continue
		}
		if name == "" {
			continue // the declaration's own problem, reported by validateContribution
		}
		if prev, dup := seen[name]; dup {
			problems = append(problems, fmt.Sprintf("contributes[%d]: %s has the owner key %s/%s, "+
				"which %s (contributes[%d]) already has — the key names the check record, the "+
				"build and every explicit act (`yolo pack update`, `yolo capture %s/%s`), so two "+
				"declarations under one would share them; give one of them another \"into\" leaf "+
				"or bin", i, what, pack, name, prev.what, prev.at, pack, name))
			continue
		}
		seen[name] = first{at: i, what: what}
	}
	return problems
}

package packdecl

// patchedext_test.go pins the manifest half of a PATCHED EXTENSION (docs/design/patched-extensions.md
// §4, PPX-D1 to PPX-D3, PPX-D6): `source` and `patches` on `files` in place of `from`; `from`,
// `fork_of`, `agent` and `agents` refused beside them, each naming why; the fork fields still
// refused on a `files` contribution with no series, with their message unchanged; the extension
// key's uniqueness against the pack's patched programs; and a recipe tagged as a tree's.

import (
	"strings"
	"testing"
)

func goodPatchedExtension() Contribution {
	return Contribution{Kind: KindFiles, Into: ".pi/agent/yolo-patched/pi-subagents",
		Source:  "git+https://github.com/upstream/pi-subagents?ref=main",
		Patches: "patches/pi-subagents",
		Build:   "npm install --omit=dev --legacy-peer-deps --ignore-scripts"}
}

func TestAPatchedExtensionValidates(t *testing.T) {
	for _, edit := range []struct {
		name string
		fn   func(*Contribution)
	}{
		{"as written", func(*Contribution) {}},
		{"with no build", func(c *Contribution) { c.Build = "" }},
		{"with produces", func(c *Contribution) { c.Produces = []string{"dist/pi-extension.js", "package.json"} }},
		{"following head", func(c *Contribution) { c.Follow = "head" }},
		{"held at a tag", func(c *Contribution) { c.Source = "git+https://github.com/upstream/pi-subagents?ref=v0.75.0" }},
	} {
		c := goodPatchedExtension()
		edit.fn(&c)
		if probs := validateContribution("contributes[0]", c); len(probs) > 0 {
			t.Errorf("%s: a patched extension is refused:\n%s", edit.name, strings.Join(probs, "\n"))
		}
	}
	c := goodPatchedExtension()
	if !c.IsPatchedExtension() || c.IsFork() || c.IsPatchedFork() {
		t.Error("a files contribution with patches does not read as a patched extension alone")
	}
	if got := c.ExtensionName(); got != "pi-subagents" {
		t.Errorf("ExtensionName = %q, want the last segment of into", got)
	}
}

func TestPatchedExtensionRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Contribution)
		want []string
	}{
		{"from beside source", func(c *Contribution) { c.From = "ext" },
			[]string{`takes no "from"`, "names a tree in the pack itself"}},
		{"fork_of beside source", func(c *Contribution) { c.ForkOf = "pi" },
			[]string{`takes no "fork_of"`, "no bin and no base program"}},
		{"agent beside source", func(c *Contribution) { c.Agent = "pi" },
			[]string{`takes no "agent"`, "DESTINATION"}},
		{"agents beside source", func(c *Contribution) { c.Into, c.Agents = "", []string{"pi"} },
			[]string{`takes no "agents"`, `needs "into"`}},
		{"no source", func(c *Contribution) { c.Source = "" },
			[]string{`needs "source"`, "?ref="}},
		{"a directory source", func(c *Contribution) { c.Source = "file:///srv/ext" },
			[]string{"a fork builds a REVISION"}},
		{"HEAD as the ref", func(c *Contribution) { c.Source = "git+https://github.com/u/x?ref=HEAD" },
			[]string{"HEAD is none of them"}},
		{"no into", func(c *Contribution) { c.Into = "" },
			[]string{`needs "into"`, "extension's name"}},
		{"a two-line build", func(c *Contribution) { c.Build = "npm ci\nnpm run build" },
			[]string{"ONE command line"}},
		{"an absolute produces", func(c *Contribution) { c.Produces = []string{"/dist/x.js"} },
			[]string{"relative to the built tree"}},
		{"an escaping produces", func(c *Contribution) { c.Produces = []string{"dist/../../x"} },
			[]string{"relative to the built tree"}},
		{"a duplicate produces", func(c *Contribution) { c.Produces = []string{"a.js", "a.js"} },
			[]string{"already listed at [0]"}},
		{"a follow outside the grammar", func(c *Contribution) { c.Follow = "latest" },
			[]string{"follow"}},
		{"an escaping series", func(c *Contribution) { c.Patches = "../patches" },
			[]string{`no ".."`}},
		{"into on PATH", func(c *Contribution) { c.Into = ".local/bin/ext" },
			[]string{"on the jail's PATH"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := goodPatchedExtension()
			tc.edit(&c)
			joined := strings.Join(validateContribution("contributes[0]", c), "\n")
			for _, w := range tc.want {
				if !strings.Contains(joined, w) {
					t.Errorf("want a problem containing %q, got:\n%s", w, joined)
				}
			}
		})
	}
}

// WITHOUT A SERIES the fork fields are still a fork's alone on `files`, with the message they had
// (patched-extensions.md §4: "a `files` contribution with `build` and no series is still refused,
// with the fork fields' message").
func TestForkFieldsOnFilesWithoutPatchesKeepTheirRefusal(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    Contribution
		want string
	}{
		{"build", Contribution{Kind: KindFiles, Into: ".pi/x", From: "x", Build: "make"}, `kind "files" does not take "build"`},
		{"source", Contribution{Kind: KindFiles, Into: ".pi/x", From: "x", Source: "git+https://h/x?ref=main"},
			`kind "files" does not take "source"`},
		{"produces", Contribution{Kind: KindFiles, Into: ".pi/x", From: "x", Produces: []string{"a"}},
			`kind "files" does not take "produces"`},
		{"follow", Contribution{Kind: KindFiles, Into: ".pi/x", From: "x", Follow: "head"},
			`kind "files" does not take "follow"`},
	} {
		joined := strings.Join(validateContribution("contributes[0]", tc.c), "\n")
		if !strings.Contains(joined, tc.want) || !strings.Contains(joined, "FORK") {
			t.Errorf("%s: want the fork fields' refusal %q, got:\n%s", tc.name, tc.want, joined)
		}
	}
}

// THE EXTENSION KEY IS UNIQUE PER PACK across its patched extensions and its patched programs' bins
// (PPX-D2), and the refusal names both declarations.
func TestTheExtensionKeyIsUniqueAcrossAPacksPatchedDeclarations(t *testing.T) {
	twin := goodPatchedExtension()
	twin.Into = ".pi/agent/other/pi-subagents"
	m := Manifest{Name: "matt", Contributes: []Contribution{goodPatchedExtension(), twin}}
	joined := strings.Join(m.validatePatchedOwnerKeys(), "\n")
	for _, w := range []string{"contributes[1]", "contributes[0]", "matt/pi-subagents", "owner key"} {
		if !strings.Contains(joined, w) {
			t.Errorf("two extensions named pi-subagents: want %q in:\n%s", w, joined)
		}
	}
	fork := goodPatchedFork()
	ext := goodPatchedExtension()
	ext.Into = ".pi/agent/yolo-patched/" + fork.Bin
	m = Manifest{Name: "matt", Contributes: []Contribution{fork, ext}}
	if probs := m.validatePatchedOwnerKeys(); len(probs) != 1 || !strings.Contains(probs[0], "the patched fork of") {
		t.Errorf("an extension named for the pack's patched fork's bin: want one refusal naming the fork, got %q", probs)
	}
	other := goodPatchedExtension()
	other.Into = ".pi/agent/yolo-patched/pi-archimedes"
	m = Manifest{Name: "matt", Contributes: []Contribution{goodPatchedExtension(), other, goodFork()}}
	if probs := m.validatePatchedOwnerKeys(); len(probs) != 0 {
		t.Errorf("distinct keys, and a plain fork: refused %q", probs)
	}
	// And the manifest's own validation reaches it.
	m = Manifest{Name: "matt", Contributes: []Contribution{goodPatchedExtension(), twin}}
	if !strings.Contains(strings.Join(m.Validate(), "\n"), "owner key") {
		t.Error("Manifest.Validate does not run the owner-key check")
	}
}

// A TREE'S RECIPE is tagged as one: it never equals a patched program's of the same inputs, its
// produces' order is immaterial, and every input moves it.
func TestATreeRecipeIsTaggedAndCarriesItsInputs(t *testing.T) {
	series := strings.Repeat("a", 64)
	tree := TreeRecipe("npm ci", []string{"b", "a"}, "", series)
	if tree == PatchedForkRecipe("npm ci", []string{"a", "b"}, "", series) {
		t.Error("a tree's recipe equals a patched program's of the same inputs")
	}
	if tree != TreeRecipe("npm ci", []string{"a", "b"}, "", series) {
		t.Error("the order of produces moves a tree's recipe")
	}
	for _, other := range []string{
		TreeRecipe("npm i", []string{"a", "b"}, "", series),
		TreeRecipe("npm ci", []string{"a"}, "", series),
		TreeRecipe("npm ci", []string{"a", "b"}, "sub", series),
		TreeRecipe("npm ci", []string{"a", "b"}, "", strings.Repeat("b", 64)),
	} {
		if other == tree {
			t.Error("an input of a tree's recipe does not move it")
		}
	}
	if got := TreeSourceRecipe("git+https://h/mono//packages/ext?ref=main", "npm ci", []string{"a", "b"}, series); got !=
		TreeRecipe("npm ci", []string{"a", "b"}, "packages/ext", series) {
		t.Error("TreeSourceRecipe does not read the subdirectory off the source")
	}
}

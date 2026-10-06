package packdecl

// unmodifiedext_test.go pins the manifest half of an UNMODIFIED EXTENSION
// (docs/design/pi-extension-store-builds.md §4.1, XB-D1 to XB-D7): `source` on `files` with no
// `patches`, a git address or `npm:<name>[@<spec>]`; the fields it refuses, each naming why; the
// `fallback` it alone takes; its default build and its list entry.

import (
	"strings"
	"testing"
)

const pinnedCommit = "5650274186687d1c5bfb18c8d11276a31830e0b3"

func goodUnmodifiedGit() Contribution {
	return Contribution{Kind: KindFiles, Into: ".pi/agent/yolo-ext/pi-thread-goal",
		Source:   "git+https://github.com/T50-Systems/pi-thread-goal?ref=" + pinnedCommit,
		Fallback: "git:github.com/T50-Systems/pi-thread-goal@" + pinnedCommit}
}

func goodUnmodifiedNpm() Contribution {
	return Contribution{Kind: KindFiles, Into: ".pi/agent/yolo-ext/pi-web-access",
		Source: "npm:pi-web-access", Fallback: "npm:pi-web-access"}
}

func TestAnUnmodifiedExtensionValidates(t *testing.T) {
	for _, tc := range []struct {
		name string
		c    func() Contribution
		edit func(*Contribution)
	}{
		{"git, held at a commit", goodUnmodifiedGit, func(*Contribution) {}},
		{"git, following a branch", goodUnmodifiedGit, func(c *Contribution) {
			c.Source = "git+https://github.com/T50-Systems/pi-thread-goal?ref=main"
		}},
		{"git, with a build and produces", goodUnmodifiedGit, func(c *Contribution) {
			c.Build, c.Produces = "npm ci && npm run build", []string{"dist/index.js"}
		}},
		{"git, following releases", goodUnmodifiedGit, func(c *Contribution) { c.Follow = "release" }},
		{"git, with no fallback", goodUnmodifiedGit, func(c *Contribution) { c.Fallback = "" }},
		{"npm, latest", goodUnmodifiedNpm, func(*Contribution) {}},
		{"npm, an exact version", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:pi-web-access@1.4.2" }},
		{"npm, a range", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:pi-web-access@^1.4.0" }},
		{"npm, a dist-tag", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:pi-web-access@next" }},
		{"npm, scoped", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:@org/pi-thing@~2.1" }},
		{"npm, with produces", goodUnmodifiedNpm, func(c *Contribution) {
			c.Produces = []string{"node_modules/pi-web-access/package.json"}
		}},
	} {
		c := tc.c()
		tc.edit(&c)
		if probs := validateContribution("contributes[0]", c); len(probs) > 0 {
			t.Errorf("%s: an unmodified extension is refused:\n%s", tc.name, strings.Join(probs, "\n"))
		}
		if !c.IsBuiltTree() || !c.IsUnmodifiedExtension() || c.IsPatchedExtension() {
			t.Errorf("%s: a files contribution with a source and no patches does not read as an unmodified "+
				"extension alone", tc.name)
		}
	}
	if got := goodUnmodifiedNpm().ExtensionName(); got != "pi-web-access" {
		t.Errorf("ExtensionName = %q, want the last segment of into", got)
	}
}

func TestAnUnmodifiedExtensionRefusesFromBesideItsSource(t *testing.T) {
	c := goodUnmodifiedGit()
	c.From = "ext"
	joined := strings.Join(validateContribution("contributes[0]", c), "\n")
	if !strings.Contains(joined, `takes no "from"`) || !strings.Contains(joined, "unmodified extension") {
		t.Errorf("source and from on files: want the unmodified extension's refusal of from, got:\n%s", joined)
	}
}

func TestUnmodifiedExtensionRefusals(t *testing.T) {
	cases := []struct {
		name string
		c    func() Contribution
		edit func(*Contribution)
		want []string
	}{
		{"fork_of", goodUnmodifiedGit, func(c *Contribution) { c.ForkOf = "pi" }, []string{`takes no "fork_of"`}},
		{"agent", goodUnmodifiedGit, func(c *Contribution) { c.Agent = "pi" }, []string{`takes no "agent"`}},
		{"agents", goodUnmodifiedGit, func(c *Contribution) { c.Into, c.Agents = "", []string{"pi"} },
			[]string{`takes no "agents"`, `needs "into"`}},
		{"a file:// source", goodUnmodifiedGit, func(c *Contribution) { c.Source = "file:///tmp/x" },
			[]string{"REVISION", "npm:<name>"}},
		{"?ref=HEAD", goodUnmodifiedGit, func(c *Contribution) {
			c.Source = "git+https://github.com/o/r?ref=HEAD"
		}, []string{"?ref=HEAD moves"}},
		{"a bad follow", goodUnmodifiedGit, func(c *Contribution) { c.Follow = "newest" }, []string{"not a follow rule"}},
		{"a two-line build", goodUnmodifiedGit, func(c *Contribution) { c.Build = "a\nb" }, []string{"ONE command line"}},
		{"an npm follow", goodUnmodifiedNpm, func(c *Contribution) { c.Follow = "head" },
			[]string{`"follow" is a git source's`}},
		{"an npm build", goodUnmodifiedNpm, func(c *Contribution) { c.Build = "npm run build" },
			[]string{`an npm source takes no "build"`}},
		{"an npm name in capitals", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:Pi-Web" },
			[]string{"lowercase"}},
		{"an npm name led by -", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:--prefix" },
			[]string{"starts with -"}},
		{"an npm @ with no spec", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:pi-web-access@" },
			[]string{"names no spec"}},
		{"an npm spec that is none of the three", goodUnmodifiedNpm, func(c *Contribution) {
			c.Source = "npm:pi-web-access@>=1.2.3 <=foo"
		}, []string{"neither a version"}},
		{"an npm scope with no package", goodUnmodifiedNpm, func(c *Contribution) { c.Source = "npm:@org" },
			[]string{"a scope with no package"}},
		{"a fallback that is the tree's entry", goodUnmodifiedNpm, func(c *Contribution) {
			c.Fallback = "~/.pi/agent/yolo-ext/pi-web-access/node_modules/pi-web-access"
		}, []string{"is the tree's own list entry"}},
		{"a two-line fallback", goodUnmodifiedNpm, func(c *Contribution) { c.Fallback = "npm:a\nnpm:b" },
			[]string{"one list entry on one line"}},
		{"a padded fallback", goodUnmodifiedNpm, func(c *Contribution) { c.Fallback = " npm:a" },
			[]string{"one list entry on one line"}},
		{"a bare name into", goodUnmodifiedNpm, func(c *Contribution) { c.Into = "" }, []string{`needs "into"`}},
	}
	for _, tc := range cases {
		c := tc.c()
		tc.edit(&c)
		joined := strings.Join(validateContribution("contributes[0]", c), "\n")
		for _, w := range tc.want {
			if !strings.Contains(joined, w) {
				t.Errorf("%s: want a problem containing %q, got:\n%s", tc.name, w, joined)
			}
		}
	}
}

// A FALLBACK IS AN UNMODIFIED EXTENSION'S ALONE (XB-D7): refused beside `patches`, since the upstream
// unpatched is not what a series asks for, and on every other kind, since nothing reads it there.
func TestAFallbackIsAnUnmodifiedExtensionsAlone(t *testing.T) {
	patched := goodPatchedExtension()
	patched.Fallback = "git:github.com/upstream/pi-subagents"
	if joined := strings.Join(validateContribution("contributes[0]", patched), "\n"); !strings.Contains(joined,
		`a patched extension takes no "fallback"`) {
		t.Errorf("a fallback beside patches: want its refusal, got:\n%s", joined)
	}
	prog := Contribution{Kind: KindProgram, Bin: "x", Via: "npm", Package: "x", Fallback: "npm:x"}
	if joined := strings.Join(validateContribution("contributes[0]", prog), "\n"); !strings.Contains(joined,
		`kind "program" does not take "fallback"`) {
		t.Errorf("a fallback on a program: want its refusal, got:\n%s", joined)
	}
	tree := Contribution{Kind: KindFiles, Into: ".pi/x", From: "x", Fallback: "npm:x"}
	if joined := strings.Join(validateContribution("contributes[0]", tree), "\n"); !strings.Contains(joined,
		`kind "files" does not take "fallback"`) {
		t.Errorf("a fallback on a pack's own tree: want its refusal, got:\n%s", joined)
	}
}

// THE BUILD AND THE LIST ENTRY (XB-D3, XB-D6): an unmodified git extension with no build runs npm's
// production install whenever the checkout has a package.json; an npm extension's build is npm's own
// install into the checkout, its version filled in per build; a patched extension keeps its own
// build, none being no command (PPX-D3). An npm tree's list entry is the package inside the prefix.
func TestTheBuildLineAndTheListEntryOfEachBuiltTree(t *testing.T) {
	git := goodUnmodifiedGit()
	if got := git.TreeBuild(); got != UnmodifiedGitBuild {
		t.Errorf("an unmodified git extension with no build: TreeBuild = %q, want %q", got, UnmodifiedGitBuild)
	}
	git.Build = "make dist"
	if got := git.TreeBuild(); got != "make dist" {
		t.Errorf("an unmodified git extension's own build is replaced: %q", got)
	}
	npm := goodUnmodifiedNpm()
	if got, want := npm.TreeBuild(), NpmTreeInstall("pi-web-access", "<version>"); got != want {
		t.Errorf("an npm extension's TreeBuild = %q, want %q", got, want)
	}
	if got, want := NpmTreeInstall("@org/x", "1.2.3"), "npm install @org/x@1.2.3 --prefix . --legacy-peer-deps"; got != want {
		t.Errorf("NpmTreeInstall = %q, want %q", got, want)
	}
	patched := goodPatchedExtension()
	patched.Build = ""
	if got := patched.TreeBuild(); got != "" {
		t.Errorf("a patched extension with no build got a default: %q", got)
	}
	if got, want := npm.TreeListEntry(), "~/.pi/agent/yolo-ext/pi-web-access/node_modules/pi-web-access"; got != want {
		t.Errorf("an npm tree's list entry = %q, want %q", got, want)
	}
	if got, want := goodUnmodifiedGit().TreeListEntry(), "~/.pi/agent/yolo-ext/pi-thread-goal"; got != want {
		t.Errorf("a git tree's list entry = %q, want %q", got, want)
	}
}

// THE OWNER KEY is unique across a pack's patched and unmodified extensions alike (PPX-D2).
func TestTheOwnerKeyIsUniqueAcrossPatchedAndUnmodifiedExtensions(t *testing.T) {
	ext := goodUnmodifiedNpm()
	ext.Into = ".pi/agent/other/pi-subagents"
	m := Manifest{Name: "matt", Contributes: []Contribution{goodPatchedExtension(), ext}}
	if probs := m.validatePatchedOwnerKeys(); len(probs) != 1 || !strings.Contains(probs[0], "matt/pi-subagents") {
		t.Errorf("a patched and an unmodified extension named pi-subagents: want one refusal, got %q", probs)
	}
}

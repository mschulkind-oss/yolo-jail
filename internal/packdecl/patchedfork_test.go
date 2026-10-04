package packdecl

// patchedfork_test.go pins the manifest half of a PATCHED fork (docs/design/patched-forks.md §3.1,
// PF-D1 to PF-D3, PF-D7): `patches` and `follow` accepted on a fork and refused everywhere else
// with the declaration that reads them named, their grammar, the recipe that gains the series
// digest for a patched fork only, and the strict decode's next step for an unknown field.

import (
	"strings"
	"testing"
)

func goodPatchedFork() Contribution {
	c := goodFork()
	c.Source = "git+https://github.com/earendil-works/pi?ref=main"
	c.Patches = "patches"
	return c
}

func TestAPatchedForkValidates(t *testing.T) {
	for _, follow := range []string{"", "release", "head", "release:pi-coding-agent@"} {
		c := goodPatchedFork()
		c.Follow = follow
		if probs := validateContribution("contributes[0]", c); len(probs) > 0 {
			t.Errorf("a patched fork following %q is refused:\n%s", follow, strings.Join(probs, "\n"))
		}
	}
	c := goodPatchedFork()
	c.Patches = "forks/pi/patches"
	if probs := validateContribution("contributes[0]", c); len(probs) > 0 {
		t.Errorf("a nested series directory is refused:\n%s", strings.Join(probs, "\n"))
	}
	if !c.IsPatchedFork() || !c.IsFork() {
		t.Error("a fork with patches does not read as a patched fork")
	}
	if goodFork().IsPatchedFork() {
		t.Error("a plain fork reads as a patched fork")
	}
}

func TestPatchedForkRefusals(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Contribution)
		want []string
	}{
		{"follow without patches", func(c *Contribution) { c.Patches, c.Follow = "", "head" },
			[]string{`"follow" needs "patches"`, "yolo pack update", "add a \"patches\" directory"}},
		{"a follow outside the grammar", func(c *Contribution) { c.Follow = "latest" },
			[]string{"follow", `"release"`, `"head"`}},
		{"an empty release prefix", func(c *Contribution) { c.Follow = "release:" },
			[]string{"empty prefix"}},
		{"an absolute series", func(c *Contribution) { c.Patches = "/srv/patches" },
			[]string{"must be relative"}},
		{"an escaping series", func(c *Contribution) { c.Patches = "../patches" },
			[]string{`no ".."`}},
		{"an unclean series", func(c *Contribution) { c.Patches = "patches/" },
			[]string{"clean path"}},
		{"the pack root as the series", func(c *Contribution) { c.Patches = "." },
			[]string{"the pack's root itself"}},
		{"HEAD as the ref", func(c *Contribution) { c.Source = "git+https://github.com/x/pi?ref=HEAD" },
			[]string{"HEAD is none of them", "?ref=main"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := goodPatchedFork()
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

// OFF A FORK, `patches` and `follow` are read by nothing, and the refusal names what reads them.
func TestPatchedForkFieldsOffAForkAreRefused(t *testing.T) {
	cases := []struct {
		name string
		c    Contribution
		want string
	}{
		{"patches on an npm program", Contribution{Kind: KindProgram, Bin: "pi", Via: "npm", Package: "pi",
			Patches: "patches"}, `a program delivered via "npm" does not take "patches"`},
		{"follow on an installer program", Contribution{Kind: KindProgram, Bin: "x", Via: "installer",
			URL: "https://x/i.sh", Follow: "head"}, `a program delivered via "installer" does not take "follow"`},
		{"patches on skills", Contribution{Kind: KindSkills, From: "skills", Patches: "patches"},
			`kind "skills" does not take "patches"`},
		{"follow on env", Contribution{Kind: KindEnv, Vars: map[string]string{"A": "1"}, Follow: "head"},
			`kind "env" does not take "follow"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			joined := strings.Join(validateContribution("contributes[0]", tc.c), "\n")
			if !strings.Contains(joined, tc.want) || !strings.Contains(joined, `"fork_of" and "patches" declares`) {
				t.Errorf("want a problem containing %q and naming the patched fork that reads it, got:\n%s",
					tc.want, joined)
			}
		})
	}
}

// A PLAIN FORK'S RECIPE IS BYTE FOR BYTE WHAT IT WAS before patched forks existed, so no store entry
// a plain fork built stops hitting the day this ships (PF-D7). The hashes were computed from the
// tree at 283f25c13, before this mode's code.
func TestAPlainForkRecipeIsByteForByteWhatItWas(t *testing.T) {
	for _, tc := range []struct{ got, want string }{
		{ForkRecipe(`npm ci && npm run build`, []string{".npm-global/lib/node_modules/pi-fork", ".npm-global/bin/pi"}, ""),
			"10a9afc9052ee9553d3931c39cd0741626707cc4543ec4e8387c7d0179f5956a"},
		{ForkRecipe("sh build.sh", []string{".local/bin/tool"}, "packages/coding-agent"),
			"cf26ad1756e65a24d8adef47a6bb6ab711c954ab91ceb85f10ccdd06aca61c86"},
		{ForkSourceRecipe("git+https://github.com/you/pi-fork//packages/agent?ref=main", "make", []string{".local/bin/pi"}),
			"80ed025ac1d9c970f798d6f0d7e9de161f20e7739b00097345bc5a5e9f4632d4"},
		{Install{Kind: InstallKindSource, Source: "git+https://github.com/you/pi-fork//packages/agent?ref=main",
			Build: "make", Produces: []string{".local/bin/pi"}}.SourceRecipe(),
			"80ed025ac1d9c970f798d6f0d7e9de161f20e7739b00097345bc5a5e9f4632d4"},
	} {
		if tc.got != tc.want {
			t.Errorf("a plain fork's recipe moved: %s, was %s", tc.got, tc.want)
		}
	}
}

// A PATCHED FORK'S RECIPE gains the series digest: two series never share an entry, and none equals
// the plain recipe of the same build. Its Install names no plain recipe at all, so a reader that
// missed the patched arm finds nothing to serve.
func TestAPatchedForkRecipeCarriesTheSeriesDigest(t *testing.T) {
	plain := ForkRecipe("make", []string{".local/bin/pi"}, "")
	a := PatchedForkRecipe("make", []string{".local/bin/pi"}, "", strings.Repeat("a", 64))
	b := PatchedForkRecipe("make", []string{".local/bin/pi"}, "", strings.Repeat("b", 64))
	if a == plain || b == plain || a == b {
		t.Errorf("patched recipes %s / %s against plain %s: the series digest is not in them", a, b, plain)
	}
	in := Install{Kind: InstallKindSource, Source: "git+https://h/up//sub?ref=main", Build: "make",
		Produces: []string{".local/bin/pi"}, Patches: "patches"}
	if !in.IsPatchedFork() || in.SourceRecipe() != "" {
		t.Errorf("a patched fork's Install: patched %v, plain recipe %q (want \"\")", in.IsPatchedFork(), in.SourceRecipe())
	}
	if got, want := in.PatchedSourceRecipe("d"), PatchedForkRecipe("make", []string{".local/bin/pi"}, "sub", "d"); got != want {
		t.Errorf("PatchedSourceRecipe = %s, want the subdirectory read off the source: %s", got, want)
	}
}

// THE REWRITTEN BASE'S INSTALL carries the patched fork's series directory and follow rule, so every
// reader of the program can tell a patched fork from a plain one.
func TestTheRewrittenBaseProgramProjectsAPatchedFork(t *testing.T) {
	base := goodPatchedFork()
	base.ForkOf, base.ForkedBy, base.Follow = "", "pi-fork", "head"
	in := (&Manifest{Contributes: []Contribution{base}}).InstallContributions()[0]
	if !in.IsPatchedFork() || in.Patches != "patches" || in.Follow != "head" {
		t.Errorf("the projection lost the patched fork: %+v", in)
	}
}

// THE STRICT DECODE'S UNKNOWN-FIELD REFUSAL names the next step (PF-D1): a newer yolo may read it.
func TestAnUnknownFieldNamesANewerYolo(t *testing.T) {
	_, problems := Decode([]byte(`{"name":"p","contributes":[{"kind":"program","bin":"x","via":"npm",` +
		`"package":"x","fieldFromANewerYolo":1}]}`))
	joined := strings.Join(problems, "\n")
	for _, w := range []string{`unknown field "fieldFromANewerYolo"`, "a newer yolo", "update yolo"} {
		if !strings.Contains(joined, w) {
			t.Errorf("the refusal lacks %q: %s", w, joined)
		}
	}
	// And a decode error of another kind gets no such hint.
	_, problems = Decode([]byte(`{"name":1}`))
	if strings.Contains(strings.Join(problems, "\n"), "newer yolo") {
		t.Errorf("a type error carries the unknown-field hint: %v", problems)
	}
}

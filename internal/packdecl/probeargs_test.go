package packdecl

import (
	"strings"
	"testing"
)

// probeargs_test.go covers `program`'s `probe_args` (a version probe, pi-extension-store-builds.md
// XB-D24) and `temp_caches` (the temporary-directory compile caches, XB-D52): each survives the
// projection every launcher reads, is refused on kinds no launcher serves, and each malformed
// shape that would silently do nothing is refused by name.

// TestProbeArgsAndTempCachesSurviveTheProjection: InstallContributions is the only way core reads
// a program, so a declaration that does not reach Install never reaches a launcher. For every via,
// absent stays absent, and the projection copies.
func TestProbeArgsAndTempCachesSurviveTheProjection(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"pi","via":"npm","package":"pi-coding-agent",
	   "probe_args":["--version","-v"],"temp_caches":["jiti"]},
	  {"kind":"program","bin":"claude","via":"installer","url":"https://claude.ai/install.sh",
	   "probe_args":["--version"]},
	  {"kind":"program","bin":"copilot","via":"npm","package":"@github/copilot"}]}`))
	if len(probs) != 0 {
		t.Fatalf("the declarations should decode cleanly, got: %v", probs)
	}
	byBin := map[string]Install{}
	for _, in := range m.InstallContributions() {
		byBin[in.Bin] = in
	}
	if got := strings.Join(byBin["pi"].ProbeArgs, " "); got != "--version -v" {
		t.Errorf("pi's probe arguments arrived as %q", got)
	}
	if got := strings.Join(byBin["pi"].TempCaches, " "); got != "jiti" {
		t.Errorf("pi's temporary-directory caches arrived as %q", got)
	}
	if got := strings.Join(byBin["claude"].ProbeArgs, " "); got != "--version" {
		t.Errorf("an installer-delivered program's probe must survive the projection too, got %q", got)
	}
	if byBin["copilot"].ProbeArgs != nil || byBin["copilot"].TempCaches != nil {
		t.Errorf("a program declaring neither must project neither: %+v", byBin["copilot"])
	}
	pi := byBin["pi"]
	pi.ProbeArgs[0], pi.TempCaches[0] = "mutated", "mutated"
	for _, in := range m.InstallContributions() {
		if in.Bin == "pi" && (in.ProbeArgs[0] != "--version" || in.TempCaches[0] != "jiti") {
			t.Error("InstallContributions aliased the manifest's lists")
		}
	}
}

func TestProbeArgsAndTempCachesRefusals(t *testing.T) {
	program := Contribution{Kind: KindProgram, Bin: "pi", Via: "npm", Package: "pi-coding-agent"}
	for _, tc := range []struct {
		name string
		edit func(*Contribution)
		want string
	}{
		{"probe on a non-program", func(c *Contribution) {
			*c = Contribution{Kind: KindRequires, Bin: "pi", ProbeArgs: []string{"--version"}}
		}, `does not take "probe_args"`},
		{"empty probe list", func(c *Contribution) { c.ProbeArgs = []string{} }, "makes nothing a version probe"},
		{"empty probe word", func(c *Contribution) { c.ProbeArgs = []string{""} }, "is not a word"},
		{"padded probe word", func(c *Contribution) { c.ProbeArgs = []string{" --version"} }, "is not a word"},
		{"duplicate probe", func(c *Contribution) { c.ProbeArgs = []string{"-v", "-v"} }, "is listed twice"},
		{"caches on a non-program", func(c *Contribution) {
			*c = Contribution{Kind: KindRequires, Bin: "pi", TempCaches: []string{"jiti"}}
		}, `does not take "temp_caches"`},
		{"empty cache list", func(c *Contribution) { c.TempCaches = []string{} }, "an empty list keeps nothing"},
		{"a cache path", func(c *Contribution) { c.TempCaches = []string{"jiti/x"} }, "one bare directory name"},
		{"a parent", func(c *Contribution) { c.TempCaches = []string{".."} }, "one bare directory name"},
		{"an empty cache", func(c *Contribution) { c.TempCaches = []string{""} }, "one bare directory name"},
		{"duplicate cache", func(c *Contribution) { c.TempCaches = []string{"jiti", "jiti"} }, "is listed twice"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := program
			tc.edit(&c)
			joined := strings.Join(validateContribution("contributes[0]", c), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tc.want, joined)
			}
		})
	}
	ok := program
	ok.ProbeArgs, ok.TempCaches = []string{"--version", "-v"}, []string{"jiti"}
	if probs := validateContribution("contributes[0]", ok); len(probs) != 0 {
		t.Errorf("pi's own declaration is refused: %v", probs)
	}
}

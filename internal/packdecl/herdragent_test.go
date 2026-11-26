package packdecl

import (
	"strings"
	"testing"
)

// herdragent_test.go covers `program`'s `herdr_agent`, the name herdr knows the program by.

// TestHerdrAgentSurvivesTheProjection: InstallContributions is the only way core reads a
// program, so a declaration that does not reach Install never reaches the launcher's report.
func TestHerdrAgentSurvivesTheProjection(t *testing.T) {
	m, probs := Decode([]byte(`{"name":"x","contributes":[
	  {"kind":"program","bin":"oh-omp","via":"npm","package":"x","herdr_agent":"omp"},
	  {"kind":"program","bin":"pi","via":"npm","package":"pi-coding-agent"}]}`))
	if len(probs) != 0 {
		t.Fatalf("the declarations should decode cleanly, got: %v", probs)
	}
	byBin := map[string]Install{}
	for _, in := range m.InstallContributions() {
		byBin[in.Bin] = in
	}
	if got := byBin["oh-omp"].HerdrAgent; got != "omp" {
		t.Errorf("oh-omp's herdr agent arrived as %q, want omp", got)
	}
	if got := byBin["pi"].HerdrAgent; got != "" {
		t.Errorf("a program declaring none must project none, got %q", got)
	}
}

func TestHerdrAgentRefusals(t *testing.T) {
	str := func(s string) *string { return &s }
	program := Contribution{Kind: KindProgram, Bin: "oh-omp", Via: "npm", Package: "x"}
	for _, tc := range []struct {
		name string
		edit func(*Contribution)
		want string
	}{
		{"empty", func(c *Contribution) { c.HerdrAgent = str("") }, "herdr_agent: an empty name"},
		{"whitespace", func(c *Contribution) { c.HerdrAgent = str("o mp") }, "herdr_agent"},
		{"upper case", func(c *Contribution) { c.HerdrAgent = str("OMP") }, "herdr_agent"},
		{"a path", func(c *Contribution) { c.HerdrAgent = str("bin/omp") }, "herdr_agent"},
		{"off a program", func(c *Contribution) {
			*c = Contribution{Kind: KindConfig, HerdrAgent: str("omp")}
		}, `does not take "herdr_agent"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := program
			tc.edit(&c)
			joined := strings.Join(validateContribution("contributes[0]", c), "\n")
			if !strings.Contains(joined, tc.want) {
				t.Errorf("want a problem containing %q, got:\n%s", tc.want, joined)
			}
			if !strings.Contains(joined, "omit the key") && tc.name != "off a program" {
				t.Errorf("the problem must name the fix, got:\n%s", joined)
			}
		})
	}
	ok := program
	ok.HerdrAgent = str("omp")
	if probs := validateContribution("contributes[0]", ok); len(probs) != 0 {
		t.Errorf("a valid herdr_agent was refused: %v", probs)
	}
}

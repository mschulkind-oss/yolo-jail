package packload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheDigestCoversTheWholeBuildLine: OQ-RO9 requires the digest over the whole line, never a
// prefix, so a change at the last character — where a payload can sit — moves it.
func TestTheDigestCoversTheWholeBuildLine(t *testing.T) {
	line := strings.Repeat("node -e 'x'; ", 50) + "true"
	d := BuildLineDigest(line)
	if len(d) != buildLineDigestLen {
		t.Errorf("digest %q is %d characters, want %d", d, len(d), buildLineDigestLen)
	}
	if BuildLineDigest(line[:len(line)-1]+"X") == d {
		t.Error("changing the line's last character left its digest unchanged")
	}
	if BuildLineDigest(line) != d {
		t.Error("the digest is not a function of the line")
	}
}

// forkPackForTest loads a fork pack whose program is built by build, from a temp dir.
func forkPackForTest(t *testing.T, build string) *Pack {
	t.Helper()
	root := t.TempDir()
	body := `{"contributes":[{"kind":"program","bin":"tool","via":"source","fork_of":"basepack",` +
		`"source":"git+https://example.invalid/tool.git?ref=main","build":"` + build + `","produces":[".local/bin/tool"]}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := LoadDir(root, "forkpack")
	if len(probs) > 0 {
		t.Fatalf("the fork pack fixture does not load: %v", probs)
	}
	return p
}

// TestALaunchNamesAForksBuildLineByItsDigest: the fork's claim carries its build line and key, the
// footprint's sentence keeps the line whole, and the launch's sentence names it by its digest and
// the command that prints it, with the line itself nowhere in it.
func TestALaunchNamesAForksBuildLineByItsDigest(t *testing.T) {
	const build = "make install PREFIX=$HOME/.local"
	var c *Claim
	for _, cl := range FootprintOf(forkPackForTest(t, build)).Claims {
		if cl.BuildLine != "" {
			cl := cl
			c = &cl
		}
	}
	if c == nil {
		t.Fatal("the fork's claim carries no build line")
	}
	if c.BuildLine != build || c.BuildKey != "forkpack/tool" {
		t.Errorf("claim build line %q key %q, want %q and forkpack/tool", c.BuildLine, c.BuildKey, build)
	}
	if full := c.DisclosureSentence(); !strings.Contains(full, BuildLineQuote(build)) {
		t.Errorf("the footprint's sentence lost the whole line:\n%s", full)
	}
	got := c.LaunchDisclosureSentence()
	if strings.Contains(got, build) {
		t.Errorf("the launch's sentence still prints the build line:\n%s", got)
	}
	for _, want := range []string{"built by recipe " + BuildLineDigest(build), "`yolo pack status forkpack/tool`"} {
		if !strings.Contains(got, want) {
			t.Errorf("the launch's sentence lacks %q:\n%s", want, got)
		}
	}
}

// TestASentenceThatDoesNotQuoteTheLineKeepsItWhole: the substitution falls back to the full
// sentence, never to a truncated one.
func TestASentenceThatDoesNotQuoteTheLineKeepsItWhole(t *testing.T) {
	c := Claim{Kind: "files", Target: "x", Detail: "something else", BuildLine: "make", BuildKey: "p/x"}
	if c.LaunchDisclosureSentence() != c.DisclosureSentence() {
		t.Error("a sentence that does not quote its line was rewritten")
	}
}

// TestEveryBuildClaimNamesWhatPackStatusPrints: a claim's BuildKey and BuildLine are the key and
// line `yolo pack status` resolves through Forks and PatchedTrees, for a fork, an unmodified git
// tree and an npm tree, so the digest a launch shows is always the digest status prints.
func TestEveryBuildClaimNamesWhatPackStatusPrints(t *testing.T) {
	root := t.TempDir()
	body := `{"contributes":[` +
		`{"kind":"program","bin":"tool","via":"source","fork_of":"basepack","source":"git+https://example.invalid/t.git?ref=main","build":"make","produces":[".local/bin/tool"]},` +
		`{"kind":"files","into":".tool/ext/git-ext","source":"git+https://example.invalid/e.git?ref=main","build":"npm ci"},` +
		`{"kind":"files","into":".tool/ext/npm-ext","source":"npm:npm-ext"}]}`
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := LoadDir(root, "multi")
	if len(probs) > 0 {
		t.Fatalf("fixture: %v", probs)
	}
	want := map[string]string{}
	for _, f := range append(Forks([]*Pack{p}), PatchedTrees([]*Pack{p})...) {
		want[f.Key()] = f.Build
	}
	got := map[string]string{}
	for _, c := range FootprintOf(p).Claims {
		if c.BuildKey != "" {
			got[c.BuildKey] = c.BuildLine
		}
	}
	if len(want) != 3 || len(got) != len(want) {
		t.Fatalf("claims %v, forks and trees %v: want the same three keys", got, want)
	}
	for k, line := range want {
		if got[k] != line {
			t.Errorf("key %s: claim names %q, pack status prints %q", k, got[k], line)
		}
	}
}

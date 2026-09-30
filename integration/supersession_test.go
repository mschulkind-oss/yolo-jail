package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLaunchRefusesAnUnmatchedSupersession is docs/design/reference-mismatch-diagnostics.md
// §7 step 4 in a real launch, on the design's own example: a pack superseding
// `claude-oauth-refersh` beside the shipped claude pack, whose broker serves
// `claude-oauth-refresh`. The claim retires nothing, so the launch must STOP rather than start
// a jail whose author believes the broker was turned off, and `yolo check` must predict that
// with a [FAIL] and a non-zero exit (RM-D1).
//
// Here rather than only in run.TestLaunchRefusesAnUnmatchedSupersession because the served set
// is the one the suite's CLI ships — the capability comes from the embedded claude pack, not a
// fixture — and "the launch stops" is a fact about the binary. The unit tests assert the words.
func TestLaunchRefusesAnUnmatchedSupersession(t *testing.T) {
	requireJail(t)

	pack := filepath.Join(t.TempDir(), "claude-bedrock")
	if err := os.MkdirAll(pack, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pack, "pack.json"), []byte(
		`{"name":"claude-bedrock","description":"d","supersedes":[`+
			`{"capability":"claude-oauth-refersh","because":"Bedrock overrides the OAuth path"}]}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	dir := writeProject(t, `{}`)
	packHome(t, `{"packs": ["claude", "file://`+pack+`"]}`)

	r := runYolo(t, dir, "true")
	if r.rc == 0 {
		t.Fatalf("the launch STARTED with a supersession that retires nothing; a claim that "+
			"matches no served capability is indistinguishable from one that worked\n%s", r.combined())
	}
	sentence := "supersedes capability 'claude-oauth-refersh', which NO loophole on this machine serves"
	for _, want := range []string{
		"Refusing to launch: a selected pack's `supersedes`",
		sentence,
		"did you mean 'claude-oauth-refresh'?",
		"Fix the pack",
	} {
		if !strings.Contains(r.combined(), want) {
			t.Errorf("refusal missing %q:\n%s", want, r.combined())
		}
	}
	if n := strings.Count(r.combined(), sentence); n != 1 {
		t.Errorf("the sentence appears %d times, want once (as the refusal, never first as a "+
			"warning):\n%s", n, r.combined())
	}

	c := runYoloCLI(t, dir, "check", "--no-build")
	if c.rc == 0 {
		t.Errorf("`yolo check` exited 0 on a config the launch just refused:\n%s", c.combined())
	}
	for _, want := range []string{
		"[FAIL] pack supersession matched no served capability",
		"The launch refuses this.",
	} {
		if !strings.Contains(c.combined(), want) {
			t.Errorf("`yolo check` is missing %q:\n%s", want, c.combined())
		}
	}
}

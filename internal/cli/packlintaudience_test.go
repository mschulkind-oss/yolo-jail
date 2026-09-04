package cli

// packlintaudience_test.go is R5 applied literally — "move the gate, do not lower the
// severity" (briefing-audiences.md §4.3): `yolo pack lint` must NOT refuse an unknown agent
// name.
//
// This is a NEGATIVE, and it is the kind of test that only exists because the temptation is
// real: lint is where an author looks, so a validator that can name a bad string wants to
// refuse it there. It cannot. `yolo pack lint` takes a single pack ROOT with no config
// (`yolo pack --help`), so it does not know which packs are enabled and therefore cannot tell
// `agents: ["cloude"]` from `agents: ["codex"]` in a jail that has codex. Refusing on the
// wider "does this name exist anywhere" set is exactly the second, laxer tier P3 rules out.
//
// The severity lives upstream, at the two gates that hold the enabled set — the launch
// pre-flight (internal/cli/run/packagentaudience_test.go) and `yolo host apply`
// (applyhostaudience_test.go) — where the user has every remedy.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPackLintDoesNotRefuseAnUnknownAgentName pins the negative over the WORST case for it: a
// name that is not merely unselected but almost certainly a typo, so nothing about the fixture
// makes refusing look unreasonable.
func TestPackLintDoesNotRefuseAnUnknownAgentName(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "prose"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"briefing","from":"prose/x.md","agents":["cloude"]}]}`)
	writeFile(t, filepath.Join(dir, "prose", "x.md"), "House rules.\n")

	var out, errw bytes.Buffer
	rc := packMain([]string{"lint", dir}, &out, &errw, false)
	report := out.String() + errw.String()
	if rc != 0 {
		t.Fatalf("`pack lint` refused an agent name it cannot possibly adjudicate — it takes a "+
			"pack root with NO config, so it does not know which packs are enabled, and "+
			"refusing here would either be wrong for a legitimate name or would need a second, "+
			"laxer vocabulary that P3 rules out. rc=%d\n%s", rc, report)
	}
	// And it must not sneak the refusal in as a warning either: the whole point of R5 is that
	// this notch cannot answer the question, so it does not raise it.
	for _, forbidden := range []string{"no pack in `packs` provides", "did you mean"} {
		if strings.Contains(report, forbidden) {
			t.Errorf("`pack lint` printed the enabled-set diagnostic %q, which it has no basis "+
				"for:\n%s", forbidden, report)
		}
	}
}

// The manifest-level refusals lint DOES own are unaffected, so this is not "lint ignores the
// fields": `agents` beside `into` is decidable from the manifest ALONE and stays fatal here.
func TestPackLintStillRefusesAgentsBesideInto(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pack.json"),
		`{"name":"house","description":"h","contributes":[`+
			`{"kind":"briefing","into":".claude/CLAUDE.md","agents":["claude"]}]}`)
	writeFile(t, filepath.Join(dir, "AGENTS.md"), "House rules.\n")

	var out, errw bytes.Buffer
	if rc := packMain([]string{"lint", dir}, &out, &errw, false); rc == 0 {
		t.Fatalf("`into` and `agents` are two answers to one question and lint can see both "+
			"without any config, so it must refuse:\n%s", out.String()+errw.String())
	}
	if report := out.String() + errw.String(); !strings.Contains(report, "not both") {
		t.Errorf("the refusal must say what the rule is; got:\n%s", report)
	}
}

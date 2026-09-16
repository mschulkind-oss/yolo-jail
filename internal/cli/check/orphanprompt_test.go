package check

// The orphan-cleanup prompt, which was unreachable for its whole life: nothing in
// internal/cli assigned Options.Stdin, so the nil branch answered "N" on every real
// invocation while the question printed anyway, and the report then prescribed the
// command it had just declined to run. G28 in docs/plans/setup-support-gaps.md.
//
// Fixing the wiring alone would have been a trade rather than a fix, which is what
// these tests are really pinning: with Stdin assigned unconditionally, a piped stdin
// (`yolo check < file`, any CI job) becomes an answer nobody typed, on a prompt whose
// yes branch runs `rm -f` against containers. So the question is asked only on a
// terminal, and a pipe gets a statement plus the command instead.

import (
	"bytes"
	"strings"
	"testing"
)

// promptOpts is an Options with the TTY predicate and Stdin under the test's control,
// which are the two inputs the decision reads.
func promptOpts(stdin string, isTTY bool) (*Options, *reporter, *bytes.Buffer) {
	var out bytes.Buffer
	o := &Options{
		IsTTYStdout: func() bool { return isTTY },
	}
	if stdin != "" {
		o.Stdin = strings.NewReader(stdin)
	}
	return o, newReporter(&out, false), &out
}

func TestOrphanPromptAcceptsYesOnATerminal(t *testing.T) {
	for _, answer := range []string{"y\n", "yes\n", "Y\n", "YES\n"} {
		o, r, out := promptOpts(answer, true)
		if !o.orphanCleanupPrompt(r, 2) {
			t.Errorf("answer %q was not read as yes; output:\n%s", answer, out.String())
		}
		if !strings.Contains(out.String(), "[y/N]") {
			t.Errorf("answer %q: no question was asked on a terminal:\n%s", answer, out.String())
		}
	}
}

func TestOrphanPromptDeclinesAnythingElseOnATerminal(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", "later\n"} {
		o, r, out := promptOpts(answer, true)
		if o.orphanCleanupPrompt(r, 1) {
			t.Errorf("answer %q was read as yes; output:\n%s", answer, out.String())
		}
	}
}

// The half that makes assigning Stdin safe. A redirected stdin holding "y" must not
// stop containers: nobody saw a prompt, so there is no answer to honor.
func TestOrphanPromptNeverReadsAPipeAsYes(t *testing.T) {
	o, r, out := promptOpts("y\n", false)

	if o.orphanCleanupPrompt(r, 3) {
		t.Fatal("a piped \"y\" was honored with no terminal to have shown the prompt on — " +
			"this is the implicit yes that assigning Options.Stdin would otherwise have created")
	}
	got := out.String()
	if strings.Contains(got, "[y/N]") {
		t.Errorf("a question was asked where no answer can be read:\n%s", got)
	}
	// Silence would be its own defect: the orphans are still there and the user needs
	// the command that removes them.
	if !strings.Contains(got, "3 orphaned jail(s)") {
		t.Errorf("the non-terminal line does not say how many orphans are here:\n%s", got)
	}
	if !strings.Contains(got, "yolo prune --apply") {
		t.Errorf("the non-terminal line does not name the command that removes them:\n%s", got)
	}
}

// A nil Stdin is the state the CLI used to be in. It must behave like the pipe — one
// factual line, no question — rather than printing a prompt it cannot read, because
// that is the exact output the audit found.
func TestOrphanPromptWithNoStdinStatesRatherThanAsks(t *testing.T) {
	o, r, out := promptOpts("", true)

	if o.orphanCleanupPrompt(r, 1) {
		t.Fatal("a nil Stdin was read as yes")
	}
	if strings.Contains(out.String(), "[y/N]") {
		t.Errorf("a prompt was printed with no Stdin to answer it:\n%s", out.String())
	}
}

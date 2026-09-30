package installerbody

import (
	"strings"
	"testing"
)

// Cases is the rule's table (the package comment), each cell one clause or one edge of it.
// Exported to the parity test in internal/entrypoint through Fixtures, so the shell check the
// jail's launcher makes is run against exactly these bodies too.
func TestClassifyFollowsTheRule(t *testing.T) {
	for _, f := range Fixtures() {
		t.Run(f.Name, func(t *testing.T) {
			got, err := Classify(strings.NewReader(f.Body))
			if err != nil {
				t.Fatal(err)
			}
			if got != f.Want {
				t.Errorf("Classify = %s, want %s", got, f.Want)
			}
		})
	}
}

// Every refused kind has a reason and advice, and a script has no reason.
func TestEveryRefusalSaysWhyAndWhatToDo(t *testing.T) {
	for _, k := range []Kind{Markup, Binary, NonText} {
		if Why(k) == "" || Advice(k) == "" {
			t.Errorf("%s has no reason or no advice", k)
		}
	}
	if Why(Script) != "" {
		t.Errorf("a script must not carry a refusal reason: %q", Why(Script))
	}
}

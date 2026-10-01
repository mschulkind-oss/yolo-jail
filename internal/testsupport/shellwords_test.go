package testsupport

import (
	"slices"
	"testing"
)

func TestShellWordsSplitsTheWayAShellDoes(t *testing.T) {
	for _, tc := range []struct {
		line string
		want []string
	}{
		{"mv /a/b /c/", []string{"mv", "/a/b", "/c/"}},
		{"mv '/My Projects/b' /c/", []string{"mv", "/My Projects/b", "/c/"}},
		{"rm /My Projects/b", []string{"rm", "/My", "Projects/b"}},
	} {
		if got := ShellWords(t, tc.line); !slices.Equal(got, tc.want) {
			t.Errorf("ShellWords(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
	t.Setenv("HOME", "/Users/Jane Doe")
	if got := ShellWords(t, `rm ~/'Library/Application Support/x'`); !slices.Equal(got,
		[]string{"rm", "/Users/Jane Doe/Library/Application Support/x"}) {
		t.Errorf("a tilde before a quoted rest did not expand to one word: %q", got)
	}
}

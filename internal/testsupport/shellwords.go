package testsupport

import (
	"os/exec"
	"strings"
	"testing"
)

// ShellWords returns the words /bin/sh splits one simple command line into, the way a reader
// who pastes a printed remedy gets them, and runs none of them: the line becomes the operands
// of `set --`.
//
// It exists for the test that asserts a printed command names a path. Compared as text, such
// a test passes a remedy that left a path with a space in it bare, which a shell then reads as
// two arguments; and a test cutting the line on spaces cannot tell a quoted path from a bare
// one. Only a shell can say how many words a line is.
//
// The line must be one simple command, with no `;`, `&&` or `|`. It is still shell source, so
// a `$(…)` in it would run: hand it only lines a test built from paths it chose.
func ShellWords(t testing.TB, line string) []string {
	t.Helper()
	out, err := exec.Command("/bin/sh", "-c", "set -- "+line+"\nprintf '%s\\0' \"$@\"").Output()
	if err != nil {
		t.Fatalf("a shell cannot read %q: %v", line, err)
	}
	return strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
}

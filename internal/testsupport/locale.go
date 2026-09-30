package testsupport

import (
	"os"
	"testing"
)

// UnsetLCAll removes LC_ALL from the process environment until t ends, for a test that
// compares a child shell's stderr, or its combined output, byte for byte.
//
// When LC_ALL names a locale the machine has not generated, bash and sh print "warning:
// setlocale: LC_ALL: cannot change locale" on stderr before they run anything. That is an
// ordinary machine: ssh forwards a Mac's LC_ALL=en_US.UTF-8 (SendEnv LC_*) to a minimal
// Linux box or container that has only C and C.UTF-8. Measured on 2026-09-30 with bash
// 5: only LC_ALL warns. LANG, LC_CTYPE, LC_MESSAGES and the other LC_* variables naming
// the same missing locale are silent, and so is an empty LC_ALL.
//
// It unsets rather than setting C because unset is the state the tests' expected output
// was written against. It uses t.Setenv to restore the old value, so a test that calls it
// cannot also call t.Parallel.
func UnsetLCAll(t testing.TB) {
	t.Helper()
	t.Setenv("LC_ALL", "")
	_ = os.Unsetenv("LC_ALL")
}

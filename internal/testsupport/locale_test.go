package testsupport

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
)

// TestUnsetLCAllSilencesAShellsLocaleWarning sets the machine fact itself: an LC_ALL
// naming a locale no machine has. The bare shell must print its setlocale warning, or
// this test proves nothing on this platform; after UnsetLCAll it must print nothing, and
// the old value must be back once the test ends.
func TestUnsetLCAllSilencesAShellsLocaleWarning(t *testing.T) {
	const missing = "xx_XX.UTF-8"
	t.Setenv("LC_ALL", missing)
	stderrOf := func() string {
		var stderr bytes.Buffer
		cmd := exec.Command("/bin/sh", "-c", "exit 0")
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		return stderr.String()
	}
	if stderrOf() == "" {
		t.Skip("this platform's /bin/sh does not warn about a missing LC_ALL locale")
	}
	t.Run("unset", func(t *testing.T) {
		UnsetLCAll(t)
		if v, ok := os.LookupEnv("LC_ALL"); ok {
			t.Errorf("LC_ALL = %q after UnsetLCAll, want it unset", v)
		}
		if got := stderrOf(); got != "" {
			t.Errorf("the shell still wrote to stderr: %q", got)
		}
	})
	if got := os.Getenv("LC_ALL"); got != missing {
		t.Errorf("LC_ALL = %q after the subtest, want %q restored", got, missing)
	}
}

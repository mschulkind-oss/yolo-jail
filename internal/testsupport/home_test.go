package testsupport

import (
	"os"
	"testing"
)

// TestIsolateHomeReplacesTheStartingHomeAndRestoresIt: the binary's home becomes a fresh
// empty directory, marked for helper children; a second call (what a helper child's TestMain
// makes) keeps the home it inherited; the cleanup restores the starting home and removes the
// directory.
func TestIsolateHomeReplacesTheStartingHomeAndRestoresIt(t *testing.T) {
	const startHome = "/the/real/home"
	t.Setenv("HOME", startHome)
	t.Setenv(homeInheritEnv, "")

	cleanup := IsolateHome()
	home := os.Getenv("HOME")
	if home == startHome || home == "" {
		t.Fatalf("HOME = %q after IsolateHome, want a fresh directory", home)
	}
	if entries, err := os.ReadDir(home); err != nil || len(entries) != 0 {
		t.Errorf("the isolated home %s is not an empty directory (%d entries, err=%v)", home, len(entries), err)
	}
	if got := os.Getenv(homeInheritEnv); got != home {
		t.Errorf("%s = %q, want the isolated home %q for helper children", homeInheritEnv, got, home)
	}

	t.Setenv("HOME", "/a/test/set/this")
	inner := IsolateHome()
	if got := os.Getenv("HOME"); got != "/a/test/set/this" {
		t.Errorf("a helper child's IsolateHome replaced the HOME it inherited with %q", got)
	}
	inner()
	t.Setenv("HOME", home)

	cleanup()
	if got := os.Getenv("HOME"); got != startHome {
		t.Errorf("HOME = %q after the cleanup, want the starting %q", got, startHome)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Errorf("the cleanup left %s behind (err=%v)", home, err)
	}
}

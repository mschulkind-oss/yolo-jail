package testsupport

import (
	"os"
)

// IsolateHome points HOME at a fresh empty directory for the whole test binary and returns
// the cleanup that removes it and restores HOME. Call it from the TestMain of a package
// whose tests reach code that writes under the home (yolo's state dir, a daemon's logs),
// with the cleanup after m.Run.
//
// A test that sets HOME itself still gets the home it set: this only replaces the one the
// binary was started with, the developer's real home, so that a test that forgets its own
// writes into a directory nobody keeps rather than into ~/.local/share/yolo-jail.
//
// A helper child (a test that re-execs this binary) keeps the HOME it was handed, which is
// whatever its parent test had set: the parent's directory is marked in the environment,
// as IsolateHostSingletons marks its own.
func IsolateHome() (cleanup func()) {
	if os.Getenv(homeInheritEnv) != "" {
		return func() {}
	}
	dir, err := os.MkdirTemp("", "yolo-test-home-")
	if err != nil {
		panic("testsupport: creating a private home: " + err.Error())
	}
	prev, had := os.LookupEnv("HOME")
	_ = os.Setenv("HOME", dir)
	_ = os.Setenv(homeInheritEnv, dir)
	return func() {
		if had {
			_ = os.Setenv("HOME", prev)
		} else {
			_ = os.Unsetenv("HOME")
		}
		_ = os.Unsetenv(homeInheritEnv)
		_ = os.RemoveAll(dir)
	}
}

// IsolatedHome is the home IsolateHome made for this binary, or "" when it made none, so a
// package can pin that its TestMain isolates.
func IsolatedHome() string { return os.Getenv(homeInheritEnv) }

// homeInheritEnv marks a binary whose home IsolateHome already replaced, so a helper child
// it re-execs keeps the HOME it inherits. Read here and nowhere else, never by production
// code, so it is not a yolo dial.
const homeInheritEnv = "TESTSUPPORT_ISOLATED_HOME"

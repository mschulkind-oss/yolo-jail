package testsupport

import (
	"os"
	"path/filepath"
)

// StubPodman puts a `podman` that only refuses at the front of PATH for the whole test binary
// and returns the cleanup that restores PATH and removes it. Call it from the TestMain of a
// package whose tests reach the runtime code (`podman info`, `ps`, `rm`, `images`), after any
// child dispatch, with the cleanup after m.Run.
//
// A short test must never run the machine's podman. A rootless `podman` leaves its pause
// process (catatonit) running, which is a process of the run that outlives it, and the
// TMPDIR leak gate (scripts/tmp-leak-gate.py) fails `just test-fast` naming it (CI run
// 45452376220). It also makes the answer the machine's rather than the fixture's. A test that
// wants podman to answer puts a fake of its own first on PATH, which this one sits behind.
//
// The stub is a podman that holds nothing: `ps` and `images` answer with an empty list,
// `container exists` and `rm` say there is no such container (status 1), and anything else,
// `info` included, fails with 125, podman's own "the runtime failed" status. That is what an
// idle podman says on a CI runner, so a keeper that asks whether a jail still runs is told it
// does not.
func StubPodman() (cleanup func()) {
	dir, err := os.MkdirTemp("", "yolo-test-podman-")
	if err != nil {
		panic("testsupport: creating a stub podman dir: " + err.Error())
	}
	script := `#!/bin/sh
case "$1" in
ps|images) exit 0 ;;
container|rm) echo "podman: no such container (testsupport.StubPodman)" >&2; exit 1 ;;
esac
echo "podman: refused by the test suite (testsupport.StubPodman)" >&2
exit 125
`
	if err := os.WriteFile(filepath.Join(dir, "podman"), []byte(script), 0o755); err != nil {
		panic("testsupport: writing a stub podman: " + err.Error())
	}
	prev, had := os.LookupEnv("PATH")
	if had {
		_ = os.Setenv("PATH", dir+string(os.PathListSeparator)+prev)
	} else {
		_ = os.Setenv("PATH", dir)
	}
	return func() {
		if had {
			_ = os.Setenv("PATH", prev)
		} else {
			_ = os.Unsetenv("PATH")
		}
		_ = os.RemoveAll(dir)
	}
}

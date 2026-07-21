package entrypoint

import "syscall"

// sysExec wraps syscall.Exec (the execve(2) family), replacing the current
// process image with argv0/argv/env. On success it never returns; on failure it
// returns the syscall error. syscall.Exec is
// available on every Unix target (linux + darwin), so no build-tag split is
// needed: the entrypoint only ever RUNS in-jail (Linux), but this compiles for
// the darwin cross-build check too.
func sysExec(argv0 string, argv []string, env []string) error {
	return syscall.Exec(argv0, argv, env)
}

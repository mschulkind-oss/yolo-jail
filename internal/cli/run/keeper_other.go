//go:build !linux

package run

import "syscall"

// keeperSelfExe is "" off Linux: there is no /proc/self/exe, so the keeper is spawned from
// os.Executable()'s path (execx.SelfExecArgv), and a keeper of another build refuses the launch's
// plan by its build stamp instead (docs/design/jail-lifetime-last-session-wins.md JL-D5, JL-D20).
func keeperSelfExe() string { return "" }

// setChildDeathSignal does nothing off Linux, which has no parent-death signal: a keeper SIGKILLed
// on a Mac leaves the fronted daemons and forwards it started running, unreachable once their
// fronts and socket paths are gone, until something ends them. Owed to the Mac backends' step
// (§7 step 4; JL-D60).
func setChildDeathSignal(*syscall.SysProcAttr) {}

// moveKeeperIntoScope does nothing off Linux: there is no systemd, so the keeper has the session of
// its own its spawn gave it, and no scope.
func (o *Options) moveKeeperIntoScope(string) (scope, line string) {
	return "", "keeper: no systemd on this platform, so it runs in a session of its own and no scope of its own"
}

//go:build linux

package run

import (
	"strconv"
	"strings"
	"syscall"
	"time"
)

// keeperSelfExe is what the keeper is spawned from, and what it spawns its own later self-execs
// from: the running binary's inode, which /proc/self/exe names even after `just install` has
// replaced the file at its path (docs/design/jail-lifetime-last-session-wins.md JL-D5). Not
// execx.SelfExecArgv, which re-resolves os.Executable() to a path, and a keeper's scratch remover
// runs at a teardown days after the launch.
func keeperSelfExe() string { return "/proc/self/exe" }

// setChildDeathSignal gives a child the keeper starts an end of its own: the kernel sends it SIGTERM
// when the keeper dies, however it dies (JL-D32). Every socat forward and fronted daemon of a jail
// would otherwise run on unowned after a SIGKILLed keeper, reached by no pane close and killed by no
// reaper. The signal follows the THREAD that forked the child; the Go runtime ends a thread only
// when a goroutine locked to it exits, which nothing in a keeper does.
func setChildDeathSignal(attr *syscall.SysProcAttr) { attr.Pdeathsig = syscall.SIGTERM }

// keeperScopeTimeout bounds the scope move's one D-Bus call.
const keeperScopeTimeout = 5 * time.Second

// moveKeeperIntoScope moves this process into a transient systemd user scope of its own, before it
// starts anything, so the terminal's scope going (a logout under KillUserProcesses, systemd-oomd
// acting on the terminal's cgroup) does not take the jail's host services with it (§9.1). Every
// child started afterwards lands in the scope too.
//
// THROUGH busctl, systemd's own D-Bus client: StartTransientUnit with this pid in the unit's PIDs,
// the call podman makes for conmon. Not `systemd-run --user --scope`, which would exec the keeper
// in its own process and stop it being the launch's child. Where there is no user bus, no busctl,
// or the call fails, the keeper keeps the session of its own that its spawn gave it, and the
// returned line, which the keeper logs, says which (JL-D61).
func (o *Options) moveKeeperIntoScope(cname string) (scope, line string) {
	if o.inContainer() {
		return "", "keeper: no systemd user bus inside a container, so it runs in a session of its own and no scope of its own"
	}
	busctl, ok := o.LookPath("busctl")
	if !ok {
		return "", "keeper: busctl is not on PATH, so it runs in a session of its own and no scope of its own"
	}
	pid := strconv.Itoa(o.Getpid())
	unit := "yolo-jail-keeper-" + cname + "-" + pid + ".scope"
	res := o.Exec([]string{busctl, "--user", "call", "org.freedesktop.systemd1",
		"/org/freedesktop/systemd1", "org.freedesktop.systemd1.Manager", "StartTransientUnit",
		"ssa(sv)a(sa(sv))", unit, "fail", "2",
		"PIDs", "au", "1", pid,
		"Description", "s", "yolo jail keeper for " + cname,
		"0"}, "", nil, keeperScopeTimeout)
	if !res.Ran || res.Timeout || res.RC != 0 {
		why := strings.TrimSpace(res.Stderr)
		if why == "" {
			why = execFailure(res)
		}
		return "", "keeper: could not move into a systemd user scope (" + why + "), so it runs in a session of its own and no scope of its own"
	}
	return unit, "keeper: moved into the systemd user scope " + unit
}

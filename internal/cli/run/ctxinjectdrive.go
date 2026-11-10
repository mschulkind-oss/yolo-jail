package run

// ctxinjectdrive.go is the HOST DRIVER of live mount injection (docs/design/live-config-apply.md
// §5, the plan's Task C): it decides whether a mount change can reach a running jail, and runs the
// injector, `yolo internal ctx-attach` (internalctxattach.go, internal/ctxinject), against it.
//
// WHERE THE INJECTOR RUNS (§5.3), decided from `podman info` BEFORE anything runs and never by
// retrying a failure, as the image copy decides its own prefix:
//
//   - rootless podman: under `podman unshare`, in podman's rootless user and mount namespaces,
//     where the host source is visible and from which the jail's user namespace is a child;
//   - a nested launch (podman forced onto --userns host) and a rootful podman yolo drives as
//     root: directly;
//   - a rootful podman driven by anyone else: not at all. The command never asks for sudo, so
//     the change waits for the next restart;
//   - a macOS Podman Machine and Apple Container: not built (§5.3), at next restart.

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/ctxinject"
)

// ctxInjectDeadline bounds one injector run, unshare prefix included.
const ctxInjectDeadline = 10 * time.Second

// ctxAttachVerbName is the injector's verb (internal/cli's ctxAttachVerb, which this package
// cannot import).
const ctxAttachVerbName = "ctx-attach"

// ctxGeteuid is os.Geteuid, a variable for the rootful branch's tests.
var ctxGeteuid = os.Geteuid

// ctxProcessIdentitySeam captures the target's process start time and mount-namespace identity
// after runtime inspection, before the injector can mutate either namespace.
var ctxProcessIdentitySeam = ctxinject.ReadProcessIdentity

// ctxInjectBranch is where the injector runs for rt: unshare is true for the `podman unshare`
// prefix. reason is non-empty when it cannot run at all, in the outcome line's words.
func (o *Options) ctxInjectBranch(rt string) (unshare bool, reason string) {
	return o.ctxInjectBranchBy(rt, time.Time{})
}

// stepTimeout is limit, shortened to what is left before deadline; a zero deadline is no step
// deadline. ok is false once the deadline has passed, when nothing more may run.
func stepTimeout(limit time.Duration, deadline time.Time) (time.Duration, bool) {
	if deadline.IsZero() {
		return limit, true
	}
	left := time.Until(deadline)
	if left <= 0 {
		return 0, false
	}
	return min(limit, left), true
}

// ctxInjectBranchBy is ctxInjectBranch with its one podman call bounded by deadline as well.
func (o *Options) ctxInjectBranchBy(rt string, deadline time.Time) (unshare bool, reason string) {
	switch {
	case rt == "container": // parity: NotApplicable — Apple Container has no verb to add a share to a running VM (design §5.3, M4)
		return false, "Apple Container shares are fixed when its VM boots"
	case rt != "podman": // parity: NotApplicable — macos-user has no mounts, only links a new session re-stages (design §5.3)
		return false, "a macos-user session has no mounts to add to"
	case o.IsMacOS:
		return false, "the Podman Machine path is not built yet"
	case o.inContainer():
		// A nested podman runs on --userns host: the injector and the jail share one user
		// namespace, so the verb runs directly. It proves nothing about rootless (AGENTS.md).
		return false, ""
	}
	if o.podmanFacts == nil {
		if o.Exec == nil {
			return false, "podman info could not be run"
		}
		timeout, ok := stepTimeout(ctxInjectDeadline, deadline)
		if !ok {
			return false, "the mount step ran out of time before podman info"
		}
		res := o.Exec([]string{"podman", "info", "--format", "json"}, "", nil, timeout)
		if !res.Ran || res.RC != 0 || res.Timeout {
			return false, "podman info did not answer" + firstLineSuffix(res.Stderr)
		}
		o.acceptPodmanFacts(res.Stdout)
	}
	if !o.podmanFacts.parsed || o.podmanFacts.info.Host.Security.Rootless == nil {
		return false, "podman info did not say whether this podman is rootless"
	}
	if *o.podmanFacts.info.Host.Security.Rootless {
		return true, ""
	}
	if ctxGeteuid() != 0 {
		return false, "this podman is rootful and yolo is not running as root"
	}
	return false, ""
}

// ctxMountsLive reports whether config mount changes can be applied to the running jail cname
// now, and the outcome line's reason when not. envLines is the jail's inspected env.
func (o *Options) ctxMountsLive(rt, cname string, envLines []string) (ok bool, reason string) {
	// The backend first: every container backend freezes the same tag list (contracttags.go).
	if rt != "podman" || o.IsMacOS { // parity: Warned — every other backend's mount change waits for a restart, with ctxInjectBranch's reason as the outcome line
		_, reason := o.ctxInjectBranch(rt)
		return false, reason
	}
	tags, known := jailContractTags(envLines)
	if !known {
		return false, "yolo could not read this jail's environment"
	}
	if !tags[contractLiveCtxMounts] {
		return false, "this jail started before live mounts existed"
	}
	if ctxMountRecordInvalidated(cname, envLines) {
		return false, "the host's live-mount record is missing or does not match this jail's launch token"
	}
	if _, ok := readCtxMountRecord(cname, envLines); !ok {
		return false, "this jail started before live mounts existed"
	}
	if _, reason := o.ctxInjectBranch(rt); reason != "" {
		return false, reason
	}
	return true, ""
}

// injectCtxMount mounts m into the running jail cname. Every call it makes ends by deadline (a
// zero deadline leaves each call its own bound alone), so nothing it starts outlives the step.
// An add is idempotent: the injector succeeds when the destination already holds this source in
// this mode, so a mount a step made before it was cut off is recorded by the next apply.
func (o *Options) injectCtxMount(rt, cname string, m configCtxMount, deadline time.Time) error {
	args := []string{"--source", m.source, "--dest", m.dest}
	if m.rw {
		args = append(args, "--rw")
	}
	return o.runCtxAttach(rt, cname, args, deadline)
}

// removeCtxMount unmounts e from the running jail cname and removes its mountpoint, by deadline.
func (o *Options) removeCtxMount(rt, cname string, e ctxMountEntry, deadline time.Time) error {
	return o.runCtxAttach(rt, cname, []string{"--dest", e.Dest, "--remove"}, deadline)
}

// ctxPidInspectTimeout bounds the pid inspect, before the verb and after it.
const ctxPidInspectTimeout = 3 * time.Second

// runCtxAttach runs the injector verb against cname's main process with args, every call bounded
// by deadline. It compares the runtime PID around a process-start/mount-namespace snapshot; the
// injector pins a pidfd and namespace fd and verifies that exact snapshot before any mutation.
func (o *Options) runCtxAttach(rt, cname string, args []string, deadline time.Time) error {
	unshare, reason := o.ctxInjectBranchBy(rt, deadline)
	if reason != "" {
		return errors.New(reason)
	}
	pid, err := o.jailMainPid(rt, cname, deadline)
	if err != nil {
		return err
	}
	identity, err := ctxProcessIdentitySeam(pid)
	if err != nil {
		return fmt.Errorf("could not pin this jail's process identity before mount injection: %w", err)
	}
	confirmedPID, err := o.jailMainPid(rt, cname, deadline)
	if err != nil {
		return fmt.Errorf("could not confirm this jail's process before mount injection: %w", err)
	}
	if confirmedPID != pid {
		return fmt.Errorf("this jail's process changed before mount injection (pid %d, now %d)", pid, confirmedPID)
	}
	// The injector child runs from THIS process's own pin: `yolo config apply` is no launch, so it
	// resolves one from the bundle it was started from, once (PH-D7, ensureHelperPin).
	if err := o.ensureHelperPin(); err != nil {
		return err
	}
	argv, err := o.selfExecArgv(append([]string{"yolo", "internal", ctxAttachVerbName,
		"--pid", strconv.Itoa(pid),
		"--start-time", strconv.FormatUint(identity.StartTime, 10),
		"--mount-ns-device", strconv.FormatUint(identity.MountNSDevice, 10),
		"--mount-ns-inode", strconv.FormatUint(identity.MountNSInode, 10)}, args...))
	if err != nil {
		return fmt.Errorf("the mount step cannot run: %w", err)
	}
	if unshare {
		argv = append([]string{"podman", "unshare", "--"}, argv...)
	}
	// The pid check after the verb keeps its own share of the step, so a verb that used the rest
	// of it cannot leave a mount that landed reported as one that did not.
	// A quarter of what is left, at most the inspect's own bound.
	verbDeadline := deadline
	if !deadline.IsZero() {
		verbDeadline = deadline.Add(-min(ctxPidInspectTimeout, time.Until(deadline)/4))
	}
	timeout, ok := stepTimeout(ctxInjectDeadline, verbDeadline)
	if !ok {
		return errors.New("the mount step ran out of time before the injector could run")
	}
	res := o.Exec(argv, "", nil, timeout)
	switch {
	case !res.Ran:
		return fmt.Errorf("could not run %s", argv[0])
	case res.Timeout:
		return fmt.Errorf("the mount step did not finish within %s", timeout.Round(time.Millisecond))
	case res.RC != 0:
		msg := strings.TrimSpace(res.Stderr)
		msg = strings.TrimPrefix(msg, "yolo internal "+ctxAttachVerbName+": ")
		if msg == "" {
			msg = "exit " + strconv.Itoa(res.RC)
		}
		return errors.New(msg)
	}
	after, err := o.jailMainPid(rt, cname, deadline)
	if err != nil {
		return fmt.Errorf("the mount step ran, but yolo could not confirm it reached this jail: %w", err)
	}
	if after != pid {
		return fmt.Errorf("this jail's process changed while the mount step ran (pid %d, now %d), so "+
			"the step may have reached another process", pid, after)
	}
	return nil
}

// jailMainPid is the host pid of cname's main process, the process whose mount namespace is the
// jail's, asked by deadline.
func (o *Options) jailMainPid(rt, cname string, deadline time.Time) (int, error) {
	if o.Exec == nil {
		return 0, errors.New("could not ask " + rt + " for the jail's pid")
	}
	timeout, ok := stepTimeout(ctxPidInspectTimeout, deadline)
	if !ok {
		return 0, errors.New("the mount step ran out of time before " + rt + " inspect")
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{.State.Pid}}", cname}, "", nil, timeout)
	if !res.Ran || res.RC != 0 || res.Timeout {
		return 0, fmt.Errorf("%s inspect %s did not answer%s", rt, cname, firstLineSuffix(res.Stderr))
	}
	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil || pid <= 0 {
		return 0, fmt.Errorf("%s reports no running process for %s (pid %q)", rt, cname, strings.TrimSpace(res.Stdout))
	}
	return pid, nil
}

// firstLineSuffix is ": <first line of s>", or "" for an empty s.
func firstLineSuffix(s string) string {
	if l := firstLine(s); l != "" {
		return ": " + l
	}
	return ""
}

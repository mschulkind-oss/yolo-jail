package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// iopriority.go is the launcher's half of `resources.io.priority`: the environment value the
// entrypoint applies, and the one line a launch prints where the declaration does nothing
// (docs/design/io-priority.md §5.2). The decision itself is appliedIOPriority
// (backendcaps.go), shared with the briefing.
//
// WHY A LINE AT ALL. P1 of docs/design/declaration-parity.md: "the one state that is never
// legal is accepting a declaration and doing nothing". A declared priority on a disk whose
// scheduler ignores it, or on a backend that cannot pass it, is exactly that state, so the
// launch says so — one disclosure line, never a refusal (OQ-DP5 (a)). And like every
// disclosure it has no switch: a launch has no quiet mode (OQ-RO3).

// ioPriorityEnvArgs spells the decision for the container: `-e YOLO_IO_PRIORITY=<value>`
// when a priority is passed, nothing otherwise. The entrypoint reads only this, never the
// config, so an attach applies the value the jail was launched with.
func ioPriorityEnvArgs(p ioprio.Priority) []string {
	if !p.Declared() {
		return nil
	}
	return []string{"-e", ioprio.EnvVar + "=" + string(p)}
}

// noteIOPriority prints the launch's one line about a declared priority, or nothing.
//
//   - Apple Container and podman on macOS: the Warned line. The value was not passed, and
//     could do nothing if it were: workspace I/O crosses VirtioFS.
//   - podman on Linux (nested included): the parent disks of the workspace are graded with
//     the resolver `yolo check` uses, and each disk whose scheduler ignores the value is
//     named. A disk that honors it, or one that cannot be resolved (ZFS, NFS, an unreadable
//     sysfs), prints nothing: the first needs no line, and the second proves nothing, which
//     `yolo check` says in its own row.
//   - macos-user never reaches here: that arm of Run returns before the container path, and
//     its disk policy and the one warning it can print are the orchestrator's (IO-D13).
func (o *Options) noteIOPriority(rt string, p ioprio.Priority) {
	if !p.Declared() {
		return
	}
	out := o.pr(o.Stderr)
	switch {
	case rt == "container": // parity: Warned — the workspace reaches the Mac over VirtioFS, which carries no I/O priority; this is the line
		out.print(ioPriorityWarnedLine(p, "Apple Container"))
	case rt == "podman" && o.IsMacOS: // parity: Warned — the same VirtioFS crossing, through podman's VM; this is the line
		out.print(ioPriorityWarnedLine(p, "podman on macOS"))
	case rt == "podman": // parity: Honored — passed to the entrypoint; the line names each disk under the workspace that ignores it
		res := ioprio.Resolve(o.ioSysRoot, o.Workspace)
		if bad := ioprio.NoEffect(p, res); len(bad) > 0 {
			out.print(`[yellow]Warning: resources.io.priority "` + string(p) + `" has no effect on ` +
				ioprio.DiskList(bad) + ", the disk under " + res.Path + "[/yellow] — " +
				ioprio.IgnoresWhy + ". It is still set; `yolo check` names the host change.")
		}
	}
}

// ioPriorityForBriefing is the briefing's field: the passed value, or "" where none was.
func ioPriorityForBriefing(p ioprio.Priority) string {
	if !p.Declared() {
		return ""
	}
	return string(p)
}

// ioPriorityWarnedLine is the VirtioFS backends' line.
func ioPriorityWarnedLine(p ioprio.Priority, backend string) string {
	return `[yellow]Warning: resources.io.priority "` + string(p) + `" is NOT applied on ` + backend +
		"[/yellow] — the jail runs in a VM, and its workspace reaches the Mac over VirtioFS, " +
		"which carries no I/O priority."
}

// launchedIOPriority is the value a RUNNING jail was launched with, which is the value an
// attach's entrypoint applies and the one its briefing states.
//
// On podman on Linux it is read from the container's frozen environment, because an attach's
// entrypoint reads YOLO_IO_PRIORITY from there and never from the config (a config edit takes
// effect at the next fresh launch). A jail launched before the key existed has no such
// variable and applies nothing. Elsewhere no value is ever passed, so nothing was applied.
func (o *Options) launchedIOPriority(rt string, envLines []string) ioprio.Priority {
	if rt != "podman" || o.IsMacOS { // parity: Warned — AC and podman on macOS never pass the value, so no jail of theirs holds one
		return ioprio.Normal
	}
	for _, line := range envLines {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), ioprio.EnvVar+"="); ok {
			return ioprio.Priority(v)
		}
	}
	return ioprio.Normal
}

// attachIOPriority is the value an attach's line is about. On podman on Linux that is the
// value its shell receives (launchedIOPriority), so an edited config is not graded. On the two
// VM backends no value is ever passed, so the Warned line is about the current declaration,
// which is true of whatever the jail was launched with.
func (o *Options) attachIOPriority(rt string, cfg *jsonx.OrderedMap, envLines []string) ioprio.Priority {
	if rt == "podman" && !o.IsMacOS { // parity: Honored — only podman on Linux passes the value, so only its frozen env holds one
		return o.launchedIOPriority(rt, envLines)
	}
	return ioprio.FromResources(cfgMap(cfg, "resources"))
}

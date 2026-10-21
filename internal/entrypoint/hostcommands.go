package entrypoint

// hostcommands.go is the boot's half of agentcfg.DropHostOnlyCommands: the stat it asks, and the
// line it prints for each object the jail's copy of a surface left out.

import (
	"fmt"
	"os"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
)

// jailCanStat says whether this jail can reach p at the spelling the host wrote, as a
// `mounts` or `host_files` entry at the same path makes it. Any error counts as unreachable:
// a path the jail cannot stat is one a command cannot run either.
func jailCanStat(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// noteHostOnlyCommandDrops prints one line per object a host-only command took out of the
// jail's copy, on stderr like every other host-layer disclosure, because a hook the user relies
// on must never vanish without a trace (a launch has no quiet mode, OQ-RO3). Each line names
// the next step: making the path reachable at that spelling is what brings the command back.
func noteHostOnlyCommandDrops(e *Env, surface manifest.Surface, drops []agentcfg.CommandDrop) {
	for _, d := range drops {
		e.warn(fmt.Sprintf("%s: your host %s has %s, which runs %q, and this jail has no %s, so "+
			"this jail's copy leaves it out. Your host file is unchanged; to run it here, make "+
			"that path reachable at the same spelling (`yolo config-ref`: mounts, host_files).",
			surface.Agent+"/"+surface.Name, surface.Path, pointerLabel(d.Pointer), d.Command,
			d.Path))
	}
}

package check

import (
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// noteUnifiedHomeWaiting is `yolo check`'s report of homes that wait to merge into the
// workspace's shared home (docs/design/unified-workspace-home.md §9.4, OQ-UH2): a launch is the
// only thing that merges, and this is the read-only plan of what it would move. It prints ONE
// line, naming the source homes and the next step, and nothing when nothing waits. Like the
// base-home report it writes to o.Stderr, so `--format json` is not reshaped by it.
func (o *Options) noteUnifiedHomeWaiting() {
	if o.inJail() {
		return
	}
	selection := o.unifiedHomePacks
	if selection == nil {
		selection = config.UserScopeSelectedPacks
	}
	packs, _ := selection()
	wait := run.UnifiedHomeWaiting(o.Workspace, packs,
		func(name string) (string, bool) { return o.LookPath(name) },
		func(argv []string, dir string, env []string, timeout time.Duration) run.ExecResult {
			return run.ExecResult(o.Exec(argv, dir, env, timeout))
		})
	if len(wait.Homes) == 0 {
		return
	}
	which := strings.Join(wait.Homes, " and ")
	switch {
	case wait.Running:
		fmt.Fprintf(o.Stderr, "The %s home waits to merge into this workspace's shared home: a jail of this project is running. "+
			"Run `yolo stop` and launch again to merge it.\n", which)
	case wait.Unknown:
		fmt.Fprintf(o.Stderr, "The %s home waits to merge into this workspace's shared home: yolo could not ask the container "+
			"runtime whether a jail of this project is running. Run `yolo stop` and launch again to merge it.\n", which)
	default:
		fmt.Fprintf(o.Stderr, "The %s home will merge into this workspace's shared home at your next launch; "+
			"no jail of this project is running.\n", which)
	}
}

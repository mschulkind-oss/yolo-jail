package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/broker"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// noteSingletonSettingsDrift is the ATTACH half of docs/design/host-daemon-ownership.md
// HD-D2: for every enabled host-wide daemon that is running, say so when the settings it
// was started with differ from what the config now resolves to. Key names only, never
// values.
//
// AN ATTACH REPORTS AND NEVER RESTARTS, and that is a rule about the approval gate rather
// than caution. A fresh launch applies a settings change because it passed the
// config-change approval first (run.go, "Fresh launch: config-change approval"); an attach
// is dispatched ABOVE that gate, and loopholesettings.go's OQ-K3 note is the ruling that a
// change to what a loophole may do takes effect only where the gate lives. Restarting from
// here would apply an unapproved change to every jail sharing the daemon.
//
// An UNRECORDED daemon (started by a yolo that kept no record) says nothing here: that is
// "cannot tell", and `yolo check` is where it is graded.
func (o *Options) noteSingletonSettingsDrift(cfg *jsonx.OrderedMap) {
	loopCfg := cfgMap(cfg, "loopholes")
	set := loopholes.NewHostSet(loopCfg)
	for _, lp := range set.Enabled() {
		drift, applicable := broker.ConfiguredSettingsDrift(lp, suppliedSettings(loopCfg, lp.Name))
		if !applicable || len(drift.Changed) == 0 {
			continue
		}
		o.pr(o.Stdout).print("[yellow]Note: the host-wide daemon for '" + lp.Name +
			"' is running with settings that differ from the config (" +
			strings.Join(drift.Changed, ", ") + "). An attach never applies a config change; " +
			"the next fresh launch restarts it with them.[/yellow]")
	}
}

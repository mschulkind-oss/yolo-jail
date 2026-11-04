package run

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
)

// noteLoopholesOff discloses, on the launch stream, each selected pack's off-switch loophole the
// config keeps off for this launch (docs/design/legacy-config-compat.md LC-D3): a legacy
// `loopholes.<name>.enabled: false` or a TOML user config's `[allow.<pack>] off = true`. Off, it
// starts no daemon and serves the jail nothing, so a user whose agent finds no login or no
// credentials reads why here, with the step that turns it back on.
func (o *Options) noteLoopholesOff(cfg *jsonx.OrderedMap) {
	allow := cfgMap(cfg, "allow")
	for _, lp := range loopholes.NewHostSet(loopholes.LaunchBlock(cfg)).All() {
		if lp.Source != loopholes.SourcePack || lp.Enabled || !loopholedecl.HasOffSwitch(lp.Name) {
			continue
		}
		key := "loopholes." + lp.Name + ".enabled"
		said := "`" + key + ": false`"
		if pack, _ := loopholedecl.OffSwitchPack(lp.Name); loopholes.AllowOff(allow, pack) {
			key = "allow." + pack + "." + loopholedecl.AllowOffKey
			said = "`" + key + " = true`"
		}
		o.pr(o.Stderr).print("[yellow]loophole " + richtext.Escape(lp.Name) + " is off for this launch: your config sets " +
			richtext.Escape(said) + ", so it starts nothing and nothing it serves reaches this jail. To run it, " +
			"`yolo config unset " + richtext.Escape(key) + "`.[/yellow]")
	}
}

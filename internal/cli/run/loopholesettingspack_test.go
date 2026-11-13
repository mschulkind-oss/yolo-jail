package run

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
)

// A path pack's loophole whose name is not its pack's: a TOML user config lays its settings at
// `[allow.<pack>]`, which ConfigBlock files under the PACK's name. The launch reads them there
// too (loopholes.SuppliedSettings, docs/reference/config/rulings.md LC-D10), or a config that
// set the value before `yolo config migrate` launches with the default after it.
func TestWriteLoopholeSettingsReadsAPathPacksSettingsUnderThePackName(t *testing.T) {
	redirectState(t)
	o := &Options{}
	fillDefaults(o)
	var buf strings.Builder
	o.Stdout = &buf
	lp := &loopholes.Loophole{
		Name: "note-relay",
		Pack: "sample-packs/note-relay",
		Settings: []loopholes.Setting{
			{Key: "greeting", Type: loopholes.SettingTypeString, Default: "hi"},
		},
	}
	o.writeLoopholeSettings([]*loopholes.Loophole{lp},
		settingsCfg(t, "sample-packs/note-relay", "greeting", "hello"))

	if got := readSettings(t, "note-relay")["greeting"]; got != "hello" {
		t.Errorf("greeting = %v, want the value the config set under the pack's name", got)
	}
}

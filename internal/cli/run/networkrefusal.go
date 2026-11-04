package run

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// networkrefusal.go is THE NETWORK REFUSAL (docs/design/named-setups.md SU-R5; plan slice 4b): a
// network mode the setup's technology cannot hold — `host` under Apple Container, `bridge` under
// Seatbelt or host — is refused before anything is built, in a launch (Run, right after the setup
// gate) and in `yolo check` (internal/cli/check's section_network.go), through this one function.
// An unwritten mode is the technology's own network and never refused, which is why this reads
// the WRITTEN value and not resolveNetMode, whose answer for an unwritten one is the bridge
// default (SUP-D13).

// NetworkRefusal is the refusal for setup s with flag (the typed `--network`, "" for none) and
// the config's written `network.mode`, "" when s can hold what was asked. A typed flag is judged
// in place of the key, as resolveNetModeSource lets it win. A key's refusal leads with where it
// was written (src, nil for none) and names the `unset` for that file's scope.
func NetworkRefusal(s setup.Setup, cfg *jsonx.OrderedMap, flag string, src *config.Sources) string {
	if flag != "" {
		return setup.NetworkFlagProblem(s, flag)
	}
	mode := ""
	if netSec := cfgMap(cfg, "network"); netSec != nil {
		mode = mapStr(netSec, "mode")
	}
	if mode == "" {
		return ""
	}
	locs := src.Locations("config.network.mode")
	text := setup.NetworkModeProblemIn(s, mode, networkUnsetScope(locs))
	if text != "" && len(locs) > 0 {
		text = locs[0] + ": " + text
	}
	return text
}

// networkUnsetScope is the `yolo config unset` scope flag for the file that wrote the winning
// value (locs[0]): `--user` for a user-scope file, `--local` for the workspace's local file,
// else `--project`. The user scope is read only here, on a refusal.
func networkUnsetScope(locs []string) string {
	if len(locs) == 0 {
		return "--project"
	}
	for _, u := range config.UserScopeSources().Locations("config.network.mode") {
		if u == locs[0] {
			return "--user"
		}
	}
	if strings.Contains(locs[0], "yolo-jail.local.") {
		return "--local"
	}
	return "--project"
}

// refuseNetworkMode is the launch's call: the refusal for this launch's setup, printed as a
// setup refusal, and whether it refused. Under the seal a build is always bridged whatever the
// config says (resolveNetModeSource), so nothing written is in play.
func (o *Options) refuseNetworkMode(cfg *jsonx.OrderedMap) bool {
	if o.Sealed {
		return false
	}
	text := NetworkRefusal(o.Setup, cfg, o.Network, o.configSources)
	if text == "" {
		return false
	}
	o.printSetupRefusal(text)
	return true
}

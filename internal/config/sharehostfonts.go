package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// shareHostFontsKey is the user-scope opt-in that shares the user's own font folder read-only
// into a jail that does not run as them: a container jail and the macos-user separate-account
// mode (OQ-NX9, ruled 2026-10-23, docs/design/provisioner-sets.md). The run-as-you mode reads
// ~/Library/Fonts always, so the key changes nothing there, and `yolo host` is the host.
const shareHostFontsKey = "share_host_fonts"

// ShareHostFonts reports whether the user opted in. Read from the USER config directly, as
// HostWrappersEnabled is and for the same reason: a workspace config travels with the
// repository and is agent-editable, and this key opens a folder of the human's home to the
// jail, so workspace scope is inexpressible rather than merely refused. A user config that
// cannot be read yields false: an opt-in nobody could read was not given.
func ShareHostFonts() bool {
	return shareHostFontsValue(UserScopeConfigOrEmpty())
}

func shareHostFontsValue(cfg *jsonx.OrderedMap) bool {
	v, present := cfg.Get(shareHostFontsKey)
	b, ok := v.(bool)
	return present && ok && b
}

// validateShareHostFonts shape-checks the key and refuses a workspace value, which would
// otherwise be a silent no-op (ShareHostFonts never reads it).
func validateShareHostFonts(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	v, present := config.Get(shareHostFontsKey)
	if !present {
		return
	}
	if v != nil && !isBool(v) {
		add(errs, "config."+shareHostFontsKey+": expected a boolean (got "+pyReprValue(v)+")")
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if wsValue, atWorkspace := wsCfg.Get(shareHostFontsKey); atWorkspace && wsValue != nil && WorkspaceUserScopeOnlyKey(shareHostFontsKey) {
		add(errs, "config."+shareHostFontsKey+": user-scope only — it shares your own font "+
			"folder with the jail, so it is read from "+paths.UserConfigDisplayPath()+" and a "+
			"workspace value refuses the launch. Move it there, or remove it from the workspace config.")
	}
}

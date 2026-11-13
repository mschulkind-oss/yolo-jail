package config

import (
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// validateRequestsUserScope refuses `requests` in an old-format user file at launch. The key is a
// project's (its schema rule is workspace-only, docs/reference/config/user-config.md#what-a-jail-may-do-with-your-logins): it asks the user
// for access, and a user config answers with `allow`. A TOML user file is refused by its own
// reader; the JSONC one has no schema, so without this its `requests` merged into the launch's
// config and read as if a project had asked. An explicit edit is checked by
// validateExplicitRawReadableScopes instead.
func validateRequestsUserScope(cfg *jsonx.OrderedMap, errs *[]string) {
	if _, present := cfg.Get("requests"); !present {
		return
	}
	userPath := paths.UserConfigPath()
	userCfg, err := loadUserScopeConfig(userPath, userPath, false, func(string) {})
	if err != nil || userCfg == nil {
		return
	}
	if _, atUser := userCfg.Get("requests"); !atUser {
		return
	}
	userFile, _ := UserConfigFile()
	add(errs, "config.requests: a project's key, not the user config's — it asks you for access "+
		"at launch, and your config answers with `allow`. Remove it from "+userFile+", and in the "+
		"project that needs it run `yolo request <source> <capability> [values]` (for example "+
		"`yolo request git push main`), which writes it to that project's file.")
}

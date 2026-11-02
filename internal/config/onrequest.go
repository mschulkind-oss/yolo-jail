package config

import (
	"fmt"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// onrequest.go is the config half of the request hook (boundary-broker.md §6.6, BB-D75): a
// command, named in the USER config alone, that each host broker runs once per request its
// jail files, with the request-filed event's JSON on stdin. It is off unless set, and a workspace
// config naming it is refused, as for long-lived access (§5.8): the command runs on the host
// as the user, and the command it runs can call `yolo approve`.

// OnRequestKey is the top-level key naming the hook command.
const OnRequestKey = "on_request"

// OnRequest is the hook command from the user config, "" when unset. It never consults
// the workspace config.
func OnRequest() string {
	return onRequestValue(UserScopeConfigOrEmpty())
}

// onRequestValue is the reading; anything but a non-blank string reads unset, and the
// validator is what reports it.
func onRequestValue(cfg *jsonx.OrderedMap) string {
	v, _ := cfg.Get(OnRequestKey)
	s, _ := v.(string)
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

// validateOnRequest type-checks the key and refuses it in a workspace config. A launch
// runs this validation too, so a workspace that names it refuses the launch as `yolo check`
// reports it.
func validateOnRequest(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	if v, present := config.Get(OnRequestKey); present && v != nil {
		if s, ok := v.(string); !ok || strings.TrimSpace(s) == "" {
			add(errs, fmt.Sprintf("config.%s: expected a shell command as a non-empty string, such as "+
				`"notify-send \"yolo: $YOLO_REQUEST_HEADLINE\"" (got %s); set it in %s or remove it`,
				OnRequestKey, pyReprValue(v), paths.UserConfigDisplayPath()))
		}
	}
	wsCfg, err := loadValidationWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if _, atWorkspace := wsCfg.Get(OnRequestKey); atWorkspace && WorkspaceUserScopeOnlyKey(OnRequestKey) {
		add(errs, "config."+OnRequestKey+": user-scope only — it names a command yolo runs on the host, "+
			"as you, for every request a jail files, and the command it runs can call `yolo approve`, so a workspace "+
			"config, which the agent can edit, may not name one. Put it in "+paths.UserConfigDisplayPath()+
			" and remove it from this workspace's config.")
	}
}

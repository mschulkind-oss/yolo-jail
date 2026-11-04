package config

import (
	"fmt"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// setupsretired.go refuses the three config keys named setups replace (docs/design/named-setups.md
// §10; named-setups-plan.md slice 5): `confinement`, `runtime` and `macos_user_mode`. Each is
// refused naming the setup its value meant and the verbs that move it, never aliased (MT-R3, the
// owner's: "we can break current"). `yolo config migrate` converts the values that name exactly
// one setup in a user file (internal/configmigrate, SetupForRemovedKey), so a legacy user config
// holding `runtime: "podman"` converts with nothing left over.
//
// ERROR ON THE HOST, WARNING INSIDE A JAIL (SUP-D6), the retired-key pattern of
// validateAgentsRetired: in a jail the config is the host-generated snapshot, which the user
// cannot fix at its source.

// RemovedSetupKeys are the retired keys setups replace, in the order a refusal reports them.
var RemovedSetupKeys = []string{"confinement", "runtime", "macos_user_mode"}

// SetupForRemovedKey is the built-in setup a removed key's value names by itself, "" when it
// names none or more than one: `runtime` "podman" and "container" are one setup each, while
// "macos-user" and `confinement = "guest"` are the two Seatbelt setups (the account the old
// value ran as lived in another key), and `confinement` "jail" and "host" name no setup
// (the first is the default, the second is `yolo host`). It is what `yolo config migrate`
// writes as `setup`.
func SetupForRemovedKey(key string, value any) string {
	s, _ := value.(string)
	if key == "runtime" && (s == "podman" || s == "container") {
		return SetupForRemovedRuntime(s)
	}
	return ""
}

// RemovedConfinementIsDefault reports a `confinement` value that said only what an unset
// selection says: "jail", the old default, which `yolo config migrate` drops.
func RemovedConfinementIsDefault(key string, value any) bool {
	s, _ := value.(string)
	return key == "confinement" && s == "jail"
}

// RemovedSetupKeyMessage is the refusal of one removed key at its value, without the
// "config.<key>: " prefix: design §10's sentence for the value, then the step that removes the
// line from the file that holds it (unsetFlag is "--user", "--project" or "--local").
func RemovedSetupKeyMessage(key string, value any, unsetFlag string) string {
	s, _ := value.(string)
	var body string
	switch key {
	case "confinement":
		switch s {
		case "guest":
			body = "`guest` is now a setup: `yolo config set setup seatbelt-yolo-account --user` " +
				"(as the yolo account) or `seatbelt-self` (as you)."
		case "host":
			body = "Run on your own machine with `yolo host -- <cmd>`."
		default:
			body = "`confinement` now lives in a setup. For a container: `yolo config set setup " +
				"podman-self --user` (or `apple-container-self` on a Mac), or leave it unset for the default."
		}
	case "runtime":
		switch s {
		case "podman", "container":
			body = fmt.Sprintf("`runtime` is now a setup: `yolo config set setup %s --user`.", SetupForRemovedRuntime(s))
		case "macos-user":
			body = "`runtime` is now a setup: `yolo config set setup seatbelt-yolo-account --user` " +
				"(as the yolo account) or `seatbelt-self` (as you)."
		default:
			body = "`runtime` is now a setup: `yolo config set setup podman-self --user` " +
				"(or `apple-container-self` on a Mac; `yolo setup list` names the rest)."
		}
	case "macos_user_mode":
		name := "seatbelt-yolo-account"
		if s == "self" {
			name = "seatbelt-self"
		}
		body = "Choose the account by setup: `seatbelt-self` or `seatbelt-yolo-account`, as in " +
			"`yolo config set setup " + name + " --user`."
	}
	why := "Then remove the old line"
	if unsetFlag != "--user" {
		why = "A project cannot choose where its agent runs; remove the line"
	}
	return "REMOVED — " + body + "\n" + why + ":\n    yolo config unset " + key + " " + unsetFlag
}

// validateSetupsRetired reports each removed key the merged config holds, naming the file's own
// scope flag: the workspace config's when it writes the key there, else the user config's.
func validateSetupsRetired(config *jsonx.OrderedMap, workspace string, errs, warns *[]string, edit ...*editValidationContext) {
	var wsCfg *jsonx.OrderedMap
	loaded := false
	for _, key := range RemovedSetupKeys {
		v, present := config.Get(key)
		if !present {
			continue
		}
		if !loaded {
			wsCfg, _ = loadValidationWorkspaceConfig(workspace, edit...)
			loaded = true
		}
		flag := "--user"
		if wsCfg != nil {
			if wv, atWorkspace := wsCfg.Get(key); atWorkspace {
				flag, v = "--project", wv
			}
		}
		msg := "config." + key + ": " + RemovedSetupKeyMessage(key, v, flag)
		if inJail() {
			add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
				"so remove the key from the HOST config.)")
			continue
		}
		add(errs, msg)
	}
}

package config

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

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

// RemovedSetupKeyConverts reports a user file's removed-key value `yolo config migrate`
// converts: one naming one setup (written as `setup`) or the default (dropped).
func RemovedSetupKeyConverts(key string, value any) bool {
	return SetupForRemovedKey(key, value) != "" || RemovedConfinementIsDefault(key, value)
}

// IsRemovedSetupKey reports a top-level key named setups replace (RemovedSetupKeys).
func IsRemovedSetupKey(key string) bool {
	for _, k := range RemovedSetupKeys {
		if k == key {
			return true
		}
	}
	return false
}

// isWorkspaceLocalLocation reports a "<file>:<line>:<col>" location in the workspace's local
// config (yolo-jail.local.*), whose unset scope flag is --local.
func isWorkspaceLocalLocation(loc string) bool {
	file := locationFile(loc)
	base := filepath.Base(file)
	return base == WorkspaceLocalTOMLConfigName || base == WorkspaceLocalLegacyConfigName
}

// isTOMLLocation reports a "<file>:<line>:<col>" location in a TOML file.
func isTOMLLocation(loc string) bool {
	return strings.HasSuffix(locationFile(loc), ".toml")
}

// locationFile is the file of a Sources location, "<file>:<line>:<col>" or a bare file.
func locationFile(loc string) string {
	for i := 0; i < 2; i++ {
		j := strings.LastIndexByte(loc, ':')
		if j < 0 {
			break
		}
		if _, err := strconv.Atoi(loc[j+1:]); err != nil {
			break
		}
		loc = loc[:j]
	}
	return loc
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
	var wsSrc *Sources
	loaded := false
	for _, key := range RemovedSetupKeys {
		v, present := config.Get(key)
		if !present {
			continue
		}
		if !loaded {
			wsCfg, wsSrc, _ = loadValidationWorkspaceConfigWithSources(workspace, edit...)
			loaded = true
		}
		flag := "--user"
		var locs []string
		if wsCfg != nil {
			if wv, atWorkspace := wsCfg.Get(key); atWorkspace {
				flag, v = "--project", wv
				locs = wsSrc.Locations("config." + key)
				if len(locs) > 0 && isWorkspaceLocalLocation(locs[0]) {
					flag = "--local"
				}
			}
		}
		if flag == "--user" {
			locs = validationUserSources(edit...).Locations("config." + key)
		}
		msg := "config." + key + ": " + RemovedSetupKeyMessage(key, v, flag)
		// A TOML FILE HOLDING THE KEY is one v0.16.1 wrote (`yolo config set runtime …`, or its
		// migrate copying the key verbatim), and `yolo config migrate` converts it as it does a
		// JSONC file (named-setups-plan.md SUP-D17): the refusal names it where the conversion
		// would act, a user file's value that names one setup or the default.
		if flag == "--user" && len(locs) > 0 && isTOMLLocation(locs[0]) && RemovedSetupKeyConverts(key, v) {
			msg += "\nOr let yolo convert it:\n    yolo config migrate"
		}
		if inJail() {
			add(warns, msg+" (ignored here: this is the host-generated config snapshot, "+
				"so remove the key from the HOST config.)")
			continue
		}
		add(errs, msg)
	}
}

package entrypoint

import (
	"fmt"
	"maps"
	"os"
	"reflect"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// ImagePodmanRequiredConf is the image's file of the podman settings a nested podman in the
// jail cannot run without (flake.nix, mkBinPathLinks's withNestedPodman block): cgroups
// disabled, because the jail mounts /sys/fs/cgroup read-only, plus the cgroupfs manager and
// the file events logger, because the jail runs no systemd. Only the variants that keep the
// nested-podman config bake it; the minimal one has no /etc/containers at all.
const ImagePodmanRequiredConf = "/etc/containers/yolo-required.conf"

// ContainersConfOverrideEnv is podman's own variable naming a containers.conf it loads LAST,
// over /etc/containers/containers.conf and every user file, merging key by key.
const ContainersConfOverrideEnv = "CONTAINERS_CONF_OVERRIDE"

// ExportPodmanRequiredConf points CONTAINERS_CONF_OVERRIDE at ImagePodmanRequiredConf when
// the image bakes it.
//
// WHY AN OVERRIDE AND NOT ONLY /etc/containers/containers.conf. Podman stops applying the
// /etc file's `cgroups = "disabled"` once a user ~/.config/containers/containers.conf exists,
// even one holding a single unrelated [engine] key, and the jail home persists per workspace,
// so one stale file left by an older tool breaks every `podman run` with crun's
// "cgroup.subtree_control: Read-only file system" (exit 126). The override is read after the
// user files and merges, so a user file still sets every other key.
//
// An image without the file gets no variable: podman refuses to start when the override names
// a missing file.
func ExportPodmanRequiredConf(e *Env) {
	exportPodmanRequiredConfFrom(e, ImagePodmanRequiredConf)
}

// exportPodmanRequiredConfStep is the boot step table's body for export_podman_required_conf,
// named so a test can pin that the table really runs it.
func exportPodmanRequiredConfStep(b *bootRun) { ExportPodmanRequiredConf(b.e) }

func exportPodmanRequiredConfFrom(e *Env, path string) {
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return
	}
	switch cur := e.Getenv(ContainersConfOverrideEnv); cur {
	case "", path:
		setEnvBoth(e, ContainersConfOverrideEnv, path)
	default:
		// Podman takes ONE override file, and this one was set on purpose (a pack's env, the
		// user's config), so it stays; the jail's requirements then have to ride on it. Say so
		// only when they do not: a file that already holds every required key is compliant,
		// and a warning that following its own advice cannot clear is a defect.
		if why := missingRequiredPodmanKeys(path, cur); why != "" {
			e.warnOnce("Warning: " + ContainersConfOverrideEnv + "=" + cur + " is already set and " + why +
				", so a nested `podman run` can fail with a read-only cgroup error. Copy the keys of " +
				path + " into " + cur + ", or drop the variable from the jail: find where it is set with " +
				"`yolo config why env.set." + ContainersConfOverrideEnv + "`, or remove it from every " +
				"config and pack env with `yolo config add env.unset '\"" + ContainersConfOverrideEnv +
				"\"'`, then start a new session.")
		}
	}
}

// missingRequiredPodmanKeys compares the override file someone else set (foreign) against the
// image's required file (required), key by key under each table, and returns "" when foreign
// holds every required key with the required value, or else what is wrong with it, for the
// warning's sentence.
func missingRequiredPodmanKeys(required, foreign string) string {
	want, err := tomlx.DecodeFile(required)
	if err != nil {
		return "the image's " + required + " could not be read (" + err.Error() + ")"
	}
	have, err := tomlx.DecodeFile(foreign)
	if err != nil {
		return "it could not be read as TOML (" + err.Error() + ")"
	}
	var wrong []string
	for _, table := range slices.Sorted(maps.Keys(want)) {
		wantKeys, _ := want[table].(map[string]any)
		haveKeys, _ := have[table].(map[string]any)
		for _, key := range slices.Sorted(maps.Keys(wantKeys)) {
			got, ok := haveKeys[key]
			switch {
			case !ok:
				wrong = append(wrong, fmt.Sprintf("[%s] %s is missing", table, key))
			case !reflect.DeepEqual(got, wantKeys[key]):
				wrong = append(wrong, fmt.Sprintf("[%s] %s is %v, not %v", table, key, got, wantKeys[key]))
			}
		}
	}
	if len(wrong) == 0 {
		return ""
	}
	return "in it " + strings.Join(wrong, ", ")
}

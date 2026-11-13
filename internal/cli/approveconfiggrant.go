package cli

// approveconfiggrant.go holds the next step that names how a grant declared in a project's
// config is removed, which `yolo approve --revoke` prints for a grant the store never held.

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// configGrantRemoval is the next step that takes a grant declared in a project's config out of
// it: kind on name (a branch or tag, or "" for a create-branch grant), declared in one of files
// (the project's config, then its local file), in workspace. A TOML file names the `yolo config
// unset` that removes the request entry, with its position when the file shows it; a legacy
// JSONC file is converted first.
func configGrantRemoval(workspace string, files []string, source, kind, name string) string {
	capability := grantKindCapability(source, kind)
	key := "requests." + configKeyComponent(source)
	if capability != "" {
		key += "." + configKeyComponent(capability)
	}
	ws := termsafe.Visible(workspace)
	step := ""
	if capability == "" {
		// No kind, or no selected pack to map it: name the table and the verb's shape.
		step = fmt.Sprintf("in %s, `yolo config why %s` shows what the project requests and `yolo config unset %s.<capability> --project` removes one",
			ws, key, key)
	} else if name == "" {
		step = fmt.Sprintf("in %s, run `%s`", ws, shquote.Join([]string{"yolo", "config", "unset", key, "--project"}))
	} else if at, flag, ok := findRequestEntry(files, key, name); ok {
		step = fmt.Sprintf("in %s, run `%s` to remove %s from %s", ws,
			shquote.Join([]string{"yolo", "config", "unset", fmt.Sprintf("%s[%d]", key, at), flag}), termsafe.Visible(name), key)
	} else {
		step = fmt.Sprintf("in %s, remove %s from %s with `%s`, <n> being its position from 0 as `yolo config why %s` lists it", ws,
			termsafe.Visible(name), key, "yolo config unset '"+key+"[<n>]' --project", key)
	}
	// The project's committed file decides the format: the readable names resolve to a TOML
	// name unless a legacy file is what the workspace holds (configBranchFiles).
	if len(files) > 0 && files[0] != "" && filepath.Ext(files[0]) != ".toml" {
		entry := "the grant"
		if kind != "" {
			entry = fmt.Sprintf("its `%s` grant entry", termsafe.Visible(kind))
		}
		step = fmt.Sprintf("%s is in the deprecated JSONC format, which keeps %s under `brokered.%s.grants`: "+
			"convert it first with `yolo config migrate` in %s (preview with `yolo config migrate --dry-run`), then %s",
			termsafe.Visible(files[0]), entry, termsafe.Visible(source), ws, step)
	}
	return step + ", then run `yolo config apply` there"
}

func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// configBranchFiles is the config file and local file of workspace that a scope file's
// names point at. The launch records the legacy names (config.ResolveWorkspaceConfigPath), so
// where the workspace is readable here its files are resolved again with the TOML names first;
// where it is not, the scope file's names are all there is.
func configBranchFiles(workspace, configName, localName string) (string, string) {
	if dirExists(workspace) || configName == "" {
		project, _ := config.ResolveReadableWorkspaceConfigPath(workspace, config.WorkspaceConfigName)
		local, _ := config.ResolveReadableWorkspaceConfigPath(workspace, config.WorkspaceLocalConfigName)
		if _, err := os.Stat(local); err != nil {
			local = "" // no local file declares anything
		}
		return project, local
	}
	local := ""
	if localName != "" {
		local = filepath.Join(workspace, localName)
	}
	return filepath.Join(workspace, configName), local
}

// grantKindCapability is the request capability a selected pack declares for a grant kind of
// source (git's branch is push, tag push_tags, create-branch new_branches), or "" when no
// selected pack declares source.
func grantKindCapability(source, kind string) string {
	brokers, _, err := allowSelectedBrokers()
	if err != nil {
		return ""
	}
	for _, b := range brokers {
		if b.Brokered.Source == source {
			return b.KindCapabilities[kind]
		}
	}
	return ""
}

// findRequestEntry is the position of name in the request list at key in the first TOML file
// of files that holds it, with the `yolo config unset` target flag that selects that file.
func findRequestEntry(files []string, key, name string) (int, string, bool) {
	steps, err := parseConfigEditKey(key)
	if err != nil {
		return 0, "", false
	}
	for i, f := range files {
		if f == "" || filepath.Ext(f) != ".toml" {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		v, ok, err := configEditAuthoredValue(data, configdoc.TOML, steps)
		items, isList := v.([]any)
		if err != nil || !ok || !isList {
			continue
		}
		if at := slices.Index(items, any(name)); at >= 0 {
			flag := "--project"
			if i > 0 {
				flag = "--local"
			}
			return at, flag, true
		}
	}
	return 0, "", false
}

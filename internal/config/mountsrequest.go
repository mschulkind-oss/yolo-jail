package config

// mountsrequest.go is RC-D24 (docs/design/readable-config.md): a project's TOML config may
// REQUEST a writable host mount, and the user config says whether any project may.
//
//   - The ceiling is `[allow.mounts] on_request = ["write"]` in the user config, read through
//     the same reader the git broker's `allow.git` ceiling uses (readableUserAllows). It has no
//     `always`: every project's writable mount is approved by a human, per project, at the
//     config-change gate, so a standing grant would be the one thing the ruling rules out.
//   - The request is a `mounts` table with `write = true` in yolo-jail.toml or
//     yolo-jail.local.toml (or a TOML file either includes). The JSONC reader is not
//     backported: a read-write element a JSONC workspace file wrote keeps validateMountScope's
//     refusal, the user's allow line notwithstanding.
//   - The approval is the config-change gate's: the element is in the workspace config the gate
//     diffs, so approving the diff approves it, and changing it asks again. The gate hands the
//     prompter the writable mounts (ChangeReport.WritableMounts) so it can show them loud.
//
// What an allowed request may BE is unchanged: the /ctx-only rule (ParseMountElement), the
// refusal set (validateRWMountSources), the broker mount fence and the destination rules judge
// it exactly as they judge a user-scope element. The allow line widens WHERE one may be
// written, never what it may reach.

import (
	"fmt"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const (
	// allowMountsName is the `allow` table core reads itself; it names no broker or pack, so
	// the broker allowance resolver passes over it (ResolveReadableAllowance).
	allowMountsName = "mounts"
	// allowMountsWrite is the one capability `allow.mounts.on_request` takes.
	allowMountsWrite = "write"
)

// validateAllowMountsTable checks `allow.mounts` itself, value v; "" when it is valid. A
// tombstone ("off") is valid and allows nothing.
func validateAllowMountsTable(v any) []string {
	if isReadableTableOff(v) {
		return nil
	}
	table, ok := asMap(v)
	if !ok {
		return []string{"config.allow.mounts: expected a table, such as [allow.mounts] with on_request = [\"write\"]"}
	}
	var problems []string
	for _, key := range table.Keys() {
		value := readableValue(table, key)
		switch key {
		case "on_request":
			values, err := readableStringList(value, "allow.mounts.on_request")
			if err != nil {
				problems = append(problems, "config."+err.Error())
				continue
			}
			for _, capability := range values {
				if capability != allowMountsWrite {
					problems = append(problems, fmt.Sprintf("config.allow.mounts.on_request: %q is not a mount "+
						"capability; the only one is %q, which lets a project's TOML config request a writable "+
						"mount (each approved at launch). Read-only mounts need no allowance",
						termsafe.Visible(capability), allowMountsWrite))
				}
			}
		case "always":
			problems = append(problems, "config.allow.mounts.always: mounts have no `always`. Every writable "+
				"mount a project requests is approved by you, per project, at the config-change prompt, so "+
				"a standing grant is the one thing this table cannot say. Write `on_request = [\"write\"]` "+
				"under [allow.mounts] instead")
		default:
			problems = append(problems, "config.allow.mounts."+termsafe.Visible(key)+
				": unknown key (known: on_request)")
		}
	}
	return problems
}

// validateAllowMounts runs validateAllowMountsTable over the composed config's `allow.mounts`.
// `allow` is user-scope only (the readable schema refuses it in a workspace file), so the
// composed value is the user's.
func validateAllowMounts(config *jsonx.OrderedMap, errs *[]string) {
	allowV, present := config.Get("allow")
	if !present {
		return
	}
	allow, ok := asMap(allowV)
	if !ok {
		return
	}
	v, present := allow.Get(allowMountsName)
	if !present {
		return
	}
	for _, p := range validateAllowMountsTable(v) {
		add(errs, p)
	}
}

// withReadableScopeKeys is known plus the three readable-config tables a TOML scope may write
// and the legacy key list does not hold: `allow`, the user-scope ceiling table, `requests`,
// the workspace-scope ask (docs/design/readable-config.md RC-D12), and `active_provider`, the
// user-scope selection (OQ-RC14), which validateActiveProvider judges. A launch's validation of a
// composed config holding the user's [allow.mounts] (or any [allow.<name>], which the broker
// allowance resolver judges) or a project's [requests.<name>] (which the same resolver judges
// against that ceiling) must not call either table unknown: `yolo request` writes the second,
// and the check it runs afterwards refused what it had just written. The readable schema
// already refuses `allow` in a workspace TOML file and `requests` in a user one, and
// validateWorkspaceAllow refuses `allow` in a JSONC workspace file (validateRequestsUserScope
// `requests` in a JSONC user file, validateActiveProvider `active_provider` in a JSONC workspace
// file).
func withReadableScopeKeys(known map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(known)+3)
	for k := range known {
		out[k] = struct{}{}
	}
	out["allow"] = struct{}{}
	out["requests"] = struct{}{}
	out["active_provider"] = struct{}{}
	return out
}

// userAllowsWritableMountRequests is the ceiling: does user lists "write" under
// `allow.mounts.on_request`? The reader is the broker's own (readableUserAllows).
func userAllowsWritableMountRequests(user *jsonx.OrderedMap) bool {
	return user != nil && readableUserAllows(user, allowMountsName, "on_request", allowMountsWrite)
}

// validationUserScope is the user scope a validation reads: the planned one for an edit, the
// files on disk otherwise. nil when it cannot be read, which allows nothing.
func validationUserScope(edit ...*editValidationContext) *jsonx.OrderedMap {
	if len(edit) != 0 && edit[0] != nil {
		return edit[0].rawUser
	}
	user, _, err := UserScopeConfigWithSources(false, func(string) {})
	if err != nil {
		return nil
	}
	return user
}

// loadMountsWorkspaceConfig is the workspace config, with its record, as validateMountScope and
// validateWorkspaceAllow re-read it. It is the format-aware reader (loadReadableWorkspaceConfig:
// yolo-jail.toml or yolo-jail.jsonc, and each one's local file), never loadWorkspaceConfig,
// which loadValidationWorkspaceConfigWithNode uses and which finds no TOML workspace file: a
// scope check through it would never see a TOML project's request at all. An edit validates
// its planned files instead.
func loadMountsWorkspaceConfig(workspace string, edit ...*editValidationContext) (*jsonx.OrderedMap, *srcNode, error) {
	if len(edit) != 0 && edit[0] != nil {
		return loadValidationWorkspaceConfigWithNode(workspace, edit...)
	}
	return loadReadableWorkspaceConfig(workspaceOrCwd(workspace), false, func(string) {}, true)
}

// writableMountRequestRefusal is the refusal for a TOML request the user config does not allow,
// and its next step is the exact two lines and the exact file, then the one command that writes
// them (happy-path-principle.md). `config add` creates the missing table and list (OQ-RC10).
func writableMountRequestRefusal(m ContextMount) string {
	userPath, _, toml, err := resolveUserConfigPath(paths.UserConfigPath())
	lines := "[allow.mounts]\n    on_request = [\"write\"]"
	if err != nil || userPath == "" {
		userPath = paths.UserConfigPath()
	} else if !toml {
		lines = `"allow": {"mounts": {"on_request": ["write"]}}`
	}
	return fmt.Sprintf("config.mounts: %s asks for a writable host mount (write = true), and your "+
		"user config does not let a project ask for one. To let projects request writable mounts, "+
		"each still approved by you at launch, run `yolo config add allow.mounts.on_request write --user` "+
		"or add to %s:\n    %s\nOtherwise remove `write = true` from the entry and it mounts read-only",
		termsafe.Visible(m.Spec), userPath, lines)
}

// workspaceReadWriteMountRefusal is the refusal for a read-write element in the workspace config
// that is not a request (isWorkspaceRequest). Written by yolo-jail.jsonc or
// yolo-jail.local.jsonc itself, it is the convert-first refusal: RC-D24 made a writable request
// TOML-only, so converting is the step that turns this element into one. Anywhere else (a file
// an include reached) it keeps the user-scope advice, since converting would not make it a
// request.
func workspaceReadWriteMountRefusal(m ContextMount, node *srcNode) string {
	if path := legacyWorkspaceFileWriter(node); path != "" {
		return fmt.Sprintf("config.mounts: %s is still in the deprecated JSONC format, and a writable "+
			"mount request (%s) needs the TOML format. %s; the converted entry is then a request your "+
			"user config must allow, approved by you at launch. Or move the entry to %s, where a "+
			"read-write mount needs no request, or make it read-only.",
			termsafe.Visible(path), termsafe.Visible(m.Spec), ConvertFirstMigrateStep, paths.UserConfigDisplayPath())
	}
	return fmt.Sprintf("config.mounts: %s is in the workspace config, and a read-write "+
		"mount is user-scope only — move it to %s. A workspace config is agent-editable, "+
		"so it cannot grant a read-write host mount (docs/reference/context-mounts.md#where-a-read-write-mount-may-be-declared; "+
		"a read-only entry may stay where it is)", m.Spec, paths.UserConfigDisplayPath())
}

// legacyWorkspaceFileWriter is the path of the legacy JSONC workspace file (yolo-jail.jsonc or
// yolo-jail.local.jsonc, not one an include reached) that wrote n, or "" when none did.
func legacyWorkspaceFileWriter(n *srcNode) string {
	for _, w := range n.writerList() {
		f := w.file
		if f == nil || f.format != configdoc.JSONC || f.via != "" {
			continue
		}
		if base := filepath.Base(f.path); base == WorkspaceConfigName || base == WorkspaceLocalConfigName {
			return f.path
		}
	}
	return ""
}

// validateWorkspaceAllow refuses an `allow` table a workspace config wrote. `allow` is the
// user's ceiling: a TOML workspace file is refused it by the readable schema, and a JSONC one
// here, since the composed config a launch validates accepts the key (withReadableScopeKeys) and would
// otherwise take a workspace's table without a word. Re-read only when the composed config
// holds one.
func validateWorkspaceAllow(config *jsonx.OrderedMap, workspace string, errs *[]string, edit ...*editValidationContext) {
	if _, has := config.Get("allow"); !has {
		return
	}
	wsCfg, wsNode, err := loadMountsWorkspaceConfig(workspace, edit...)
	if err != nil || wsCfg == nil {
		return
	}
	if _, has := wsCfg.Get("allow"); !has {
		return
	}
	add(errs, locatedAt(wsNode.key("allow"), fmt.Sprintf("config.allow: is in the workspace config, "+
		"and `allow` is user-scope only: it is your ceiling on what a project may ask for, so a "+
		"project cannot set it. Move it to %s, or remove it", paths.UserConfigDisplayPath())))
}

// WritableMountMarker is the words every rendering of a writable mount at the gate carries, so
// it stays loud with color off (docs/reference/cli-color.md).
const WritableMountMarker = "WRITABLE HOST MOUNT"

// WritableMountBlock is the plain-text block the gate shows for lines (WorkspaceMount.Line):
// one marked line per mount, then what approving means. nil for none.
func WritableMountBlock(lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	out := make([]string, 0, len(lines)+1)
	for _, l := range lines {
		out = append(out, "⚠  "+WritableMountMarker+": "+l)
	}
	out = append(out, "   The jail can create, change and delete files there on your machine. "+
		"Approving this config approves "+itOrThem(len(lines))+"; a changed request, or a path that "+
		"now resolves elsewhere, asks again.")
	return out
}

func itOrThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

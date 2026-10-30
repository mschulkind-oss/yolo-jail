package cli

// approveremember.go is `yolo approve <id> --remember`: Allow once a pending git push and
// remember each ref it needed approval for in the project's config, as the request that
// `yolo request git push <branch>` (or `push_tags <tag>`) writes, through that verb's own code
// path (requestRunW), so its ceiling report, post-write check and named host step are the
// same. It also holds the next step that names how a grant declared in a project's config is
// removed, which `--revoke` prints for a grant the store never held.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/brokerstore"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/gitbroker"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

// rememberedRef is one ref a push needed approval for, as the request that remembers it:
// `yolo request git <capability> <name>`.
type rememberedRef struct{ capability, name string }

// rememberCapability maps a ref a push needed approval for to the request capability that
// covers it, as the git pack declares them (kind_capabilities: branch = "push",
// tag = "push_tags").
func rememberCapability(ref string) (rememberedRef, bool) {
	if name, ok := strings.CutPrefix(ref, brokerscope.RefNamespace(brokerscope.KindBranch)); ok && name != "" {
		return rememberedRef{"push", name}, true
	}
	if name, ok := strings.CutPrefix(ref, brokerscope.RefNamespace(brokerscope.KindTag)); ok && name != "" {
		return rememberedRef{"push_tags", name}, true
	}
	return rememberedRef{}, false
}

// approveRemember answers request id with Allow once and writes, for each ref the push needed
// approval for, the project request that covers that ref from the next `yolo config apply` on.
// The checks that refuse run before anything is written or decided; the write comes before the
// decision, so a write that fails leaves the request pending for another answer.
func approveRemember(stores []*brokerstore.Store, id string, out, errs io.Writer) int {
	s, q, err := findRequest(stores, id)
	if err != nil {
		return approveFailure(err, errs)
	}
	if q.State != "pending" {
		return approveFailure(fmt.Errorf("%s is %s: %w", id, q.State, brokerstore.ErrAlreadyDecided), errs)
	}
	if s.SourceName() != gitbroker.Source || q.Set != "push" {
		fmt.Fprintf(errs, "yolo approve: --remember is only for a git push request. Next: run `yolo approve %s` with no flags to Allow once.\n", termsafe.Visible(id))
		return approveExitPolicy
	}
	var refs []rememberedRef
	for _, ref := range q.NeedsApproval {
		r, ok := rememberCapability(ref)
		if !ok {
			fmt.Fprintf(errs, "yolo approve: %s pushes %s, which is neither a branch nor a tag, so no request can remember it. Nothing was written. Next: run `yolo approve %s` with no flags to Allow once.\n",
				termsafe.Visible(id), termsafe.Visible(ref), termsafe.Visible(id))
			return approveExitPolicy
		}
		refs = append(refs, r)
	}
	if len(refs) == 0 {
		fmt.Fprintf(errs, "yolo approve: %s names no ref that needed approval, so there is nothing to remember. Nothing was written. Next: run `yolo approve %s` with no flags to Allow once.\n",
			termsafe.Visible(id), termsafe.Visible(id))
		return approveExitPolicy
	}
	if legacy, ok := legacyWorkspaceConfig(q.Workspace); ok {
		fmt.Fprintf(errs, "yolo approve: %s is still in the deprecated JSONC format, and remembering a grant needs the TOML format. "+
			"Convert it first: `yolo config migrate` (preview with `yolo config migrate --dry-run`), then run this command again. Nothing was written.\n",
			termsafe.Visible(legacy))
		return approveExitPolicy
	}

	for _, r := range refs {
		rc, werr := requestInWorkspace(q.Workspace, []string{gitbroker.Source, r.capability, r.name}, out, errs)
		if werr != nil || !requestWritten(q.Workspace, gitbroker.Source, r.capability, r.name) {
			if werr != nil {
				fmt.Fprintf(errs, "yolo approve: cannot write the request in %s: %v.\n", termsafe.Visible(q.Workspace), werr)
			}
			if rc == 0 {
				rc = approveExitFailed
			}
			fmt.Fprintf(errs, "yolo approve: %s was not remembered (see above), and %s is still pending. "+
				"Next: fix what is named above and run `yolo approve %s --remember` again, or run `yolo approve %s` to Allow once without remembering.\n",
				termsafe.Visible(r.name), termsafe.Visible(id), termsafe.Visible(id), termsafe.Visible(id))
			return rc
		}
	}

	g, err := s.Decide(id, "pending", brokerstore.Decision{})
	if err != nil {
		fmt.Fprintf(errs, "yolo approve: the request was remembered in %s's config, but this push was not allowed.\n", termsafe.Visible(q.Workspace))
		return approveFailure(err, errs)
	}
	fmt.Fprintf(out, "Allowed %s: one exact command, %d use(s) for every jail of %s, across restarts, expires %s (%s). Run `yolo approve` to list or `yolo approve --revoke %s` to revoke.\n",
		termsafe.Visible(id), g.Uses, termsafe.Visible(g.Workspace), g.Expires.Format(time.RFC3339), g.ID, g.ID)
	file, _ := config.ResolveReadableWorkspaceConfigPath(q.Workspace, config.WorkspaceConfigName)
	for _, r := range refs {
		fmt.Fprintf(out, "Remembered %s in %s: requests.%s.%s now lists it, as `yolo request %s %s %s` run in %s writes it.\n",
			termsafe.Visible(r.name), termsafe.Visible(file), gitbroker.Source, r.capability,
			gitbroker.Source, r.capability, shquote.Quote(r.name), termsafe.Visible(q.Workspace))
	}
	fmt.Fprintf(out, "Later pushes need no approval once you approve the change: run `yolo config apply` in %s and answer y.\n", termsafe.Visible(q.Workspace))
	return 0
}

// findRequest is the store holding request id, and the request as it stands.
func findRequest(stores []*brokerstore.Store, id string) (*brokerstore.Store, brokerstore.Request, error) {
	for _, s := range stores {
		rs, _, err := s.List()
		if err != nil {
			return nil, brokerstore.Request{}, err
		}
		for _, q := range rs {
			if q.ID == id {
				return s, q, nil
			}
		}
	}
	return nil, brokerstore.Request{}, fmt.Errorf("no request %s: %w", id, brokerstore.ErrUnknownID)
}

// legacyWorkspaceConfig is the workspace's committed config file when it is still in the
// legacy JSONC (or JSON) format, the one the request verb's TOML write cannot extend.
func legacyWorkspaceConfig(workspace string) (string, bool) {
	path, name := config.ResolveReadableWorkspaceConfigPath(workspace, config.WorkspaceConfigName)
	if name == config.WorkspaceTOMLConfigName {
		return "", false
	}
	if fi, err := os.Stat(path); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

// requestInWorkspace runs the request verb, `yolo request <args>`, for workspace: the verb
// resolves its workspace from the cwd, as it does when a person runs it there, so the cwd is
// that workspace for the call and restored after it.
func requestInWorkspace(workspace string, args []string, out, errs io.Writer) (int, error) {
	if workspace == "" || !filepath.IsAbs(workspace) {
		return 0, errors.New("the request records no absolute workspace path")
	}
	prev, err := os.Getwd()
	if err != nil {
		return 0, err
	}
	if err := os.Chdir(workspace); err != nil {
		return 0, err
	}
	defer func() { _ = os.Chdir(prev) }()
	return requestRunW(args, out, errs), nil
}

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
			entry = fmt.Sprintf("its {\"kind\": \"%s\", …} entry", termsafe.Visible(kind))
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

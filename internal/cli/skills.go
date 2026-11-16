package cli

// Architecture and invariants: docs/reference/workspace-git-skills/README.md

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/gitproto"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/skillimports"
	"github.com/mschulkind-oss/yolo-jail/internal/termsafe"
)

const skillsUsage = `Usage: yolo skills <add|update|pin|list|remove> [arguments]

Manage this workspace's Git skill imports. Imported content is projected as skills only,
through this workspace's approved Git broker access, and staged below workspace skills.

Commands:
  add <url> [--as <handle>] [--ref <ref>] [--track] [--path <dir>] [--only a,b]
                         Fetch through the Git broker and add a pinned import (default)
                         or a moving ref (--track). Without --ref, use the remote's
                         advertised default branch. Without --as, use the final repository
                         path component (after removing one terminal .git).
  update [<handle>]      Refresh entries with valid resolution records; a pinned entry
                         needs its ref label to update
  pin <handle>           Pin a moving ref to its recorded commit without network access
  list                   Show declarations in a jail; show host-side resolution/store state
                         on the host
  remove <handle>        Remove only the workspace config entry; keep host store and record

add, update and pin run on the host. Inside a jail they print a yolo host-run line and do nothing.
remove and jail list use workspace config only.

Flags for add:
  --as HANDLE     Override the derived import handle
  --ref REF       Branch or tag; defaults to the remote's advertised branch
  --track         Keep a moving ref instead of pinning the fetched commit
  --path DIR      Repository path containing immediate skill directories (default: skills)
  --only a,b      Import only these immediate skill directory names
  --help, -h      Show this help

Examples:
  yolo skills add https://github.com/acme/skills --as acme
  yolo skills update acme
  yolo skills list`

var resolveSkillImportForCommand = func(ctx context.Context, store *skillimports.Store, entry config.SkillImport,
	authority skillimports.Authority) (skillimports.Resolution, error) {
	return store.FetchForCommand(ctx, entry, authority)
}

var readSkillInventorySourcesForCommand = run.ReadSkillInventorySources

var inspectImportedSkillShadowsForCommand = jailcontent.InspectImportedSkillShadows

func runSkillsVerb(args []string) int { return skillsMain(args[1:]) }

func skillsMain(args []string) int {
	if answerHelp("skills", args, os.Stdout) {
		return 0
	}
	if len(args) == 0 {
		fmt.Fprintln(os.Stdout, skillsUsage)
		return 0
	}
	switch args[0] {
	case "add":
		if config.InJail() {
			printSkillsHostRun(args)
			return 1
		}
		return skillsAdd(args[1:])
	case "update":
		if config.InJail() {
			printSkillsHostRun(args)
			return 1
		}
		return skillsUpdate(args[1:])
	case "pin":
		if config.InJail() {
			printSkillsHostRun(args)
			return 1
		}
		return skillsPin(args[1:])
	case "list":
		return skillsList(args[1:])
	case "remove":
		return skillsRemove(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "yolo skills: unknown command %q; see `yolo skills --help`\n", termsafe.Visible(args[0]))
		return 2
	}
}

type skillAddArgs struct {
	url, handle, ref, path string
	track                  bool
	only                   []string
	handleSet, refSet      bool
}

func parseSkillAddArgs(args []string) (skillAddArgs, error) {
	parsed := skillAddArgs{path: "skills"}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		name, value, glued := strings.Cut(arg, "=")
		if name == "--track" {
			if glued {
				return parsed, errors.New("--track takes no value")
			}
			parsed.track = true
			continue
		}
		switch name {
		case "--as", "--ref", "--path", "--only":
			if !glued {
				if i+1 >= len(args) {
					return parsed, fmt.Errorf("%s needs a value", name)
				}
				i++
				value = args[i]
			}
			switch name {
			case "--as":
				if parsed.handleSet {
					return parsed, errors.New("--as may appear only once")
				}
				parsed.handle, parsed.handleSet = value, true
			case "--ref":
				if parsed.refSet {
					return parsed, errors.New("--ref may appear only once")
				}
				parsed.ref, parsed.refSet = value, true
			case "--path":
				parsed.path = value
			case "--only":
				parsed.only = strings.Split(value, ",")
			}
		default:
			if strings.HasPrefix(arg, "-") {
				return parsed, fmt.Errorf("unknown flag %s", termsafe.Visible(arg))
			}
			if parsed.url != "" {
				return parsed, errors.New("add takes one Git repository URL")
			}
			parsed.url = arg
		}
	}
	if parsed.url == "" {
		return parsed, errors.New("name a Git repository URL")
	}
	return parsed, nil
}

func skillsAdd(args []string) int {
	return withSkillsWriterLock(args, func(args []string, store *skillimports.Store) int { return skillsAddLocked(args, store) })
}

func skillsAddLocked(args []string, store *skillimports.Store) int {
	parsed, err := parseSkillAddArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills add: %v; see `yolo skills add --help`\n", err)
		return 2
	}
	workspace := configEditWorkspace()
	cfg, err := loadSkillCommandConfig(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills add: cannot read this workspace's config: %s; fix it, then run `yolo skills add` again\n", termsafe.Visible(err.Error()))
		return 1
	}
	if !config.WorkspaceSkillImportsEnabled(cfg) {
		return skillImportVetoRefusal(os.Stderr)
	}
	handle := parsed.handle
	if !parsed.handleSet {
		handle, err = deriveSkillImportHandle(parsed.url)
		if err != nil || !config.ValidSkillImportHandle(handle) {
			fmt.Fprintf(os.Stderr, "yolo skills add: cannot derive a valid handle from %s; choose one with `--as <handle>`, then retry\n", termsafe.Visible(shquote.QuoteDisplay(parsed.url)))
			return 1
		}
	}
	if !config.ValidSkillImportHandle(handle) {
		fmt.Fprintf(os.Stderr, "yolo skills add: %q is not a valid handle; use a non-empty name without slash, colon, query or fragment characters and retry with `--as <handle>`\n", termsafe.Visible(handle))
		return 1
	}
	if skillImportHandleExists(cfg, handle) {
		fmt.Fprintf(os.Stderr, "yolo skills add: handle %q already exists in this workspace; nothing was fetched or written. Choose a distinct handle with `--as <handle>`\n", termsafe.Visible(handle))
		return 1
	}
	validationRef := parsed.ref
	if validationRef == "" {
		validationRef = "main" // the actual default branch is resolved through the broker below
	}
	if problems := config.ValidateSkillImportDeclaration(handle, skillImportEntryValue(parsed.url, validationRef, "", parsed.path, parsed.only, len(parsed.only) > 0)); len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "yolo skills add: %s; correct the URL or fields, then retry `yolo skills add`\n", termsafe.Visible(problems[0]))
		return 1
	}
	mutation := configedit.Mutation{Action: configedit.Add,
		Path:  []configdoc.Step{{Key: "skill_imports"}, {Key: handle}},
		Value: skillImportEntryValue(parsed.url, validationRef, "", parsed.path, parsed.only, len(parsed.only) > 0)}
	target, err := configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetProject})
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills add: cannot prepare the workspace config edit: %s; fix the workspace config, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}

	entry := config.SkillImport{Handle: handle, Git: parsed.url, Ref: parsed.ref, Path: parsed.path,
		Only: parsed.only, OnlySet: len(parsed.only) > 0}
	record, err := resolveSkillImportForCommand(context.Background(), store, entry, skillImportCommandAuthority(workspace))
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills add: the Git broker did not resolve %q: %s; check this workspace's approved Git access, then retry\n", termsafe.Visible(handle), termsafe.Visible(err.Error()))
		return 1
	}
	if record.Ref == "" || !store.HasCommit(record.Commit) {
		fmt.Fprintln(os.Stderr, "yolo skills add: the broker returned no stored branch/tag commit; nothing was added. Pass `--ref <branch>` and retry")
		return 1
	}
	entryValue := skillImportEntryValue(parsed.url, record.Ref, "", parsed.path, parsed.only, len(parsed.only) > 0)
	if !parsed.track {
		record.Rev = record.Commit
		record.ResolvedRef = ""
		entryValue.Set("rev", record.Commit)
	}
	mutation.Value = entryValue
	result, err := configedit.ApplyTargeted(workspace, target, mutation, configedit.ApplyOptions{HeldParentLock: store.CommandDirectoryLock(), Verify: func(_, _ []byte) error { return store.WriteResolution(record) }})
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills add: the import was fetched, but the workspace config edit was refused: %s; no config entry was written. Fix the named config, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	entry.Ref = record.Ref
	entry.Rev = record.Rev
	inspection, inspectErr := store.Inspect(entry, skillImportCommandAuthority(workspace))
	if inspectErr == nil {
		fmt.Fprintf(os.Stdout, "%s: resolution created at %s (%s access), ref/pin %s; %d skills; visible paths unimported: %s.\n", termsafe.Visible(handle), record.Commit, termsafe.Visible(record.AccessClass), termsafe.Visible(record.Ref), len(inspection.Skills), displaySkillNames(inspection.Unimported))
	}
	mode := "moving ref"
	if !parsed.track {
		mode = "pinned"
	}
	fmt.Fprintf(os.Stdout, "%s\nAdded %q from %s at %s (%s, %s access). It is in the workspace config and will be staged after the config-change gate on the next fresh launch or attach; a running jail was not changed.\n",
		result.Path, termsafe.Visible(handle), termsafe.Visible(record.Repo), record.Commit, mode, termsafe.Visible(record.AccessClass))
	return 0
}

func deriveSkillImportHandle(remote string) (string, error) {
	identity, err := gitproto.Identity(remote)
	if err != nil {
		return "", err
	}
	base := filepath.Base(identity)
	if base == "" || base == "." || base == string(filepath.Separator) {
		return "", errors.New("repository URL has no final path component")
	}
	return base, nil
}

func skillImportEntryValue(remote, ref, rev, path string, only []string, onlySet bool) *jsonx.OrderedMap {
	value := jsonx.NewOrderedMap()
	value.Set("git", remote)
	if rev != "" {
		value.Set("rev", rev)
	}
	if ref != "" {
		value.Set("ref", ref)
	}
	if path != "" && path != "skills" {
		value.Set("path", path)
	}
	if onlySet {
		names := make([]any, len(only))
		for i, name := range only {
			names[i] = name
		}
		value.Set("only", names)
	}
	return value
}

func skillImportHandleExists(cfg *jsonx.OrderedMap, handle string) bool {
	if cfg == nil {
		return false
	}
	raw, ok := cfg.Get("skill_imports")
	if !ok {
		return false
	}
	imports, ok := raw.(*jsonx.OrderedMap)
	if !ok {
		return false
	}
	_, found := imports.Get(handle)
	return found
}

func loadSkillCommandConfig(workspace string) (*jsonx.OrderedMap, error) {
	return config.LoadReadableConfig(workspace, true, func(string) {})
}

func skillImportCommandAuthority(workspace string) skillimports.Authority {
	return skillimports.AuthorityFromWorkspace(workspace, config.ApprovedScope(workspace, "git"),
		config.ApprovedGrants(workspace, "git"))
}

func skillImportVetoRefusal(errw io.Writer) int {
	fmt.Fprintln(errw, "yolo skills: workspace skill imports are disabled by `workspace_skill_imports = false` in your user config; enable them with `yolo config set workspace_skill_imports true --user`, then retry. Nothing was fetched or written.")
	return 1
}

func skillsUpdate(args []string) int {
	return withSkillsWriterLock(args, func(args []string, store *skillimports.Store) int { return skillsUpdateLocked(args, store) })
}

func skillsUpdateLocked(args []string, store *skillimports.Store) int {
	handle, err := oneSkillHandle("update", args, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills update: %v; see `yolo skills --help`\n", err)
		return 2
	}
	workspace := configEditWorkspace()
	cfg, err := loadSkillCommandConfig(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills update: cannot read this workspace's config: %s; fix it, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	if !config.WorkspaceSkillImportsEnabled(cfg) {
		return skillImportVetoRefusal(os.Stderr)
	}
	entries := config.SkillImports(cfg)
	if handle != "" {
		found := false
		for _, entry := range entries {
			if entry.Handle == handle {
				found = true
				break
			}
		}
		if !found {
			fmt.Fprintf(os.Stderr, "yolo skills update: %q is not a valid declared import; edit `skill_imports` or run `yolo skills add` first\n", termsafe.Visible(handle))
			return 1
		}
	}

	authority := skillImportCommandAuthority(workspace)
	var failures int
	for _, entry := range entries {
		if handle != "" && entry.Handle != handle {
			continue
		}
		previous, ok := store.Resolution(entry.Handle, entry)
		if !ok || !store.HasCommit(previous.Commit) {
			fmt.Fprintf(os.Stdout, "%s: no valid stored resolution; it will resolve on the next fresh launch, so no request was made.\n", termsafe.Visible(entry.Handle))
			continue
		}
		if entry.Rev != "" && entry.Ref == "" {
			fmt.Fprintf(os.Stdout, "%s: pinned at %s with no ref label; left unchanged. Add a ref label before updating.\n", termsafe.Visible(entry.Handle), previous.Commit)
			continue
		}
		var pinTarget config.ResolvedEditTarget
		pinMutation := configedit.Mutation{Action: configedit.Set, Path: []configdoc.Step{{Key: "skill_imports"}, {Key: entry.Handle}, {Key: "rev"}}, Value: entry.Rev}
		if entry.Rev != "" {
			var targetErr error
			pinTarget, targetErr = configedit.ResolveTarget(workspace, pinMutation, config.EditTargetChoice{Kind: config.EditTargetProject})
			if targetErr != nil {
				failures++
				fmt.Fprintf(os.Stderr, "%s: cannot prepare config update: %s; fix the named config, then retry `yolo skills update`\n", termsafe.Visible(entry.Handle), termsafe.Visible(targetErr.Error()))
				continue
			}
		}
		refresh := entry
		refresh.Rev = ""
		fresh, err := resolveSkillImportForCommand(context.Background(), store, refresh, authority)
		if err != nil {
			failures++
			fmt.Fprintf(os.Stderr, "%s: update refused or fetch failed; config and resolution were left unchanged: %s\n", termsafe.Visible(entry.Handle), termsafe.Visible(err.Error()))
			continue
		}
		if entry.Rev != "" {
			fresh.Rev = fresh.Commit
			fresh.ResolvedRef = ""
			pinMutation.Value = fresh.Commit
			result, targetErr := configedit.ApplyTargeted(workspace, pinTarget, pinMutation, configedit.ApplyOptions{HeldParentLock: store.CommandDirectoryLock(), Verify: func(_, _ []byte) error { return store.WriteResolution(fresh) }})
			// Configedit deliberately skips Verify on semantic no-ops. Refresh still replaces the
			// record (including access class and timestamp) under this complete skills transaction.
			if targetErr == nil && result.Plan.Noop {
				targetErr = store.WriteResolution(fresh)
			}

			if targetErr != nil {
				failures++
				_ = store.WriteResolution(previous)
				fmt.Fprintf(os.Stderr, "%s: config update failed; previous record restored: %s; fix the named config, then retry `yolo skills update`\n", termsafe.Visible(entry.Handle), termsafe.Visible(targetErr.Error()))
				continue
			}
		} else if err := store.WriteResolution(fresh); err != nil {
			failures++
			_ = store.WriteResolution(previous)
			fmt.Fprintf(os.Stderr, "%s: record update failed: %s; retry `yolo skills update`\n", termsafe.Visible(entry.Handle), termsafe.Visible(err.Error()))
			continue
		}

		if fresh.Commit == previous.Commit {
			fmt.Fprintf(os.Stdout, "%s: already at %s (%s access). Running jails keep their staged skills until the next attach or fresh launch.\n", termsafe.Visible(entry.Handle), fresh.Commit, termsafe.Visible(fresh.AccessClass))
		} else {
			fmt.Fprintf(os.Stdout, "%s: resolution moved from %s to %s (%s access). Running jails keep their staged skills until the next attach or fresh launch.\n", termsafe.Visible(entry.Handle), previous.Commit, fresh.Commit, termsafe.Visible(fresh.AccessClass))
		}
	}
	if failures > 0 {
		return 1
	}
	return 0
}

func skillsPin(args []string) int {
	return withSkillsWriterLock(args, func(args []string, store *skillimports.Store) int { return skillsPinLocked(args, store) })
}

func skillsPinLocked(args []string, store *skillimports.Store) int {
	handle, err := oneSkillHandle("pin", args, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills pin: %v; see `yolo skills --help`\n", err)
		return 2
	}
	workspace := configEditWorkspace()
	cfg, err := loadSkillCommandConfig(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills pin: cannot read this workspace's config: %s; fix it, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	entry, ok := findSkillImport(cfg, handle)
	if !ok {
		fmt.Fprintf(os.Stderr, "yolo skills pin: %q is not a valid declared import; edit `skill_imports` or run `yolo skills add` first\n", termsafe.Visible(handle))
		return 1
	}
	if entry.Rev != "" {
		fmt.Fprintf(os.Stdout, "%s is already pinned at %s.\n", termsafe.Visible(handle), entry.Rev)
		return 0
	}

	record, ok := store.Resolution(handle, entry)
	if !ok || !store.HasCommit(record.Commit) {
		fmt.Fprintf(os.Stderr, "%s has no valid stored resolution; it will resolve on the next fresh launch. Nothing was pinned.\n", termsafe.Visible(handle))
		return 1
	}
	if err := setSkillImportRev(workspace, handle, record.Commit, store.CommandDirectoryLock()); err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills pin: config update failed; nothing was pinned: %s\n", termsafe.Visible(err.Error()))
		return 1
	}
	fmt.Fprintf(os.Stdout, "%s is now pinned at %s. Running jails keep their staged skills until the next attach or fresh launch.\n", termsafe.Visible(handle), record.Commit)
	return 0
}

func skillsRemove(args []string) int {
	if config.InJail() {
		return skillsRemoveLocked(args, nil)
	}
	return withSkillsWriterLock(args, func(args []string, store *skillimports.Store) int { return skillsRemoveLocked(args, store) })
}

func skillsRemoveLocked(args []string, store *skillimports.Store) int {
	handle, err := oneSkillHandle("remove", args, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills remove: %v; see `yolo skills --help`\n", err)
		return 2
	}
	workspace := configEditWorkspace()
	cfg, err := loadSkillCommandConfig(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills remove: cannot read this workspace's config: %s; fix it, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	if !skillImportHandleExists(cfg, handle) {
		fmt.Fprintf(os.Stderr, "yolo skills remove: %q is not declared; nothing was changed. Run `yolo skills list` to see the workspace imports\n", termsafe.Visible(handle))
		return 1
	}
	mutation := configedit.Mutation{Action: configedit.Remove, Path: []configdoc.Step{{Key: "skill_imports"}, {Key: handle}}, PruneEmptiedTable: true}
	path, err := applySkillImportMutation(workspace, mutation, store.CommandDirectoryLock())
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills remove: cannot update the workspace config: %s; fix it, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	fmt.Fprintf(os.Stdout, "%s\nRemoved %q from workspace config. The host import store and resolution record were left alone; running jails keep their staged skills until the next attach or fresh launch.\n", path, termsafe.Visible(handle))
	return 0
}

func skillsList(args []string) int {
	if len(args) != 0 {
		fmt.Fprintln(os.Stderr, "yolo skills list takes no arguments; see `yolo skills --help`")
		return 2
	}
	workspace := configEditWorkspace()
	cfg, err := loadSkillCommandConfig(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills list: cannot read this workspace's config: %s; fix it, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	entries := config.SkillImports(cfg)
	if len(entries) == 0 {
		fmt.Fprintln(os.Stdout, "No skill_imports are declared in this workspace.")
		return 0
	}
	fmt.Fprintf(os.Stdout, "Workspace skill imports in %s:\n", termsafe.Visible(workspace))
	for _, entry := range entries {
		mode := "moving ref"
		selector := "ref " + entry.Ref
		if entry.Rev != "" {
			mode, selector = "pinned", "rev "+entry.Rev
		}
		fmt.Fprintf(os.Stdout, "  %s: %s %s, %s, path %s", termsafe.Visible(entry.Handle), mode,
			termsafe.Visible(shquote.QuoteDisplay(entry.Git)), selector, termsafe.Visible(entry.Path))
		if entry.OnlySet {
			quoted := make([]string, len(entry.Only))
			for i, name := range entry.Only {
				quoted[i] = termsafe.Visible(name)
			}
			fmt.Fprintf(os.Stdout, ", only [%s]", strings.Join(quoted, ", "))
		}
		fmt.Fprintln(os.Stdout)
	}
	if config.InJail() {
		fmt.Fprintln(os.Stdout, "Resolved commits and the import store are host-side and are not visible in this jail.")
		printSkillsHostRun([]string{"list"})
		return 0
	}
	if !config.WorkspaceSkillImportsEnabled(cfg) {
		fmt.Fprintln(os.Stdout, "Imports are disabled by the user-scope workspace_skill_imports veto; no skill would be staged.")
	}
	store, err := skillimports.Open(workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills list: host import state is unavailable: %s\n", termsafe.Visible(err.Error()))
		return 1
	}
	authority := skillImportCommandAuthority(workspace)
	inventory, inventoryErr := readSkillInventorySourcesForCommand(workspace)
	importedOwners := map[string]string{}
	var inspectionBudget skillimports.ProjectionBudget
	for _, entry := range entries {
		inspection, err := store.InspectWithBudget(entry, authority, &inspectionBudget)
		if err != nil {
			fmt.Fprintf(os.Stdout, "  %s: inspection unavailable: %s\n", termsafe.Visible(entry.Handle), termsafe.Visible(err.Error()))
			continue
		}
		if !inspection.HasResolution {
			fmt.Fprintf(os.Stdout, "  %s: no valid resolution record; it resolves on a fresh launch or `yolo skills add`.\n", termsafe.Visible(entry.Handle))
			continue
		}
		record := inspection.Resolution
		fmt.Fprintf(os.Stdout, "  %s: commit %s, resolved %s, obtained %s; stored=%t; current Git access=%s\n",
			termsafe.Visible(entry.Handle), record.Commit, record.ResolvedAt.Format(time.RFC3339),
			termsafe.Visible(record.AccessClass), inspection.CommitStored, termsafe.Visible(inspection.CurrentAccess))
		if !inspection.Allowed {
			fmt.Fprintln(os.Stdout, "    not currently permitted by this workspace's Git broker decision; nothing would be delivered.")
			continue
		}
		for _, refusal := range inspection.Refused {
			fmt.Fprintf(os.Stdout, "    refused %s: %s; inspect the source, then retry `yolo skills list`\n", termsafe.Visible(refusal.Path), termsafe.Visible(refusal.Reason))
		}
		fmt.Fprintf(os.Stdout, "    skills that would be projected: %s\n", displaySkillNames(inspection.Skills))
		fmt.Fprintf(os.Stdout, "    visible paths not imported: %s\n", displaySkillNames(inspection.Unimported))
		if inventoryErr != nil {
			fmt.Fprintf(os.Stdout, "    shadow inventory incomplete: %s; start this workspace with `yolo`, then run `yolo skills list` again.\n", termsafe.Visible(inventoryErr.Error()))
			continue
		}
		if len(inventory.Targets) == 0 {
			fmt.Fprintln(os.Stdout, "    no selected skills destinations are present in the last live pack tree; these imports are not delivered to an agent destination.")
			continue
		}
		shadows, err := inspectImportedSkillShadowsForCommand(&jailcontent.WorkspaceSkills{
			Root: workspace, Dirs: inventory.WorkspaceDirs,
		}, inventory.PackSources, inventory.Targets, inspection.Skills)
		if err != nil {
			fmt.Fprintf(os.Stdout, "    shadow inventory incomplete: %s; fix the named source, then run `yolo skills list` again.\n", termsafe.Visible(err.Error()))
			continue
		}
		for _, name := range inspection.Skills {
			if owner := importedOwners[name]; owner != "" {
				for _, target := range inventory.Targets {
					destination := "~/" + target.Dest
					higher := false
					for _, shadow := range shadows {
						if shadow.Name == name && shadow.Destination == destination {
							higher = true
							break
						}
					}
					if !higher {
						shadows = append(shadows, jailcontent.ImportedSkillShadow{Name: name, Destination: destination, By: "import " + owner})
					}
				}
			} else {
				importedOwners[name] = entry.Handle
			}
		}
		if len(shadows) == 0 {
			fmt.Fprintln(os.Stdout, "    shadowed names: none for the last live pack tree.")
			continue
		}
		fmt.Fprintln(os.Stdout, "    shadowed names:")
		for _, shadow := range shadows {
			fmt.Fprintf(os.Stdout, "      %s at %s by %s\n", termsafe.Visible(shadow.Name),
				termsafe.Visible(shadow.Destination), termsafe.Visible(shadow.By))
		}
	}
	fmt.Fprintln(os.Stdout, "Imported skills are the lowest-precedence source; workspace, built-in, selected-pack and local-pack skills may shadow matching names.")
	return 0
}

func displaySkillNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	out := make([]string, len(names))
	for i, name := range names {
		out[i] = termsafe.Visible(name)
	}
	return strings.Join(out, ", ")
}

func oneSkillHandle(verb string, args []string, optional bool) (string, error) {
	if len(args) == 0 && optional {
		return "", nil
	}
	if len(args) != 1 || strings.HasPrefix(args[0], "-") {
		return "", fmt.Errorf("name exactly one import handle")
	}
	return args[0], nil
}

func findSkillImport(cfg *jsonx.OrderedMap, handle string) (config.SkillImport, bool) {
	for _, entry := range config.SkillImports(cfg) {
		if entry.Handle == handle {
			return entry, true
		}
	}
	return config.SkillImport{}, false
}

func setSkillImportRev(workspace, handle, rev string, lock *configedit.DirectoryLock) error {
	mutation := configedit.Mutation{Action: configedit.Set,
		Path: []configdoc.Step{{Key: "skill_imports"}, {Key: handle}, {Key: "rev"}}, Value: rev}
	_, err := applySkillImportMutation(workspace, mutation, lock)
	return err
}

func applySkillImportMutation(workspace string, mutation configedit.Mutation, lock *configedit.DirectoryLock) (string, error) {
	target, err := configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetProject})
	if err != nil {
		return "", err
	}
	if mutation.Action == configedit.Set && !configEditPathPresent(target.ExpectedBytes(), target.Format, target.AuthoredPath()) {
		mutation.Action = configedit.Add
		target, err = configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetProject})
		if err != nil {
			return "", err
		}
	}
	result, err := configedit.ApplyTargeted(workspace, target, mutation, configedit.ApplyOptions{HeldParentLock: lock})
	if err != nil {
		return "", err
	}
	return result.Path, nil
}

func printSkillsHostRun(args []string) {
	workspace := os.Getenv("YOLO_HOST_DIR")
	if workspace == "" {
		workspace = config.JailWorkspace()
	}
	command := shquote.Join(append([]string{"yolo", "skills"}, args...))
	fmt.Fprintf(os.Stderr, "yolo skills: this command runs on the host; nothing ran here. Ask the human to paste this ONE line:\n  yolo host-run -C %s -- %s\n",
		shquote.QuoteDisplay(workspace), command)
}

// The skills transaction precedes configedit's physical-target lock. Locked handlers re-read
// declarations and authority, and Resolve never recursively reacquires the transaction lock.
func withSkillsWriterLock(args []string, command func([]string, *skillimports.Store) int) int {
	store, err := skillimports.Open(configEditWorkspace())
	if err != nil {
		fmt.Fprintf(os.Stderr, "yolo skills: host import state is unavailable: %s; fix host storage, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	rc := 1
	commandFailed := errors.New("skills command did not commit")
	err = store.WithCommandLock(func(locked *skillimports.Store) error {
		rc = command(args, locked)
		if rc != 0 {
			return commandFailed
		}
		return nil
	})
	if err != nil && err != commandFailed {
		fmt.Fprintf(os.Stderr, "yolo skills: transaction failed: %s; fix host storage, then retry\n", termsafe.Visible(err.Error()))
		return 1
	}
	return rc
}

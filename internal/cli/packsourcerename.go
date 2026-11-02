package cli

// packsourcerename.go is `yolo pack source rename OLD NEW` (docs/design/pack-naming.md §3.6): it
// renames one `[pack_sources]` key, every handle that uses it, and every user-config table keyed by
// one of those handles (`[allow."OLD/x"]`), as one validated write of the user config, after
// showing the edit and every agent-facing name it changes and asking. A source's key IS its
// namespace (PN-D11), so this is the one way to change the word a source's skills carry, and the
// reason the verb asks first.
//
// Records follow the pack, not the handle (PN-D10): the verb moves each renamed pack's pins and
// records to its new handle right after the write (configmigrate.ReconcilePackRecords, which the
// next launch would otherwise run), and fetches nothing, so no pack moves to a newer commit. Only
// the user config names a source (`packs` and `pack_sources` are user-scope keys), so the whole
// edit is one file, written once through configedit's transaction (ApplyOptions.Then): either
// every reference is renamed or none is.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/cli/run"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

const packSourceUsage = `yolo pack source rename OLD NEW [--dry-run] [--yes]
  rename the [pack_sources] key OLD to NEW, every handle that uses it (OLD/x becomes NEW/x) and
  every table keyed by one of those handles ([allow."OLD/x"]), as one edit of your user config.
  The key is the namespace the source's skills, plugins and files are delivered under, so the edit
  lists every agent-facing name that changes and asks before writing; --yes answers it, --dry-run
  only shows it. Each pack's pins and records follow it to its new handle; nothing is fetched.`

// packKeyedTables are the user-config tables whose keys may be pack handles (`requests` is a
// workspace key, which this user-config edit never reaches).
var packKeyedTables = []string{"allow"}

func packSource(args []string, in io.Reader, out, errw io.Writer) int {
	if len(args) == 0 || args[0] != "rename" {
		fmt.Fprintln(errw, packSourceUsage)
		return 2
	}
	var names []string
	dryRun, yes := false, false
	for _, a := range args[1:] {
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "--yes" || a == "-y":
			yes = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack source rename: unknown flag %s\n\n%s\n", a, packSourceUsage)
			return 2
		default:
			names = append(names, a)
		}
	}
	if len(names) != 2 {
		fmt.Fprintf(errw, "yolo pack source rename: name the source and its new key, for example "+
			"`yolo pack source rename acme tools`\n\n%s\n", packSourceUsage)
		return 2
	}
	oldKey, newKey := names[0], names[1]
	if err := packname.Word("source key", newKey); err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: %v\n", err)
		return 2
	}
	if newKey == "local" || newKey == "yolo" {
		fmt.Fprintf(errw, "yolo pack source rename: %q is yolo's own word for a pack source; choose another key\n", newKey)
		return 2
	}
	workspace := configEditWorkspace()
	if config.InJail() {
		// The host-run line runs with no terminal to answer the y/N question, so it answers it:
		// the user has read the plan this line came with, and runs it to make that edit.
		hostArgs := args
		if !yes && !dryRun {
			hostArgs = append(append([]string(nil), args...), "--yes")
		}
		printPackHostRun(errw, workspace, "source", hostArgs)
		return 1
	}
	planned, target, rc := planSourceRename(workspace, oldKey, newKey, errw)
	if rc != 0 {
		return rc
	}
	// The names agents see, read before the edit: the source's packs as they are delivered now.
	changes := renamedAgentNames(oldKey, newKey)

	result, err := configedit.ApplyTargeted(workspace, target, planned.first,
		configedit.ApplyOptions{DryRun: true, Then: planned.then})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: refused: %v — nothing written. The same rename, one "+
			"edit at a time, is:\n%s", err, renameAsCommands(planned))
		return 1
	}
	writeConfigEditPreexisting(errw, result)
	fmt.Fprintf(out, "%s\n", result.Path)
	writeConfigEditHunks(out, result.Plan.Hunks)
	fmt.Fprintf(out, "Renames source %q to %q, %d handle(s) that use it", oldKey, newKey, planned.handles)
	if len(planned.tables) > 0 {
		fmt.Fprintf(out, ", and the table(s) %s", strings.Join(planned.tables, ", "))
	}
	fmt.Fprintln(out, ".")
	if len(changes) == 0 {
		fmt.Fprintln(out, "No agent-facing name changes: the source is bare, or no selected pack delivers from it.")
	} else {
		fmt.Fprintln(out, "Agent-facing names that change (an agent invokes the new name after the next launch or `yolo host apply`):")
		for _, c := range changes {
			fmt.Fprintf(out, "  %s\n", c)
		}
	}
	fmt.Fprintln(out, "Each pack's pins and records follow it to its new handle; nothing is fetched or updated.")
	if dryRun {
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	if !yes {
		fmt.Fprint(out, "Rename? [y/N] ")
		answer, _ := bufio.NewReader(in).ReadString('\n')
		answer = strings.ToLower(strings.TrimSpace(answer))
		if answer != "y" && answer != "yes" {
			fmt.Fprintln(out, "Nothing written. Run it again with --yes to rename without asking.")
			return 1
		}
	}
	// Before the write, the index learns which handle each pack's records are under now, so the
	// read after it can move them; on a machine whose last launch predates the index this is the
	// migration of its records (configmigrate.ReconcilePackRecords).
	reconcileSelectedRecords(out, errw)

	// Re-read and re-plan by key: what is written is planned over the file as it is now, and only
	// if that is the edit that was shown. A `packs[N]` index planned over an older read is never
	// written into a file someone changed meanwhile.
	again, target, rc := planSourceRename(workspace, oldKey, newKey, errw)
	if rc != 0 {
		return rc
	}
	check, err := configedit.ApplyTargeted(workspace, target, again.first,
		configedit.ApplyOptions{DryRun: true, Then: again.then})
	if err == nil && !bytes.Equal(check.Plan.Updated, result.Plan.Updated) {
		err = fmt.Errorf("%s changed after the edit above was shown", target.LogicalPath)
	}
	if err == nil {
		_, err = configedit.ApplyTargeted(workspace, target, again.first, configedit.ApplyOptions{Then: again.then})
	}
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: refused: %v — nothing written. Run it again to see the "+
			"edit over the file as it is now\n", err)
		return 1
	}
	fmt.Fprintf(out, "Renamed source %q to %q.\n", oldKey, newKey)
	// After it, by the new handles: each pack's records move to its new handle, nothing fetched.
	reconcileSelectedRecords(out, errw)
	fmt.Fprintln(out, "Agents see the new names after the next launch or `yolo host apply`.")
	return 0
}

// renamePlan is the user-config edit renaming one source key: first adds the source under its new
// key, then renames every handle and handle-keyed table and removes the old key.
type renamePlan struct {
	first   configedit.Mutation
	then    []configedit.Mutation
	handles int
	tables  []string
}

// planSourceRename reads the user config and plans the rename over it, by key: each mutation's
// path is computed from this read.
func planSourceRename(workspace, oldKey, newKey string, errw io.Writer) (renamePlan, config.ResolvedEditTarget, int) {
	first := configedit.Mutation{Action: configedit.Add,
		Path: []configdoc.Step{{Key: "pack_sources"}, {Key: newKey}}}
	target, err := configedit.ResolveTarget(workspace, first, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: cannot edit the user config: %v\n", err)
		return renamePlan{}, target, 1
	}
	doc, err := configdoc.Parse(target.ExpectedBytes(), target.Format)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: cannot read %s: %v — fix that file, then run this again\n",
			target.LogicalPath, err)
		return renamePlan{}, target, 1
	}
	rawSources, _ := doc.Values.Get("pack_sources")
	sources, _ := rawSources.(packsrc.Object)
	if sources == nil {
		fmt.Fprintf(errw, "yolo pack source rename: %s has no [pack_sources]; nothing to rename. "+
			"`yolo pack ls` lists where each configured pack comes from\n", target.LogicalPath)
		return renamePlan{}, target, 1
	}
	definition, ok := sources.Get(oldKey)
	if !ok {
		fmt.Fprintf(errw, "yolo pack source rename: %s has no source %q; its sources are %s\n",
			target.LogicalPath, oldKey, strings.Join(sources.Keys(), ", "))
		return renamePlan{}, target, 1
	}
	if _, taken := sources.Get(newKey); taken {
		fmt.Fprintf(errw, "yolo pack source rename: %s already has a source %q; choose another new key, "+
			"or remove that one first with `%s` (the packs that read from it then stop resolving)\n",
			target.LogicalPath, newKey, shquote.Join([]string{"yolo", "config", "unset", "pack_sources." + configKeyComponent(newKey), "--user"}))
		return renamePlan{}, target, 1
	}
	first.Value = definition
	plan := renamePlan{first: first}
	plan.then, plan.handles = renamedHandles(doc, oldKey, newKey)
	tables, renamedTables, err := renamedTables(doc, oldKey, newKey)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: %v — nothing written\n", err)
		return renamePlan{}, target, 1
	}
	plan.then = append(plan.then, tables...)
	plan.tables = renamedTables
	plan.then = append(plan.then, configedit.Mutation{Action: configedit.Remove,
		Path: []configdoc.Step{{Key: "pack_sources"}, {Key: oldKey}}})
	return plan, target, 0
}

// renamedHandles is the edits that point every `packs` handle under oldKey at newKey: a string
// item, or a table's `pack` field. Other items are left alone.
func renamedHandles(doc *configdoc.Document, oldKey, newKey string) ([]configedit.Mutation, int) {
	raw, _ := doc.Values.Get("packs")
	list, _ := raw.([]any)
	prefix := oldKey + "/"
	var out []configedit.Mutation
	for i, item := range list {
		switch v := item.(type) {
		case string:
			if strings.HasPrefix(v, prefix) {
				out = append(out, configedit.Mutation{Action: configedit.Set,
					Path:  []configdoc.Step{{Key: "packs"}, {Index: i, IsIndex: true}},
					Value: newKey + "/" + strings.TrimPrefix(v, prefix)})
			}
		case packsrc.Object:
			handle, _ := v.Get("pack")
			if s, ok := handle.(string); ok && strings.HasPrefix(s, prefix) {
				out = append(out, configedit.Mutation{Action: configedit.Set,
					Path:  []configdoc.Step{{Key: "packs"}, {Index: i, IsIndex: true}, {Key: "pack"}},
					Value: newKey + "/" + strings.TrimPrefix(s, prefix)})
			}
		}
	}
	return out, len(out)
}

// renamedTables is the edits that move every table keyed by a handle under oldKey
// (`[allow."OLD/x"]`) to its new handle, and the tables' new names. A new
// handle's table that already exists is refused rather than merged.
func renamedTables(doc *configdoc.Document, oldKey, newKey string) ([]configedit.Mutation, []string, error) {
	prefix := oldKey + "/"
	var out []configedit.Mutation
	var names []string
	for _, root := range packKeyedTables {
		raw, _ := doc.Values.Get(root)
		table, _ := raw.(packsrc.Object)
		if table == nil {
			continue
		}
		for _, key := range table.Keys() {
			if !strings.HasPrefix(key, prefix) {
				continue
			}
			to := newKey + "/" + strings.TrimPrefix(key, prefix)
			if _, taken := table.Get(to); taken {
				return nil, nil, fmt.Errorf("[%s.%q] and [%s.%q] both exist, and the rename would make them one: "+
					"`yolo config why %s` shows both, and `%s` removes the second, after which this rename moves the "+
					"first in its place", root, key, root, to, root,
					shquote.Join([]string{"yolo", "config", "unset", root + "." + configKeyComponent(to), "--user"}))
			}
			value, _ := table.Get(key)
			out = append(out,
				configedit.Mutation{Action: configedit.Add, Path: []configdoc.Step{{Key: root}, {Key: to}}, Value: value},
				configedit.Mutation{Action: configedit.Remove, Path: []configdoc.Step{{Key: root}, {Key: key}}})
			names = append(names, fmt.Sprintf("[%s.%q]", root, to))
		}
	}
	return out, names, nil
}

// reconcileSelectedRecords moves the configured packs' records to the handles the user config
// gives them now (run.ReconcilePackRecords), printing each pack whose records moved.
func reconcileSelectedRecords(out, errw io.Writer) {
	entries, err := config.LoadPacks(func(string) {})
	if err != nil {
		fmt.Fprintf(errw, "Warning: the configured packs could not be read (%v), so their records were not "+
			"moved; the next launch moves them\n", err)
		return
	}
	run.ReconcilePackRecords(entries, func(line string) { fmt.Fprintln(out, line) },
		func(line string) { fmt.Fprintf(errw, "Warning: %s\n", line) })
}

// renamedAgentNames is "old → new" for every agent-facing name the rename changes: each selected
// pack of the source's files landing and /ctx directory, and every skill and plugin it delivers.
// A pack that cannot be read is named as such rather than skipped silently.
func renamedAgentNames(oldKey, newKey string) []string {
	sel := selectHostPacks(func(e config.PackEntry) (*packload.Pack, error) {
		return resolveConfiguredPackWithSpec(e, hostPackResolveSpec(true))
	}, config.UserScopeSelection())
	if sel.loadErr != nil {
		return []string{fmt.Sprintf("(the configured packs could not be read: %v; `yolo check` says why)", sel.loadErr)}
	}
	owner := "source:" + oldKey
	var out []string
	var loaded []*packload.Pack
	for _, p := range sel.packs {
		if p.NamespaceOwner != owner {
			continue
		}
		renamed := newKey + strings.TrimPrefix(p.Landing, oldKey)
		out = append(out, fmt.Sprintf("files and /ctx/host-%s: %s → %s", p.Landing, p.Landing, renamed))
		loaded = append(loaded, p)
	}
	for _, u := range sel.unresolved {
		if strings.HasPrefix(u.Name, oldKey+"/") {
			out = append(out, fmt.Sprintf("pack %s: its skills could not be read (%s); each is renamed %s-<skill> → %s-<skill>",
				u.Name, u.Reason, oldKey, newKey))
		}
	}
	for _, name := range hostskills.NamesInNamespace(hostskills.ComposeHostSkills(loaded, paths.Home()), oldKey) {
		out = append(out, fmt.Sprintf("skill %s → %s", name, newKey+strings.TrimPrefix(name, oldKey)))
	}
	return out
}

// packSourceStdin is the reader the confirmation reads: the terminal, in the real command.
var packSourceStdin io.Reader = os.Stdin

// renameAsCommands is the planned rename as the `yolo config` commands that make the same edits,
// one per line, for when the verb cannot make them in one write.
func renameAsCommands(plan renamePlan) string {
	var b strings.Builder
	for _, m := range append([]configedit.Mutation{plan.first}, plan.then...) {
		argv := []string{"yolo", "config"}
		switch m.Action {
		case configedit.Remove:
			argv = append(argv, "unset", configEditKey(m.Path))
		default:
			value, err := configdoc.EncodeValue(m.Value, configdoc.TOML)
			if err != nil {
				continue
			}
			argv = append(argv, "set", configEditKey(m.Path), string(value))
		}
		fmt.Fprintf(&b, "  %s\n", shquote.Join(append(argv, "--user")))
	}
	return b.String()
}

// configEditKey is path as `yolo config set` and `unset` read a key (parseConfigEditKey).
func configEditKey(path []configdoc.Step) string {
	var b strings.Builder
	for i, s := range path {
		if s.IsIndex {
			fmt.Fprintf(&b, "[%d]", s.Index)
			continue
		}
		if i > 0 {
			b.WriteByte('.')
		}
		b.WriteString(configKeyComponent(s.Key))
	}
	return b.String()
}

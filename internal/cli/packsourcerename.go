package cli

// packsourcerename.go is `yolo pack source rename OLD NEW` (docs/design/pack-naming.md §3.6):
// it renames one `[pack_sources]` key and rewrites every handle that uses it, as one validated
// write of the user config, after showing the edit and every agent-facing name it changes and
// asking. A source's key IS its namespace (PN-D11), so this is the one way to change the word a
// source's skills carry, and the reason the verb asks first.
//
// No record moves (PN-D10): records key on where a pack lives, which a rename does not change.
// Only the user config names a source (`packs` and `pack_sources` are user-scope keys), so the
// whole edit is one file, written once through configedit's transaction (ApplyOptions.Then):
// either every reference is renamed or none is.

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/hostskills"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packname"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const packSourceUsage = `yolo pack source rename OLD NEW [--dry-run] [--yes]
  rename the [pack_sources] key OLD to NEW and every handle that uses it (OLD/x becomes NEW/x),
  as one edit of your user config. The key is the namespace the source's skills, plugins and
  files are delivered under, so the edit lists every agent-facing name that changes and asks
  before writing; --yes answers it, --dry-run only shows it. Nothing yolo recorded moves.`

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
		printPackHostRun(errw, workspace, "source", args)
		return 1
	}
	first := configedit.Mutation{Action: configedit.Add,
		Path: []configdoc.Step{{Key: "pack_sources"}, {Key: newKey}}}
	target, err := configedit.ResolveTarget(workspace, first, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: cannot edit the user config: %v\n", err)
		return 1
	}
	doc, err := configdoc.Parse(target.ExpectedBytes(), target.Format)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: cannot read %s: %v — fix that file, then run this again\n",
			target.LogicalPath, err)
		return 1
	}
	rawSources, _ := doc.Values.Get("pack_sources")
	sources, _ := rawSources.(packsrc.Object)
	if sources == nil {
		fmt.Fprintf(errw, "yolo pack source rename: %s has no [pack_sources]; nothing to rename. "+
			"`yolo pack ls` lists where each configured pack comes from\n", target.LogicalPath)
		return 1
	}
	definition, ok := sources.Get(oldKey)
	if !ok {
		fmt.Fprintf(errw, "yolo pack source rename: %s has no source %q; its sources are %s\n",
			target.LogicalPath, oldKey, strings.Join(sources.Keys(), ", "))
		return 1
	}
	if _, taken := sources.Get(newKey); taken {
		fmt.Fprintf(errw, "yolo pack source rename: %s already has a source %q; choose another new key, "+
			"or remove that one first\n", target.LogicalPath, newKey)
		return 1
	}
	first.Value = definition
	then, rewritten := renamedHandles(doc, oldKey, newKey)
	then = append(then, configedit.Mutation{Action: configedit.Remove,
		Path: []configdoc.Step{{Key: "pack_sources"}, {Key: oldKey}}})

	// The names agents see, read before the edit: the source's packs as they are delivered now.
	changes := renamedAgentNames(oldKey, newKey)

	result, err := configedit.ApplyTargeted(workspace, target, first, configedit.ApplyOptions{DryRun: true, Then: then})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: refused: %v — nothing written. Rename it by hand in %s, "+
			"then run `yolo check`\n", err, target.LogicalPath)
		return 1
	}
	fmt.Fprintf(out, "%s\n", result.Path)
	writeConfigEditHunks(out, result.Plan.Hunks)
	fmt.Fprintf(out, "Renames source %q to %q and %d handle(s) that use it.\n", oldKey, newKey, rewritten)
	if len(changes) == 0 {
		fmt.Fprintln(out, "No agent-facing name changes: the source is bare, or no selected pack delivers from it.")
	} else {
		fmt.Fprintln(out, "Agent-facing names that change (an agent invokes the new name after the next launch or `yolo host apply`):")
		for _, c := range changes {
			fmt.Fprintf(out, "  %s\n", c)
		}
	}
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
	target, err = configedit.ResolveTarget(workspace, first, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err == nil {
		_, err = configedit.ApplyTargeted(workspace, target, first, configedit.ApplyOptions{Then: then})
	}
	if err != nil {
		fmt.Fprintf(errw, "yolo pack source rename: refused: %v — nothing written. Run it again, or rename "+
			"it by hand in %s and run `yolo check`\n", err, target.LogicalPath)
		return 1
	}
	fmt.Fprintf(out, "Renamed source %q to %q. It is read at the next launch.\n", oldKey, newKey)
	return 0
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

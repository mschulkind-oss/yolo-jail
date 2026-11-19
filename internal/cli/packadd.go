package cli

// packadd.go is `yolo pack add <handle>` and `yolo pack remove <handle>`: the two named verbs
// that edit the user config's `packs` list (docs/reference/config/editing.md#the-commands and §4).
//
// They earn a command over a text edit by what a text edit cannot do: add resolves the handle
// to the source it names and says where it was found, refuses a duplicate before writing, and
// says when the pack is read; remove changes the one item's own bytes and nothing around it, so
// a neighboring comment stays where it is. Both write through configedit.ApplyTargeted, the
// same transaction `yolo config add/unset` uses, so the edit is validated against the whole
// effective config before it lands and verified after.
//
// THE USER CONFIG IS THE HOST'S. In a jail both verbs print the one `yolo host-run` line that
// does the edit and write nothing. Shipped names are not looked up here: a bare name that is
// not a shipped pack is refused by the edit validation, which reads the captured embedded set,
// so this file never reads packload.Embedded* to reserve or check a name.

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configedit"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func printPackHostRun(errw io.Writer, workspace, verb string, args []string) {
	command := shquote.Join(append([]string{"yolo", "pack", verb}, args...))
	fmt.Fprintf(errw, "the pack list is the user config's, which only the host edits; run: "+
		"yolo host-run -C %s --name pack-%s -- %s\n", shquote.QuoteDisplay(workspace), verb, command)
}

func packAdd(args []string, out, errw io.Writer) int {
	options, rc := packEditArgs("add", args, errw)
	dryRun := options.DryRun
	if rc != 0 {
		return rc
	}
	workspace := configEditWorkspace()
	if config.InJail() {
		printPackHostRun(errw, workspace, "add", args)
		return 1
	}
	value := options.value()
	entry, err := packEditReference(workspace, value)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: %v — nothing written\n", err)
		return 1
	}
	handle := entry.Name
	mutation := configedit.Mutation{Action: configedit.Append, Path: []configdoc.Step{{Key: "packs"}}, Value: value}
	target, err := configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: cannot edit the user config: %v\n", err)
		return 1
	}
	if _, table := value.(string); !table && target.Format != configdoc.TOML {
		fmt.Fprintln(errw, "yolo pack add: table options require TOML; run yolo config migrate, then retry — nothing written")
		return 1
	}
	entries, err := authoredPackEntries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: %v\n", err)
		return 1
	}
	for i, previous := range entries {
		if previous.ImportIdentity() == entry.ImportIdentity() {
			fmt.Fprintf(errw, "yolo pack add: %q is already in %s at packs[%d] (%s); nothing written. To edit it: yolo config set packs[%d]; to drop it: yolo pack remove %s\n", handle, target.LogicalPath, i, previous.ConfigOrigin, i, handle)
			return 1
		}
	}
	found := "direct Git repository"
	if options.Git == "" {
		found, err = packHandleSource(workspace, handle)
	}
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: %v\n", err)
		return 1
	}
	// A user config that writes no `packs` list yet (the one `yolo init-user-config` writes,
	// or any file that never selected a pack) gets the list, holding this pack, as
	// `yolo config add` does: the append would otherwise refuse a missing value.
	mutation, target, ok := addMissingKey(workspace, target, mutation, value,
		config.EditTargetChoice{Kind: config.EditTargetUser}, errw)
	if !ok {
		return 1
	}
	result, err := configedit.ApplyTargeted(workspace, target, mutation, configedit.ApplyOptions{DryRun: dryRun})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: refused: %v — nothing written. `yolo pack ls` lists the packs "+
			"yolo ships; `yolo pack --help` says how to name another\n", err)
		return 1
	}
	writeConfigEditPreexisting(errw, result)
	fmt.Fprintf(out, "%s\n", result.Path)
	writeConfigEditHunks(out, result.Plan.Hunks)
	if dryRun {
		if entry.Skills != nil && !printScopedPackInspection(out, errw, entry, false) {
			return 1
		}
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	fmt.Fprintf(out, "Added %q (%s). It is read at the next launch; `yolo check` reports what it delivers.\n", handle, found)
	return 0
}

func packRemove(args []string, out, errw io.Writer) int {
	options, rc := packEditArgs("remove", args, errw)
	handle, dryRun := options.Handle, options.DryRun
	if rc != 0 {
		return rc
	}
	workspace := configEditWorkspace()
	if config.InJail() {
		printPackHostRun(errw, workspace, "remove", args)
		return 1
	}
	entries, err := authoredPackEntries()
	if err != nil {
		fmt.Fprintf(errw, "yolo pack remove: %v\n", err)
		return 1
	}
	at := -1
	var matches []int
	for i, entry := range entries {
		if entry.Name != handle {
			continue
		}
		if options.HasSubdir && entry.Subdir != options.Subdir {
			continue
		}
		dir := ""
		if entry.Skills != nil {
			dir = entry.Skills.Dir
		}
		if options.HasSkillsDir && dir != options.SkillsDir {
			continue
		}
		matches = append(matches, i)
	}
	if len(matches) > 1 {
		fmt.Fprintf(errw, "yolo pack remove: %q is ambiguous; nothing written. Choose --subdir/--skills-dir, or remove one exact indexed path:\n", handle)
		for _, i := range matches {
			entry := entries[i]
			dir := ""
			if entry.Skills != nil {
				dir = entry.Skills.Dir
			}
			fmt.Fprintf(errw, "  packs[%d] subdir=%q skills.dir=%q (%s): yolo config unset 'packs[%d]' --user\n", i, entry.Subdir, dir, entry.ConfigOrigin, i)
		}
		return 1
	}
	if len(matches) == 1 {
		at = matches[0]
	}
	if at < 0 {
		if config.IsSelectedByDefault(handle) {
			// Removing nothing cannot deselect it, and "not in packs" would read as already gone.
			fmt.Fprintf(errw, "yolo pack remove: %q is not in %s; nothing written. %s\n", handle,
				paths.UserConfigDisplayPath(), config.SelectedByDefaultNoOff(handle))
			return 1
		}
		fmt.Fprintf(errw, "yolo pack remove: %q is not in %s; nothing written. `yolo pack ls` lists the "+
			"packs a launch reads and where each is listed\n", handle, paths.UserConfigDisplayPath())
		return 1
	}
	mutation := configedit.Mutation{Action: configedit.Remove, Path: []configdoc.Step{{Key: "packs"}, {Index: at, IsIndex: true}}}
	target, err := configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack remove: cannot edit the user config: %v\n", err)
		return 1
	}
	result, err := configedit.ApplyTargeted(workspace, target, mutation, configedit.ApplyOptions{DryRun: dryRun})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack remove: refused: %v — nothing written. Edit %s by hand, then run `yolo check`\n",
			err, target.LogicalPath)
		return 1
	}
	writeConfigEditPreexisting(errw, result)
	fmt.Fprintf(out, "%s\n", result.Path)
	writeConfigEditHunks(out, result.Plan.Hunks)
	if dryRun {
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	if config.IsSelectedByDefault(handle) {
		// The line is gone, but the pack is not: the next launch selects it anyway.
		fmt.Fprintf(out, "Removed %q from packs. %s.\n", handle, config.SelectedByDefaultNoOff(handle))
		return 0
	}
	fmt.Fprintf(out, "Removed %q. The next launch no longer reads it.\n", handle)
	return 0
}

// packHandleSource says where a handle's pack lives, and refuses a local one whose directory is
// missing (the add would otherwise land and fail at the next launch). A bare name is checked by
// the edit validation, so it is reported as shipped without a lookup here.
func packHandleSource(workspace, handle string) (string, error) {
	if !strings.Contains(handle, "/") {
		return "shipped with yolo", nil
	}
	definitions := map[string]packsrc.SourceDefinition{}
	if effective, err := config.LoadReadableConfig(workspace, false, nil); err == nil && effective != nil {
		if raw, ok := effective.Get("pack_sources"); ok {
			if parsed, perr := packsrc.ParseSourceDefinitions(raw); perr == nil {
				definitions = parsed
			}
		}
	}
	userDir := filepath.Dir(paths.UserConfigPath())
	ref, err := packsrc.ParsePackReference(handle, definitions, filepath.Join(userDir, "packs"), paths.Home(),
		"pack handle", map[string]bool{})
	if err != nil {
		// yolo/<name> names a shipped pack, which only the edit validation can confirm.
		if strings.HasPrefix(handle, "yolo/") {
			return "shipped with yolo", nil
		}
		return "", fmt.Errorf("%w — a handle is a shipped name or source/pack, such as local/matt", err)
	}
	if ref.LocalPath != "" {
		if info, statErr := os.Stat(ref.LocalPath); statErr != nil || !info.IsDir() {
			return "", fmt.Errorf("%q names %s, which is not a directory — create it with "+
				"`yolo pack init %s`, then run `yolo pack add %s` again", handle, ref.LocalPath,
				shquote.QuoteDisplay(ref.LocalPath), handle)
		}
		return "found at " + ref.LocalPath, nil
	}
	return "from " + ref.Source, nil
}

// packAddInProcess is `yolo pack add <handle>` for a caller that wants a verdict, not an exit
// code: the pack-switch nudge in `yolo check`. A refusal's own words (which end in the next step)
// are the error.
func packAddInProcess(handle string) error {
	var out, errw strings.Builder
	if packAdd([]string{handle}, &out, &errw) != 0 {
		return fmt.Errorf("%s", strings.TrimSpace(errw.String()))
	}
	return nil
}

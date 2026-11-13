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

// packEditArgs parses `<handle> [--dry-run]` for add and remove.
func packEditArgs(verb string, args []string, errw io.Writer) (handle string, dryRun bool, rc int) {
	for _, a := range args {
		switch {
		case a == "--dry-run":
			dryRun = true
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack %s: unknown flag %s — it takes one pack handle and --dry-run "+
				"(see `yolo pack --help`)\n", verb, a)
			return "", false, 2
		case handle != "":
			fmt.Fprintf(errw, "yolo pack %s: one pack handle at a time, got a second: %q\n", verb, a)
			return "", false, 2
		default:
			handle = a
		}
	}
	if handle == "" {
		fmt.Fprintf(errw, "yolo pack %s: name the pack, for example `yolo pack %s local/matt` "+
			"(`yolo pack --help`)\n", verb, verb)
		return "", false, 2
	}
	if strings.Contains(handle, "://") {
		fmt.Fprintf(errw, "yolo pack %s: %q is an address, not a handle; `pack %s` takes a shipped name or "+
			"source/pack such as local/matt. To list a git pack, run "+
			"`yolo config add packs '{ git = \"URL\" }' --user`\n", verb, handle, verb)
		return "", false, 2
	}
	return handle, dryRun, 0
}

func printPackHostRun(errw io.Writer, workspace, verb string, args []string) {
	command := shquote.Join(append([]string{"yolo", "pack", verb}, args...))
	fmt.Fprintf(errw, "the pack list is the user config's, which only the host edits; run: "+
		"yolo host-run -C %s --name pack-%s -- %s\n", shquote.QuoteDisplay(workspace), verb, command)
}

// userPackItems is the user target's authored `packs` strings, by list index, with isString false
// for an item that is not a plain string (a table), which neither verb matches by handle.
func userPackItems(target config.ResolvedEditTarget) (items []string, isString []bool, err error) {
	data := target.ExpectedBytes()
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil, nil, nil
	}
	doc, err := configdoc.Parse(data, target.Format)
	if err != nil {
		return nil, nil, err
	}
	raw, _ := doc.Values.Get("packs")
	list, _ := raw.([]any)
	for _, item := range list {
		s, ok := item.(string)
		items = append(items, s)
		isString = append(isString, ok)
	}
	return items, isString, nil
}

func packAdd(args []string, out, errw io.Writer) int {
	handle, dryRun, rc := packEditArgs("add", args, errw)
	if rc != 0 {
		return rc
	}
	workspace := configEditWorkspace()
	if config.InJail() {
		printPackHostRun(errw, workspace, "add", args)
		return 1
	}
	mutation := configedit.Mutation{Action: configedit.Append, Path: []configdoc.Step{{Key: "packs"}}, Value: handle}
	target, err := configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: cannot edit the user config: %v\n", err)
		return 1
	}
	items, isString, err := userPackItems(target)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: cannot read %s: %v — fix that file, then run `yolo pack add %s` again\n",
			target.LogicalPath, err, handle)
		return 1
	}
	for i, item := range items {
		if isString[i] && item == handle {
			fmt.Fprintf(errw, "yolo pack add: %q is already in %s; nothing written. "+
				"To drop it: `yolo pack remove %s`\n", handle, target.LogicalPath, handle)
			return 1
		}
	}
	found, err := packHandleSource(workspace, handle)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack add: %v\n", err)
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
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	fmt.Fprintf(out, "Added %q (%s). It is read at the next launch; `yolo check` reports what it delivers.\n", handle, found)
	return 0
}

func packRemove(args []string, out, errw io.Writer) int {
	handle, dryRun, rc := packEditArgs("remove", args, errw)
	if rc != 0 {
		return rc
	}
	workspace := configEditWorkspace()
	if config.InJail() {
		printPackHostRun(errw, workspace, "remove", args)
		return 1
	}
	probe := configedit.Mutation{Action: configedit.Remove, Path: []configdoc.Step{{Key: "packs"}, {Index: 0, IsIndex: true}}}
	target, err := configedit.ResolveTarget(workspace, probe, config.EditTargetChoice{Kind: config.EditTargetUser})
	if err != nil {
		fmt.Fprintf(errw, "yolo pack remove: cannot edit the user config: %v\n", err)
		return 1
	}
	items, isString, err := userPackItems(target)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack remove: cannot read %s: %v — fix that file, then run `yolo pack remove %s` again\n",
			target.LogicalPath, err, handle)
		return 1
	}
	at := -1
	for i, item := range items {
		if isString[i] && item == handle {
			at = i
			break
		}
	}
	if at < 0 {
		if config.IsSelectedByDefault(handle) {
			// Removing nothing cannot deselect it, and "not in packs" would read as already gone.
			fmt.Fprintf(errw, "yolo pack remove: %q is not in %s; nothing written. %s\n", handle,
				target.LogicalPath, config.SelectedByDefaultNoOff(handle))
			return 1
		}
		fmt.Fprintf(errw, "yolo pack remove: %q is not in %s; nothing written. `yolo pack ls` lists the "+
			"packs a launch reads and where each is listed\n", handle, target.LogicalPath)
		return 1
	}
	mutation := configedit.Mutation{Action: configedit.Remove, Path: []configdoc.Step{{Key: "packs"}, {Index: at, IsIndex: true}}}
	target, err = configedit.ResolveTarget(workspace, mutation, config.EditTargetChoice{Kind: config.EditTargetUser})
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

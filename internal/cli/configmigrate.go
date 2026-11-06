package cli

// configmigrate.go is `yolo config migrate`: the one-time conversion of the old JSONC config
// files into the TOML files that replace them (docs/design/config-migration.md §1 and §4). The
// work is internal/configmigrate's: it reads the whole old graph, proves the converted files say
// what the old ones say, and writes nothing otherwise. This verb picks what the migration covers,
// shows the library's transcript, asks, and applies.
//
// WHAT IT COVERS (§4 item 6). On the host: the user config with everything it includes and its
// config.d fragments, plus this workspace's project and local files. In a jail: only this
// workspace's files, since the user config there is the host's, inherited for the launch. Named
// files (`yolo config migrate FILE...`, RC-D46 as amended 2026-11-01) are converted on their own
// instead, each as the --user-layer over the user config, and nothing else is.
//
// IT ASKS BEFORE WRITING. With a terminal it asks [y/N]; without one it writes only when told
// to with --yes, which is how an agent in a jail runs it on the project's files (§5). A plan
// that does not prove equal is shown, difference by difference, and nothing is written.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const configMigrateUsage = `Usage: yolo config migrate [--dry-run] [--yes] [--only] [FILE...]

Convert the old-format config files (config.jsonc, yolo-jail.jsonc, yolo-jail.local.jsonc and
every file they include) into their TOML twins, once. On the host it converts the user config
and this workspace's files; in a jail, only this workspace's files.

With FILE, it converts only each named old-format file, such as one you pass with
--user-layer, and the files it includes: <name>.toml beside it, proved over your user config
as a launch layers it. The user config and this workspace's files are not touched. In a jail,
a FILE must be inside this workspace. Each include entry in a converted file names the
converted include. With --only, each FILE is converted alone: the old files it includes stay
as they are, and the converted file keeps including them (a TOML file may include an old one).

It proves the converted files say exactly what the old ones say (the effective config before
and after, compared as canonical JSON), and that the next launch accepts them (its own config
checks, run over the converted files staged off to the side). Otherwise it changes nothing and
prints why. Only then does it move the converted files into place, by renames alone: each
original is kept, byte for byte, as <name>.pre-migrate, and the records only yolo reads
(approval records, .yolo/config-boot.json) are respelled so the next launch asks nothing new.
If it is killed part way, the launch says so, and running it again finishes the moves.

Without FILE, only the files the config reaches are converted: a --user-layer file, or any
file no include names, is left as it is.

Convert only once every machine that shares these files runs a yolo that reads TOML: an older
yolo finds no config.jsonc and starts with no packs.

Flags:
  --dry-run   Show what it would write, and write nothing.
  --yes       Write without asking (for use with no terminal, such as an agent in a jail).
  --only      With FILE: convert each FILE alone, not the files it includes.
  --help, -h  Show this help.`

// configMigrateInteractive is whether the migration can ask its question: a person at stdin
// who sees the prompt on stderr.
var configMigrateInteractive = func() bool { return isTTY(os.Stdin) && isTTY(os.Stderr) }

func configMigrateMain(args []string, out, errw io.Writer) int {
	return configMigrate(args, os.Stdin, configMigrateInteractive(), out, errw)
}

// configMigrateRequest is what a migration started here covers: this workspace's files, and on
// the host the user config's too.
func configMigrateRequest(workspace string) configmigrate.Request {
	req := configmigrate.Request{Workspace: workspace}
	if !config.InJail() {
		req.UserConfigDir = filepath.Dir(paths.UserConfigPath())
	}
	return req
}

func configMigrate(args []string, in io.Reader, interactive bool, out, errw io.Writer) int {
	dryRun, yes, only := false, false, false
	var files []string
	endOfFlags := false
	for _, a := range args {
		switch {
		case endOfFlags || !strings.HasPrefix(a, "-"):
			files = append(files, a)
		case a == "--":
			endOfFlags = true
		case a == "--dry-run":
			dryRun = true
		case a == "--yes":
			yes = true
		case a == "--only":
			only = true
		case a == "-h" || a == "--help":
			writeCLIUsage(out, colorForWriter(out), configMigrateUsage)
			return 0
		default:
			fmt.Fprintf(errw, "yolo config migrate: unexpected argument %q; it takes --dry-run, --yes, --only and file paths "+
				"(`yolo config migrate --help`)\n", a)
			return 2
		}
	}
	if len(files) == 1 && files[0] == "help" {
		writeCLIUsage(out, colorForWriter(out), configMigrateUsage)
		return 0
	}
	workspace := configEditWorkspace()
	req := configMigrateRequest(workspace)
	if only && len(files) == 0 {
		fmt.Fprintln(errw, "yolo config migrate: --only converts a named FILE alone, and no file was named; "+
			"run `yolo config migrate --only FILE`, or `yolo config migrate` with no flag to convert every file the config reaches")
		return 2
	}
	req.Only = only
	if len(files) > 0 {
		layers, rc := configMigrateLayerPaths(files, workspace, errw)
		if rc != 0 {
			return rc
		}
		if rc := finishInterruptedMigration(req, workspace, dryRun, out, errw); rc != 0 {
			return rc
		}
		return configMigrateLayers(req, workspace, layers, dryRun, yes, in, interactive, out, errw)
	}
	if req.UserConfigDir == "" {
		fmt.Fprintln(errw, "In a jail only this workspace's config files migrate; the user config is the host's. "+
			"Run `yolo config migrate` on the host to convert it.")
	}
	if rc := finishInterruptedMigration(req, workspace, dryRun, out, errw); rc != 0 {
		return rc
	}
	graph, err := configmigrate.Discover(req)
	if err != nil {
		fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
		return 1
	}
	plan, err := configmigrate.Plan(graph)
	if plan != nil {
		fmt.Fprint(out, plan.Transcript())
	}
	if err != nil {
		var unequal *configmigrate.InequalityError
		if !errors.As(err, &unequal) || plan == nil {
			fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
		}
		return 1
	}
	if len(plan.Targets) == 0 {
		return 0
	}
	if dryRun {
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	if !yes {
		if !interactive {
			fmt.Fprintln(errw, "yolo config migrate: there is no terminal to ask on, so nothing was written. "+
				"Read the plan above, then run `yolo config migrate --yes` to write it.")
			return 1
		}
		question := fmt.Sprintf("Write %d %s, keeping each original as <name>.pre-migrate? [y/N] ",
			len(plan.Targets), plural(len(plan.Targets), "file", "files"))
		if !tty.Confirm(errw, in, question, false) {
			fmt.Fprintln(errw, "Nothing was written. `yolo config migrate` asks again whenever you are ready.")
			return 1
		}
	}
	result, err := configmigrate.Apply(plan, configmigrate.ApplyOptions{})
	if err != nil {
		fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
		return 1
	}
	// Every change is named here, so the closing line can say nothing else changed (RC-D34).
	fmt.Fprintf(out, "Converted: %s.\n", strings.Join(result.Written, ", "))
	printLinks(out, workspace, result.Links)
	if len(result.Backups) > 0 {
		fmt.Fprintf(out, "Kept the %s as %s; delete %s once the new files work for you.\n",
			plural(len(result.Backups), "original", "originals"), strings.Join(shownPaths(workspace, result.Backups), ", "),
			plural(len(result.Backups), "it", "them"))
		if keptUserBackup(result.Backups) {
			fmt.Fprintln(out, "While the user config's .pre-migrate copy is kept, the launch, `yolo check` and "+
				"`yolo host apply` report any pack whose skill names, files landing or /ctx directory a "+
				"default namespace changes (docs/reference/pack-system.md#pack-name-migration); once it is deleted, they no longer do.")
		}
	}
	if s := configmigrate.Respelled(result.Records); s != "" {
		fmt.Fprintf(out, "Records only yolo reads, respelled in place: %s.\n", s)
	}
	printPackRecords(out, result.PackRecords)
	for _, r := range result.Records {
		if r.State == configmigrate.RecordLeft {
			fmt.Fprintf(out, "The %s %s is left: %s.\n", r.Kind, r.Path, r.Why)
		}
	}
	for _, ig := range result.Ignores {
		fmt.Fprintln(out, ig.Report(workspace))
	}
	fmt.Fprintln(out, "Nothing else changed. Next: `yolo check`.")
	return 0
}

// finishInterruptedMigration finishes a migration a kill or a failed move stopped part way, first,
// forward, never back (config-migration.md, the commit steps), or else removes what a run killed
// before its journal left staged. It returns non-zero when the verb must stop there.
func finishInterruptedMigration(req configmigrate.Request, workspace string, dryRun bool, out, errw io.Writer) int {
	if journal := configmigrate.Interrupted(req); journal != "" {
		if dryRun {
			fmt.Fprintf(errw, "yolo config migrate: an earlier migration stopped part way (%s); run `yolo config migrate` without --dry-run to finish it. Nothing was changed.\n", journal)
			return 1
		}
		res, err := configmigrate.Resume(journal)
		if err != nil {
			fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
			return 1
		}
		fmt.Fprintf(out, "Finished the migration that stopped part way. Converted: %s; originals kept as %s.\n",
			strings.Join(shownPaths(workspace, res.Written), ", "), strings.Join(shownPaths(workspace, res.Backups), ", "))
		printLinks(out, workspace, res.Links)
		printPackRecords(out, res.PackRecords)
	} else if !dryRun {
		// A converted file staged by a run killed before its journal is a hidden copy of private
		// bytes nothing will move into place: removed here, never left for a later migrate.
		for _, p := range configmigrate.RemoveStaleStaged(req) {
			fmt.Fprintf(out, "Removed %s, left staged by a migration stopped before it began.\n", shownPaths(workspace, []string{p})[0])
		}
	}
	return 0
}

// layerArg is one FILE named to `yolo config migrate`: as given, and absolute.
type layerArg struct{ given, abs string }

// configMigrateLayerPaths makes each named FILE absolute. In a jail a FILE outside the workspace is
// refused, naming the host command: the jail converts only this workspace's files.
func configMigrateLayerPaths(files []string, workspace string, errw io.Writer) ([]layerArg, int) {
	out := make([]layerArg, 0, len(files))
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			fmt.Fprintf(errw, "yolo config migrate: %s: %v\n", f, err)
			return nil, 1
		}
		if config.InJail() {
			rel, err := filepath.Rel(workspace, abs)
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				fmt.Fprintf(errw, "yolo config migrate: %s is outside this workspace, and in a jail only this workspace's "+
					"files migrate; nothing was changed. Run `yolo config migrate %s` on the host to convert it.\n", f, abs)
				return nil, 1
			}
		}
		out = append(out, layerArg{given: f, abs: abs})
	}
	return out, 0
}

// configMigrateLayers is `yolo config migrate FILE...` (RC-D46, amended 2026-11-01): each named
// file, a --user-layer file, is planned on its own over the user config it layers on, all are
// shown, and only once every one proves equal does it ask, then convert each in turn.
func configMigrateLayers(base configmigrate.Request, workspace string, layers []layerArg, dryRun, yes bool, in io.Reader, interactive bool, out, errw io.Writer) int {
	plans := make([]*configmigrate.MigrationPlan, 0, len(layers))
	targets := 0
	for _, layer := range layers {
		req := base
		req.Layer = layer.abs
		if !dryRun {
			for _, p := range configmigrate.RemoveStaleStaged(req) {
				fmt.Fprintf(out, "Removed %s, left staged by a migration stopped before it began.\n", shownPaths(workspace, []string{p})[0])
			}
		}
		graph, err := configmigrate.Discover(req)
		if err != nil {
			fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
			return 1
		}
		plan, err := configmigrate.Plan(graph)
		if plan != nil {
			fmt.Fprint(out, plan.Transcript())
		}
		if err != nil {
			var unequal *configmigrate.InequalityError
			if !errors.As(err, &unequal) || plan == nil {
				fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
			}
			if len(layers) > 1 {
				fmt.Fprintln(errw, "Nothing was written for any of the files named.")
			}
			return 1
		}
		plans = append(plans, plan)
		targets += len(plan.Targets)
	}
	if dryRun {
		fmt.Fprintln(out, "Dry run; nothing was written.")
		return 0
	}
	if !yes {
		if !interactive {
			fmt.Fprintln(errw, "yolo config migrate: there is no terminal to ask on, so nothing was written. "+
				"Read the plan above, then run the same command with --yes to write it.")
			return 1
		}
		question := fmt.Sprintf("Write %d %s, keeping each original as <name>.pre-migrate? [y/N] ",
			targets, plural(targets, "file", "files"))
		if !tty.Confirm(errw, in, question, false) {
			fmt.Fprintln(errw, "Nothing was written. Run the same command again whenever you are ready.")
			return 1
		}
	}
	var next []string
	for i, plan := range plans {
		result, err := configmigrate.Apply(plan, configmigrate.ApplyOptions{})
		if err != nil {
			fmt.Fprintf(errw, "yolo config migrate: %v\n", err)
			if i > 0 {
				fmt.Fprintf(errw, "The files named before %s were converted; name the rest again once that is fixed.\n", layers[i].given)
			}
			return 1
		}
		fmt.Fprintf(out, "Converted: %s.\n", strings.Join(result.Written, ", "))
		printLinks(out, workspace, result.Links)
		if len(result.Backups) > 0 {
			fmt.Fprintf(out, "Kept the %s as %s; delete %s once the new file works for you.\n",
				plural(len(result.Backups), "original", "originals"), strings.Join(result.Backups, ", "),
				plural(len(result.Backups), "it", "them"))
		}
		printPackRecords(out, result.PackRecords)
		given := layers[i].given
		twin := strings.TrimSuffix(given, filepath.Ext(given)) + ".toml"
		next = append(next, fmt.Sprintf("`--user-layer %s` in place of %s, which a launch no longer finds", twin, given))
	}
	fmt.Fprintln(out, "Your user config and this workspace's files were not touched, and nothing else changed.")
	fmt.Fprintf(out, "Next: launch with %s.\n", strings.Join(next, "; and "))
	return 0
}

// keptUserBackup reports whether one of backups is the user config's .pre-migrate copy, the file
// whose presence packload.ConvertedConfigIn reads as "this config was converted".
func keptUserBackup(backups []string) bool {
	userDir := filepath.Dir(paths.UserConfigPath())
	for _, b := range backups {
		if filepath.Dir(b) == userDir && slices.Contains(packload.ConvertedConfigBackups, filepath.Base(b)) {
			return true
		}
	}
	return false
}

// printPackRecords names each record of a renamed pack that moved to the pack's new name.
// printLinks says, for each symlink a migration moved aside, that its target was not changed and
// that a dotfiles manager needs the new file copied in by hand (RC-D41).
func printLinks(out io.Writer, workspace string, links []configmigrate.LinkedFile) {
	for _, l := range links {
		shown := shownPaths(workspace, []string{l.Link, l.Backup, l.Target, l.New})
		fmt.Fprintln(out, configmigrate.LinkReport(configmigrate.LinkedFile{Link: shown[0], Backup: shown[1], Target: shown[2], New: shown[3]}))
	}
}

func printPackRecords(out io.Writer, moved []string) {
	if len(moved) == 0 {
		return
	}
	fmt.Fprintln(out, "Moved to the renamed packs' new names:")
	for _, m := range moved {
		fmt.Fprintf(out, "  %s\n", m)
	}
}

// shownPaths names each path relative to the workspace when it lies inside it, else in full.
func shownPaths(workspace string, list []string) []string {
	out := make([]string, len(list))
	for i, p := range list {
		out[i] = p
		if rel, err := filepath.Rel(workspace, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			out[i] = rel
		}
	}
	return out
}

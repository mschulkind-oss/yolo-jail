package cli

// configmigrate.go is `yolo config migrate`: the one-time conversion of the old JSONC config
// files into the TOML files that replace them (docs/design/config-migration.md §1 and §4). The
// work is internal/configmigrate's: it reads the whole old graph, proves the converted files say
// what the old ones say, and writes nothing otherwise. This verb picks what the migration covers,
// shows the library's transcript, asks, and applies.
//
// WHAT IT COVERS (§4 item 6). On the host: the user config with everything it includes and its
// config.d fragments, plus this workspace's project and local files. In a jail: only this
// workspace's files, since the user config there is the host's, inherited for the launch.
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
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const configMigrateUsage = `Usage: yolo config migrate [--dry-run] [--yes]

Convert the old-format config files (config.jsonc, yolo-jail.jsonc, yolo-jail.local.jsonc and
every file they include) into their TOML twins, once. On the host it converts the user config
and this workspace's files; in a jail, only this workspace's files.

It proves the converted files say exactly what the old ones say (the effective config before
and after, compared as canonical JSON), and that the next launch accepts them (its own config
checks, run over the converted files staged off to the side). Otherwise it changes nothing and
prints why. Only then does it move the converted files into place, by renames alone: each
original is kept, byte for byte, as <name>.pre-migrate, and the records only yolo reads
(approval records, .yolo/config-boot.json) are respelled so the next launch asks nothing new.
If it is killed part way, the launch says so, and running it again finishes the moves.

Only the files the config reaches are converted: a --user-layer file, or any file no include
names, is left as it is.

Convert only once every machine that shares these files runs a yolo that reads TOML: an older
yolo finds no config.jsonc and starts with no packs.

Flags:
  --dry-run   Show what it would write, and write nothing.
  --yes       Write without asking (for use with no terminal, such as an agent in a jail).
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
	dryRun, yes := false, false
	for _, a := range args {
		switch a {
		case "--dry-run":
			dryRun = true
		case "--yes":
			yes = true
		case "-h", "--help", "help":
			writeCLIUsage(out, colorForWriter(out), configMigrateUsage)
			return 0
		default:
			fmt.Fprintf(errw, "yolo config migrate: unexpected argument %q; it takes --dry-run and --yes "+
				"(`yolo config migrate --help`)\n", a)
			return 2
		}
	}
	workspace := configEditWorkspace()
	req := configMigrateRequest(workspace)
	if req.UserConfigDir == "" {
		fmt.Fprintln(errw, "In a jail only this workspace's config files migrate; the user config is the host's. "+
			"Run `yolo config migrate` on the host to convert it.")
	}
	// A migration a kill or a failed move stopped part way is finished first, forward, never back
	// (config-migration.md, the commit steps).
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
		printPackRecords(out, res.PackRecords)
	} else if !dryRun {
		// A converted file staged by a run killed before its journal is a hidden copy of private
		// bytes nothing will move into place: removed here, never left for a later migrate.
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
	for _, l := range result.Links {
		shown := shownPaths(workspace, []string{l.From, l.To})
		fmt.Fprintf(out, "Renamed the symlink %s to %s; if a dotfiles manager made it, you may need to update that too.\n", shown[0], shown[1])
	}
	if len(result.Backups) > 0 {
		fmt.Fprintf(out, "Kept the %s as %s; delete %s once the new files work for you.\n",
			plural(len(result.Backups), "original", "originals"), strings.Join(shownPaths(workspace, result.Backups), ", "),
			plural(len(result.Backups), "it", "them"))
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

// printPackRecords names each record of a renamed pack that moved to the pack's new name.
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

package run

// migrateoffer.go is the launch's offer to convert old-format config files
// (docs/design/config-migration.md §1 and §3 step 1; RC-D19, RC-D21).
//
// This release reads both formats and keeps the old reader for a few releases, so an old file is
// never a refusal: the launch ASKS, once per launch, before anything reads the config.
//
//   - With a terminal: "[y/N]". `y` runs the same migration `yolo config migrate` runs (proven
//     equal or nothing written), then the launch continues on the new files. Anything else
//     launches on the old files and says how to convert later or stop the question.
//   - Without one (or for --dry-run, which writes nothing): one line, and the launch continues.
//   - `"convert_later": true` in the old user file, written by hand, silences the offer for every
//     old file this machine reads. No command writes it (config.ConvertLaterKey).
//
// WHERE IT SITS: before the pack refresh and the config load, so a converted config is the one
// the launch reads; and before the config-change prompt, since the migration respells the
// approval records and the boot baseline, so a converted workspace is not then asked about a
// change it did not make. Nothing here refuses a launch: a migration that cannot prove equality,
// or fails to write, leaves every old file as it was and the launch goes on with them.
//
// ONLY A LAUNCH THAT WILL START A JAIL IS ASKED, never an attach to a running one: that jail's config
// was frozen at its boot (an attach never re-stages), so a conversion under it is a change it does
// not see, and every extra terminal would be asked again. The attach decision itself is made far
// below, after the config load, under the launch lock and against the runtime the config names; so
// this asks the one thing knowable before the config is read: whether this workspace's jail has a
// keeper (probeKeeper, a local lock, no runtime call). A keeper is alive for as long as its jail
// runs, so a held lock is an attach, or a jail being ended; either way this launch says nothing
// and the next fresh launch asks. An attach that RESTARTS an older jail (the contract gate) is not
// asked either: it decides that only after the config is read. "Could not ask" is not an attach:
// the offer is made as before, and nothing here refuses a launch. --never-attach is always asked.
//
// What it covers is what `yolo config migrate` covers from here: on the host the user config and
// this workspace's files; in a jail only the workspace's, since the user config is the host's.

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// offerConfigMigration offers to convert this launch's old-format config files, and converts
// them when the person says yes. It returns false only when the person asked for the conversion
// and it did not happen: the launch stops there, on unchanged files (RC-D35).
func (o *Options) offerConfigMigration() bool {
	if o.launchWillAttach() {
		return true
	}
	req := configmigrate.Request{Workspace: o.Workspace}
	if !config.InJail() {
		req.UserConfigDir = filepath.Dir(paths.UserConfigPath())
	}
	graph, err := configmigrate.Discover(req)
	if err != nil {
		// The launch's own read below reports what is wrong with these files.
		return true
	}
	old := graph.Old()
	if len(old) == 0 {
		return true
	}
	userFile, hasLegacyUser, silenced := convertLaterState()
	if silenced {
		return true
	}
	names := make([]string, len(old))
	for i, f := range old {
		names[i] = f.Logical
	}
	stop := ""
	if hasLegacyUser {
		stop = fmt.Sprintf("add [green]\"%s\": true[/green] to [cyan]%s[/cyan]", config.ConvertLaterKey, richtext.Escape(userFile))
	}
	pr := o.pr(o.Stderr)
	header := fmt.Sprintf("[bold yellow]Deprecated config format:[/bold yellow] yolo now uses TOML, and %s still JSONC:",
		plural(len(old), "this file is", "these files are"))
	if o.DryRun || !o.IsTTYStdin() || !o.IsTTYStderr() {
		pr.printf("%s %s", header, richtext.Escape(strings.Join(names, ", ")))
		pr.print("  Preview:      [green]yolo config migrate --dry-run[/green]")
		if stop != "" {
			pr.print("  Stop asking:  " + stop)
		}
		return true
	}
	// The mental model, then the two ways out, then the question (RC-D35): the old format still
	// works for now, converting keeps each original beside it, and nothing is explained beyond that.
	pr.print(header)
	for _, n := range names {
		pr.printf("  [cyan]%s[/cyan]", richtext.Escape(n))
	}
	pr.print("The deprecated format will keep working for a few more releases.")
	pr.printf("Converting keeps %s next to it, %s [cyan]%s.pre-migrate[/cyan].",
		plural(len(old), "the original", "each original"), plural(len(old), "as", "e.g."),
		richtext.Escape(filepath.Base(names[0])))
	pr.print("")
	pr.print("  Preview:      [green]yolo config migrate --dry-run[/green]")
	if stop != "" {
		pr.print("  Stop asking:  " + stop)
	}
	pr.print("")
	if _, err := fmt.Fprint(o.Stderr, richtext.Render("[bold]Convert to TOML now? [y/N][/bold] ", o.streamColor())); err != nil {
		return true
	}
	reply := ""
	if scanner := bufio.NewScanner(o.Stdin); scanner.Scan() {
		reply = strings.ToLower(strings.TrimSpace(scanner.Text()))
	}
	if reply != "y" && reply != "yes" {
		pr.printf("Keeping the JSONC %s for now.", plural(len(old), "file", "files"))
		return true
	}
	plan, err := configmigrate.Plan(graph)
	if err != nil {
		o.reportMigrationRefusal(plan, err, names)
		return false
	}
	res, err := configmigrate.Apply(plan, configmigrate.ApplyOptions{})
	if err != nil {
		fmt.Fprintf(o.Stderr, "%v\n", err)
		pr.print("[red]Couldn't write the converted files.[/red] Nothing was changed. Run the same command again and answer [bold]n[/bold] to launch with your current files.")
		return false
	}
	backups := make([]string, len(res.Backups))
	for i, b := range res.Backups {
		backups[i] = filepath.Base(b)
	}
	pr.printf("Converted. The %s next to the new %s, as %s.", plural(len(backups), "original is", "originals are"),
		plural(len(backups), "file", "files"), richtext.Escape(joinAnd(backups)))
	for _, ig := range res.Ignores {
		pr.print(richtext.Escape(ig.Report(o.Workspace)))
	}
	return true
}

// reportMigrationRefusal says why the offer converted nothing; the launch then stops. A setting
// with no TOML spelling yet names its own next step and is not a bug. A converted config that
// would mean something else, with every setting converted, is a yolo bug, and says so.
func (o *Options) reportMigrationRefusal(plan *configmigrate.MigrationPlan, err error, names []string) {
	pr := o.pr(o.Stderr)
	var ineq *configmigrate.InequalityError
	if !errors.As(err, &ineq) || plan == nil {
		fmt.Fprintf(o.Stderr, "%v\n", err)
		pr.printf("[red]Couldn't convert your config.[/red] Nothing was changed. %s", keepGoing)
		return
	}
	// A setting with no TOML spelling yet is left out of the converted file, so the proof also
	// shows what leaving it out changes; those settings, with their reasons, are the answer then.
	if len(plan.NotConverted) > 0 {
		pr.print("[yellow]Some settings can't be converted to TOML yet:[/yellow]")
		for _, nc := range plan.NotConverted {
			where := nc.Path
			if nc.File != "" {
				where = filepath.Base(nc.File) + ": " + nc.Path
			}
			pr.printf("  %s — %s", richtext.Escape(where), richtext.Escape(nc.Reason))
		}
		pr.printf("Nothing was changed. Change those settings and convert again, or %s", strings.ToLower(keepGoing[:1])+keepGoing[1:])
		return
	}
	pr.print("The converted config would not mean the same as yours:")
	for _, d := range plan.Differences {
		pr.printf("  %s: %s", richtext.Escape(d.Layer), richtext.Escape(d.Path))
		pr.printf("    [red]- %s[/red]", richtext.Escape(d.Before))
		pr.printf("    [green]+ %s[/green]", richtext.Escape(d.After))
	}
	what := "your config"
	if len(names) == 1 {
		what = filepath.Base(names[0])
	}
	pr.printf("[red]Couldn't convert %s[/red] — this is a yolo bug. Nothing was changed. "+
		"Please file it at %s with the output above. %s", richtext.Escape(what), migrationBugURL, keepGoing)
}

// keepGoing is how a person whose requested conversion failed launches anyway.
const keepGoing = "Run the same command again and answer [bold]n[/bold] to launch with your current files."

// migrationBugURL is where a conversion that would change a config's meaning is reported.
const migrationBugURL = "https://github.com/mschulkind-oss/yolo-jail/issues"

// joinAnd words a list as "a", "a and b" or "a, b and c".
func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

// launchWillAttach is whether this launch will most likely attach to this workspace's running jail,
// read before the config is: a keeper holds the jail's liveness lock. keeperUnknown is not an
// attach, so a probe that could not answer leaves the offer as it was.
func (o *Options) launchWillAttach() bool {
	if o.NeverAttach {
		return false
	}
	return probeKeeper(yoloruntime.FromWorkspace(o.Workspace)) == keeperAlive
}

// convertLaterState reads the user's old-format config file: where a person would write
// convert_later (userFile), whether that is a place it can go (hasLegacyUser: an old-format
// user file, or no user file at all, since a new config.jsonc is still an old-format user
// file), and whether it is already set to true there. A TOML user config has no place for it.
func convertLaterState() (userFile string, hasLegacyUser, silenced bool) {
	userFile = paths.UserConfigPath()
	dir := filepath.Dir(userFile)
	if _, err := os.Lstat(filepath.Join(dir, paths.UserConfigTOMLName)); err == nil {
		return userFile, false, false
	}
	for _, name := range []string{paths.UserConfigLegacyJSONCName, paths.UserConfigLegacyJSONName} {
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		doc, err := configdoc.Parse(data, configdoc.JSONC)
		if err != nil {
			return path, true, false
		}
		value, _ := doc.Values.Get(config.ConvertLaterKey)
		on, _ := value.(bool)
		return path, true, on
	}
	return userFile, true, false
}

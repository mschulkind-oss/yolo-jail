package cli

// packmigrate.go is `yolo pack migrate <path>`: rewrite a local pack's legacy pack.json as the
// grouped pack.toml and move its plain-copy `files` content into home/
// (docs/design/pack-manifests.md §5). The work is internal/packmigrate's, which proves the
// migrated pack delivers the same files, modes and posture or changes nothing; this verb shows
// its plan, asks, and applies.
//
// This release reads both manifest formats, so a pack.json still loads: the migration is the
// author's act, taken when they choose, and a pack it cannot yet say in TOML (a JSON null option
// value, for one) stays on its legacy manifest with the reason printed.

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/tty"
)

const packMigrateUsage = `Usage: yolo pack migrate <path> [--dry-run] [--yes] [--keep-install-hints]

Rewrite the pack at <path> from its legacy pack.json (or pack.jsonc) to a grouped pack.toml,
keeping every comment in order, and move its plain-copy "files" content into home/. Each loophole
module the pack ships on a legacy manifest.jsonc becomes a loophole.toml in the same directory,
comments kept in order (a pack already on pack.toml still gets its loophole modules converted).
In a git checkout each file moves with git mv, so history follows.

It proves the migrated pack says and delivers the same thing (manifest meaning, every delivered
file's content and mode, the posture rows) before it writes, checks again after, and undoes the
migration on any difference.

Flags:
  --dry-run             Show the plan, and change nothing.
  --yes                 Apply without asking (for use with no terminal).
  --keep-install-hints  Keep every install hint on its own manager line, as the shipped packs
                        do so each hint keeps its source citation.
  --help, -h            Show this help.`

// packMigrateInteractive is whether the migration can ask its question.
var packMigrateInteractive = func() bool { return isTTY(os.Stdin) && isTTY(os.Stderr) }

func packMigrateMain(args []string, out, errw io.Writer) int {
	return packMigrate(args, os.Stdin, packMigrateInteractive(), out, errw)
}

func packMigrate(args []string, in io.Reader, interactive bool, out, errw io.Writer) int {
	dryRun, yes := false, false
	var opts packmigrate.Options
	dir := ""
	for _, a := range args {
		switch {
		case a == "--dry-run":
			dryRun = true
		case a == "--yes":
			yes = true
		case a == "--keep-install-hints":
			opts.KeepInstallHints = true
		case a == "-h" || a == "--help":
			writeCLIUsage(out, colorForWriter(out), packMigrateUsage)
			return 0
		case strings.HasPrefix(a, "-"):
			fmt.Fprintf(errw, "yolo pack migrate: unknown flag %s (`yolo pack migrate --help`)\n", a)
			return 2
		case dir != "":
			fmt.Fprintf(errw, "yolo pack migrate: one pack directory at a time, got a second: %q\n", a)
			return 2
		default:
			dir = a
		}
	}
	if dir == "" {
		fmt.Fprintln(errw, "yolo pack migrate: name the pack directory, for example `yolo pack migrate ./my-pack` "+
			"(`yolo pack migrate --help`)")
		return 2
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack migrate: %v\n", err)
		return 1
	}
	m, err := packmigrate.Plan(abs, opts)
	if err != nil {
		fmt.Fprintf(errw, "yolo pack migrate: %v\n", err)
		return 1
	}
	writePackMigrationPlan(out, m)
	if dryRun {
		fmt.Fprintln(out, "Dry run; nothing was changed.")
		return 0
	}
	if !yes {
		if !interactive {
			fmt.Fprintf(errw, "yolo pack migrate: there is no terminal to ask on, so nothing was changed. "+
				"Read the plan above, then run `yolo pack migrate %s --yes` to apply it.\n", dir)
			return 1
		}
		question := fmt.Sprintf("Write %s and move %d %s? [y/N] ", packdecl.ManifestTOMLName,
			len(m.Moves), plural(len(m.Moves), "file", "files"))
		if m.Manifest == nil {
			question = fmt.Sprintf("Write %d %s? [y/N] ", len(m.Loopholes), loopholeTOMLName(len(m.Loopholes)))
		} else if m.Legacy == packdecl.ManifestTOMLName {
			question = fmt.Sprintf("Rewrite %s? [y/N] ", packdecl.ManifestTOMLName)
		}
		if !tty.Confirm(errw, in, question, false) {
			fmt.Fprintln(errw, "Nothing was changed.")
			return 1
		}
	}
	if err := packmigrate.Apply(m); err != nil {
		fmt.Fprintf(errw, "yolo pack migrate: %v\n", err)
		return 1
	}
	fmt.Fprintf(out, "Migrated. Next: `yolo pack lint %s`.\n", dir)
	return 0
}

// writePackMigrationPlan is what a pack migration shows before it asks: the manifest it
// replaces, the moves, what stays declared and why, and the equality verdict.
func writePackMigrationPlan(out io.Writer, m *packmigrate.Migration) {
	if m.Manifest != nil {
		legacy := filepath.Join(m.Dir, m.Legacy)
		oldLines := 0
		if data, err := os.ReadFile(legacy); err == nil {
			oldLines = lineCount(data)
		}
		fmt.Fprintf(out, "--- %s\n+++ %s\n", legacy, filepath.Join(m.Dir, packdecl.ManifestTOMLName))
		if m.Legacy == packdecl.ManifestTOMLName {
			fmt.Fprintf(out, "  (%d lines → %d, rewritten in place; every comment and every line not named below kept)\n",
				oldLines, lineCount(m.Manifest))
		} else {
			fmt.Fprintf(out, "  (%d lines → %d; %d %s kept, in order)\n", oldLines, lineCount(m.Manifest),
				len(m.Comments), plural(len(m.Comments), "comment", "comments"))
		}
	}
	if len(m.Moves) > 0 {
		fmt.Fprintf(out, "Moved into home/ (%d %s):\n", len(m.Moves), plural(len(m.Moves), "file", "files"))
		for _, mv := range m.Moves {
			fmt.Fprintf(out, "  %s → %s\n", mv.From, mv.To)
		}
	}
	for _, k := range m.Kept {
		fmt.Fprintf(out, "Left declared: files entry %d (from %q): %s.\n", k.Index, k.From, k.Reason)
	}
	for _, f := range m.ProfileFolds {
		if f.Variant == "" {
			fmt.Fprintf(out, "Folded: [[profile]] %q is now an alias of [providers.%s].\n", f.Profile, f.Provider)
		} else {
			fmt.Fprintf(out, "Folded: [[profile]] %q is now [providers.%s.variants.%s], named %q.\n", f.Profile, f.Provider, f.Variant, f.Profile)
		}
	}
	for _, k := range m.KeptProfiles {
		fmt.Fprintf(out, "Left legacy: [[profile]] %q: %s.\n", k.Profile, k.Reason)
	}
	for _, l := range m.Loopholes {
		fmt.Fprintf(out, "--- %s\n+++ %s\n", filepath.Join(m.Dir, l.Dir, l.Legacy), filepath.Join(m.Dir, l.Dir, loopholedecl.ManifestTOMLName))
		fmt.Fprintf(out, "  (%d %s kept, in order; the loophole decodes to the identical manifest, checked before writing)\n",
			len(l.Comments), plural(len(l.Comments), "comment", "comments"))
	}
	if m.Manifest != nil {
		fmt.Fprintln(out, "Manifest meaning, delivered files and modes, and posture: identical (checked before writing).")
	}
}

func loopholeTOMLName(n int) string { return plural(n, "loophole.toml", "loophole.toml files") }

func lineCount(data []byte) int {
	if len(data) == 0 {
		return 0
	}
	n := bytes.Count(data, []byte("\n"))
	if data[len(data)-1] != '\n' {
		n++
	}
	return n
}

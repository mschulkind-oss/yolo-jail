package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// MigrationJournalName is the file `yolo config migrate` writes before it moves the first
// converted file into place and removes after the last (docs/design/config-migration.md, the
// commit steps). While it exists the config files are part way from the old layout to the new
// one, and the next `yolo config migrate` finishes the moves it lists: forward, never back.
const MigrationJournalName = ".config-migrate-journal.json"

// MigrationJournalPaths are the places a journal can be for a launch in workspace: the user
// config directory, where a migration that converts the user config writes it, and the
// workspace's .yolo, where one converting only the workspace's files does.
func MigrationJournalPaths(workspace string) []string {
	out := []string{filepath.Join(filepath.Dir(paths.UserConfigPath()), MigrationJournalName)}
	if workspace != "" {
		out = append(out, filepath.Join(workspace, ".yolo", MigrationJournalName))
	}
	return out
}

// InterruptedMigration is the journal of a `yolo config migrate` that stopped part way, for a
// launch in workspace, or "" when there is none.
func InterruptedMigration(workspace string) string {
	for _, p := range MigrationJournalPaths(workspace) {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

// interruptedMigrationBeside is the journal of a migration stopped part way that covers the
// top-level config file file: one in its directory (the user config directory) or in that
// directory's .yolo (a workspace). "" when there is none.
func interruptedMigrationBeside(file string) string {
	dir := filepath.Dir(file)
	for _, p := range []string{filepath.Join(dir, MigrationJournalName), filepath.Join(dir, ".yolo", MigrationJournalName)} {
		if _, err := os.Lstat(p); err == nil {
			return p
		}
	}
	return ""
}

// InterruptedMigrationRefusal is what a launch, `yolo check` and the reader say while a
// migration is part way: the files are neither the old layout nor the new one, and the one
// step is to finish it.
func InterruptedMigrationRefusal(journal string) string {
	return fmt.Sprintf("config: `yolo config migrate` stopped part way while it moved your converted "+
		"config files into place (its list of moves is %s), so your config files are half old and half "+
		"new. Run `yolo config migrate` to finish the moves it recorded; it only moves files forward "+
		"and keeps every original as <name>.pre-migrate", journal)
}

// validateNoInterruptedMigration refuses a launch's config while a migration is part way.
func validateNoInterruptedMigration(workspace string, errs *[]string) {
	if j := InterruptedMigration(workspace); j != "" {
		add(errs, InterruptedMigrationRefusal(j))
	}
}

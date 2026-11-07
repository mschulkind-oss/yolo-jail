package check

// packmigrateadvice.go is the Packs section's one advice line for a local pack still on its legacy
// manifest (docs/design/pack-manifests.md §5): this release reads pack.json and pack.jsonc, so the
// pack passes, and the migration is the author's act, taken when they choose. The line names the
// verb that takes it. A pack.toml still writing a provider in a legacy spelling (`[[provider]]
// name =`, or a `[[profile]]` that folds into its provider; docs/design/readable-config.md RC-D69)
// gets the same kind of line.

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/packsrc"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// packMigrateAdvice is the advice for entry e, or "" when there is none: e is a local pack (not
// shipped, not fetched) whose directory holds a legacy pack.json or pack.jsonc and no pack.toml,
// or a pack.toml still writing a legacy provider spelling. A source not visible from here (a jail
// reading a host path) gives none: there is nothing here to migrate.
func packMigrateAdvice(e config.PackEntry) string {
	if e.Embedded() || !e.IsLocal() {
		return ""
	}
	addr, err := packsrc.Parse(e.Source)
	if err != nil || addr.Path == "" {
		return ""
	}
	if manifest := filepath.Join(addr.Path, packdecl.ManifestTOMLName); regularFile(manifest) {
		return legacySpellingAdvice(addr.Path, manifest)
	}
	for _, legacy := range []string{packdecl.ManifestLegacyJSONName, packdecl.ManifestLegacyJSONCName} {
		if regularFile(filepath.Join(addr.Path, legacy)) {
			return "its manifest is the old " + legacy + "; `yolo pack migrate " + shquote.Quote(addr.Path) +
				"` converts it to " + packdecl.ManifestTOMLName + " (it still loads as it is)"
		}
	}
	return ""
}

// legacySpellingAdvice is the note for a pack.toml that still writes a provider spelling
// `yolo pack migrate` rewrites, or "". A `[[profile]]` the migration cannot fold (its provider is
// another pack's) gets none: there is nothing for the verb to do.
func legacySpellingAdvice(dir, manifest string) string {
	data, err := os.ReadFile(manifest)
	if err != nil {
		return ""
	}
	legacy := packdecl.LegacyProviderSpellings(data)
	if len(legacy) == 0 {
		return ""
	}
	if rewrite, err := packmigrate.RewriteProviderSpellings(data); err == nil && rewrite == nil {
		return ""
	}
	return "its " + packdecl.ManifestTOMLName + " writes the legacy " + strings.Join(legacy, ", ") +
		"; `yolo pack migrate " + shquote.Quote(dir) + "` rewrites it as [providers.<id>] (it still loads as it is)"
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

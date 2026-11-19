package packload

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/mschulkind-oss/yolo-jail/internal/pluginpack"
)

// OrdinarySkillDirectories enumerates immediate directory children in source
// order, later sources winning a name. Markerless folders are ordinary content.
func OrdinarySkillDirectories(sources, skip []string) (map[string]string, error) {
	return ordinarySkillDirectories(sources, skip, os.ReadDir, os.Stat, filepath.Join,
		func(root, candidate string) bool { return pluginpack.Contains(root, candidate) })
}

func ordinarySkillDirectories(sources, skip []string, readDir func(string) ([]fs.DirEntry, error), stat func(string) (fs.FileInfo, error), join func(...string) string, contains func(string, string) bool) (map[string]string, error) {
	skipped := func(candidate string) bool {
		for _, root := range skip {
			if contains(root, candidate) {
				return true
			}
		}
		return false
	}
	out := map[string]string{}
	for _, source := range sources {
		if skipped(source) {
			continue
		}
		entries, err := readDir(source)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		for _, entry := range entries {
			full := join(source, entry.Name())
			if skipped(full) {
				continue
			}
			info, err := stat(full)
			if err == nil && info.IsDir() {
				out[entry.Name()] = full
			}
		}
	}
	return out, nil
}

func ordinarySkillDirectoriesFS(source fs.FS, roots []string) (map[string]string, error) {
	return ordinarySkillDirectories(roots, nil, func(p string) ([]fs.DirEntry, error) { return fs.ReadDir(source, p) }, func(p string) (fs.FileInfo, error) { return fs.Stat(source, p) }, path.Join, func(string, string) bool { return false })
}

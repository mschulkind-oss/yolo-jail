package packstage

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

// CheckEmbedded applies the same file filters to immutable shipped content;
// there is no destination or filesystem extraction capability.
func CheckEmbedded(source fs.FS, only, exclude []string) (*Result, error) {
	result := &Result{}
	w := walker{spec: Spec{Only: only, Exclude: exclude}, res: result}
	err := fs.WalkDir(source, ".", func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == "." {
			return nil
		}
		if e.IsDir() {
			if path == ".git" || strings.HasSuffix(path, "/.git") {
				return fs.SkipDir
			}
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("embedded pack %s is not a regular file", path)
		}
		return w.file(path, path, info)
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(result.Staged)
	sort.Strings(result.Excluded)
	return result, nil
}

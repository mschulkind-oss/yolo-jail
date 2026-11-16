package packload

import (
	"errors"
	"fmt"
	"io/fs"
	"path"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
)

// EmbeddedProjectSkillDirs reads only manifest metadata from the registered embedded filesystem.
// Unlike Embedded, it does not materialize or cache a pack tree.
func EmbeddedProjectSkillDirs() ([]string, error) {
	fsys := EmbeddedFS()
	if fsys == nil {
		return nil, nil
	}
	names, err := EmbeddedNamesFromFS(fsys)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var dirs []string
	for _, name := range names {
		manifestPath := path.Join(name, "pack.toml")
		data, err := fs.ReadFile(fsys, manifestPath)
		var manifest *packdecl.Manifest
		var problems []string
		switch {
		case err == nil:
			manifest, problems = packdecl.DecodeTOML(data)
		case errors.Is(err, fs.ErrNotExist):
			data, err = fs.ReadFile(fsys, path.Join(name, "pack.json"))
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err == nil {
				manifest, problems = packdecl.Decode(data)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("read embedded pack %s manifest: %w", name, err)
		}
		if len(problems) > 0 {
			return nil, fmt.Errorf("embedded pack %s manifest: %s", name, problems[0])
		}
		if manifest == nil {
			continue
		}
		for _, dir := range manifest.ProjectSkillDirs() {
			if !seen[dir] {
				seen[dir] = true
				dirs = append(dirs, dir)
			}
		}
	}
	return dirs, nil
}

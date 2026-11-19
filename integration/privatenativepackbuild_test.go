package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func packBuildNativeFixture(home, root string) *privateNativeFixture {
	const pack, bin = "packbuildfx", "packbuildfx-x"
	build := `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	manifest := `{"name":"` + pack + `","contributes":[` +
		`{"kind":"program","bin":"` + bin + `","via":"pack","from":"tools/x","build":"` + buildJSON + `","run":"x"}]}`
	cfg := `{"packs": [{"source": "file://` + root + `", "name": "` + pack + `"}]}`
	return &privateNativeFixture{home: home, userConfig: cfg, packs: map[string]string{pack: root}, check: func() error {
		if err := privateNativeJSON(filepath.Join(root, "pack.json"), manifest); err != nil {
			return err
		}
		for dir, names := range map[string][]string{root: {"pack.json", "tools"}, filepath.Join(root, "tools"): {"x"}} {
			if err := privateNativeOwnedPath(dir, true); err != nil {
				return err
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != len(names) {
				return fmt.Errorf("private native packbuild fixture graph drift: %s", dir)
			}
			for i, entry := range entries {
				if entry.Name() != names[i] {
					return fmt.Errorf("private native packbuild unclassified input: %s", entry.Name())
				}
			}
		}
		return privateNativeFiles(filepath.Join(root, "tools", "x"), map[string]string{"VERSION": "1\n"})
	}}
}

package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func privateNativeDirectories(dirs map[string][]string) error {
	for dir, names := range dirs {
		if err := privateNativeOwnedPath(dir, true); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != len(names) {
			return fmt.Errorf("private native directory graph drift: %s", dir)
		}
		for i, entry := range entries {
			if entry.Name() != names[i] {
				return fmt.Errorf("private native unclassified directory input: %s", entry.Name())
			}
		}
	}
	return nil
}

func packTreeNativeFixture(home, root string) *privateNativeFixture {
	build := `printf '#!/bin/sh\necho x-%s\n' "$(cat VERSION)" > x`
	buildJSON := strings.ReplaceAll(strings.ReplaceAll(build, `\`, `\\`), `"`, `\"`)
	manifest := `{"name":"packtreefx","contributes":[{"kind":"program","bin":"packtreefx-x","via":"pack","from":"tools/x","build":"` + buildJSON + `","run":"x"}]}`
	cfg := `{"packs": [{"source": "file://` + root + `", "name": "packtreefx"}]}`
	return &privateNativeFixture{home: home, userConfig: cfg, packs: map[string]string{"packtreefx": root}, check: func() error {
		if err := privateNativeJSON(filepath.Join(root, "pack.json"), manifest); err != nil {
			return err
		}
		if err := privateNativeDirectories(map[string][]string{root: {"pack.json", "tools"}, filepath.Join(root, "tools"): {"x"}}); err != nil {
			return err
		}
		src := filepath.Join(root, "tools", "x")
		version, err := os.ReadFile(filepath.Join(src, "VERSION"))
		if err != nil || (string(version) != "1\n" && string(version) != "2\n") {
			return fmt.Errorf("private native packtree VERSION is not the planned 1 or 2")
		}
		return privateNativeFiles(src, map[string]string{"VERSION": string(version)})
	}}
}

func packTreeFailureNativeFixture(home, root string) *privateNativeFixture {
	manifest := `{"name":"packtreefail","contributes":[` +
		`{"kind":"program","bin":"packtreefail-broken","via":"pack","from":"tools/broken","build":"echo building; exit 3","run":"x"},` +
		`{"kind":"program","bin":"packtreefail-other","via":"installer","url":"file:///ctx/packs/packtreefail/install.sh"}]}`
	installer := "#!/bin/bash\nset -euo pipefail\nmkdir -p \"$HOME/.local/bin\"\n" +
		"printf '#!/bin/sh\\necho OTHER-RAN\\n' > \"$HOME/.local/bin/packtreefail-other\"\n" +
		"chmod +x \"$HOME/.local/bin/packtreefail-other\"\n"
	cfg := `{"packs": [{"source": "file://` + root + `", "name": "packtreefail"}]}`
	return &privateNativeFixture{home: home, userConfig: cfg, packs: map[string]string{"packtreefail": root}, check: func() error {
		if err := privateNativeJSON(filepath.Join(root, "pack.json"), manifest); err != nil {
			return err
		}
		if err := privateNativeDirectories(map[string][]string{root: {"install.sh", "pack.json", "tools"}, filepath.Join(root, "tools"): {"broken"}}); err != nil {
			return err
		}
		if err := privateNativeOwnedPath(filepath.Join(root, "install.sh"), false); err != nil {
			return err
		}
		current, err := os.ReadFile(filepath.Join(root, "install.sh"))
		if err != nil || string(current) != installer {
			return fmt.Errorf("private native packtree failure installer changed")
		}
		return privateNativeFiles(filepath.Join(root, "tools", "broken"), map[string]string{"main.sh": "echo never built\n"})
	}}
}

package loopholes

import (
	"os"
	"path/filepath"
	"testing"
)

func repoRootDir(t *testing.T) string {
	t.Helper()
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func withRepoBundled(t *testing.T) string {
	t.Helper()
	root := repoRootDir(t)
	orig := BundledLoopholesDir
	dir := filepath.Join(root, "bundled_loopholes")
	BundledLoopholesDir = func() string { return dir }
	t.Cleanup(func() { BundledLoopholesDir = orig })
	return dir
}

func TestBundledManifestsParse(t *testing.T) {
	dir := withRepoBundled(t)
	for _, name := range []string{"audio", "claude-oauth-broker", "host-processes"} {
		lp, err := LoadLoophole(filepath.Join(dir, name))
		if err != nil {
			t.Errorf("load %s: %v", name, err)
			continue
		}
		if lp.Name != name {
			t.Errorf("%s: name = %q", name, lp.Name)
		}
	}
}

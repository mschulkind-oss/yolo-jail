package reporoot

import (
	"os"
	"path/filepath"
	"strings"
)

// FlakeArgv preserves source-checkout Git filtering, but makes local flake references
// explicit path references for a shipped bundle: flake.nix and prebuilt bin/<platform>,
// without go.mod. Such a bundle can sit inside Homebrew's ignored Cellar directory.
// The returned argv has its own backing array. Its command must run in root.
func FlakeArgv(root string, argv []string) []string {
	out := append([]string(nil), argv...)
	if root == "" {
		return out
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); !os.IsNotExist(err) {
		return out
	}
	if _, err := os.Stat(filepath.Join(root, "flake.nix")); err != nil {
		return out
	}
	bundle := false
	for _, platform := range []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"} {
		if info, err := os.Stat(filepath.Join(root, "bin", platform)); err == nil && info.IsDir() {
			bundle = true
			break
		}
	}
	if !bundle {
		return out
	}
	for i, arg := range out {
		if strings.HasPrefix(arg, ".#") {
			out[i] = "path:" + arg
		}
	}
	return out
}

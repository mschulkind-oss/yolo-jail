package hostfloor

import (
	"os"
	"path/filepath"
	"strings"
)

// copies.go names the OTHER copies of a floor program on this machine — the one a user installed
// by hand, Homebrew's, mise's — so the second copy HP-DIR4 creates is never a surprise
// (host-tool-provisioning.md §4, "The other copy is named, never hidden"). It only reports:
// nothing here decides what runs.

// HintLocations is the compiled list host-agent-environment.md's miss line names: the directories an
// agent a user installed by hand usually lands in, looked at whatever PATH yolo was given.
func HintLocations(home string) []string {
	return []string{
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, ".npm-global", "bin"),
		filepath.Join(home, "go", "bin"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, ".nix-profile", "bin"),
		"/opt/homebrew/bin",
		"/home/linuxbrew/.linuxbrew/bin",
	}
}

// OtherCopies lists every executable named bin on pathEnv or at a hint location, skipping the
// floor's own bin/ and any directory in skip (yolo's launch wrappers), each path once, in PATH
// order and then hint order.
func (f *Floor) OtherCopies(bin, pathEnv, home string, skip []string) []string {
	skipped := map[string]bool{filepath.Clean(f.BinDir()): true}
	for _, s := range skip {
		skipped[filepath.Clean(s)] = true
	}
	var dirs []string
	for _, d := range strings.Split(pathEnv, string(os.PathListSeparator)) {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	dirs = append(dirs, f.hints(home)...)
	seen := map[string]bool{}
	var out []string
	for _, d := range dirs {
		d = filepath.Clean(d)
		if skipped[d] {
			continue
		}
		p := filepath.Join(d, bin)
		if seen[p] {
			continue
		}
		seen[p] = true
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode().Perm()&0o111 != 0 {
			out = append(out, p)
		}
	}
	return out
}

// hints is Floor.Hints for home, or the compiled list when no Hints is set.
func (f *Floor) hints(home string) []string {
	if f.Hints != nil {
		return f.Hints(home)
	}
	return HintLocations(home)
}

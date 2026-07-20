package runcmd

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// retireJailMadeVenv ports _retire_jail_made_venv: delete workspace venvs a jail
// materialized under the old shared-store model. Host-side only (skip in-jail),
// only on the fresh-container path. Detection: pyvenv.cfg's `home =` names a
// jail-flavored interpreter dir (/workspace/, /mise/, or the old shared
// ~/.local/share/mise) that does not exist on the host. A resolving home or a
// symlinked venv is left alone.
func (o *Options) retireJailMadeVenv(cfg *jsonx.OrderedMap) {
	if o.inJail() {
		return
	}
	rels := map[string]struct{}{".venv": {}}
	if miseVenv, ok := MiseConfigVenvPathFromDir(o.Workspace); ok && miseVenv != "" {
		rels[miseVenv] = struct{}{}
	}
	jailPrefixes := []string{
		"/workspace/",
		"/mise/",
		filepath.Join(homeDir(), ".local", "share", "mise"),
	}
	out := o.pr(o.Stdout)
	sorted := make([]string, 0, len(rels))
	for r := range rels {
		sorted = append(sorted, r)
	}
	sort.Strings(sorted)
	for _, rel := range sorted {
		if !ValidPerSideRel(rel) {
			continue
		}
		venvDir := filepath.Join(o.Workspace, rel)
		if isSymlink(venvDir) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(venvDir, "pyvenv.cfg"))
		if err != nil {
			continue
		}
		home := ""
		for _, line := range strings.Split(string(data), "\n") {
			key, val, found := strings.Cut(line, "=")
			if found && strings.TrimSpace(key) == "home" {
				home = strings.TrimSpace(val)
				break
			}
		}
		if home == "" {
			continue
		}
		if !hasAnyPrefix(home, jailPrefixes) || fileExists(home) {
			continue
		}
		out.print("[yellow]Removing jail-made " + rel + " — its interpreter at " + home +
			" does not exist on the host[/yellow]")
		_ = os.RemoveAll(venvDir)
	}
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

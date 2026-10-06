package run

// retiredshareddirs.go says, at a fresh launch on a container backend, that a machine-scope
// directory a selected pack once shared is still in the machine store (paths.GlobalHome) and
// can be deleted. A pack says it stopped sharing one with an `unshare_directory` hook, whose
// `at` names the old directory; pi does for `.pi-shared-git` and, since XB-D14 of
// docs/design/pi-extension-store-builds.md, for `.pi-shared-npm`, its old shared npm prefix.
//
// NOTHING DELETES IT FOR YOU, by the move-over-delete rule the unsharing was built under
// (pi-git-extension-caching.md §3.10): a jail an older yolo launched still has the directory
// mounted, and pi's extensions there load from it until that jail stops. So the line names the
// command and the condition, and the user decides.
//
// Container backends only. macos-user keeps the machine tier in the sandbox account's own
// home, which the launching user may not be able to list or delete in without sudo, and which
// nothing here has run against on a Mac; that arm says nothing.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// hookUnshareDirectory is the hook whose `at` names a retired machine-scope dir. Spelled, as
// basehome spells it, because this package must not import internal/entrypoint for one
// constant; TestRetiredSharedDirsReadTheKnownHook pins it to packdecl.KnownHooks.
const hookUnshareDirectory = "unshare_directory"

// retiredSharedDir is one machine-scope dir a selected pack no longer shares.
type retiredSharedDir struct {
	pack, dir string
}

// retiredSharedDirs is the `at` of every unshare_directory hook the selected packs declare,
// less any directory a selected pack still declares shared, less any a SHIPPED pack declares
// shared, and less anything that is not a clean path inside the machine store (packdecl refuses
// one already; this runs on the host, where a path that leaves the store would be a suggestion
// to delete something else).
//
// The shipped set is the one exception to "only the selected packs" (AGENTS.md names it):
// storage.EnsureGlobalStorage makes every shipped pack's shared dir on every machine, whatever a
// workspace selects, so another workspace's jails mount it. A configured pack's hook may name
// any directory, `.claude-shared-credentials` included, and offering that one's `rm -rf` in a
// workspace that does not select claude would log out every claude jail on the machine.
func retiredSharedDirs(packs []*packload.Pack) []retiredSharedDir {
	live := map[string]bool{}
	for _, d := range append(packload.SharedDirs(packs), packload.EmbeddedSharedDirs()...) {
		live[filepath.Clean(filepath.FromSlash(d))] = true
	}
	seen := map[string]bool{}
	var out []retiredSharedDir
	for _, p := range packs {
		if p == nil || p.Decl == nil {
			continue
		}
		for _, h := range p.Decl.HookContributions() {
			if h.Name != hookUnshareDirectory || h.SharedDir == "" {
				continue
			}
			clean := filepath.Clean(filepath.FromSlash(h.SharedDir))
			if filepath.IsAbs(clean) || clean == "." || clean == ".." ||
				strings.HasPrefix(clean, ".."+string(filepath.Separator)) || live[clean] || seen[clean] {
				continue
			}
			seen[clean] = true
			out = append(out, retiredSharedDir{pack: p.Name, dir: clean})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// noteRetiredSharedDirs prints one line for each retired dir still present in the machine
// store as a real directory. Absent, unreadable, a symlink or a file: nothing is said, since
// none of those is the directory the old hook used, and "could not tell" is not "it is there".
func (o *Options) noteRetiredSharedDirs(packs []*packload.Pack) {
	store := paths.GlobalHome()
	for _, r := range retiredSharedDirs(packs) {
		path := filepath.Join(store, r.dir)
		fi, err := os.Lstat(path)
		if err != nil || !fi.IsDir() {
			continue
		}
		o.pr(o.Stderr).print("[dim]" + richtext.Escape(r.pack+" no longer uses its old shared "+
			"folder; once every jail started before this update has stopped, you can delete it: "+
			"rm -rf "+shquote.Quote(path)) + "[/dim]")
	}
}

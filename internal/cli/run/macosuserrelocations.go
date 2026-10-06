package run

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
)

// macosuserrelocations.go is the HOST half of `cache_relocations` on macos-user
// (docs/plans/cache-relocation.md, the macos-user section): the backend has no bind to nest
// inside ~/.cache, so each user-scope entry becomes a link the bootstrap lays at the sandbox
// home's ~/.cache/<subdir> to the target, with the Seatbelt profile opening the target
// (internal/macosuser/ctxlinks.go, its cache_relocations section). This file decides which
// entries the launch delivers and prepares their targets; the backend stages the rest.
//
// ⚠ THE READ IS THE CREDENTIAL BOUNDARY, as it is on podman: the entries come from
// config.LoadCacheRelocations, which reads ~/.config/yolo-jail/config.jsonc alone, and NEVER
// from the merged config, where a workspace-scope entry — one the agent can write — would hand
// out a writable host folder. validateCacheRelocations refuses such an entry at preflight too;
// that is defence in depth, and this read is the boundary.

// macosRelocationACETimeout bounds each `chmod +a` the host CLI runs on a target it created.
const macosRelocationACETimeout = 30 * time.Second

// planMacosUserCacheRelocations returns the relocations this launch delivers, or prints the
// refusal and returns false. Called on the macos-user arm right after the context mounts are
// planned (links are their delivered links, whose sources no target may overlap), before the
// approval prompt, a host service or any staging: a refusal says what to do before the launch
// asks anything else (DP-D15), and a --dry-run refuses too.
//
// For each entry, in order:
//
//  1. RESOLVE the target as the host sees it: through every symlink when it exists, through its
//     parent's when it does not yet (only the last component is ever made). The profile names
//     a path verbatim, and a rule on an unresolved spelling matches nothing.
//  2. SITE it (macosuser.SiteCacheRelocations): never a home, the sandbox home, the state dir,
//     the workspace or a context source; one volume under /Volumes is admitted.
//  3. On a real launch, MAKE a missing target (storage.EnsureCacheRelocationTargets, the last
//     component only, as you) and grant a target made now the sandbox account's inheriting
//     access entries (macosuser.CacheRelocationACECommands), so what the sandbox caches there
//     stays yours to read and delete. A dry run makes and grants nothing, and its plan names
//     the target it would make.
//
// UNDER THE SEAL there are none (seal.go): a fork build's cache is its own workspace's.
func (o *Options) planMacosUserCacheRelocations(links []macosuser.ContextLink) ([]macosuser.CacheRelocation, bool) {
	if o.Sealed {
		return nil, true
	}
	out := o.pr(o.Stderr)
	entries, err := config.LoadCacheRelocations(func(msg string) {
		out.printf("[yellow]Warning: %s[/yellow]", msg)
	})
	if err != nil {
		out.printf("[bold red]%s[/bold red]", err.Error())
		return nil, false
	}
	if len(entries) == 0 {
		return nil, true
	}
	relocs := make([]macosuser.CacheRelocation, 0, len(entries))
	var unresolved []string
	for _, e := range entries {
		target, err := resolveRelocationTarget(e.Target)
		if err != nil {
			unresolved = append(unresolved, "  • ~/.cache/"+e.Subdir+" → "+e.Target+": "+err.Error())
			continue
		}
		relocs = append(relocs, macosuser.CacheRelocation{Subdir: e.Subdir, Target: target, Named: e.Target})
	}
	siting := macosuser.DarwinContextSiting()
	if o.macosCtxSiting != nil {
		siting = *o.macosCtxSiting
	}
	refused := macosuser.SiteCacheRelocations(siting, resolvePath(o.Workspace), relocs, links)
	if len(unresolved) > 0 || len(refused) > 0 {
		out.print("[bold red]Refusing the macos-user launch: this backend cannot deliver every " +
			"cache_relocations entry.[/bold red]")
		for _, line := range unresolved {
			out.print(line)
		}
		for _, r := range refused {
			out.print("  • ~/.cache/" + r.Relocation.Subdir + " → " + r.Relocation.NamedTarget() + ": " + r.Reason)
		}
		out.print("On macos-user a relocation is a link in the sandbox account's ~/.cache to the " +
			"folder itself, and the Seatbelt profile opens that folder, so it must be one the " +
			"sandbox account can reach and that nothing else it uses overlaps: outside every home " +
			"and outside the workspace, on this disk or on one volume under /Volumes. Point the " +
			"entry in ~/.config/yolo-jail/config.jsonc at such a folder, remove it, or use " +
			"`runtime: \"podman\"`" + o.containerStepClause() + ", which mounts it instead.")
		return nil, false
	}
	if o.DryRun {
		return relocs, true
	}
	toEnsure := make([]config.CacheRelocation, len(relocs))
	for i, r := range relocs {
		toEnsure[i] = config.CacheRelocation{Subdir: r.Subdir, Target: r.Target}
	}
	created, err := storage.EnsureCacheRelocationTargets(toEnsure)
	if err != nil {
		out.printf("[bold red]Refusing the macos-user launch: %s[/bold red]", err.Error())
		return nil, false
	}
	for i := range relocs {
		if !created[i] {
			continue
		}
		relocs[i].Created = true
		if why := o.grantRelocationTarget(relocs[i].Target); why != "" {
			out.print("[bold red]Refusing the macos-user launch: the cache_relocations target " +
				relocs[i].Target + " was created, and could not be opened to the sandbox account " +
				"(" + why + ").[/bold red] Grant it with\n  yolo macos-fix-permissions " +
				shquote.Quote(relocs[i].Target) + "\nand launch again, or point the entry at another folder.")
			return nil, false
		}
	}
	return relocs, true
}

// grantRelocationTarget runs macosuser.CacheRelocationACECommands on target, as you, and returns
// why it failed, or "".
func (o *Options) grantRelocationTarget(target string) string {
	for _, argv := range macosuser.CacheRelocationACECommands(target) {
		r := o.Exec(argv, "", nil, macosRelocationACETimeout)
		switch {
		case !r.Ran:
			return "`" + shquote.JoinDisplay(argv) + "` could not be run"
		case r.Timeout:
			return "`" + shquote.JoinDisplay(argv) + "` did not finish"
		case r.RC != 0:
			msg := strings.TrimSpace(r.Stderr)
			if msg == "" {
				msg = fmt.Sprintf("exit %d", r.RC)
			}
			return "`" + shquote.JoinDisplay(argv) + "`: " + msg
		}
	}
	return ""
}

// resolveRelocationTarget is target as the kernel will see it: every symlink resolved when it
// exists, its parent's when only the last component is missing. A missing parent is the typo
// config validation refuses (a launch never creates more than the last component).
func resolveRelocationTarget(target string) (string, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", fmt.Errorf("it is a symbolic link to a folder that does not exist")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", fmt.Errorf("its parent folder %s does not exist (only the last path component "+
			"is created for you)", filepath.Dir(abs))
	}
	return filepath.Join(parent, filepath.Base(abs)), nil
}

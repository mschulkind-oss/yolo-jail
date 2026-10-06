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
//     stays yours to read and delete, and SAY so at once. A grant that fails is a warning, not
//     a refusal: the backend's DAC preflight and write probe decide. A dry run makes and grants
//     nothing, and its plan names the target it would make.
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
	created, ensureErr := ensureMacosRelocationTargets(toEnsure)
	// EVERY TARGET MADE NOW IS GRANTED AND SAID, before a later entry's failure is reported: a
	// folder yolo just made in your filesystem and opened to another account is disclosed the
	// moment it exists, since anything from here to the agent (the approval prompt, an unmet
	// precondition, the account-home hold, the nix build) may still end the launch, and the
	// backend's own per-launch line comes only after all of them.
	for i := range relocs {
		if i >= len(created) || !created[i] {
			continue
		}
		relocs[i].Created = true
		where := "~/.cache/" + relocs[i].Subdir + " (cache_relocations." + relocs[i].Subdir + ")"
		// A GRANT THAT FAILS IS SAID, NOT FATAL (CR-D4's gates are the DAC preflight and the write
		// probe). The usual cause is a volume that takes no access entries, an exFAT or FAT
		// drive, and such a volume usually ignores ownership as well, so the sandbox can write
		// the folder anyway; `yolo macos-fix-permissions` would only fail the same way. If it
		// cannot, the backend's probe refuses, and its message says why this folder cannot be
		// fixed in place (macosuser.CacheRelocationRefusalMessage).
		if why := o.grantRelocationTarget(relocs[i].Target); why != "" {
			relocs[i].GrantFailure = why
			out.print("[yellow]Warning: created " + relocs[i].Target + " for " + where + ", and " +
				"could not add the " + macosuser.SandboxUser + " account's access entries to it (" + why +
				").[/yellow] Its volume most likely does not support them. The launch asks, before the " +
				"agent starts, whether the sandbox can write it anyway, and stops if it cannot.")
			continue
		}
		out.print("[bold]Cache relocation:[/bold] created " + relocs[i].Target + " and opened it to " +
			macosuser.SandboxUser + " for " + where + ".")
	}
	if ensureErr != nil {
		out.print("[bold red]Refusing the macos-user launch: " + ensureErr.Error() + "[/bold red]")
		out.print("yolo creates only a relocation's last folder, as you, and never more. Create the " +
			"folder yourself (`sudo mkdir` and `sudo chown \"$USER\"` it, if only an administrator " +
			"may write the folder above it), point the entry in ~/.config/yolo-jail/config.jsonc at " +
			"a folder you can create, or remove the entry.")
		return nil, false
	}
	return relocs, true
}

// ensureMacosRelocationTargets makes each missing target (storage.EnsureCacheRelocationTargets).
// A variable only so a test can fail one entry: a failing mkdir needs a folder its user may not
// write, which a test running as root cannot make.
var ensureMacosRelocationTargets = storage.EnsureCacheRelocationTargets

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
		// A TARGET THAT IS A FILE is refused here, with the siting's next steps, rather than by
		// the mkdir a real launch would run: a dry run then refuses it too.
		if st, err := os.Stat(resolved); err != nil {
			return "", err
		} else if !st.IsDir() {
			return "", fmt.Errorf("it is a file, not a folder (%s)", resolved)
		}
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

package entrypoint

// hostfiles.go is the entrypoint-side half of host_files
// (docs/reference/composed-file-permissions.md):
// the generic render loop that stages every `host_files` entry into the jail
// home. Where prism.go renders the fixed set of BUILTIN agent surfaces, this
// renders USER-declared surfaces — one per host_files entry — through the very
// same composition engine, so a user file gets defaults/host/overlay/managed
// layering and §5 capture identical to a builtin.
//
// The entries arrive resolved, via the YOLO_HOST_FILES env var (config.Marshal
// HostFiles): the host CLI is the single source of truth (it alone can read the
// user config and stat host sources), and it guarantees the slug this code
// derives for a surface Name matches the /ctx/host-user/<slug> mount the CLI
// emitted. This code never re-reads config — it cannot, on darwin/macos-user,
// where the sandbox user can't see the invoking user's config.
//
// Fail-open throughout: a single malformed or unstageable entry warns and is
// skipped, never aborting boot. A missing host source is normal (the surface
// falls back to its defaults layer); a read-only parent is a CLI-side staging
// gap that degrades to a warning here, not a crash.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// hostUserPath is where the CLI put one source-bearing host_files entry's host bytes:
// <ctx root>/host-user/<slug>, the slug being config.HostFileEntry.Slug.
//
// ⚠ IT READS ctxRoot AT EVERY CALL, and that is the whole design rather than a style.
// There are TWO /ctx readers — packsurfaces.go's, for a pack's `reads-host` grant, and
// this one, for the user's own `host_files` — and the YOLO_CTX_ROOT relocation has to
// reach both or a relocated launch composes the pack's file from the moved tree while
// looking for the user's at a path nothing wrote.
//
// It was `var hostUserDir = ctxRoot + "/host-user"` until 2026-09-13, which derived the
// root correctly and still left the two SEPARATELY OVERRIDABLE: a test could point one at
// a temp dir and leave the other at /ctx, so nothing observed the property the derivation
// existed to guarantee, and rewriting the var as the literal "/ctx/host-user" passed the
// whole suite (measured). Resolving through the one var makes the divergence
// unrepresentable instead of checked — a test that relocates ctxRoot relocates both
// readers by construction.
//
// The relocation is not Apple Container's alone any more. On macos-user /ctx is not merely
// unmounted but ABSENT — a new top-level directory on macOS needs /etc/synthetic.conf and
// a reboot — so a reader left behind names a path that cannot be made to exist (DP-L1).
func hostUserPath(slug string) string {
	return filepath.Join(ctxRootDir(), "host-user", slug)
}

// HostUserPath is hostUserPath for the host-side verbs, which resolve the same staged copy:
// a `user` surface's host layer is the file a LAUNCH put at this path, never the destination
// it was rendered into ([OQ-CR6](docs/reference/config-target-resolution.md#oq-cr6)). Exported
// rather than re-derived in internal/cli for the reason the unexported one exists: two
// spellings of this join is how a reader ends up looking somewhere nothing wrote.
func HostUserPath(slug string) string { return hostUserPath(slug) }

// ConfigureHostFiles stages every host_files entry declared in YOLO_HOST_FILES.
// It is the boot step (and, via RunDarwinBootstrap, the macos-user step) that
// realizes the feature: decode the resolved entries, render each into the jail
// home. An unset/empty var is the feature simply being off — no entries, no
// work, no error.
func ConfigureHostFiles(e *Env) error {
	entries, err := config.UnmarshalHostFiles(e.Getenv("YOLO_HOST_FILES"))
	if err != nil {
		// A decode failure means the CLI emitted something this build can't read —
		// a version skew, effectively. Warn once and stage nothing rather than
		// abort boot over a config-transport problem.
		return fmt.Errorf("host_files: %w", err)
	}
	// FAIL-CLOSED (A12 ruling): a host_files entry that cannot be staged is an
	// ERROR, not a warning. This loop used to warn and continue, which meant a
	// user who declared a file got a jail that came up looking fine with the file
	// missing. A config surface must not fail silently; the caller aborts boot
	// with this error.
	layout, laid := hostFilesLayout(e)
	for _, entry := range entries {
		if err := stageHostFile(e, entry, layout, laid); err != nil {
			return fmt.Errorf("host_files: staging ~/%s: %w", entry.Path, err)
		}
	}
	return nil
}

// hostFilesLayout is the macos-user home layout this launch laid, derived the way the layout
// step derived it (darwinHomeLayoutFor, from the staged packs and this same wire), and false
// on every boot that lays none: the container boot, and an install capture's flat staging
// home.
//
// The packs are loaded here rather than handed in because this step's signature is the boot
// table's generator shape, as the other generators that read packs load them. They are
// needed, not decoration, twice over: a destination under a selected pack's state dir
// (`~/.claude/x`) is reached through that pack's layout link, and one at or below a link that
// pack's hooks laid in configure_pack_surfaces, just before this step (`~/.claude/.credentials.json`),
// through that link (withPackHookLinks, HT-D14). A layout derived without the packs would
// refuse either as a link nobody laid. A load that fails yields the layout the layout step
// laid from the same failure, and configure_pack_surfaces reports it.
func hostFilesLayout(e *Env) (DarwinHomeLayout, bool) {
	if e.DarwinSidecar() == "" {
		return DarwinHomeLayout{Home: e.Home}, false
	}
	packs, _ := LoadJailPacks(e)
	l, laid := darwinHomeLayoutFor(e, packs)
	return l.withPackHookLinks(e, packs), laid
}

// hostFileDestination is where ONE entry is written: ~/<path> wherever no macos-user layout
// was laid, and, where one was, the PHYSICAL path the layout's own links lead to
// (DarwinHomeLayout.homeFileThroughLayout), or the refusal naming the link on the way that is
// not one of them.
//
// WHY. On macos-user this step runs outside Seatbelt as the sandbox account, which can write
// every workspace under the shared root, and the composition engine writes by PATH — its
// MkdirAll, its truncating write and the readonly chmod each follow every link they meet. A
// home-root entry is a layout link to .config/yolo-home/<slug> and ~/.config is a link into
// the workspace sidecar, which the agent can write, so a link the agent left there (at
// yolo-home, at the file itself, or below any other ~/.config destination) carried this write
// into a directory the agent cannot reach, such as another workspace's .git (MEASURED on
// Linux against the real bootstrap, 2026-10-04). Every destination is walked, not only those
// past a layout link: a link in the account home is somebody else's just the same, since
// yolo lays none there that this walk does not know. The links the selected packs' hooks lay
// are among those it knows, so an entry at `~/.claude/.credentials.json` is written into the
// shared credential that link leads to, as on podman.
//
// What a path check cannot cover is stated on homeFileThroughLayout: a link swapped in between
// this walk and the write. For a destination in the workspace sidecar only a session of that
// workspace can make the swap; for one in the account home (`~/.aws/config`, a machine-scope
// shared dir, the shared file a hook's link leads to) a session of ANY workspace can, since
// every session's sandbox profile allows writes to the whole account home.
//
// A LOGIN RC FILE IS REFUSED where a layout was laid (HT-D13). The bootstrap writes
// DarwinLoginRCFiles itself later in this same boot (WriteLoginRC), so the entry's bytes would
// be replaced before any shell read them, in every mode, and a `readonly` entry would leave the
// shared account-home file 0444, which WriteLoginRC, running as the account that owns it, cannot
// open for writing: every workspace's launch on the Mac would then fail. An entry that cannot be
// delivered is an error here (the A12 ruling above), so the refusal names the next step instead.
func hostFileDestination(e *Env, entry config.HostFileEntry, layout DarwinHomeLayout, laid bool) (string, error) {
	if !laid {
		return expandHomePath(e, "~/"+entry.Path), nil
	}
	if slices.Contains(DarwinLoginRCFiles(), entry.Path) {
		return "", loginRCHostFileError(entry.Path)
	}
	return layout.homeFileThroughLayout(entry.Path)
}

// loginRCHostFileError is HT-D13's refusal: what yolo does with the file, why the entry cannot
// reach it, and the one edit that clears it. zsh reads ~/.zshenv first, and nothing yolo writes
// is named that, so a zsh entry has somewhere to go; bash's login file has no such sibling here
// (bash reads only the first of .bash_profile, .bash_login and .profile, and ~/.bashrc is
// yolo's on every backend).
func loginRCHostFileError(rel string) error {
	msg := fmt.Sprintf("on macos-user yolo writes ~/%s itself on every launch, to put the "+
		"sandbox PATH back after macOS path_helper reorders it, so this entry would be "+
		"replaced before any shell read it. Remove ~/%s from host_files", rel, rel)
	if strings.HasPrefix(rel, ".z") {
		msg += "; for zsh, declare ~/.zshenv instead, which zsh reads before ~/.zprofile and " +
			"~/.zshrc and yolo does not write"
	}
	return errors.New(msg)
}

// stageHostFile renders or copies ONE entry. A directory entry is a recursive
// copy (there is no per-file codec to run); a file entry routes through the
// composition engine per its mode.
func stageHostFile(e *Env, entry config.HostFileEntry, layout DarwinHomeLayout, laid bool) error {
	if entry.IsDir {
		// A directory entry is always source-bearing (checkHostFileObject rejects a
		// dir with no source), so its tree lives at the /ctx/host-user/<slug> mount.
		// Nothing there (source absent on the host, or macos-user, which delivers
		// FILE sources by copy since DP-L1 and still refuses a DIRECTORY one — a copy
		// does not scale to an arbitrary tree) leaves nothing to copy: fail-open,
		// matching a missing file source.
		src := hostUserPath(entry.Slug())
		if _, err := os.Stat(src); err != nil {
			return nil
		}
		dest, err := hostFileDestination(e, entry, layout, laid)
		if err != nil {
			return err
		}
		return copyTree(src, dest)
	}
	dest, err := hostFileDestination(e, entry, layout, laid)
	if err != nil {
		return err
	}
	return renderHostFileSurface(e, entry, dest)
}

// renderHostFileSurface composes a single FILE entry, dispatching on its mode.
// The four modes differ only in what happens across boots and to in-jail edits;
// they share the surface construction and the host-layer bytes.
// No overlays cross into a host_files surface, and that is a boundary rather than an
// omission: a `config-overlay` names a surface a PACK owns, while these are the USER's own
// entries (Agent="user", Name=slug). A pack that could contribute keys to them would be
// asserting into a file the user declared for themselves, which is the opposite of the
// consent direction every other pack claim runs in.
//
// dest is the file to write (hostFileDestination). Where it is not simply ~/<path> — on
// macos-user, past a layout link — the surface's own path is pointed at it, so the stat, the
// render and the chmod below all name the one checked file rather than re-resolving the links
// the walk checked. The §5 sidecars key on the surface's agent and name, never on its path, so
// a captured edit is found again on every boot whatever the path resolved to; what changes is
// only that the capture notice names the physical file there.
func renderHostFileSurface(e *Env, entry config.HostFileEntry, dest string) error {
	surface := HostFileSurface(entry)
	if dest != expandHomePath(e, surface.Path) {
		surface.Path = dest
	}
	hostBytes := hostFileLayerBytes(entry)

	switch entry.Mode {
	case config.HostFileModeCapture:
		// THE overlay exception: render statefully so in-jail edits are captured
		// into a sidecar that outranks the host layer. The sidecars key on
		// (surface.Agent="user", surface.Name=slug), collision-free with builtins.
		_, err := renderSurfaceStatefulSurface(e, surface, hostBytes, nil, nil, nil)
		return err

	case config.HostFileModeOnce:
		// Seed when absent, then never touch. An existing file — a prior seed the
		// agent may since have edited — is left exactly as it is.
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		_, err := renderSurfaceStatelessSurface(e, surface, hostBytes, nil, nil)
		return err

	case config.HostFileModeReadonly:
		// Re-render every boot at 0o444 (0o555 when the source is executable). The dest
		// from a prior boot is not writable, and a non-root agent can't reopen it O_TRUNC
		// (writeInPlaceString's truncate-in-place needs write permission) — so restore a
		// writable mode first, then re-lock. A root agent (Claude YOLO) bypasses the bits
		// either way; the chmod is harmless there and load-bearing for everyone else.
		//
		// The exec bit must survive BOTH chmods: unlocking to a non-executable 0o644 and
		// then re-locking to 0o555 would work, but unlocking to 0o644 and re-locking to
		// 0o444 (the old code) silently strips it — which is the whole bug. Derive both
		// modes from the source so there is one decision, not two that can disagree.
		locked, unlocked := hostFileModes(entry)
		// The unlock's error is dropped because the render below reports the same fact
		// better: the chmod exists only so the truncating write can open the file, so a
		// failure that matters returns from renderSurfaceStatelessSurface as a named,
		// FATAL host_files error. A failure that does not matter (the agent is root and
		// the bits never applied to it) would be a warning about a write that worked.
		if _, err := os.Stat(dest); err == nil {
			_ = os.Chmod(dest, unlocked)
		}
		if _, err := renderSurfaceStatelessSurface(e, surface, hostBytes, nil, nil); err != nil {
			return err
		}
		return os.Chmod(dest, locked)

	default: // config.HostFileModeCopy
		// Overwrite every boot at 0o644 (0o755 when the source is executable); in-jail
		// edits are deliberately not kept.
		if _, err := renderSurfaceStatelessSurface(e, surface, hostBytes, nil, nil); err != nil {
			return err
		}
		_, unlocked := hostFileModes(entry)
		return os.Chmod(dest, unlocked)
	}
}

// hostFileModes returns the (locked, unlocked) permission pair for an entry, derived from
// whether its HOST SOURCE carries an execute bit.
//
// Source-derived rather than a new config knob, and that is the point: `host_files` means
// "mirror this host file into the jail", so a file that is executable on the host must
// arrive executable — otherwise an agent told to run it (a `fileSuggestion` command, a git
// hook, any wired-up script) gets EACCES. No mode previously yielded an executable, so
// `host_files` could not carry a script at all.
//
// Only the 0o111 bits are taken from the source; the read/write bits stay yolo's decision,
// so a group- or world-writable host file does not widen the jail copy.
func hostFileModes(entry config.HostFileEntry) (locked, unlocked os.FileMode) {
	if hostSourceIsExecutable(entry) {
		return 0o555, 0o755
	}
	return 0o444, 0o644
}

// hostSourceIsExecutable reports whether the entry's host source has any execute bit.
//
// Fail-CLOSED on anything unclear (no source, unreadable mount, a content/layers-only
// entry): a file yolo cannot prove was executable is rendered non-executable. Granting the
// exec bit on a guess is the wrong direction to be wrong in.
func hostSourceIsExecutable(entry config.HostFileEntry) bool {
	if !entry.SourceBearing() {
		return false
	}
	fi, err := os.Stat(hostUserPath(entry.Slug()))
	if err != nil {
		return false
	}
	return fi.Mode().Perm()&0o111 != 0
}

// HostFileSurface lowers a resolved entry into the manifest.Surface the engine
// composes. Owner is the fixed pseudo-agent "user" (no real agent is named
// that), and the surface Name is the injective slug — together they key the §5
// sidecars and keep every user surface distinct from every builtin.
//
// EXPORTED for the host-side `yolo config reset` of a jail's captured edits
// ([OQ-CR4](docs/reference/config-target-resolution.md#oq-cr4)), which has to compose the pure
// render of a `user` surface and until then had no codec for one at all — the
// PruneWorkspaceKeyed precedent, a boot-render internal exported so the CLI's truncation
// calls the one definition rather than a second one free to disagree about which layers an
// entry contributes.
func HostFileSurface(entry config.HostFileEntry) manifest.Surface {
	return manifest.Surface{
		Agent:    "user",
		Name:     entry.Slug(),
		Path:     "~/" + entry.Path,
		Codec:    entry.Codec,
		Defaults: entry.Defaults,
		Managed:  entry.Managed,
	}
}

// hostFileLayerBytes resolves the `host` layer bytes for a file entry:
//
//   - a source-bearing entry reads its /ctx/host-user/<slug> path, fail-open —
//     nothing there (the host source does not exist, or this launch delivered no
//     host bytes) yields nil and the surface falls back to defaults<managed. ⚠ That
//     parenthetical named macos-user until 2026-09-13, when DP-L1 gave it a copy into
//     a root-owned tree hostUserPath resolves through; a FILE source arrives there
//     like anywhere else now;
//   - a content entry uses the inline literal verbatim (HasContent distinguishes
//     an explicit empty file from an absent one);
//   - a layers-only entry (defaults/managed, no source/content) has no host
//     layer at all — nil.
func hostFileLayerBytes(entry config.HostFileEntry) []byte {
	switch {
	case entry.SourceBearing():
		b, _ := os.ReadFile(hostUserPath(entry.Slug()))
		return b
	case entry.HasContent:
		return []byte(entry.Content)
	default:
		return nil
	}
}

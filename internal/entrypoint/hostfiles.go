package entrypoint

// hostfiles.go is the entrypoint-side half of docs/plans/host-file-staging.md:
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
	"fmt"
	"os"
	"path/filepath"

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
	for _, entry := range entries {
		if err := stageHostFile(e, entry); err != nil {
			return fmt.Errorf("host_files: staging ~/%s: %w", entry.Path, err)
		}
	}
	return nil
}

// stageHostFile renders or copies ONE entry. A directory entry is a recursive
// copy (there is no per-file codec to run); a file entry routes through the
// composition engine per its mode.
func stageHostFile(e *Env, entry config.HostFileEntry) error {
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
		return copyTree(src, expandHomePath(e, "~/"+entry.Path))
	}
	return renderHostFileSurface(e, entry)
}

// renderHostFileSurface composes a single FILE entry, dispatching on its mode.
// The four modes differ only in what happens across boots and to in-jail edits;
// they share the surface construction and the host-layer bytes.
// No overlays cross into a host_files surface, and that is a boundary rather than an
// omission: a `config-overlay` names a surface a PACK owns, while these are the USER's own
// entries (Agent="user", Name=slug). A pack that could contribute keys to them would be
// asserting into a file the user declared for themselves, which is the opposite of the
// consent direction every other pack claim runs in.
func renderHostFileSurface(e *Env, entry config.HostFileEntry) error {
	surface := HostFileSurface(entry)
	hostBytes := hostFileLayerBytes(entry)
	dest := expandHomePath(e, surface.Path)

	switch entry.Mode {
	case config.HostFileModeCapture:
		// THE overlay exception: render statefully so in-jail edits are captured
		// into a sidecar that outranks the host layer. The sidecars key on
		// (surface.Agent="user", surface.Name=slug), collision-free with builtins.
		_, err := renderSurfaceStatefulSurface(e, surface, hostBytes, nil, nil)
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

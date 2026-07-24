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

// hostUserDir is the read-only mount root under which the CLI binds each
// source-bearing host_files entry at /ctx/host-user/<slug> — the entry's slug
// (config.HostFileEntry.Slug) is the leaf. A var so tests can point it at a temp
// dir, mirroring hostPiDir / hostClaudeDir.
var hostUserDir = "/ctx/host-user"

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
	for _, entry := range entries {
		if err := stageHostFile(e, entry); err != nil {
			e.warn(fmt.Sprintf("Warning: host_files: staging %s: %v",
				"~/"+entry.Path, err))
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
		// A missing mount (source absent on the host, or macos-user with no /ctx)
		// leaves nothing to copy — fail-open, matching a missing file source.
		src := filepath.Join(hostUserDir, entry.Slug())
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
func renderHostFileSurface(e *Env, entry config.HostFileEntry) error {
	surface := hostFileSurface(entry)
	hostBytes := hostFileLayerBytes(entry)
	dest := expandHomePath(e, surface.Path)

	switch entry.Mode {
	case config.HostFileModeCapture:
		// THE overlay exception: render statefully so in-jail edits are captured
		// into a sidecar that outranks the host layer. The sidecars key on
		// (surface.Agent="user", surface.Name=slug), collision-free with builtins.
		_, err := renderSurfaceStatefulSurface(e, surface, hostBytes, nil)
		return err

	case config.HostFileModeOnce:
		// Seed when absent, then never touch. An existing file — a prior seed the
		// agent may since have edited — is left exactly as it is.
		if _, err := os.Stat(dest); err == nil {
			return nil
		}
		_, err := renderSurfaceStatelessSurface(e, surface, hostBytes, nil)
		return err

	case config.HostFileModeReadonly:
		// Re-render every boot at 0o444. The dest from a prior boot is 0o444, and a
		// non-root agent can't reopen a 0o444 file O_TRUNC (writeInPlaceString's
		// truncate-in-place needs write permission) — so restore 0o644 first, then
		// re-lock. A root agent (Claude YOLO) bypasses the bits either way; the
		// chmod is harmless there and load-bearing for everyone else.
		if _, err := os.Stat(dest); err == nil {
			_ = os.Chmod(dest, 0o644)
		}
		if _, err := renderSurfaceStatelessSurface(e, surface, hostBytes, nil); err != nil {
			return err
		}
		return os.Chmod(dest, 0o444)

	default: // config.HostFileModeCopy
		// Overwrite every boot at 0o644; in-jail edits are deliberately not kept.
		_, err := renderSurfaceStatelessSurface(e, surface, hostBytes, nil)
		return err
	}
}

// hostFileSurface lowers a resolved entry into the manifest.Surface the engine
// composes. Owner is the fixed pseudo-agent "user" (no real agent is named
// that), and the surface Name is the injective slug — together they key the §5
// sidecars and keep every user surface distinct from every builtin.
func hostFileSurface(entry config.HostFileEntry) manifest.Surface {
	return manifest.Surface{
		Agent:     "user",
		Name:      entry.Slug(),
		Path:      "~/" + entry.Path,
		Codec:     entry.Codec,
		Defaults:  entry.Defaults,
		Managed:   entry.Managed,
		Transform: entry.Transform,
	}
}

// hostFileLayerBytes resolves the `host` layer bytes for a file entry:
//
//   - a source-bearing entry reads its /ctx/host-user/<slug> mount, fail-open —
//     a missing mount (host source absent, or macos-user with no /ctx) yields
//     nil and the surface falls back to defaults<managed;
//   - a content entry uses the inline literal verbatim (HasContent distinguishes
//     an explicit empty file from an absent one);
//   - a layers-only entry (defaults/managed, no source/content) has no host
//     layer at all — nil.
func hostFileLayerBytes(entry config.HostFileEntry) []byte {
	switch {
	case entry.SourceBearing():
		b, _ := os.ReadFile(filepath.Join(hostUserDir, entry.Slug()))
		return b
	case entry.HasContent:
		return []byte(entry.Content)
	default:
		return nil
	}
}

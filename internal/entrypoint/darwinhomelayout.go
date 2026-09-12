package entrypoint

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// darwinhomelayout.go is the macos-user WORKSPACE TIER: the symlink layout that gives the
// one sandbox account a per-workspace home without moving HOME
// (docs/design/macos-user-home-tiers.md, alternative A′).
//
// WHAT IT IS. `HOME` stays /Users/_yolojail. Every directory the podman argv binds from
// <workspace>/.yolo/home becomes a symlink from the account home into that same sidecar, so
// a project's agent state lives at the location every other backend already uses — a
// symlink where podman has a bind. Everything the layout does NOT link stays in the account
// home, which is the machine tier: credentials, the mise store, ~/.cache.
//
// WHY NOT A PER-WORKSPACE HOME (alternative A, the recorded runner-up). Credential sharing
// is one mechanism on every backend — a pack declares a `scope: machine` dir and the
// `shared_credentials` hook writes a RELATIVE symlink into it — and a home that reached
// credentials some other way would make "where are my credentials" a per-backend question
// that every pack touching them would have to feature-detect (§5.0).
//
// THE TRAP, AND IT IS THE WHOLE REASON Mirrors EXISTS. That hook's symlink is relative BY
// DESIGN, so it resolves through whichever mount backs the agent's state dir. The kernel
// resolves `..` PHYSICALLY — measured on Linux and confirmed on macOS 26.5 — so through a
// bare ~/.claude → <ws>/.yolo/home/claude link, `../.claude-shared-credentials/…` lands in
// the SIDECAR, not the account home, and dangles. A dangling shared path is not inert:
// linkThroughShared's "the shared file always wins" rule then copies the local credential
// into the dangling location and the machine tier never sees it again. So every
// `sharedDirs` entry is mirrored back into the sidecar as a symlink to the account home's
// real directory, and the hook's output stays byte-identical on every backend.
//
// NO MIGRATION (OQ-HT2). A real directory where a link belongs is not migrated, copied or
// renamed — the launch refuses and names the path. `sudo rm -rf /Users/_yolojail` before
// the first launch IS the migration.

// DarwinHomeSidecarEnv names the workspace sidecar (<workspace>/.yolo/home) for the native
// bootstrap. ABSENCE MEANS "LAY NO LAYOUT", which is not a degraded mode: an install
// capture bootstraps a throwaway staging home whose whole contract is that everything an
// installer writes lands under it (capture walks paths.HomeSurfaces() to compute its
// delta, and WalkDir does not follow symlinks), so a capture must keep the flat home. The
// launcher sets it; the capture planner does not.
const DarwinHomeSidecarEnv = "YOLO_DARWIN_HOME_SIDECAR"

// DarwinLoginPathEnv carries the sandbox's real PATH (macosuser.SandboxPath). It is baked
// onto BOTH argvs the backend emits, by the same call: the bootstrap reads it to decide what
// the launch already provides (imageProbePath, agentPath), and the login rc files written by
// WriteLoginRC re-prepend it after macOS path_helper has reordered PATH.
//
// One name for one value, because the rc files live in a home every workspace shares: the
// moment the rc bakes a literal instead of reading this, it is one workspace's `packages:`
// store dirs in the next workspace's login shell.
const DarwinLoginPathEnv = "YOLO_DARWIN_LOGIN_PATH"

// DarwinHomeLink is one symlink in the layout: where it goes, and the target string
// written verbatim.
//
// The sidecar links use ABSOLUTE targets. That is not in tension with the relative link
// the shared_credentials hook writes: the hook's output is pack-facing and must be
// identical on every backend, while these are the launcher's own layout — the "make it
// appear at this path" half of a bind, done with a symlink.
type DarwinHomeLink struct {
	Path   string
	Target string
}

// DarwinHomeLayout is the whole layout for one launch, in the order it must be applied.
type DarwinHomeLayout struct {
	// Home is the account home the layout was derived for. Carried so a refusal can name
	// the reset without this package having to know what that account is called —
	// macosuser imports entrypoint, never the reverse.
	Home string
	// Dirs are created BEFORE any link. Two reasons, both load-bearing: a link to a
	// missing directory dangles, and MkdirAll THROUGH a dangling symlink fails (Stat
	// misses, Mkdir hits EEXIST, Lstat says "not a directory") — which is how the first
	// generator to write through ~/.yolo/bin would fail the boot.
	Dirs []string
	// Links are the workspace tier: an account-home path pointing into the sidecar.
	Links []DarwinHomeLink
	// Mirrors are the machine tier as the SIDECAR sees it: <sidecar>/<sharedDir> pointing
	// back at the account home's real directory, so the hook's relative link resolves.
	// They must exist before RunPackHooks — see the file header for what happens if not.
	Mirrors []DarwinHomeLink
	// FileRedirects are the home-root FILES the container keeps as symlinks into a
	// per-workspace directory (storage.EnsureGlobalStorage writes the same three into
	// GlobalHome). Their targets are relative, spelled as the container spells them, and
	// they resolve through the Links above — so they are created after them.
	FileRedirects []DarwinHomeLink
}

// DeriveDarwinHomeLayout is the pure deriver: home, the sidecar, and the two pack-declared
// tier lists in, the layout out.
//
// THE LIST IS THE CONTAINER'S, NOT A NEW ONE, and that is a design constraint rather than
// tidiness: a directory added to the podman mount table and not here (or the reverse) is
// exactly the drift A′ exists to end, so every entry below cites the mount it mirrors.
// `writableDirs` is packload.WritableDirs (scope: workspace) and `sharedDirs` is
// packload.SharedDirs (scope: machine) — the same two lists assemble.go consumes, which is
// what makes the tier of a path the PACK's declaration on this backend too (P2).
func DeriveDarwinHomeLayout(home, sidecar string, writableDirs, sharedDirs []string) DarwinHomeLayout {
	l := DarwinHomeLayout{Home: home}
	link := func(homeRel, subtree string) {
		target := filepath.Join(sidecar, subtree)
		l.Dirs = append(l.Dirs, target)
		l.Links = append(l.Links, DarwinHomeLink{Path: filepath.Join(home, homeRel), Target: target})
	}
	// The three INSTALLED-PROGRAM surfaces, from the one list prune and capture also key
	// on (paths.HomeSurfaces; podman binds each at /home/agent/<HomeRel>).
	for _, s := range paths.HomeSurfaces() {
		link(s.HomeRel, s.Subtree)
	}
	// The generated-script anchor and the config dir (assemble_parts.go, podmanBaseMounts).
	// ~/.yolo/bin is FIRST among what the boot writes through — GenerateShims is genStep #1
	// and writes into it — which is why the layout is applied above every generator rather
	// than merely before the pack hooks.
	link(filepath.Join(".yolo", "bin"), "yolo-bin")
	link(".config", "config")
	// Pack-declared workspace state. The `.`-trim is the container's own spelling
	// (assemble.go: <wsState>/claude ⇒ /home/agent/.claude).
	for _, dir := range writableDirs {
		link(dir, strings.TrimPrefix(dir, "."))
	}
	// Pack-declared machine state, mirrored INTO the sidecar. The real directory is created
	// in the account home first so the mirror never dangles — and so the hook's MkdirAll of
	// it cannot be the thing that fails.
	for _, dir := range sharedDirs {
		real := filepath.Join(home, dir)
		l.Dirs = append(l.Dirs, real)
		l.Mirrors = append(l.Mirrors, DarwinHomeLink{Path: filepath.Join(sidecar, dir), Target: real})
	}
	for _, r := range paths.HomeFileRedirects() {
		l.FileRedirects = append(l.FileRedirects,
			DarwinHomeLink{Path: filepath.Join(home, r.Name), Target: r.Target})
	}
	return l
}

// Apply lays the layout down, idempotently, in the order the fields are declared.
//
// Every occupied path is collected and reported TOGETHER. One at a time would make the
// first launch after this shipped a sequence of refusals, each naming one directory of an
// account the user is being told to wipe anyway.
func (l DarwinHomeLayout) Apply() error {
	for _, dir := range l.Dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var occupied []string
	for _, group := range [][]DarwinHomeLink{l.Links, l.Mirrors} {
		for _, ln := range group {
			if err := ensureLayoutSymlink(ln.Path, ln.Target, &occupied); err != nil {
				return err
			}
		}
	}
	for _, r := range l.FileRedirects {
		// The target's directory, resolved THROUGH the links above: ~/.gitconfig points at
		// .config/git/config, and `git config --global` writing through a symlink whose
		// parent does not exist is a failure the container never sees, because its
		// .config is a mount of a directory that does.
		dir := filepath.Join(filepath.Dir(r.Path), filepath.Dir(r.Target))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		if err := ensureLayoutSymlink(r.Path, r.Target, &occupied); err != nil {
			return err
		}
	}
	if len(occupied) > 0 {
		return fmt.Errorf("the sandbox home predates the per-workspace layout: %s %s real, "+
			"where the workspace tier's symlink belongs.\n"+
			"There is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here is "+
			"copied, renamed or deleted for you.\n"+
			"Move what you want to keep, then reset the account:\n  sudo rm -rf %s",
			strings.Join(occupied, ", "), plural(len(occupied), "is", "are"), l.Home)
	}
	return nil
}

// ensureLayoutSymlink makes path a symlink to target, or records path as occupied.
//
// A real file or directory is NEVER removed: that is OQ-HT2's no-migration ruling as code.
// A symlink pointing somewhere else IS replaced — it is a layout yolo wrote, and the
// sidecar it names moved.
func ensureLayoutSymlink(path, target string, occupied *[]string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if cur, err := os.Readlink(path); err == nil {
		if cur == target {
			return nil
		}
		if err := os.Remove(path); err != nil {
			return err
		}
	} else if _, lerr := os.Lstat(path); lerr == nil {
		*occupied = append(*occupied, path)
		return nil
	}
	return os.Symlink(target, path)
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// InstallDarwinHomeLayout is the boot-path entry: derive from this Env and the staged packs,
// then apply. A launch that named no sidecar lays nothing (see DarwinHomeSidecarEnv).
func InstallDarwinHomeLayout(e *Env, packs []*packload.Pack) error {
	sidecar := e.Getenv(DarwinHomeSidecarEnv)
	if sidecar == "" {
		return nil
	}
	return DeriveDarwinHomeLayout(e.Home, sidecar,
		packload.WritableDirs(packs), packload.SharedDirs(packs)).Apply()
}

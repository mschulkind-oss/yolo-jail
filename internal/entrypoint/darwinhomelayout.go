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
// the SIDECAR, not the account home, and dangles. So every `sharedDirs` entry is mirrored
// back into the sidecar as a symlink to the account home's real directory, and the hook's
// output stays byte-identical on every backend.
//
// ⚠ WHO LOSES THE CREDENTIAL, corrected 2026-09-12. This comment used to say the loss
// happens IN THE HOOK — that linkThroughShared's "the shared file always wins" rule copies
// the local credential into the dangling location. It does not, and the correction matters
// because it is what the ordering constraint is actually about. `linkSharedCredential`
// builds `shared` as filepath.Join(e.Home, sharedDir, base) — ABSOLUTE (packhooks.go) —
// and every write in linkThroughShared targets that path, never the relative one
// (claude.go). The hook therefore lands its bytes correctly with no mirror at all. What
// dangles is the LINK IT LEAVES BEHIND, and the loss happens later, when the AGENT reads
// or writes through it. So the constraint is "the mirror exists before anything resolves
// through the link", i.e. before the agent — not "before RunPackHooks". It is applied with
// the Links anyway, which satisfies both readings and is why the overstatement was
// invisible.
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
	// They must exist before anything RESOLVES one — the agent, in practice — which is why
	// they are applied with the Links, above every generator. See the file header for the
	// correction: it is not the pack hook that loses the credential.
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
	// The home-relative directories THIS layout lays, which is what decides whether a
	// home-root file redirect has anywhere to point (see the FileRedirects loop below).
	laid := map[string]struct{}{}
	link := func(homeRel, subtree string) {
		target := filepath.Join(sidecar, subtree)
		l.Dirs = append(l.Dirs, target)
		l.Links = append(l.Links, DarwinHomeLink{Path: filepath.Join(home, homeRel), Target: target})
		laid[homeRel] = struct{}{}
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
	// Home-root FILES, but ONLY the ones whose holding directory this layout actually lays.
	// The list is CORE (paths.HomeFileRedirects) while the directories it points into are
	// not: `.claude.json` targets `.claude/claude.json`, and `.claude` is a link only when a
	// PACK declares it. A launch that declares no packs therefore used to require ~/.claude
	// to exist as a directory anyway — and where a previous launch had left a link to a
	// workspace since DELETED, Apply's MkdirAll hit a dangling symlink and failed with
	// `mkdir …: file exists`, refusing the boot with no remedy named and no way out for any
	// later launch without that pack. Three of the six macos-user twins failed exactly this
	// way on their first hardware run (2026-09-12).
	//
	// P2's rule settles it: the layout manages what THIS launch declares. ~/.claude.json
	// means nothing without a ~/.claude to hold it, so it is not laid — and the stale link
	// itself is left ALONE rather than removed, because removing it would either break a live
	// sidecar's link or leave a real directory OQ-HT2 then refuses forever.
	for _, r := range paths.HomeFileRedirects() {
		holder := strings.SplitN(filepath.ToSlash(r.Target), "/", 2)[0]
		if _, ok := laid[holder]; !ok {
			continue
		}
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
//
// ⚠ AND IN TWO GROUPS, BECAUSE THEY HAVE TWO DIFFERENT REMEDIES — which is the whole
// reason this is not one list. A Link or a FileRedirect is a path in the ACCOUNT HOME, so
// `sudo rm -rf <home>` reaches it. A Mirror is a path in the WORKSPACE SIDECAR, which that
// command does not touch: prescribing it for a mirror sends the reader to destroy the
// machine tier the mirror exists to preserve AND get the identical refusal on the next
// launch, because the offending directory was never in the account. A refusal whose remedy
// cannot reach the path it names is worse than no remedy — it looks actionable.
//
// Recorded as one of the two defects a mutation pass found and this fixes:
// docs/design/macos-user-home-tiers.md §10, runbook item 10
// (docs/plans/runbooks/macos-user-manual-checks.md).
func (l DarwinHomeLayout) Apply() error {
	for _, dir := range l.Dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	var inHome []string
	var inSidecar []DarwinHomeLink
	for _, ln := range l.Links {
		occupied, err := ensureLayoutSymlink(ln.Path, ln.Target)
		if err != nil {
			return err
		}
		if occupied {
			inHome = append(inHome, ln.Path)
		}
	}
	for _, ln := range l.Mirrors {
		occupied, err := ensureLayoutSymlink(ln.Path, ln.Target)
		if err != nil {
			return err
		}
		if occupied {
			inSidecar = append(inSidecar, ln)
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
		occupied, err := ensureLayoutSymlink(r.Path, r.Target)
		if err != nil {
			return err
		}
		if occupied {
			inHome = append(inHome, r.Path)
		}
	}
	if len(inHome)+len(inSidecar) == 0 {
		return nil
	}
	return occupiedLayoutError(l.Home, inHome, inSidecar)
}

// occupiedLayoutError is the refusal, split out so the two-remedy rule above is one
// function a test can read rather than a format string inside a loop.
//
// The paths are listed one per line and INDENTED. A comma-joined run of absolute paths is
// the form a reader cannot copy out of a CI log, and this refusal's whole job is to be
// acted on by somebody who has only the log.
func occupiedLayoutError(home string, inHome []string, inSidecar []DarwinHomeLink) error {
	var b strings.Builder
	n := len(inHome) + len(inSidecar)
	fmt.Fprintf(&b, "the per-workspace home layout cannot be laid: %d %s real, where the "+
		"workspace tier's symlink belongs.\n", n, plural(n, "path is", "paths are"))
	b.WriteString("There is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here " +
		"is copied, renamed or deleted for you.\n")
	if len(inHome) > 0 {
		b.WriteString("\nIn the SANDBOX ACCOUNT HOME, which predates this layout:\n")
		for _, p := range inHome {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		fmt.Fprintf(&b, "Move what you want to keep, then reset the account:\n  sudo rm -rf %s\n", home)
	}
	if len(inSidecar) > 0 {
		b.WriteString("\nIn the WORKSPACE SIDECAR — and `sudo rm -rf " + home +
			"` does NOT touch these, so resetting the account would lose your credentials " +
			"and leave the launch refusing:\n")
		for _, ln := range inSidecar {
			fmt.Fprintf(&b, "  %s (mirrors %s)\n", ln.Path, ln.Target)
		}
		b.WriteString("Each is a real directory where a mirror of a machine-scope directory " +
			"belongs — a copy stranded by a container-era launch, whose live original is the " +
			"path in parentheses. Remove the WORKSPACE copy:\n")
		for _, ln := range inSidecar {
			fmt.Fprintf(&b, "  sudo rm -rf %s\n", ln.Path)
		}
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// ensureLayoutSymlink makes path a symlink to target, and reports whether a real file or
// directory was sitting there instead.
//
// A real file or directory is NEVER removed: that is OQ-HT2's no-migration ruling as code.
// A symlink pointing somewhere else IS replaced — it is a layout yolo wrote, and the
// sidecar it names moved.
//
// It RETURNS the occupancy rather than appending to a caller's slice, because the caller
// is the only thing that knows which TIER the path is in, and that decides the remedy
// (see Apply).
func ensureLayoutSymlink(path, target string) (occupied bool, err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if cur, err := os.Readlink(path); err == nil {
		if cur == target {
			return false, nil
		}
		if err := os.Remove(path); err != nil {
			return false, err
		}
	} else if _, lerr := os.Lstat(path); lerr == nil {
		return true, nil
	}
	return false, os.Symlink(target, path)
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

// DarwinSidecar returns this Env's workspace sidecar (<workspace>/.yolo/home), or "" when
// the launcher named none.
//
// "" is the honest answer to "which per-workspace directory backs this home", not a
// degraded one: the container's answer is a set of bind mounts rather than a path this
// process can name, and an install capture's throwaway staging home genuinely has no
// workspace tier. A generator that needs a per-workspace FILE asks this and falls back to
// the spelling its own backend already uses (lspSentinelExpr is the worked example).
func (e *Env) DarwinSidecar() string { return e.Getenv(DarwinHomeSidecarEnv) }

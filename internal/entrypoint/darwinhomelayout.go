package entrypoint

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
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
// because it is what the ordering constraint is actually about. The shared path is built
// ABSOLUTELY — filepath.Join(e.Home, h.SharedDir) in linkIntoSharedDir (packhooks.go),
// joined with the payload's own leaf by sharedFileNode.sharedPath (sharedlink.go) — and
// every write in linkThroughShared targets that path, never the relative one. (Attributed
// to `linkSharedCredential` until 2026-09-21; that is now a one-line delegation, and the
// hook family has two members.) The hook therefore lands its bytes correctly with no
// mirror at all. What
// dangles is the LINK IT LEAVES BEHIND, and the loss happens later, when the AGENT reads
// or writes through it. So the constraint is "the mirror exists before anything resolves
// through the link", i.e. before the agent — not "before RunPackHooks". It is applied with
// the Links anyway, which satisfies both readings and is why the overstatement was
// invisible.
//
// NO MIGRATION (OQ-HT2). A real directory where a link belongs is not migrated, copied or
// renamed — the launch refuses and names the path. `sudo rm -rf /Users/_yolojail` before
// the first launch IS the migration. The one narrower remedy is a home-root host_files file
// (HostFileRedirects), which an older launch rendered into the shared home: the refusal
// names `sudo rm` of that one file.

// DarwinHomeSidecarEnv names the workspace sidecar (<workspace>/.yolo/home) for the native
// bootstrap. ABSENCE MEANS "LAY NO LAYOUT", which is not a degraded mode: an install
// capture bootstraps a throwaway staging home whose whole contract is that everything an
// installer writes lands under it (capture walks paths.InstalledProgramSurfaces() to
// compute its delta — inner.go:268, which is HomeSurfaces plus the codex standalone
// payload — and WalkDir does not follow symlinks), so a capture must keep the flat home. The
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
	// Sidecar is the workspace sidecar the Links point into (<workspace>/.yolo/home), or ""
	// for a layout derived with none. Carried because the sidecar is in the WORKSPACE, which
	// the agent can always write, so nothing may be laid through a symbolic link in it
	// (linkedSidecarPaths).
	Sidecar string
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
	// per-workspace directory (on podman, buildHomeSkeleton in internal/cli/run writes the
	// same three into each jail's home skeleton). Their targets are relative, spelled as the container spells them, and
	// they resolve through the Links above — so they are created after them.
	FileRedirects []DarwinHomeLink
	// HostFileRedirects are the user's HOME-ROOT `host_files` destinations (`~/.npmrc`), one
	// link each, with the relative target podman's skeleton lays for the same entry
	// (config.HostFileEntry.SymlinkTarget, `.config/yolo-home/<slug>`): it resolves through
	// the ~/.config link above, so each workspace's file is in its own sidecar. Added by
	// WithHostFileRedirects, never by the deriver, because the list is CONFIG and not core.
	//
	// Kept apart from FileRedirects for the refusal's sake, not the link's: a real file at
	// one of these paths is what an earlier launch rendered into the shared account home, so
	// its remedy is removing that one file, where an occupied core path's is the account
	// reset (occupiedLayoutError). And the directory the target names is NOT created here,
	// as a core redirect's is: the host_files step creates it through the path it checked
	// (DarwinHomeLayout.homeFileThroughLayout), so a link planted at
	// <sidecar>/config/yolo-home is refused there rather than followed by this unconfined
	// process.
	//
	// None is laid at a file the bootstrap writes itself (DarwinLoginRCFiles), where podman's
	// skeleton does lay one: that is the one home-root entry the two backends link differently.
	HostFileRedirects []DarwinHomeLink
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
	l := DarwinHomeLayout{Home: home, Sidecar: sidecar}
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
//
// ⚠ A SYMBOLIC LINK IN THE SIDECAR IS REFUSED FIRST, before anything is created, because
// MkdirAll follows one: the layout would be laid, and the skills and briefings delivered,
// wherever the link points (linkedSidecarPaths says who can put one there and what it breaks).
// That refusal is reported ALONE rather than with the occupied paths below: finding those
// means laying links, and laying them through a planted link is the harm.
func (l DarwinHomeLayout) Apply() error {
	if linked := l.linkedSidecarPaths(); len(linked) > 0 {
		return &LinkedSidecarError{Links: linked}
	}
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
	// Left DANGLING, as podman's skeleton leaves the same link: `once` seeds only a file it
	// cannot stat, and the host_files step makes the directory when it writes the file.
	var inHostFiles []string
	for _, r := range l.HostFileRedirects {
		occupied, err := ensureLayoutSymlink(r.Path, r.Target)
		if err != nil {
			return err
		}
		if occupied {
			inHostFiles = append(inHostFiles, r.Path)
		}
	}
	if len(inHome)+len(inSidecar)+len(inHostFiles) == 0 {
		return nil
	}
	return occupiedLayoutError(l.Home, inHome, inSidecar, inHostFiles...)
}

// occupiedLayoutError is the refusal, split out so the two-remedy rule above is one
// function a test can read rather than a format string inside a loop.
//
// The paths are listed one per line and INDENTED. A comma-joined run of absolute paths is
// the form a reader cannot copy out of a CI log, and this refusal's whole job is to be
// acted on by somebody who has only the log.
//
// hostFiles is a THIRD group with a third remedy: account-home paths where a home-root
// `host_files` link belongs (HostFileRedirects). Each is a file an earlier launch rendered
// into the shared account home, before this backend kept those files per workspace, so the
// remedy removes that one path and the rest of the account is left alone. Variadic so the
// two-group callers read as they did.
func occupiedLayoutError(home string, inHome []string, inSidecar []DarwinHomeLink, hostFiles ...string) error {
	var b strings.Builder
	n := len(inHome) + len(inSidecar) + len(hostFiles)
	fmt.Fprintf(&b, "the per-workspace home layout cannot be laid: %d %s real, where the "+
		"workspace tier's symlink belongs.\n", n, plural(n, "path is", "paths are"))
	b.WriteString("There is no migration (macos-user-home-tiers.md OQ-HT2) — nothing here " +
		"is copied, renamed or deleted for you.\n")
	if len(inHome) > 0 {
		b.WriteString("\nIn the SANDBOX ACCOUNT HOME, which predates this layout:\n")
		for _, p := range inHome {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		fmt.Fprintf(&b, "Move what you want to keep, then reset the account:\n  sudo rm -rf %s\n",
			shquote.Quote(home))
	}
	if len(inSidecar) > 0 {
		b.WriteString("\nIn the WORKSPACE SIDECAR — and `sudo rm -rf " + shquote.Quote(home) +
			"` does NOT touch these, so resetting the account would lose your credentials " +
			"and leave the launch refusing:\n")
		for _, ln := range inSidecar {
			fmt.Fprintf(&b, "  %s (mirrors %s)\n", ln.Path, ln.Target)
		}
		b.WriteString("Each is a real directory where a mirror of a machine-scope directory " +
			"belongs — a copy stranded by a container-era launch, whose live original is the " +
			"path in parentheses. Remove the WORKSPACE copy:\n")
		for _, ln := range inSidecar {
			fmt.Fprintf(&b, "  sudo rm -rf %s\n", shquote.Quote(ln.Path))
		}
	}
	if len(hostFiles) > 0 {
		b.WriteString("\nIn the SANDBOX ACCOUNT HOME, where a `host_files` entry's link belongs. " +
			"Most likely each is the copy an older yolo rendered there, when every workspace on " +
			"this Mac shared one; each workspace now keeps its own, rendered from your " +
			"host_files entry, so the shared copy is no longer used:\n")
		for _, p := range hostFiles {
			fmt.Fprintf(&b, "  %s\n", p)
		}
		b.WriteString("Move what you want to keep, then remove each one (nothing else in the " +
			"account is touched):\n")
		for _, p := range hostFiles {
			rm := "sudo rm"
			if fi, err := os.Lstat(p); err == nil && fi.IsDir() {
				rm = "sudo rm -rf"
			}
			fmt.Fprintf(&b, "  %s %s\n", rm, shquote.Quote(p))
		}
	}
	return fmt.Errorf("%s", strings.TrimRight(b.String(), "\n"))
}

// linkedSidecarPaths returns every path from <workspace>/.yolo down to each Link's target that
// is a symbolic link, stopping each chain at the first one (what lies below it is the link's
// target, not the layout).
//
// WHY A LINK HERE IS NOT THE LAYOUT'S TO REPLACE. The layout creates these as real directories
// and never writes a link among them. The sidecar is inside the workspace, which the agent can
// always write, so a link there was left by an earlier session whose sandbox profile did not
// cover it — a `packs: []` launch, or one with another pack selection — or by the user. Laid
// through, it sends the agent's state, and the skills and briefings the overlay delivers, to a
// path of its author's choosing, and the session's content rules
// (macosuser.ResolveHomeReadonly) name the sidecar spelling the kernel then never reports. It
// is not replaced either, because the directory it points at may hold the agent's real
// history: the refusal names it and removes nothing, the rule OQ-HT2 gives a real directory in
// the account home.
//
// `<workspace>/.yolo` is checked as the sidecar's parent: the sidecar is always
// paths.WorkspaceHomeState, and `.yolo` is as writable as the rest of the workspace. The
// workspace itself is not: the launcher resolved it, and its parent is outside the writable set.
func (l DarwinHomeLayout) linkedSidecarPaths() []string {
	if l.Sidecar == "" {
		return nil
	}
	root := filepath.Dir(l.Sidecar)
	chains := [][]string{{root, l.Sidecar}}
	for _, ln := range l.Links {
		rel, err := filepath.Rel(l.Sidecar, ln.Target)
		if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		chain := []string{root, l.Sidecar}
		cur := l.Sidecar
		for _, part := range strings.Split(filepath.ToSlash(rel), "/") {
			cur = filepath.Join(cur, part)
			chain = append(chain, cur)
		}
		chains = append(chains, chain)
	}
	seen := map[string]bool{}
	var out []string
	for _, chain := range chains {
		for _, p := range chain {
			fi, err := os.Lstat(p)
			if err != nil {
				break // absent: nothing below it exists yet, so nothing below it is a link
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
			break
		}
	}
	return out
}

// homeFileThroughLayout returns the physical path a write to ~/<rel> lands at when it follows
// only the links this layout lays, or an error naming the first link on the way that is not one
// of them.
//
// WHY A WRITE BY PATH NEEDS IT. The bootstrap runs outside Seatbelt as the sandbox account,
// which can write every workspace under the shared root. The overlay install writes through
// handles it opens beneath the home or the sidecar (openOverlayInstallRoots), but two writers
// take a path, and follow every link they meet, the last component included: another program —
// `git config --global`, whose lockfile resolves a symlinked config file before it locks it —
// and the host_files step, which renders each entry with the composition engine's own path
// writes (hostFileDestination). The sidecar is in the workspace, which the agent can write, so
// a link the agent planted anywhere below a layout link's target would aim that write at a
// directory the agent itself cannot reach, such as another workspace's .git. So the path is
// walked here first, one component at a time, and the writer is handed the PHYSICAL path the
// walk arrived at, with no link left in it to follow.
//
// THE RULE IS overlayLinks.route's, applied to a file rather than to a delivered tree:
//
//   - A LAYOUT LINK (a directory Link, a FileRedirect or a HostFileRedirect) is the one way
//     through, and only while it is the link this launch laid, to the target it laid. A layout
//     path that is not yet that link — absent, a real file or directory the layout step refused
//     to replace (OQ-HT2), or a stale link to another workspace — is refused rather than
//     written: a real file written where the layout's link belongs is one the next launch then
//     refuses forever.
//   - ANY OTHER LINK is refused, in the account home and in the sidecar alike, the file itself
//     included. The layout lays none there, so one is somebody else's.
//   - An absent component below everything the layout lays ends the walk: nothing below it
//     exists, so nothing below it is a link, and the writer creates what it needs.
//
// ⚠ WHAT A PATH CHECK CANNOT COVER is a component swapped for a link between this walk and the
// writer's own resolution of the path. The sidecar belongs to one workspace, so only a
// session of the SAME workspace running while this bootstrap does can make that swap; nothing
// here closes it, because the writer takes a path and not a handle.
func (l DarwinHomeLayout) homeFileThroughLayout(rel string) (string, error) {
	if linked := l.linkedSidecarPaths(); len(linked) > 0 {
		return "", &LinkedSidecarError{Links: linked}
	}
	laid := map[string]string{}
	for _, ln := range l.Links {
		laid[ln.Path] = ln.Target
	}
	redirects := map[string]string{}
	for _, r := range slices.Concat(l.FileRedirects, l.HostFileRedirects) {
		redirects[r.Path] = r.Target
	}
	parts := strings.Split(filepath.ToSlash(filepath.Clean(rel)), "/")
	cur := l.Home
	for i := 0; i < len(parts); i++ {
		next := filepath.Join(cur, parts[i])
		want, isLink := laid[next]
		redirect, isRedirect := redirects[next]
		fi, err := os.Lstat(next)
		if isLink || isRedirect {
			if isRedirect {
				want = redirect
			}
			got, rerr := os.Readlink(next)
			if err != nil || rerr != nil || got != want {
				return "", fmt.Errorf("~/%s was not written: %s is not this launch's layout link "+
					"to %s (the darwin_home_layout step says why), and a path the layout owns is "+
					"never written as anything else", rel, next, want)
			}
			if isRedirect {
				// A redirect's target is relative to the directory holding it, and each is
				// followed at most once: it is deleted before the walk restarts from there.
				delete(redirects, next)
				parts = append(strings.Split(filepath.ToSlash(redirect), "/"), parts[i+1:]...)
				i = -1
				continue
			}
			cur = want
			continue
		}
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.Join(append([]string{next}, parts[i+1:]...)...), nil
			}
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			target, _ := os.Readlink(next)
			return "", fmt.Errorf("~/%s was not written: %s -> %s, on its way, is a symbolic link "+
				"the layout did not lay, and this write runs outside the sandbox, so following "+
				"it would land where the session's sandbox profile does not protect. Remove the "+
				"link (what it points at is left alone):\n  sudo rm %s",
				rel, next, target, shquote.Quote(next))
		}
		cur = next
	}
	return cur, nil
}

// LinkedSidecarError is the refusal of a symbolic link where the layout lays a directory of its
// own in the workspace (linkedSidecarPaths). Both the layout step and the overlay step return
// it, because every boot step runs after one fails, and the overlay must not deliver through
// the link the layout refused.
type LinkedSidecarError struct {
	// Links are the linked paths, in the order the layout walks them.
	Links []string
}

func (e *LinkedSidecarError) Error() string {
	var b strings.Builder
	n := len(e.Links)
	fmt.Fprintf(&b, "the per-workspace home layout cannot be laid: %d %s in the workspace "+
		"%s a symbolic link, where the layout's own directory belongs.\n", n,
		plural(n, "path", "paths"), plural(n, "is", "are"))
	b.WriteString("Nothing is laid or delivered through a link the layout did not lay: the " +
		"agent's state, and the skills and briefings this launch delivers, would land wherever " +
		"it points, where the session's sandbox profile does not protect them.\n")
	for _, p := range e.Links {
		if target, err := os.Readlink(p); err == nil {
			fmt.Fprintf(&b, "  %s -> %s\n", p, target)
		} else {
			fmt.Fprintf(&b, "  %s\n", p)
		}
	}
	b.WriteString("Nothing here is removed for you. Move what you want to keep out of the " +
		"directory each link points at, then remove the LINK, which leaves that directory " +
		"alone; yolo recreates the directory on the next launch:\n")
	for _, p := range e.Links {
		fmt.Fprintf(&b, "  sudo rm %s\n", shquote.Quote(p))
	}
	return strings.TrimRight(b.String(), "\n")
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
	l, ok := darwinHomeLayoutFor(e, packs)
	if !ok {
		return nil
	}
	return l.Apply()
}

// darwinHomeLayoutFor derives this Env's layout from the staged packs, and reports false when
// the launcher named no sidecar. The ONE derivation both boot steps use — the layout step lays
// it, and the overlay step follows only the links it names (InstallHomeOverlay) — so the two
// cannot disagree about which links are yolo's.
//
// The user's home-root host_files destinations come from YOLO_HOST_FILES, the wire the
// launcher already hands the bootstrap for the host_files step. An undecodable wire adds
// none: the host_files step reports that wire itself, fatally, and a layout guessing at it
// would lay links nothing then writes.
func darwinHomeLayoutFor(e *Env, packs []*packload.Pack) (DarwinHomeLayout, bool) {
	sidecar := e.Getenv(DarwinHomeSidecarEnv)
	if sidecar == "" {
		return DarwinHomeLayout{Home: e.Home}, false
	}
	l := DeriveDarwinHomeLayout(e.Home, sidecar,
		packload.WritableDirs(packs), packload.SharedDirs(packs))
	entries, _ := config.UnmarshalHostFiles(e.Getenv("YOLO_HOST_FILES"))
	return l.WithHostFileRedirects(entries, packs), true
}

// WithHostFileRedirects returns the layout with one HostFileRedirect for each entry podman's
// home skeleton gives a symlink (config.HostFileStagingSymlink: a home-root FILE outside
// every writable root of this pack selection), to the SAME relative target the skeleton lays
// (config.HostFileEntry.SymlinkTarget). One deciding call and one target for both backends,
// so `~/.npmrc` is per-workspace on both or on neither (paths.HomeFileRedirects states the
// same rule for core's three files). packs are the launch's selected packs, the ones the
// launcher's staging decision read.
//
// THE ONE EXCEPTION is a file this bootstrap itself writes by path on every launch,
// DarwinLoginRCFiles: it stays a real account-home file, as before this layout linked any
// host_files entry (HT-D12).
//
// A layout with no sidecar gains nothing: an install capture's staging home has no workspace
// tier for the link to resolve into (DarwinHomeSidecarEnv).
func (l DarwinHomeLayout) WithHostFileRedirects(entries []config.HostFileEntry, packs []*packload.Pack) DarwinHomeLayout {
	if l.Sidecar == "" {
		return l
	}
	l.HostFileRedirects = nil
	ownWrites := DarwinLoginRCFiles()
	for _, entry := range entries {
		if entry.StagingFor(packs) != config.HostFileStagingSymlink || slices.Contains(ownWrites, entry.Path) {
			continue
		}
		l.HostFileRedirects = append(l.HostFileRedirects, DarwinHomeLink{
			Path:   filepath.Join(l.Home, filepath.FromSlash(entry.Path)),
			Target: filepath.FromSlash(entry.SymlinkTarget()),
		})
	}
	return l
}

// DarwinLoginRCFiles are the home-root files the macos-user bootstrap writes BY PATH on every
// launch, WriteLoginRC's three login rc files, which config validation reserves for no
// backend. WithHostFileRedirects lays no host_files link at one of them, and that is what
// keeps every workspace bootable: a link laid there by the one workspace that declares the
// entry is left by every other launch (P2), and WriteLoginRC's write followed it into the
// sidecar of whichever workspace launched next, where `.config/yolo-home` need not exist —
// a fatal ENOENT in a workspace that declared nothing (macos-user-home-tiers.md HT-D12).
//
// The host_files step refuses an entry naming one of them (hostFileDestination, HT-D13): that
// write would replace it before any shell read it.
//
// ⚠ WriteLoginRC (darwin.go) still spells the three names itself;
// TestDarwinLoginRCFilesAreTheFilesWriteLoginRCWrites runs it and fails when the two differ.
func DarwinLoginRCFiles() []string { return []string{".zprofile", ".zshrc", ".bash_profile"} }

// DarwinSidecar returns this Env's workspace sidecar (<workspace>/.yolo/home), or "" when
// the launcher named none.
//
// "" is the honest answer to "which per-workspace directory backs this home", not a
// degraded one: the container's answer is a set of bind mounts rather than a path this
// process can name, and an install capture's throwaway staging home genuinely has no
// workspace tier. A generator that needs a per-workspace FILE asks this and falls back to
// the spelling its own backend already uses (the bootstrap script's own path is the worked
// example, DarwinBootstrapScriptPath).
func (e *Env) DarwinSidecar() string { return e.Getenv(DarwinHomeSidecarEnv) }

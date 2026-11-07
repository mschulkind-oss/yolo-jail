package entrypoint

// sessionhome.go is a session pass's SESSION HOMES (docs/design/per-session-profiles.md §4.3
// steps 4-5; "session home" is a term coined there: a per-session copy of the one directory a
// pack's program reads its config from, holding that session's render of the pack's surfaces in
// it and links to everything else). It closes the deadlock §2.3 traces: every entry used to
// re-render the shared ~/.pi/agent files a running session's subagents read, so a later attach on
// another profile changed what an earlier session's children ran on.
//
// The boot step prepare_session_homes (bootsteps.go) runs on a session's pass only, before
// configure_pack_surfaces. For every selected pack's `session_homes` declaration
// (packdecl.SessionHome) it builds the home with internal/sessionhome, points the render target
// at it (render.Session: the surface paths in the declared directory, and their baseline
// sidecars), and exports the declared variable into the session's environment, which execBash's
// shell and every process after it inherit (SP-D4). Core reads the declaration; it knows no agent.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/codec"
	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/nativehome"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/sessionhome"
)

// sessionRenderLockName is the in-jail render lock beside the shared sidecars: a session pass
// holds it across its renders into its session homes, so two attaches render one at a time
// (the plan's task 2; task 4 extends it over capture).
const sessionRenderLockName = ".session-render.lock"

// sessionSidecarsDir is the directory under the shared sidecar store holding each session's own
// baseline sidecars, one directory per session id (SP-D14).
const sessionSidecarsDir = "sessions"

// SessionHomesEnv carries a session's SESSION-HOME BINDINGS (a term coined here and in
// docs/design/per-session-profiles.md SP-D36): one line per program of every pack that got a
// session home, `<program bin>\t<declared variable>\t<session home>`. The session pass exports
// it beside each declared variable; every agent launcher (agentEnvShellFn) exports the variables
// its own program's lines name before it execs the program.
//
// Why both: two packs may declare ONE variable (pi and oh-omp both read stock pi's
// PI_CODING_AGENT_DIR), and a session-wide variable holds one value, so without the binding the
// last declaration's home is every program's, and one agent reads, and writes its state into,
// another agent's config directory. The session-wide export stays (SP-D4): it is what a process
// that runs no launcher inherits, and with one declaration per variable it is already right.
const SessionHomesEnv = "YOLO_SESSION_HOMES"

// sessionHomeBindings is SessionHomesEnv's value for plans: each plan's programs in declaration
// order, a pack with no program contributing nothing. A field holding a tab or a newline would
// break the line format and is skipped; none can (a bin and a variable are validated names, a
// home path is built from them under the jail's home).
func sessionHomeBindings(plans []sessionHomePlan) string {
	var lines []string
	for _, pl := range plans {
		for _, bin := range pl.bins {
			if strings.ContainsAny(bin+pl.env+pl.spec.Home, "\t\n") {
				continue
			}
			lines = append(lines, bin+"\t"+pl.env+"\t"+pl.spec.Home)
		}
	}
	return strings.Join(lines, "\n")
}

// programBins is p's program binaries, in declaration order.
func programBins(p *packload.Pack) []string {
	var out []string
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindProgram && c.Bin != "" {
			out = append(out, c.Bin)
		}
	}
	return out
}

// seededBaselineSuffix marks a session baseline seeded from the shared one (seedSessionBaselines)
// that no render of the session has replaced yet. Capture skips such a home: its file, if any, is
// a render that stopped before its baseline was written, and diffing it against the shared
// baseline would read that session's profile values as the user's edits.
const seededBaselineSuffix = ".seeded"

// prepareSessionHomesStep is the boot step: a session's pass with a valid YOLO_SESSION_ID gets a
// session home per declaration, and every other run (the jail's own boot, a raw exec with no id,
// a sealed build, which runs no agent) gets none and renders the shared copies as today.
func prepareSessionHomesStep(b *bootRun) {
	if !b.sessionPass || b.sealed {
		return
	}
	id := os.Getenv(SessionIDEnv)
	if b.e.nativeSessionID != "" {
		id = b.e.nativeSessionID
	}
	if !validSessionID(id) {
		return
	}
	// A load failure is reported once, at configure_pack_surfaces.
	packs, err := b.jailPacks()
	if err != nil {
		return
	}
	genStep(b.e, "prepare_session_homes", func() error {
		if b.e.nativeSessionID != "" {
			if len(declaredSessionHomes(b.e, packs)) == 0 {
				return nil
			}
			if _, err := nativehome.Prepare(b.e.Home, b.e.WorkspaceDir(), id); err != nil {
				var skips []string
				for _, d := range declaredSessionHomes(b.e, packs) {
					skips = append(skips, d.shared)
				}
				b.e.session = &render.Session{Skips: skips}
				return fmt.Errorf("cannot prepare native session config: %w; repair the path and retry yolo", err)
			}
		}
		return prepareSessionHomes(b.e, packs, id)
	})
}

// sessionDecl is one selected pack's session-home declaration resolved at this home: everything
// but the session id. The pass that makes a home and the pass that captures from one read the
// same resolution.
type sessionDecl struct {
	pack string
	env  string
	// bins are the declaring pack's program binaries, whose launchers bind env to this
	// declaration's home (SessionHomesEnv).
	bins []string
	// shared is the declared directory, absolute.
	shared string
	// nativeRoot is workspace-qualified; empty keeps the container layout.
	nativeRoot string
	nativeRel  string
	names      []string
	// prefer is the declaration's prefer map with each target expanded.
	prefer map[string]string
	// rendered are the files, relative to the directory and slash-separated, its surfaces render (sessionhome.Spec.Rendered).
	rendered []string
	// surfaces are the surfaces whose path lies in the directory; rmw the read-modify-write ones
	// among them, seeded from the shared file before a session's render (SP-D11).
	surfaces []manifest.Surface
	rmw      []manifest.Surface
}

// spec is the link builder's input for this declaration's home at home.
func (d sessionDecl) spec(home string) sessionhome.Spec {
	return sessionhome.Spec{Shared: d.shared, Home: home, Names: d.names, Prefer: d.prefer,
		Rendered: d.rendered}
}

func (d sessionDecl) root() string {
	if d.nativeRoot != "" {
		return d.nativeRoot
	}
	return sessionhome.SessionsRoot(d.shared)
}
func (d sessionDecl) home(id string) (string, error) {
	if d.nativeRoot != "" {
		if !nativehome.ValidID(id) {
			return "", fmt.Errorf("invalid native session ID %q", id)
		}
		return filepath.Join(d.nativeRoot, id, d.nativeRel), nil
	}
	return sessionhome.Home(d.shared, id)
}

// sessionHomePlan is one declaration with the home this session makes for it.
type sessionHomePlan struct {
	sessionDecl
	spec sessionhome.Spec
}

// declaredSessionHomes resolves every selected pack's session-home declaration at e's home.
func declaredSessionHomes(e *Env, packs []*packload.Pack) []sessionDecl {
	base := e.sharedRenderTarget() // the declared paths, never this session's redirect of them
	// Every selected pack's declared surfaces, read for their PATHS and modes alone: which of
	// them lie in a declared directory. Decoded here rather than planned, because this decides
	// no render (the one loop, surfaceloop.go, does); a malformed declaration is reported there.
	var surfaces []manifest.Surface
	for _, p := range packs {
		if raw := p.Decl.SurfaceContributions(); len(raw) > 0 {
			s, _ := manifest.DecodeSurfaces(raw)
			surfaces = append(surfaces, s...)
		}
	}
	var out []sessionDecl
	for _, p := range packs {
		for _, decl := range p.Decl.SessionHomes() {
			d := sessionDecl{pack: p.Name, env: decl.Env, names: decl.Shared, bins: programBins(p),
				shared: filepath.Join(e.Home, filepath.FromSlash(strings.TrimSuffix(decl.Dir, "/")))}
			if len(decl.Prefer) > 0 {
				d.prefer = map[string]string{}
				for name, target := range decl.Prefer {
					d.prefer[name] = base.ExpandHome(target)
				}
			}
			if e.nativeSessionID != "" {
				d.nativeRoot = nativehome.Root(e.Home, e.WorkspaceDir())
				d.nativeRel = filepath.FromSlash(strings.TrimSuffix(decl.Dir, "/"))
			}
			seen := map[string]bool{}
			for _, s := range surfaces {
				rel, ok := strings.CutPrefix(base.SurfacePath(s), d.shared+string(filepath.Separator))
				if !ok {
					continue
				}
				d.surfaces = append(d.surfaces, s)
				if top := filepath.ToSlash(rel); !seen[top] {
					seen[top] = true
					d.rendered = append(d.rendered, top)
				}
				if s.ResolvedMode() == manifest.ModeRMW {
					d.rmw = append(d.rmw, s)
				}
			}
			out = append(out, d)
		}
	}
	return out
}

// prepareSessionHomes builds every declared session home for session id, then points e's render
// target at them and exports each declared variable. A home that cannot be made is the step's
// error, and the boot refuses the session on it (SP-D6): falling back to the shared directory
// would silently bring the deadlock back. Nothing is exported or redirected unless every home was
// made.
func prepareSessionHomes(e *Env, packs []*packload.Pack, id string) error {
	base := e.sharedRenderTarget()
	var plans []sessionHomePlan
	// skips are the declared directories this pass renders nothing into: SP-D2 holds whether or
	// not the session got a home, so a preset variable (SP-D5) and a refused session (SP-D6) both
	// leave the shared copies to the jail's own boot.
	var skips []string
	for _, d := range declaredSessionHomes(e, packs) {
		// SP-D5: the user's own value wins, in this session alone.
		if v := e.Vars[d.env]; v != "" || os.Getenv(d.env) != "" {
			e.warn(fmt.Sprintf("Note: pack %s: %s is already set in this session, so the session "+
				"reads the directory you set and gets no config directory of its own; another "+
				"session's profile can change what it reads, so unset the variable for yolo to "+
				"give each session its own", d.pack, d.env))
			skips = append(skips, d.shared)
			continue
		}
		home, err := d.home(id)
		if err != nil {
			return err
		}
		plans = append(plans, sessionHomePlan{sessionDecl: d, spec: d.spec(home)})
	}
	if len(plans) == 0 {
		if len(skips) > 0 {
			e.session = &render.Session{Skips: skips}
		}
		return nil
	}
	// A refused session renders nothing into any declared directory either: the boot refuses
	// after every step has run, and the render below must not reach the shared copies first.
	refuse := func(err error) error {
		for _, pl := range plans {
			skips = append(skips, pl.spec.Shared)
		}
		e.session = &render.Session{Skips: skips}
		return err
	}
	for _, pl := range plans {
		if err := sessionhome.Build(pl.spec); err != nil {
			return refuse(sessionHomeRefusal(pl, err))
		}
		if err := seedRMWSurfaces(base, pl); err != nil {
			return refuse(sessionHomeRefusal(pl, err))
		}
	}
	sess := &render.Session{Surfaces: map[manifest.SurfaceKey]bool{}, Skips: skips}
	if dir := base.SidecarDir(); dir != "" {
		sess.SidecarDir = filepath.Join(dir, sessionSidecarsDir, id)
		if err := os.MkdirAll(sess.SidecarDir, base.SidecarDirMode()); err != nil {
			return refuse(fmt.Errorf("cannot make this session's sidecar directory %s: %w\n\n"+
				"Without it the session's render would overwrite the shared baseline.\n\n"+
				"Check the workspace's .yolo directory, then start the session again:\n  ls -ld %s",
				sess.SidecarDir, err, filepath.Dir(sess.SidecarDir)))
		}
	}
	var surfaces []manifest.Surface
	for _, pl := range plans {
		sess.Redirects = append(sess.Redirects, render.Redirect{From: pl.spec.Shared, To: pl.spec.Home})
		for _, s := range pl.surfaces {
			sess.Surfaces[s.Key()] = true
		}
		surfaces = append(surfaces, pl.surfaces...)
	}
	// Under the render lock: a pass with no session id may be rewriting the shared baseline in
	// place, and a torn copy would make this session's first render a first migration.
	var release func()
	var err error
	if e.nativeSessionID != "" {
		release, err = takeSessionRenderLock(e)
		if err != nil {
			return refuse(err)
		}
	} else {
		release = holdSessionRenderLock(e)
	}
	err = seedSessionBaselines(base, sess, surfaces, e.nativeSessionID != "")
	release()
	if err != nil {
		return refuse(err)
	}
	e.session = sess
	for _, pl := range plans {
		setEnvBoth(e, pl.env, pl.spec.Home)
	}
	// SP-D36: each launcher rebinds its own program's variables, since two declarations may
	// name one variable and the session-wide value above is then only the last one's.
	if b := sessionHomeBindings(plans); b != "" {
		setEnvBoth(e, SessionHomesEnv, b)
	}
	return nil
}

// seedSessionBaselines gives each redirected `stateful` surface a baseline in the session's own
// sidecar dir, copied from the shared one, before the session's first render of it. Without one
// that render is a FIRST MIGRATION (agentcfg.ComposeStateful): it starts from an empty overlay
// instead of the shared one, so the session's file would lack every captured edit, and the
// persisted overlay, which is shared (SP-D14), would be written back empty, discarding them for
// every later render. With one, and no file yet in the home, the render is steady state with
// nothing to capture: the shared overlay folds in whole. A session baseline already there is kept.
func seedSessionBaselines(base render.Target, sess *render.Session, surfaces []manifest.Surface, native bool) error {
	if sess.SidecarDir == "" {
		return nil
	}
	at := base.WithSession(sess)
	for _, s := range surfaces {
		if !sess.Surfaces[s.Key()] || s.ResolvedMode() != manifest.ModeStateful {
			continue
		}
		to := at.LastRenderPath(s.Agent, s.Name)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		data, err := os.ReadFile(base.LastRenderPath(s.Agent, s.Name))
		if err != nil {
			if os.IsNotExist(err) && native {
				c, ok := codec.LookupCodec(s.Codec)
				if !ok {
					continue
				}
				data, err = c.Encode(s.Kind().ZeroValue())
				if err != nil {
					return err
				}
			} else {
				continue
			}
		}
		if err := writeSidecar(at, to+seededBaselineSuffix, ""); err != nil {
			return fmt.Errorf("cannot mark this session's seeded baseline %s: %w", to, err)
		}
		if err := writeSidecar(at, to, string(data)); err != nil {
			return fmt.Errorf("cannot seed this session's baseline %s: %w\n\n"+
				"Without it the session's render would discard the shared captured edits.\n\n"+
				"Check the workspace's .yolo directory, then start the session again:\n  ls -ld %s",
				to, err, filepath.Dir(to))
		}
	}
	return nil
}

// seedRMWSurfaces copies the shared file of each read-modify-write surface into the session home
// before its render, since an rmw render reads the existing file as its base (SP-D11). A
// surface already in the home keeps its bytes.
func seedRMWSurfaces(base render.Target, pl sessionHomePlan) error {
	for _, s := range pl.rmw {
		from := base.SurfacePath(s)
		rel, _ := filepath.Rel(pl.spec.Shared, from)
		to := filepath.Join(pl.spec.Home, rel)
		if _, err := os.Lstat(to); err == nil {
			continue
		}
		data, err := os.ReadFile(from)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return fmt.Errorf("reading %s to seed the session's copy: %w", from, err)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(to, data, 0o644); err != nil {
			return fmt.Errorf("seeding %s: %w", to, err)
		}
	}
	return nil
}

// sessionHomeRefusal is SP-D6's message: headline, the facts, then what to run.
func sessionHomeRefusal(pl sessionHomePlan, err error) error {
	root := sessionhome.SessionsRoot(pl.spec.Shared)
	return fmt.Errorf("cannot make this session's own config directory %s for pack %s: %v\n\n"+
		"Without it the session would read the shared %s, which another session's profile "+
		"rewrites, so the session does not start.\n\n"+
		"Free space or clear what is in the way under %s, then start the session again:\n"+
		"  ls -la %s\n  df -h %s",
		pl.spec.Home, pl.pack, err, pl.spec.Shared, root, root, filepath.Dir(root))
}

// holdSessionRenderLock takes the in-jail render lock, which a container pass holds across its
// capture from session homes and its render whenever a selected pack declares a session home
// (beginSessionCapture), and returns its release. A lock that cannot be taken is a warning, never
// a refusal: it orders two passes, and each still renders only its own session's files.
func holdSessionRenderLock(e *Env) func() {
	release, err := takeSessionRenderLock(e)
	if err != nil {
		e.warn("warning: " + err.Error() + "; rendering this container session's files without it")
	}
	return release
}

// Native capture cannot proceed unordered: its shared overlay outlives this launch.
func takeSessionRenderLock(e *Env) (func(), error) {
	noop := func() {}
	dir := e.sharedRenderTarget().SidecarDir()
	if dir == "" {
		return noop, nil
	}
	err := os.MkdirAll(dir, e.sharedRenderTarget().SidecarDirMode())
	var f *os.File
	if err == nil {
		f, err = os.OpenFile(filepath.Join(dir, sessionRenderLockName), os.O_CREATE|os.O_RDWR, 0644)
	}
	if err == nil {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX)
	}
	if err != nil {
		if f != nil {
			_ = f.Close()
		}
		return noop, fmt.Errorf("cannot take the session render lock in %s: %w; repair the sidecar path and retry yolo", dir, err)
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }, nil
}

// SharedDirsAt is the `scope: machine` dirs (packload.SharedDirs) of the packs staged at
// packRoot, read through the same strict host-side reader as HasSessionHomesAt. The macos-user
// plan builder asks it for a run-as-you session's profile, which opens each selected one in the
// shared store and no other (MMP-D6). An empty packRoot is a launch that staged no packs: none.
func SharedDirsAt(packRoot string) ([]string, error) {
	if packRoot == "" {
		return nil, nil
	}
	packs, err := loadPackRoot(&Env{}, packRoot)
	if err != nil {
		return nil, err
	}
	return packload.SharedDirs(packs), nil
}

// HasSessionHomesAt reads the selected staged tree through the same strict host-side
// reader as DeclaredNodeFloorsAt. A nondeclaring launch needs no config-home wrapper.
func HasSessionHomesAt(packRoot string) (bool, error) {
	if packRoot == "" {
		return false, nil
	}
	packs, err := loadPackRoot(&Env{}, packRoot)
	if err != nil {
		return false, err
	}
	for _, p := range packs {
		if len(p.Decl.SessionHomes()) > 0 {
			return true, nil
		}
	}
	return false, nil
}

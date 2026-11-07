package cli

// hostsessionhome.go is `yolo host`'s per-launch SESSION HOME (docs/design/per-session-profiles.md
// §4.7, ruled by OQ-SP4; "session home" is a term coined there, §4.1: a per-session copy of the one
// directory a pack's program reads its config from, holding that session's render of the pack's
// surfaces in it and a link to everything else). It is the jail's prepare_session_homes step
// (internal/entrypoint/sessionhome.go) at the host, so a host launch's children follow the launch's
// -p as a jail session's do: two `yolo host -p` launches of pi on different profiles no longer
// share one ~/.pi/agent/settings.json, and neither rewrites it.
//
// For every selected pack's `session_homes` declaration (packdecl.SessionHome) a launch:
//
//   - reaps the session homes of launches that have ended (prune.ReapHostSessionHomes);
//   - makes a throwaway directory under the state dir, <state>/host-session-homes/<pack>-<random>/,
//     holding a `.lease` it locks shared and a scratch HOME (coined here: a directory standing in
//     for $HOME for this render alone) whose declared directory is the session home;
//   - links the session home into the user's real directory by internal/sessionhome's rules, the
//     jail's own link builder;
//   - renders the selected packs' surfaces in the declared directory into the scratch home, with
//     this launch's selection (the -p, else the configured profile), so every byte of the render's
//     bookkeeping (baselines, provenance, the adoption archive) lands in the scratch home and none
//     in the user's real home or real capture store;
//   - exports the declared variable into this launch's child environment only.
//
// The launch removes its directory when its program exits where it stays resident; on the exec
// path the lease crosses the exec, the program holds it for its life, and the next launch or
// `yolo prune` reaps the directory once the kernel has dropped it. Core reads the declaration
// alone: nothing here names an agent.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/packoverlay"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/sessionhome"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// hostScratchHomeName is the scratch home's name inside a launch's directory (SP-D22).
const hostScratchHomeName = "home"

// hostSessionManifestName is the LAUNCH MANIFEST's file name, beside the scratch home and outside
// the declared directory: the launch's own record of what it rendered and where the real config
// directory is (design §7, SHR-D3). The reaper classifies a retained home against THIS, never
// against today's pack selection, so a pack edited after the session cannot make that session's
// bytes look unaccounted for. A home with no manifest (built before this existed) falls back to
// the baselines plus the current selection.
const hostSessionManifestName = "session-home.json"

// hostSessionManifest is one launch's record. One file per launch directory, one entry per
// session-home declaration it built.
type hostSessionManifest struct {
	Version int                       `json:"version"`
	Homes   []hostSessionManifestHome `json:"homes"`
}

type hostSessionManifestHome struct {
	Pack string `json:"pack"`
	Env  string `json:"env"`
	// Rel is the declared directory relative to the scratch home; Shared is the real directory
	// it stands in for.
	Rel    string `json:"rel"`
	Shared string `json:"shared"`
	// Rendered are the paths the launch wrote as real files, relative to the declared directory
	// and slash-separated. Everything else in there is a link, a lock, or a stray.
	Rendered []string `json:"rendered"`
}

// hostSessionHomes is one launch's session homes: the directories it made, the leases that keep
// them from being reaped while its program runs, and the variables it exports.
type hostSessionHomes struct {
	dirs   []string
	leases []*os.File
	// vars are the KEY=VALUE entries the launch adds to its child's environment.
	vars []string
	// captures names the stateful surfaces and render homes whose edits must be folded into
	// the host capture overlay before a resident home is removed.
	captures []hostSessionCapture
	// kept is set once the leases are handed across the exec: the program that replaces this
	// process owns the directories from then on, and Close leaves them for the reaper.
	kept bool
	// preserved keeps a resident home for a later capture attempt after this one refused.
	preserved bool
}

// hostSessionCapture is one render home and the surfaces its session-home declaration holds.
type hostSessionCapture struct {
	home     string
	rel      string // the declared directory relative to the scratch home
	surfaces []manifest.Surface
}

// Close removes the launch's directories and releases their leases, unless KeepAcrossExec handed
// them to the program or capture preserved them for a retry. Safe on nil and more than once.
func (h *hostSessionHomes) Close() {
	if h == nil || h.kept || h.preserved {
		return
	}
	h.remove()
}

// preserveForRetry releases resident leases but leaves their files for the next host launch or
// `yolo prune --apply` to capture. A normal close after this only releases descriptors.
func (h *hostSessionHomes) preserveForRetry() {
	if h == nil || h.preserved {
		return
	}
	h.preserved = true
	for _, f := range h.leases {
		_ = f.Close()
	}
	h.leases = nil
}

// remove is Close without the kept check: an exec that failed after KeepAcrossExec gives the
// directories back too.
func (h *hostSessionHomes) remove() {
	if h == nil {
		return
	}
	for _, d := range h.dirs {
		_ = os.RemoveAll(d) // a session home holds links into the real directory; RemoveAll never follows one
	}
	for _, f := range h.leases {
		_ = f.Close()
	}
	h.dirs, h.leases = nil, nil
}

// KeepAcrossExec clears close-on-exec on every lease, so the program this process execs inherits
// the shared locks and holds them until it exits (the model menu's lock does the same:
// modelmenu.Held.KeepAcrossExec). A lease that cannot be kept is a line, never a refusal: the
// directory is then reaped by the next launch while the program may still read it, which loses
// nothing of the user's (every shared entry is a link) but may change what the program reads.
func (h *hostSessionHomes) KeepAcrossExec(errw io.Writer) {
	if h == nil {
		return
	}
	h.kept = true
	for _, f := range h.leases {
		if _, err := unix.FcntlInt(f.Fd(), unix.F_SETFD, 0); err != nil {
			fmt.Fprintf(errw, "yolo host: could not keep the lease on %s across the exec (%v), so a later "+
				"launch may remove this launch's config directory while it runs.\n", filepath.Dir(f.Name()), err)
		}
	}
}

// environ is env with each exported variable set, replacing an empty entry of the same name.
func (h *hostSessionHomes) environ(env []string) []string {
	if h == nil || len(h.vars) == 0 {
		return env
	}
	set := map[string]bool{}
	for _, kv := range h.vars {
		k, _, _ := strings.Cut(kv, "=")
		set[k] = true
	}
	out := make([]string, 0, len(env)+len(h.vars))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !set[k] {
			out = append(out, kv)
		}
	}
	return append(out, h.vars...)
}

// hostSessionDecl is one selected pack's declaration resolved at the real home.
type hostSessionDecl struct {
	pack, env string
	// rel is the declared directory relative to the home; shared its absolute path there.
	rel, shared string
	names       []string
	prefer      map[string]string
	// surfaces are every selected pack's surfaces whose path lies in the directory, by owner;
	// rendered the files they land on, relative to the directory and slash-separated.
	surfaces map[*packload.Pack][]manifest.Surface
	rendered []string
	// bins are the declaring pack's program binaries (oneHostHomePerVariable).
	bins []string
}

// oneHostHomePerVariable keeps one declaration per variable (SP-D36): a host launch execs ONE
// program with one environment, so of two packs declaring one variable (pi and oh-omp both read
// PI_CODING_AGENT_DIR) only one home can be the program's, and the other would be a directory
// nothing reads. The launched program's own pack wins; when neither declares it, the first in
// pack order does. Each dropped declaration gets one line, since a launch has no quiet mode.
func oneHostHomePerVariable(decls []hostSessionDecl, agent string, errw io.Writer) []hostSessionDecl {
	owns := func(d hostSessionDecl) bool {
		for _, b := range d.bins {
			if b == agent {
				return true
			}
		}
		return false
	}
	keep := map[string]int{} // variable -> index in decls of the one kept
	for i, d := range decls {
		j, seen := keep[d.env]
		if !seen || (owns(d) && !owns(decls[j])) {
			keep[d.env] = i
		}
	}
	var out []hostSessionDecl
	for i, d := range decls {
		if keep[d.env] == i {
			out = append(out, d)
			continue
		}
		fmt.Fprintf(errw, "yolo host: pack %s: %s is pack %s's for this launch of %s, so pack %s gets "+
			"no config directory of its own in it; launch %s's own program for that.\n",
			d.pack, d.env, decls[keep[d.env]].pack, agent, d.pack, d.pack)
	}
	return out
}

// hostSessionDecls resolves every selected pack's session-home declaration at home, with the
// surfaces each directory holds, read from every selected pack (another pack may declare a
// surface in it, as in a jail: declaredSessionHomes).
func hostSessionDecls(packs []*packload.Pack, home string) []hostSessionDecl {
	at := render.Host(home, nil, render.OwnershipOwn)
	type owned struct {
		p *packload.Pack
		s manifest.Surface
	}
	var surfaces []owned
	for _, p := range packs {
		if raw := p.Decl.SurfaceContributions(); len(raw) > 0 {
			decoded, _ := manifest.DecodeSurfaces(raw)
			for _, s := range decoded {
				surfaces = append(surfaces, owned{p, s})
			}
		}
	}
	var out []hostSessionDecl
	for _, p := range packs {
		for _, decl := range p.Decl.SessionHomes() {
			rel := filepath.FromSlash(strings.TrimSuffix(decl.Dir, "/"))
			d := hostSessionDecl{pack: p.Name, env: decl.Env, rel: rel, names: decl.Shared,
				shared: filepath.Join(home, rel), surfaces: map[*packload.Pack][]manifest.Surface{}}
			for _, c := range p.Decl.Contributions() {
				if c.Kind == packdecl.KindProgram && c.Bin != "" {
					d.bins = append(d.bins, c.Bin)
				}
			}
			if len(decl.Prefer) > 0 {
				d.prefer = map[string]string{}
				for name, target := range decl.Prefer {
					d.prefer[name] = at.ExpandHome(target)
				}
			}
			seen := map[string]bool{}
			for _, o := range surfaces {
				rest, ok := strings.CutPrefix(at.SurfacePath(o.s), d.shared+string(filepath.Separator))
				if !ok {
					continue
				}
				d.surfaces[o.p] = append(d.surfaces[o.p], o.s)
				if top := filepath.ToSlash(rest); !seen[top] {
					seen[top] = true
					d.rendered = append(d.rendered, top)
				}
			}
			out = append(out, d)
		}
	}
	return out
}

// hostSessionInputs is the derive inputs a session render runs on: what `yolo host apply` composes
// from the user scope (every agent's configured selection, the MCP and LSP tables), with this
// launch's own selection for its agent in place of the configured one when a -p was typed (SP-D23).
func hostSessionInputs(launch *hostComposition, home string) (*entrypoint.HostInputs, error) {
	comp, err := composeHostInputs(launch.cfg, launch.packs, home)
	if err != nil {
		return nil, err
	}
	in := comp.inputs
	if launch.typedProfile == "" {
		return in, nil
	}
	own := launch.wireTables()
	vars := map[string]string{}
	for k, v := range in.Vars {
		vars[k] = v
	}
	vars[entrypoint.ProvidersWireEnv] = own[entrypoint.ProvidersWireEnv]
	vars[entrypoint.ProfilesWireEnv] = own[entrypoint.ProfilesWireEnv]
	use := decodeWireTable(vars[entrypoint.UseProfilesWireEnv])
	if v, ok := decodeWireTable(own[entrypoint.UseProfilesWireEnv]).Get(launch.agent); ok {
		use.Set(launch.agent, v)
	} else {
		use.Delete(launch.agent)
	}
	vars[entrypoint.UseProfilesWireEnv] = wireJSON(use)
	return &entrypoint.HostInputs{Vars: vars, Packs: in.Packs, AgentLookup: in.AgentLookup}, nil
}

// decodeWireTable is a wire table's object, empty when it holds none.
func decodeWireTable(text string) *jsonx.OrderedMap {
	if v, err := jsonx.Decode([]byte(text)); err == nil {
		if m, ok := v.(*jsonx.OrderedMap); ok {
			return m
		}
	}
	return jsonx.NewOrderedMap()
}

// hostSessionHomesRoot is where every launch's session homes live: the state dir, never ~/.cache,
// which every jail mounts read-write (prune.HostSessionHomesLeaf says why that matters).
func hostSessionHomesRoot() string {
	return filepath.Join(paths.GlobalStorage(), prune.HostSessionHomesLeaf)
}

// prepareHostSessionHomes is the launch's step: a session home per selected pack's declaration,
// rendered for this launch's selection, its variable added to environ. It returns the homes (nil
// when there are none, and in a jail, whose own session pass made them: SP-D25) and the child's
// environment. An error is SP-D6's refusal: the launch does not start, since a program falling
// back to the real directory would read another launch's profile.
func prepareHostSessionHomes(launch *hostComposition, environ []string, errw io.Writer) (*hostSessionHomes, []string, error) {
	if config.InJail() {
		return nil, environ, nil
	}
	if config.HostManagementMode() != config.HostManagementOwn {
		return nil, environ, fmt.Errorf("host session homes require `host_management: \"own\"`")
	}
	home := paths.Home()
	root := hostSessionHomesRoot()
	// THE REAP FIRST, on every owned launch, whatever this one declares: a launch whose program
	// died leaves its directory for the next one (SP-D24). Capture precedes removal; a refusal
	// leaves the source for retry and stops this launch from proceeding past the failed capture.
	reaped := prune.ReapHostSessionHomes(root, true, time.Now(), func(dir string) (string, string, error) {
		return captureHostSessionHomePath(dir, errw)
	})
	// A reap problem is janitorial work, never a reason to deny THIS launch (design §5, SHR-D8).
	// The source is kept and named, and the launch proceeds: refusing here let one dead session's
	// 20-byte stray file lock every future launch out, with a next step that could not work.
	for _, entry := range reaped.Removed {
		if entry.Err != nil {
			fmt.Fprintf(errw, "yolo host: could not capture the ended session home %s: %v\n"+
				"  it was kept, and this launch continues: the problem is on that directory, not on this launch. "+
				"`yolo prune --apply` retries it once the cause is gone, or inspect it and remove it by hand.\n",
				entry.Path, entry.Err)
		}
	}
	packs, _ := packload.ResolveDestinations(launch.packs)
	decls := oneHostHomePerVariable(hostSessionDecls(packs, home), launch.agent, errw)
	if len(decls) == 0 {
		return nil, environ, nil
	}
	lookup := environLookup(environ)
	var homes hostSessionHomes
	var inputs *entrypoint.HostInputs
	var overlays *packoverlay.OverlaySet
	fail := func(d hostSessionDecl, at string, err error) (*hostSessionHomes, []string, error) {
		homes.remove()
		return nil, environ, hostSessionRefusal(d, at, root, err)
	}
	for _, d := range decls {
		// SP-D5: the user's own value wins, for this launch, and one line says so.
		if v, ok := lookup(d.env); ok && v != "" {
			fmt.Fprintf(errw, "yolo host: pack %s: %s is already set, so %s reads the directory you set "+
				"(%s) and this launch gets no config directory of its own; another launch's profile can "+
				"then change what it reads. Unset %s for yolo to give each launch its own.\n",
				d.pack, d.env, launch.agent, v, d.env)
			continue
		}
		if inputs == nil {
			in, err := hostSessionInputs(launch, home)
			if err != nil {
				return fail(d, root, fmt.Errorf("this launch's selection cannot be composed: %w", err))
			}
			inputs = in
			listPacks, _ := hostTreeFallbacks(packs)
			overlays = packoverlay.Collect(listPacks, render.ProfileFor(render.KindHost).AgentAutonomy,
				overlayGateProfiles(render.KindHost, packs))
		}
		dir, lease, err := makeHostSessionDir(root, d.pack)
		if err != nil {
			return fail(d, root, err)
		}
		homes.dirs = append(homes.dirs, dir)
		if lease != nil {
			homes.leases = append(homes.leases, lease)
		}
		scratch := filepath.Join(dir, hostScratchHomeName)
		sessionHome := filepath.Join(scratch, d.rel)
		spec := sessionhome.Spec{Shared: d.shared, Home: sessionHome, Names: d.names,
			Prefer: d.prefer, Rendered: d.rendered}
		if err := sessionhome.Build(spec); err != nil {
			return fail(d, sessionHome, err)
		}
		if err := seedHostSession(d, home, scratch); err != nil {
			return fail(d, sessionHome, err)
		}
		for p, surfaces := range d.surfaces {
			only := make([]string, 0, len(surfaces))
			for _, s := range surfaces {
				only = append(only, s.Agent+"/"+s.Name)
			}
			results, err := entrypoint.RenderHostPack(p, scratch, render.OwnershipOwn, false, overlays, inputs, only...)
			if err != nil {
				return fail(d, sessionHome, err)
			}
			for _, r := range results {
				if strings.HasPrefix(r.Action, "refused:") {
					return fail(d, r.Path, errors.New("its render "+r.Action))
				}
			}
		}
		var captureSurfaces []manifest.Surface
		for _, surfaces := range d.surfaces {
			captureSurfaces = append(captureSurfaces, surfaces...)
		}
		sort.Slice(captureSurfaces, func(i, j int) bool {
			if captureSurfaces[i].Agent != captureSurfaces[j].Agent {
				return captureSurfaces[i].Agent < captureSurfaces[j].Agent
			}
			return captureSurfaces[i].Name < captureSurfaces[j].Name
		})
		homes.captures = append(homes.captures, hostSessionCapture{home: scratch, rel: d.rel, surfaces: captureSurfaces})
		if err := writeHostSessionManifest(dir, hostSessionManifestHome{
			Pack: d.pack, Env: d.env, Rel: d.rel, Shared: d.shared, Rendered: d.rendered,
		}); err != nil {
			return fail(d, sessionHome, err)
		}
		homes.vars = append(homes.vars, d.env+"="+sessionHome)
		fmt.Fprintf(errw, "yolo host: yolo SET %s=%s for %s, from pack %s: this launch's own config "+
			"directory, rendered for %s, with everything else linked to %s, which it leaves untouched; "+
			"yolo captures user edits to its stateful config surfaces into the normal host overlay when "+
			"this launch ends, then removes the session directory. Generated profile values keep their "+
			"normal precedence and are regenerated. If this launch is exec'd and later dies, capture "+
			"runs when the ended home is reaped.\n", d.env, sessionHome, launch.agent, d.pack,
			hostSessionSelectionWords(launch), d.shared)
	}
	if len(homes.dirs) == 0 {
		return nil, environ, nil
	}
	return &homes, homes.environ(environ), nil
}

func finishHostSessionHomes(h *hostSessionHomes, errw io.Writer) error {
	if h == nil || h.kept {
		return nil
	}
	if err := settleHostSessionHomes(h, errw); err != nil {
		h.preserveForRetry()
		return err
	}
	h.remove()
	return nil
}

// settleHostSessionHomes applies the reaper's rule to a home this launch still holds, so every
// path follows one rule (design §5, SHR-D5): capture what the render accounts for, then either
// let the caller delete the directory or move it aside whole with one line. The resident path
// used to delete a stray silently — the only path that could actually lose a byte.
func settleHostSessionHomes(h *hostSessionHomes, errw io.Writer) error {
	type kept struct{ dir, reason string }
	var captures []hostSessionCapture
	var keptWhole []kept
	type straySet struct {
		dir, rel, shared string
		strays           []string
	}
	var strays []straySet
	for _, c := range h.captures {
		dir := filepath.Dir(c.home)
		audit := auditHostSessionHome(dir, c.rel, c.surfaces)
		if audit.keep != "" {
			// Capture would read this session's profile values as the user's edits (SP-D18).
			keptWhole = append(keptWhole, kept{dir, audit.keep})
			continue
		}
		captures = append(captures, c)
		if len(audit.strays) > 0 {
			strays = append(strays, straySet{dir, c.rel, audit.shared, audit.strays})
		}
	}
	if err := captureHostSessionDirs(captures, errw); err != nil {
		return err
	}
	for _, set := range strays {
		sort.Strings(set.strays)
		if reason := repatriateHostSessionStrays(set.dir, set.rel, set.shared, set.strays, errw); reason != "" {
			keptWhole = append(keptWhole, kept{set.dir, reason})
		}
	}
	for _, k := range keptWhole {
		if _, _, err := setAsideHostSessionHome(k.dir, errw, k.reason); err != nil {
			return err
		}
	}
	return nil
}

// captureHostSessionHomePath captures one ended host session home before the lease-protected
// reaper removes it. Its name identifies the pack; the current user-scope selection supplies the
// same surfaces and host composition that ordinary capture uses.
//
// It returns keptAt non-empty when the home must be KEPT rather than deleted, with why: either it
// held entries yolo neither rendered nor linked (a STRAY, design §3), or its own bookkeeping does
// not account for what is there, in which case capture would read this session's profile values as
// the user's edits (SP-D18) and nothing is captured. A stray is what the program wrote into its
// own config directory — every extension that keeps state there produces one — so it is a normal
// event, never a reason to refuse a launch (design §5, SHR-D8).
func captureHostSessionHomePath(dir string, errw io.Writer) (keptAt, why string, err error) {
	set := selectConfiguredHostPacks()
	decls := hostSessionDecls(set.packs, paths.Home())
	name := filepath.Base(dir)
	packName := ""
	for _, d := range decls {
		if strings.HasPrefix(name, d.pack+"-") && len(d.pack) > len(packName) {
			packName = d.pack
		}
	}
	if packName == "" {
		return setAsideHostSessionHome(dir, errw, fmt.Sprintf("no selected pack in %s declares a session home whose directory name starts %q", paths.UserConfigDisplayPath(), name))
	}
	var captures []hostSessionCapture
	type straySet struct {
		rel, shared string
		strays      []string
	}
	var strays []straySet
	unaccounted := ""
	for _, d := range decls {
		if d.pack != packName {
			continue
		}
		var surfaces []manifest.Surface
		for _, owned := range d.surfaces {
			surfaces = append(surfaces, owned...)
		}
		sort.Slice(surfaces, func(i, j int) bool {
			if surfaces[i].Agent != surfaces[j].Agent {
				return surfaces[i].Agent < surfaces[j].Agent
			}
			return surfaces[i].Name < surfaces[j].Name
		})
		audit := auditHostSessionHome(dir, d.rel, surfaces)
		if audit.keep != "" && unaccounted == "" {
			unaccounted = audit.keep
		}
		if len(audit.strays) > 0 {
			strays = append(strays, straySet{d.rel, audit.shared, audit.strays})
		}
		captures = append(captures, hostSessionCapture{
			home: filepath.Join(dir, hostScratchHomeName), rel: d.rel, surfaces: surfaces,
		})
	}
	if unaccounted != "" {
		return setAsideHostSessionHome(dir, errw, unaccounted)
	}
	if err := captureHostSessionDirs(captures, errw); err != nil {
		return "", "", err
	}
	for _, set := range strays {
		sort.Strings(set.strays)
		if reason := repatriateHostSessionStrays(dir, set.rel, set.shared, set.strays, errw); reason != "" {
			return setAsideHostSessionHome(dir, errw, reason)
		}
	}
	return "", "", nil
}

// setAsideHostSessionHome moves an ended home out of the swept root, under
// prune.HostSessionHomesKeptLeaf, so nothing is lost and no later sweep judges it again, and says
// so in one line with a step that works (design §5, SHR-D8/D9).
func setAsideHostSessionHome(dir string, errw io.Writer, reason string) (string, string, error) {
	root := filepath.Join(paths.GlobalStorage(), prune.HostSessionHomesKeptLeaf)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", "", fmt.Errorf("cannot keep the ended session home %s (%s): creating %s: %w; inspect that directory and remove it by hand",
			dir, reason, root, err)
	}
	for n := 0; ; n++ {
		to := filepath.Join(root, filepath.Base(dir))
		if n > 0 {
			to = fmt.Sprintf("%s-%d", to, n)
		}
		if _, err := os.Lstat(to); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", "", err
		}
		if err := os.Rename(dir, to); err != nil {
			return "", "", fmt.Errorf("cannot keep the ended session home %s (%s): %w; inspect that directory and remove it by hand",
				dir, reason, err)
		}
		if errw != nil {
			fmt.Fprintf(errw, "yolo host: kept an ended session home whole instead of deleting it: %s → %s, because %s.\n"+
				"  Nothing was lost. Inspect it, then delete it when you no longer need it: rm -rf %s\n",
				filepath.Base(dir), to, reason, shquote.Quote(to))
		}
		return to, reason, nil
	}
}

// writeHostSessionManifest records one launch's render beside its scratch home.
func writeHostSessionManifest(dir string, home hostSessionManifestHome) error {
	data, err := json.MarshalIndent(hostSessionManifest{Version: 1, Homes: []hostSessionManifestHome{home}}, "", "  ")
	if err != nil {
		return fmt.Errorf("recording what this session rendered: %w", err)
	}
	path := filepath.Join(dir, hostSessionManifestName)
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("writing the launch manifest %s: %w", path, err)
	}
	return nil
}

// readHostSessionManifest is the launch manifest of a retained home, or false when it has none
// (a home built before the manifest existed).
func readHostSessionManifest(dir string) (hostSessionManifest, bool) {
	data, err := os.ReadFile(filepath.Join(dir, hostSessionManifestName))
	if err != nil {
		return hostSessionManifest{}, false
	}
	var m hostSessionManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return hostSessionManifest{}, false
	}
	return m, true
}

// hostSessionAudit is what a retained home looks like to the reaper: what the program left in it,
// where its real config directory is, and — when the home cannot be deleted — why.
type hostSessionAudit struct {
	// strays are regular files under the declared root that the launch did not render, as
	// slash-separated paths relative to it.
	strays []string
	// shared is the real config directory the home stands in for.
	shared string
	// keep, when non-empty, is the reason this home cannot be deleted at all.
	keep string
}

// auditHostSessionHome classifies one retained home against the LAUNCH that built it, from its own
// manifest (SHR-D3), falling back to the retained baselines plus the current selection for a home
// that predates the manifest.
//
// Two answers, because they need opposite treatment (design §2, SHR-D10):
//
//   - keep is set when the launch's own record cannot be honoured: it rendered a path the current
//     packs no longer declare (so that file's edits cannot be captured), a stateful surface has no
//     retained baseline (capture would read this session's profile values as the user's edits,
//     SP-D18), or the declared root is gone or unreadable. Nothing is captured and the directory
//     is preserved whole.
//   - strays are the entries the program created in what it was told is its config directory. They
//     are not an error and never a refusal: they are repatriated after capture (SHR-D4).
func auditHostSessionHome(dir, rel string, surfaces []manifest.Surface) hostSessionAudit {
	scratch := filepath.Join(dir, hostScratchHomeName)
	target := render.Host(scratch, nil, render.OwnershipOwn)
	out := hostSessionAudit{shared: filepath.Join(paths.Home(), rel)}
	manifestHomes, _ := readHostSessionManifest(dir)
	var declared hostSessionManifestHome
	for _, h := range manifestHomes.Homes {
		if h.Rel == rel {
			declared = h
			break
		}
	}
	if declared.Shared != "" {
		out.shared = declared.Shared
	}

	// What the launch wrote, as paths relative to the declared root.
	rendered := make(map[string]bool, len(declared.Rendered))
	if declared.Rel != "" {
		for _, p := range declared.Rendered {
			rendered[filepath.FromSlash(p)] = true
		}
	} else {
		for _, s := range surfaces {
			if at, ok := strings.CutPrefix(target.SurfacePath(s), filepath.Join(scratch, rel)+string(filepath.Separator)); ok {
				rendered[filepath.FromSlash(at)] = true
			}
		}
	}

	// Capture needs a CURRENT surface to interpret each file the launch rendered. One the packs no
	// longer declare cannot be captured, so its bytes stay where they are.
	known := map[string]bool{}
	for _, s := range surfaces {
		if at, ok := strings.CutPrefix(target.SurfacePath(s), filepath.Join(scratch, rel)+string(filepath.Separator)); ok {
			known[filepath.FromSlash(at)] = true
		}
	}
	for relPath := range rendered {
		if !known[relPath] {
			out.keep = fmt.Sprintf("it rendered %s, which the current selected packs no longer declare, so its edits cannot be captured", filepath.ToSlash(relPath))
			return out
		}
	}

	if declared.Rel == "" {
		// No launch record for this directory: the baselines are the only inventory, and one naming a surface the
		// current packs no longer declare means the home predates a pack change.
		baselines := map[string]bool{}
		for _, s := range surfaces {
			baselines[s.Agent+"-"+s.Name+".last_render"] = true
		}
		if sidecars, err := os.ReadDir(target.SidecarDir()); err == nil {
			for _, entry := range sidecars {
				if !strings.HasSuffix(entry.Name(), ".last_render") {
					continue
				}
				if !baselines[entry.Name()] {
					out.keep = "the retained render has baseline " + entry.Name() + ", which the current selected packs no longer declare"
					return out
				}
				if info, err := entry.Info(); err != nil || !info.Mode().IsRegular() {
					out.keep = "the retained baseline " + entry.Name() + " is not a regular file"
					return out
				}
			}
		} else if !os.IsNotExist(err) {
			out.keep = fmt.Sprintf("its retained capture baselines cannot be read: %v", err)
			return out
		}
	}

	for _, s := range surfaces {
		if s.Mode != "" && s.Mode != manifest.ModeStateful {
			continue
		}
		info, err := os.Lstat(target.SurfacePath(s))
		if os.IsNotExist(err) {
			continue // deleting the whole surface file is the capture engine's existing no-op.
		}
		if err != nil {
			out.keep = fmt.Sprintf("cannot inspect rendered surface %s/%s: %v", s.Agent, s.Name, err)
			return out
		}
		if !info.Mode().IsRegular() {
			continue
		}
		baseline, err := os.Lstat(target.LastRenderPath(s.Agent, s.Name))
		if err != nil || !baseline.Mode().IsRegular() {
			out.keep = fmt.Sprintf("rendered stateful surface %s/%s has no retained baseline, so its edits cannot be told from this session's profile values", s.Agent, s.Name)
			return out
		}
	}

	root := filepath.Join(scratch, rel)
	rootInfo, err := os.Lstat(root)
	if err != nil {
		if os.IsNotExist(err) {
			out.keep = fmt.Sprintf("the retained session-home root %s is missing", rel)
		} else {
			out.keep = fmt.Sprintf("cannot inspect retained session-home root %s: %v", rel, err)
		}
		return out
	}
	if !rootInfo.IsDir() {
		out.keep = fmt.Sprintf("the retained session-home root %s is not a real directory", rel)
		return out
	}
	// Every directory on a rendered file's path is one yolo created (a MERGED directory); any
	// other directory is the program's own, and is reported whole rather than file by file, since
	// that is the entry a user or a repatriation acts on.
	merged := map[string]bool{}
	for relPath := range rendered {
		parts := strings.Split(filepath.ToSlash(relPath), "/")
		for i := 1; i < len(parts); i++ {
			merged[strings.Join(parts[:i], "/")] = true
		}
	}
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relPath, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if entry.IsDir() {
			if relPath != "." && !merged[filepath.ToSlash(relPath)] {
				out.strays = append(out.strays, filepath.ToSlash(relPath))
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		// A lock name is the program's own lock, never a stray (SP-D34): the session home never
		// links one, so finding one is expected and means nothing.
		if packdecl.SessionHomeNameProblem(entry.Name()) != "" {
			return nil
		}
		if !rendered[relPath] {
			out.strays = append(out.strays, filepath.ToSlash(relPath))
		}
		return nil
	}); err != nil && !os.IsNotExist(err) {
		out.keep = fmt.Sprintf("cannot read the retained session home: %v", err)
		return out
	}
	return out
}

// repatriateHostSessionStrays is sessionhome.Repatriate at the host notch: it moves what the
// program created in an ended session home into the real config directory, and returns a keep
// reason for anything that cannot go there (design §3, SHR-D4 — the rule itself lives in
// internal/sessionhome, so the jail's retire path and this one cannot disagree).
func repatriateHostSessionStrays(dir, rel, shared string, strays []string, errw io.Writer) (keep string) {
	spec := sessionhome.Spec{Home: filepath.Join(dir, hostScratchHomeName, rel), Shared: shared}
	outcomes, err := sessionhome.Repatriate(spec, strays)
	var moved, stayed []string
	for _, o := range outcomes {
		if o.To != "" {
			moved = append(moved, o.Name+" → "+o.To)
		} else {
			stayed = append(stayed, o.Name+" ("+o.Kept+")")
		}
	}
	if len(moved) > 0 && errw != nil {
		fmt.Fprintf(errw, "yolo host: an ended session home held %d entr%s yolo neither rendered nor linked, and moved %s into your config directory: %s.\n"+
			"  From the program's side the session home was that directory, so that is where its state belongs; later launches link it, so that name cannot become a stray again.\n",
			len(moved), plural(len(moved), "y", "ies"), plural(len(moved), "it", "them"), strings.Join(moved, ", "))
	}
	if err != nil {
		return fmt.Sprintf("moving what the program created into %s failed: %v", shared, err)
	}
	if len(stayed) > 0 {
		return "it held " + strings.Join(stayed, ", ")
	}
	return ""
}

// captureHostSessionDirs captures each home under the ordinary host apply lock. The render reads
// the ended session's own file and baseline, while configTarget keeps the normal host overlay and
// list-capture sidecars as the only persistent destination.
func captureHostSessionDirs(captures []hostSessionCapture, errw io.Writer) error {
	if len(captures) == 0 {
		return nil
	}
	target := hostConfigTarget("an ended owned host session home")
	if !target.hostOwned() {
		return fmt.Errorf("host session-home capture requires `host_management: \"own\"`; the source was kept. Set it to `own` and retry with `yolo prune --apply` or a host command launch")
	}
	var identity string
	for _, c := range captures {
		if len(c.surfaces) > 0 {
			identity = surfaceIdentity(c.surfaces[0].Agent, c.surfaces[0].Name)
			break
		}
	}
	if identity != "" {
		lock, ok := holdOwnedHostLock(target, "capture", "not captured", identity, errw)
		if !ok {
			return fmt.Errorf("the host capture lock was unavailable; source kept; retry after `yolo host apply` or another capture finishes")
		}
		defer lock.Close()
	}
	if err := os.MkdirAll(target.sidecarDir(), target.store.SidecarDirMode()); err != nil {
		return fmt.Errorf("cannot create the host capture store %s: %w; the source was kept. Fix the path or its permissions and retry with `yolo prune --apply` or a host command launch",
			target.sidecarDir(), err)
	}
	var messages bytes.Buffer
	pr := richtext.Printer{W: &messages}
	for _, c := range captures {
		owned := newOwnedHostRender(c.home)
		for _, s := range c.surfaces {
			if err := seedCurrentHostCaptureSidecars(target, c.home, s); err != nil {
				return fmt.Errorf("refresh capture inputs for %s/%s: %w", s.Agent, s.Name, err)
			}
			n, refused, err := owned.capture(pr, target, s)
			if err != nil {
				return fmt.Errorf("capture of %s/%s failed: %w", s.Agent, s.Name, err)
			}
			if refused {
				return fmt.Errorf("capture of %s/%s was refused; source kept for retry: %s",
					s.Agent, s.Name, strings.TrimSpace(messages.String()))
			}
			if n >= 0 {
				pr.Printf("Captured [cyan]%s/%s[/cyan] from an ended host session — %d %s now recorded.",
					s.Agent, s.Name, n, plural(n, "key", "keys"))
			}
			if err := seedMissingHostSessionBaseline(target, c.home, s); err != nil {
				return fmt.Errorf("preserve the first host session baseline for %s/%s: %w", s.Agent, s.Name, err)
			}
		}
	}
	if errw != nil {
		_, _ = io.Copy(errw, &messages)
	}
	return nil
}

// seedMissingHostSessionBaseline retains the ended session's trusted baseline in the ordinary host
// capture store when this was the first owned session render and launch-time apply was disabled.
// Later session renders then apply the captured overlay normally without writing session bytes to
// the native home. Existing apply baselines always win.
func seedMissingHostSessionBaseline(durable configTarget, sessionHome string, s manifest.Surface) error {
	if s.Mode != "" && s.Mode != manifest.ModeStateful {
		return nil
	}
	durableBaseline := durable.lastRenderFile(s.Agent, s.Name)
	if _, err := durableBaseline.lstat(); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	session := render.Host(sessionHome, nil, render.OwnershipOwn)
	sessionBaseline := session.LastRenderPath(s.Agent, s.Name)
	info, err := os.Lstat(sessionBaseline)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("session baseline %s is not a regular file", sessionBaseline)
	}
	data, err := os.ReadFile(sessionBaseline)
	if err != nil {
		return err
	}
	return durableBaseline.replace(data, durable.sidecarFileMode())
}

// seedCurrentHostCaptureSidecars makes each capture calculate the session's change from its
// launch-time baseline against the latest durable overlay and list record. The host lock is held
// by the caller, so distinct sessions accumulate their deltas without replacing one another.
func seedCurrentHostCaptureSidecars(durable configTarget, sessionHome string, s manifest.Surface) error {
	session := render.Host(sessionHome, nil, render.OwnershipOwn)
	for _, paths := range [][2]string{
		{durable.overlayPath(s.Agent, s.Name), session.OverlayPath(s.Agent, s.Name)},
		{durable.listCapturePath(s.Agent, s.Name), session.ListCapturePath(s.Agent, s.Name)},
	} {
		if paths[0] == "" || paths[1] == "" {
			continue
		}
		data, err := os.ReadFile(paths[0])
		if os.IsNotExist(err) {
			if err := os.Remove(paths[1]); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(paths[1]), session.SidecarDirMode()); err != nil {
			return err
		}
		if err := os.WriteFile(paths[1], data, session.SidecarFileMode()); err != nil {
			return err
		}
	}
	return nil
}

// hostSessionSelectionWords names the selection a session home is rendered for.
func hostSessionSelectionWords(launch *hostComposition) string {
	if launch.typedProfile != "" {
		return "-p " + shquote.Quote(launch.typedProfile)
	}
	return "your configured profile"
}

// makeHostSessionDir makes one launch's directory for pack under root and its lease, locked
// shared from the moment the file exists. A filesystem without flock leaves the lease unlocked
// (nil file): the reaper then cannot prove the directory unused and keeps it past its floor only
// while the lease reads held, so the directory is removed by this launch's own Close alone.
func makeHostSessionDir(root, pack string) (string, *os.File, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	dir, err := os.MkdirTemp(root, pack+"-")
	if err != nil {
		return "", nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, packload.EmbeddedLeaseName), os.O_RDONLY|os.O_CREATE|os.O_EXCL, 0o444)
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, err
	}
	for {
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_SH)
		if !errors.Is(err, syscall.EINTR) {
			break
		}
	}
	if err != nil {
		_ = f.Close()
		return dir, nil, nil
	}
	return dir, f, nil
}

// seedHostSession gives the scratch home what a render of the real home would start from (SP-D22):
// each surface's real file, copied, so a key of the user's own (a theme, `packages`) is in the
// session's render as it is in theirs; and, under `host_management: own`, the real capture store's
// sidecars for those surfaces, so the render is the next `yolo host apply` with this launch's
// selection, the captured edits folded in. Copies only: the real files and store are read, never
// written.
func seedHostSession(d hostSessionDecl, home, scratch string) error {
	real, own := render.Host(home, nil, render.OwnershipOwn), render.Host(scratch, nil, render.OwnershipOwn)
	copyFile := func(from, to string, mode os.FileMode) error {
		if from == "" || to == "" {
			return nil
		}
		data, err := os.ReadFile(from)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("reading %s: %w", from, err)
		}
		if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
			return err
		}
		return os.WriteFile(to, data, mode)
	}
	owned := hostOwnership() == render.OwnershipOwn
	for _, surfaces := range d.surfaces {
		for _, s := range surfaces {
			to := own.SurfacePath(s)
			if fi, err := os.Lstat(to); err == nil && fi.Mode()&os.ModeSymlink == 0 {
				continue
			}
			if err := copyFile(real.SurfacePath(s), to, 0o600); err != nil {
				return err
			}
			if !owned {
				continue
			}
			for _, pair := range [][2]string{
				{real.OverlayPath(s.Agent, s.Name), own.OverlayPath(s.Agent, s.Name)},
				{real.LastRenderPath(s.Agent, s.Name), own.LastRenderPath(s.Agent, s.Name)},
				{real.SelectionPath(s.Agent, s.Name), own.SelectionPath(s.Agent, s.Name)},
				{real.ListCapturePath(s.Agent, s.Name), own.ListCapturePath(s.Agent, s.Name)},
				{real.ListRecordPath(s.Agent, s.Name), own.ListRecordPath(s.Agent, s.Name)},
				{real.ProvenancePath(s.Agent, s.Name), own.ProvenancePath(s.Agent, s.Name)},
			} {
				if err := copyFile(pair[0], pair[1], 0o600); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// hostSessionRefusal is SP-D6's message at the host: headline, the facts, then what to run.
func hostSessionRefusal(d hostSessionDecl, at, root string, err error) error {
	return fmt.Errorf("cannot make this launch's own config directory for pack %s at %s: %v\n\n"+
		"Without it the program would read the shared %s, which another launch's profile can change, "+
		"so the launch does not start.\n\n"+
		"Free space or clear what is in the way under %s, then launch again:\n  ls -la %s\n  df -h %s",
		d.pack, at, err, d.shared, root, root, filepath.Dir(root))
}

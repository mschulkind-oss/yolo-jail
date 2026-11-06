package run

// THE PERSISTENCE MAP (docs/reference/jail-home.md#storage-class-terms, a term coined there): the
// list of paths a launch makes writable, each with a durability class and a scope, computed
// from the SAME definitions the mount argv reads. It feeds the briefing's storage-classes
// section, and TestThePersistenceMapIsTheMountPlan compares it against the
// assembled argv in both directions (DS-D1), so a writable mount added without the map
// knowing fails the unit gate.
//
// It is a VIEW of the mount plan, not a second source of truth about mounts: every list
// below is either one the argv emitters iterate too (podmanEarlyHomeBinds,
// podmanLateHomeBinds, prune.ScratchSlots, packload.WritableDirs, packload.SharedDirs,
// config.WritableHomeDirs) or a mount the emitter spells once and this file names beside a
// pointer to it (/workspace, ~/.cache, /mise, /run, /dev/shm).

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
)

// jailHome is the home every container backend binds, the destination side of every home
// mount in the argv.
const jailHome = "/home/agent"

// homeBind is one per-workspace bind podmanBaseMounts makes into the :ro home skeleton:
// <wsState>/<Subtree> on the host at $HOME/<HomeRel> in the jail, and the class the
// persistence map gives it.
type homeBind struct {
	Subtree string
	HomeRel string
	Class   jailcontent.PathClass
}

// podmanEarlyHomeBinds are the per-workspace home binds podmanBaseMounts emits BEFORE
// ~/.cache, in argv order: the capture surfaces (paths.HomeSurfaces), the generated-script
// anchor and ~/.config.
//
// `~/.yolo/bin` is internal: the entrypoint clears and rewrites both of its script dirs on
// every boot (resetAnchorDir), so nothing an agent put there survives a restart in any
// useful sense, and it is not a place for work.
func podmanEarlyHomeBinds() []homeBind {
	var out []homeBind
	for _, s := range paths.HomeSurfaces() {
		out = append(out, homeBind{s.Subtree, s.HomeRel, jailcontent.PathWorkspaceDurable})
	}
	return append(out,
		homeBind{"yolo-bin", ".yolo/bin", jailcontent.PathInternal},
		homeBind{"config", ".config", jailcontent.PathWorkspaceDurable},
	)
}

// podmanLateHomeBinds are the per-workspace home binds podmanBaseMounts emits AFTER the
// cache relocations and host-CAS aliases, in argv order: yolo's own single-file binds (a
// bootstrap script, logs, a lock, a CA bundle, the shell history), all internal, then ~/.ssh.
func podmanLateHomeBinds() []homeBind {
	var out []homeBind
	for _, f := range []string{
		"yolo-bootstrap.sh", "yolo-venv-precreate.sh", "yolo-perf.log", "yolo-socat.log",
		"yolo-entrypoint.lock", "yolo-ca-bundle.crt", "bash_history",
	} {
		out = append(out, homeBind{f, "." + f, jailcontent.PathInternal})
	}
	return append(out, homeBind{"ssh", ".ssh", jailcontent.PathWorkspaceDurable})
}

// alwaysTmpfsDirs are the two scratch dirs both container backends mount as tmpfs whatever
// `ephemeral_storage` says (ScratchMountArgs, appleContainerBaseMounts).
var alwaysTmpfsDirs = []string{"/run", "/dev/shm"}

// persistenceMapFor is the launch's persistence map on a container backend, or nil on a
// native one (macos-user mounts nothing, and its section is a later slice of the design).
//
// Its inputs are the ones the argv's home, shadow and scratch mounts read: the runtime, the
// config (`ephemeral_storage`, `writable_home_dirs`, `per_side_paths`), the selected packs and
// the workspace (whose own entries decide which per-side shadows mount). An attach re-derives
// the answer the launch argv acted on.
//
// NOT IN THE MAP, and stated so the pin test's scope is plain (DS-D14): a `host_files`
// entry's staged parent dir, which the run pipeline resolves after the briefing is written;
// a loophole's own bind mounts; and the nix daemon socket. None is a place for an agent's
// work, and the pin's fixture declares none of them.
//
// sealed is THE SEAL (seal.go): a fork's build jail has no machine tier. Its ~/.cache and /mise
// are private directories of its own workspace (sealedStores), and no pack's machine-scope
// directory is bound, so the map says this workspace for the two and names none of the rest.
type persistenceSources struct {
	cacheSource      string
	miseSource       string
	cacheRelocations []config.CacheRelocation
	hostCASAliases   []hostcas.Disposition
	scratchID        string
	// home is the host directory the launch binds as its home (Options.homeDir); "" names the
	// legacy <ws>/.yolo/home, which only a unit test that selects no setup means.
	home string
}

func persistenceMapFor(rt string, cfg *jsonx.OrderedMap, packs []*packload.Pack, workspace string, sealed bool, cname ...string) *jailcontent.PersistenceMap {
	return persistenceMapForSources(rt, cfg, packs, workspace, sealed, persistenceSources{}, cname...)
}

func persistenceMapForSources(rt string, cfg *jsonx.OrderedMap, packs []*packload.Pack, workspace string, sealed bool, sources persistenceSources, cname ...string) *jailcontent.PersistenceMap {
	if slices.Contains(paths.NativeRuntimes, rt) { // parity: NotApplicable — macos-user mounts nothing; its section is the design's §8 step 5
		return nil
	}
	m := &jailcontent.PersistenceMap{HostSources: make(map[string]string)}
	add := func(p string, c jailcontent.PathClass, source string) {
		m.Paths = append(m.Paths, jailcontent.PersistentPath{Path: p, Class: c})
		m.HostSources[p] = source
	}
	home := func(rel string) string { return jailHome + "/" + rel }
	wsState := sources.home
	if wsState == "" {
		wsState = paths.LegacyHomeState(workspace)
	}
	cacheSource := paths.GlobalCache()
	miseSource := jailMiseStoreDir(false)
	if sources.cacheSource != "" {
		cacheSource = sources.cacheSource
	}
	if sources.miseSource != "" {
		miseSource = sources.miseSource
	}

	// The workspace bind, `-v <workspace>:/workspace` on both backends: the host's own
	// directory, its own class. The per-side shadows beneath it (/workspace/.venv,
	// /workspace/node_modules, per_side_paths) are NOT the host's: each is a bind from
	// <ws>/.yolo/home/venv-shadows, so per workspace — the set venvShadowMountArgs mounts,
	// computed by the same rules (perSideShadowRels).
	add("/workspace", jailcontent.PathProject, workspace)
	for _, rel := range perSideShadowRels(cfg, workspace) {
		backing := filepath.Join(wsState, "venv-shadows", strings.ReplaceAll(rel, "/", "__"))
		add("/workspace/"+rel, jailcontent.PathWorkspaceDurable, backing)
	}

	if rt == "container" { // parity: HonoredBy — Apple Container binds wsState whole at the home, which is the per-workspace tier podman binds dir by dir
		// Apple Container binds <ws>/.yolo/home read-write, WHOLE, at the home
		// (appleContainerBaseMounts), so every home path not bound elsewhere is durable
		// for this workspace, and there is no read-only base.
		add(jailHome, jailcontent.PathWorkspaceDurable, wsState)
	} else {
		for _, b := range podmanEarlyHomeBinds() {
			add(home(b.HomeRel), b.Class, filepath.Join(wsState, b.Subtree))
		}
		for _, b := range podmanLateHomeBinds() {
			add(home(b.HomeRel), b.Class, filepath.Join(wsState, b.Subtree))
		}
		for _, rel := range sortedWritableHomeDirs(config.WritableHomeDirs(cfg, packs)) {
			add(home(rel), jailcontent.PathWorkspaceDurable, filepath.Join(wsState, config.WritableHomeBackingSubdir, rel))
		}
		for _, dir := range packload.WritableDirs(packs) {
			sourceRel := strings.TrimPrefix(filepath.FromSlash(dir), ".")
			add(home(dir), jailcontent.PathWorkspaceDurable, filepath.Join(wsState, sourceRel))
		}
	}

	// The machine tier, on both backends: paths.GlobalCache() at ~/.cache and each selected
	// pack's shared dir from paths.GlobalHome(). /mise is podman's machine store (a store dir,
	// or the one named volume on a Mac) and Apple Container's per-workspace tool disk
	// (OQ-MB1, actooldisk.go). Under the seal the two stores are the build's own and no shared
	// dir is bound (assembleInput.cacheSource, miseSource, podmanBaseMounts).
	if sealed {
		add(home(".cache"), jailcontent.PathWorkspaceDurable, cacheSource)
		add("/mise", jailcontent.PathWorkspaceDurable, miseSource)
	} else {
		add(home(".cache"), jailcontent.PathMachineDurable, cacheSource)
		for _, dir := range packload.SharedDirs(packs) {
			add(home(dir), jailcontent.PathMachineDurable, filepath.Join(paths.GlobalHome(), filepath.FromSlash(dir)))
		}
		if rt == "container" { // parity: HonoredBy — Apple Container's /mise is its per-workspace named tool volume, not the Podman machine store
			volumeLabel := prune.MiseVolumeName(firstCname(cname))
			if sources.miseSource != "" {
				volumeLabel = sources.miseSource
			}
			add("/mise", jailcontent.PathWorkspaceDurable, volumeLabel)
		} else {
			add("/mise", jailcontent.PathMachineDurable, miseSource)
		}
		for _, rel := range sortedCacheRelocations(sources.cacheRelocations) {
			add(home(".cache/"+rel.Subdir), jailcontent.PathWorkspaceDurable, rel.Target)
		}
		for _, alias := range hostcas.Aliased(sources.hostCASAliases) {
			add(alias.Dest, jailcontent.PathMachineDurable, alias.Source)
		}
	}

	// Scratch destinations use the exact volume identifiers emitted by this launch.
	if rt != "container" && cfgStr(cfg, "ephemeral_storage") != "tmpfs" && sources.scratchID != "" { // parity: HonoredBy — Apple Container emits scratch slots as tmpfs, not named volumes
		for i, name := range ScratchVolumeNames(cfgStr(cfg, "ephemeral_storage"), firstCname(cname), sources.scratchID) {
			if i < len(prune.ScratchSlots) {
				add(prune.ScratchSlots[i].Dest, jailcontent.PathPerLaunch, name)
			}
		}
	} else {
		for _, s := range prune.ScratchSlots {
			add(s.Dest, jailcontent.PathPerLaunch, "tmpfs")
		}
	}
	for _, d := range alwaysTmpfsDirs {
		add(d, jailcontent.PathPerLaunch, "tmpfs")
	}
	m.PerLaunchInRAM = rt == "container" || cfgStr(cfg, "ephemeral_storage") == "tmpfs" // parity: HonoredBy — Apple Container's scratch slots are tmpfs in appleContainerBaseMounts
	return m
}

func firstCname(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

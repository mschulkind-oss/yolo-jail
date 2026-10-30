package run

// inheritscope.go writes the two GENERATED user-scope files a jail inherits, replacing the
// raw `:ro` bind of the human's real config.jsonc that used to serve as an inner jail's
// user scope (OQ-LP9, docs/reference/loophole-system.md#why-its-this-way).
//
// WHAT CHANGED, in one sentence: the inner user scope is now COMPOSED from the effective
// config and FILTERED per consumer (internal/config/inherit.go), rather than being whatever
// happened to be in the human's top config file.
//
// FOUR PROPERTIES THIS FILE IS RESPONSIBLE FOR, all four load-bearing:
//
//  1. SINGLE-FILE DELIVERY INTO A JAIL-OWNED DIRECTORY (R8). Each generated file is
//     mounted as its own `:ro` single file, and the DIRECTORY around it
//     (~/.config/yolo-jail) stays the jail's own writable home. That is what makes
//     writing BESIDE the file jail-local: an in-jail agent can drop a layer file next to
//     it and pass --user-layer, and nothing it writes can reach the host. Mounting the
//     DIRECTORY instead would take that away, which is why this never does.
//  2. THE NESTED FILE IS WRITTEN ONLY WHERE NESTING IS POSSIBLE (R2). Not gated by a
//     conditional inside a file that is always written — genuinely absent. On a backend
//     that cannot nest there is no file, so nothing is mounted that serves a capability
//     the setup lacks.
//  3. RECURSION IS BY COMPOSITION (R6). This runs from the EFFECTIVE config of whatever
//     jail is launching, which at depth 2 already includes what depth 1 inherited plus any
//     --user-layer it was passed. So jail A hands B one inherited file; it never stacks a
//     chain of ancestors' files, and there is no rule that changes with depth.
//  4. LAUNCH-FROZEN (R7). These are rendered once at assembly, so a host-side config edit
//     lands at the next launch rather than live. That is a REAL behaviour change from the
//     live bind, named in the file header, and it is the jail's normal contract — env, the
//     image and the relay wiring are all frozen at container start. `yolo config drift`
//     is the existing mechanism for noticing, and it now covers this file too.

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// The in-jail names of the two generated files. Both live in the jail's own
// ~/.config/yolo-jail, beside where a --user-layer file would go.
//
// The PREFLIGHT file takes the name `config.jsonc` deliberately: it IS the inner user
// scope, so every existing in-jail reader (config.LoadConfig, LoadPacks, the loopholes
// commands, `yolo check`) finds it at paths.UserConfigPath() with no plumbing at all. The
// nested file gets its own name because nothing reads it as "the user config" — an inner
// launcher merges it explicitly.
const (
	inheritPreflightRel       = ".config/yolo-jail/config.toml"
	inheritNestedRel          = ".config/yolo-jail/inherited-launch.toml"
	inheritPreflightLegacyRel = ".config/yolo-jail/config.jsonc"
	inheritNestedLegacyRel    = ".config/yolo-jail/inherited-launch.jsonc"
)

// inheritScopeFile is one generated file to deliver: its rendered bytes and where it goes.
type inheritScopeFile struct {
	scope          config.InheritScope
	rel            string // path under /home/agent
	body           string
	format         configdoc.Format
	fallbackReason string
	keys           int
}

// canNest reports whether a jail on this backend can launch a jail of its own — the gate on
// whether the nested-launch file is written at all (R2).
//
// The answer is a property of the BACKEND, not of the current process: podman bakes a
// nested podman into the image (flake.nix corePackages: podman, fuse-overlayfs,
// slirp4netns) and the CLI has a whole podman-in-podman branch (--userns=host, forced
// --net=host, --cgroups=disabled). Apple Container has no nested container runtime inside
// its VM, and macos-user has no container at all — a Seatbelt-confined native process
// cannot start one, and the macos-user census (docs/reference/macos-user-nix-and-features.md)
// already states that nothing is bind-mounted there.
//
// Deliberately keyed on the runtime STRING rather than on a probe: this decides whether to
// write a file for the jail we are about to start, so it must answer "can the container we
// are creating nest?", which no probe of the current process can tell us.
func canNest(rt string) bool { return rt == "podman" } // parity: Honored — only Podman launches can nest; other backends omit the nested scope.

// inheritScopeFiles renders the generated user-scope files for a launch.
//
// effective is the already-composed config — the same value `yolo config dump` serializes
// (config.LoadConfig → SnapshotJSON), which is what makes both files renders of ONE
// computation rather than a second composer that could drift from the first.
//
// unknown collects config keys the census does not classify. They are DROPPED from every
// generated file and returned so the caller can say so: silently passing an unclassified
// key into a jail would defeat the census, and silently dropping it would hide a schema
// addition. It is a live path whenever a root the validator accepts has no classification:
// the readable (TOML) roots once reached it on every launch that set them, until
// config.readableInheritCensus classified them. Two census tests now hold both vocabularies
// to it (config.TestInheritCensusIsTotal, config.TestInheritCensusCoversReadableRoots), so
// a non-empty set means one of them was bypassed.
//
// A scope whose filter kept NOTHING yields keys==0, and the caller then delivers no file at
// all. That matters twice over. It is honest — a user with no relevant config had no user
// scope before this change either, and a file saying `{}` under a header explaining what was
// filtered would invite the reader to hunt for a key they never set. And it keeps the
// promise the golden argv makes: a jail launched from a config with nothing to inherit emits
// byte-identical argv to the one it emitted before this feature existed.
//
// src is effective's provenance, which each file records in its header so a refusal in the jail
// names the host file that set a key (config.RenderFilteredWithOrigins); nil records nothing.
func inheritScopeFiles(effective *jsonx.OrderedMap, src *config.Sources, rt, launchedAt string) (files []inheritScopeFile, unknown []string, err error) {
	return inheritScopeFilesWithSelector(effective, src, rt, launchedAt, nil)
}

// inheritScopeFilesWithSelector lets a backend preserve its current destination family before
// the first stage/write. The selector may keep a compatible generated JSONC route, or refuse an
// ambiguous destination; it never edits the destination itself.
func inheritScopeFilesWithSelector(effective *jsonx.OrderedMap, src *config.Sources, rt, launchedAt string, selectLegacy func(config.InheritScope, configdoc.Format) (bool, error)) (files []inheritScopeFile, unknown []string, err error) {
	seen := map[string]bool{}
	addUnknown := func(keys []string) {
		for _, k := range keys {
			if !seen[k] {
				seen[k] = true
				unknown = append(unknown, k)
			}
		}
	}
	render := func(scope config.InheritScope) error {
		filtered, unk, ferr := config.FilterInheritErr(effective, scope)
		if ferr != nil {
			return ferr
		}
		addUnknown(unk)
		if filtered.Len() == 0 {
			return nil
		}
		body, format, fallbackReason, rerr := config.RenderFilteredForDelivery(filtered, src, scope, launchedAt)
		if rerr != nil {
			return rerr
		}
		if selectLegacy != nil {
			forceLegacy, selectErr := selectLegacy(scope, format)
			if selectErr != nil {
				return selectErr
			}
			if forceLegacy && format != configdoc.JSONC {
				body, rerr = config.RenderFilteredWithOrigins(filtered, src, scope, launchedAt)
				if rerr != nil {
					return rerr
				}
				format = configdoc.JSONC
				fallbackReason = "a regular legacy JSON or JSONC sibling already occupies the in-jail destination family"
			}
		}
		rel := InheritedScopeRel(scope, format)
		files = append(files, inheritScopeFile{scope: scope, rel: rel, body: body, format: format, fallbackReason: fallbackReason, keys: filtered.Len()})
		return nil
	}

	if err := render(config.InheritPreflight); err != nil {
		return nil, nil, err
	}
	// R2: absent, not empty, on a backend that cannot nest.
	if canNest(rt) {
		if err := render(config.InheritNested); err != nil {
			return nil, nil, err
		}
	}
	return files, unknown, nil
}

func (o *Options) inheritedScopeWithSelector(rt string, stamped bool, selectLegacy func(config.InheritScope, configdoc.Format) (bool, error)) ([]inheritScopeFile, error) {
	out := o.pr(o.Stderr)
	stamp := ""
	if stamped {
		stamp = o.inheritStamp()
	}
	effective, src, err := o.effectiveConfigForInherit()
	if err != nil {
		return nil, fmt.Errorf("the jail's inherited user config could not be rendered because the config no longer loads: %w. "+
			"Fix the file it names, run `yolo check` to confirm, then launch again", err)
	}
	files, unknown, err := inheritScopeFilesWithSelector(effective, src, rt, stamp, selectLegacy)
	if err != nil {
		return nil, err
	}
	if len(unknown) > 0 {
		// Named, not swallowed: an unclassified key reaching a jail unreviewed is the
		// failure the census exists to prevent, so its absence has to be visible.
		out.print("[yellow]Warning: config keys with no inherit classification were " +
			"dropped from the jail's user scope: " + joinComma(unknown) +
			" — classify them in internal/config/inherit.go[/yellow]")
	}
	for _, f := range files {
		if f.fallbackReason != "" {
			out.print("[yellow]Warning: inherited " + f.scope.String() +
				" scope uses generated JSONC because " + f.fallbackReason +
				". No user-owned sibling file was changed.[/yellow]")
		}
	}
	return files, nil
}

func selectInheritedJSONCRoute(wsState string, mapDestination func(string) (string, error), scope config.InheritScope, format configdoc.Format) (bool, error) {
	return selectInheritedJSONCRouteSkipping(wsState, mapDestination, scope, format, nil)
}

// selectInheritedJSONCRouteSkipping is selectInheritedJSONCRoute judging every storage-relative
// path in gone as absent: a dry run's plan for an empty placeholder a launch moves aside first
// (clearPodmanPlaceholders).
func selectInheritedJSONCRouteSkipping(wsState string, mapDestination func(string) (string, error), scope config.InheritScope, format configdoc.Format, gone map[string]bool) (bool, error) {
	lstatHomeFile := func(wsState, rel string) (os.FileInfo, bool, error) {
		if gone[rel] {
			return nil, false, nil
		}
		return lstatHomeFile(wsState, rel)
	}
	legacyRel, tomlRel := inheritScopeFamily(scope)
	legacyJSONRel := strings.TrimSuffix(legacyRel, ".jsonc") + ".json"
	legacyPaths := []string{legacyRel, legacyJSONRel}
	var existingLegacy []string
	for _, rel := range legacyPaths {
		storageRel, err := mapDestination(rel)
		if err != nil {
			return false, err
		}
		path := filepath.Join(wsState, storageRel)
		_, exists, err := lstatHomeFile(wsState, storageRel)
		if err != nil {
			return false, fmt.Errorf("cannot establish inherited %s user-config destination %s: %w. Restore this jail-home path as a real directory tree, preserving its data, then relaunch", scope, path, err)
		}
		if exists {
			if scope == config.InheritNested && filepath.Ext(path) == ".json" {
				return false, fmt.Errorf("refusing inherited nested scope: existing legacy JSON file %s would remain beside generated JSONC; preserve its bytes and mode, review its contents, then move it aside only after review or arrange for one unambiguous inherited file before relaunch", path)
			}
			existingLegacy = append(existingLegacy, path)
		}
	}
	tomlStorageRel, err := mapDestination(tomlRel)
	if err != nil {
		return false, err
	}
	tomlPath := filepath.Join(wsState, tomlStorageRel)
	_, tomlExists, err := lstatHomeFile(wsState, tomlStorageRel)
	if err != nil {
		return false, fmt.Errorf("cannot establish inherited %s user-config destination %s: %w. Restore this jail-home path as a real directory tree, preserving its data, then relaunch", scope, tomlPath, err)
	}
	if len(existingLegacy) > 0 && tomlExists {
		return false, fmt.Errorf("refusing inherited %s user config: legacy files %s and TOML %s exist in the jail's writable home; preserve both families, move one aside only after review, then relaunch", scope, strings.Join(existingLegacy, " and "), tomlPath)
	}
	if len(existingLegacy) > 0 {
		return true, nil
	}
	if tomlExists && format != configdoc.TOML {
		legacyPath := filepath.Join(wsState, legacyRel)
		return false, fmt.Errorf("refusing inherited %s user config: its legacy JSONC payload needs %s, but the jail already has %s; preserve both, convert or back up one file, then relaunch", scope, legacyPath, tomlPath)
	}
	return false, nil
}

func inheritScopeFamily(scope config.InheritScope) (legacy, toml string) {
	if scope == config.InheritPreflight {
		return inheritPreflightLegacyRel, inheritPreflightRel
	}
	return inheritNestedLegacyRel, inheritNestedRel
}

func refuseExistingScopeDestination(wsState string, mapDestination func(string) (string, error), scope config.InheritScope, backend, overwriteRisk, emptyNote string) (bool, error) {
	legacyRel, tomlRel := inheritScopeFamily(scope)
	legacyPaths := []string{legacyRel, strings.TrimSuffix(legacyRel, ".jsonc") + ".json", tomlRel}
	var existing []string
	for _, rel := range legacyPaths {
		storageRel, err := mapDestination(rel)
		if err != nil {
			return false, err
		}
		path := filepath.Join(wsState, storageRel)
		_, exists, err := lstatHomeFile(wsState, storageRel)
		if err != nil {
			return false, fmt.Errorf("cannot establish %s inherited %s destination %s: %w. Restore this jail-home path as a real directory tree, preserving its data, then relaunch", backend, scope, path, err)
		}
		if exists {
			existing = append(existing, path)
		}
	}
	if len(existing) > 1 {
		return false, fmt.Errorf("refusing %s inherited %s scope: existing user files occupy %s; preserve them, choose one only after reviewing their contents, then relaunch", backend, scope, strings.Join(existing, " and "))
	}
	if len(existing) == 1 {
		path := existing[0]
		empty := ""
		if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Size() == 0 {
			empty = " It is empty. " + emptyNote
		}
		mv := shquote.JoinDisplay([]string{"mv", path, path + ".moved-aside"})
		return false, fmt.Errorf("refusing %s inherited %s scope: existing user file %s is in the way, and %s.%s Review it, preserving its contents and mode, then move it aside (%s) and relaunch", backend, scope, path, overwriteRisk, empty, mv)
	}
	return false, nil
}

// in the assembly prefix. It rejects duplicate equal-specificity mounts and any backing outside
// wsState, rather than guessing from a generated staging filename.
func mappedHomePath(wsState string, argv []string, containerPath string) (string, error) {
	bestDepth := -1
	var sourceRoot, mountDest string
	for i := 0; i+1 < len(argv); i++ {
		if argv[i] != "-v" {
			continue
		}
		fields := strings.SplitN(argv[i+1], ":", 3)
		if len(fields) < 2 {
			continue
		}
		source, dest := fields[0], filepath.Clean(fields[1])
		rel, err := filepath.Rel(dest, filepath.Clean(containerPath))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		depth := strings.Count(strings.Trim(dest, string(filepath.Separator)), string(filepath.Separator)) + 1
		if depth < bestDepth {
			continue
		}
		if depth == bestDepth {
			return "", fmt.Errorf("ambiguous equal-specificity home binds for %s (%s and %s); preserve user files and resolve the mount overlap before relaunch", containerPath, mountDest, dest)
		}
		bestDepth, sourceRoot, mountDest = depth, source, dest
	}
	if bestDepth < 0 || sourceRoot == "" || !filepath.IsAbs(sourceRoot) {
		return "", fmt.Errorf("no proven home bind covers %s; preserve user files and retry after the destination mapping is restored", containerPath)
	}
	suffix, err := filepath.Rel(mountDest, filepath.Clean(containerPath))
	if err != nil || suffix == ".." || strings.HasPrefix(suffix, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid home-bind containment for %s", containerPath)
	}
	backing := filepath.Join(sourceRoot, suffix)
	rel, err := filepath.Rel(filepath.Clean(wsState), backing)
	if err != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("the covering home bind for %s maps through %s outside this jail's state; preserve user files and resolve the mount before relaunch", containerPath, sourceRoot)
	}
	return rel, nil
}

// lstatHomeFile checks one actual destination beneath the already-existing jail home without
// following a link at any component. A missing component means no sibling exists; any linked or
// non-directory parent and any nonregular final object fail closed.
func lstatHomeFile(wsState, rel string) (os.FileInfo, bool, error) {
	state, err := os.Lstat(wsState)
	if os.IsNotExist(err) {
		parent, parentErr := os.Lstat(filepath.Dir(wsState))
		if parentErr == nil && (parent.Mode()&os.ModeSymlink != 0 || !parent.IsDir()) {
			return nil, false, fmt.Errorf("home state parent %s is not a real directory; restore it without following the link, preserve its data, then relaunch", filepath.Dir(wsState))
		}
		if parentErr != nil && !os.IsNotExist(parentErr) {
			return nil, false, parentErr
		}
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if state.Mode()&os.ModeSymlink != 0 || !state.IsDir() {
		return nil, false, fmt.Errorf("home state is not a real directory")
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, false, fmt.Errorf("invalid home-relative path %q", rel)
	}
	root, err := openStateRoot(wsState)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = root.Close() }()
	parts := strings.Split(clean, string(filepath.Separator))
	for i, part := range parts {
		info, err := root.Lstat(part)
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		full := filepath.Join(wsState, filepath.Join(parts[:i+1]...))
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, false, fmt.Errorf("%s is a symbolic link; the target was not followed. Replace the link with a regular config file or remove it after preserving its target data, then relaunch", full)
		}
		if i == len(parts)-1 {
			if !info.Mode().IsRegular() {
				return nil, false, fmt.Errorf("%s is not a regular file; replace it with a regular config file, preserving its contents, then relaunch", full)
			}
			return info, true, nil
		}
		if !info.IsDir() {
			return nil, false, fmt.Errorf("%s is not a directory", full)
		}
		next, err := root.OpenRoot(part)
		if err != nil {
			return nil, false, err
		}
		_ = root.Close()
		root = next
	}
	return nil, false, fmt.Errorf("invalid home-relative path %q", rel)
}

// InheritedScopeRel is the home-relative path a launch delivers an inherited scope at,
// for the format config.RenderFilteredForDelivery chose. It is exported for the
// integration suite, whose probes must read the file a launch really delivers rather
// than a spelling that goes stale when the delivery format changes.
func InheritedScopeRel(scope config.InheritScope, format configdoc.Format) string {
	if scope == config.InheritPreflight {
		if format == configdoc.TOML {
			return inheritPreflightRel
		}
		return inheritPreflightLegacyRel
	}
	if format == configdoc.TOML {
		return inheritNestedRel
	}
	return inheritNestedLegacyRel
}

// userConfigMountArgs delivers the generated inner user scope, returning the container
// argv for it.
//
// It REPLACED a raw `:ro` bind of paths.UserConfigPath(). The old comment said the mount
// was "for nested jails"; measured, its readers were broader (`yolo check`, `yolo loopholes
// list`, `yolo pack` all read user scope in-jail on every setup) and its content was
// narrower (only config.jsonc crossed, so `include_if_found` files stayed host-side). So it
// was neither the effective config nor a designed subset — it filtered by accident. Now it
// filters on purpose, per consumer.
//
// The user's ~/.config/yolo-jail/config.lua crossed here too, as the host's own file and
// unfiltered, because it was a Lua transform script rather than a config with keys to
// classify. The transform is gone (docs/reference/pack-system.md#oq-lt1) and so is that
// bind: nothing in the jail reads the file, so mounting it would be one more host path
// crossing for no reader.
func (o *Options) userConfigMountArgs(rt, wsState string) []string {
	args, err := o.userConfigMountArgsForHome(rt, wsState, nil)
	if err != nil {
		o.pr(o.Stderr).print("[yellow]Warning: could not render this jail's inherited user config (" + err.Error() + ")[/yellow]")
		return nil
	}
	return args
}

// userConfigMountArgsForAssembly checks the actual, already assembled home bind graph before
// any generated scope is staged. The most-specific covering bind decides where an existing
// sibling is stored; an unknown, ambiguous, linked, or nonregular destination refuses assembly.
func (o *Options) userConfigMountArgsForAssembly(in *assembleInput, assembled []string) ([]string, error) {
	if in == nil {
		return nil, fmt.Errorf("cannot establish the inherited user config destination without assemble input")
	}
	if in.packFilesPlan == nil {
		in.packFilesPlan = planPackFilesMounts(in.packs, in.treeDirs, in.agentsPath)
	}
	homeBinds := append([]string(nil), assembled...)
	homeBinds = append(homeBinds, plannedPackFilesBindArgs(in.packFilesPlan, in.rt)...)
	selectLegacy := func(scope config.InheritScope, format configdoc.Format) (bool, error) {
		mapDestination := func(rel string) (string, error) {
			return mappedHomePath(in.wsState, homeBinds, "/home/agent/"+rel)
		}
		if in.rt == "container" { // parity: Refused — Apple Container cannot cover an existing HOME file without overwriting it.
			return refuseExistingScopeDestination(in.wsState, mapDestination, scope,
				"Apple Container", "the whole HOME bind would overwrite it", "Reviewing it is quick.")
		}
		return selectInheritedJSONCRoute(in.wsState, mapDestination, scope, format)
	}
	return o.userConfigMountArgsForHome(in.rt, in.wsState, selectLegacy)
}

func (o *Options) userConfigMountArgsForHome(rt, wsState string, selectLegacy func(config.InheritScope, configdoc.Format) (bool, error)) ([]string, error) {
	var args []string
	out := o.pr(o.Stderr)
	files, err := o.inheritedScopeWithSelector(rt, true, selectLegacy)
	if err != nil {
		return nil, err
	}
	// The GENERATED files. Written into wsState (the jail's own per-workspace state dir,
	// which is where every other composed file goes — the gitconfig, the user-env script)
	// and mounted from there, one bind per file.
	for _, f := range files {
		// Both writes are BENEATH wsState (writeFileBeneath, wsstatebeneath.go): the jail can
		// leave a link at either destination, or above the Apple Container one, for a plain
		// write to follow onto a host file, and podman would then bind the link's target.
		stagedName := "inherit-" + f.scope.String() + filepath.Ext(f.rel)
		staged := filepath.Join(wsState, stagedName)
		if err := writeFileBeneath(wsState, stagedName, []byte(f.body), 0o644); err != nil {
			out.print("[yellow]Warning: could not stage the " + f.scope.String() +
				" user config at " + staged + " (" + err.Error() + ") — this jail gets no " +
				f.scope.String() + " user scope[/yellow]")
			continue
		}
		if rt == "container" { // parity: Honored — Apple Container delivers through its whole-HOME bind.
			// Apple Container mounts the whole wsState at /home/agent and cannot
			// bind a single file, so the content is copied to its destination inside
			// that tree instead.
			_ = writeFileBeneath(wsState, filepath.FromSlash(f.rel), []byte(f.body), 0o644)
			continue
		}
		// SINGLE-FILE bind (R8): the enclosing ~/.config/yolo-jail stays the jail's
		// own writable dir, so a --user-layer file written beside this one is
		// jail-local and cannot reach the host.
		args = append(args, "-v", staged+":/home/agent/"+f.rel+":ro")
	}
	return args, nil
}

// macosUserInheritedScope is the macos-user arm's delivery of the generated user scope: the
// files inheritedScope renders, as files of core's for the home overlay
// (buildMacosHomeOverlayWith). The overlay is copied into the sandbox account home by the
// bootstrap, through the ~/.config layout link into the workspace's sidecar, and the Seatbelt
// profile denies the agent writes to it — so the file is read-only as a container's single-file
// `:ro` bind is, and the directory around it (where a --user-layer file goes, and the
// conventional local pack) stays the agent's own (R8). canNest is false for this backend, so
// the nested-launch file is never among them (R2).
//
// AND THE ONE IT DID NOT RENDER, removed (removeStaleInheritedScope), on a launch only. A
// container's bind leaves nothing behind when it is not made; this backend's copy stays in the
// sidecar, so a user config that lost its last inheritable key would otherwise keep serving the
// previous launch's user scope to every in-sandbox reader. A plan render removes nothing.
func (o *Options) macosUserInheritedScope(rt string) ([]overlayFile, error) {
	var core []overlayFile
	wsState := paths.WorkspaceHomeState(o.Workspace)
	preserve := map[string]bool{}
	delivered := map[string][]byte{}
	cleared := false
	var gone map[string]bool
	selectLegacy := func(scope config.InheritScope, format configdoc.Format) (bool, error) {
		if scope == config.InheritPreflight && !cleared {
			// Issue #66: an empty file a podman launch's bind left here holds no data; move it
			// aside (or, in a dry run, plan as though it were) before judging the destination.
			cleared, gone = true, o.clearPodmanPlaceholders(wsState)
		}
		forceLegacy, err := selectInheritedJSONCRouteSkipping(wsState, func(rel string) (string, error) {
			// DeriveDarwinHomeLayout lays ~/.config as the `config` subtree of this sidecar.
			return filepath.FromSlash(strings.TrimPrefix(rel, ".")), nil
		}, scope, format, gone)
		if err != nil {
			return false, err
		}
		legacy, toml := inheritScopeFamily(scope)
		selected := toml
		if forceLegacy || format != configdoc.TOML {
			selected = legacy
		}
		selectedStorage := filepath.FromSlash(strings.TrimPrefix(selected, "."))
		var existing []string
		for _, rel := range []string{legacy, strings.TrimSuffix(legacy, ".jsonc") + ".json", toml} {
			storageRel := filepath.FromSlash(strings.TrimPrefix(rel, "."))
			if gone[storageRel] {
				continue
			}
			_, exists, err := lstatHomeFile(wsState, storageRel)
			if err != nil {
				return false, err
			}
			if exists {
				existing = append(existing, rel)
			}
		}
		if len(existing) == 0 {
			return forceLegacy, nil
		}
		if len(existing) != 1 || existing[0] != selected {
			return false, refuseInheritedOverwrite(wsState, scope)
		}
		body, admitted := o.priorInheritedDelivery(wsState, selected)
		if !admitted {
			return false, refuseInheritedOverwrite(wsState, scope)
		}
		preserve[selectedStorage] = true
		delivered[selected] = body
		return forceLegacy, nil
	}
	files, err := o.inheritedScopeWithSelector(rt, false, selectLegacy)
	if err != nil {
		return nil, err
	}
	// UNSTAMPED: the home overlay is content-keyed and an unchanged relaunch reuses it whole
	// (buildMacosHomeOverlayTreeIDs). A launch time in this header moved the overlay's digest on
	// every launch, so every relaunch rebuilt, re-staged and re-installed all of it.
	for _, f := range files {
		if prior, ok := delivered[f.rel]; ok && !bytes.Equal(prior, []byte(f.body)) {
			return nil, refuseInheritedOverwrite(wsState, f.scope)
		}
		core = append(core, overlayFile{Rel: f.rel, Body: []byte(f.body)})
	}
	if !o.DryRun {
		// Preserve an unchanged, admitted hardlink until the installer sees the same inode.
		removeStaleInheritedScope(wsState, preserve)
	}
	return core, nil
}

// inheritedScopeStagingRoot is private so tests can use an isolated staging tree; production
// always asks macos-user for its established root.
var inheritedScopeStagingRoot = macosuser.StagingRoot

func refuseInheritedOverwrite(wsState string, scope config.InheritScope) error {
	_, err := refuseExistingScopeDestination(wsState, func(rel string) (string, error) {
		return filepath.FromSlash(strings.TrimPrefix(rel, ".")), nil
	}, scope, "macos-user", "the home-overlay installer cannot prove it is a file it delivered before, so installing the user config there could overwrite it",
		"It was not moved aside for you because it shares its data with another hard link or belongs to another user.")
	return err
}

// priorInheritedDelivery proves that rel is the unchanged, sealed source file hardlinked into
// the sidecar by an earlier installer run. A writable receipt or matching bytes alone is not proof.
func (o *Options) priorInheritedDelivery(wsState, rel string) ([]byte, bool) {
	rootPath := inheritedScopeStagingRoot()
	probes := o.MacosUserLaunchProbes
	if probes == nil {
		probes = macosuser.RealLaunchProbes
	}
	launchProbes := probes()
	if launchProbes.Geteuid == nil || launchProbes.StagingRoot == nil {
		return nil, false
	}
	euid := launchProbes.Geteuid()
	if macosuser.JudgeStagingRoot(launchProbes.StagingRoot(rootPath), euid) != macosuser.StagingRootOK {
		return nil, false
	}
	root, err := paths.OpenStateDirRoot(rootPath)
	if err != nil {
		return nil, false
	}
	defer root.Close()
	category, err := paths.OpenStateSubdirRoot(root, "home-overlay", filepath.Join(rootPath, "home-overlay"))
	if err != nil {
		return nil, false
	}
	defer category.Close()
	if info, err := category.Stat("."); err != nil || info.Mode().Perm() != 0o755 {
		return nil, false
	}
	cname := yoloruntime.FromWorkspace(o.Workspace)
	tree, err := paths.OpenStateSubdirRoot(category, cname, filepath.Join(rootPath, "home-overlay", cname))
	if err != nil {
		return nil, false
	}
	defer tree.Close()
	if info, err := tree.Stat("."); err != nil || info.Mode().Perm() != 0o555 {
		return nil, false
	}
	clean := filepath.Clean(strings.TrimLeft(filepath.FromSlash(rel), "/"))
	if clean == "." || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return nil, false
	}
	parts := strings.Split(clean, string(filepath.Separator))
	parent := tree
	opened := []*os.Root{}
	defer func() {
		for _, r := range opened {
			_ = r.Close()
		}
	}()
	for _, part := range parts[:len(parts)-1] {
		next, err := paths.OpenStateSubdirRoot(parent, part, filepath.Join(rootPath, "home-overlay", cname, filepath.Join(parts[:len(opened)+1]...)))
		if err != nil {
			return nil, false
		}
		if info, err := next.Stat("."); err != nil || info.Mode().Perm() != 0o555 {
			_ = next.Close()
			return nil, false
		}
		opened = append(opened, next)
		parent = next
	}
	sourceName := parts[len(parts)-1]
	sourceInfo, err := parent.Lstat(sourceName)
	if err != nil || !sourceInfo.Mode().IsRegular() || sourceInfo.Mode().Perm() != 0o444 || !inheritedFileOwnedBy(sourceInfo, euid) {
		return nil, false
	}
	source, err := parent.OpenFile(sourceName, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer source.Close()
	openedInfo, err := source.Stat()
	if err != nil || !os.SameFile(sourceInfo, openedInfo) || openedInfo.Mode().Perm() != 0o444 {
		return nil, false
	}
	body, err := io.ReadAll(source)
	if err != nil {
		return nil, false
	}
	destInfo, exists, err := lstatHomeFile(wsState, filepath.FromSlash(strings.TrimPrefix(rel, ".")))
	if err != nil || !exists || destInfo.Mode().Perm() != 0o444 || !os.SameFile(sourceInfo, destInfo) {
		return nil, false
	}
	return body, true
}

func inheritedFileOwnedBy(info os.FileInfo, uid int) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && uid >= 0 && uint32(uid) == stat.Uid
}

// removeStaleInheritedScope removes only the generated user-scope files a previous macos-user
// launch installed in sidecar (the workspace's <ws>/.yolo/home, which the sandbox's ~/.config
// links into at `config`). Anything else at either format path is an agent's own and is kept.
//
// BENEATH THE SIDECAR'S ROOT, never by path (openStateRoot): the sidecar is written by the agent,
// so a link it planted above the file would otherwise take the removal to whatever it names.
// Best-effort: a stale file left behind costs a stale user scope, never a launch.
//
// ⚠ RESIDUAL: stale cleanup still recognizes only the legacy generated header for an undelivered
// file. This slice preserves a destination only when its unchanged sealed hardlink is proven; it
// does not extend ownership proof or alter cleanup for old header-only artifacts.
func removeStaleInheritedScope(sidecar string, preserve map[string]bool) {
	// No sidecar, no files: and openStateRoot would create the directories it opens.
	if _, err := os.Lstat(sidecar); err != nil {
		return
	}
	r, err := openStateRoot(sidecar)
	if err != nil {
		return
	}
	defer r.Close()
	for _, candidate := range []struct {
		rel    string
		marker string
	}{
		{inheritPreflightLegacyRel, inheritHeaderFirstLine()},
		{inheritPreflightRel, inheritTOMLHeaderFirstLine()},
	} {
		rel := filepath.FromSlash(strings.TrimPrefix(candidate.rel, "."))
		if preserve[rel] {
			continue
		}
		if fi, err := r.Lstat(rel); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		f, err := r.Open(rel)
		if err != nil {
			continue
		}
		first, _ := bufio.NewReader(f).ReadString('\n')
		_ = f.Close()
		if strings.TrimRight(first, "\n") == candidate.marker {
			_ = r.Remove(rel)
		}
	}
}

// inheritHeaderFirstLine is the first line of a generated legacy JSONC user-scope file.
// header's own renderer so the two cannot disagree.
func inheritHeaderFirstLine() string {
	line, _, _ := strings.Cut(config.InheritHeader(config.InheritPreflight, ""), "\n")
	return line
}

func inheritTOMLHeaderFirstLine() string {
	line, _, _ := strings.Cut(config.InheritTOMLHeader(config.InheritPreflight, ""), "\n")
	return line
}

// effectiveConfigForInherit returns the config the generated files are rendered FROM.
//
// It re-reads through config's own composer rather than taking the run pipeline's already-loaded
// map as a parameter, for one reason: this is the computation `yolo config dump` renders, less
// one layer (below), and going through the same function is what keeps them from diverging. It
// is also where a --user-layer (config.UserLayerPath) is folded in, so the recursion property
// (R6) comes for free — at depth 2 the effective config already contains what depth 1
// inherited plus any layer it was passed.
//
// A load failure is returned, and the launch refuses: the run pipeline has ALREADY loaded and
// validated this config (loadAndValidateConfig, strict), so a failure here means the file
// changed underneath the launch, and rendering an empty inner scope instead would start a jail
// that silently lacks the user config the host believes it delivered.
//
// LESS THE PER-WORKSPACE FILE (config.LoadConfigWithoutWorkspaceFile), the one difference from
// `yolo config dump`'s computation, and deliberate: that file's switches are for THIS workspace
// alone, keyed by a host path no jail has, and the inherited files are the jail's user scope,
// which applies to every workspace a launch inside the jail opens. Inherited, a brokered
// loophole switched on here would also reach the inner user scope, which refuses it
// (docs/design/boundary-broker.md BB-D56).
//
// The provenance comes with it (config.LoadConfigWithoutWorkspaceFileWithSources), so each file
// records which host file set each key and a refusal in the jail names that file.
func (o *Options) effectiveConfigForInherit() (*jsonx.OrderedMap, *config.Sources, error) {
	return config.LoadConfigWithoutWorkspaceFileWithSources(o.Workspace, false, func(string) {})
}

// inheritStamp is the human-readable launch time for the generated files' headers. It uses
// the injected clock so a golden test can pin the bytes.
func (o *Options) inheritStamp() string {
	now := o.Now
	if now == nil {
		return ""
	}
	return now().UTC().Format("2006-01-02T15:04:05Z")
}

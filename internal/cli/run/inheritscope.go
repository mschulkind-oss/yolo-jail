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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/configmigrate"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
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
	// omissions are the legacy-written values a TOML delivery left out (LC-D4), disclosed once
	// per launch by inheritedScopeWithSelector.
	omissions []config.InheritOmission
	keys      int
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
		delivery, rerr := config.RenderInheritedDelivery(filtered, src, scope, launchedAt, configmigrate.LowerInheritedUserScope)
		if rerr != nil {
			return rerr
		}
		body, format, fallbackReason, omissions := delivery.Body, delivery.Format, delivery.FallbackReason, delivery.Omissions
		if selectLegacy != nil {
			forceLegacy, selectErr := selectLegacy(scope, format)
			if selectErr != nil {
				return selectErr
			}
			if forceLegacy && format != configdoc.JSONC {
				body, rerr = config.RenderFilteredWithOrigins(filtered, src, scope, launchedAt)
				if rerr != nil {
					return fmt.Errorf("a legacy JSON or JSONC file already sits at ~/%s in this jail's home, which pins the "+
						"scope to JSONC, but %w. Review that file in the workspace's .yolo/home, move it aside, then relaunch",
						InheritedScopeRel(scope, configdoc.JSONC), rerr)
				}
				format, omissions = configdoc.JSONC, nil // the JSONC route carries every value
				fallbackReason = "a legacy JSON or JSONC file that is not an empty placeholder already sits at ~/" +
					InheritedScopeRel(scope, configdoc.JSONC) + " (or its .json sibling) in this jail's home; review it in the " +
					"workspace's .yolo/home and move it aside, and the next launch delivers TOML"
			}
		}
		rel := InheritedScopeRel(scope, format)
		files = append(files, inheritScopeFile{scope: scope, rel: rel, body: body, format: format, fallbackReason: fallbackReason, omissions: omissions, keys: filtered.Len()})
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
	for _, line := range inheritOmissionLines(files) {
		out.print("[yellow]" + richtext.Escape(line) + "[/yellow]")
	}
	return files, nil
}

// inheritOmissionLines is the one disclosure line per value a TOML delivery left out of the
// jail's user scope (LC-D4): each named once, however many of the scope files left it out, with
// the host file and line that wrote it, what the jail does without it, and the step where one
// is named. Nothing refuses, and nothing else is required of the user.
func inheritOmissionLines(files []inheritScopeFile) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		for _, o := range f.omissions {
			if key := o.Path + "\x00" + o.Where; !seen[key] {
				seen[key] = true
				out = append(out, "The jail's inherited user config leaves out "+o.String()+".")
			}
		}
	}
	return out
}

// selectInheritedJSONCRouteSkipping reports whether an existing legacy file at scope's
// destination pins it to the JSONC route, refusing a destination it cannot keep. Every
// storage-relative path in gone is judged absent: a dry run's plan for an empty placeholder a
// launch moves aside first (clearPodmanPlaceholders).
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
		legacyStorageRel, err := mapDestination(legacyRel)
		if err != nil {
			return false, err
		}
		legacyPath := filepath.Join(wsState, legacyStorageRel)
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

// inheritScopeDestinations is every destination scope's file may take: the legacy JSONC, its
// JSON spelling, and the TOML.
func inheritScopeDestinations(scope config.InheritScope) []string {
	legacy, toml := inheritScopeFamily(scope)
	return []string{legacy, strings.TrimSuffix(legacy, ".jsonc") + ".json", toml}
}

func refuseExistingScopeDestination(wsState string, mapDestination func(string) (string, error), scope config.InheritScope, backend, overwriteRisk, emptyNote string) (bool, error) {
	return refuseExistingScopeDestinationSkipping(wsState, mapDestination, scope, backend, overwriteRisk, emptyNote, nil)
}

// refuseExistingScopeDestinationSkipping is refuseExistingScopeDestination judging every
// storage-relative path in skip absent: a file yolo proved its own (appleContainerOwnCopies).
func refuseExistingScopeDestinationSkipping(wsState string, mapDestination func(string) (string, error), scope config.InheritScope, backend, overwriteRisk, emptyNote string, skip map[string]bool) (bool, error) {
	var existing []string
	for _, rel := range inheritScopeDestinations(scope) {
		storageRel, err := mapDestination(rel)
		if err != nil {
			return false, err
		}
		if skip[storageRel] {
			continue
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
	mapDestination := func(rel string) (string, error) {
		return mappedHomePath(in.wsState, homeBinds, "/home/agent/"+rel)
	}
	selectLegacy := func(scope config.InheritScope, format configdoc.Format) (bool, error) {
		if in.rt == "container" { // parity: Refused — Apple Container cannot cover an existing HOME file without overwriting it.
			// Its own previous copy, unchanged, is not in the way (appleContainerOwnCopies); anything
			// else at a destination is the user's and still refuses.
			own, err := o.appleContainerOwnCopies(in.wsState, mapDestination, scope)
			if err != nil {
				return false, err
			}
			if _, err := refuseExistingScopeDestinationSkipping(in.wsState, mapDestination, scope,
				"Apple Container", "the whole HOME bind would overwrite it", "Reviewing it is quick.", own); err != nil {
				return false, err
			}
			return false, o.clearAppleContainerOwnCopies(in.wsState, mapDestination, scope, format, own)
		}
		// An earlier launch's single-file bind left an empty file at the destination it used. It
		// holds no data, so one of the other format is moved aside rather than read as a legacy
		// sibling: read as one, it kept a host config converted to TOML crossing as JSONC.
		legacy, toml := inheritScopeFamily(scope)
		inTheWay := []string{toml}
		if format == configdoc.TOML {
			inTheWay = []string{legacy, strings.TrimSuffix(legacy, ".jsonc") + ".json"}
		}
		gone := o.clearPodmanPlaceholders(in.wsState, mapDestination, inTheWay...)
		return selectInheritedJSONCRouteSkipping(in.wsState, mapDestination, scope, format, gone)
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
		stagedName := inheritStageName(f.scope, f.rel)
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
// EVERY DESTINATION IS JUDGED WHERE THIS LAUNCH'S HOME KEEPS IT (macosUserInheritedStorage):
// <sidecar>/config in account mode, where ~/.config links into the sidecar, but
// <sidecar>/.config in run-as-you mode, where HOME is the sidecar itself. Judging the account
// path in self mode found nothing, left the previous delivery in place, and the installer then
// refused the relaunch.
//
// AND THE ONE IT DID NOT RENDER, removed (removeStaleInheritedScope), on a launch only. A
// container's bind leaves nothing behind when it is not made; this backend's copy stays in the
// sidecar, so a user config that lost its last inheritable key would otherwise keep serving the
// previous launch's user scope to every in-sandbox reader. A plan render removes nothing.
func (o *Options) macosUserInheritedScope(rt string) ([]overlayFile, error) {
	var core []overlayFile
	wsState := o.homeDir()
	storage := o.macosUserInheritedStorage
	preserve := map[string]bool{}
	// delivered holds each admitted previous delivery's bytes; recorded, those the delivery record
	// lists, the only ones a changed body may replace.
	delivered := map[string][]byte{}
	recorded := map[string]bool{}
	cleared := false
	var gone map[string]bool
	selectLegacy := func(scope config.InheritScope, format configdoc.Format) (bool, error) {
		if scope == config.InheritPreflight && !cleared {
			// Issue #66: an empty file a podman launch's bind left here holds no data; move it
			// aside (or, in a dry run, plan as though it were) before judging the destination.
			cleared, gone = true, o.clearPodmanPlaceholders(wsState, storage, inheritScopeDestinations(config.InheritPreflight)...)
		}
		forceLegacy, err := selectInheritedJSONCRouteSkipping(wsState, storage, scope, format, gone)
		if err != nil {
			return false, err
		}
		legacy, toml := inheritScopeFamily(scope)
		selected := toml
		if forceLegacy || format != configdoc.TOML {
			selected = legacy
		}
		selectedStorage, err := storage(selected)
		if err != nil {
			return false, err
		}
		var existing []string
		for _, rel := range []string{legacy, strings.TrimSuffix(legacy, ".jsonc") + ".json", toml} {
			storageRel, err := storage(rel)
			if err != nil {
				return false, err
			}
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
			return false, refuseInheritedOverwrite(wsState, storage, scope)
		}
		body, admitted, listed := o.admitPriorInheritedDelivery(wsState, selected)
		if !admitted {
			return false, refuseInheritedOverwrite(wsState, storage, scope)
		}
		preserve[selectedStorage] = true
		delivered[selected] = body
		recorded[selected] = listed
		return forceLegacy, nil
	}
	files, err := o.inheritedScopeWithSelector(rt, false, selectLegacy)
	if err != nil {
		return nil, err
	}
	// UNSTAMPED: the home overlay is content-keyed and an unchanged relaunch reuses it whole
	// (buildMacosHomeOverlayTreeIDs). A launch time in this header moved the overlay's digest on
	// every launch, so every relaunch rebuilt, re-staged and re-installed all of it.
	replaceable := map[string][]byte{}
	for _, f := range files {
		if prior, ok := delivered[f.rel]; ok && !bytes.Equal(prior, []byte(f.body)) {
			if !recorded[f.rel] {
				// A hardlink admitted on its inode alone, whose bytes no record lists: only an
				// unchanged one is kept, and nothing proves a changed one may be replaced.
				return nil, refuseInheritedOverwrite(wsState, storage, f.scope)
			}
			replaceable[f.rel] = prior
		}
		core = append(core, overlayFile{Rel: f.rel, Body: []byte(f.body)})
	}
	if !o.DryRun {
		// THE DELIVERY RECORD, written before anything is staged or installed: it is what lets the
		// next launch, and this launch's installer, admit a delivery that is not the staged
		// file's own inode — a copy, or a link the re-staged overlay left behind (issue #70).
		if len(files) > 0 {
			if err := o.recordInheritedDelivery(files, replaceable); err != nil {
				// Without it, a changed body cannot replace the admitted previous delivery, so the
				// installer would refuse it: refuse here instead, naming the cause.
				if len(replaceable) > 0 {
					return nil, fmt.Errorf("could not record the inherited user config this launch delivers (%v), so the changed one cannot replace the previous delivery; run `yolo check` for the staging root, then relaunch", err)
				}
				o.pr(o.Stderr).print("[yellow]Warning: could not record the inherited user config this launch delivers (" +
					err.Error() + "); if the next launch refuses it as a file in the way, run `yolo check` and relaunch[/yellow]")
			}
		}
		// Preserve an admitted previous delivery: unchanged, the installer keeps it; changed, the
		// installer replaces it through the record.
		removeStaleInheritedScope(wsState, storage, preserve)
	}
	return core, nil
}

// macosUserInheritedStorage maps a home-relative inherited destination (".config/yolo-jail/…",
// as the overlay spells it) to the path beneath the workspace sidecar where this launch's sandbox
// home keeps it. It reads the answer off the layout the bootstrap itself lays for this launch's
// mode (macosuser.LaunchHomes), so the mapping is derived in one place rather than spelled here:
// entrypoint.DeriveDarwinHomeLayout links the account's ~/.config to <sidecar>/config, and
// entrypoint.DeriveDarwinWorkspaceHome makes HOME the sidecar, with ~/.config a real `.config`
// in it. A destination the layout does not keep in the sidecar is an error, never a guess.
func (o *Options) macosUserInheritedStorage(rel string) (string, error) {
	sidecar := o.homeDir()
	home := macosuser.LaunchHomes(o.MacosUserMode, o.homeDir(), "").Home
	var layout entrypoint.DarwinHomeLayout
	if filepath.Clean(home) == filepath.Clean(sidecar) {
		layout = entrypoint.DeriveDarwinWorkspaceHome(entrypoint.DarwinHomes{Home: home}, nil, nil)
	} else {
		layout = entrypoint.DeriveDarwinHomeLayout(home, sidecar, nil, nil)
	}
	return darwinSidecarRel(layout, rel)
}

// darwinSidecarRel resolves home-relative rel through layout: beneath the most specific Link
// covering it, to the sidecar subtree that link targets; or, where HOME is the sidecar itself,
// to rel unchanged. The answer is relative to the sidecar and never leaves it.
func darwinSidecarRel(layout entrypoint.DarwinHomeLayout, rel string) (string, error) {
	sidecar := filepath.Clean(layout.Sidecar)
	abs := filepath.Join(layout.Home, filepath.FromSlash(rel))
	within := func(parent, child string) (string, bool) {
		r, err := filepath.Rel(parent, child)
		if err != nil || r == ".." || filepath.IsAbs(r) || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return "", false
		}
		return r, true
	}
	resolved, best := "", -1
	for _, l := range layout.Links {
		suffix, ok := within(filepath.Clean(l.Path), abs)
		if !ok || len(l.Path) <= best {
			continue
		}
		resolved, best = filepath.Join(l.Target, suffix), len(l.Path)
	}
	if best < 0 && sidecar == filepath.Clean(layout.Home) {
		resolved = abs
	}
	if resolved != "" {
		if r, ok := within(sidecar, resolved); ok && r != "." {
			return r, nil
		}
	}
	return "", fmt.Errorf("the macos-user home layout keeps ~/%s outside the workspace's home state %s; preserve your files and report it with this line", rel, sidecar)
}

// inheritedScopeStagingRoot is private so tests can use an isolated staging tree; production
// always asks macos-user for its established root.
var inheritedScopeStagingRoot = macosuser.StagingRoot

func refuseInheritedOverwrite(wsState string, storage func(string) (string, error), scope config.InheritScope) error {
	if err := refuseAppleContainerHome(wsState, storage, scope); err != nil {
		return err
	}
	_, err := refuseExistingScopeDestination(wsState, storage, scope, "macos-user", "the home-overlay installer cannot prove it is a file it delivered before, so installing the user config there could overwrite it",
		"It was not moved aside for you because it shares its data with another hard link or belongs to another user.")
	return err
}

// refuseAppleContainerHome is the refusal for a workspace whose home an Apple Container launch
// laid (macos-user-model-plan.md MMP-D32; the Mac run of 2026-11-03, §5). Since slice 12a both
// backends use <ws>/.yolo/home as HOME with the same dotted layout, and Apple Container COPIES the
// generated user config into it (userConfigMountArgsForHome: writable, yolo's generated header
// first), where macos-user delivers a sealed 0444 file it can prove. Nothing proves that copy
// unedited, so it is not overwritten; and the rest of an Apple Container home (files with no
// sandbox-account access, links to container paths) is not this backend's to repair file by file,
// so the step named is the one that clears it all and keeps it: move the whole home aside. nil
// when no destination of scope holds a copy of that shape.
func refuseAppleContainerHome(wsState string, storage func(string) (string, error), scope config.InheritScope) error {
	uid := os.Geteuid()
	for _, rel := range inheritScopeDestinations(scope) {
		storageRel, err := storage(rel)
		if err != nil {
			continue
		}
		if _, exists, err := lstatHomeFile(wsState, storageRel); err != nil || !exists {
			continue
		}
		path := filepath.Join(wsState, storageRel)
		if !isAppleContainerInheritedCopy(path, uid) {
			continue
		}
		mv := shquote.JoinDisplay([]string{"mv", wsState, wsState + ".apple-container"})
		return fmt.Errorf("refusing macos-user inherited %s scope: %s is the user config an Apple "+
			"Container launch of this workspace copied there (writable, with yolo's generated header), "+
			"so this workspace's home %s was laid by Apple Container, which uses the same directory as "+
			"its home; macos-user cannot prove that copy unedited and will not overwrite it. To run this "+
			"workspace on macos-user, move that home aside, keeping all of it (%s), and relaunch; to "+
			"keep it on Apple Container, launch with `--setup apple-container-self`", scope, path, wsState, mv)
	}
	return nil
}

// isAppleContainerInheritedCopy reports whether path is a generated user-scope file in the shape an
// Apple Container launch leaves it: a regular file of uid's with one link, writable (macos-user's
// own deliveries are sealed 0444), whose first line is yolo's generated header in either format.
// A shape, never a proof: it only chooses which refusal to print.
func isAppleContainerInheritedCopy(path string, uid int) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 == 0 || !inheritedFileOwnedBy(info, uid) {
		return false
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false
	}
	defer f.Close()
	line, err := bufio.NewReader(io.LimitReader(f, 4096)).ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	line = strings.TrimRight(line, "\r\n")
	return line == inheritHeaderFirstLine() || line == inheritTOMLHeaderFirstLine()
}

// isAppleContainerInheritedCopyBeneath is isAppleContainerInheritedCopy of rel below r, a root on a
// jail-writable home: a link at rel, or one on the way that leaves r, is never read.
func isAppleContainerInheritedCopyBeneath(r *os.Root, rel string, uid int) bool {
	info, err := r.Lstat(rel)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o222 == 0 || !inheritedFileOwnedBy(info, uid) {
		return false
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Nlink != 1 {
		return false
	}
	f, err := paths.OpenRegularFileBeneath(r, rel)
	if err != nil {
		return false
	}
	defer f.Close()
	if st, err := f.Stat(); err != nil || !os.SameFile(info, st) {
		return false
	}
	line, err := bufio.NewReader(io.LimitReader(f, 4096)).ReadString('\n')
	if err != nil && err != io.EOF {
		return false
	}
	line = strings.TrimRight(line, "\r\n")
	return line == inheritHeaderFirstLine() || line == inheritTOMLHeaderFirstLine()
}

// inheritedStaging is the host-owned staging root (macosuser.StagingRoot) a launch delivers the
// inherited config through, opened only once it is judged this user's (JudgeStagingRoot): the
// sealed staged overlay and the delivery record (entrypoint.InheritedDeliveryRecordPath) both live
// in it, and the sandbox can write neither — another account's directory in account mode, and a
// read-only grant of the Seatbelt profile in run-as-you mode.
type inheritedStaging struct {
	root     *os.Root
	rootPath string
	cname    string
	euid     int
}

func (s *inheritedStaging) Close() { _ = s.root.Close() }

// openInheritedStaging opens and judges the staging root for this workspace's session.
func (o *Options) openInheritedStaging() (*inheritedStaging, bool) {
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
	return &inheritedStaging{root: root, rootPath: rootPath, cname: yoloruntime.FromWorkspace(o.Workspace), euid: euid}, true
}

// recordLeaf is the delivery record's directory (one level below the root) and file name.
func (s *inheritedStaging) recordLeaf() (path, leaf, name string) {
	path = entrypoint.InheritedDeliveryRecordPath(macosuser.StagedHomeOverlay(s.cname, s.rootPath))
	rel, err := filepath.Rel(s.rootPath, path)
	if err != nil {
		return path, "", ""
	}
	return path, filepath.Dir(rel), filepath.Base(rel)
}

// openRecordLeaf opens the record's directory beneath the root, creating it (0755, this user's)
// when create is set and it is missing; anything but a real 0755 directory of this user's is
// refused.
func (s *inheritedStaging) openRecordLeaf(create bool) (*os.Root, string, error) {
	path, leaf, name := s.recordLeaf()
	if leaf == "" || leaf == "." || strings.Contains(leaf, string(filepath.Separator)) {
		return nil, "", fmt.Errorf("the inherited delivery record %s is not one directory below the staging root", path)
	}
	full := filepath.Join(s.rootPath, leaf)
	if _, err := s.root.Lstat(leaf); os.IsNotExist(err) && create {
		if err := s.root.Mkdir(leaf, 0o755); err != nil && !os.IsExist(err) {
			return nil, "", err
		}
		if err := s.root.Chmod(leaf, 0o755); err != nil {
			return nil, "", err
		}
	}
	dir, err := paths.OpenStateSubdirRoot(s.root, leaf, full)
	if err != nil {
		return nil, "", err
	}
	info, err := dir.Stat(".")
	if err != nil || info.Mode().Perm() != 0o755 || !inheritedFileOwnedBy(info, s.euid) {
		_ = dir.Close()
		return nil, "", fmt.Errorf("%s is not a 0755 directory of yours; remove it (the next launch recreates it) and relaunch", full)
	}
	return dir, name, nil
}

// readRecord reads the delivery record: only a regular, sealed 0444 file of this user's, opened
// without following a link. Anything else is no record, which admits nothing.
func (s *inheritedStaging) readRecord() (entrypoint.InheritedDeliveryRecord, bool) {
	dir, name, err := s.openRecordLeaf(false)
	if err != nil {
		return entrypoint.InheritedDeliveryRecord{}, false
	}
	defer dir.Close()
	info, err := dir.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 || !inheritedFileOwnedBy(info, s.euid) {
		return entrypoint.InheritedDeliveryRecord{}, false
	}
	f, err := dir.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return entrypoint.InheritedDeliveryRecord{}, false
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return entrypoint.InheritedDeliveryRecord{}, false
	}
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return entrypoint.InheritedDeliveryRecord{}, false
	}
	r, err := entrypoint.ParseInheritedDeliveryRecord(data)
	return r, err == nil
}

// writeRecord replaces the delivery record with r, sealed 0444: written beside it under a
// temporary name, sealed, then renamed over it, so a reader sees the old record or the new one.
func (s *inheritedStaging) writeRecord(r entrypoint.InheritedDeliveryRecord) error {
	data, err := r.Marshal()
	if err != nil {
		return err
	}
	dir, name, err := s.openRecordLeaf(true)
	if err != nil {
		return err
	}
	defer dir.Close()
	tmp := name + ".new"
	if err := dir.Remove(tmp); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := dir.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = dir.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	if err := dir.Chmod(tmp, 0o444); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	if err := dir.Rename(tmp, name); err != nil {
		_ = dir.Remove(tmp)
		return err
	}
	return nil
}

// admitPriorInheritedDelivery decides whether the existing inherited destination rel is a delivery
// of yolo's, returning its bytes: the unchanged sealed hardlink of the staged source
// (priorInheritedDelivery), or a sealed file whose content digest the delivery record lists for rel
// (recordedInheritedDelivery) — the copy a refused link made, or a link the re-staged overlay
// orphaned (issue #70).
//
// recorded says the record lists those bytes for rel. Only then may a changed body REPLACE the
// delivery: a hardlink admitted on its inode alone, with bytes no record lists, is kept when
// unchanged and refused when the body changes, as before the record.
func (o *Options) admitPriorInheritedDelivery(wsState, rel string) (body []byte, admitted, recorded bool) {
	if body, ok := o.priorInheritedDelivery(wsState, rel); ok {
		return body, true, o.inheritedRecordLists(rel, body)
	}
	body, ok := o.recordedInheritedDelivery(wsState, rel)
	return body, ok, ok
}

// inheritedRecordLists reports whether the delivery record lists body's digest for rel.
func (o *Options) inheritedRecordLists(rel string, body []byte) bool {
	staging, ok := o.openInheritedStaging()
	if !ok {
		return false
	}
	defer staging.Close()
	record, ok := staging.readRecord()
	return ok && record.Admits(rel, entrypoint.ContentDigest(body))
}

// inheritedSandboxUID is the sandbox account's uid, the owner of a copy the account-mode installer
// made; a var for tests. It asks the directory service (`id -u`), as a CGO-free os/user cannot.
var inheritedSandboxUID = func() (uint32, bool) {
	out, err := exec.Command("/usr/bin/id", "-u", macosuser.SandboxUser).Output()
	if err != nil {
		return 0, false
	}
	n, err := strconv.ParseUint(strings.TrimSpace(string(out)), 10, 32)
	return uint32(n), err == nil
}

// recordedInheritedDelivery admits rel when the delivery record lists the content digest of the
// file at its destination, and that file is regular, sealed 0444, and owned by this user (a hard
// link, or a run-as-you copy) or, in account mode, by the sandbox account (its copy). A header
// match never admits, and neither do matching bytes without a record: the agent can write both.
func (o *Options) recordedInheritedDelivery(wsState, rel string) ([]byte, bool) {
	staging, ok := o.openInheritedStaging()
	if !ok {
		return nil, false
	}
	defer staging.Close()
	record, ok := staging.readRecord()
	if !ok {
		return nil, false
	}
	destRel, err := o.macosUserInheritedStorage(rel)
	if err != nil {
		return nil, false
	}
	destInfo, exists, err := lstatHomeFile(wsState, destRel)
	if err != nil || !exists {
		return nil, false
	}
	root, err := openStateRoot(wsState)
	if err != nil {
		return nil, false
	}
	defer root.Close()
	f, err := root.OpenFile(destRel, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !os.SameFile(destInfo, info) || !info.Mode().IsRegular() || info.Mode().Perm() != 0o444 {
		return nil, false
	}
	owned := inheritedFileOwnedBy(info, staging.euid)
	if !owned && o.MacosUserMode == macosuser.ModeAccount {
		if uid, ok := inheritedSandboxUID(); ok {
			owned = inheritedFileOwnedBy(info, int(uid))
		}
	}
	if !owned {
		return nil, false
	}
	body, err := io.ReadAll(f)
	if err != nil || !record.Admits(rel, entrypoint.ContentDigest(body)) {
		return nil, false
	}
	return body, true
}

// recordInheritedDelivery writes the delivery record for this launch's inherited files: each one's
// rendered digest, plus the digest of the previous delivery the launch admitted there and the old
// record already listed (prior), so the installer may replace that one — and only that one — with
// the new body. It runs before the
// launch stages and installs, so the record already names whatever the installer lays down.
func (o *Options) recordInheritedDelivery(files []inheritScopeFile, prior map[string][]byte) error {
	staging, ok := o.openInheritedStaging()
	if !ok {
		return fmt.Errorf("the staging root %s is not a directory of yours", inheritedScopeStagingRoot())
	}
	defer staging.Close()
	r := entrypoint.InheritedDeliveryRecord{Digests: map[string][]string{}}
	for _, f := range files {
		digests := []string{entrypoint.ContentDigest([]byte(f.body))}
		if body, ok := prior[f.rel]; ok {
			if d := entrypoint.ContentDigest(body); d != digests[0] {
				digests = append(digests, d)
			}
		}
		r.Digests[f.rel] = digests
	}
	return staging.writeRecord(r)
}

// priorInheritedDelivery proves that rel is the unchanged, sealed source file hardlinked into
// the sidecar by an earlier installer run. A writable receipt or matching bytes alone is not proof.
func (o *Options) priorInheritedDelivery(wsState, rel string) ([]byte, bool) {
	staging, ok := o.openInheritedStaging()
	if !ok {
		return nil, false
	}
	defer staging.Close()
	rootPath, root, euid, cname := staging.rootPath, staging.root, staging.euid, staging.cname
	category, err := paths.OpenStateSubdirRoot(root, "home-overlay", filepath.Join(rootPath, "home-overlay"))
	if err != nil {
		return nil, false
	}
	defer category.Close()
	if info, err := category.Stat("."); err != nil || info.Mode().Perm() != 0o755 {
		return nil, false
	}
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
	destRel, err := o.macosUserInheritedStorage(rel)
	if err != nil {
		return nil, false
	}
	destInfo, exists, err := lstatHomeFile(wsState, destRel)
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
// launch installed in sidecar (the workspace's <ws>/.yolo/home), at the path storage maps each
// one to (macosUserInheritedStorage). Anything else at either format path is an agent's own and
// is kept.
//
// BENEATH THE SIDECAR'S ROOT, never by path (openStateRoot): the sidecar is written by the agent,
// so a link it planted above the file would otherwise take the removal to whatever it names.
// Best-effort: a stale file left behind costs a stale user scope, never a launch.
//
// ⚠ RESIDUAL: stale cleanup still recognizes only the legacy generated header for an undelivered
// file. A destination is preserved only when admitted (admitPriorInheritedDelivery: the unchanged
// sealed hardlink, or bytes the delivery record lists); cleanup of old header-only artifacts is
// unchanged.
func removeStaleInheritedScope(sidecar string, storage func(string) (string, error), preserve map[string]bool) {
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
		rel, err := storage(candidate.rel)
		if err != nil || preserve[rel] {
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

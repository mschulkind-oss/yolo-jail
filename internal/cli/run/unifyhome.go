package run

// unifyhome.go is the launch's half of the unified workspace home
// (docs/design/unified-workspace-home.md §5.2, §9; unified-workspace-home-plan.md U5, UHP-D6): once the
// launch is committed and the slice's directories are real (prepareWsState), it
//
//  1. merges every OTHER home of the workspace into the unified home, when no jail of the
//     workspace runs (the legacy home, and every other setup's slice), and
//  2. composes the launching setup's own slice: each shared child moves into the unified home and a
//     link takes its place; each unified child the slice lacks gets a link.
//
// Only a launch merges; there is no verb. Nothing is deleted or overwritten, nothing here refuses the
// launch, and one informational line says what arrived (UHP-D8). The pure work is
// internal/unifiedhome; this file decides WHAT is shared: the default is shared, and a path stays in
// its slice only because a selected pack renders it, installs it, or carves it out (UHP-D9).

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/mschulkind-oss/yolo-jail/internal/basehome"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/unifiedhome"
)

// jailUnifiedHome is where the jail sees the unified home, on both container backends: the
// workspace is at /workspace, so every link target is this absolute path (§5.2).
const jailUnifiedHome = "/workspace/.yolo/" + paths.UnifiedHomeName

// unifyHomeUID is the invoking user, whose homes the merge takes; a test stands another in.
var unifyHomeUID = os.Geteuid

// unifyHomeDevice is the device a path lives on, for the "its own mount" refusal: a rename cannot
// cross one. A test stands in a second device.
var unifyHomeDevice = func(p string) (uint64, bool) {
	var st unix.Stat_t
	if err := unix.Lstat(p, &st); err != nil {
		return 0, false
	}
	return uint64(st.Dev), true
}

// stayPaths is, per workspace-scope state directory D of the SELECTED packs, the paths beneath D
// (relative to it, slash separated) that stay in each slice and are never shared (UHP-D9): what a
// selected pack renders (config surfaces, skills, briefing and file destinations), the credential
// links its hooks lay, the redirect targets core lays, the installed program payloads, and the
// pack's own `per_setup_state` carve-outs. Core names no agent: every entry is read from a
// declaration of a selected pack or from core's own fixed lists.
func stayPaths(packs []*packload.Pack) map[string][]string {
	decls := basehome.DeclsFromPacks(packs)
	var homeRel []string
	homeRel = append(homeRel, decls.ConfigSurfaces...)
	homeRel = append(homeRel, decls.ContentDests...)
	homeRel = append(homeRel, decls.CredentialFiles...)
	for _, r := range paths.HomeFileRedirects() {
		homeRel = append(homeRel, filepath.ToSlash(r.Target))
	}
	for _, s := range paths.InstalledProgramSurfaces() {
		homeRel = append(homeRel, filepath.ToSlash(s.HomeRel))
	}
	out := map[string][]string{}
	for _, dir := range packload.WritableDirs(packs) {
		d := strings.Trim(filepath.ToSlash(dir), "/")
		seen := map[string]bool{}
		add := func(rel string) {
			if rel != "" && !seen[rel] {
				seen[rel] = true
				out[dir] = append(out[dir], rel)
			}
		}
		for _, rel := range homeRel {
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, d+"/") {
				add(strings.TrimPrefix(rel, d+"/"))
			}
		}
		for _, p := range packs {
			if p == nil || p.Decl == nil {
				continue
			}
			for _, c := range p.Decl.PerSetupState() {
				if strings.Trim(filepath.ToSlash(c.Dir), "/") == d {
					for _, rel := range c.Paths {
						add(rel)
					}
				}
			}
		}
		sort.Strings(out[dir])
	}
	return out
}

// stateSpellings are the two spellings a state directory takes on disk: the dotted one Apple Container
// and the guest keep, and the dot-stripped one Podman binds (§5.1).
func stateSpellings(dir string) []string {
	if stripped := strings.TrimPrefix(dir, "."); stripped != dir {
		return []string{dir, stripped}
	}
	return []string{dir}
}

// homeLabel names a workspace home for a line and for the ".from-" names: "legacy" for
// <ws>/.yolo/home, else the setup's name.
func homeLabel(workspace, home string) string {
	rel, ok := paths.HomeStateRel(workspace, home)
	if !ok || rel == "home" {
		return "legacy"
	}
	return strings.Split(rel, "/")[1]
}

// unifyTally adds up what one launch moved, for the one line it prints (UHP-D8).
type unifyTally struct {
	files, homes, keptBoth int
}

func (t *unifyTally) add(r unifiedhome.Report) {
	if r.Moved > 0 {
		t.files += r.Moved
		t.keptBoth += r.KeptBoth
	}
}

// unifyHome is the launch's merge and composition (UHP-D6): called by runContainer straight after
// prepareWsState and before deliverChannel. It never refuses the launch: whatever it cannot do it
// leaves, says so with the step that clears it, and a later launch finishes. It touches container
// setups only (UHP-D3) and never a fresh home (it shares nothing) or a dry run.
func (o *Options) unifyHome(cfg *jsonx.OrderedMap, wsState string, packs []*packload.Pack, rt string) {
	if !runsAsContainer(o.Setup) || o.FreshHome || o.DryRun {
		return
	}
	dirs := packload.WritableDirs(packs)
	if len(dirs) == 0 {
		return // no selected pack declares a state directory: nothing is composed
	}
	out := o.pr(o.Stderr)
	say := func(format string, a ...any) {
		out.print("[dim]" + richtext.Escape(fmt.Sprintf(format, a...)) + "[/dim]")
	}
	unifiedPath := paths.UnifiedHome(o.Workspace)
	for _, rel := range cfgStrList(cfg, "workspace_readonly") {
		if isUnderOrEqual(unifiedPath, filepath.Clean(filepath.Join(o.Workspace, rel))) {
			say("`workspace_readonly` entry %q makes %s read-only, so this setup keeps a private home and shares "+
				"its sessions with no other setup; remove that entry to share them.", rel, unifiedPath)
			return
		}
	}
	unified, err := o.openUnifiedHome()
	if err != nil {
		say("%v; this setup keeps a private home and shares its sessions with no other setup until it is fixed.", err)
		return
	}
	defer unified.Close()
	own, err := paths.OpenWorkspaceHomeRoot(o.Workspace, wsState)
	if err != nil {
		say("This setup's home %s could not be opened (%v), so it is not shared with the workspace's other setups.", wsState, err)
		return
	}
	defer own.Close()

	stay := stayPaths(packs)
	var tally unifyTally
	opts := unifiedhome.Options{Rename: legacyHomeRename}
	var failures []error

	// 1. Every OTHER home, once no jail of the workspace runs.
	o.mergeOtherHomes(unified, dirs, stay, &tally, &failures, say)

	// 2. The launching slice: the spelling its backend does not read (a Podman slice's dotted
	// directory, an Apple Container slice's dot-stripped one), then composition proper.
	sliceMoved := 0
	for _, dir := range dirs {
		var off []string
		for _, sp := range stateSpellings(dir) {
			if sp != filepath.ToSlash(wsStateHomeRel(rt, dir)) {
				off = append(off, sp)
			}
		}
		if len(off) > 0 {
			rep, err := unifiedhome.Merge(unified, dir, unifiedhome.Source{
				Label: o.Setup.Name, Root: own, Dirs: off, Stay: stay[dir]}, opts)
			tally.add(rep)
			sliceMoved += rep.Moved
			failures = append(failures, err)
		}
	}
	slice := unifiedhome.ComposeSpec{Label: o.Setup.Name, HomePath: jailUnifiedHome}
	for _, dir := range dirs {
		spec := slice
		spec.Generated = stay[dir]
		rep, err := unifiedhome.Compose(own, filepath.ToSlash(wsStateHomeRel(rt, dir)), unified, dir, spec, opts)
		tally.add(rep.Report)
		sliceMoved += rep.Moved
		failures = append(failures, err)
		if rep.Declined {
			say("%s", rep.DeclinedLine)
			break
		}
	}
	if sliceMoved > 0 {
		tally.homes++ // the launching slice is one home however many directories moved
	}

	if tally.files > 0 {
		line := fmt.Sprintf("Merged %d files from %d homes into this workspace's shared home.", tally.files, tally.homes)
		if tally.keptBoth > 0 {
			line += fmt.Sprintf(" %d kept twice (<name>.from-<setup>).", tally.keptBoth)
		}
		out.print(richtext.Escape(line))
	}
	if err := errors.Join(failures...); err != nil {
		say("Some of this workspace's sessions could not be moved into its shared home (%v); nothing was lost, "+
			"and the next launch tries again.", err)
	}
}

// openUnifiedHome makes the unified home and opens it as a root that refuses a link.
func (o *Options) openUnifiedHome() (*os.Root, error) {
	f, err := paths.EnsureUnifiedHome(o.Workspace)
	if err != nil {
		var linked *paths.LinkedStateDirError
		if errors.As(err, &linked) {
			return nil, fmt.Errorf("%s is a symbolic link, which yolo never writes beneath. Remove it with `%s` and launch again",
				linked.Path, shquote.JoinDisplay([]string{"rm", linked.Path}))
		}
		return nil, fmt.Errorf("the unified home %s could not be made (%v)", paths.UnifiedHome(o.Workspace), err)
	}
	f.Close()
	return paths.OpenUnifiedHomeRoot(o.Workspace)
}

// mergeOtherHomes merges the legacy home and every other setup's slice into the unified home, or
// says which wait and why.
func (o *Options) mergeOtherHomes(unified *os.Root, dirs []string, stay map[string][]string,
	tally *unifyTally, failures *[]error, say func(string, ...any)) {
	own := o.homeDir()
	var others []string
	for _, home := range paths.WorkspaceHomeStates(o.Workspace) {
		if filepath.Clean(home) != filepath.Clean(own) {
			others = append(others, home)
		}
	}
	if len(others) == 0 {
		return
	}
	_, user, _ := config.SelectedSetupFromConfig()
	var waiting []string
	state := projectJailsStopped
	stateKnown := false
	for _, home := range others {
		label := homeLabel(o.Workspace, home)
		root, err := paths.OpenWorkspaceHomeRoot(o.Workspace, home)
		if err != nil {
			var linked *paths.LinkedStateDirError
			if errors.As(err, &linked) {
				say("%s is a symbolic link, so the %s home was left in place. Remove the link and launch again: `%s`",
					linked.Path, label, shquote.JoinDisplay([]string{"rm", linked.Path}))
			}
			continue
		}
		if !holdsSharedState(root, dirs) {
			root.Close()
			continue
		}
		if label != "legacy" {
			if s, err := setup.Lookup(label, user); err == nil && !runsAsContainer(s) {
				say("The %s setup's home is a guest's, which joins this workspace's shared home in a later release, so its "+
					"sessions stay where they are.", label)
				root.Close()
				continue
			}
		}
		if !stateKnown {
			state, stateKnown = o.projectJails(), true
		}
		if state != projectJailsStopped {
			waiting = append(waiting, label)
			root.Close()
			continue
		}
		o.mergeOneHome(unified, root, home, label, dirs, stay, tally, failures, say)
		root.Close()
	}
	if len(waiting) > 0 {
		why := "a jail of this project is running"
		if state == projectJailsUnknown {
			why = "yolo could not ask the container runtime whether a jail of this project is running"
		}
		say("The %s home waits to merge into this workspace's shared home: %s. Run `yolo stop` and launch again to merge it.",
			strings.Join(waiting, " and "), why)
	}
}

// holdsSharedState reports whether root holds any of the state directories, in either spelling.
func holdsSharedState(root *os.Root, dirs []string) bool {
	for _, dir := range dirs {
		for _, sp := range stateSpellings(dir) {
			if fi, err := root.Lstat(sp); err == nil && (fi.IsDir() || fi.Mode()&fs.ModeSymlink != 0) {
				return true
			}
		}
	}
	return false
}

// mergeOneHome merges one stopped home's shared children, after the refusals §9.2 lists: a home
// that is its own mount, or another user's, stays with the step that clears it.
func (o *Options) mergeOneHome(unified, root *os.Root, home, label string, dirs []string, stay map[string][]string,
	tally *unifyTally, failures *[]error, say func(string, ...any)) {
	uDev, uok := unifyHomeDevice(paths.UnifiedHome(o.Workspace))
	if hDev, hok := unifyHomeDevice(home); uok && hok && hDev != uDev {
		say("The %s home %s is its own mount, so yolo cannot rename out of it and left it in place. Move what you want to keep "+
			"yourself while no jail of the project runs: `%s`", label, home,
			shquote.JoinDisplay([]string{"mv", "-n", path.Join(home, stateSpellings(dirs[0])[0]), paths.UnifiedHome(o.Workspace)}))
		return
	}
	uid := uint32(unifyHomeUID())
	opts := unifiedhome.Options{Rename: legacyHomeRename, OwnerUID: &uid}
	moved := 0
	var foreign bool
	for _, dir := range dirs {
		rep, err := unifiedhome.Merge(unified, dir, unifiedhome.Source{
			Label: label, Root: root, Dirs: stateSpellings(dir), Stay: stay[dir]}, opts)
		tally.add(rep)
		moved += rep.Moved
		*failures = append(*failures, err)
		for _, l := range rep.Left {
			foreign = foreign || l.Reason == "not owned by the user"
		}
	}
	if moved > 0 {
		tally.homes++
	}
	if foreign {
		say("The %s home %s belongs to another user, so it was left in place. Give it to you and launch again with no jail "+
			"of the project running: `%s`", label, home,
			shquote.JoinDisplay([]string{"sudo", "chown", "-R", fmt.Sprint(unifyHomeUID()), home}))
	}
}

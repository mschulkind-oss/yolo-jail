package run

// packtree.go is ONE IMMUTABLE PACK TREE PER LAUNCH, the maintainer's OQ-PK2 ruling, option (c)
// (docs/reference/pack-system.md#oq-pk2, 2026-09-26).
//
// A PACK TREE is the staged copy of every pack one launch selected: <tree>/_official/<name> for
// the embedded packs and <tree>/<slug> for configured ones, plus the tree's own record (below).
// Every launch stages a NEW tree under paths.PackTreeRoot(cname) and nothing edits a tree once
// it is staged:
//
//   - a podman jail binds its launch's tree :ro at /ctx/packs, Apple Container copies it into
//     the jail's home at the fresh launch, and the macos-user bootstrap copies it for the
//     sandbox. The launch's host daemons start from the loophole module dirs inside it.
//   - AN ATTACH NEVER RE-STAGES, not even by diff-sync. It stages the config's packs into a
//     tree of its own, which nothing binds, to compare and to run the pre-flights against,
//     and then discards it. Every host-side reader on the attach (the channel, the launch
//     flags, the skills and briefing refresh, the loophole record) reads the RUNNING jail's
//     tree instead (runningJailPackView), and when the two trees differ the attach says so
//     and says a restart picks the change up (noteBootedPackSetDiffers). A jail tree that will
//     not load, or whose packs cannot serve the selection, is not read around: the attach takes
//     the contract gate's disposition (attachPackSkew, settleAttachSkew).
//   - A TREE GOES ONLY ONCE ITS CONTAINER IS KNOWN GONE: the launch that started the container
//     removes it at the three ends where it sees the container go (forgetGoneContainer), on
//     the runtime's answer that no container of that name exists. "Could not ask" removes
//     nothing. A launch killed without a teardown leaves its tree to the reaper that already
//     removes a gone jail's whole AGENTS_DIR/<cname> (prune.PruneOrphanAgentStaging). That is
//     the home skeleton's lifecycle (docs/design/base-home-legacy-state.md#OQ-BH10, OQ-BH16),
//     and it is the precedent the ruling names.
//
// WHAT IT REPLACED. Every launch, an attach included, used to re-stage one shared tree,
// AGENTS_DIR/<cname>/packs, by sync. So a running jail's /ctx/packs showed whatever the config
// said at the last entry, and an attach by a newer host handed an older jail pack contracts its
// binaries could not read: v0.10.0's boot refuses claude's list-valued api_key_env_name and
// pi's two newer hooks (packs/releasedecode_test.go). A jail launched before this change still
// binds that shared tree (paths.LegacyPackStagingDir), so nothing writes it any more: an attach
// reads it, and the first fresh container launch that finds no container of the workspace's
// name retires it (retireLegacyPackStaging).

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packTreeRecordName is the tree's own record (packload.PackTreeRecordName): which directory
// holds which pack, in the one precedence order the launch loaded them in (OQ-NC4). At the
// tree's top level, where the jail's fallback walk skips every non-directory, so it renders as
// nothing.
//
// It exists for the two readers that rebuild the pack set a launch composed: the jail's boot
// (entrypoint.LoadJailPacks), which takes its ORDER, and the attach, which takes the order and
// the NAMES (a configured pack's directory is its slug, and a briefing's section label, a
// retirement record and the profile disclosure all use the pack's name). Neither can be
// recovered from the directories alone. `.yolo-pack-tree.json` was its name once, and a pack of
// that name took the record's place and refused the launch; packload says why this one cannot.
const packTreeRecordName = packload.PackTreeRecordName

// packTreeTimeLayout prefixes each tree's directory name, so a human listing the root can tell
// the launches apart. os.MkdirTemp's random suffix is what makes the name unique.
const packTreeTimeLayout = "20060102T150405Z"

// newPackTree creates a NEW, empty pack tree for this launch under paths.PackTreeRoot(cname).
func newPackTree(cname string) (string, error) {
	root := paths.PackTreeRoot(cname)
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", fmt.Errorf("packs: cannot create the pack tree root %s: %w", root, err)
	}
	dir, err := os.MkdirTemp(root, time.Now().UTC().Format(packTreeTimeLayout)+"-")
	if err != nil {
		return "", fmt.Errorf("packs: cannot create a pack tree under %s: %w", root, err)
	}
	// os.MkdirTemp creates 0700. The shared tree this replaces was 0755, and on podman the jail
	// reads it through a bind as whatever uid the entrypoint drops to.
	if err := os.Chmod(dir, 0o755); err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("packs: cannot set the mode of the pack tree %s: %w", dir, err)
	}
	return dir, nil
}

// writePackTreeRecord writes root's record from the packs a launch loaded out of it, in the
// precedence order they were loaded in (config.PackSelection.Packs).
func writePackTreeRecord(root string, packs []*packload.Pack) error {
	return packload.WritePackTreeRecord(root, packs)
}

// discardPackTree removes a pack tree no container holds: one this launch staged and then
// started no container on, or attached instead, or one whose container the runtime has answered
// is gone. It removes only a direct child of paths.PackTreeRoot(cname), so a wrong argument
// cannot reach anything else, and a failure leaves the tree to the reaper, so it is not reported.
func discardPackTree(cname, dir string) {
	if dir == "" || filepath.Dir(dir) != paths.PackTreeRoot(cname) {
		return
	}
	_ = os.RemoveAll(dir)
	// And its delivery record, which lives and dies with it (forkhanded.go).
	_ = os.Remove(handedForksPath(dir))
}

// writeLivePackTree records dir as the tree cname's running container booted from
// (paths.LivePackTreeRecord). Called by the fresh container path, holding the launch lock,
// before the container starts, so an attach, which takes the same lock, never finds a running
// container of this launch without the record.
func writeLivePackTree(cname, dir string) error {
	return os.WriteFile(paths.LivePackTreeRecord(cname), []byte(filepath.Base(dir)+"\n"), 0o644)
}

// forgetLivePackTree removes the live-tree record, but only while it still names dir: a later
// launch that restarted the jail has written its own, and a late teardown of the earlier launch
// must not take that away.
func forgetLivePackTree(cname, dir string) {
	if dir == "" {
		return
	}
	raw, err := os.ReadFile(paths.LivePackTreeRecord(cname))
	if err != nil || strings.TrimSpace(string(raw)) != filepath.Base(dir) {
		return
	}
	_ = os.Remove(paths.LivePackTreeRecord(cname))
}

// runningJailPackTree finds the pack tree cname's running jail booted from: the tree the
// live-tree record names, when that tree is still there; else the shared staging tree a jail
// launched before per-launch trees binds, when that is there. "" when neither is, with unfound
// saying why, which the caller reports rather than guesses around.
//
// ON APPLE CONTAINER THE SHARED TREE IS NOT THE BOOTED SET. That backend copied it into the
// jail's home at the fresh launch only, and every attach before per-launch trees re-staged the
// shared tree afterwards, so it holds whatever the config said at the last entry, which need not
// be what the jail copied. Only a live-tree record names an Apple Container jail's tree.
func runningJailPackTree(cname, rt string) (dir, unfound string) {
	if raw, err := os.ReadFile(paths.LivePackTreeRecord(cname)); err == nil {
		name := strings.TrimSpace(string(raw))
		if name != "" && filepath.IsLocal(name) && filepath.Base(name) == name {
			tree := filepath.Join(paths.PackTreeRoot(cname), name)
			if isDir(tree) {
				return tree, ""
			}
		}
	}
	legacyDir := paths.LegacyPackStagingDir(cname)
	switch {
	case !isDir(legacyDir):
		return "", "no record of it under " + paths.PackTreeRoot(cname)
	case rt == "container": // parity: Warned — AC copied this tree at launch and older attaches re-staged it; the attach says it cannot find the booted tree
		return "", legacyDir + " is the shared tree Apple Container copied from when this jail " +
			"launched, and attaches before per-launch pack trees re-staged it since, so it need not " +
			"hold the packs the jail has"
	}
	return legacyDir, ""
}

// loadPackTree loads the packs a staged tree holds, in the order and under the names its launch
// gave them. A tree with no record is one a launch before per-launch trees staged; it is read
// the way the jail's own loader reads it (_official's packs, then the configured ones, each
// directory sorted), with a configured pack named by the config entry whose slug its directory
// carries when there is one.
func loadPackTree(root string) ([]*packload.Pack, error) {
	rec, recorded, err := packload.ReadPackTreeRecord(root)
	if err != nil {
		return nil, err
	}
	if !recorded {
		out, err := loadUnrecordedPackTree(root)
		if err != nil {
			return nil, err
		}
		return packload.ApplyForks(out)
	}
	var out []*packload.Pack
	for _, e := range rec {
		p, problems := packload.LoadDir(filepath.Join(root, filepath.FromSlash(e.Dir)), e.Name)
		if len(problems) > 0 {
			return nil, errors.New(problems[0]) // LoadDir's problems already name the pack
		}
		out = append(out, p)
	}
	// The fork rewrite the jail's own loader runs over this tree (packload.ApplyForks), so a
	// host-side reader of a running jail's packs sees the programs that jail was given.
	return packload.ApplyForks(out)
}

// loadUnrecordedPackTree reads a tree that carries no record: the shared staging a launch before
// per-launch trees wrote.
func loadUnrecordedPackTree(root string) ([]*packload.Pack, error) {
	slugNames := map[string]string{}
	if entries, err := config.LoadPacks(func(string) {}); err == nil {
		for _, e := range entries {
			slugNames[e.Slug()] = e.Name
		}
	}
	var out []*packload.Pack
	for _, dir := range []string{filepath.Join(root, officialStagingDir), root} {
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, ent := range entries {
			if !ent.IsDir() || ent.Name() == officialStagingDir {
				continue
			}
			name := ent.Name()
			if dir == root && slugNames[name] != "" {
				name = slugNames[name]
			}
			p, problems := packload.LoadDir(filepath.Join(dir, ent.Name()), name)
			if len(problems) > 0 {
				return nil, errors.New(problems[0]) // LoadDir's problems already name the pack
			}
			out = append(out, p)
		}
	}
	return out, nil
}

// packContentDigest hashes one pack's staged directory: every entry's path, its type, a file's
// execute bit and its bytes. Two packs with the same digest render the same jail.
func packContentDigest(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			fmt.Fprintf(h, "d %s\n", filepath.ToSlash(rel))
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "l %s %s\n", filepath.ToSlash(rel), target)
		default:
			info, err := d.Info()
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "f %s %t %d\n", filepath.ToSlash(rel), info.Mode().Perm()&0o111 != 0, info.Size())
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(h, f)
			_ = f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// packSetDiff is how the configured packs differ from the ones a running jail booted with, by
// pack name.
type packSetDiff struct {
	// Added are configured packs the jail does not have.
	Added []string
	// Removed are packs the jail has that the config no longer selects.
	Removed []string
	// Changed are packs both have, whose content differs: the config's pack changed, or this
	// yolo ships a different version of an embedded one.
	Changed []string
	// Unreadable names a pack whose content could not be hashed, with why; the comparison then
	// says nothing about that pack.
	Unreadable []string
}

// empty reports whether the two sets are the same.
func (d packSetDiff) empty() bool {
	return len(d.Added)+len(d.Removed)+len(d.Changed)+len(d.Unreadable) == 0
}

// diffPackSets compares the configured packs (this launch's own staging) with the ones the
// running jail booted with.
func diffPackSets(configured, booted []*packload.Pack) packSetDiff {
	byName := func(ps []*packload.Pack) map[string]*packload.Pack {
		m := map[string]*packload.Pack{}
		for _, p := range ps {
			m[p.Name] = p
		}
		return m
	}
	want, have := byName(configured), byName(booted)
	var d packSetDiff
	for name, p := range want {
		q, ok := have[name]
		if !ok {
			d.Added = append(d.Added, name)
			continue
		}
		a, errA := packContentDigest(p.Root)
		b, errB := packContentDigest(q.Root)
		switch {
		case errA != nil:
			d.Unreadable = append(d.Unreadable, name+" ("+errA.Error()+")")
		case errB != nil:
			d.Unreadable = append(d.Unreadable, name+" ("+errB.Error()+")")
		case a != b:
			d.Changed = append(d.Changed, name)
		}
	}
	for name := range have {
		if _, ok := want[name]; !ok {
			d.Removed = append(d.Removed, name)
		}
	}
	sort.Strings(d.Added)
	sort.Strings(d.Removed)
	sort.Strings(d.Changed)
	sort.Strings(d.Unreadable)
	return d
}

// attachPackView is the pack set an attach reads: the running jail's own, and what this entry
// composes from it.
type attachPackView struct {
	// staged is the running jail's tree and the packs loaded from it; the configured staging
	// when the jail's tree could not be found (unfound); empty when it would not load
	// (unreadable).
	staged stagedPacks
	// channel is this entry's provider/profile channel, composed over staged.packs.
	channel *packChannel
	// targetCmd is the command this entry execs, with staged.packs' launch flags injected.
	targetCmd string
	// diff is how the configured packs differ from staged.packs.
	diff packSetDiff
	// unfound says why the jail's tree could not be found, or "" when it was.
	unfound string
	// unreadable is true when the jail's tree was found and would not load. Nothing is composed
	// over it, and nothing is refreshed from it.
	unreadable bool
}

// attachPackSkew is why an attach cannot compose what it delivers over the running jail's packs:
// the jail's tree is there and would not load, or what this entry selects fails over the packs in
// it. Either is a KNOWN difference between the jail and this entry, so the attach takes the
// contract gate's disposition (settleAttachSkew): never a ride-along on the configured packs.
type attachPackSkew struct {
	// dir is the jail's tree.
	dir string
	// unreadable: the tree would not load, and err says why. Otherwise err is the composition's
	// refusal over the packs it holds.
	unreadable bool
	err        error
	// diff is how the configured packs differ from the jail's, when the tree loaded.
	diff packSetDiff
}

// runningJailPackView builds the pack set an attach to cname's running jail reads: the jail's
// own tree, loaded, and the channel and command composed over it. fresh is this launch's own
// staging of the config, which nothing binds; channel and targetCmd are what Run composed from
// it.
//
// When the two trees hold the same packs with the same content, what Run composed is already the
// jail's (the declarations are byte-identical), so it is reused. When they differ, the channel is
// composed again over the jail's packs, with the environment Run already hydrated (a second
// env_sources pass could prompt twice), and the launch flags are injected again.
//
// A tree that cannot be FOUND is not a refusal. The view is then the configured staging, as every
// attach read before per-launch trees, and unfound says why, which the attach prints: it can no
// longer say whether the two differ. A tree that is found and WILL NOT LOAD is a different fact:
// the jail has packs this yolo's loader refuses, which is what every jail v0.10.0 launched with
// claude binds (that release's claude declares a hook this build removed). Composing from the
// configured packs then would deliver into that jail exactly what OQ-PK2 (c) keeps from it, so it
// is returned as a skew, with the refusal of a selection only the configured packs satisfy.
func (o *Options) runningJailPackView(cname, rt string, cfg *jsonx.OrderedMap, fresh stagedPacks,
	channel *packChannel, targetCmd string) (attachPackView, *attachPackSkew) {
	dir, unfound := runningJailPackTree(cname, rt)
	if dir == "" {
		return attachPackView{staged: fresh, channel: channel, targetCmd: targetCmd, unfound: unfound}, nil
	}
	booted, err := loadPackTree(dir)
	if err != nil {
		return attachPackView{channel: channel, targetCmd: targetCmd, unreadable: true},
			&attachPackSkew{dir: dir, unreadable: true, err: err}
	}
	view := attachPackView{
		staged:    stagedPacks{root: dir, packs: booted, briefings: quietPackBriefings(booted)},
		channel:   channel,
		targetCmd: targetCmd,
		diff:      diffPackSets(fresh.packs, booted),
	}
	if view.diff.empty() {
		return view, nil
	}
	var userEnv *jsonx.OrderedMap
	if channel != nil {
		userEnv = channel.userEnv
	}
	c, err := o.composePackChannel(cfg, booted, userEnv)
	if err != nil {
		return view, &attachPackSkew{dir: dir, err: err, diff: view.diff}
	}
	view.channel = c
	view.targetCmd = o.injectLaunchFlagsForAttach(booted, o.Args, targetCmd)
	return view, nil
}

// packSkew words an attachPackSkew for the disposition: the headline names the packs the jail
// lacks, not the composition's own remedy, which tells the user to declare what their configured
// pack already declares.
func (o *Options) packSkew(baked string, s *attachPackSkew) attachSkew {
	lines := o.versionSkewLines(baked)
	// Each difference once, unrendered for the briefing (SK-D15) and as a bullet for stderr.
	var diffs []string
	differs := func(d string) {
		diffs = append(diffs, d)
		lines = append(lines, "  • "+d)
	}
	if s.unreadable {
		differs(fmt.Sprintf("This yolo could not read the pack tree it booted from, %s: %v.", s.dir, s.err))
		return attachSkew{
			jail:        "booted from packs this yolo cannot read, so nothing this entry delivers can be composed over them",
			lines:       lines,
			keeps:       ", and its skills and briefing are not refreshed from your configured packs",
			differences: diffs,
		}
	}
	d := s.diff
	jail := "cannot take what this entry selects with the packs it was launched with"
	switch {
	case len(d.Added) > 0:
		jail = "was launched without " + strings.Join(d.Added, ", ") + ", and what this entry selects " +
			"cannot be composed over the packs it has"
	case len(d.Changed) > 0:
		jail = "has other copies of " + strings.Join(d.Changed, ", ") + " than your config, and what " +
			"this entry selects cannot be composed over the packs it has"
	}
	if len(d.Added) > 0 {
		differs("Added to your config since it launched: " + strings.Join(d.Added, ", ") + ".")
	}
	if len(d.Changed) > 0 {
		differs("Changed since it launched: " + strings.Join(d.Changed, ", ") + ".")
	}
	if len(d.Removed) > 0 {
		differs("Removed from your config since it launched: " + strings.Join(d.Removed, ", ") + ".")
	}
	differs(fmt.Sprintf("Over the packs it has: %v.", s.err))
	return attachSkew{jail: jail, lines: lines, differences: diffs}
}

// adoptPackRecords points the process-wide pack records at the running jail's packs, for the
// host-side readers an attach still runs: the skills refresh (jailcontent.SetPackSkillDirs) and
// the briefing's loophole list (the converged loophole set). stagePacks set them from the
// configured staging, which the attach then discards.
//
// The problems each reader reports are not printed again: they are the jail's launch's, which
// printed them, and an attach prints nothing on stdout ahead of its "Attaching" line.
func adoptPackRecords(packs []*packload.Pack) {
	var skills []jailcontent.PackSkillSource
	for _, p := range packs {
		sources, _ := p.SkillsSources()
		skills = append(skills, jailSkillSources(p, sources)...)
	}
	jailcontent.SetPackSkillDirs(skills)
	loopholes.SetPackModules(packLoopholeModules(packs))
	loopholes.SetPackSupersessions(packSupersessions(packs))
}

// quietPackBriefings is packBriefingProses for a set whose problems were already reported, at the
// launch that staged it.
func quietPackBriefings(packs []*packload.Pack) []jailcontent.PackBriefing {
	var out []jailcontent.PackBriefing
	for _, p := range packs {
		sources, _ := p.GovernedSources(packdecl.KindBriefing)
		for _, src := range sources {
			out = append(out, jailcontent.PackBriefing{Name: p.Name, Text: src.Text, Agents: src.By.Agents})
		}
	}
	return out
}

// noteBootedPackSetDiffers is the attach's NOTICE (OQ-PK2 (c)): the jail keeps the packs it booted
// with, so a configured change reaches it only through a restart, and the attach says which packs
// differ. Stderr, since the attach's stdout opens with its "Attaching" line and then belongs to the
// command. Silent when nothing differs, and for a tree that would not load, whose account the
// disposition already gave.
func (o *Options) noteBootedPackSetDiffers(rt, cname string, view attachPackView) {
	out := o.pr(o.Stderr)
	if view.unfound != "" {
		out.printf("[yellow]Warning: could not find the pack tree this jail booted with (%s), so this "+
			"attach composes from your configured packs and cannot say whether they differ from "+
			"the jail's.[/yellow]", view.unfound)
		return
	}
	d := view.diff
	if view.unreadable || d.empty() {
		return
	}
	var parts []string
	if len(d.Added) > 0 {
		parts = append(parts, "added "+strings.Join(d.Added, ", "))
	}
	if len(d.Removed) > 0 {
		parts = append(parts, "removed "+strings.Join(d.Removed, ", "))
	}
	if len(d.Changed) > 0 {
		parts = append(parts, "changed "+strings.Join(d.Changed, ", "))
	}
	if len(d.Unreadable) > 0 {
		parts = append(parts, "could not compare "+strings.Join(d.Unreadable, ", "))
	}
	out.printf("[yellow]Notice: your configured packs differ from the ones this jail booted with "+
		"(%s). The jail keeps its own until it restarts: %s once its sessions are done, then "+
		"launch again, picks the change up.[/yellow]", strings.Join(parts, "; "), stopRemedy(rt, cname))
}

// retireLegacyPackStaging removes the shared staging tree a launch before per-launch trees wrote
// (paths.LegacyPackStagingDir), once no jail can hold it: the runtime has ANSWERED that no
// container of cname exists, and this launch holds the workspace launch lock, so no other launch of
// the workspace is creating one. Called by the fresh container path after its stale-container
// removal. "Could not ask" removes nothing, and neither does a container that exists, running or
// not: a jail launched before the change binds this tree, and removing a directory under a live
// bind detaches it.
func (o *Options) retireLegacyPackStaging(cname, rt string) {
	legacyDir := paths.LegacyPackStagingDir(cname)
	if !isDir(legacyDir) {
		return
	}
	if id, known := o.probeExistingContainer(cname, rt, trackingProbeTimeout); !known || id != "" {
		return
	}
	_ = os.RemoveAll(legacyDir)
}

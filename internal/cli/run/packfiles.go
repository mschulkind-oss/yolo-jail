package run

// packfiles.go delivers the `files` contribution kind: an opaque tree a pack owns
// outright, bind-mounted :ro at a home-relative destination in the jail.
//
// It shipped INERT. `files` parsed, validated, printed a footprint claim ("read-only
// tree") and was refused by name at the host — but no boot-path code ever bound it, so
// in a jail it was a silent drop (docs/plans/pack-host-management-plan.md N1). A kind
// that `pack lint` and `pack footprint` both report as working while it delivers nothing
// is the exact failure mode the codebase refuses elsewhere (internal/render's FieldSet
// exists so an inapplicable kind is REFUSED by name rather than skipped in silence).

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// packFilesTarget is one resolved `files` contribution: the pack that declared it, the
// STAGED source tree, and the home-relative destination.
//
// The source is the pack's staged tree, not its origin directory, and that is
// load-bearing rather than convenient: staging is where packstage's exec-bit and
// escaping-symlink refusals ran, so binding the staged copy is what stops a `files` tree
// being a channel around them. Same argument the pack-manifest mount makes for :ro-ing
// /ctx/packs.
//
// `files` HONORS `from`, as `skills` now does (packload.SkillsSourceDir) and as `briefing`
// does at the host notch — but for `files` there is no fallback to fall back TO: there is no
// convention for an opaque tree, so the declaration is the only thing that can name it, and
// an absent source is a warning rather than a different dir.
type packFilesTarget struct {
	Pack string
	Src  string // absolute host path: <staged pack root>/<from>
	Dest string // home-relative, as declared (validated relative, no "..", no ":")

	// Root is the pack's whole staged tree, and From the `from` that was joined onto it.
	// Both exist for the SKIP MESSAGE and only for it: "this contribution's source is
	// missing" and "the pack's entire staged tree is missing" are different events with
	// different readers, and Src alone cannot tell them apart. See
	// packFilesSkipWarning.
	Root string
	From string

	// Tree is a PATCHED EXTENSION's extension key (docs/design/patched-extensions.md §8.1), "" for
	// a pack's own tree. Its Src is the launch's per-launch copy of the good build, beside the pack
	// tree and never in it, and a tree with no copy this launch is no target at all: it mounts
	// nothing, and needs no mountpoint, since an empty directory at `into` is one an agent may try
	// to load (§9).
	Tree string
}

// packFilesTargets resolves every loaded pack's `files` contributions, in declaration
// order. Deterministic without sorting: contributions come from an ordered manifest list
// and packs from the ordered config list, unlike the map-derived emitters (env, cache
// relocations) that have to sort.
//
// No origin gate: a `files` tree is the PACK's own content, not a host read, so a fetched
// pack delivers it exactly like an embedded one. What a fetched pack may NOT do with the
// tree is land it on the jail's PATH — refused at the manifest, in
// packdecl.appendJailPathProblems, so a name on PATH comes from a `program` declaration or
// from nowhere.
//
// trees is the launch's per-launch copy of each PATCHED EXTENSION it delivered, by extension key
// (Options.patchedTreeDirs); nil delivers none, and every patched extension is then no target.
// `from` is never joined for one: it has none, and joining "" would mount the pack's whole
// staged tree at `into`.
func packFilesTargets(packs []*packload.Pack, trees map[string]string) []packFilesTarget {
	// ADDRESSED contributions (`agents`, no `into`) are resolved by packload.ResolveDestinations,
	// the one resolver `yolo host apply` renders through too (docs/plans/notch-convergence.md
	// row D8). After it, each addressed tree is an ordinary `into` contribution on a copy of its
	// pack, landing at the agent pack's slot joined with the CONTRIBUTING pack's name
	// (packload.SlotLanding), so the loop below knows no addressed shape at all. This function
	// used to resolve the slot itself from a table of its own, and the two resolvers were free
	// to disagree about which slot a name meant.
	//
	// An agent that declares no slot delivers nothing here, and the launch warns about it in
	// reportUnmatchedAudiences (unmatchedaudience.go) when NO name the contribution lists has a
	// slot, which is `yolo host apply`'s granularity too.
	resolved, _ := packload.ResolveDestinations(packs)
	var out []packFilesTarget
	for _, p := range resolved {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindFiles {
				continue
			}
			// A DESTINATION is a bare slot: it ships no content, so it makes no mount. That is
			// what keeps the slot root itself raw — every tree lands UNDER it, namespaced by the
			// contributing pack, the owner's own included (design §3).
			if c.Agent != "" || c.Into == "" {
				continue
			}
			// A BUILT TREE — a patched extension or an unmodified one — mounts the per-launch copy this
			// launch made of its good build, or nothing: it names no `from` in the pack.
			if c.IsBuiltTree() {
				key := p.Name + "/" + c.ExtensionName()
				if dir := trees[key]; dir != "" {
					out = append(out, packFilesTarget{Pack: p.Name, Src: dir, Dest: c.Into, Root: p.Root, Tree: key})
				}
				continue
			}
			out = append(out, packFilesTarget{
				Pack: p.Name, Src: filepath.Join(p.Root, filepath.FromSlash(c.From)),
				Dest: c.Into, Root: p.Root, From: c.From,
			})
		}
	}
	return out
}

// packFilesMount is one planned mount emitted for `files` contributions.
// Multiple contributions targeting the same directory or nesting inside a directory
// merge into a single directory mount.
type packFilesMount struct {
	Dest    string
	Src     string
	IsDir   bool
	Packs   []string
	Targets []packFilesTarget
}

type packFilesPlan struct {
	Mounts    []packFilesMount
	Conflicts []string
	Skipped   []packFilesTarget
}

type packFilesMountpointTarget struct {
	Src   string
	IsDir bool
}

func filesStagingName(dest string) string {
	var b strings.Builder
	b.WriteString("files-")
	for _, r := range dest {
		switch r {
		case '~':
			b.WriteString("~0")
		case '/':
			b.WriteString("~1")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func filesEqual(pathA, pathB string) bool {
	if pathA == pathB {
		return true
	}
	f1, err := os.Open(pathA)
	if err != nil {
		return false
	}
	defer f1.Close()
	f2, err := os.Open(pathB)
	if err != nil {
		return false
	}
	defer f2.Close()
	fi1, err := f1.Stat()
	if err != nil {
		return false
	}
	fi2, err := f2.Stat()
	if err != nil {
		return false
	}
	if fi1.Size() != fi2.Size() {
		return false
	}
	buf1 := make([]byte, 32*1024)
	buf2 := make([]byte, 32*1024)
	for {
		n1, err1 := f1.Read(buf1)
		n2, err2 := f2.Read(buf2)
		if n1 != n2 || !bytes.Equal(buf1[:n1], buf2[:n2]) {
			return false
		}
		if err1 != nil || err2 != nil {
			return errors.Is(err1, io.EOF) && errors.Is(err2, io.EOF)
		}
	}
}

func copyOneFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	perm := os.FileMode(0o644)
	if info.Mode()&0o111 != 0 {
		perm = 0o755
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(perm)
}

func uniqueStrings(s []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, v := range s {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}

func planPackFilesMounts(packs []*packload.Pack, trees map[string]string, agentsPath string) *packFilesPlan {
	targets := packFilesTargets(packs, trees)
	plan := &packFilesPlan{}

	type targetItem struct {
		target  packFilesTarget
		dest    string
		isDir   bool
		isFile  bool
		treeKey string
	}

	var active []targetItem
	for _, t := range targets {
		dest := strings.Trim(filepath.Clean(filepath.ToSlash(t.Dest)), "/")
		if dest == "." || dest == "" {
			continue
		}
		isTargetDir := t.Tree != "" || isDir(t.Src)
		isTargetFile := t.Tree == "" && isFile(t.Src)

		if !isTargetDir && !isTargetFile {
			plan.Skipped = append(plan.Skipped, t)
			continue
		}
		active = append(active, targetItem{
			target:  t,
			dest:    dest,
			isDir:   isTargetDir,
			isFile:  isTargetFile,
			treeKey: t.Tree,
		})
	}

	byDest := map[string][]targetItem{}
	for _, item := range active {
		byDest[item.dest] = append(byDest[item.dest], item)
	}

	for dest, items := range byDest {
		hasDir := false
		hasFile := false
		var dirPacks, filePacks []string
		for _, it := range items {
			if it.isDir {
				hasDir = true
				dirPacks = append(dirPacks, it.target.Pack)
			}
			if it.isFile {
				hasFile = true
				filePacks = append(filePacks, it.target.Pack)
			}
		}
		if hasDir && hasFile {
			sort.Strings(dirPacks)
			sort.Strings(filePacks)
			plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
				"pack %s claims ~/%s as a directory, and pack %s claims it as a file — cannot merge directory and file at the same destination",
				strings.Join(dirPacks, " and "), dest, strings.Join(filePacks, " and ")))
		}
	}

	candidateRootsMap := map[string]struct{}{}
	for _, it := range active {
		if it.isDir {
			candidateRootsMap[it.dest] = struct{}{}
		}
	}
	var candidateRoots []string
	for r := range candidateRootsMap {
		candidateRoots = append(candidateRoots, r)
	}
	sort.Slice(candidateRoots, func(i, j int) bool {
		di := strings.Count(candidateRoots[i], "/")
		dj := strings.Count(candidateRoots[j], "/")
		if di != dj {
			return di < dj
		}
		return candidateRoots[i] < candidateRoots[j]
	})

	var outerRoots []string
	for _, r := range candidateRoots {
		subsumed := false
		for _, p := range outerRoots {
			if r == p || strings.HasPrefix(r, p+"/") {
				subsumed = true
				break
			}
		}
		if !subsumed {
			outerRoots = append(outerRoots, r)
		}
	}

	assigned := map[int]bool{}
	groups := map[string][]targetItem{}

	for _, root := range outerRoots {
		for i, it := range active {
			if it.dest == root || strings.HasPrefix(it.dest, root+"/") {
				groups[root] = append(groups[root], it)
				assigned[i] = true
			}
		}
	}

	unassignedByDest := map[string][]targetItem{}
	var unassignedDests []string
	for i, it := range active {
		if !assigned[i] {
			if len(unassignedByDest[it.dest]) == 0 {
				unassignedDests = append(unassignedDests, it.dest)
			}
			unassignedByDest[it.dest] = append(unassignedByDest[it.dest], it)
		}
	}
	sort.Strings(unassignedDests)

	for _, d1 := range unassignedDests {
		prefix := d1 + "/"
		for _, d2 := range unassignedDests {
			if strings.HasPrefix(d2, prefix) {
				p1 := unassignedByDest[d1][0].target.Pack
				p2 := unassignedByDest[d2][0].target.Pack
				if p1 == p2 {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
						"pack %s claims ~/%s as a file and ~/%s, inside it — cannot nest inside a file",
						p1, d1, d2))
				} else {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
						"pack %s claims ~/%s as a file, and pack %s claims ~/%s, inside it — cannot nest inside a file",
						p1, d1, p2, d2))
				}
			}
		}
	}

	for _, d := range unassignedDests {
		items := unassignedByDest[d]
		if len(items) == 1 {
			it := items[0]
			plan.Mounts = append(plan.Mounts, packFilesMount{
				Dest:    d,
				Src:     it.target.Src,
				IsDir:   false,
				Packs:   []string{it.target.Pack},
				Targets: []packFilesTarget{it.target},
			})
			continue
		}
		first := items[0]
		hasDiff := false
		var packs []string
		packs = append(packs, first.target.Pack)
		for j := 1; j < len(items); j++ {
			other := items[j]
			packs = append(packs, other.target.Pack)
			if !filesEqual(first.target.Src, other.target.Src) {
				hasDiff = true
				if first.target.Pack == other.target.Pack {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
						"pack %s delivers different content for ~/%s across multiple contributions — give them different file names, or drop one",
						first.target.Pack, d))
				} else {
					plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
						"pack %s and pack %s both deliver different content for ~/%s — give them different file names, or drop one",
						first.target.Pack, other.target.Pack, d))
				}
			}
		}
		if !hasDiff {
			var targets []packFilesTarget
			for _, it := range items {
				targets = append(targets, it.target)
			}
			plan.Mounts = append(plan.Mounts, packFilesMount{
				Dest:    d,
				Src:     first.target.Src,
				IsDir:   false,
				Packs:   uniqueStrings(packs),
				Targets: targets,
			})
		}
	}

	for _, root := range outerRoots {
		items := groups[root]
		if len(items) == 1 && items[0].dest == root && items[0].isDir {
			it := items[0]
			plan.Mounts = append(plan.Mounts, packFilesMount{
				Dest:    root,
				Src:     it.target.Src,
				IsDir:   true,
				Packs:   []string{it.target.Pack},
				Targets: []packFilesTarget{it.target},
			})
			continue
		}

		for _, it1 := range items {
			if it1.isFile {
				prefix := it1.dest + "/"
				for _, it2 := range items {
					if strings.HasPrefix(it2.dest, prefix) {
						if it1.target.Pack == it2.target.Pack {
							plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
								"pack %s claims ~/%s as a file and ~/%s, inside it — cannot nest inside a file",
								it1.target.Pack, it1.dest, it2.dest))
						} else {
							plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
								"pack %s claims ~/%s as a file, and pack %s claims ~/%s, inside it — cannot nest inside a file",
								it1.target.Pack, it1.dest, it2.target.Pack, it2.dest))
						}
					}
				}
			}
		}

		type fileEntry struct {
			pack    string
			srcPath string
			isExec  bool
		}
		fileContribs := map[string][]fileEntry{}
		var groupTargets []packFilesTarget
		var groupPacks []string

		for _, it := range items {
			groupTargets = append(groupTargets, it.target)
			groupPacks = append(groupPacks, it.target.Pack)
			relDest := strings.TrimPrefix(it.dest, root)
			relDest = strings.TrimPrefix(relDest, "/")

			if it.isDir {
				if it.treeKey != "" && !isDir(it.target.Src) {
					continue
				}
				if isDir(it.target.Src) {
					_ = filepath.WalkDir(it.target.Src, func(path string, d fs.DirEntry, err error) error {
						if err != nil || d.IsDir() {
							return nil
						}
						info, err := os.Stat(path)
						if err != nil || info.IsDir() {
							return nil
						}
						relInside, err := filepath.Rel(it.target.Src, path)
						if err != nil {
							return nil
						}
						relFile := filepath.ToSlash(filepath.Clean(filepath.Join(relDest, relInside)))
						isExec := info.Mode()&0o111 != 0
						fileContribs[relFile] = append(fileContribs[relFile], fileEntry{
							pack:    it.target.Pack,
							srcPath: path,
							isExec:  isExec,
						})
						return nil
					})
				}
			} else if it.isFile {
				relFile := filepath.ToSlash(filepath.Clean(relDest))
				info, err := os.Stat(it.target.Src)
				isExec := false
				if err == nil && info.Mode()&0o111 != 0 {
					isExec = true
				}
				fileContribs[relFile] = append(fileContribs[relFile], fileEntry{
					pack:    it.target.Pack,
					srcPath: it.target.Src,
					isExec:  isExec,
				})
			}
		}

		var relFiles []string
		for rf := range fileContribs {
			relFiles = append(relFiles, rf)
		}
		sort.Strings(relFiles)

		for _, f1 := range relFiles {
			prefix := f1 + "/"
			for _, f2 := range relFiles {
				if strings.HasPrefix(f2, prefix) {
					p1 := fileContribs[f1][0].pack
					p2 := fileContribs[f2][0].pack
					if p1 == p2 {
						plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
							"pack %s claims ~/%s/%s as a file and ~/%s/%s, inside it — cannot nest inside a file",
							p1, root, f1, root, f2))
					} else {
						plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
							"pack %s claims ~/%s/%s as a file, and pack %s claims ~/%s/%s, inside it — cannot nest inside a file",
							p1, root, f1, p2, root, f2))
					}
				}
			}
		}

		hasGroupDiff := false
		for _, rf := range relFiles {
			entries := fileContribs[rf]
			if len(entries) <= 1 {
				continue
			}
			first := entries[0]
			for j := 1; j < len(entries); j++ {
				other := entries[j]
				if !filesEqual(first.srcPath, other.srcPath) {
					hasGroupDiff = true
					fullRel := filepath.ToSlash(filepath.Join(root, rf))
					if first.pack == other.pack {
						plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
							"pack %s delivers different content for ~/%s across multiple contributions — give them different file names, or drop one",
							first.pack, fullRel))
					} else {
						plan.Conflicts = append(plan.Conflicts, fmt.Sprintf(
							"pack %s and pack %s both deliver different content for ~/%s — give them different file names, or drop one",
							first.pack, other.pack, fullRel))
					}
				}
			}
		}

		stagedDir := filesStagingName(root)
		if agentsPath != "" {
			stagedDir = filepath.Join(agentsPath, filesStagingName(root))
			if !hasGroupDiff && len(plan.Conflicts) == 0 {
				if err := os.MkdirAll(stagedDir, 0o755); err == nil {
					for _, rf := range relFiles {
						entry := fileContribs[rf][0]
						dstPath := filepath.Join(stagedDir, filepath.FromSlash(rf))
						if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err == nil {
							_ = copyOneFile(entry.srcPath, dstPath)
						}
					}
				}
			}
		}
		plan.Mounts = append(plan.Mounts, packFilesMount{
			Dest:    root,
			Src:     stagedDir,
			IsDir:   true,
			Packs:   uniqueStrings(groupPacks),
			Targets: groupTargets,
		})
	}

	sort.Slice(plan.Mounts, func(i, j int) bool {
		return plan.Mounts[i].Dest < plan.Mounts[j].Dest
	})
	sort.Strings(plan.Conflicts)
	return plan
}

// packFilesMountArgs emits one `-v <staged tree>:/home/agent/<into>:ro` per planned `files`
// mount.
//
// Multiple contributions targeting the same directory or nesting inside a directory
// merge into a single staged directory mount, avoiding runc EROFS and duplicate mount
// destination errors.
func (o *Options) packFilesMountArgs(in *assembleInput) []string {
	plan := planPackFilesMounts(in.packs, in.treeDirs, in.agentsPath)
	for _, t := range plan.Skipped {
		o.pr(o.Stdout).print("[yellow]" + packFilesSkipWarning(t) + "[/yellow]")
	}
	var args []string
	for _, m := range plan.Mounts {
		if m.IsDir {
			args = append(args, "-v", m.Src+":/home/agent/"+m.Dest+":ro")
		} else {
			if in.rt == "container" {
				acMaterialize(m.Src, m.Dest, in.wsState)
				continue
			}
			args = append(args, "-v", m.Src+":/home/agent/"+m.Dest+":ro")
		}
	}
	return args
}

// packFilesSkipWarning is the sentence a skipped `files` contribution prints, and it is
// TWO sentences because the reader has two different jobs.
//
// A MISSING `from` is the user's pack: yolo staged the tree it was given and the declared
// path is not in it, so the fix is in the pack — the `from`, or an `only`/`exclude` filter
// that dropped it. The message names the `from` verbatim rather than making the reader
// subtract the staging root out of an absolute path, and names `yolo pack lint`, which
// answers the same question without a launch.
//
// A MISSING STAGED ROOT is not the user's pack at all, and printing the advice above for it
// sends a reader to audit a manifest that was never wrong. stagePacks wrote that directory
// EARLIER IN THIS LAUNCH, unconditionally, so its absence means something removed it since —
// which is a thing that really happened (a housekeeping sweep in a concurrent capture
// sub-launch reaped it, 2026-09-09; see touchAgentStagingDir). It is also terminal: the
// pack-manifest mount's source is that same tree, so podman is about to fail the container
// with `statfs …/packs: no such file or directory`, and the four `files` warnings a real
// host printed were the only readable diagnosis of it — this emitter is the one place that
// probes a staged path before handing it to the runtime.
//
// Both share ONE Src computation (packFilesTargets: filepath.Join(Root, From)), so a
// `from` of "files/models.json" and a `from` of "bin" differ in the message for the same
// reason they differ on disk — because the two packs declared different sources, not
// because two code paths built them.
func packFilesSkipWarning(t packFilesTarget) string {
	if !isDir(t.Root) {
		return "Warning: pack " + t.Pack + "'s staged tree is GONE, so its `files` claim on " +
			t.Dest + " cannot be delivered: nothing at " + t.Root + ". yolo staged that " +
			"directory earlier in this launch, so this is not a problem with the pack's " +
			"`from` or its filters — something removed it since. Expect the container to " +
			"fail on the same path (`statfs …: no such file or directory`); re-run the launch."
	}
	return "Warning: pack " + t.Pack + " declares a `files` tree that is not in its staged " +
		"content, skipping: " + t.Dest + " (`from` is \"" + t.From + "\", and nothing " +
		"staged at " + t.Src + " — check that path in the pack, and any only/exclude " +
		"filters; `yolo pack lint <pack-dir>` reports this without a launch)"
}

// A `files` destination needs a same-shaped mountpoint before the runtime applies the
// read-only source bind. The target may live in the podman jail's home skeleton or in the
// workspace overlay: preparePackFiles owns the latter (including retirement), while
// packFilesSkeletonEntries names the former for buildHomeSkeleton.
//
// Same belt-and-braces as writable_home_dirs and host_files (buildHomeSkeleton creates all
// three) and for the same reason: the OCI runtime does not reliably create a
// mountpoint inside a :ro bind. podman 5.8.4/crun 1.27.1 does auto-create one (verified
// — see project_ro_home_mount_autocreate), but the maintainer hit the EROFS path on their
// stack, where it surfaces as the unreadable `conmon bytes "": readObjectStart`.
//
// The old implementation created every target in GlobalHome. A target below a writable
// state dir is shadowed by that dir's workspace bind, though, so the runtime created a
// second empty target in the persistent workspace overlay. Recording that real target is
// what makes a later dropped contribution retire cleanly.
//
// The leaf type has to match the source: a dir mount over a file (or the reverse) aborts
// container creation. Best-effort — a failure here degrades to whatever the runtime does
// on its own, which on the tested stack is the working auto-create.
//
// Manifest paths are host-side paths relative to wsState, not home-relative paths: podman
// maps `.pi/...` to `<wsState>/pi/...`, while Apple Container maps it to
// `<wsState>/.pi/...` through its whole-home bind.
// packFilesBeforeRetire, when set, runs between the retire loop's check that a recorded
// mountpoint is unchanged and its removal: a test seam for a directory the jail swaps for a link
// in that window.
var packFilesBeforeRetire func(rel string)

const packFilesMountpointManifestName = "pack-files-mountpoints.json"
const packFilesMountpointManifestVersion = 1

type packFilesMountpointManifest struct {
	Version int                            `json:"version"`
	Entries map[string]packFilesMountpoint `json:"entries"`
}

type packFilesMountpoint struct {
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
}

// preparePackFiles provisions the mount targets that live in THIS workspace's writable
// home overlay and records only targets yolo can prove it created. That record is what
// lets a later launch retire scaffolding for a contribution that disappeared without
// mistaking a user's file at the same path for yolo's.
//
// A target lives in wsState on Apple Container because that backend binds the whole home
// writable. On podman it lives there only when it is below a pack-declared workspace state
// dir; every other target lives in the machine-wide read-only base and cannot safely be
// retired from one workspace's view of the configured packs.
func preparePackFiles(packs []*packload.Pack, trees map[string]string, wsState, rt string) []string {
	manifestPath := filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName)
	previous := loadPackFilesMountpointManifest(manifestPath)
	current := map[string]packFilesMountpointTarget{}
	if rt != "macos-user" { // parity: NotApplicable — macos-user copies `files` trees through the home overlay (buildMacosHomeOverlay), which needs no mountpoint.
		writable := packload.WritableDirs(packs)
		plan := planPackFilesMounts(packs, trees, "")
		for _, m := range plan.Mounts {
			if rel, ok := packFilesWorkspaceRel(m.Dest, writable, rt); ok {
				current[rel] = packFilesMountpointTarget{Src: m.Src, IsDir: m.IsDir}
			}
		}
	}
	var archived []string
	migrationApplicable := rt != "macos-user" && hasSingleFilePackTarget(current) // parity: NotApplicable — macos-user copies `files` trees through the home overlay (buildMacosHomeOverlay), which needs no mountpoint.
	if migrationApplicable && previous.Version < packFilesMountpointManifestVersion {
		archived = archiveLegacyPackFileMountpoints(wsState, current)
	}

	// Retire first, so a contribution changing shape at one destination can recreate the
	// right target below.
	retirePackFileMountpoints(wsState, previous, current)

	next := &packFilesMountpointManifest{
		Version: previous.Version,
		Entries: map[string]packFilesMountpoint{},
	}
	if migrationApplicable {
		next.Version = packFilesMountpointManifestVersion
	}
	for rel, t := range current {
		dest := filepath.Join(wsState, rel)
		kind := "file"
		if t.IsDir {
			kind = "dir"
		}

		if owned, ok := previous.Entries[rel]; ok {
			// Apple Container replaces a single-file scaffold with a source snapshot.
			// Refreshing the digest keeps that snapshot removable after a pack update.
			if kind == "file" && rt == "container" { // parity: Honored — Apple Container delivers the source as a copied snapshot.
				owned.SHA256 = fileSHA256(t.Src)
			}
			next.Entries[rel] = owned
			continue
		}

		// Beneath wsState, never through a symlinked directory the jail left in its home
		// (mountpointBeneath, wsstatebeneath.go).
		created := mountpointBeneath(wsState, rel, kind)

		owned := packFilesMountpoint{Kind: kind}
		if kind == "file" && rt == "container" { // parity: Honored — Apple Container delivers the source as a copied snapshot.
			owned.SHA256 = fileSHA256(t.Src)
		}
		if created && packFilesMountpointUnchanged(dest, packFilesMountpoint{Kind: kind}) {
			next.Entries[rel] = owned
			continue
		}
		// Migration from the pre-manifest implementation: crun created zero-byte file
		// targets inside writable overlays. A non-empty source over an existing empty
		// regular file is the one old shape that is strong enough to adopt; directories
		// and non-empty files may be the user's and remain unowned.
		if kind == "file" && fileIsEmptyRegular(dest) && !fileIsEmptyRegular(t.Src) {
			next.Entries[rel] = owned
		}
	}

	if len(next.Entries) == 0 && next.Version == 0 {
		_ = os.Remove(manifestPath)
		return archived
	}
	_ = savePackFilesMountpointManifest(manifestPath, next)
	return archived
}

// retirePackFileMountpoints removes each mountpoint previous records that current no longer
// claims at the same kind, and forgets it. Removal is deliberately conditional on the recorded
// scaffold still being unchanged: a file the user replaced or a directory that gained content
// is forgotten and left alone.
//
// The check and the removal are both made beneath a root on wsState (wsstatebeneath.go), opened
// refusing a link at wsState or `.yolo`, and never creating either. The manifest is jail-written
// evidence (it sits in `.yolo`), so the jail chooses every recorded path and its digest; and
// the overlay is jail-writable, so it can put a link at wsState or at any directory on the way,
// before the launch or between the check and the removal. By plain path, os.Remove followed
// such a link and deleted the host file of that name: through a link at wsState the old
// EvalSymlinks containment check resolved both sides into the host directory and passed, and a
// forged digest made any host file whose content the jail knows count as "unchanged". Beneath
// the root, a link leaving it is refused at every component, and r.Remove never follows a link
// at the final name.
func retirePackFileMountpoints(wsState string, previous *packFilesMountpointManifest, current map[string]packFilesMountpointTarget) {
	var overlay *os.Root
	opened := false
	defer func() {
		if overlay != nil {
			overlay.Close()
		}
	}()
	for rel, owned := range previous.Entries {
		if !safePackFilesManifestRel(rel) {
			delete(previous.Entries, rel)
			continue
		}
		if claimed, stillClaimed := current[rel]; stillClaimed {
			claimedKind := "file"
			if claimed.IsDir {
				claimedKind = "dir"
			}
			if claimedKind == owned.Kind {
				continue
			}
		}
		if !opened {
			opened = true
			overlay, _ = openExistingStateRoot(wsState)
		}
		if overlay != nil && packFilesMountpointUnchangedBeneath(overlay, rel, owned) {
			if packFilesBeforeRetire != nil {
				packFilesBeforeRetire(rel)
			}
			_ = overlay.Remove(rel)
		}
		delete(previous.Entries, rel)
	}
}

// openExistingStateRoot is openStateRoot that creates nothing: dir (wsState) and its parent
// (`.yolo`) must already exist, and a link at either is refused (paths.OpenStateDirRoot,
// paths.OpenStateSubdirRoot).
func openExistingStateRoot(dir string) (*os.Root, error) {
	dir = filepath.Clean(dir)
	parent, err := paths.OpenStateDirRoot(filepath.Dir(dir))
	if err != nil {
		return nil, err
	}
	defer parent.Close()
	return paths.OpenStateSubdirRoot(parent, filepath.Base(dir), dir)
}

// packFilesMountpointUnchangedBeneath is packFilesMountpointUnchanged for rel below r: a link
// at rel is never unchanged, and a file's digest is read only from a regular file beneath r.
func packFilesMountpointUnchangedBeneath(r *os.Root, rel string, owned packFilesMountpoint) bool {
	info, err := r.Lstat(rel)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	switch owned.Kind {
	case "file":
		if !info.Mode().IsRegular() {
			return false
		}
		if info.Size() == 0 {
			return true
		}
		if owned.SHA256 == "" {
			return false
		}
		f, err := paths.OpenRegularFileBeneath(r, rel)
		if err != nil {
			return false
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return false
		}
		return hex.EncodeToString(h.Sum(nil)) == owned.SHA256
	case "dir":
		if !info.IsDir() {
			return false
		}
		d, err := r.Open(rel)
		if err != nil {
			return false
		}
		defer d.Close()
		entries, err := d.ReadDir(1)
		return errors.Is(err, io.EOF) && len(entries) == 0
	default:
		return false
	}
}

func hasSingleFilePackTarget(targets map[string]packFilesMountpointTarget) bool {
	for _, t := range targets {
		if !t.IsDir && isFile(t.Src) {
			return true
		}
	}
	return false
}

// archiveLegacyPackFileMountpoints is the one-time bridge from the implementation that
// let the OCI runtime create unrecorded zero-byte targets. There is no proof left for one
// exact file after its contribution disappears. The strongest recoverable inference is an
// unclaimed empty regular file directly beside a currently managed single-file target: that
// is the precise shape crun left for thinking-preview.ts. Move, never delete, because an
// empty file the user intentionally put there has the same bytes.
func archiveLegacyPackFileMountpoints(wsState string, current map[string]packFilesMountpointTarget) []string {
	claimed := map[string]struct{}{}
	dirs := map[string]struct{}{}
	for rel, t := range current {
		claimed[filepath.Clean(rel)] = struct{}{}
		if !t.IsDir && isFile(t.Src) {
			dirs[filepath.Dir(rel)] = struct{}{}
		}
	}
	var archived []string
	for relDir := range dirs {
		dir := filepath.Join(wsState, relDir)
		if !pathParentWithin(filepath.Join(dir, "candidate"), wsState) {
			continue
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			rel := filepath.Join(relDir, entry.Name())
			if _, current := claimed[rel]; current {
				continue
			}
			src := filepath.Join(wsState, rel)
			if !fileIsEmptyRegular(src) {
				continue
			}
			if dest, err := archiveLegacyPackFileMountpoint(wsState, rel); err == nil {
				archived = append(archived, dest)
			}
		}
	}
	sort.Strings(archived)
	return archived
}

// archiveLegacyPackFileMountpoint moves rel below wsState to `.yolo/archive/pack-files/legacy`,
// and returns where it went. Both sides are named beneath one root on `.yolo`
// (wsstatebeneath.go): the directory is jail-writable, and through a link the jail left at
// `.yolo/archive` the MkdirAll and the rename put directories and the file in a host
// directory of its choosing.
func archiveLegacyPackFileMountpoint(wsState, rel string) (string, error) {
	stateDir := filepath.Dir(wsState)
	r, err := openDirRefusingLink(stateDir)
	if err != nil {
		return "", err
	}
	defer r.Close()
	dest := filepath.Join("archive", "pack-files", "legacy", rel)
	if err := r.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	base := dest
	for i := 2; ; i++ {
		if _, err := r.Lstat(dest); os.IsNotExist(err) {
			break
		}
		dest = fmt.Sprintf("%s.%d", base, i)
	}
	if err := r.Rename(filepath.Join(filepath.Base(wsState), rel), dest); err != nil {
		return "", err
	}
	return filepath.Join(stateDir, dest), nil
}

func pathUnderAny(rel string, roots []string) bool {
	rel = filepath.Clean(filepath.FromSlash(rel))
	for _, root := range roots {
		root = filepath.Clean(filepath.FromSlash(root))
		if rel == root || strings.HasPrefix(rel, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func packFilesWorkspaceRel(dest string, writable []string, rt string) (string, bool) {
	dest = filepath.Clean(filepath.FromSlash(dest))
	if rt == "container" { // parity: Honored — Apple Container binds wsState over the whole home.
		return dest, true
	}
	// Podman binds <wsState>/<state-without-leading-dot> over the declared home
	// path, so the host-side mountpoint spelling is not the home-relative spelling.
	// Prefer the deepest state root in case declarations ever nest.
	best := ""
	for _, root := range writable {
		root = filepath.Clean(filepath.FromSlash(root))
		if (dest == root || strings.HasPrefix(dest, root+string(filepath.Separator))) && len(root) > len(best) {
			best = root
		}
	}
	if best == "" {
		return "", false
	}
	remainder := strings.TrimPrefix(strings.TrimPrefix(dest, best), string(filepath.Separator))
	return filepath.Join(strings.TrimPrefix(best, "."), remainder), true
}

func packFilesTargetKind(t packFilesTarget) string {
	if isDir(t.Src) {
		return "dir"
	}
	if isFile(t.Src) {
		return "file"
	}
	return ""
}

func safePackFilesManifestRel(rel string) bool {
	clean := filepath.Clean(rel)
	return rel == clean && clean != "." && !filepath.IsAbs(clean) && clean != ".." &&
		!strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// pathParentWithin reports whether path's parent, resolving every link, is still below root.
// The legacy archive uses it to skip listing a directory that escapes the overlay; its move is
// made beneath a root regardless. It is not a containment check for a removal: through a link
// at root itself both sides resolve alike (retirePackFileMountpoints).
func pathParentWithin(path, root string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedParent)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// loadPackFilesMountpointManifest reads the ownership manifest at path, which sits in the
// jail-writable `.yolo`. Only a regular file is read (readRegularFileIn): a link the jail left
// at the name is not followed to a host file.
func loadPackFilesMountpointManifest(path string) *packFilesMountpointManifest {
	m := &packFilesMountpointManifest{Entries: map[string]packFilesMountpoint{}}
	body, err := readRegularFileIn(filepath.Dir(path), filepath.Base(path))
	if err != nil || json.Unmarshal(body, m) != nil || m.Entries == nil {
		m.Entries = map[string]packFilesMountpoint{}
	}
	return m
}

// savePackFilesMountpointManifest writes the manifest at path through a temp file renamed
// over it, both beneath a root on the manifest's directory, `.yolo`: a link the jail left at
// the temp name is replaced (writeBeneath), where os.WriteFile truncated the host file it
// named and wrote the manifest into it.
func savePackFilesMountpointManifest(path string, m *packFilesMountpointManifest) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	r, err := openDirRefusingLink(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer r.Close()
	name := filepath.Base(path)
	tmp := name + ".tmp"
	if err := writeBeneath(r, tmp, 0o644, 0, writeBytes(append(body, '\n'))); err != nil {
		return err
	}
	return r.Rename(tmp, name)
}

func packFilesMountpointUnchanged(path string, owned packFilesMountpoint) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	switch owned.Kind {
	case "file":
		if !info.Mode().IsRegular() {
			return false
		}
		return info.Size() == 0 || (owned.SHA256 != "" && fileSHA256(path) == owned.SHA256)
	case "dir":
		if !info.IsDir() {
			return false
		}
		entries, err := os.ReadDir(path)
		return err == nil && len(entries) == 0
	default:
		return false
	}
}

func fileIsEmptyRegular(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular() && info.Size() == 0
}

func fileSHA256(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

// packFilesSkeletonEntries returns the `files` destinations that need a mountpoint in a podman
// jail's home skeleton (buildHomeSkeleton), split by the leaf type of their source: every
// target no selected pack's writable dir covers. A target under a writable dir is prepared in
// the workspace overlay instead (preparePackFiles), because that dir's own bind shadows the
// skeleton there; an absent source mounts nothing (packFilesMountArgs warns), so it needs no
// mountpoint.
//
// It replaced preparePackFilesGlobal, which created the same entries in the shared base home
// every podman jail mounted — so one workspace's pack `files` mountpoints showed up in every
// other jail on the machine. The backend gate that function carried is the builder's call
// site's now: only podman builds a skeleton (runContainer).
func packFilesSkeletonEntries(packs []*packload.Pack, trees map[string]string) (dirs, files []string) {
	writable := packload.WritableDirs(packs)
	plan := planPackFilesMounts(packs, trees, "")
	for _, m := range plan.Mounts {
		if pathUnderAny(m.Dest, writable) {
			continue
		}
		if m.IsDir {
			dirs = append(dirs, m.Dest)
		} else {
			files = append(files, m.Dest)
		}
	}
	return dirs, files
}

// PackDestConflicts reports true collisions among contributions of this kind.
//
// Multiple contributions targeting the same directory or nesting inside a directory
// merge automatically into a single staged directory mount (planPackFilesMounts), preventing runc
// read-only file system (EROFS) and duplicate mount destination errors. A conflict is reported
// only for true file collisions (different content for the same relative path) or invalid nesting
// inside a single-file destination.
//
// Keyed on a KIND so the check is reusable, but only `files` is wired to it today.
func PackDestConflicts(packs []*packload.Pack, kind packdecl.Kind) []string {
	if kind != packdecl.KindFiles {
		return nil
	}
	plan := planPackFilesMounts(packs, everyPatchedTree(packs), "")
	return plan.Conflicts
}

func packDestConflicts(packs []*packload.Pack, kind packdecl.Kind) []string {
	return PackDestConflicts(packs, kind)
}

// PackFilesShadowedSurfaces reports every config surface whose path falls INSIDE a `files`
// destination — a conflict that podman cannot see and that kills the boot with an error
// naming neither culprit.
func PackFilesShadowedSurfaces(packs []*packload.Pack) []string {
	return packFilesShadowedSurfaces(packs)
}

// packFilesShadowedSurfaces reports every config surface whose path falls INSIDE a `files`
// destination — a conflict that podman cannot see and that kills the boot with an error
// naming neither culprit.
//
// The mechanism: a `files` claim is a :ro bind mount over its whole destination directory, so
// a surface the entrypoint must WRITE beneath that path hits a read-only filesystem. It is
// not a duplicate-mount collision (the paths differ), so packDestConflicts misses it, and it
// is not visible to the pack author either — `pack lint` sees two legal claims.
//
// Found by running the real thing: a pack declaring `files → .claude` alongside claude's own
// `~/.claude/settings.json` surface produced
//
//	configure_claude_settings: open /home/agent/.claude/settings.json: read-only file system
//
// which is an A12 boot refusal pointing at the surface rather than at the `files` claim that
// caused it. Cross-pack too — the shadowing pack and the surface's owner are usually
// different packs, so neither author can see the problem alone.
//
// Reported rather than resolved: yolo could exclude the surface's path from the mount, but a
// pack claiming a directory an agent actively writes is a design mistake in the pack, and
// silently working around it would hide that. The remedy is a narrower `into`.
func packFilesShadowedSurfaces(packs []*packload.Pack) []string {
	// Collect the config surfaces every pack renders into the home, by home-relative path.
	type owned struct{ pack, surface, path string }
	var surfaces []owned
	for _, p := range packs {
		decl, problems := p.Surfaces()
		if len(problems) > 0 {
			continue // a malformed surface is reported by the render path, not here
		}
		for _, s := range decl {
			rel := strings.TrimPrefix(s.Path, "~/")
			if rel == s.Path {
				continue // not home-relative; a files mount cannot shadow it
			}
			surfaces = append(surfaces, owned{p.Name, s.Agent + "/" + s.Name, rel})
		}
	}

	var out []string
	// Over the RESOLVED targets, not the raw contributions: an addressed `files` contribution
	// has `Into == ""` and would be invisible here, and a slot makes no mount at all. Every patched
	// extension counts as delivered here, since any launch may mount one at its `into`.
	for _, t := range packFilesTargets(packs, everyPatchedTree(packs)) {
		dir := strings.TrimSuffix(t.Dest, "/") + "/"
		for _, s := range surfaces {
			if !strings.HasPrefix(s.path, dir) {
				continue
			}
			out = append(out, fmt.Sprintf(
				"pack %s claims ~/%s as a `files` tree, which is mounted READ-ONLY — but "+
					"pack %s renders the config surface %s at ~/%s, inside it. The jail "+
					"would refuse to start (read-only file system) with an error naming the "+
					"surface, not this claim. Narrow the `files` into a subdirectory the "+
					"agent does not write.",
				t.Pack, strings.TrimSuffix(t.Dest, "/"), s.pack, s.surface, s.path))
		}
	}
	sort.Strings(out)
	return out
}

// everyPatchedTree marks every patched extension packs carry as delivered, for a check that asks
// where a launch MAY mount one rather than where this one did (packFilesShadowedSurfaces).
func everyPatchedTree(packs []*packload.Pack) map[string]string {
	out := map[string]string{}
	for _, f := range packload.PatchedTrees(packs) {
		out[f.Key()] = "(a patched extension's tree)"
	}
	return out
}

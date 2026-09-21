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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
func packFilesTargets(packs []*packload.Pack) []packFilesTarget {
	// The SLOT table first: an agent pack's files DESTINATION (`agent` + `into`, no `from`) names
	// where addressed content lands. Built in one pass so a contribution's POSITION cannot change
	// which slot it resolves against.
	//
	// ONE destination per agent is the rule, not a property of this map (pi-pack-extensions.md §8
	// invariant 1 / OQ-1), and it is enforced where a load error belongs: packdecl's
	// validateFilesDestinations, which every HOST read runs. The map keeps the last declaration
	// because that is all it can do — this notch decodes tolerantly (packload.TolerateSkew), so a
	// pack staged by a newer host than the baked entrypoint can still reach it, and silently
	// honoring one of two is strictly better here than refusing the boot.
	aliasByAgent := map[string]string{}
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind == packdecl.KindFiles && c.Agent != "" && c.Into != "" {
				aliasByAgent[c.Agent] = c.Into
			}
		}
	}
	var out []packFilesTarget
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindFiles {
				continue
			}
			// A DESTINATION is a bare slot: it ships no content, so it makes no mount. That is
			// what keeps the slot root itself raw — every tree lands UNDER it, namespaced by the
			// contributing pack, the owner's own included (design §3).
			if c.Agent != "" {
				continue
			}
			src := filepath.Join(p.Root, filepath.FromSlash(c.From))
			switch {
			case c.Into != "":
				// A plain tree the pack owns, at the path it named.
				out = append(out, packFilesTarget{
					Pack: p.Name, Src: src, Dest: c.Into, Root: p.Root, From: c.From,
				})
			case len(c.Agents) > 0:
				// ADDRESSED: the destination is the agent pack's slot, namespaced by the
				// CONTRIBUTING pack so two packs shipping a same-named file cannot collide. The
				// jail resolves it here rather than through packload.ResolveDestinations, because
				// the jail never calls that — packload's borrowing is the HOST notch.
				//
				// THE LANDING PATH ITSELF comes from packload.SlotLanding, the one authority both
				// notches read. Spelling the join here was how the two came to disagree: this side
				// joined and the host side did not, so one pack.json delivered to two paths.
				for _, a := range c.Agents {
					alias, ok := aliasByAgent[a]
					if !ok {
						// The agent is enabled but declares no slot; delivered nowhere, and
						// reported by the pre-flight, not silently dropped here.
						continue
					}
					out = append(out, packFilesTarget{
						Pack: p.Name, Src: src,
						Dest: packload.SlotLanding(c.Kind, alias, p.Name),
						Root: p.Root, From: c.From,
					})
				}
			}
		}
	}
	return out
}

// packFilesMountArgs emits one `-v <staged tree>:/home/agent/<into>:ro` per `files`
// contribution.
//
// :ro is the contract, not tidiness: a `files` claim is CombineExclusive — the pack owns
// the path — so the jail reads the tree and nobody writes it. A writable bind would let
// an agent edit content the next launch silently reverts.
//
// Two cases the emitter has to split on, both of them "or it vanishes silently":
//
//   - Apple Container is handed a COPY rather than a single-FILE bind (apple/container#1089
//     is false on 1.1.0, measured; the copy needs no version gate) — the same treatment
//     that already routes yolo-user-env.sh and every briefing through acMaterialize. A
//     `files` contribution naming one file is therefore COPIED into ws_state (which AC
//     mounts wholesale at /home/agent) instead of mounted. A directory needs no such
//     dance: AC nests dir mounts under /home/agent fine (GlobalCache at .cache proves
//     it), so only the single-file case diverges.
//   - An ABSENT source is skipped WITH A WARNING rather than mounted: podman kills the
//     whole container with a bare "statfs …: no such file or directory" on a missing bind
//     source, and an `only`/`exclude` filter that dropped the tree is an ordinary (if
//     usually mistaken) user config, not a reason to refuse the launch. The warning is
//     what keeps this from being another silent drop. Which warning it is, is
//     packFilesSkipWarning's decision — the two causes need different reactions.
func (o *Options) packFilesMountArgs(in *assembleInput) []string {
	var args []string
	for _, t := range packFilesTargets(in.packs) {
		switch {
		case isDir(t.Src):
			args = append(args, "-v", t.Src+":/home/agent/"+t.Dest+":ro")
		case isFile(t.Src):
			if in.rt == "container" {
				acMaterialize(t.Src, t.Dest, in.wsState)
				continue
			}
			args = append(args, "-v", t.Src+":/home/agent/"+t.Dest+":ro")
		default:
			o.pr(o.Stdout).print("[yellow]" + packFilesSkipWarning(t) + "[/yellow]")
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
// read-only source bind. The target may live in GlobalHome or in the workspace overlay:
// preparePackFiles owns the latter (including retirement), while preparePackFilesGlobal
// handles the machine-wide base.
//
// Same belt-and-braces as writable_home_dirs and host_files (prepareWsState,
// prepareHostFiles) and for the same reason: the OCI runtime does not reliably create a
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
func preparePackFiles(packs []*packload.Pack, wsState, rt string) []string {
	manifestPath := filepath.Join(filepath.Dir(wsState), packFilesMountpointManifestName)
	previous := loadPackFilesMountpointManifest(manifestPath)
	current := map[string]packFilesTarget{}
	if rt != "macos-user" { // parity: NotApplicable — macos-user does not deliver `files` contributions.
		writable := packload.WritableDirs(packs)
		for _, t := range packFilesTargets(packs) {
			if rel, ok := packFilesWorkspaceRel(t.Dest, writable, rt); ok {
				current[rel] = t
			}
		}
	}
	var archived []string
	migrationApplicable := rt != "macos-user" && hasSingleFilePackTarget(current) // parity: NotApplicable — macos-user does not deliver `files` contributions.
	if migrationApplicable && previous.Version < packFilesMountpointManifestVersion {
		archived = archiveLegacyPackFileMountpoints(wsState, current)
	}

	// Retire first, so a contribution changing shape at one destination can recreate the
	// right target below. Removal is deliberately conditional on the recorded scaffold
	// still being unchanged: a file the user replaced or a directory that gained content
	// is forgotten and left alone.
	for rel, owned := range previous.Entries {
		if !safePackFilesManifestRel(rel) {
			delete(previous.Entries, rel)
			continue
		}
		if claimed, stillClaimed := current[rel]; stillClaimed && packFilesTargetKind(claimed) == owned.Kind {
			continue
		}
		dest := filepath.Join(wsState, rel)
		if pathParentWithin(dest, wsState) && packFilesMountpointUnchanged(dest, owned) {
			_ = os.Remove(dest)
		}
		delete(previous.Entries, rel)
	}

	next := &packFilesMountpointManifest{
		Version: previous.Version,
		Entries: map[string]packFilesMountpoint{},
	}
	if migrationApplicable {
		next.Version = packFilesMountpointManifestVersion
	}
	for rel, t := range current {
		dest := filepath.Join(wsState, rel)
		kind := packFilesTargetKind(t)
		if kind == "" {
			continue
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

		_, statErr := os.Lstat(dest)
		created := os.IsNotExist(statErr)
		if created {
			_ = os.MkdirAll(filepath.Dir(dest), 0o755)
			if kind == "dir" {
				_ = os.Mkdir(dest, 0o755)
			} else {
				f, err := os.OpenFile(dest, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
				if err == nil {
					_ = f.Close()
				}
			}
		}

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

func hasSingleFilePackTarget(targets map[string]packFilesTarget) bool {
	for _, t := range targets {
		if isFile(t.Src) {
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
func archiveLegacyPackFileMountpoints(wsState string, current map[string]packFilesTarget) []string {
	claimed := map[string]struct{}{}
	dirs := map[string]struct{}{}
	for rel, t := range current {
		claimed[filepath.Clean(rel)] = struct{}{}
		if isFile(t.Src) {
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

func archiveLegacyPackFileMountpoint(wsState, rel string) (string, error) {
	dest := filepath.Join(filepath.Dir(wsState), "archive", "pack-files", "legacy", rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	base := dest
	for i := 2; ; i++ {
		if _, err := os.Lstat(dest); os.IsNotExist(err) {
			break
		}
		dest = fmt.Sprintf("%s.%d", base, i)
	}
	if err := os.Rename(filepath.Join(wsState, rel), dest); err != nil {
		return "", err
	}
	return dest, nil
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

// pathParentWithin rejects a stale ownership record whose parent now escapes the
// workspace overlay through a symlink. The record is weak evidence; it never authorizes
// removing a path outside the tree that contains it.
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

func loadPackFilesMountpointManifest(path string) *packFilesMountpointManifest {
	m := &packFilesMountpointManifest{Entries: map[string]packFilesMountpoint{}}
	body, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(body, m) != nil || m.Entries == nil {
		m.Entries = map[string]packFilesMountpoint{}
	}
	return m
}

func savePackFilesMountpointManifest(path string, m *packFilesMountpointManifest) error {
	body, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(body, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
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

func preparePackFilesGlobal(packs []*packload.Pack, rt string) {
	for _, t := range packFilesTargets(packs) {
		if rt == "macos-user" || rt == "container" || pathUnderAny(t.Dest, packload.WritableDirs(packs)) { // parity: HonoredBy — those backends/paths are prepared in the workspace overlay above.
			continue
		}
		dest := filepath.Join(paths.GlobalHome(), filepath.FromSlash(t.Dest))
		if isDir(t.Src) {
			_ = os.MkdirAll(dest, 0o755)
			continue
		}
		if isFile(t.Src) {
			_ = os.MkdirAll(filepath.Dir(dest), 0o755)
			touchFile(dest)
		}
		// Absent source: nothing is mounted (packFilesMountArgs warns), so there is no
		// mountpoint to provision.
	}
}

// packDestConflicts reports every home destination that more than one contribution of
// this kind claims, naming the packs involved.
//
// THE POINT is which error the user sees. The assembler emits one bind per contribution
// with no dedup by destination, so two claims on one `into` reach podman as "duplicate
// mount destination" and kill the boot with a runtime error naming neither pack
// (pack-system.md §14's known sharp edge). `files` is CombineExclusive — a second
// claimant is ALREADY a footprint violation — so it is reported here, before the
// container exists, with both pack names in the message.
//
// Keyed on a KIND so the check is reusable, but only `files` is wired to it today. The
// identical podman failure exists for two `skills` contributions sharing an `into`, and
// there it is a DIFFERENT bug: skills are a designed merge, so the fix is mount dedup,
// not a collision error. Deliberately out of scope (plan OQ-C,
// project_pack_tooling_gaps).
func packDestConflicts(packs []*packload.Pack, kind packdecl.Kind) []string {
	// Claim count and claimant set per destination, keeping first-seen order so the
	// report is deterministic.
	type claim struct {
		count int
		packs []string
		seen  map[string]struct{}
	}
	byDest := map[string]*claim{}
	var order []string
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != kind || c.Into == "" {
				continue
			}
			cl := byDest[c.Into]
			if cl == nil {
				cl = &claim{seen: map[string]struct{}{}}
				byDest[c.Into] = cl
				order = append(order, c.Into)
			}
			cl.count++
			if _, dup := cl.seen[p.Name]; !dup {
				cl.seen[p.Name] = struct{}{}
				cl.packs = append(cl.packs, p.Name)
			}
		}
	}

	var out []string
	for _, dest := range order {
		cl := byDest[dest]
		if cl.count < 2 {
			continue
		}
		// A single pack claiming one path twice is the same fatal duplicate mount, so it
		// is reported too — with wording that does not pretend there is a second pack.
		who := fmt.Sprintf("pack %s declares %d %q contributions", cl.packs[0], cl.count, kind)
		if len(cl.packs) > 1 {
			sorted := append([]string(nil), cl.packs...)
			sort.Strings(sorted)
			who = fmt.Sprintf("packs %s each declare a %q contribution",
				strings.Join(sorted, " and "), kind)
		}
		out = append(out, fmt.Sprintf(
			"%s at ~/%s — %s is sole-owned (one claimant per path), and two binds at one "+
				"destination fail the container at boot with a duplicate-mount-destination "+
				"error. Give them different `into` paths, or drop one.",
			who, dest, kind))
	}
	return out
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
	// has `Into == ""` and would be invisible here, and a slot makes no mount at all.
	for _, t := range packFilesTargets(packs) {
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

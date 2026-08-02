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
	"fmt"
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
// Unlike skills/briefing — where `from` is decorative, because the stager reads the
// conventional skills/ dir and root AGENTS.md regardless (pack-system.md §14) — `files`
// HONORS `from`: there is no convention for an opaque tree, so the declaration is the
// only thing that can name it.
type packFilesTarget struct {
	Pack string
	Src  string // absolute host path: <staged pack root>/<from>
	Dest string // home-relative, as declared (validated relative, no "..", no ":")
}

// packFilesTargets resolves every loaded pack's `files` contributions, in declaration
// order. Deterministic without sorting: contributions come from an ordered manifest list
// and packs from the ordered config list, unlike the map-derived emitters (env, cache
// relocations) that have to sort.
//
// No origin gate: a `files` tree is the PACK's own content, not a host read, so a fetched
// pack delivers it exactly like an embedded one. The trust question a fetched pack raises
// here is what packstage's exec-bit refusal and `allow_exec` already answer.
func packFilesTargets(packs []*packload.Pack) []packFilesTarget {
	var out []packFilesTarget
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindFiles {
				continue
			}
			out = append(out, packFilesTarget{
				Pack: p.Name,
				Src:  filepath.Join(p.Root, filepath.FromSlash(c.From)),
				Dest: c.Into,
			})
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
//   - Apple Container cannot bind a single FILE (apple/container#1089) — the same limit
//     that already routes yolo-user-env.sh and every briefing through acMaterialize. A
//     `files` contribution naming one file is therefore COPIED into ws_state (which AC
//     mounts wholesale at /home/agent) instead of mounted. A directory needs no such
//     dance: AC nests dir mounts under /home/agent fine (GlobalCache at .cache proves
//     it), so only the single-file case diverges.
//   - An ABSENT source is skipped WITH A WARNING rather than mounted: podman kills the
//     whole container with a bare "statfs …: no such file or directory" on a missing bind
//     source, and an `only`/`exclude` filter that dropped the tree is an ordinary (if
//     usually mistaken) user config, not a reason to refuse the launch. The warning is
//     what keeps this from being another silent drop.
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
			o.pr(o.Stdout).print("[yellow]Warning: pack " + t.Pack + " declares a `files` tree " +
				"that is not in its staged content, skipping: " + t.Dest +
				" (nothing staged at " + t.Src + " — check the pack's `from`, and any " +
				"only/exclude filters)[/yellow]")
		}
	}
	return args
}

// preparePackFiles creates each `files` destination's mountpoint inside GlobalHome,
// before the :ro home bind is applied.
//
// Same belt-and-braces as writable_home_dirs and host_files (prepareWsState,
// prepareHostFiles) and for the same reason: the OCI runtime does not reliably create a
// mountpoint inside a :ro bind. podman 5.8.4/crun 1.27.1 does auto-create one (verified
// — see project_ro_home_mount_autocreate), but the maintainer hit the EROFS path on their
// stack, where it surfaces as the unreadable `conmon bytes "": readObjectStart`. Nesting
// under a pack's WRITABLE state dir (e.g. `.claude/fkdir` when the claude pack owns
// `.claude`) auto-creates fine either way; this covers the destination that lands
// straight on the :ro base.
//
// The leaf type has to match the source: a dir mount over a file (or the reverse) aborts
// container creation. Best-effort — a failure here degrades to whatever the runtime does
// on its own, which on the tested stack is the working auto-create.
//
// MkdirAll cannot escape GlobalHome: packdecl validated every `into` as relative, with no
// ".." segment and no ":".
func preparePackFiles(packs []*packload.Pack) {
	for _, t := range packFilesTargets(packs) {
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
	for _, p := range packs {
		for _, c := range p.Decl.Contributions() {
			if c.Kind != packdecl.KindFiles || c.Into == "" {
				continue
			}
			dir := strings.TrimSuffix(c.Into, "/") + "/"
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
					p.Name, strings.TrimSuffix(c.Into, "/"), s.pack, s.surface, s.path))
			}
		}
	}
	sort.Strings(out)
	return out
}

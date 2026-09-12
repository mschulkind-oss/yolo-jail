package run

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// packhostgrants.go mounts what a PACK declared it may read from the host — the
// `host_files` and `mount` contribution kinds — read-only under /ctx.
//
// The file was `hostclaude.go` until 2026-08-17, from when this was a per-agent constant
// in the Go registry and claude was the only entry. Nothing in it has been claude-specific
// since: it reads pack declarations and switches on nothing (pack-code-separation.md §3.5).
// The §3.5 ruling said `hostfiles.go`, and that name was already taken by a DIFFERENT
// mechanism — the user's `host_files` config key, which resolves user-scope entries into
// /ctx/host-user/<slug> and is gated by config rather than by pack origin. Two host-file
// paths with one filename between them is exactly the confusion the rename was meant to
// end, so this took the `packhost*` prefix its sibling tests already use.
//
// hostFileArgs mounts each pack's DECLARED host files read-only under /ctx.
//
// THE CREDENTIAL BOUNDARY, and what enforces it is NOT what this comment said until
// 2026-09-09. It used to be a fixed per-agent constant in the Go registry, unwidenable by
// config (which is what retiring host_claude_files/host_pi_files bought). It then became a
// pack declaration gated on the pack's content ORIGIN — embedded yes, fetched refused.
//
// THAT ORIGIN GATE NO LONGER EXISTS. OQ-TP9 (docs/design/trust-paths.md) deleted it on
// 2026-09-04 as theatre: selecting a pack means writing user-scope config as the host user,
// and `packs` is inexpressible at workspace scope by construction, so the gate refused an
// actor who had already passed a stronger one — gate-placement-principle.md's Test 1.
// HonoredHostFiles now returns every declaration and refuses nothing; its always-nil
// `refused` return is vestigial.
//
// So the boundary today is DISCLOSURE, not consent: every grant is enumerated on the launch
// banner and by `yolo pack footprint`, and what keeps a hostile declaration out is that
// adding one requires user-scope config access in the first place.
//
// ⚠ Do not read the absence of a gate here as an absence of enforcement, and do not
// re-add one without re-reading OQ-TP9 — a refusal that duplicates a stronger upstream
// check is the exact shape that ruling deleted.
//
// Still no config key is read and no YOLO_HOST_*_FILES env is emitted.
func (o *Options) hostFileArgs(in *assembleInput) []string {
	var args []string
	for _, p := range in.packs {
		// HonoredHostFiles refuses NOTHING (OQ-TP9 retired the origin gate 2026-09-04) —
		// the second return is always nil. An empty grant here means the pack declared no
		// host files, never that one was withheld.
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			hostFile := filepath.Join(homeDir(), filepath.FromSlash(hf.From))
			if !isFile(hostFile) {
				// An absent host file is a NORMAL state (the user has not created it),
				// and the surface falls back to its defaults layer. Mounting a missing
				// source would kill the container with a bare statfs error.
				//
				// NOT RECORDED as delivered, which is the whole of what makes the jail's
				// read able to fail closed: this is the one case where nothing arriving is
				// correct, and it is decided HERE, by the half that can see the user's home.
				continue
			}
			// packload.CtxPath is THE definition of where this lands, shared with the
			// entrypoint's host-layer read. Two copies of this derivation would silently
			// compose the wrong user file (or none) into the surface.
			//
			// StagedSlug, NOT p.Name — one definition is not enough if the two sides feed
			// it different strings, which is exactly what happened until 2026-09-05. The
			// jail names a pack from its staged directory because that is all it has, and
			// the host stages a configured pack under config.PackEntry.Slug, so a name
			// carrying anything outside [A-Za-z0-9.-] mounted here and was read there.
			dest := packload.CtxPath(p.StagedSlug(), hf)
			// APPLE CONTAINER CANNOT BIND A SINGLE FILE (apple/container#1089), and this
			// grant is always exactly one file. Left as a bind it does not error — it
			// silently does not arrive, and the surface then composes from its defaults
			// layer because the entrypoint reads the host layer fail-open
			// (packsurfaces.go hostSurfaceBytes). The user's whole ~/.claude/settings.json
			// disappears from the composition with nothing in the launch to say so —
			// while the disclosure line still prints "reads-host .claude/settings.json",
			// which is the part that makes it worse than an omission.
			//
			// Same answer the other five single-file sites already reached: copy it into
			// wsState (which IS /home/agent on this backend) and tell the entrypoint where
			// to look. The read side was already parameterized for tests; YOLO_CTX_ROOT
			// promotes that seam to a production one.
			//
			// RECORDED AS DELIVERED EVEN THOUGH acMaterialize SWALLOWS ITS ERRORS, and
			// that is the point rather than an oversight: a copy that fails is exactly the
			// silent-wrong-composition this record exists to turn loud. The launcher says
			// what it undertook to do; the jail reports what it found.
			in.hostLayersDelivered = append(in.hostLayersDelivered, dest)
			if in.rt == "container" {
				acMaterialize(hostFile, filepath.Join(acCtxDirRel,
					filepath.FromSlash(strings.TrimPrefix(dest, packload.CtxRoot+"/"))), in.wsState)
				in.acCtxMaterialized = true
				continue
			}
			args = append(args, ROFileMountArg(
				hostFile, dest, in.wsState,
				"ctx-"+strings.ReplaceAll(strings.TrimPrefix(dest, "/ctx/"), "/", "-"),
				in.mountTargets, nil)...)
		}
	}
	return args
}

// hostLayerEnv emits the launcher's host-layer report — what a `readsHost` surface's
// bytes did on this launch — for the entrypoint's fail-closed read
// (packload.HostLayerReport states the contract and why the jail cannot derive it).
//
// EMITTED ON EVERY LAUNCH, including the one that delivered nothing, and that is the
// property the whole mechanism rests on: an absent variable then means "launcher older
// than the variable" and nothing else, so the jail can tolerate absence without
// tolerating a real delivery failure. Emitting it only when something crossed would make
// those two indistinguishable again, one level up.
//
// This is the CONTAINER path, so Delivery is always "supported" — every backend that
// reaches here can carry a host file, Apple Container by copying it into the home rather
// than binding it. The macos-user arm reports "unsupported" from its own plan builder
// (internal/macosuser/runplan.go), which is the one backend with no mechanism at all.
func (o *Options) hostLayerEnv(in *assembleInput) []string {
	wire, err := packload.HostLayerReport{
		Delivery:  packload.HostLayersSupported,
		Delivered: in.hostLayersDelivered,
	}.Marshal()
	if err != nil {
		// Unreachable for a []string, and silence would be the wrong failure anyway: no
		// variable means "unknown" in the jail, so the read degrades to the fail-open
		// behaviour that shipped before it rather than refusing anything.
		o.pr(o.Stdout).print("[yellow]Warning: host layers: " + err.Error() +
			" — the jail cannot check that they arrived[/yellow]")
		return nil
	}
	return []string{"-e", packload.HostLayerEnvVar + "=" + wire}
}

// acCtxDirRel is where Apple Container's materialized /ctx host-file copies live,
// relative to the home (= wsState on that backend). A dotted name because it sits in
// the agent's home rather than in a mount namespace it cannot see.
const acCtxDirRel = ".yolo-ctx"

// hostMountArgs mounts each pack's DECLARED `mount` contributions read-only under
// /ctx. Same credential boundary as hostFileArgs — which since OQ-TP9 means DISCLOSURE
// rather than an origin gate; HonoredMounts also refuses nothing, for the stated reason
// that a mount reads the host home exactly like a host file. The difference from
// hostFileArgs is shape, not authority: the source may be a whole DIRECTORY and the
// destination is the pack's chosen /ctx path rather than a config-surface feed.
//
// A directory is mounted directly (a dir source is not the single-file nested-bind
// case ROFileMountArg guards against); a single-file mount reuses ROFileMountArg so
// the nested-jail inode-copy dance still applies. An absent source is skipped rather
// than mounted — a missing bind source kills the container with a bare statfs error.
//
// On Apple Container BOTH forms are dropped with a reason, via roBindsUnsupported —
// the same rule the config `mounts` key has always applied, reached from here at last.
// A `mount` is the one host grant with no relocation available: `reads-host` and
// `host_files` materialize into .yolo-ctx because their reader is the ENTRYPOINT,
// which YOLO_CTX_ROOT can redirect. This grant's reader is the AGENT, following the
// /ctx path its own briefing names, so there is nowhere else to put it.
func (o *Options) hostMountArgs(in *assembleInput) []string {
	var args []string
	for _, p := range in.packs {
		granted, _ := p.HonoredMounts()
		for _, mt := range granted {
			src := filepath.Join(homeDir(), filepath.FromSlash(mt.From))
			dest := "/ctx/" + strings.TrimPrefix(mt.To, "/")
			if reason := roBindsUnsupported(in.rt); reason != "" && (isDir(src) || isFile(src)) {
				o.pr(o.Stdout).print("[yellow]Skipping pack " + p.Name + " mount ~/" +
					mt.From + " → " + dest + ": " + reason + "[/yellow]")
				continue
			}
			switch {
			case isDir(src):
				args = append(args, "-v", src+":"+dest+":ro")
			case isFile(src):
				args = append(args, ROFileMountArg(
					src, dest, in.wsState,
					"ctx-"+strings.ReplaceAll(strings.TrimPrefix(dest, "/ctx/"), "/", "-"),
					in.mountTargets, nil)...)
			default:
				// Absent source: skip. The pack's content simply is not present; a
				// missing bind source would otherwise abort the container start.
				o.pr(o.Stdout).print("[yellow]Warning: pack " + p.Name + " mount source " +
					"does not exist, skipping: ~/" + mt.From + "[/yellow]")
			}
		}
	}
	return args
}

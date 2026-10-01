package run

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// macosctxtree.go builds the CONTEXT TREE: the host bytes a `/ctx` mount carries on
// every other backend, laid out host-side at the paths the jail will open, ready to be
// staged root-owned and read-only under /var/yolo-jail.
//
// It is DP-L1 (docs/design/declaration-parity.md §6.1, ruled by OQ-DP4: "yes, build,
// however we can make it work"). Two declarations were accepted, validated and then
// silently dropped on macos-user — a pack's `reads-host` grant, which rendered its
// surface from DEFAULTS so the agent ran on a settings file the human did not write; and
// a source-bearing `host_files` entry, which was filtered out of the wire entirely. Both
// crossed on a bind mount, and this backend has no mounts.
//
// # Why a copy, and why not a symlink
//
// The mechanism is a COPY, and Seatbelt was never what ruled the alternative out.
// Measured on hardware 2026-09-13 (macOS 26.5, arm64), §6.1 probe 1: under a profile
// denying reads beneath one directory, `cat` of a symlink sitting in an ALLOWED directory
// and pointing into the denied one returns `Operation not permitted` — for an absolute
// link and a relative one alike, with three controls behaving. So Seatbelt evaluates the
// TARGET, and a link into the invoking user's home would need a per-source allow. That
// half is fixable. The DAC half is not fixable by any profile: the sandbox runs as a
// foreign uid (macosuser.SandboxUser) and a macOS home is not required to be
// world-traversable, so the open fails at the POSIX layer before the profile is consulted.
//
// # Why here and not in the backend
//
// ⚠ THE READ IS THE CREDENTIAL BOUNDARY, which is why this file is in the run pipeline
// and not in internal/macosuser. Composing the tree means reading the USER's own config
// (config.LoadHostFiles goes to ~/.config/yolo-jail directly — that is what makes a
// source-bearing entry inexpressible at workspace scope) and reading files out of the
// invoking user's home. macosuser.BuildRunPlan is PURE by contract, so that it can render
// a --dry-run plan without touching disk, and OQ-DP4's ledger row states the constraint
// the ruling does not override: the copy runs in the host CLI, never in the plan builder.
//
// # What is NOT here
//
// Directory-shaped deliveries. Config `mounts`, a pack `mount` grant and a directory
// `host_files` entry can each name an arbitrary user tree, and a copy does not scale to
// one — this repo's own yolo-jail.jsonc mounts a growing log directory. Those are DP-D15,
// ruled separately ("we can't do /ctx by copying, some of these directories are huge").
//
// ⚠ TWO OF THE THREE NOW REFUSE THE LAUNCH, and the third is still named. Config `mounts`
// and pack `mount` grants never reach this composer: their readers are container-side (the
// assembler's `mounts` loop and hostMountArgs, both through ctxmounts.go), so on this
// backend they were accepted, validated and dropped — first in silence, then behind a
// warning the maintainer ruled not enough (DP-D15: "a fatal error on setups that don't
// support it rather than having it be surprisingly not there with an easily missed
// warning"). refuseMacosUserCtxMounts below is that refusal, run at the top of the
// macos-user arm. A directory `host_files` entry is not a context mount (context-mounts.md's
// Defined terms name two: config `mounts` and pack `mount`), and it still reaches this
// function and comes back in undeliveredDirs for noteMacosUserHostByteGaps to print.
//
// STILL A GAP THE REFUSAL DOES NOT CLOSE: the single-FILE form of a pack `mount` is a copy
// this tree would scale to perfectly well, so refusing it is the honest interim rather than
// the answer (CX-D10). DP-D15 rules only the directory-shaped delivery out.
//
// One destination over, noted here because this is where a reader comes looking: a pack
// `files` contribution lands in the HOME rather than /ctx, so it belongs to the home overlay
// and not to this tree. The overlay copies each files tree beside the skills and briefings
// (macoshomeoverlay.go, reading packFilesTargets), which is how packs/pi's
// `extensions/yolo-openai-auth.js` reaches a macos-user home.

// macosCtxTreeLeaf is the staging-dir subdir the tree is composed into. It sits beside
// the home overlay's own leaf, in the same per-jail staging dir, because both are
// host-side scratch that a launch rebuilds from scratch every time.
const macosCtxTreeLeaf = "ctx-tree"

// macosCtxDelivery is what one launch's composition produced: the tree and its record
// (macosuser.HostContext), plus the entries it could NOT carry.
type macosCtxDelivery struct {
	ctx macosuser.HostContext
	// undeliveredDirs are the destination paths of source-bearing `host_files` entries
	// whose source is a DIRECTORY. They are returned rather than warned about here so the
	// launch has exactly one printer for what did not cross
	// (noteMacosUserHostByteGaps) — a second one would be the OQ-BP-3 shape, a warning
	// the reader learns to skip because two of them say overlapping things.
	undeliveredDirs []string
}

// buildMacosCtxTree composes the tree under `staging` and returns what it delivered.
//
// It reads the SAME two declaration lists the container path mounts from — every pack's
// HonoredHostFiles and config.LoadHostFiles' source-bearing half — and lays each one out
// at the SAME destination the container assembler would have bound it to
// (packload.CtxPath, and /ctx/host-user/<slug>). A third derivation of either path here
// is what would let the two backends disagree silently, which is the bug class
// packload.CtxPath's own doc comment exists to close.
//
// FAIL-CLOSED on a copy that was attempted and failed, fail-open on a source that is not
// there. The two are different facts: an absent host file is the overwhelmingly common
// case and the surface correctly composes from its lower layers, while a source that
// exists and could not be copied means the launch would deliver a config file that looks
// like the human's and is not. The second returns an error and the caller ends the launch.
func (o *Options) buildMacosCtxTree(staging string, packs []*packload.Pack,
	cfg *jsonx.OrderedMap) (macosCtxDelivery, error) {
	var out macosCtxDelivery
	tree := filepath.Join(staging, macosCtxTreeLeaf)

	// WHAT NO TREE CAN CARRY is not said here any more: a declared context mount (config
	// `mounts`, a pack `mount`) refuses the launch at the top of the arm
	// (refuseMacosUserCtxMounts), long before a tree is composed. The directory `host_files`
	// line is still noteMacosUserHostByteGaps', printed once the caller has this delivery.

	// Rebuilt from scratch every launch, for buildMacosHomeOverlay's reason and one
	// sharper: a grant the user REVOKED — a pack dropped from `packs`, a `host_files`
	// entry deleted from their config — must stop being delivered, and a tree that only
	// ever accumulated would keep composing a host file into a surface whose declaration
	// is gone. The staged copy is replaced by rename for the same reason on the other
	// side of the boundary (macosuser.StageCtxCommands).
	if err := os.RemoveAll(tree); err != nil {
		return out, fmt.Errorf("clearing the macos-user context tree: %w", err)
	}

	wrote := false

	// --- Pack `reads-host` grants -------------------------------------------------
	//
	// StagedSlug, never Pack.Name: the jail names a pack from the directory it was
	// staged into, and a name carrying anything outside [A-Za-z0-9.-] is escaped on the
	// way there. Keying on the name mounted at one path and read at another for months
	// (measured 2026-09-05) — the same trap, and the reason both sides call CtxPath.
	for _, p := range packs {
		if p == nil {
			continue
		}
		// The second return is always nil since OQ-TP9 retired the origin gate; an empty
		// list here means the pack declared no host files, never that one was withheld.
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			src := filepath.Join(homeDir(), filepath.FromSlash(hf.From))
			dest := packload.CtxPath(p.StagedSlug(), hf)
			if !isFile(src) {
				// NOT RECORDED as delivered, and that is the whole of what lets the
				// jail's read fail closed: this is the one case where nothing arriving is
				// correct, and it is decided HERE, by the half that can see the home. The
				// container arm's line names anything but an absent file, a dangling link
				// above all (hostsourceunread.go), and so does this one.
				o.noteUnreadHostSource(hostFileSubject(p.Name, hf.From),
					"The sandbox composes without it", unreadHostSource(src))
				continue
			}
			if err := copyCtxFile(src, tree, dest); err != nil {
				return out, fmt.Errorf("staging pack %s's host file ~/%s for the sandbox: %w",
					p.Name, hf.From, err)
			}
			out.ctx.Delivered = append(out.ctx.Delivered, dest)
			// AND WHAT THOSE BYTES ARE — the container launcher's own question, asked with its
			// own call (hostLayerIsRender, packhostgrants.go), so the two backends cannot read
			// one home differently. A surface yolo has already rendered into this home is
			// labelled a render, and the jail keeps it as a baseline instead of folding yolo's
			// own keys and entries back in as the user's ([OQ-CR6]; render-mark parity,
			// notch-scoped-config-contributions.md NS-D3). Without it this was the one backend
			// on which a host-only entry `yolo host apply` wrote reached a jail.
			if hostLayerIsRender(p, dest) {
				out.ctx.Rendered = append(out.ctx.Rendered, dest)
			}
			wrote = true
		}
	}

	// --- The user's own `host_files` ----------------------------------------------
	//
	// probeSource is `!o.inJail()`, the SAME rule the container arm applies, because the
	// reason for it is the same on both: a host path is deliberately not in a jail's mount
	// namespace, so stat'ing one from a nested run turns a perfectly valid host config into
	// a fatal error. On a real host it earns its keep here — the probe is what catches a
	// source whose KIND contradicts the destination (a directory behind a file entry), and
	// without it such an entry would fall through the file branch below and be skipped in
	// silence, which is indistinguishable from "the user has not created it yet".
	entries, err := config.LoadHostFiles(cfg, func(msg string) {
		o.pr(o.Stderr).printf("[yellow]Warning: %s[/yellow]", msg)
	}, !o.inJail())
	if err != nil {
		// READ FAIL-OPEN, like every other reader of this key: the entrypoint renders
		// each entry fail-open anyway, and a jail that starts without one composed file
		// is the feature degrading rather than the launch running against the wrong
		// bytes. The container arm makes the same call and the same choice.
		o.pr(o.Stderr).printf("[yellow]Warning: host_files: %s — no host files staged[/yellow]",
			err.Error())
		entries = nil
	}
	for _, e := range entries {
		if !e.SourceBearing() {
			// Source-less entries cross in the wire alone and need no bytes. The plan
			// builder reads them straight out of the merged config, which is the only
			// half of this key a pure function may see (macosuser.hostFilesWire).
			continue
		}
		if e.IsDir {
			// DP-D15, not DP-L1. A directory entry names an arbitrary user tree and a
			// copy does not scale to one; the launch says so by name instead.
			out.undeliveredDirs = append(out.undeliveredDirs, e.Path)
			continue
		}
		// ⚠ THE ENTRY CROSSES WHETHER OR NOT ITS SOURCE EXISTS, and the two halves are
		// deliberately separate. The container path emits every source-bearing entry on
		// the wire and skips only the BIND whose source is missing, so the entrypoint
		// renders the destination from `defaults`/`content` — an absent dotfile is a
		// normal state, and the user still gets the file they declared. Gating the wire on
		// the copy would give this backend a different answer for that state: no file at
		// all, which is the pre-DP-L1 behaviour surviving in the one case nobody would
		// think to look at.
		out.ctx.HostFiles = append(out.ctx.HostFiles, e)
		if !isFile(e.Source) {
			continue
		}
		dest := hostUserCtxDir + "/" + e.Slug()
		if err := copyCtxFile(e.Source, tree, dest); err != nil {
			return out, fmt.Errorf("staging host_files source %s for the sandbox: %w",
				e.Source, err)
		}
		wrote = true
	}

	if !wrote {
		// Nothing to deliver — no packs with grants, no source-bearing entries, or none
		// of their sources exist on this machine. Returning "" rather than an empty
		// directory keeps the staging step, the env var and the host-layer report's
		// `supported` claim off the launch entirely, so a bare `yolo -- bash` pays
		// nothing and says nothing.
		_ = os.RemoveAll(tree)
		return out, nil
	}
	sort.Strings(out.ctx.Delivered)
	sort.Strings(out.ctx.Rendered)
	out.ctx.Tree = tree
	return out, nil
}

// refuseMacosUserCtxMounts is DP-D15's FATAL REFUSAL, built (docs/design/context-mounts.md
// §4 step 3): a macos-user launch that declares a context mount this backend cannot deliver
// refuses, naming every one, rather than starting a sandbox where the declared /ctx path is
// "surprisingly not there with an easily missed warning" — the maintainer's words for what
// the warning this replaces was.
//
// EVERYTHING IS UNDELIVERABLE TODAY. OQ-CX5 narrowed DP-D15 — deliver by link plus Seatbelt
// where the sandbox uid can reach the source, refuse the rest — but that delivery is §4
// steps 4–5 and needs Mac hardware to build, so until it lands every declared context mount
// refuses. Both declarations are covered: a config `mounts` element in either form or mode,
// and a selected pack's `mount` grant, the single-FILE form included (CX-D10: a copy could
// carry that one, and nothing copies it yet, so it is as absent as a directory).
//
// KEYED ON THE DECLARATION BEING PRESENT, never on a default: a config with no `mounts` and
// no pack that declares a `mount` refuses nothing, so no ordinary launch can hit this. A
// declared entry whose SOURCE DOES NOT EXIST is skipped with the container backends' own
// line and does not refuse (CX-D9): it would be absent on every backend, which is not this
// backend's deficiency, and it is the case §2.1 keeps a warning for once delivery exists.
//
// The host nvim config is NOT here although DP-D15's row names it: nothing declares it —
// the container arm binds ~/.config/nvim whenever it exists — so refusing it would refuse
// every macos-user launch on a machine with an nvim config, the exact trap the row's own
// "never on a default" warning names.
//
// Returns true when the launch must end (the refusal is printed).
func (o *Options) refuseMacosUserCtxMounts(cfg *jsonx.OrderedMap, packs []*packload.Pack) bool {
	var undeliverable []string
	// The merged view, as a validation-only read may be: this decides a REFUSAL, never what
	// is mounted, so an element's provenance cannot widen anything here.
	for _, m := range config.ParseMounts(cfg) {
		src := resolveExpand(m.Host)
		if !fileExists(src) {
			o.pr(o.Stderr).print("[yellow]Warning: mount path does not exist, skipping: " + src + "[/yellow]")
			continue
		}
		mode := "read-only"
		if m.RW {
			mode = "read-write"
		}
		undeliverable = append(undeliverable, "`mounts`: "+src+" → "+m.DestFor(src)+" ("+mode+")")
	}
	for _, p := range packs {
		if p == nil {
			continue
		}
		granted, _ := p.HonoredMounts()
		for _, mt := range granted {
			src := filepath.Join(homeDir(), filepath.FromSlash(mt.From))
			if !isDir(src) && !isFile(src) {
				o.pr(o.Stderr).print("[yellow]Warning: pack " + p.Name + " mount source " +
					"does not exist, skipping: ~/" + mt.From + "[/yellow]")
				continue
			}
			undeliverable = append(undeliverable, "pack "+p.Name+"'s `mount`: ~/"+mt.From+
				" → "+packload.MountCtxPath(mt))
		}
	}
	if len(undeliverable) == 0 {
		return false
	}
	out := o.pr(o.Stderr)
	out.print("[bold red]Refusing the macos-user launch: this backend cannot deliver a context " +
		"mount yet.[/bold red]")
	for _, u := range undeliverable {
		out.print("  • " + u)
	}
	out.print("A context mount is a bind into a container, and this backend starts none. Its " +
		"own delivery — a root-owned link named by $" + paths.ContextDirEnv + ", with the " +
		"Seatbelt profile deciding access — is designed and not built " +
		"(docs/design/context-mounts.md §3), and a declared mount is refused rather than " +
		"left absent. Remove the `mounts` entry (or the pack) for this workspace, or use a " +
		"container runtime (`runtime: \"podman\"` or `\"container\"`).")
	return true
}

// copyCtxFile copies one host file into the tree at its /ctx destination, creating the
// parents. `dest` is the absolute in-jail path (/ctx/...); the leading root is stripped so
// the tree's own layout IS the /ctx layout, which is what lets the jail find everything
// with one env var and no table.
//
// THE EXEC BIT IS CARRIED, because a reader downstream decides a mode from it:
// entrypoint.hostSourceIsExecutable stats the staged copy, so a host script arriving
// without its bit renders non-executable and the agent told to run it gets EACCES. The
// explicit chmod is load-bearing — os.OpenFile's mode is masked by umask and ignored
// outright for a file that already exists.
func copyCtxFile(src, tree, dest string) error {
	rel := strings.TrimPrefix(dest, packload.CtxRoot+"/")
	if rel == dest {
		return fmt.Errorf("context destination %q is not under %s", dest, packload.CtxRoot)
	}
	target := filepath.Join(tree, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := copyFile2(src, target); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info.Mode().Perm()&0o111 != 0 {
		mode = 0o755
	}
	return os.Chmod(target, mode)
}

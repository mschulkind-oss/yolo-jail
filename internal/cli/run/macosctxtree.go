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
// ⚠ TWO OF THE THREE ARE DELIVERED BY LINK NOW, never by this copy, and the third is still
// named. Config `mounts` and pack `mount` grants never reach this composer: they are context
// mounts (context-mounts.md's Defined terms name exactly those two), and planMacosUserCtxMounts
// below delivers each as a root-owned link in the context dir with the Seatbelt profile
// deciding access to its source (§3, OQ-CX5's narrowing of DP-D15), or refuses the launch,
// naming it, where this backend cannot (DP-D15: "a fatal error on setups that don't support it
// rather than having it be surprisingly not there"). Nothing is copied, so DP-D15's size
// argument stands and the bytes are live. A directory `host_files` entry is not a context
// mount, and it still reaches this function and comes back in undeliveredDirs for
// noteMacosUserHostByteGaps to print.
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
	// `mounts`, a pack `mount`) is linked into the same context dir, or refuses the launch, at
	// the top of the arm (planMacosUserCtxMounts), long before a tree is composed. The
	// directory `host_files` line is still noteMacosUserHostByteGaps', printed once the caller
	// has this delivery.

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

// macosCtxLinks is the macos-user decider for the two context-mount declarations
// (docs/design/context-mounts.md §3, §4 steps 4-5): every config `mounts` element and every
// selected pack's `mount` grant this launch would deliver, as the link macosuser stages for it,
// and every one this backend cannot deliver, with its reason.
//
// ONE READING WITH THE CONTAINER PATH. The declarations come out of configCtxMounts and
// packCtxMounts, the deciders the container argv and briefing use, so a read-write element
// still comes from the user scope alone (§2.2: the merged view has lost provenance), and a
// source that does not exist is skipped there with the container backends' own line and never
// refuses (CX-D9: it would be absent on every backend). `note` receives those lines; nil drops
// them, for the briefing's caller, whose launch printed them already.
//
// What is new here is the SITING, macosuser.SiteContextLinks, over the source as the host
// resolves it: a pack grant's `~/<from>` is resolved here, because the profile names a source
// verbatim and a rule on an unresolved path matches nothing. The siting's macOS facts are
// o.macosCtxSiting when a test set them, and a default macOS install's otherwise.
//
// KEYED ON THE DECLARATION BEING PRESENT, never on a default: a config with no `mounts` and no
// pack declaring a `mount` yields nothing. The host nvim config is not a declaration (CX-D10)
// and is not here.
func (o *Options) macosCtxLinks(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	note func(string)) ([]macosuser.ContextLink, []macosuser.ContextRefusal) {
	var links []macosuser.ContextLink
	for _, m := range o.configCtxMounts("macos-user", cfg, note) {
		links = append(links, macosuser.ContextLink{Dest: m.dest, Source: m.source, RW: m.rw,
			Dir: isDir(m.source)})
	}
	for _, m := range o.packCtxMounts("macos-user", packs, note) {
		links = append(links, macosuser.ContextLink{Dest: m.dest, Source: resolvePath(m.source),
			Named: "~/" + m.from, Dir: !m.file, Pack: m.pack})
	}
	if len(links) == 0 {
		return nil, nil
	}
	siting := macosuser.DarwinContextSiting()
	if o.macosCtxSiting != nil {
		siting = *o.macosCtxSiting
	}
	return links, macosuser.SiteContextLinks(siting, resolvePath(o.Workspace), links,
		macosuser.ContextOccupied(macosDeclaredHostFileDests(packs)))
}

// macosDeclaredHostFileDests is every /ctx destination a selected pack's `reads-host` grant
// names — what the composed tree may hold beside the links. DECLARED, not delivered: a link
// that collides with a grant collides whether or not the grant's host file exists today, so a
// launch's validity does not depend on which dotfiles this machine has (CX-D15's rule for the
// reserved names).
func macosDeclaredHostFileDests(packs []*packload.Pack) []string {
	var out []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		granted, _ := p.HonoredHostFiles()
		for _, hf := range granted {
			out = append(out, packload.CtxPath(p.StagedSlug(), hf))
		}
	}
	return out
}

// planMacosUserCtxMounts runs the decider for the launch, first on the macos-user arm: it
// returns the links to hand the backend, or prints DP-D15's FATAL REFUSAL — every context mount
// this backend cannot deliver, each with its reason, and what to do — and returns false.
//
// A refusal ends the whole launch rather than dropping the one entry, because DP-D15 ruled a
// fatal error better than a mount that is "surprisingly not there with an easily missed
// warning", and the narrowing (OQ-CX5) kept that half: deliver where the sandbox can reach the
// source, refuse fatally everywhere else.
func (o *Options) planMacosUserCtxMounts(cfg *jsonx.OrderedMap, packs []*packload.Pack) ([]macosuser.ContextLink, bool) {
	links, refused := o.macosCtxLinks(cfg, packs, func(line string) { o.pr(o.Stderr).print(line) })
	if len(refused) == 0 {
		return links, true
	}
	out := o.pr(o.Stderr)
	out.print("[bold red]Refusing the macos-user launch: this backend cannot deliver every " +
		"context mount.[/bold red]")
	for _, r := range refused {
		out.print("  • " + r.Link.Origin() + ": " + r.Link.NamedSource() + " → " + r.Link.Dest +
			" (" + r.Link.Mode() + "): " + r.Reason)
	}
	out.print("On macos-user a context mount is a root-owned link in $" + paths.ContextDirEnv +
		" to the host folder itself, and the Seatbelt profile decides what the sandbox may do " +
		"there, so the folder must be one the sandbox account can reach and must not overlap " +
		"anything else it writes: a read-only one outside every home and outside the " +
		"sandbox's writable places (under /Users/Shared, or outside /Users), a read-write one " +
		"under " + macosuser.SharedRootDefault() + " (docs/design/context-mounts.md §3). Move " +
		"the folder there, remove the entry (or the pack) for this workspace, or use a " +
		"container runtime (`runtime: \"podman\"` or `\"container\"`).")
	return nil, false
}

// noteMacosUserRWMounts is §2.4's disclosure on this backend: one line per read-write context
// mount, every launch, at the point the links are handed to the backend — the container
// assembler's rule (CX-D12) that nothing can deliver one without the other. The jail path is
// the one the agent opens here, under the context dir. A disclosure, so nothing suppresses it
// (OQ-RO3); there is no rootful sentence, because there is no podman: a file the sandbox writes
// is _yolojail's, and the shared root's inherited access is what lets you change it (§2.5).
func (o *Options) noteMacosUserRWMounts(cname string, links []macosuser.ContextLink) {
	dir := macosuser.StagedCtxRoot(cname, "")
	for _, l := range links {
		if l.RW {
			o.pr(o.Stderr).print(rwMountDisclosure(configCtxMount{source: l.Source,
				dest: dir + "/" + l.Rel(), rw: true}, false))
		}
	}
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

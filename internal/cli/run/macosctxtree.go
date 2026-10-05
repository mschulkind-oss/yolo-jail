package run

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
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
// # What is copied, and what is linked
//
// Three declarations can name a user DIRECTORY: config `mounts`, a pack `mount` grant and a
// directory `host_files` entry. DP-D15 refused the first two on this backend because "we
// can't do /ctx by copying, some of these directories are huge" — this repo's own
// yolo-jail.jsonc mounts a growing log directory — and they are context mounts (context-
// mounts.md's Defined terms name exactly those two), which planMacosUserCtxMounts below
// delivers as a root-owned link in the context dir with the Seatbelt profile deciding access
// to the source (§3, OQ-CX5's narrowing of DP-D15), or refuses, naming each, where this
// backend cannot. Nothing of a directory mount is copied, so DP-D15's size argument stands
// and those bytes are live.
//
// ⚠ A DIRECTORY `host_files` ENTRY IS COPIED HERE, and DP-D15's size reason never covered it:
// on a container backend it is bound read-only and then COPIED into the jail home at boot
// (entrypoint.stageHostFile → copyTree), so it is a full copy on every backend that delivers
// it, and this one adds the host-side copy in front. It is copied CONFINED to its source
// (copyCtxTreeConfined: a link is followed only where it resolves inside the source, as a
// container bind resolves nothing outside it) and CAPPED (macosDirHostFileEntryCap), and a
// copy that fails or crosses the cap ends the launch naming the entry.
//
// ⚠ AND A PACK `mount` OF ONE FILE IS COPIED TOO (context-mounts.md CX-D23). A pack grant names
// a path in the user's home, which a link cannot serve here (OQ-CX7: the sandbox account
// reaches nothing inside a real home), and one file has none of DP-D15's size problem. The
// decider hands those grants over as copies (macosCtxLinks) and this function lands each at
// its /ctx path; a directory grant keeps the link and its refusal.
//
// The host's global gitignore rides the same tree (hostGlobalGitignore), because the git
// identity reaches this backend's bootstrap only by name and a file needs bytes.
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
// (macosuser.HostContext).
type macosCtxDelivery struct {
	ctx macosuser.HostContext
	// undeliveredDirs is RETIRED and nothing writes it: a directory `host_files` entry is
	// copied now (buildMacosCtxTree), so the one shape noteMacosUserHostByteGaps named crosses
	// and that printer has no caller. The field stays only until the printer's own file deletes
	// it (loopholeinert.go), and goes with it.
	undeliveredDirs []string
}

// dirCopyCap is a bound on a confined directory copy: bytes of regular files, and entries
// (files plus directories, the source's own root not counted).
type dirCopyCap struct{ bytes, entries int64 }

// THE DIRECTORY host_files CAPS, per entry and per launch. A directory entry is copied at EVERY
// launch, three times over (into this tree, by root into the staged tree, by the bootstrap into
// the home), so the bound is what a launch may be made to copy, set where no config-shaped tree
// meets it. Measured 2026-10-05 in this repo's own jail, regular-file bytes with links followed
// and entries as files plus directories: ~/.ssh 92 bytes in 2; ~/.config/git 947 in 3;
// ~/.config/nvim 4,860 in 9; ~/.config/opencode 526,352 in 65; ~/.config/go 686,623 in 58; a
// whole ~/.config 1,877,305 in 191; and the host log directory this repo's yolo-jail.jsonc mounts,
// 2,662,745 bytes in 39. The per-entry caps are workspace skills' (WS-D18) and sit about 12× and
// 21× above the largest of those; a launch may carry two entries that size. An agent's whole
// state directory (this jail's ~/.claude holds about 3 GB) is not a host_files tree, and the cap
// refuses one.
const (
	maxDirHostFileBytes         = 32 << 20 // 32 MiB
	maxDirHostFileEntries       = 4096     // files and directories
	maxDirHostFileLaunchBytes   = 64 << 20 // 64 MiB
	maxDirHostFileLaunchEntries = 8192
)

// macosDirHostFileEntryCap and macosDirHostFileLaunchCap are the caps buildMacosCtxTree
// enforces: the constants above. Variables only so a test can cross them without writing tens
// of megabytes.
var (
	macosDirHostFileEntryCap  = dirCopyCap{bytes: maxDirHostFileBytes, entries: maxDirHostFileEntries}
	macosDirHostFileLaunchCap = dirCopyCap{bytes: maxDirHostFileLaunchBytes, entries: maxDirHostFileLaunchEntries}
)

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
// The global gitignore is the one exception: identity is never fatal (configureGit's rule),
// so a gitignore that cannot be copied is warned about and the launch goes on without it.
//
// `copies` are the selected packs' single-file `mount` grants the decider chose to copy
// (macosCtxLinks, already sited by planMacosUserCtxMounts); none for a caller that decided none.
func (o *Options) buildMacosCtxTree(staging string, packs []*packload.Pack,
	cfg *jsonx.OrderedMap, copies ...macosuser.ContextLink) (macosCtxDelivery, error) {
	var out macosCtxDelivery
	tree := filepath.Join(staging, macosCtxTreeLeaf)

	// WHAT NO TREE CAN CARRY is not said here any more: a declared context mount (config
	// `mounts`, a pack `mount`) is linked into the same context dir, copied into this tree, or
	// refuses the launch, at the top of the arm (planMacosUserCtxMounts), long before a tree is
	// composed; and a directory `host_files` entry is copied below or ends the launch.

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
	// One launch's spend against macosDirHostFileLaunchCap, across every directory entry.
	var launchSpent dirCopyCap
	for _, e := range entries {
		if !e.SourceBearing() {
			// Source-less entries cross in the wire alone and need no bytes. The plan
			// builder reads them straight out of the merged config, which is the only
			// half of this key a pure function may see (macosuser.hostFilesWire).
			continue
		}
		if e.IsDir {
			// A DIRECTORY ENTRY CROSSES ON THE FILE ENTRY'S RULE: on the wire whether or not its
			// source exists (the entrypoint finds nothing at host-user/<slug> and writes
			// nothing, as on a container whose bind was skipped), with its bytes when it does.
			// The copy is confined and capped (copyCtxTreeConfined), and a source that exists
			// and could not be copied whole ends the launch, naming the entry.
			out.ctx.HostFiles = append(out.ctx.HostFiles, e)
			if !isDir(e.Source) {
				continue
			}
			modes, err := copyCtxTreeConfined(e.Source, tree, hostUserCtxDir+"/"+e.Slug(),
				macosDirHostFileEntryCap, macosDirHostFileLaunchCap, &launchSpent)
			if err == nil {
				err = writeCtxHostFileModes(tree, e.Slug(), modes)
			}
			if err != nil {
				return out, dirHostFileCopyError(e, err)
			}
			wrote = true
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

	// --- A pack's single-file `mount` -----------------------------------------------
	//
	// Copied at the grant's own /ctx path (packload.MountCtxPath), where a container binds it
	// and where pack text names it, and recorded in Copied for the plan's re-siting. The
	// decider kept only sources that existed when it ran, so a file gone since is skipped as
	// it would have been then; one that is there and cannot be read ends the launch.
	for _, c := range copies {
		if !isFile(c.Source) {
			continue
		}
		if err := copyCtxFile(c.Source, tree, c.Dest); err != nil {
			return out, packFileMountCopyError(c, err)
		}
		out.ctx.Copied = append(out.ctx.Copied, c)
		wrote = true
	}

	// --- The host's global gitignore ------------------------------------------------
	//
	// The container launch binds it read-only and points the composed core.excludesFile at the
	// bind (gitIdentityMountArgs); here it is copied into the tree, and the plan names its
	// staged path to the bootstrap (macosuser.GlobalGitignoreEnv). Never fatal, and never in
	// Delivered: it is identity, not a pack's host layer.
	if ignore := o.hostGlobalGitignore(); ignore != "" {
		if err := copyCtxFile(ignore, tree, paths.ContextGlobalGitignore); err != nil {
			o.pr(o.Stderr).print("[yellow]" + richtext.Escape("Warning: your global gitignore "+
				ignore+" was not copied for the sandbox ("+err.Error()+"), so git there ignores "+
				"only what each repository lists. Make it readable to you, or point git's "+
				"core.excludesFile at a file that is.") + "[/yellow]")
		} else {
			out.ctx.GlobalGitignore = paths.ContextGlobalGitignore
			wrote = true
		}
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
	sort.Slice(out.ctx.Copied, func(i, j int) bool { return out.ctx.Copied[i].Dest < out.ctx.Copied[j].Dest })
	out.ctx.Tree = tree
	return out, nil
}

// macosCtxLinks is the macos-user decider for the two context-mount declarations
// (docs/design/context-mounts.md §3, §4 steps 4-5): every config `mounts` element and every
// selected pack's `mount` grant this launch would deliver — as the LINK macosuser stages for it,
// or, for a pack grant whose source is a single FILE, as the COPY buildMacosCtxTree lands at its
// /ctx path (CX-D23) — and every one this backend cannot deliver, with its reason.
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
// o.macosCtxSiting when a test set them, and a default macOS install's otherwise. A copy is
// sited by its DESTINATION alone (macosuser.SiteContextCopies), because the sandbox never opens
// its source; and each copy's destination is occupied for the links, so neither may land at,
// inside or around the other.
//
// A FILE GRANT IS COPIED, NEVER LINKED, and a directory grant never copied. The link-first
// alternative — link a file wherever the siting admits it, copy only the rest — was rejected:
// every pack grant names a path in the user's home, so the link would be refused in practice
// everywhere (OQ-CX7), and two mechanisms for one declaration would answer differently for a
// file moved between two folders.
//
// KEYED ON THE DECLARATION BEING PRESENT, never on a default: a config with no `mounts` and no
// pack declaring a `mount` yields nothing. The host nvim config is not a declaration (CX-D10)
// and is not here.
func (o *Options) macosCtxLinks(cfg *jsonx.OrderedMap, packs []*packload.Pack,
	note func(string)) (links, copies []macosuser.ContextLink, refused []macosuser.ContextRefusal) {
	for _, m := range o.configCtxMounts("macos-user", cfg, note) {
		links = append(links, macosuser.ContextLink{Dest: m.dest, Source: m.source, RW: m.rw,
			Dir: isDir(m.source)})
	}
	for _, m := range o.packCtxMounts("macos-user", packs, note) {
		l := macosuser.ContextLink{Dest: m.dest, Source: resolvePath(m.source),
			Named: "~/" + m.from, Dir: !m.file, Pack: m.pack}
		if m.file {
			copies = append(copies, l)
			continue
		}
		links = append(links, l)
	}
	if len(links) == 0 && len(copies) == 0 {
		return nil, nil, nil
	}
	siting := macosuser.DarwinContextSiting()
	if o.macosCtxSiting != nil {
		siting = *o.macosCtxSiting
	}
	declared := macosDeclaredHostFileDests(packs)
	forLinks := append([]string(nil), declared...)
	for _, c := range copies {
		forLinks = append(forLinks, c.Dest)
	}
	refused = macosuser.SiteContextLinks(siting, resolvePath(o.Workspace), links,
		macosuser.ContextOccupied(forLinks))
	refused = append(refused, macosuser.SiteContextCopies(siting, copies, links,
		macosuser.ContextOccupied(declared))...)
	return links, copies, refused
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
// returns the links to hand the backend and the pack files to copy, or prints DP-D15's FATAL
// REFUSAL — every context mount this backend cannot deliver, each with its reason, and what to
// do — and returns false.
//
// A refusal ends the whole launch rather than dropping the one entry, because DP-D15 ruled a
// fatal error better than a mount that is "surprisingly not there with an easily missed
// warning", and the narrowing (OQ-CX5) kept that half: deliver where the sandbox can reach the
// source, refuse fatally everywhere else.
func (o *Options) planMacosUserCtxMounts(cfg *jsonx.OrderedMap, packs []*packload.Pack) (links, copies []macosuser.ContextLink, ok bool) {
	links, copies, refused := o.macosCtxLinks(cfg, packs, func(line string) { o.pr(o.Stderr).print(line) })
	if len(refused) == 0 {
		return links, copies, true
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
		"under " + macosuser.SharedRootDefault() + " (docs/design/context-mounts.md §3). A " +
		"pack's single-file `mount` is copied there instead, and refuses only where its path " +
		"collides with another. Move the folder there, remove the entry (or the pack) for this " +
		"workspace, or use a container runtime (`runtime: \"podman\"` or `\"container\"`)" +
		o.containerStepClause() + ".")
	return nil, nil, false
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
// without its bit renders non-executable and the agent told to run it gets EACCES. AND NO
// OTHER BIT IS ADDED (ctxCopyMode): a 0600 credentials file stays 0600 in the host-side
// staging dir, which other accounts can reach. The explicit chmod is load-bearing —
// os.OpenFile's mode is masked by umask and ignored outright for a file that already exists.
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
	return os.Chmod(target, ctxCopyMode(info.Mode()))
}

// copyCtxTreeConfined copies the directory src into the tree at dest (the /ctx path, as
// copyCtxFile takes it), CONFINED to src and charged against two caps: entryCap for this entry
// alone, and launchCap for every directory entry of the launch, whose spend so far is
// *launchSpent and grows by what this copy writes. It returns each copied file's permission bits
// on the host, keyed by its slash path under src, for the caller to record
// (entrypoint.HostFileDirModes).
//
// CONFINED AS A CONTAINER BIND IS. A bind of src shows the jail what is under src and nothing
// else, and the boot's copy (entrypoint.copyTree) follows every link it meets there: a link that
// resolves inside src delivers what it names, a file or a whole folder, and one that leads out
// names a path the jail does not have, so the copy skips it as dangling. So here the walk runs
// through an os.Root on src and follows a link only where it resolves INSIDE src, to a file or a
// folder; an absolute link (os.Root refuses every one), a `../` link that leaves and a dangling
// link are skipped, as a dangling link is skipped at boot. A folder link that leads back to a
// folder the walk is already inside (`loop -> .`) is skipped too: its contents are copied where
// the walk found them, and following it would repeat the tree until a lookup limit stopped it.
// Never entrypoint's copyTree or copyFile2 here: both follow every link, and a link in a source
// tree that leads into the rest of the home would be a host-file read nobody declared.
//
// REGULAR FILES AND DIRECTORIES ONLY, each file opened non-blocking and checked by fstat after
// the open, so a FIFO swapped in for a file between the listing and the open neither blocks the
// launch nor crosses.
//
// NO WIDER THAN THE HOST FILE: each copy is written with its source's bits, readable by you and
// writable by no other account (ctxCopyMode), so a 0600 key copied here is not readable by every
// account that can reach the host-side staging dir; the source's exact bits are what the caller
// records.
//
// EVERY WRITE IS CHARGED BEFORE IT IS MADE, a file its size before a byte of it is read, and at
// most that many bytes are copied (a file growing while it is read arrives as it was when
// charged). Crossing either cap returns a *dirCopyCapError, and the caller ends the launch.
func copyCtxTreeConfined(src, tree, dest string, entryCap, launchCap dirCopyCap,
	launchSpent *dirCopyCap) (entrypoint.HostFileDirModes, error) {
	rel := strings.TrimPrefix(dest, packload.CtxRoot+"/")
	if rel == dest {
		return nil, fmt.Errorf("context destination %q is not under %s", dest, packload.CtxRoot)
	}
	target := filepath.Join(tree, filepath.FromSlash(rel))
	root, err := os.OpenRoot(src)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	top, err := root.Stat(".")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return nil, err
	}
	var spent dirCopyCap
	charge := func(bytes, entries int64, what string) error {
		if spent.bytes+bytes > entryCap.bytes || spent.entries+entries > entryCap.entries {
			return &dirCopyCapError{cap: entryCap, spent: spent, what: what}
		}
		if launchSpent.bytes+bytes > launchCap.bytes || launchSpent.entries+entries > launchCap.entries {
			return &dirCopyCapError{cap: launchCap, spent: *launchSpent, what: what, launch: true}
		}
		spent.bytes, spent.entries = spent.bytes+bytes, spent.entries+entries
		launchSpent.bytes, launchSpent.entries = launchSpent.bytes+bytes, launchSpent.entries+entries
		return nil
	}
	modes := entrypoint.HostFileDirModes{}

	// walk copies the folder at dir (a slash path under src); `inside` is every folder the walk
	// is in, src's own root first, which is what a looping folder link is recognised by.
	var walk func(dir string, inside []fs.FileInfo) error
	walk = func(dir string, inside []fs.FileInfo) error {
		ents, err := fs.ReadDir(root.FS(), dir)
		if err != nil {
			return err
		}
		for _, d := range ents {
			p := path.Join(dir, d.Name())
			out := filepath.Join(target, filepath.FromSlash(p))
			link := d.Type()&fs.ModeSymlink != 0
			if d.IsDir() || link {
				// Resolved through the root, so only inside src: an absolute, escaping or
				// dangling link fails here and is what a bind would show the jail as dangling.
				info, err := root.Stat(p)
				if err != nil {
					if link {
						continue
					}
					return err
				}
				if info.IsDir() {
					if slices.ContainsFunc(inside, func(a fs.FileInfo) bool { return os.SameFile(a, info) }) {
						continue
					}
					if err := charge(0, 1, p+"/"); err != nil {
						return err
					}
					if err := os.MkdirAll(out, 0o755); err != nil {
						return err
					}
					if err := walk(p, append(slices.Clip(inside), info)); err != nil {
						return err
					}
					continue
				}
				if !info.Mode().IsRegular() {
					continue
				}
			} else if !d.Type().IsRegular() {
				// A socket, FIFO or device node holds no bytes a copy could carry.
				continue
			}
			perm, copied, err := copyConfinedCtxFile(root, p, out, link, charge)
			if err != nil {
				return err
			}
			if copied {
				modes[p] = perm
			}
		}
		return nil
	}
	if err := walk(".", []fs.FileInfo{top}); err != nil {
		return nil, err
	}
	return modes, nil
}

// copyConfinedCtxFile copies the regular file at p under root to out, charged first, and returns
// the host file's permission bits; copied is false for a file that is not there to copy any more
// (a link that changed since it was resolved, or something swapped in that is not a file).
func copyConfinedCtxFile(root *os.Root, p, out string, link bool,
	charge func(bytes, entries int64, what string) error) (perm os.FileMode, copied bool, err error) {
	f, err := root.OpenFile(p, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		if link {
			return 0, false, nil // the link changed since it was resolved; skipped as dangling
		}
		return 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return 0, false, err
	}
	if !info.Mode().IsRegular() {
		return 0, false, nil // swapped for something that is not a file since the listing
	}
	if err := charge(info.Size(), 1, p); err != nil {
		return 0, false, err
	}
	mode := ctxCopyMode(info.Mode())
	w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return 0, false, err
	}
	if _, err := io.Copy(w, io.LimitReader(f, info.Size())); err != nil {
		w.Close()
		return 0, false, err
	}
	if err := w.Close(); err != nil {
		return 0, false, err
	}
	if err := os.Chmod(out, mode); err != nil {
		return 0, false, err
	}
	return info.Mode().Perm(), true, nil
}

// ctxCopyMode is the mode a host-side copy into the tree is written with: the source's bits,
// readable by you, and never writable by another account. No other account gets a bit the source
// did not give it, so the host-side staging dir, under your yolo state dir, holds no copy another
// account can read where the source was yours alone. (The root-owned staged copy is made readable
// to the sandbox account whatever this is: macosuser.StageCtxCommands.)
func ctxCopyMode(src os.FileMode) os.FileMode {
	return (src.Perm() | 0o400) &^ 0o022
}

// writeCtxHostFileModes records modes for the directory entry slug at its place in the tree
// (entrypoint.HostFileDirModesPath), for the bootstrap to create each file in the home with.
func writeCtxHostFileModes(tree, slug string, modes entrypoint.HostFileDirModes) error {
	at := entrypoint.HostFileDirModesPath(slug)
	rel := strings.TrimPrefix(at, packload.CtxRoot+"/")
	if rel == at {
		return fmt.Errorf("context destination %q is not under %s", at, packload.CtxRoot)
	}
	b, err := json.Marshal(modes)
	if err != nil {
		return err
	}
	target := filepath.Join(tree, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, b, 0o644)
}

// dirCopyCapError is a confined copy that would cross a cap: which cap, what had been charged
// against it, and the path whose write would have crossed it.
type dirCopyCapError struct {
	cap, spent dirCopyCap
	what       string
	launch     bool
}

func (e *dirCopyCapError) Error() string {
	scope := "a directory host_files entry"
	if e.launch {
		scope = "every directory host_files entry of one launch together"
	}
	return fmt.Sprintf("copying %s would pass what macos-user copies for %s (at most %s in %d "+
		"files and folders; %s in %d were copied before it)", e.what, scope,
		ctxCopySize(e.cap.bytes), e.cap.entries, ctxCopySize(e.spent.bytes), e.spent.entries)
}

// dirHostFileCopyError is the launch's refusal for a directory host_files entry whose source is
// there and did not copy whole: the entry, why, and what to do instead. A cap gets its own
// remedies, since the tree is fine and only its size is the problem.
func dirHostFileCopyError(e config.HostFileEntry, err error) error {
	var capErr *dirCopyCapError
	if errors.As(err, &capErr) {
		return fmt.Errorf("host_files ~/%s (source %s) is not copied for the macos-user sandbox: %v. This "+
			"backend has no bind mounts, so a directory entry is copied at every launch, and the "+
			"copy is capped. Split the entry into FILE entries for the files the agent needs, or "+
			"use a container runtime, which binds the directory instead (`runtime: \"podman\"`, "+
			"or `\"container\"` from Apple Container %s)", e.Path, e.Source, err, acROBindsFloor)
	}
	return fmt.Errorf("host_files ~/%s (source %s) could not be copied for the macos-user "+
		"sandbox: %v. A partial copy would hand the agent a directory that looks "+
		"like yours and is not. Make every file under it readable to you, or remove the entry for "+
		"this workspace", e.Path, e.Source, err)
}

// packFileMountCopyError is the launch's refusal for a pack's single-file `mount` whose source
// is there and could not be copied. The copy runs as you, so the usual cause is a file you
// cannot read, and on a Mac that includes one macOS's privacy controls guard from this terminal.
func packFileMountCopyError(c macosuser.ContextLink, err error) error {
	return fmt.Errorf("pack %s's `mount` %s → %s could not be copied for the macos-user "+
		"sandbox: %v. On macos-user a single-file pack `mount` is copied at launch "+
		"by yolo, as you, so the file must be readable to you here: if it sits in a folder macOS's "+
		"privacy controls guard (Desktop, Documents, Downloads), allow your terminal there in "+
		"System Settings → Privacy & Security. Or remove the pack for this workspace, or use a "+
		"container runtime (`runtime: \"podman\"` or `\"container\"`)",
		c.Pack, c.NamedSource(), c.Dest, err)
}

// ctxCopySize is n in the largest binary unit it fills ("32 MiB", "1.5 KiB"), exact for a whole
// number of units so a cap reads as the constant that set it.
func ctxCopySize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d bytes", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}[exp]
	if n%div == 0 {
		return fmt.Sprintf("%d %s", n/div, suffix)
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), suffix)
}

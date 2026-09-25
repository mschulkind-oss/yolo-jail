package cli

// configtarget.go resolves THE config target a `yolo config` invocation is about — one
// answer, computed once, disclosed on every verb
// (docs/reference/config-target-resolution.md#the-config-target and
// docs/reference/config-target-resolution.md#the-disclosure).
//
// # What it replaced, and why the pair was the defect
//
// There used to be TWO predicates, resolved independently: `workspaceRoot()` picked the
// capture store from the cwd, and `surfacesAreLocal()` picked the home — and therefore the
// provenance record and the write guard — from `YOLO_VERSION`. They can disagree, so one
// report could describe two homes: a host-side `yolo config diff claude` in a checkout
// printed that jail's captured edits beside the INVOKING USER'S real home's provenance, as
// two blocks of one report, with nothing in either naming a home (F1). And because the
// store was resolved a second time by `reset` through `render.Target` and a first time by
// `diff` through the hand-joined twins, the shipped inspect-then-undo pair disagreed on an
// owned host: `diff` reported no divergence and `reset` discarded an edit the user was never
// shown (F3).
//
// The ruling keeps the INFERENCE and deletes the PAIR
// ([OQ-CR1](docs/reference/config-target-resolution.md#oq-cr1), (a), against its own leaning):
// the cwd selects the TARGET — notch, workspace, store, home root and provenance all come
// off it — and `--at` overrides. Choosing what a report is ABOUT is not the write-side hazard
// `host-render-target.md` §6.6's *"the cwd selects nothing"* governs, and the cwd is the one
// input that already names the jail a reader means by "this one".
//
// # The marker is an ARTIFACT, never a directory
//
// `.yolo/config-boot.json` OR a workspace config file
// ([OQ-CR2](docs/reference/config-target-resolution.md#oq-cr2)). A bare `.yolo/` cannot be the
// test: `/home/agent/.yolo` exists in EVERY jail — it is the anchor for the generated
// `bin/block` and `bin/launch` dirs — so `cd ~` used to describe a workspace at
// `/home/agent` that has never existed. And the launch artifact cannot be the test alone,
// because a freshly cloned repo carrying a committed `yolo-jail.jsonc` is a workspace before
// its first launch — the directory the user is about to launch in, and the one they will run
// `yolo config ls` in to see what they are about to get.
//
// A directory that resolves NEITHER gets the HOST target, under disclosure — not a refusal.
// Outside a workspace the only home there is to describe is the host's, and standing there is
// how the user says so. What the design removes is the CONFIDENT EMPTY ANSWER, and naming the
// host target removes it exactly as well as an error does, with an answer instead. It is a
// TARGET, NOT A PERMISSION: the write guard (refuseHostSideWrite, and the host contract it
// consults) is untouched by anything in this file.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

// configTarget is what one `yolo config` invocation resolves, once, before any verb runs:
// which home the verb is about, which capture store describes it, and how that was decided.
//
// EVERY PATH READS THIS ONE ANSWER. That is what makes F1 and F3 unrepresentable rather than
// fixed — there is no second predicate left to disagree with — so nothing outside this file
// may resolve a store dir, a home root or a provenance path by hand
// (§4.4: *"no verb resolves a home root or a store path by hand"*).
type configTarget struct {
	// notch is `jail` or `host`. `guest` is refused at the boundary (resolveConfigTarget),
	// and `preview` is not a notch a report can be about.
	notch render.Kind
	// workspace is the jail being described, and is empty at the host notch.
	workspace string
	// chosenBy is the disclosure's second clause: what selected this target. A sentence
	// fragment, not an enum — nothing decides on it.
	chosenBy string
	// ownership is the declared `host_management` contract, at the host notch only. It
	// decides whether that notch keeps a capture store at all, which is why the store below
	// is built from it rather than beside it.
	ownership render.HostOwnership
	// local reports whether THIS process's home is the target's home — the fact
	// surfacesAreLocal() used to answer from `YOLO_VERSION` and a second workspace walk. It
	// is what the write guard reads, and it is false at the host notch by construction: a
	// `--at host` invocation inside a jail is about the jail's disposable home, and letting
	// that count as local would let `reset` truncate it.
	local bool
	// store is the render.Target every capture sidecar comes off — resolved, never
	// hand-joined ([OQ-CR3](docs/reference/config-target-resolution.md#oq-cr3)). At the jail
	// notch it is byte-identical to the retired prismSidecarDir; at the host notch it is ""
	// unless the user declared `own`, which is the contract's decision and not a caller's.
	store render.Target
	// wsStore is the WORKSPACE store, which stays the subject of `promote` at every notch.
	// Promote reads a jail's captured keys host-side BY DESIGN — that is its whole job — and
	// its destination is user scope, which is not a notch, so it is deliberately not
	// retargeted by this design. Unconstructed (and therefore storeless) at a host target,
	// where there is no workspace to key on.
	wsStore render.Target
	// refusals collects the links this invocation's readers refused in the workspace's
	// jail-writable state, for the verb to name (stateRefusals). A pointer, so every copy of
	// the target a verb hands down records into one log.
	refusals *stateRefusals
	// runtime is the resolved container runtime, and it exists for exactly one question:
	// WHERE a non-local workspace target's jail home holds a surface. The two container
	// backends lay that home out differently (Apple Container binds ws_state AT the home;
	// podman nests a per-dir overlay with the leading dot stripped), so the answer is the
	// runtime's and not a filesystem guess. Empty at a local or host target, where the
	// mapping cannot arise.
	runtime string
}

// jailConfigTarget builds the target for a resolved workspace: that workspace's jail.
func jailConfigTarget(workspace, chosenBy string) configTarget {
	t := configTarget{
		notch:     render.KindJail,
		workspace: workspace,
		chosenBy:  chosenBy,
		local:     processOwnsWorkspace(workspace),
		store:     render.Jail(paths.Home(), workspace, nil),
		wsStore:   render.Jail(paths.Home(), workspace, nil),
		refusals:  &stateRefusals{},
	}
	if !t.local {
		// Resolved the way `yolo ps` resolves it — env > the workspace's `runtime` key >
		// platform probe — so a report and a launch agree about which backend this
		// workspace uses. Only the non-local case asks: in the jail that owns the
		// workspace a surface resolves through the process home, which needs no backend
		// knowledge at all.
		t.runtime = detectListingRuntime(workspace)
	}
	return t
}

// hostConfigTarget builds the target for the invoking process's real home.
//
// The contract is read HERE and passed into the Target, rather than re-read by each store
// query, so one invocation cannot describe two contracts — the same reason
// render.Host takes ownership as a parameter instead of reading the user's config itself.
func hostConfigTarget(chosenBy string) configTarget {
	own := hostOwnership()
	return configTarget{
		notch:     render.KindHost,
		chosenBy:  chosenBy,
		ownership: own,
		store:     render.Host(paths.Home(), nil, own),
		// A bare render.Target, whose SidecarDir is "" by its own default arm — never a
		// render.Jail with an empty workspace, which would join ".yolo/prism" against
		// whatever directory the process happens to be sitting in.
		wsStore: render.Target{},
	}
}

// processOwnsWorkspace reports whether this process's home is the home of the jail launched
// for workspace: in-jail AND for the workspace bind-mounted at the fixed destination.
//
// Deliberately NOT a package var any more. It was one half of the retired predicate pair, and
// a stubbable predicate is how the store and the home came to be resolvable independently;
// tests now construct the configTarget they mean, which is a seam on the whole answer rather
// than on one of its inputs.
func processOwnsWorkspace(workspace string) bool {
	if os.Getenv("YOLO_VERSION") == "" {
		return false // host-side
	}
	// In-jail the workspace is bind-mounted at the fixed dest; a resolved root anywhere else
	// means we are inspecting some OTHER workspace's surfaces (a nested jail, and every
	// integration test).
	return workspace == containerWorkspace
}

// resolveConfigTarget is THE resolution point: `--at` where given, else the cwd.
//
// One call, at the top of the verb dispatch, before any verb's first read — which is what
// [P2](docs/reference/config-target-resolution.md#principles)
// ("one resolution per invocation") means in code. It returns a refusal STRING rather than an
// error so the caller prints it verbatim: each one names what is missing, and per §4.2 none
// of them may silently degrade to the other notch, because degrading is F1.
//
// THE VOCABULARY IS render's, and it is the one `yolo apply --at` already crosses. A notch
// name resolves through render.KindForNotch, whose selectable set
// render.TestNotchNamesMatchTheConfigVocabulary pins against
// config.KnownConfinements in both directions — so the read verbs and `apply` cannot come to
// accept different sets, which is what
// [P4](docs/reference/config-target-resolution.md#principles) — the SELECTOR rule,
// not report-tiers.md's own P4 — forbids ("a second spelling for the read verbs would be a
// second vocabulary for one fact").
func resolveConfigTarget(at string) (configTarget, string) {
	if at == "" {
		if ws, ok := resolveWorkspaceRoot(); ok {
			return jailConfigTarget(ws, "the cwd"), ""
		}
		return hostConfigTarget("the cwd resolving no workspace"), ""
	}
	notch, ok := render.KindForNotch(at)
	if !ok {
		return configTarget{}, fmt.Sprintf("--at %q is not a confinement level (%s)",
			at, strings.Join(notchNames(), "|"))
	}
	switch notch {
	case render.KindHost:
		return hostConfigTarget("--at"), ""
	case render.KindJail:
		ws, found := resolveWorkspaceRoot()
		if !found {
			// AN EXPLICIT REQUEST FOR A THING THAT DOES NOT EXIST is a different case from an
			// unstated default ([OQ-CR2](docs/reference/config-target-resolution.md#oq-cr2)), so
			// this refuses where a bare invocation answers about the host. Degrading silently
			// to the other notch is F1.
			//
			// ⚠ EVERY REMEDY IS A `cd` OR AN `--at`, never a flag naming a workspace.
			// internal/cli/run/run.go refuses a launch with *"there is no --workspace flag: cd
			// into the project you meant"*, and a second way to name a workspace is exactly
			// what that refusal rules out.
			return configTarget{}, "--at jail needs a workspace, and " + describeCwd() +
				" resolves none — no .yolo/config-boot.json and no " +
				config.WorkspaceConfigName + " in it or above it. cd into the project you " +
				"meant, or pass --at host to ask about your real home."
		}
		return jailConfigTarget(ws, "--at"), ""
	default:
		// render.NotchUnbuilt is the SENTENCE, not a literal: `yolo apply --at guest` and the
		// launch gate both say it, and three spellings of one notch's status is the drift
		// docs/design/declaration-parity.md exists to name (OQ-DP3).
		return configTarget{}, render.NotchUnbuilt("config")
	}
}

// notchNames is the selectable notches' names, for a refusal that lists what it would have
// accepted. Read off render.SelectableNotches so it cannot drift from what KindForNotch
// accepts — the vocabulary is render's and this is only its spelling.
func notchNames() []string {
	names := make([]string, 0, len(render.SelectableNotches()))
	for _, k := range render.SelectableNotches() {
		names = append(names, k.String())
	}
	return names
}

// describeCwd names the working directory for a refusal, or says it could not be read.
func describeCwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return "the working directory"
	}
	return wd
}

// resolveWorkspaceRoot is the marker walk: the cwd, walked upward to the nearest directory
// carrying a workspace MARKER, with ok=false when there is none.
//
// Deliberately NOT hardcoded to the container workspace when in-jail. That shortcut is wrong
// the moment the CLI runs from a DIFFERENT workspace than the one it is jailed for — exactly
// what happens in a nested jail, and in the integration tests, where `yolo config ls` runs
// against a temp workspace while /workspace is the outer checkout. It silently read the wrong
// sidecars and `reset` would have deleted them (alternative B, rejected).
//
// THE WALK STILL STOPS AT A DIRECTORY THAT MAY NOT BE A WORKSPACE
// (paths.WorkspaceScopeBreach: the home itself, or either of yolo's own two host dirs), and
// the ruled marker does NOT make that stop redundant. The marker excludes a stray `.yolo`
// anchor; the stop is the only thing that rejects a PRE-GUARD `~/.yolo/config-boot.json`,
// which nothing can create any more (EnsureWorkspaceStateDir refuses) but which machines
// already carry. Stopping rather than skipping and continuing upward: every ancestor of a
// boundary root is itself one (a parent of the home CONTAINS the home), so there is nothing
// above to find.
func resolveWorkspaceRoot() (string, bool) {
	wd, err := os.Getwd()
	if err != nil {
		return "", false
	}
	for dir := wd; ; {
		if paths.WorkspaceScopeBreach(dir) != nil {
			break
		}
		if workspaceMarked(dir) {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", false
}

// workspaceRoot is `yolo apply --sealed`'s spelling: the resolved workspace, else the bare
// cwd, which is the answer that verb has always been given.
//
// ⚠ IT IS DELIBERATELY NOT THE CONFIG TARGET, and that is an open question rather than a
// choice this design made. `applySealed` reproduces F2 in a verb
// docs/reference/config-target-resolution.md never names — from a directory with no workspace it
// resolves an empty store, finds nothing undeclared, and reports **"sealed"**, the confident
// empty answer, in the one verb whose entire job is refusing on undeclared input. Whether it
// takes the target or keeps the bare cwd is Blocker 3 of
// docs/design/config-target-resolution-plan.md and is unruled, so this keeps its behaviour
// byte-identical rather than letting the resolution decide it silently. `yolo config`'s own
// verbs never call it.
func workspaceRoot() string {
	if ws, ok := resolveWorkspaceRoot(); ok {
		return ws
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

// sealedWorkspaceStore is the ONE definition of the store `yolo apply --sealed` reads: the
// render.Target for the CWD's workspace, whatever workspaceRoot resolved.
//
// It exists so the Blocker-3 path has a name rather than a hand-joined path at each call
// site — the hazard docs/design/config-ownership-and-promotion.md §6.2 states as *"the
// directory is RESOLVED, never hand-built"*. It is deliberately NOT the config target: see
// workspaceRoot above for why that question is unruled.
func sealedWorkspaceStore() render.Target {
	return render.Jail(paths.Home(), workspaceRoot(), nil)
}

// sealedConfigTarget is the configTarget `yolo apply --sealed` opens sealedWorkspaceStore's
// files through, so they take storeFile's rule: host-side the store is a jail's writable
// state, and a link at a sidecar, at `.yolo/prism` or at `.yolo` is refused, never followed.
// It carries the store and nothing else a config verb resolves — no disclosure, no runtime —
// because the sealed verb reads sidecars only, and its workspace stays workspaceRoot's.
func sealedConfigTarget() configTarget {
	ws := workspaceRoot()
	return configTarget{
		notch:     render.KindJail,
		workspace: ws,
		local:     processOwnsWorkspace(ws),
		store:     sealedWorkspaceStore(),
		refusals:  &stateRefusals{},
	}
}

// workspaceMarked reports whether dir carries a workspace marker: the launch artifact
// `.yolo/config-boot.json`, or any of the four workspace config file names.
//
// Either artifact is enough, and the four names are derived from config's own two constants
// plus resolveWorkspaceConfigPath's `.jsonc`→`.json` fallback rather than spelled as four
// literals here — the set has to be whatever config.LoadWorkspaceConfig would READ, or a
// directory yolo will happily launch in fails to resolve.
//
// `config-boot.json` is a weaker signal than it looks, which is why it is not the only one:
// it is written by a FRESH launch and never by an attach, and it is best-effort (a warning,
// not a failure), so its absence does not even mean "never launched"
// (config.WriteWorkspaceBootBaseline).
//
// Regular files only. A DIRECTORY named `yolo-jail.jsonc` is not a config, and neither is one
// named `config-boot.json` — the whole reason the marker is an artifact rather than a
// directory is that a directory's mere existence is what went wrong the first time.
func workspaceMarked(dir string) bool {
	if isRegularFile(config.WorkspaceConfigBootPath(dir)) {
		return true
	}
	for _, name := range workspaceConfigNames() {
		if isRegularFile(filepath.Join(dir, name)) {
			return true
		}
	}
	return false
}

// workspaceConfigNames is every file name config.LoadWorkspaceConfig reads, in its own
// precedence order: the two base names and each one's `.json` fallback.
func workspaceConfigNames() []string {
	var names []string
	for _, base := range []string{config.WorkspaceConfigName, config.WorkspaceLocalConfigName} {
		names = append(names, base, strings.TrimSuffix(base, "c"))
	}
	return names
}

// isRegularFile reports whether path exists and is a plain file.
func isRegularFile(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}

// --- what every verb reads off the one answer -------------------------------------------

// sidecarDir is the capture store this invocation describes, or "" when the target keeps
// none (a host home the user has not declared `own`).
func (t configTarget) sidecarDir() string { return t.store.SidecarDir() }

// overlayPath, lastRenderPath and provenancePath are one surface's records under THIS
// target. All three come off the resolved store — the rule
// docs/design/config-ownership-and-promotion.md §6.2 stated as *"the directory is RESOLVED,
// never hand-built"* and that the readers then broke.
func (t configTarget) overlayPath(agent, name string) string {
	return t.store.OverlayPath(agent, name)
}

func (t configTarget) lastRenderPath(agent, name string) string {
	return t.store.LastRenderPath(agent, name)
}

// listCapturePath is the per-entry capture at one surface's config-list paths, off the same
// resolved store as the overlay beside it (render.Target.ListCapturePath).
func (t configTarget) listCapturePath(agent, name string) string {
	return t.store.ListCapturePath(agent, name)
}

func (t configTarget) provenancePath(agent, name string) string {
	return t.store.ProvenancePath(agent, name)
}

// storeFile is how a verb opens one of this target's store files, path being one of the
// accessors above.
//
// AT THE JAIL NOTCH IT IS ROOTED on `<workspace>/.yolo/prism`, never a plain path: the store
// is in the jail's writable state, and a host-side verb runs as the host user, so a link the
// jail left at a sidecar, at `.yolo/prism` or at `.yolo` is refused rather than followed
// (captureFile; docs/reference/jail-home.md, "Host code in jail-writable state"). The name is
// the path's base because every store file sits directly in the store
// (TestTheJailStoreIsWhereThePrismTwinsPutIt pins that the store is that directory). At the
// host notch the store is in the machine store, which no jail can write, and in the jail that
// owns the workspace every path is the process's own, so the plain path stands. An empty path (a target that keeps no store) is the zero captureFile.
func (t configTarget) storeFile(path string) captureFile {
	if path == "" {
		return captureFile{}
	}
	if t.notch != render.KindJail || t.local {
		return captureFile{name: path}
	}
	return captureFile{workspace: t.workspace, dir: "prism", name: filepath.Base(path), refusals: t.refusals}
}

// storeDir is the store itself, as storeFile opens it, for a listing.
func (t configTarget) storeDir() captureFile {
	dir := t.sidecarDir()
	if dir == "" || t.notch != render.KindJail || t.local {
		return captureFile{name: dir}
	}
	return captureFile{workspace: t.workspace, dir: "prism", name: ".", refusals: t.refusals}
}

func (t configTarget) overlayFile(agent, name string) captureFile {
	return t.storeFile(t.overlayPath(agent, name))
}

func (t configTarget) lastRenderFile(agent, name string) captureFile {
	return t.storeFile(t.lastRenderPath(agent, name))
}

func (t configTarget) listCaptureFile(agent, name string) captureFile {
	return t.storeFile(t.listCapturePath(agent, name))
}

func (t configTarget) provenanceFile(agent, name string) captureFile {
	return t.storeFile(t.provenancePath(agent, name))
}

// sidecarFileMode is the mode a re-seeded sidecar is written with: the STORE'S own, 0600 in a
// real home and 0644 in a workspace. Off the Target that decided where the store is, because
// a hand-copied 0644 would put a host user's own config bytes, credentials included, in a
// world-readable file.
func (t configTarget) sidecarFileMode() os.FileMode { return t.store.SidecarFileMode() }

// hostOwned reports whether this target's surfaces are ones yolo owns at the HOST notch —
// i.e. a host target under `host_management: own`. It is what the reset guard and the reset
// paths both read, so "which store does reset operate on" and "may reset run at all" cannot
// answer differently.
//
// Spelled as *"does this target keep a capture store?"* rather than as a comparison against
// the config value, so it is the same render.Target answer the WRITER acted on: a contract
// that composes nothing keeps no store and gets no reset.
func (t configTarget) hostOwned() bool {
	return t.notch == render.KindHost && t.store.SidecarDir() != ""
}

// surfaceStateFile resolves a surface's declared path (`~/.claude/settings.json`) to the file
// THIS target's home holds it at, with ok=false for a path this target cannot resolve.
//
// Three cases, and the third is what §4.1's last row is about:
//
//   - the HOST target, or the jail that OWNS the workspace: the process home, through the
//     same render.Target.ExpandHome the boot render resolves "~" with, as a plain path.
//   - a NON-LOCAL workspace target: the host file backing that jail's home
//     (<workspace>/.yolo/home/…), via jailHomeRel — which is backend-aware and is the one
//     definition of that mapping — ROOTED on the workspace overlay, because the jail can
//     write it: a link there, or at a directory on the way, is refused rather than followed
//     (captureFile).
//   - a path no jail home holds (an absolute surface path): ok=false.
//
// ⚠ IT MUST NEVER FALL BACK TO THE PROCESS HOME for a workspace target, and that is the whole
// payoff. Host-side the process home is the invoking human's own dotfiles, so presence read
// through it reported every file the jail DID render as absent and every host dotfile the jail
// never wrote as present — which is why `ls` had to decline the column and why its existence
// filter stopped applying (§2.3 F2's four-row inflation).
func (t configTarget) surfaceStateFile(surfacePath string) (captureFile, bool) {
	if t.notch == render.KindHost || t.local {
		return captureFile{name: expandHome(surfacePath)}, true
	}
	rel, ok := jailHomeRel(t.runtime, surfacePath)
	if !ok {
		return captureFile{}, false
	}
	return captureFile{workspace: t.workspace, dir: "home", name: rel, refusals: t.refusals}, true
}

// surfaceReach is what THIS target can honestly say about a surface's file. THREE answers,
// because collapsing the last two is the misreport §4.1 names: a path this target cannot
// resolve is not a file that is absent, and saying "absent" for it sends the reader looking
// for a render that was never going to land here.
type surfaceReach int

const (
	// surfaceReachable: resolved here, and the file is present.
	surfaceReachable surfaceReach = iota
	// surfaceMissing: resolved here, and the file is not present. A measurement.
	surfaceMissing
	// surfaceUnreachable: NOT RESOLVABLE AT THIS NOTCH — the surface's home is one this
	// target does not back (a machine-scope `shared` dir, a home-root redirect, an absolute
	// path), or this workspace has no jail home at all because nothing has launched in it.
	surfaceUnreachable
)

// reachSurface resolves one surface's file and reports which of the three it is.
//
// The workspace-target arm decides reachable-vs-unreachable from the DIRECTORY, and that is
// the bind's own shape rather than a second derivation of it: `<ws>/.yolo/home/claude` exists
// exactly when this workspace backs `~/.claude`, because that directory IS the mount source
// (run/prepare.go's prepareWsState). So a missing file inside a directory that exists is a
// genuine absence, and a directory that does not exist means this workspace never backed that
// home at all.
//
// Presence is an Lstat, so a link the jail left at the surface counts as present and is not
// followed. A link at a directory on the way is refused and named by the verb (the target's
// refusals), and the surface reads as unreachable.
func (t configTarget) reachSurface(surfacePath string) surfaceReach {
	f, ok := t.surfaceStateFile(surfacePath)
	if !ok {
		return surfaceUnreachable
	}
	_, err := f.lstat()
	if err == nil {
		return surfaceReachable
	}
	if errors.Is(err, errStateIsLink) {
		return surfaceUnreachable
	}
	if t.notch == render.KindJail && !t.local {
		dir := f
		dir.name = filepath.Dir(f.name)
		if st, err := dir.lstat(); err != nil || !st.IsDir() {
			return surfaceUnreachable
		}
	}
	return surfaceMissing
}

// surfaceFileExists reports whether this target's copy of a surface is present. Knowable at
// every target the resolution can produce, which is the payoff §3 predicted: with a home root
// on the target, a workspace target's files are reachable host-side, so `ls` no longer has to
// decline the column — and its existence filter applies again.
func (t configTarget) surfaceFileExists(surfacePath string) bool {
	return t.reachSurface(surfacePath) == surfaceReachable
}

// composeTarget is the render.Target a `yolo config` verb COMPOSES at, as distinct from the
// one it reads sidecars off. See localTarget: its "~" and its "${workspace}" answer to
// different things, and collapsing the two either moves the store to /workspace/.yolo/prism
// or previews host paths into a jail file.
func (t configTarget) composeTarget() render.Target { return localTarget() }

// wsOverlayFile is `promote`'s reader: the WORKSPACE store's overlay for one surface, or the
// zero captureFile at a host target. Promote is deliberately not retargeted by this design —
// host-side it reads the cwd's workspace store because lifting a jail's captured keys into a
// pack is its whole job — so it gets its own accessor rather than the target's store. It is
// opened through storeFile all the same: promote runs host-side only (refuseInJailPromote), so
// the store it reads is a jail's writable state, rooted rather than read by plain path.
func (t configTarget) wsOverlayFile(agent, name string) captureFile {
	return t.storeFile(t.wsStore.OverlayPath(agent, name))
}

// wsLastRenderFile and wsListCaptureFile are wsOverlayFile's baseline and list-capture twins,
// for the same reason.
func (t configTarget) wsLastRenderFile(agent, name string) captureFile {
	return t.storeFile(t.wsStore.LastRenderPath(agent, name))
}

func (t configTarget) wsListCaptureFile(agent, name string) captureFile {
	return t.storeFile(t.wsStore.ListCapturePath(agent, name))
}

// --- the three answers a store can give -------------------------------------------------

// storeState is what a capture store can honestly say about itself. FOUR answers, not one,
// and collapsing them is the failure
// [P3](docs/reference/config-target-resolution.md#principles)
// names: *"unknown is not empty"*. Today an absent store and an unreadable one both read as
// no edits, stated with the same confidence as a real negative
// (`os.ReadDir` error → nil, §4.2).
type storeState int

const (
	// storeReadable: the store exists and was read. A negative from here is a measurement.
	storeReadable storeState = iota
	// storeNone: this target keeps no capture store BY CONTRACT — a host home the user has
	// not declared `own`. Expected, and different from a store that should exist and does not.
	storeNone
	// storeAbsent: the store dir is not there, so nothing has ever rendered here. The state
	// `capture` already reports ("never rendered here") and `diff` did not.
	storeAbsent
	// storeUnreadable: it is there and we could not read it (permissions). The third state,
	// and the one that currently reads as empty.
	storeUnreadable
)

// storeState reports which of the four this target's store is in, with the underlying error
// for the unreadable case.
//
// It opens and reads ONE name rather than stat-ing: a directory can be stat-able and still
// refuse to be listed (0600 on a directory lists nothing while `ls` shows the names —
// render.Target.SidecarDirMode's ⚠ measures exactly that), which is the permission case this
// distinguishes.
func (t configTarget) storeState() (storeState, error) {
	if t.sidecarDir() == "" {
		return storeNone, nil
	}
	// Through the store's own opener (storeDir), so at the jail notch a link at `.yolo` or at
	// `.yolo/prism` is refused, and named, rather than listed.
	readOne := func(f *os.File, err error) error {
		if err != nil {
			return err
		}
		defer f.Close()
		if _, err := f.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
			return err
		}
		return nil
	}
	var err error
	if dir := t.storeDir(); dir.rooted() {
		err = dir.beneath("read", false, func(r *os.Root) error { return readOne(r.Open(".")) })
	} else {
		err = readOne(os.Open(dir.name))
	}
	switch {
	case err == nil:
		return storeReadable, nil
	case os.IsNotExist(err):
		return storeAbsent, nil
	default:
		return storeUnreadable, err
	}
}

// noCaptureReason is the sentence a verb prints when it found no captured edits and the store
// is not simply empty — or "" when a plain negative is the honest answer.
func (t configTarget) noCaptureReason(state storeState) string {
	switch state {
	case storeAbsent:
		return "never rendered here — nothing has written " + t.sidecarDir()
	case storeNone:
		return "this target keeps no capture store (host_management: " + t.ownership.String() +
			"), so there is nothing to have captured"
	default:
		return ""
	}
}

// --- the disclosure ----------------------------------------------------------------------

// disclosure is the one line every `yolo config` verb prints before its report
// ([OQ-CR5](docs/reference/config-target-resolution.md#oq-cr5): unconditional, on every
// invocation, always). It matches the shape a launch already uses for
// `Flake source: <path> (<what selected it>)`: what this is about, then what chose it.
//
// UNCONDITIONAL, AND THAT IS THE RULING. Not "only when surprising" — *surprising* is a
// judgement the reader cannot check, so the line's ABSENCE would carry information they have
// no way to decode, and a disclosure you have to know the rules to notice the lack of is not
// a disclosure ([P4](docs/reference/report-tiers.md#principles): compression is allowed,
// suppression is not).
//
// ⚠ IT GOES TO STDERR, and the design's transcripts do not say so because a terminal
// conflates the two streams. `yolo config dump` emits canonical JSON on stdout and
// `yolo config promote --format json` emits a document, so a prose line on stdout would make
// this design break every machine consumer of those two verbs — which report-tiers.md's own
// *"exit 2, stdout empty"* rule for the JSON postures exists to prevent. Routing is not
// suppression: the line is emitted on every invocation either way.
func (t configTarget) disclosure() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Surfaces: %s — %s notch, from %s", t.describeHome(), t.notch, t.chosenBy)
	if t.notch == render.KindHost {
		fmt.Fprintf(&b, " · host_management: %s", t.ownership)
	}
	if dir := t.sidecarDir(); dir != "" {
		fmt.Fprintf(&b, " · store %s", dir)
	} else {
		b.WriteString(" · no capture store at this target")
	}
	return b.String()
}

// describeHome names what the report is about: the workspace at a jail target, the home at a
// host one. The workspace rather than <workspace>/.yolo/home, because the workspace is the
// name a reader means by "this jail" and the home overlay is an implementation of it.
func (t configTarget) describeHome() string {
	if t.notch == render.KindJail {
		return t.workspace
	}
	return paths.Home()
}

// notResolvableHere is the phrase a report prints for a surface this target cannot resolve —
// §4.1's last row. It names the notch, because the same surface IS resolvable at another one,
// which is the actionable half of the fact.
func (t configTarget) notResolvableHere() string {
	return "not resolvable at the " + t.notch.String() + " notch"
}

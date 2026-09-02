package run

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Run validates config, resolves the runtime, then either execs into
// an existing container or launches a fresh one. Returns the process exit code.
// The whole flow is driven off the
// injected seams so the probe + argv-assembly paths are unit-testable.
func Run(opts Options) int {
	fillDefaults(&opts)
	o := &opts

	// --- Phase 1: probes (repo root, storage, config, runtime) ---
	// Repo-root resolution is a HARD GATE for the container backends: without a
	// flake there is nothing to build the image from, and silently running a
	// stale loaded/cached image instead is worse than failing — it hides that the
	// environment is not what the config describes. (This reverts D2's graceful
	// degradation: launching on an old image with no rebuild was deemed a
	// footgun, not a convenience.) The gate is applied below, under an explicit
	// `rt != "macos-user"` guard rather than by sitting after the macos-user
	// branch: macos-user with empty `packages:` genuinely needs no repo (it
	// materializes native darwin packages only when `packages:` is non-empty, and
	// MaterializeDarwin fails loudly on a bad flake root of its own accord), and
	// naming that exemption beats encoding it in statement order — which is how it
	// was expressed until pack staging had to move above the dispatch (B-0), and
	// what would have silently gated macos-user the moment anything moved again.
	repoRes, repoRootOK := o.RepoRoot()
	repoRoot := repoRes.Root
	if err := ensureStorage(); err != nil {
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return 1
	}
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		return 1
	}
	rt, ok := o.resolveRuntime(cfg)
	if !ok {
		return 1
	}

	// The two container-only gates, hoisted ABOVE pack staging so a launch that is
	// going to refuse still refuses before it does any staging work — the order the
	// container path has always had, kept intact now that staging moved earlier.
	// Both are skipped for macos-user: --dry-run is that backend's own flag, and a
	// native run with empty `packages:` genuinely needs no repo (see the comment on
	// repoRoot above).
	if rt != "macos-user" {
		if o.DryRun {
			o.pr(o.Stdout).print(
				"[bold red]--dry-run is only supported for the macos-user runtime.[/bold red]  " +
					`Set runtime: "macos-user" (or YOLO_RUNTIME=macos-user) to use it.`)
			return 1
		}
		// Container backends need a flake to build the image from. A missing repo
		// root is FATAL rather than a degraded launch on a stale image: running the
		// wrong environment silently is the failure this refuses. reporoot.Resolve
		// already found nothing here (env → beside-the-binary bundle → staged
		// bundle all missed), so print the same actionable fix the resolver would
		// and exit.
		if !repoRootOK {
			o.pr(o.Stderr).print("[bold red]Cannot find yolo-jail repo root.[/bold red]\n" +
				"The yolo CLI needs the repo (a flake) to build the jail image, and refuses to\n" +
				"launch on a possibly-stale cached image instead.\n\n" +
				"Fix: reinstall so the flake bundle ships with the binary (`just install`), or\n" +
				"point yolo at a checkout with [bold]YOLO_REPO_ROOT[/bold]. The working directory\n" +
				"is never consulted, so standing in a checkout is not enough:\n" +
				"  YOLO_REPO_ROOT=~/code/yolo-jail yolo …")
			return 1
		}
		// Having found the flake, say WHICH one and what selected it, before
		// anything expensive and before the skew gate below — so a refusal reads
		// as an explanation of the line above it rather than a surprise.
		o.reportFlakeSource(repoRes)
		// Then refuse to build an image from source THIS binary was not built
		// from. The image half of yolo redeploys itself on every launch and the
		// host half never does, so a commit that moves a host↔jail contract leaves
		// the machine skewed by default — see refuseOnSourceSkew for what that
		// costs when it is discovered at boot instead of here.
		if o.refuseOnSourceSkew(repoRoot) {
			return 1
		}
	}

	// --- Phase 2: pack staging, BEFORE the backend dispatch ---
	//
	// THE ORDERING IS THE FIX (B-0). Staging used to live inside runContainer, which
	// the macos-user branch below returns before ever reaching — so that backend was
	// handed no YOLO_PACK_ROOT and its RunDarwinBootstrap loops (LoadJailPacks →
	// ConfigurePackSurfaces → RunPackHooks) iterated an empty list on every launch. Not
	// an error, not a warning: a backend that looked provisioned and configured nothing.
	//
	// Staging is host-side work that no backend owns — it resolves the user's `packs`
	// entries, copies their trees, and prunes the ones that left the config — so it
	// belongs above the dispatch rather than inside one arm of it. Every backend that
	// renders pack surfaces now gets the same staged set from the same call, which is
	// what makes "a pack works on macos-user" a property of the pipeline instead of a
	// second implementation. Pinned by TestPacksAreStagedBeforeBackendDispatch.
	cname := runtime.FromWorkspace(o.Workspace)
	staged, stagedOK := o.stageRunPacks(cname)
	if !stagedOK {
		return 1
	}

	// PACK LAUNCH FLAGS, ABOVE THE DISPATCH — the same B-0 move pack staging made, for
	// the same reason. The injection used to sit inside runContainer, which the
	// macos-user arm returns before reaching, so on that backend a `launch`
	// contribution did nothing at all. copilot's `--yolo --no-auto-update` is a plain
	// launch contribution with NO autonomy config half to fall back on, so it was a
	// 100% drop; claude's `--dangerously-skip-permissions` fell back to
	// defaultMode: acceptEdits, which auto-accepts EDITS and not Bash or WebFetch.
	//
	// THE STAGED SET, and the EFFECTIVE PROFILE TABLE with it — both read off staging,
	// which is why this sits BELOW stageRunPacks rather than above it. When the
	// injection moved up out of runContainer, staging had not moved yet, so this line
	// took packload.Embedded(): it was the only pack set in scope, and "what yolo ships
	// is what a bare `yolo -- <bin>` gets" was the cheapest true sentence available. It
	// stopped being the right sentence the moment staging hoisted: the embedded set is
	// NOT what the jail runs, the staged set is, and the difference is every configured
	// pack — whose launch contributions an embedded-set injection drops, silently. It
	// is also the set the jail's own alias fold reads (LoadJailPacks over the staged
	// tree), so using it here is what keeps the two spellings of one launch — the
	// interactive alias and `yolo -- <bin>` — from disagreeing; they agree by
	// construction rather than by both folding the same table. (The injection took a
	// profile table until OQ-PT8 shrank the kind: profile bodies no longer carry launch
	// flags, so there is no variant table left to fold, and the parameter is gone rather
	// than accepted-and-ignored.)
	//
	// Guarded on len>0 so the empty case still reaches each arm's own default (a bare
	// `yolo` is bash in a container and an interactive zsh natively); injecting into an
	// empty argv would invent a binary neither arm asked for.
	//
	// THE CHANNEL, composed once here — the third B-0 hoist, after the pack trees and the
	// launch flags. The effective profile table YOLO_USE_PROFILES carries is one part of
	// what a profile launch composes; the pack env fold, the composed provider table, the
	// provider env vars and the hydrated env_sources are the rest, and every one of them
	// used to be composed inside the container arm, which this branch returns before
	// reaching. internal/cli/run/profilechannel.go is the whole story; what matters here
	// is that both arms below consume THIS value, so `yolo -p zai -- claude` composes the
	// same environment on a container and on a native sandbox instead of composing it
	// twice (or, on one of them, not at all).
	channel, err := o.composePackChannel(cfg, staged.packs, nil)
	if err != nil {
		// The composed provider table is the one thing a launch cannot disagree with
		// itself about, so a composition that refuses refuses HERE — above the backend
		// dispatch, before either arm starts a thing. printProviderRefusal is the same
		// renderer the credential pre-flight uses, so both refusals read alike.
		o.printProviderRefusal([]string{"Refusing to launch: " + err.Error()})
		return 1
	}
	injectedArgs := o.Args
	if len(injectedArgs) > 0 {
		injectedArgs = packload.InjectLaunchFlags(staged.packs, injectedArgs)
	}

	// macos-user native branch: route to the injected handler,
	// which wires internal/macosuser (SBPL sandbox, dscl provisioning, the
	// sandbox-exec launch) + the darwinpkg streaming-build materialize adapter.
	// Falls back to an actionable error if the front door didn't inject it.
	if rt == "macos-user" {
		if o.MacosUserRun == nil {
			o.pr(o.Stdout).print(
				"[bold red]macos-user runtime handler not wired.[/bold red]  " +
					"This build cannot launch the native macOS backend.")
			return 1
		}
		// (a bare `yolo` opens an interactive login zsh in the sandbox).
		agentArgv := injectedArgs
		if len(agentArgv) == 0 {
			agentArgv = []string{"/bin/zsh", "-l"}
		}
		// CONFIG-CHANGE APPROVAL, on this arm too (docs/design/config-safety.md:
		// "Every config change requires explicit approval"). The gate used to live
		// only inside runContainer, several lines below the return above — the same
		// shape of omission pack staging had before B-0, and with the same signature:
		// nothing failed, the backend simply launched a config no human had seen. It
		// is not a container-only concern. macos-user reads `security.blocked_tools`,
		// `mcp_servers`, `lsp_servers` and `packages` off the very config an agent can
		// edit in the workspace, and `mcp_servers` in particular is a command line the
		// agent's own MCP client executes — so the ONE backend with no container around
		// it was the one accepting those edits unprompted.
		//
		// Placed here rather than hoisted above the dispatch because the container arm
		// gates the FRESH-LAUNCH path only: attaching to a running jail deliberately
		// skips the check (the container was already started with its config). This
		// backend has no attach — every macos-user invocation is a fresh sandbox — so
		// the arm's own call site is where the two backends agree.
		//
		// --dry-run is exempt: it prints the plan and launches nothing, so there is no
		// change to approve, and refusing a plan render would only hide the diff a user
		// is asking to inspect.
		wsCfg, _ := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
		if !o.DryRun && !o.checkConfigChanges(wsCfg) {
			return 1
		}
		// Same notice as the container paths: a brand-new macos-user user has no packs
		// either, and the native backend is where a "where is my agent?" is hardest to
		// diagnose (no image, no provisioning output to read back).
		o.warnIfNoPacks()
		// INERT LOOPHOLES, on this arm too. Every pack-shipped loophole is inert here —
		// this backend never reaches startLoopholes at all — and until 2026-08-24 that
		// was the ONE inert backend that said nothing about it, because the report hangs
		// off startLoopholesDisclosed inside runContainer and this arm returns above it.
		//
		// The gap survived because the test for it called notePackLoopholesInert
		// DIRECTLY for both backends, so the macos-user half asserted a line no launch
		// could produce: the callee was pinned and the call site did not exist. That is
		// the shape AGENTS.md names, found in the code that was written to prevent the
		// same shape one layer down ("a pack whose whole purpose is a loophole must not
		// look installed on a backend that ignores it").
		//
		// It is NOT routed through startLoopholesDisclosed: that wrapper exists to make
		// disclosure inseparable from the SPAWN, and nothing spawns here. Disclosing
		// beside warnIfNoPacks is the honest placement — both answer "what will this
		// launch not do for you", which is the only question this backend can raise.
		o.notePackLoopholesInert(rt, staged.packs, cfg)
		// AND THE OTHER TIER COLLAPSE, which is #39's mirror image. Apple Container made
		// the MACHINE-wide tier per-workspace; this backend makes the PER-WORKSPACE tier
		// machine-wide, because SandboxHome() is a constant — /Users/_yolojail — with no
		// workspace component. Every pack `state` dir at scope:workspace (.claude, .codex,
		// .pi, .copilot, .gemini) is therefore shared by every workspace on the machine.
		//
		// The sharp part is not the sharing, it is that the sandbox ENFORCES the boundary
		// one layer down and leaks it here: the Seatbelt profile denies reading a sibling
		// workspace's files, and then ~/.claude/projects/<other-workspace>/*.jsonl is
		// readable because it lives in the shared home.
		//
		// WARN rather than fix, deliberately. Splitting the home would break the MACHINE
		// tier to repair the workspace tier — the single home IS the shared-credentials
		// mechanism on this backend — so a fix has to restore both tiers explicitly, which
		// is a design change and not a launch-time patch.
		o.noteMachineWideWorkspaceState(staged.packs)
		o.noteMacosUserContentGaps(staged.packs, cfg)
		// WHERE THE PROFILE SELECTIONS LANDED, on this arm too. Until the channel hoist
		// this line had no honest form here — the launch line prints what a launch
		// DELIVERS (OQ-10: never a verb that overclaims), and this backend delivered
		// nothing. It now layers the whole channel into its plan env, so the line
		// describes a delivery again. Printed beside the three notes above rather than
		// beside the container's banner: this is the block that answers "what will this
		// launch do for you", and it is the only stderr this arm writes before the
		// backend takes the terminal.
		o.noteUseProfiles(channel.profiles, staged.packs)
		// THE SELECTED-PACK CREDENTIAL PRE-FLIGHT, on this arm too. It used to live only
		// in runContainer, below the return above, so a native launch with a provider
		// pack and no key started a sandbox that failed its first API call and said
		// nothing about why — the §6.1 symptom, unrefused on exactly the backend that
		// composes no env of its own.
		//
		// This arm rather than above the dispatch, deliberately, and for the reason the
		// config-change approval above gives: the container arm gates the FRESH-LAUNCH
		// path only (attaching to a running jail delivers no environment, so the
		// question "would this launch deliver the key" has no subject there and refusing
		// it would block re-entry into a jail that already has its key). On THIS backend
		// every invocation is a fresh sandbox, so the arm's own call site is where the
		// two backends agree. The channel is the same value both arms check.
		if lines, refuse := o.checkProviderCredentials(cfg, staged.packs, channel, nil); len(lines) > 0 {
			o.printProviderRefusal(lines)
			if refuse {
				return 1
			}
		}
		// The channel crosses in launch-env form — the pack env fold, the shape vars and
		// the two wire tables, flattened in the container argv's layering order. The
		// backend layers it into its plan env ahead of env_sources (the container's
		// precedence) and relays the two wire tables to its bootstrap, so the native
		// derives read the same provider table a container jail's do.
		//
		// The staged tree crosses as a PATH, not as the loaded declarations: the native
		// bootstrap re-reads the manifests itself (LoadJailPacks), exactly as the
		// container entrypoint does off its /ctx/packs mount, so the two backends render
		// from the same input in the same way.
		return o.MacosUserRun(cfg, o.Workspace, config.SelectedAgents(cfg), agentArgv,
			repoRoot, staged.root, o.DryRun, channel.launchEnv())
	}
	return o.runContainer(cfg, rt, repoRoot, cname, staged, injectedArgs, channel)
}

// stagedPacks is one run's staged pack set — the single result of the single staging
// call a launch makes, threaded to whichever backend the dispatch picks.
//
// It exists because staging produces three things a backend needs together (the root to
// point a renderer at, the declarations the mount assembler acts on, and the collected
// briefing prose) and returning them as one value is what let staging move above the
// dispatch without every arm growing a four-value signature.
type stagedPacks struct {
	root      string
	packs     []*packload.Pack
	briefings []jailcontent.PackBriefing
}

// stageRunPacks stages this run's packs and reports the failure itself, so the caller's
// dispatch stays a straight line. FAIL-CLOSED (A12): a pack that cannot be staged ends
// the launch — the same contract stagePacks has always had, now applied before any
// backend runs rather than inside one of them.
//
// It also runs the loophole-state RETIREMENT pass, immediately after staging and its
// staged-tree prune. That placement is the requirement, not a convenience: the launch is the
// only thing that reads `packs`, compares it to what is staged, and prunes — so it is the only
// place a DESELECTION is observed at all (loophole-packaging.md §4.5, and see
// loopholeretire.go for why `yolo host apply` and the host-render archive sweep cannot see it).
// Never fatal: a bookkeeping failure over the host state dir must not cost the user a jail.
//
// It runs on EVERY invocation, attach included, for the same reason the staged-tree prune
// does: config says the pack is gone, and a state dir holding a CA private key should not
// wait for the next fresh launch to be retired.
func (o *Options) stageRunPacks(cname string) (stagedPacks, bool) {
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return stagedPacks{}, false
	}
	o.recordAndRetirePackLoopholes(packs)
	return stagedPacks{root: root, packs: packs, briefings: briefings}, true
}

// warnIfNoPacks prints the empty-packs notice when the user has no packs configured.
//
// The text is config.NoPacksMessage/NoPacksGuidance, shared with the `yolo check` Packs
// section so the two surfaces cannot drift; the sentence-ending period is added here
// because this is prose and a check badge line is not. It is deliberately free of
// blame: an empty pack list is exactly what a brand-new install looks like, not a
// mistake anybody made. It names `yolo pack --help` rather than `yolo config-ref`
// because that is the shorter answer to "what do I put here" — packUsage opens with
// what a pack is, where config-ref's `packs` entry is the key schema underneath it.
//
// Packs are the only way content — an agent included — gets installed into a jail, so
// an empty list is not a lean jail, it is a jail with nothing in it. That state is
// otherwise SILENT: with no packs there are no selected agents, so refreshJailBriefings
// writes zero briefings (its loop runs over the RESOLVED agents) and stages zero
// per-agent skills. There is no file left to put a note in, which is why this is
// printed rather than written — and why it keys off the PACK list rather than the agent
// list, which is both the thing the user edits and the thing that still exists.
//
// Silent whenever `packs` is present but UNUSABLE, which is not the same test as
// "LoadPacks returned an error". An error covers only a JSONC parse failure; a
// non-list value and a list whose every entry is invalid both come back as zero
// entries with a nil error, because checkPacks routes per-entry problems to the warn
// callback instead. All three mean the user DID configure packs, so "you have no
// packs" would misdiagnose "your packs are malformed" — and stagePacks (via
// validatePacks) already fails the launch naming the real problem. Hence the callback
// is non-nil and any problem suppresses the notice: only a genuinely absent or empty
// list reaches the print.
//
// Counting callback invocations is exact rather than approximate because LoadPacks
// loads strict: every loader-side warning (parse failure, bad include_if_found) is an
// ERROR under strict, so the only thing that can reach this callback is a checkPacks
// per-entry problem. An unrelated config warning cannot false-suppress the notice.
//
// It re-reads the user config rather than taking a count threaded down from stagePacks:
// one small file read per launch is cheaper than making every staging-side signature
// carry a value only this notice consumes.
func (o *Options) warnIfNoPacks() {
	problems := 0
	entries, err := config.LoadPacks(func(string) { problems++ })
	// HasConfiguredPack, not len(entries): the conventional local pack is included with no
	// config line (config.localPackEntry), and it is CONTENT — a jail whose only pack is
	// ~/.config/yolo-jail/local has skills and prose and still nothing to run them. Counting
	// it here would silence a notice that is still true.
	if err != nil || problems > 0 || config.HasConfiguredPack(entries) {
		return
	}
	// Stderr, like every other launch notice: a launch is usually `yolo -- cmd`, and
	// the user redirects the COMMAND's stdout — a notice on stdout would be swallowed
	// by that redirect, or corrupt a piped payload.
	out := o.pr(o.Stderr)
	out.print("[bold yellow]" + config.NoPacksMessage + ".[/bold yellow]")
	out.print("[yellow]" + config.NoPacksGuidance + "[/yellow]")
}

// notePackHostAccess prints, to stderr, what each loaded pack READS from the host this
// launch — its mounts, host-file reads, installer URLs, host-prepended briefings, and env
// vars. This is the transparency half of the fetched-pack approval model: a pack (fetched or
// local) that touches the host says so at every launch, not just once in a lockfile, so the
// effective environment is always visible.
//
// It reads the FOOTPRINT, which already reflects the approval gate: an unapproved fetched
// pack has MayAccessHost=false, so its host-read claims are absent from the footprint and
// correctly do not appear here (they were refused). A pack that touches nothing prints
// nothing.
//
// WHICH KINDS ARE COVERED IS DATA, NOT A SWITCH HERE (packloopholes.go's
// disclosureClasses). This function used to switch on a hardcoded `KindMount, KindReadsHost,
// KindEnv` and DROP every other claim kind, with no test to catch it — so kinds that read
// the host through a different declaration (`program via installer`, `briefing after
// host:`) were never disclosed, and the next host-crossing kind would have been dropped the
// same way (loophole-packaging.md §3.3, §4.3 G4). The classification is now exhaustive over
// packdecl.KnownKinds() by test.
//
// Host EXECUTION does NOT print here. It prints at the spawn boundary, BEFORE
// startLoopholes — see startLoopholesDisclosed. For a read, printing at the banner is
// cosmetic; for an exec it would be a notification that something already happened.
func (o *Options) notePackHostAccess(loadedPacks []*packload.Pack) {
	lines := disclosedClaims(loadedPacks, disclosureRead)
	if len(lines) == 0 {
		return
	}
	out := o.pr(o.Stderr)
	out.print("[dim]Pack environment this launch:[/dim]")
	for _, l := range lines {
		out.print("[dim]  " + l.pack + ": " + l.claim + "[/dim]")
	}
}

// noteUseProfiles prints, to stderr, where this launch's profile selections landed
// (profiles-as-pack-variants.md §3.3): one line per DISTINCT name in the effective
// table, naming the packs that DECLARE a variant of that name and the packs that
// RECEIVED it. It reads the same merge the env block emits (effectiveUseProfiles), so
// the line cannot describe a table the jail did not get.
//
// RECEIVED is deliberately every selected pack, not the pack the name keys to: the
// table crosses to the jail whole and every pack's derive sees all of it, so "who got
// it" has no narrower honest answer. DECLARED is the packs shipping a `kind: "profile"`
// with that name — the half that says whether the name means anything to any selected
// pack. It is NOT the packs that will act on it: a pack may declare the name and then do
// its variant work inside a derive this process cannot see.
//
// The verb is deliberately never "honored" (OQ-10). What a derive does with the string
// is unobservable from here, and a transparency print that overclaims is the
// silent-skip failure wearing a badge.
//
// Printed only when a name was selected at all. A launch with no profile is the common
// case, and restating its absence on every launch would be noise, not disclosure.
func (o *Options) noteUseProfiles(effective *jsonx.OrderedMap, loadedPacks []*packload.Pack) {
	if effective.Len() == 0 {
		return
	}
	// The line is per NAME, and the table is keyed by CLI: fold the values to the
	// distinct set. A non-string value (a null in use_profiles, which REMOVES a
	// profile) is not a selection and prints nothing.
	seen := map[string]bool{}
	var names []string
	for _, cli := range effective.Keys() {
		v, _ := effective.Get(cli)
		name, ok := v.(string)
		if !ok || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
	}
	if len(names) == 0 {
		return
	}
	sort.Strings(names)
	received := make([]string, 0, len(loadedPacks))
	for _, p := range loadedPacks {
		received = append(received, p.Name)
	}
	sort.Strings(received)
	out := o.pr(o.Stderr)
	for _, name := range names {
		// DECLARED: every selected pack whose manifest ships a variant of this exact name.
		// The name is owned only WITHIN a pack (§3.4), so two packs declaring it are both
		// listed — they are unrelated declarations of one selector value, and saying so is
		// more useful than hiding the coincidence.
		var declared []string
		for _, p := range loadedPacks {
			if p.Decl.ProfileFor(name) != nil {
				declared = append(declared, p.Name)
			}
		}
		sort.Strings(declared)
		who := "none"
		if len(declared) > 0 {
			who = strings.Join(declared, ", ")
		}
		out.print("[dim]Profile " + name + ":[/dim] declared: " + who + "; received: " +
			strings.Join(received, ", "))
	}
}

// ensureStorage wraps storage.EnsureGlobalStorage, wiring the v2 layout
// migration (audit 2026-07-18 §B#2: passing nil left the dangling-mise-symlink
// heal + layout-version stamp as dead code that never ran under the gate).
// canReclaim returns false — the conservative fail-safe (DEFER the heal
// when it can't confirm no live jail holds the store, leaving the marker
// unstamped to retry); the full live-container probe is the run-slice's concern,
// and declining is always safe. insideJail short-circuits (never scans /mise).
func ensureStorage() error {
	return storage.EnsureGlobalStorage(func() {
		insideJail := os.Getenv("YOLO_VERSION") != ""
		storage.MigrateStorageLayout(insideJail, func() bool { return false }, func(msg string) {
			fmt.Fprintln(os.Stderr, msg)
		})
	})
}

// runContainer is the post-config flow: the attach-to-existing decision
// (with orphan reaping), then the fresh-launch path (config-change approval,
// workspace flock + raced re-check, stale-container removal, image load, argv
// assembly, host-service start, tracking/owner-PID, port forwarding, the
// run_with_proxy launch with the FROZEN teardown guard stack).
//
// channel is the profile/provider environment Run composed above the dispatch. This arm
// consumes it rather than re-deriving any part of it: assembly emits it onto the argv,
// and the credential pre-flight answers against it.
func (o *Options) runContainer(cfg *jsonx.OrderedMap, rt, repoRoot, cname string, staged stagedPacks,
	injectedArgs []string, channel *packChannel) int {
	out := o.pr(o.Stdout)
	// Staged above the dispatch (see Run): this path consumes the result rather than
	// producing it. packStaging is the tree /ctx/packs binds; loadedPacks is what the
	// mount assembler reads declarations from.
	packStaging, loadedPacks := staged.root, staged.packs

	// Command construction (needed for both exec and run paths).
	//
	// The flags are already in injectedArgs: Run resolves them above the backend dispatch,
	// from the staged set and the launch's effective profile table, and this arm consumes
	// that one result rather than repeating the fold (see the note at the injection site).
	// That placement is also why this construction serves the ATTACH path into an
	// already-running jail unchanged: staging runs on attach too, so the set the injection
	// read is the set the jail is running.
	fullCommand := append([]string{}, injectedArgs...)
	targetCmd := "bash"
	if len(fullCommand) > 0 {
		targetCmd = shquoteJoin(fullCommand)
	}

	// Sweep jails orphaned by an uncatchable kill before the attach decision.
	o.reapOrphanedJails(rt)

	existingCID := ""
	if !o.New {
		existingCID = o.findRunningContainer(cname, rt)
	}

	// Refresh the per-jail skills + AGENTS/CLAUDE staging on every invocation.
	agentsPath, err := o.refreshJailBriefings(cname, cfg, rt, staged)
	if err != nil {
		out.printf("[bold red]%s[/bold red]", err.Error())
		return 1
	}

	if existingCID != "" {
		return o.attachExisting(cname, rt, targetCmd, cfg, false)
	}

	// --- Fresh launch: config-change approval ---
	wsCfg, _ := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
	if !o.checkConfigChanges(wsCfg) {
		return 1
	}

	// --- Freeze this launch's config artifacts under <workspace>/.yolo ---
	o.writeLaunchConfigArtifacts(cfg)

	// --- Workspace flock (non-blocking first, then blocking with a notice) ---
	//
	// Both notices go to STDOUT, alongside the "Attaching to jail started by
	// another process" line the wait usually ends in: the two are one sequence to
	// the reader — why the terminal paused, and what it did when the pause ended —
	// and splitting them across streams would let a piped log show one without the
	// other.
	lockDir := filepath.Join(paths.GlobalStorage(), "locks")
	_ = os.MkdirAll(lockDir, 0o755)
	lock, lerr := acquireWorkspaceLock(filepath.Join(lockDir, cname+".lock"), o.Workspace,
		lockNotices{
			warn:    func(msg string) { out.printf("[dim]Warning: %s[/dim]", msg) },
			waiting: func(msg string) { out.printf("[bold cyan]%s[/bold cyan]", msg) },
		})
	if lerr != nil {
		out.printf("[bold red]%s[/bold red]", lerr.Error())
		return 1
	}

	// Re-check after acquiring the lock — another process may have won.
	if !o.New {
		if raced := o.findRunningContainer(cname, rt); raced != "" {
			lock.Close()
			return o.attachExisting(cname, rt, targetCmd, cfg, true)
		}
	}

	// Remove any stopped container left from an unclean shutdown.
	if stale := o.findExistingContainer(cname, rt); stale != "" {
		o.pr(o.Stderr).printf("Removing stale container %s...", cname)
		o.removeStaleContainer(cname, rt)
	}

	// Retire jail-made workspace venvs from the old shared-store model.
	o.retireJailMadeVenv(cfg)

	timingStart := o.Now()

	// Image build/load. The result carries the REF of the image it made ready —
	// content-addressed on the normal path (C2), the legacy :latest tag on a
	// degraded fallback that has no store path to hash. Everything downstream
	// that names an image reads it from assembleInput.imageRef below; nothing
	// re-derives it.
	loadedImage := o.autoLoadImage(cfg, rt, repoRoot)
	if !loadedImage.OK {
		lock.Close()
		return 1
	}

	// ws_state overlay prep.
	wsState := o.prepareWsState(cfg, loadedPacks)

	// yolo-user-env.sh (frozen writer). The map is the channel's hydration, not a second
	// ResolveEnvSources pass: one walk, one set of warnings, and the file cannot describe
	// a channel the pre-flight below checked a different copy of.
	userEnv := channel.userEnv
	writeUserEnvFile(filepath.Join(wsState, "yolo-user-env.sh"), userEnv)

	// Broker singleton + relay: ensure BEFORE building the argv (the sockets-dir
	// mount + broker env are emitted by the assembler when the socket exists).
	//
	// GATED ON THE LOOPHOLE RECORD (docs/design/loophole-activation.md OQ-A11), which
	// is the same predicate the assembler already consults to decide the endpoint
	// variable, the CA mount and the in-jail terminator. Until this gate existed the
	// broker was THE counterexample to R1 sitting in the run pipeline: brokerEnsure was
	// called on every launch with no lookup at all, so the host singleton and one relay
	// per jail ran for EVERYBODY — including a user with `packs: []` who has never heard
	// of claude — while the jail was wired to it only when the loophole was Active. Both
	// halves failed, in opposite directions (§1.1).
	//
	// One predicate for the spawn and the wiring is the property to keep: they used to
	// disagree, and the disagreement is what made a daemon nobody's surfaces named. A
	// jail that does not get the broker's address must not leave a broker running on the
	// host either.
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	if rt != "container" && brokerLoopholeActive(cfg) {
		mkdirHostServicesDir(socketsDir)
		o.brokerEnsure()
	} else if rt != "container" {
		// The services dir is NOT the broker's — every loophole's endpoint file lands
		// there and the assembler mounts it unconditionally — so it is created either
		// way. Folding it into the gate above would leave the mount naming a directory
		// that does not exist on any launch where the broker is off.
		mkdirHostServicesDir(socketsDir)
	}

	// Store-prune gate (host-only; never from inside a jail — an inner CLI can't
	// see its siblings).
	//
	// THE ORPHAN-RELAY REAP THAT SHARED THIS ENUMERATION IS GONE. It existed
	// because a per-jail relay outlived the yolo process that spawned it, so a jail
	// ended from an attach session leaked one. There are no per-jail relays any
	// more: the front that replaced them is a goroutine in this process and dies
	// with it, and the daemon behind it is host-wide on purpose. A relay left over
	// from a PRE-UPGRADE yolo is swept by `yolo prune --apply`, which keeps that
	// sweep for exactly one release.
	storePruneOK := false
	if !o.inJail() {
		live, known := o.liveYoloContainers(rt)
		if known && len(live) == 0 {
			storePruneOK = true
		}
	}

	// Cache relocations: read from the HOST user config only (never the merged
	// config — see config.LoadCacheRelocations for the threat model) and
	// provisioned BEFORE the argv is assembled. Both halves of the ordering
	// matter: podman kills the whole container with a bare
	// "statfs …: no such file or directory" when a bind source is missing, and
	// the mountpoint it would otherwise invent for us is root-owned. A failure
	// here is fatal rather than a warning — continuing would start a jail whose
	// cache silently sits back on the filesystem the user moved it off.
	relocations, relErr := config.LoadCacheRelocations(func(msg string) {
		out.printf("[yellow]Warning: %s[/yellow]", msg)
	})
	if relErr != nil {
		out.printf("[bold red]%s[/bold red]", relErr.Error())
		lock.Close()
		return 1
	}
	// Apple Container gets the list (assembly warns that it is skipping them) but
	// not the directories: provisioning a mountpoint nothing will mount over just
	// leaves an empty stub in the cache that reads like lost data.
	if rt != "container" {
		if err := storage.EnsureCacheRelocations(relocations); err != nil {
			out.printf("[bold red]%s[/bold red]", err.Error())
			lock.Close()
			return 1
		}
	}

	// User host_files (docs/plans/host-file-staging.md). Read with the same
	// scope rule as cache_relocations — a SOURCE-BEARING entry comes only from the
	// host user config, never the merged/workspace one, so a repo cannot decide
	// which host files cross into the jail (config.LoadHostFiles enforces that by
	// construction). probeSource is on host-side only: host paths are deliberately
	// not in a jail's mount namespace, so stat'ing them from a nested run would
	// turn a valid host config into a fatal error.
	//
	// Unlike cache_relocations a failure here is a WARNING, not fatal: every entry
	// renders fail-open in the entrypoint anyway (a missing source falls back to
	// the defaults layer), so a jail that starts without one composed file is the
	// feature degrading, not the jail running against the wrong storage.
	hostFiles, hfErr := config.LoadHostFiles(cfg, func(msg string) {
		out.printf("[yellow]Warning: %s[/yellow]", msg)
	}, !o.inJail())
	if hfErr != nil {
		out.printf("[yellow]Warning: host_files: %s — no host files staged[/yellow]", hfErr.Error())
		hostFiles = nil
	}
	// Provision each destination's writable staging BEFORE the argv is assembled:
	// a missing bind source kills the whole container, and the GlobalHome symlink
	// hatch must exist before the :ro base is applied.
	if rt != "container" {
		prepareHostFiles(wsState, hostFiles)
	}

	// --- Assemble the ordered argv ---
	in := &assembleInput{
		cfg:              cfg,
		rt:               rt,
		cname:            cname,
		imageRef:         loadedImage.Ref,
		packs:            loadedPacks,
		agentsPath:       agentsPath,
		packStaging:      packStaging,
		wsState:          wsState,
		miseStore:        jailMiseStoreDir(o.inJail()),
		hostTZ:           detectHostTZ(),
		yoloVersion:      o.yoloVersion(repoRoot),
		mountTargets:     BindMountTargets(),
		lspNPMInstall:    lspNPMOf(cfg),
		lspGoInstall:     lspGoOf(cfg),
		storePruneOK:     storePruneOK,
		cacheRelocations: relocations,
		writableHomeDirs: config.WritableHomeDirs(cfg),
		hostFiles:        hostFiles,
		userEnv:          userEnv,
		channel:          channel,
	}
	runCmd := o.assembleRunCmd(in)

	// THE SEVENTH bespoke pre-flight (profiles-as-pack-variants.md §6.2, OQ-13), at the
	// one point in the pipeline where the assembled launch environment exists to check it
	// against: userEnv was hydrated above, and runCmd carries every -e pair the container
	// will start with. Refusing HERE, before the port forwarders and the loophole daemons
	// below, keeps the failure before any host-side process a refusal would have to clean
	// up — a jail that would fail its first API call is a failed launch, and the lock is
	// already held so the release travels with the return.
	//
	// The channel it checks is the one Run composed above the dispatch — the same value
	// the macos-user arm checks — and the argv pairs are folded into its lookup, because
	// a pack-shipped loophole's jail_env can put a credential on this argv that the
	// channel alone does not know about. The check itself is shared
	// (checkProviderCredentials); only the placement differs, for the attach reason
	// recorded on the macos-user arm.
	if lines, refuse := o.checkProviderCredentials(cfg, loadedPacks, channel, envPairs(runCmd)); len(lines) > 0 {
		o.printProviderRefusal(lines)
		lock.Close()
		// A set escape hatch turns the refusal into a loud continuation, so the verdict —
		// not the presence of output — is what ends the launch.
		if refuse {
			return 1
		}
	}

	// Determine the port-forward socket dir (Linux podman + AC only).
	//
	// Through hostForwardPorts, which reads the APPLIED network mode: this site used to
	// re-derive the CONFIGURED one inline and was the last of the four port gates not
	// reading the shared predicate (see the method's comment for both failures that
	// caused).
	forwardHostPorts := o.hostForwardPorts(cfg, rt)
	var portSocketDir string
	if len(forwardHostPorts) > 0 && (rt == "container" || !o.IsMacOS) {
		portSocketDir = o.fwdSocketDir(cname)
	}

	// Tracking + owner-PID + window title.
	_ = runtimeWriteTracking(cname, o.Workspace)
	o.writeOwnerPID(cname)

	// Start host-side port forwarding BEFORE the container.
	var socatProcs []*exec.Cmd
	if portSocketDir != "" {
		socatProcs = o.startHostPortForwarding(forwardHostPorts, cname, portSocketDir)
	}

	// Start host services (cgroup delegate + external) BEFORE the container,
	// inserting each `-e VAR=<path>` pair at index(image).
	//
	// Through startLoopholesDisclosed, never startLoopholes directly: the host-EXECUTION
	// disclosure has to precede the spawn (§4.3 G4). It used to be an entire phase LOWER,
	// down in the banner block — so a pack-shipped daemon was already running when its line
	// printed, and the spawn is silent on success, which meant "a fetched pack's daemon could
	// start on every launch for months with the only host-side record being a lockfile the
	// user has to go read." The wrapper also carries the inert-backend report, so a backend
	// that will start nothing says so instead of looking provisioned (B-0).
	hostServices := o.startLoopholesDisclosed(cname, rt, cfg, loadedPacks)
	// in.imageRef — NOT a re-derivation. The insert point is found by searching
	// the argv for the image ref, so this must be the very value assembly put
	// there or every pair below is silently dropped (see insertHostServiceEnv).
	// Reading the same field is what makes that divergence unrepresentable rather
	// than merely unlikely: before C2 both sides called jailImageRef(rt) and
	// agreed only because both hardcoded a constant.
	runCmd = insertHostServiceEnv(runCmd, in.imageRef, hostServices)

	// Final internal command tail.
	runCmd = append(runCmd, buildFinalInternalCmd(targetCmd, o.Timing))

	if o.Getenv("YOLO_DEBUG") != "" {
		// Write RAW (not via the rich-stripping printer): the argv contains
		// literal bracket sequences (e.g. the grep block_flags "-*[rR]*", the
		// "[path]" suggestion) that the rich-tag regex would eat.
		fmt.Fprintln(o.Stderr, shquoteJoinDebug(runCmd))
	}

	// Launch under the TTY proxy. on_started releases the lock once the
	// container is visible; on_terminate is the window-close/SIGTERM teardown.
	onStarted := func(_ *os.Process) {
		for i := 0; i < lockReleasePollAttempts; i++ {
			if o.findRunningContainer(cname, rt) != "" {
				break
			}
			time.Sleep(time.Duration(lockReleasePollIntervalSeconds * float64(time.Second)))
		}
		lock.Close()
	}
	onTerminate := func() {
		o.stopJail(cname, rt)
		cleanupPortForwarding(socatProcs, portSocketDir)
		lock.Close()
		o.stopLoopholes(hostServices, socketsDir, cname, rt)
		// E3, after stopJail so the jail is not still writing the surfaces we read.
		o.captureConfigOnTerminate(rt)
	}

	// Fresh-launch startup banner (with resource parts) to stderr for log
	// capture (audit §B#4.
	o.emitStartupBanner(rt, cname, resPartsFor(cfg, rt), "")

	// The empty-packs notice rides immediately behind the banner: this is the LAST
	// host-side output before the container takes the terminal, so it is the only spot
	// where the message is still on screen when the agent (or the fallback bash)
	// starts. Printed any earlier it scrolls away behind the nix build.
	o.warnIfNoPacks()

	// Right behind that: what each loaded pack READS from the host this launch. A fetched
	// pack CAN read the host now (with approval), so the effective host access must be
	// visible every launch, not just recorded in a lockfile — the transparency half of the
	// approval model.
	//
	// The READ half only. Host EXECUTION was disclosed above, before startLoopholes, and
	// deliberately not repeated here: this point in the pipeline is after the spawn, where
	// the same line would be a notification rather than a disclosure (§4.3 G4).
	o.notePackHostAccess(loadedPacks)

	// Right behind that: where the launch's profile selections landed. Same stderr, same
	// dim register, same reason — a selected profile is part of the effective
	// environment, and the human reading the launch should see the name every pack's
	// derive is about to receive rather than infer it from an env var.
	o.noteUseProfiles(channel.profiles, loadedPacks)

	rc, runErr := runWithProxy(runCmd, onStarted, onTerminate)
	if runErr != nil {
		out.printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", rt)
		out.print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		cleanupPortForwarding(socatProcs, portSocketDir)
		// Release the lock BEFORE stop_loopholes (its guard takes the same lock
		// non-blocking, and on_started never ran).
		lock.Close()
		o.stopLoopholes(hostServices, socketsDir, cname, rt)
		clearOwnerPID(cname)
		return 1
	}

	// Normal exit teardown.
	cleanupPortForwarding(socatProcs, portSocketDir)
	o.stopLoopholes(hostServices, socketsDir, cname, rt)
	// E3: the container is `--rm` and now gone, so fold this session's in-jail config
	// edits into their overlay sidecars from the host side, before anyone can ask
	// `yolo config diff` and get last session's answer.
	o.captureConfigOnTerminate(rt)
	clearOwnerPID(cname)
	o.maybeWarnAboutOOMKiller(rc, rt)

	// The host-side half of --timing's report — the other is the env pair
	// assemble.go puts on the container argv, which timingenv_test.go pins. This
	// half is deliberately UNPINNED, and a reader should know that before trusting
	// a green suite here: no test names the flag below this line, runContainer is
	// out of a unit test's reach, and deleting the block ships green. Debt handed
	// down rather than incurred — o.Profile was equally unpinned before the
	// OQ-PT5 rename (provider-table-fidelity.md §5.2). Whoever next touches this
	// surface owns the pin; if none comes, the flag's tested surface is the parse
	// and the argv pair, and the total is best-effort.
	if o.Timing {
		o.pr(o.Stderr).printf("[bold cyan]--- Host-side timing ---[/bold cyan]")
		o.pr(o.Stderr).printf("  Total (host-side):  %.3fs", o.Now().Sub(timingStart).Seconds())
	}
	return rc
}

// hostForwardPorts is the `network.forward_host_ports` entries this launch will
// actually forward — the list the socat spawner and the socket dir answer to.
//
// THE FOURTH AND LAST SPELLING OF THE PORT GATE. a38fe0ab moved `-p`,
// `forward_host_ports` and the `route_localnet` sysctl onto appliedNetMode — the mode
// the launch RUNS under rather than the one the config asked for — and its own commit
// message recorded what it left behind: this site, which re-derived the CONFIGURED mode
// inline. So the host half and the container half of one feature read two different
// answers, and they disagree in both directions:
//
//   - a NESTED launch declaring the key started one socat per forward and a socket dir
//     on the host, while the assembler (reading the applied mode, which is "host" there
//     because podman-in-podman is forced onto the launcher's netns) emitted neither the
//     `-v /tmp/yolo-fwd-…` mount nor `YOLO_FORWARD_HOST_PORTS`. Harmless — the processes
//     die at exit and a shared netns needs no forwarding hop — but nothing in the jail
//     was ever told about them;
//   - on Apple Container an unhonored `network.mode: "host"` was the harmful direction:
//     appliedNetMode answers "bridge" there whatever the key says, so the assembler
//     emitted `--publish-socket <hostSock>:…` for sockets THIS function then declined to
//     create, leaving the jail's forwards dead rather than merely unmentioned.
//
// One predicate for both halves is what makes each of those unrepresentable instead of
// separately fixed — the reason backendcaps.go gives for appliedNetMode existing at all.
// resolveNetMode and o.inContainer() rather than fresh copies of either, for the same
// reason the assembler uses them.
func (o *Options) hostForwardPorts(cfg *jsonx.OrderedMap, rt string) []any {
	netSec := cfgMap(cfg, "network")
	if netSec == nil {
		return nil
	}
	if appliedNetMode(rt, o.resolveNetMode(cfg), o.inContainer()) != "bridge" {
		return nil
	}
	return asAnyList(mapGet(netSec, "forward_host_ports"))
}

// insertHostServiceEnv splices each host service's `-e VAR=<jailPath>` pair into
// the container argv immediately BEFORE the image ref — the boundary between
// `podman run`'s flags and its positional arguments, so the pairs land as flags
// and not as arguments to the entrypoint.
//
// imageRef MUST be the same value assembly appended, which is why runNormal
// passes assembleInput.imageRef rather than re-deriving anything. Under C2 the
// ref is content-addressed and therefore no longer a constant two call sites can
// independently arrive at: hand this a stale ref and indexOfSlice returns -1,
// every pair is dropped, and the jail starts with no broker endpoint, no cgroup
// delegate and no host-process socket — silently, because a `continue` is not an
// error. The in-jail reachability witness would refuse the launch several
// hundred lines and one process boundary away from the cause.
func insertHostServiceEnv(runCmd []string, imageRef string, services []loopholeDaemon) []string {
	for _, svc := range services {
		// An EMPTY envVarName means "this handle has no variable to insert", which
		// today is the HOST-SCOPED loophole's front (startHostSingleton). Its
		// variable is emitted much earlier, by hostServicesMountArgs at argv-assembly
		// time, and deliberately OPTIMISTICALLY: a jail told about a service whose
		// front never published is refused by the in-jail reachability witness, where
		// a jail never told at all just runs without Claude auth and says nothing
		// (loopback-tls-reachability.md §7.3). Inserting it here as well would put
		// the same `-e` in the argv twice.
		if svc.envVarName == "" {
			continue
		}
		idx := indexOfSlice(runCmd, imageRef)
		if idx < 0 {
			continue
		}
		// The value is always a PATH — never a port, never an address. That is the
		// bootstrap-ordering invariant: the container's environment is frozen at
		// `podman run` time, so anything that can change (a restarted daemon's port,
		// a rotated token) has to live behind a stable path the client re-reads.
		runCmd = insertStrsAt(runCmd, idx, []string{"-e", svc.envVarName + "=" + svc.jailPath})
	}
	return runCmd
}

// attachExisting runs the exec-into-existing-container branch (and the
// raced-attach twin). raced selects the second banner text.
func (o *Options) attachExisting(cname, rt, targetCmd string, cfg *jsonx.OrderedMap, raced bool) int {
	out := o.pr(o.Stdout)
	// Startup banner to stderr — surfaces the jail's BAKED version so a host CLI
	// upgrade attaching to a pre-upgrade container (stale shims/mounts/entrypoint)
	// is visible at a glance (audit §B#4.
	o.emitStartupBanner(rt, cname, nil, o.bakedJailVersion(rt, cname))
	if raced {
		out.printf("[bold cyan]Attaching to jail started by another process [dim](%s)[/dim]...[/bold cyan]", cname)
	} else {
		out.printf("[bold cyan]Attaching to existing jail [dim](%s)[/dim]...[/bold cyan]", cname)
	}
	// Attach gets the notice too, and that is not symmetry for its own sake: once a
	// jail is up, attaching is how a user re-enters it, so a fresh-launch-only notice
	// is one a user with a long-lived jail may never see.
	o.warnIfNoPacks()
	// NOTHING TO HEAL HERE ANY MORE, and the absence is worth a note because the
	// call this replaces was deliberate. An attach used to re-ensure the per-jail
	// broker relay (behind the same gate the launch path uses, OQ-A11), because the
	// relay was a separate process that could have died since launch. The jail's
	// half of the credential path is now a front owned by the yolo process that
	// LAUNCHED the jail; a different process attaching cannot heal it, and starting
	// a second front over the same endpoint file would hand the jail a credential
	// its terminator never asked for. A jail whose launcher is gone is relaunched,
	// not attached-and-repaired.

	execFlags := []string{"-i"}
	if o.IsTTYStdout() {
		execFlags = append(execFlags, "-t")
	}
	runCmd := append([]string{rt, "exec"}, execFlags...)
	runCmd = append(runCmd, cname, "yolo-entrypoint", targetCmd)

	rc, err := runWithProxy(runCmd, nil, nil)
	if err != nil {
		out.printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", rt)
		out.print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		return 1
	}
	o.maybeWarnAboutOOMKiller(rc, rt)
	return rc
}

// detectHostTZ resolves the host timezone for the TZ env (or "").
func detectHostTZ() string {
	if tz, ok := storage.DetectHostTimezone(); ok {
		return tz
	}
	return ""
}

func lspNPMOf(cfg *jsonx.OrderedMap) string { n, _ := resolveLSPInstalls(cfg); return n }
func lspGoOf(cfg *jsonx.OrderedMap) string  { _, g := resolveLSPInstalls(cfg); return g }

// runtimeWriteTracking wraps runtime.WriteContainerTracking with the resolved
// workspace path.
func runtimeWriteTracking(cname, workspace string) error {
	resolved := resolvePath(workspace)
	return writeTracking(cname, resolved)
}

// emitStartupBanner writes the start-of-run banner to stderr (audit §B#4). It
// reuses StartupBanner for consistent formatting. version is
// version.Get; jailVersion is the container's baked
// YOLO_VERSION (attach path only, else "").
func (o *Options) emitStartupBanner(rt, cname string, resParts []string, jailVersion string) {
	// Resolve the repo root via the shared method (o.RepoRoot → reporoot.Resolve),
	// so the banner version matches run/check and describes the yolo-jail repo,
	// not whatever repo the cwd happens to sit in. "" → version.Get falls back to
	// the baked stamp / "unknown".
	repoRoot := ""
	if o.RepoRoot != nil {
		if rr, ok := o.RepoRoot(); ok {
			repoRoot = rr.Root
		}
	}
	banner := StartupBanner(version.Get(repoRoot), rt, cname, resParts, jailVersion)
	// Fprintln, not Fprint: StartupBanner returns no trailing newline, so the old
	// Fprint left the cursor mid-line and whatever printed next was glued onto the
	// banner ("…pids=32768No packs are configured…", observed in a nested launch).
	// It was invisible before only because the next writer happened to be the
	// container's own output, which opens with its own newline.
	fmt.Fprintln(o.Stderr, banner)
}

// bakedJailVersion reads the YOLO_VERSION baked into a running container via
// `<rt> inspect`, or "". Shown in the
// attach banner only when it differs from the host version.
func (o *Options) bakedJailVersion(rt, cname string) string {
	if o.Exec == nil {
		return ""
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cname}, "", nil, 3*time.Second)
	if !res.Ran || res.RC != 0 {
		return ""
	}
	if v, ok := runtime.BakedYoloVersionFromInspectEnv(strings.Split(res.Stdout, "\n")); ok {
		return v
	}
	return ""
}

// resPartsFor reconstructs the banner's resource-limit parts (memory/cpus/pids)
// from the resources config, matching the res_parts built
// during argv assembly. Podman path: pids defaults to 32768. Apple Container's
// half-host defaults are the run-slice's concern; here only explicit config is
// surfaced (the native run path is podman/Linux).
func resPartsFor(cfg *jsonx.OrderedMap, rt string) []string {
	var parts []string
	res, _ := cfg.Get("resources")
	rm, _ := res.(*jsonx.OrderedMap)
	get := func(k string) (any, bool) {
		if rm == nil {
			return nil, false
		}
		return rm.Get(k)
	}
	if mem, ok := get("memory"); ok {
		if s, ok := mem.(string); ok && s != "" {
			parts = append(parts, "memory="+s)
		}
	}
	if cpus, ok := get("cpus"); ok && cpus != nil {
		parts = append(parts, "cpus="+pyStrCoerce(cpus))
	}
	if rt != "container" {
		pids := "32768"
		if p, ok := get("pids_limit"); ok && p != nil {
			pids = pyStrCoerce(p)
		}
		parts = append(parts, "pids="+pids)
	}
	return parts
}

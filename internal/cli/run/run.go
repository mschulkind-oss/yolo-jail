package run

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/claudeview"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/durable"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/hostcas"
	"github.com/mschulkind-oss/yolo-jail/internal/ioprio"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	_ "github.com/mschulkind-oss/yolo-jail/internal/packreg" // registers the embedded packs with packload
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"github.com/mschulkind-oss/yolo-jail/internal/richtext"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
	"github.com/mschulkind-oss/yolo-jail/internal/storage"
	"github.com/mschulkind-oss/yolo-jail/internal/version"
)

// Run validates config, resolves the runtime, then either execs into
// an existing container or launches a fresh one. Returns the process exit code.
// The whole flow is driven off the
// injected seams so the probe + argv-assembly paths are unit-testable.
// The result is NAMED so the deferred launch-log trailer records the code this
// function actually returns, from any of its many exits, rather than the one the
// happy path was about to produce.
func Run(opts Options) (rc int) {
	fillDefaults(&opts)
	o := &opts

	// THE LIVE-OVERLAY GUARD, before ANY other work — no storage touch, no config
	// load, no staging: this launch would jail the running session's own live
	// workspace over its own home (liveoverlayguard.go states the two measured
	// incidents). Refusing here is the cheapest refusal in the whole pipeline.
	if refuseLiveWorkspaceLaunch(o) {
		o.pr(o.Stderr).print("[bold red]Refusing to launch: the workspace is /workspace and " +
			"this yolo is already inside a jail — /workspace is the LIVE bind of the " +
			"running session's own workspace, whose .yolo overlay IS that session's " +
			"home; a fresh launch here regenerates its agent config under it.[/bold red]")
		o.pr(o.Stderr).print("[dim]Run nested launches from a throwaway workspace " +
			"(AGENTS.md, Nested-jail verification): mkdir -p /tmp/yolo-nested && " +
			"cd /tmp/yolo-nested && yolo … — any directory but /workspace. Set " +
			"YOLO_ALLOW_LIVE_WORKSPACE=1 if you truly mean it.[/dim]")
		return 1
	}

	// THE WORKSPACE-SCOPE GUARD, in the same window and for the same reason: this launch
	// would bind the host side of the credential boundary into the jail — the home itself,
	// or one of yolo's own two host directories (workspacescopeguard.go states what each
	// one grants a jail that can write it). Also before the launch log, and here that is
	// not merely tidy: the workspace being refused is the HOME, so the tee would write
	// ~/.yolo/launch.log — the very stray directory this guard exists to stop.
	if refusal := refuseWorkspaceScope(o); refusal != nil {
		o.pr(o.Stderr).print("[bold red]Refusing to launch: " + refusal.what +
			". The workspace is bind-mounted into the jail, so " + refusal.why +
			".[/bold red]")
		o.pr(o.Stderr).print("[dim]yolo takes the CURRENT DIRECTORY as the workspace " +
			"(there is no --workspace flag): cd into the project you meant. To jail a " +
			"dotfiles tree, name the directory that holds it (~/.dotfiles), never the " +
			"home itself — there is no override for this one.[/dim]")
		return 1
	}

	// THE LINKED-STATE GUARD, still before the launch log, whose tee is the first write under
	// <workspace>/.yolo. `.yolo` and `.yolo/home` are both writable from inside the jail
	// (through the workspace bind), so either can be a link the last jail left: every host
	// write below it would follow it, and podman would bind whatever the bind sources under
	// it then resolve to (wsstatebeneath.go, linkedWorkspaceState).
	if linked := linkedWorkspaceState(o.Workspace); linked != "" {
		o.pr(o.Stderr).print("[bold red]Refusing to launch: " + linked + " is a symbolic " +
			"link. The jail can write this workspace's .yolo, so a link there would carry " +
			"the launcher's writes, and the container's binds, to wherever it points.[/bold red]")
		o.pr(o.Stderr).print("[dim]Remove the link (rm " + shquote.Quote(linked) + "); yolo recreates the " +
			"directory on the next launch. There is no override for this one.[/dim]")
		return 1
	}

	// PERSIST THE LAUNCHER'S HALF (report-tiers.md, the launch stream). Everything this process
	// prints from here on is teed into <workspace>/.yolo/launch.log, beside the
	// entrypoint's boot.log, so the half of a launch that used to vanish when the
	// terminal scrolled is readable afterwards — including after a launch that
	// refused, where there is no jail left to read anything from.
	//
	// AFTER the live-overlay guard and before everything else: that refusal is the
	// launch's first act precisely because it happens before any side effect, and
	// creating a file under the workspace it is refusing to touch would be one.
	// Everything below it is fair game, and a failure here is silent by design
	// (launchlog.go).
	launchLog := attachLaunchLog(o)
	defer func() { launchLog.finish(rc) }()

	// THE MACHINE-WIDE LAUNCH LINE (launchrecord.go, OQ-PR3): one line per launch in
	// GLOBAL_STORAGE/logs/launches.log, written when the container starts or the attach
	// begins — or here, at return, for a launch that did neither. Armed in the same window as
	// the launch log, for the same reason: the guards above refuse before any side effect.
	o.armLaunchRecord()
	defer func() { o.recordLaunchExit(rc) }()

	// THIS LAUNCH'S PACK RECORDS ARE ITS OWN. stagePacks records the pack-shipped
	// loophole modules, the `supersedes` claims and the pack skills sources
	// process-wide — the convergence point that stopped seven discovery surfaces
	// assembling seven views of this machine — and a process runs more than one
	// launch: auto-capture below runs this very pipeline for each throwaway capture
	// jail, whose staging root its own cleanup then deletes. Releasing the scope on
	// every return path is what keeps a sub-launch's record from becoming the parent's
	// (packrecords.go has the measurement, and why restoring is not `Set…(nil)`).
	//
	// Here rather than beside stageRunPacks: the snapshot has to be taken before
	// anything can write a record, and a defer at the top covers the refusals below
	// too. It is pure memory, which is why it can sit above the timing collector and
	// still leave the live-overlay guard as the launch's first act.
	releasePackRecords := packRecordScope()
	defer releasePackRecords()

	// The timing collector starts here — after the live-overlay refusal has had
	// its say (a refused launch writes no file), before Phase 1, so the probes
	// are spanned too. cname derives from the workspace alone
	// (runtime.FromWorkspace), which is what makes it knowable this early.
	o.initPerf(runtime.FromWorkspace(o.Workspace))

	// --- Phase 1: probes (repo root, storage, config, runtime) ---
	// Repo-root resolution is a HARD GATE for the container backends: without a
	// flake there is nothing to build the image from, and silently running a
	// stale loaded/cached image instead is worse than failing — it hides that the
	// environment is not what the config describes. (This reverts D2's graceful
	// degradation: launching on an old image with no rebuild was deemed a
	// footgun, not a convenience.) The gate is applied below, under an explicit
	// `rt != "macos-user"` guard rather than by sitting after the macos-user
	// branch, because the two arms refuse for different reasons and say so
	// differently — one is about an image build, the other about a native nix
	// build. Naming the split beats encoding it in statement order, which is how it
	// was expressed until pack staging had to move above the dispatch (B-0).
	//
	// ⚠ THE EXEMPTION IS GONE (2026-09-12), and its history is the reason this
	// comment is long. It began as "macos-user needs no repo at all", which was
	// true while that backend's only nix work was the user's own `packages:`. Then
	// it narrowed to "…unless `packages:` is non-empty", after a launch with an
	// empty repo root reached darwinpkg.Materialize, left exec.Cmd.Dir empty and
	// had nix resolve a flake from the user's own cwd (measured 2026-09-03). Now
	// there is no exemption: every macos-user launch builds the non-container FLOOR
	// (docs/design/macos-user-provisioning.md, OQ-P1), so every macos-user launch
	// needs the flake. --dry-run stays exempt because it materializes nothing.
	repoRes, repoRootOK := o.RepoRoot()
	repoRoot := repoRes.Root
	if err := o.ensureStorage(); err != nil {
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return 1
	}
	// THE PACK REFRESH, before anything resolves a pack (maintainer ruling, 2026-09-25):
	// a never-fetched git pack is fetched here and a branch-following one re-fetched at
	// most hourly (packrefresh.go). Above config validation, not merely above staging,
	// because validation already resolves the selected packs (writable_home_dirs
	// reservation, profile keys) and would otherwise judge a pack this launch is
	// about to deliver as absent. A no-op in a jail. Pinned by
	// TestLaunchFetchesANeverInstalledGitPack.
	o.refreshPacks()
	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		return 1
	}
	// THE NOTCH GATE, above the backend dispatch and above the repo-root gate, because
	// which notch this is decides whether there is a launch at all — before which
	// mechanism would run it, and before anything is built for it.
	if rc, refused := refuseUnbuiltNotch(o, cfg); refused {
		return rc
	}
	rt, ok := o.resolveRuntime(cfg)
	if !ok {
		if o.readinessInterrupted() {
			return 130 // a Ctrl-C during the podman readiness wait (podmanready.go)
		}
		return 1
	}
	o.runtime = rt
	o.Perf.Mark("probes.done")

	// The two container-only gates, hoisted ABOVE pack staging so a launch that is
	// going to refuse still refuses before it does any staging work — the order the
	// container path has always had, kept intact now that staging moved earlier.
	// Both are skipped for macos-user: --dry-run is that backend's own flag, and the
	// repo-root refusal it needs says something different (it is about a native nix
	// build, not an image), so it lives in the `else` arm below.
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
	} else if !repoRootOK && !o.DryRun {
		// THE MACOS-USER ARM, which used to be conditional on `packages:` and is not
		// any more. Every launch on this backend is materialized by a host-side `nix
		// build` against this flake — the FLOOR alone is everything darwinpkg.FloorNames()
		// lists, before the user declares anything — so the repo is exactly as required
		// here as it is for an image build.
		//
		// Un-gated, the launch reached darwinpkg.Materialize with repoRoot "", which
		// left exec.Cmd.Dir empty — so nix inherited the CALLER's cwd and resolved a
		// flake from the user's own workspace. Measured 2026-09-03: the failure reads
		// `path "<workspace>" is not part of a flake`, naming a directory the user
		// never pointed yolo at and never mentioning the repo root that is actually
		// missing. Worse than the confusing message: if the workspace HAPPENS to be a
		// flake, nix evaluates THAT one — an unrelated project's flake, on the one
		// backend whose build step runs unconfined as the invoking user
		// (docs/design/macos-user-build-step-threat-model.md Vector A).
		//
		// --dry-run stays exempt because it materializes nothing (RunMacosUser
		// returns before the nix build, with darwin=nil), and refusing a plan render
		// would hide the very plan a user asked to inspect.
		o.pr(o.Stderr).print("[bold red]Cannot find yolo-jail repo root.[/bold red]\n" +
			"The macos-user backend builds its tools from the yolo-jail flake with native\n" +
			"nix — the core set every jail gets (git, node, mise, ripgrep, …) as well as\n" +
			"anything in [bold]packages:[/bold] — so it needs the flake even when you declare nothing.\n\n" +
			"Fix: reinstall so the flake bundle ships with the binary (`just install`), or\n" +
			"point yolo at a checkout with [bold]YOLO_REPO_ROOT[/bold]. The working directory\n" +
			"is never consulted, so standing in a checkout is not enough:\n" +
			"  YOLO_REPO_ROOT=~/code/yolo-jail yolo …\n\n" +
			"[dim]`yolo run --dry-run` still works with no repo: it prints the plan and builds\n" +
			"nothing.[/dim]")
		return 1
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
	o.stagingCfg = cfg
	// Each arm takes the workspace launch lock where it first touches what the workspace's
	// launches share (holdLaunchLock); this is the release for every return that does not reach
	// an arm's own, earlier one. Idempotent.
	defer o.releaseLaunchLock()
	// And the session lock a container arm takes (sessionlock.go), released at every return:
	// auto-capture runs this pipeline in-process, so a process exit is not the only release.
	defer o.releaseSessionLock()
	// And every loopback port this launch reserved and did not hand on (servedaddresses.go), for
	// the same reason: a refused launch, a dry run, an attach.
	defer o.releaseReservedPorts()
	// THE HERDR PANE's slot, made here, before any signal arm exists, so an arm's teardown can
	// release it from its own goroutine. Each arm registers into it only once its arm is
	// installed, just before its session starts, and releases it when the session returns
	// (herdragent.go's WHEN); this defer covers every other return.
	o.herdr = &herdrPane{}
	defer o.releaseHerdrAgent()
	staged, stagedOK := o.stageRunPacks(cname)
	if !stagedOK {
		return 1
	}
	// THE LAUNCH GUARD (launchguard.go, JL-D75), from the moment there is a pack tree to lose: a
	// signal before the keeper's spawn ends the launch through it, which discards what no container
	// holds and gives the terminal back, where the default action ran no cleanup at all. The
	// keeper's arm or an attach's takes over from it; this is its retirement at every other return,
	// after the discard below.
	if rt != "macos-user" { // parity: Dropped — macos-user has no keeper to hand the tree to, and its host daemons run from the tree under the TTY proxy's own arm, beside which a guard would act too; a signal before its session leaves the tree for the reaper, as it did (JL-D75)
		o.armLaunchGuard(cname, rt)
		defer o.endLaunchGuard()
	}
	// This launch's pack tree goes at return unless a started container holds it (packtree.go).
	defer o.discardUnheldPackTree(cname)

	// THE FORK PINS, read (never resolved) and disclosed above the dispatch, so every backend and
	// an attach say which revision each source-built program is at (OQ-FP6, forkbuild.go).
	o.forkPinned = o.noteForkPins(staged.packs)

	// PACK LAUNCH FLAGS, ABOVE THE DISPATCH — the same B-0 move pack staging made, for
	// the same reason. The injection used to sit inside runContainer, which the
	// macos-user arm returns before reaching, so on that backend a pack's declared
	// launch flags did nothing at all. copilot's `--yolo` was a 100% drop — it had no
	// config half to fall back on (it was a plain `launch` contribution then and is an
	// autonomy posture's now, which changes where it is declared and not whether it is
	// injected); claude's `--dangerously-skip-permissions` fell back to
	// defaultMode: acceptEdits, which auto-accepts EDITS and not Bash or WebFetch.
	//
	// THE STAGED SET, and the EFFECTIVE PROFILE TABLE with it — both read off staging,
	// which is why this sits BELOW stageRunPacks rather than above it. When the
	// injection moved up out of runContainer, staging had not moved yet, so this line
	// took packload.Embedded(): it was the only pack set in scope, and "what yolo ships
	// is what a bare `yolo -- <bin>` gets" was the cheapest true sentence available. It
	// stopped being the right sentence the moment staging hoisted: the embedded set is
	// NOT what the jail runs, the staged set is, and the difference is every configured
	// pack — whose declared launch flags an embedded-set injection drops, silently. It
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
	channel, err := o.launchChannel(cfg, staged.packs)
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
		injectedArgs = o.injectLaunchFlagsDisclosed(staged.packs, injectedArgs)
	}
	// A SHARED NETWORK, SAID (OQ-NC3): above the dispatch for the launch flags' reason, so every
	// arm and an attach print it from here (sharednetwork.go).
	o.noteSharedNetwork(cfg, rt)

	// THE JAIL-DAEMON PAYLOAD, composed once here — the fourth B-0 hoist, after the pack
	// trees, the launch flags and the channel, and for the same reason as every one of
	// them: it was composed INSIDE the container argv assembler, which this dispatch's
	// native arm returns before reaching.
	//
	// What that cost is stated in docs/reference/macos-user-nix-and-features.md and it is
	// the DEFAULT configuration rather than an edge case: `packs/claude` `needs` both
	// `openai-auth` and `wire-bridge` unconditionally, so a bare `"packs": ["claude"]`
	// selects two jail daemons, and on macos-user the payload naming them was never
	// composed at all — the endpoint was published and ACL-granted with nothing listening,
	// and `CODEX_REFRESH_TOKEN_URL_OVERRIDE` pointed at a dead port. Neither arm said so.
	//
	// BOTH ARMS CONSUME THIS VALUE: the container arm threads it onto the argv as
	// `-e YOLO_JAIL_DAEMONS=` (assembleInput.jailDaemons), and the native arm splits it
	// (loopholes.JailDaemonsRunIn) into the daemons its Seatbelt guest runs — handed to the
	// guest's supervisor, OQ-DP8/OQ-DP9 — and the ones it declines BY NAME.
	jailDaemons := o.jailDaemonsFor(cfg, rt, staged.packs)
	// None under the seal (seal.go): a fork build runs no loophole and no pack service.
	if o.Sealed {
		jailDaemons = nil
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
		// THE CONTEXT MOUNTS, first on this arm (docs/design/context-mounts.md §4 steps 3-5):
		// each declared one this backend can deliver becomes a root-owned link plus Seatbelt
		// rules, and one it cannot ends the launch HERE, naming each, before the approval
		// prompt, a host service or any staging — a refusal says what to do before the launch
		// asks anything else (DP-D15). A --dry-run refuses too: its plan would describe a
		// launch that cannot happen. No `mounts` key and no pack `mount` decides nothing.
		ctxLinks, ok := o.planMacosUserCtxMounts(cfg, staged.packs)
		if !ok {
			return 1
		}
		// (a bare `yolo` opens an interactive login zsh in the sandbox).
		agentArgv := injectedArgs
		if len(agentArgv) == 0 {
			agentArgv = []string{"/bin/zsh", "-l"}
		}
		var homeOverlay macosuser.HomeOverlay
		// CONFIG-CHANGE APPROVAL, on this arm too (docs/reference/config-safety.md:
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
		if !o.DryRun && !o.checkConfigChanges(wsCfg, cfg, rt) {
			return 1
		}
		// Same notice as the container paths: a brand-new macos-user user has no packs
		// either, and the native backend is where a "where is my agent?" is hardest to
		// diagnose (no image, no provisioning output to read back).
		o.warnIfNoPacks()
		// THE HOST SERVICES, on this arm too — the WHOLE set, through the same spawn
		// boundary the container path uses.
		//
		// WHAT THIS REPLACED, AND WHAT IT WAS RIGHT ABOUT. Until now this arm started ONE
		// service (the OpenAI credential broker, through startOpenAIAuthDisclosed) and
		// handed notePackLoopholesInert withoutOpenAIAuthPack, so the one pack whose
		// service DID start was not also reported inert. That scoping is correct for a
		// SUBSET and is an underclaim for the full set: the exec disclosure must name every
		// daemon about to run, and a pack reported inert while its daemon starts is the
		// same untruth with the sign flipped. Both halves therefore take the whole pack set
		// now, and the pairing stays complementary by construction rather than by two
		// filters agreeing.
		//
		// NOTHING HERE NEEDED A REACHABILITY MEASUREMENT, which is why the subset was all
		// that was left to generalise. sharesLauncherNetns is true for this backend by
		// CONSTRUCTION (loopholesruntime.go), so every loopback-TLS daemon publishes
		// 127.0.0.1 and the sandbox — an ordinary child of this process on this process's
		// own network stack — dials the listener's own loopback; the Seatbelt profile
		// contains no network operation at all. The credential boundary that a 0600
		// endpoint file raises across the uid split is answered in code:
		// macosuser.EndpointGrantCommands grants the sandbox account read on the file and
		// search on its directory, staged by BuildRunPlan for every endpoint variable this
		// launch carries.
		//
		// IT IS ROUTED THROUGH startLoopholesDisclosed, and that is the generalisation
		// rather than a tidier spelling of it. The paragraph this replaced said the wrapper
		// was not used because "disclosure inseparable from the SPAWN stopped being free
		// here" once this arm spawned pack code of its own — which argued for a second
		// boundary while the arm started a second, smaller set. With one set there is one
		// boundary, and inheriting it is strictly stronger than restating the ordering:
		// notePackHostExec cannot be separated from the spawn by any later edit to this
		// arm. The READ half is still printed by this arm itself, below the context-tree
		// composition (notePackHostAccess).
		//
		// Placing the inert REPORT beside warnIfNoPacks is still the honest choice for it:
		// both answer "what will this launch not do for you". What it has left to say here
		// is the PLATFORM axis alone — a backend that starts every host service has no
		// backend-shaped reason left, which is why loopholeinert.go no longer has a
		// macos-user case.
		//
		// ⚠ NEVER EXECUTED ON HARDWARE. Every line of this is pinned by unit tests and none
		// of it has run on a Mac; the self-hosted arm64 runner
		// (docs/plans/runbooks/mac-actions-runner.md) is where that changes.
		// ONE AGENT'S LAUNCH ENV: this backend runs one command under one session env file,
		// so the credential gate's per-agent half can reach only the program this invocation
		// starts (launchEnv's doc; noteMacosUserCredentialScope says so on the terminal).
		launched := filepath.Base(agentArgv[0])
		launchEnv := channel.launchEnv(launched)
		// THE GUEST'S HALF OF THE PAYLOAD (OQ-DP8, OQ-DP9; macosuserguestdaemons.go): the
		// daemons this sandbox runs, confined, and the ones it declines by name. The split
		// is loopholes.JailDaemonsRunIn, the same one the served set composed the channel
		// with, so what the agent was pointed at and what the supervisor starts are one set.
		guestDaemons, declinedDaemons := loopholes.JailDaemonsRunIn(rt, jailDaemons)
		// THE DOORWAYS OUTSIDE (HS-D15; macosuserdoorways.go): every credential doorway the
		// payload declares a host argv for opens on the Mac's loopback as this launch's own
		// listener, at the served address and caller token the channel composed its clients with.
		doorways := o.planMacosUserDoorways(rt, jailDaemons, staged.packs, channel)
		// A client that binds its daemon's caller token itself reads it from its own
		// environment, as it does from a container's shared channel: the Codex launcher's
		// auth.json writer binds the refresh doorway's, wherever that doorway runs.
		for k, v := range channel.guestSharedCallerTokens(loopholes.ServedJailDaemons(rt, jailDaemons)) {
			launchEnv.Set(k, v)
		}
		o.noteCredentialScope(channel)
		o.noteMacosUserCredentialScope(channel, launched)
		if o.DryRun {
			// A plan render starts nothing, so the spawn boundary is not crossed: there is
			// no host EXECUTION to disclose (a line saying otherwise would name daemons this
			// invocation will not run), and the inert half — the one about what the launch
			// will NOT do — prints on its own.
			//
			// THE ONE ENDPOINT A DRY RUN STILL NAMES is the credential service's, at the
			// path the spawn would publish, and it must not grow into a prediction of the
			// rest: with no handles to read paths off, predicting the others means a second
			// copy of startLoopholesMatching's selection, which is exactly the drift that
			// lets a report and a lifecycle disagree. It is named because the plan is read
			// to check the ACL grant it gets (macosuser.EndpointGrantCommands) — the one
			// thing a dry run is the right instrument for here.
			//
			// THE DIR IN THAT PATH IS A PLACEHOLDER. A live session publishes into a dir of its
			// own that its spawn creates (servicessession.go), so a plan render, which creates
			// nothing, cannot know its name; servicesSessionPlanDir names its shape.
			o.notePackLoopholesInert(rt, staged.packs, cfg)
			for _, plan := range o.launchServices {
				o.pr(o.Stderr).print(fmt.Sprintf("Would start the %q service (pack %q) on %v for "+
					"this launch, outside the sandbox, until the command exits.", plan.Service,
					plan.Pack, o.servicePointedAt(plan, channel)))
			}
			for _, plan := range doorways {
				o.pr(o.Stderr).print(fmt.Sprintf("Would open the %q doorway (pack %q) on %v for "+
					"this launch, outside the sandbox, until the command exits: %s", plan.Service,
					plan.Pack, plan.Addresses(), strings.Join(plan.Cmd, " ")))
			}
			if openAIAuthLoopholeActive(cfg) {
				launchEnv.Set(hostServiceEnvVar(openAIAuthBrokerName),
					filepath.Join(servicesSessionPlanDir(cname, o.IsMacOS),
						openAIAuthBrokerName+paths.ServiceEndpointExt))
			}
		} else {
			// THE SESSION'S OWN DIR, created by the spawn and removed by this teardown alone
			// (servicessession.go). Two sessions of one workspace used to share the dir the
			// workspace's cname selects, and this deferred teardown, which takes no container
			// guard, removed it under the other session (OQ-HD10's second run, measured).
			handles := o.startLoopholesDisclosed(cname, rt, cfg, staged.packs, jailDaemons)
			defer o.endServicesSession(handles)
			// THE CREDENTIAL VIEW, opt-in until a Mac measures it (CL-D11): the
			// workspace's view registered and written now that the broker singleton is up, and
			// the resolved switch handed to the bootstrap, which then does not link the shared
			// file.
			o.registerClaudeCredentialView(rt, cname, cfg)
			if o.claudeCredentialView(rt, cfg) {
				launchEnv.Set(claudeview.SwitchEnv, claudeview.ResolvedValue(true))
			}
			for _, h := range handles {
				// THE HOST PATH, never the jail path: there is no jail filesystem here, so
				// the file the daemon published IS the file the sandbox opens.
				// insertHostServiceEnv's container twin passes h.jailPath for the mirror
				// reason — its consumer reads the bind destination.
				launchEnv.Set(hostServiceLaunchEnvVar(h), h.hostPath)
			}
			// THE CREDENTIAL SERVICE IS STILL FAIL-CLOSED, and it is deliberately the only
			// one: a launch whose OpenAI loophole is active and whose broker did not start
			// hands the agent a subscription it cannot refresh, silently. Every other
			// service degrades to "the jail cannot reach it", which startLoopholesMatching
			// already warns about by name and which no launch of this backend is refused
			// for — this arm emits no reachability disposition at all (loopholesruntime.go).
			if openAIAuthLoopholeActive(cfg) && !startedLoophole(handles, openAIAuthBrokerName) {
				o.pr(o.Stderr).print("[bold red]OpenAI credential service did not start; refusing the macos-user launch.[/bold red]")
				return 1
			}
			// THE DOORWAYS (macosuserdoorways.go), once the host services they forward to are up
			// and their endpoint files are on launchEnv, and stopped when the command returns. One
			// that does not start refuses the launch before the command runs.
			stopDoorways, err := o.startMacosUserDoorways(doorways, launchEnv)
			if err != nil {
				o.pr(o.Stderr).printf("[bold red]Refusing the macos-user launch: %s[/bold red]", err.Error())
				return 1
			}
			defer stopDoorways()
			// THE LAUNCH-OWNED SERVICES (macosuserservices.go): the host half of every pack
			// service a profiled agent's pairing needs, started after the credential service
			// it may ask for a view, and stopped when the sandboxed command exits. One that does
			// not start refuses the launch before the command runs.
			stopServices, err := o.startMacosUserServices(channel)
			if err != nil {
				o.pr(o.Stderr).printf("[bold red]Refusing the macos-user launch: %s[/bold red]", err.Error())
				return 1
			}
			defer stopServices()
		}
		// AND THE OTHER HALF OF THAT LIFECYCLE, WHICH THIS BACKEND NOW HAS: the guest's
		// supervisor starts the daemons it runs (MacosUserRun's last argument), and the ones
		// it declines are said once per launch, one line per daemon with its reason, from the
		// payload Run composed above the dispatch (jaildaemondecline.go).
		//
		// BELOW the block above and outside both its branches: a decline is not a spawn, so
		// a --dry-run states it too — and stating it once here is what keeps the live path
		// and the plan render from needing two printers that could disagree.
		o.noteMacosUserJailDaemonDeclines(declinedDaemons)
		o.noteRefusedDoorways(declinedDaemons)
		o.noteUnstartedProfileDaemons()
		o.noteShadowedServices()
		// THE OTHER TIER COLLAPSE — #39's mirror image — USED TO BE WARNED ABOUT HERE, and
		// is fixed rather than reported: the bootstrap now symlinks every scope:workspace
		// state dir into <workspace>/.yolo/home, the sidecar the container backends bind
		// from (entrypoint.InstallDarwinHomeLayout, docs/design/macos-user-home-tiers.md).
		// What the warning said was that the Seatbelt profile enforces a boundary the home
		// then leaked — a sibling workspace's transcripts unreadable under /Users and
		// readable at ~/.claude/projects/<other>/ — which the layout closes with no profile
		// change, because the sidecar is under the workspace the profile already isolates.
		//
		// ⚠ The claim that stood here until then, "the single home IS the shared-credentials
		// mechanism", is RETRACTED (§3). The mechanism is the `shared_credentials` HOOK,
		// which runs here unchanged; colocation only ever supplied the backing of the dir
		// the pack declared at scope:machine. That directory has not moved.
		//
		// AND THE CONTENT NOTE THAT STOOD HERE IS RETIRED TOO (G14): skills and briefings
		// are still copied rather than mounted, and the Seatbelt profile now write-protects
		// the copy, so "the agent can edit its own skills" is no longer true to say
		// (loopholeinert.go records why, where the function was).
		// THE PLATFORM KEYS AND THE PORT KEYS, on this arm for the same structural
		// reason as everything above: their only other printers live inside
		// assembleRunCmd (deviceArgs, kvmArgs, the GPU line) and hostForwardPorts,
		// every one of them below the return a few lines down. So `devices`, `gpu`,
		// `kvm`, `network.ports` and `network.forward_host_ports` were accepted,
		// validated, and then not mentioned by anything — the DP-B4 and DP-B3 rows of
		// docs/design/declaration-parity.md, fixed by DP-L10 and DP-L2's stderr half.
		//
		// HERE RATHER THAN ABOVE THE DISPATCH, which is where the catalog's cells
		// describe the call site: hoisted, they would double-warn on a macOS podman or
		// Apple Container launch, whose own warnings still fire from the assembler.
		// The keys that are wrong on THIS backend are wrong for a reason no other
		// backend shares, so the printer is this backend's.
		o.noteMacosUserPlatformGaps(cfg)
		o.noteMacosUserPortKeys(cfg)
		// A FORK DELIVERS NO PROGRAM ON THIS BACKEND, and says so (FP-D3; forkbuild.go): the build
		// trigger sits below this arm's return, and no macos-user launch can read the capture
		// store yet (hand-off H4).
		o.noteMacosUserForks()
		// YOLO_STORE_PACKAGES's only consumer (planStorePackages) is below this arm's
		// return, so on this backend the dial vanished without a line. A NOTICE, not a
		// refusal — planStorePackages' own ruling for an ineligible launch — and here the
		// dial's outcome is already this backend's only mode: no image, `packages:` from
		// the nix store.
		if envTruthy(o.Getenv(StorePackagesOptInEnv)) {
			o.pr(o.Stderr).printf("[bold yellow]%s=1 ignored:[/bold yellow] the macos-user "+
				"backend has no image, and its `packages:` already come from the nix store.",
				StorePackagesOptInEnv)
		}
		// WHERE THE PROFILE SELECTIONS LANDED, on this arm too. Until the channel hoist
		// this line had no honest form here — the launch line prints what a launch
		// DELIVERS (providers.md#pv-oq-10: never a verb that overclaims), and this backend delivered
		// nothing. It now layers the whole channel into its plan env, so the line
		// describes a delivery again. Printed beside the three notes above rather than
		// beside the container's banner: this is the block that answers "what will this
		// launch do for you", and it is the only stderr this arm writes before the
		// backend takes the terminal.
		o.noteUseProfiles(channel, staged.packs, nil)
		// THE SELECTED-PACK CREDENTIAL PRE-FLIGHT, on this arm too. It used to live only
		// in runContainer, below the return above, so a native launch with a provider
		// pack and no key started a sandbox that failed its first API call and said
		// nothing about why — the §6.1 symptom, unrefused on exactly the backend that
		// composes no env of its own.
		//
		// This arm rather than above the dispatch, deliberately, and for the reason the
		// config-change approval above gives: the container arm gates its own arm
		// (the fresh path directly; the attach path inside deliverChannelOnAttach,
		// which delivers the channel and checks it there). On THIS backend every
		// invocation is a fresh sandbox, so the arm's own call site is where the two
		// backends agree. The channel is the same value both arms check.
		if lines := o.checkEnvOverrides(cfg, rt, staged.packs, channel, nil); len(lines) > 0 {
			o.printProviderRefusal(lines)
			return 1
		}
		o.notePlatformSwitchConflicts(staged.packs, channel)
		if lines, refuse := o.checkProviderCredentials(cfg, staged.packs, channel, nil); len(lines) > 0 {
			o.printProviderRefusal(lines)
			if refuse {
				return 1
			}
		}
		// The channel crosses in launch-env form — the pack env fold, the launched agent's
		// shape vars, the wire tables and the gate-narrowed env_sources last (launchEnv).
		// The backend layers it into its plan env and relays the two wire tables to its
		// bootstrap, so the native derives read the same provider table a container
		// jail's do.
		//
		// The staged tree crosses as a PATH, not as the loaded declarations: the native
		// bootstrap re-reads the manifests itself (LoadJailPacks), exactly as the
		// container entrypoint does off its /ctx/packs mount, so the two backends render
		// from the same input in the same way.
		// CONTENT, on this arm too — the third and last of the B-0 omissions, after
		// pack staging and launch flags. refreshJailBriefings composes the skills tree
		// and every pack-declared briefing into a per-workspace staging dir; the
		// container path delivers that by MOUNTING it, which this backend cannot do, so
		// until today it composed nothing and the agent started with no AGENTS.md, no
		// CLAUDE.md and no skills — including the built-in suite — while the
		// blocked-tool shims WERE generated, so `grep -r` exited 127 with nothing
		// explaining it.
		//
		// Called on EVERY entry, which is what the container path does too: it runs
		// above the attach branch precisely so a re-entry re-renders whatever the config
		// now says. This backend has no attach at all, so every invocation is that case.
		//
		// The staged tree is then laid out BY DESTINATION (buildMacosHomeOverlay) and
		// delivered as one copy over the sandbox home. See that file for why a tree
		// rather than a manifest.
		//
		// ⚠ THE DESTINATION HOME IS SHARED, and that is this step's known defect rather
		// than an oversight: SandboxHome() is the constant /Users/_yolojail, so a second
		// workspace launching concurrently overwrites the first's briefings while its
		// agent is mid-session — and a briefing is per-project prose, so that agent
		// would go on reading another project's description. The hazard is not new (the
		// bootstrap already rewrites agent configs and shims on every launch) but this
		// widens it from workspace-independent files to per-project ones. The fix is a
		// per-workspace home, which is a design change and has its own doc:
		// docs/design/macos-user-home-tiers.md.
		// NOT gated on --dry-run, and that is deliberate: the plan's job is to describe
		// the launch, and a plan that omitted the content staging would show a launch
		// nobody runs. Composing writes only into the host-side staging dir, which pack
		// staging above already does on a dry-run for the same reason; RunMacosUser
		// still returns before executing a single staged command.
		//
		// THE WORKSPACE LAUNCH LOCK OPENS HERE on this backend, and not before staging, where
		// the staging-race fix had put it. The skills and briefing staging written below, and
		// the home-overlay and context trees built from it, are per-WORKSPACE directories
		// under AGENTS_DIR/<cname>, rebuilt by each launch and copied for the sandbox by the
		// orchestrator's stage, so two launches of the workspace must not interleave between
		// this write and that copy. The orchestrator is handed this hold
		// (AcquireWorkspaceLockFor) and releases it before the agent. What no longer needs it
		// is everything above: the pack tree is this launch's own (packtree.go), so its
		// staging and the host daemons started from it above cannot race another launch's.
		o.holdLaunchLock(cname)
		// THE DURABLE DIR, on this backend too: every invocation here is a fresh launch. It is
		// the workspace's own `.yolo/durable` at its real path, inside the Seatbelt write set
		// with the rest of the workspace, and it reaches the sandbox through the launch env
		// the plan layers for the agent (and relays to the bootstrap, whose launch line
		// reports it) only when it was made (durabledir.go).
		if d := o.ensureDurableDir(rt, cfg); d.Path != "" {
			launchEnv.Set(durable.EnvVar, d.Path)
		}
		// CLAUDE'S CREDENTIAL STORE, on this backend too (CL-D22, claudesecurestorage.go): the
		// account home's machine-scope directory, which the bootstrap lays as a real directory
		// (entrypoint.DeriveDarwinHomeLayout) inside the Seatbelt profile's home write grant.
		// Through the launch env, so the session env file carries it to every process of the
		// session, as the container argv carries it to every exec. A --dry-run plan shows it.
		if dir := o.claudeSecureStorageDir(rt, cfg, staged.packs, macosuser.SandboxHome()); dir != "" {
			launchEnv.Set(claudeview.SecureStorageEnv, dir)
		}
		staging, err := o.refreshJailBriefings(cname, cfg, rt, staged,
			appliedIOPriority(rt, o.IsMacOS, cfgMap(cfg, "resources")))
		if err != nil {
			o.pr(o.Stderr).printf("[bold red]%s[/bold red]", err.Error())
			return 1
		}
		homeOverlay, err = buildMacosHomeOverlay(staging, staged.packs, func(line string) {
			o.pr(o.Stdout).print("[yellow]" + line + "[/yellow]")
		})
		if err != nil {
			o.pr(o.Stderr).printf("[bold red]%s[/bold red]", err.Error())
			return 1
		}
		// THE HOST BYTES, on this arm and only on this arm (DP-L1, macosctxtree.go).
		// Composed here for the same three reasons the overlay above is: the container
		// path delivers this content by MOUNTING it and returns below, the composition
		// needs the resolved pack set this function already holds, and a --dry-run must
		// describe the launch a user would really get rather than a smaller one.
		//
		// FATAL on failure, unlike the overlay's own sources: a `reads-host` grant whose
		// bytes exist and could not be copied would compose the agent a settings file
		// that looks like the human's and is not, which is the exact failure OQ-CO10
		// made the jail's read fail closed over. An ABSENT source is not a failure and
		// does not reach here.
		ctxDelivery, err := o.buildMacosCtxTree(staging, staged.packs, cfg)
		if err != nil {
			o.pr(o.Stderr).printf("[bold red]%s[/bold red]", err.Error())
			return 1
		}
		// AND THE DISCLOSURE THAT MUST TRAVEL WITH THEM. Until DP-L1 nothing crossed
		// here, so nothing had to be disclosed; now a pack reads the human's home on
		// this backend exactly as it does on every other one, and the banner is the
		// whole trust boundary (packhostgrants.go: "the boundary today is DISCLOSURE,
		// not consent"). The container path prints this inside runContainer, below the
		// return above — the same B-0 shape as pack staging, launch flags and the
		// channel, and the same fix: the arm prints its own.
		//
		// THE `mount` CLAIMS ARE BACK (DP-B2's banner half): this backend delivers a pack
		// `mount` now, by link, so the banner's host READ is true here as it is on a container —
		// and a grant it could not deliver refused the launch above (planMacosUserCtxMounts),
		// so no banner line on this arm describes a read that does not happen beyond what the
		// container arm's does for an absent source.
		o.notePackHostAccess(staged.packs, channel)
		o.noteMacosUserHostByteGaps(ctxDelivery)
		// THE CONTEXT MOUNTS cross inside the host context, and each read-write one is
		// disclosed at the same point (§2.4), so the backend is never handed one unsaid.
		ctxDelivery.ctx.Links = ctxLinks
		o.noteMacosUserRWMounts(cname, ctxLinks)
		// EVERY PROFILED AGENT'S OWN ENV FILE, on this backend too (providers.md
		// OQ-CN9, ruled 2026-09-28): the container vehicle's writer, into the sidecar directory
		// the bootstrap's home layout links the sandbox's ~/.config to, so an agent started from
		// a bare `yolo`'s login shell sources its profile's values the way its container twin
		// does. Not on a dry run, which starts no agent to read them.
		if !o.DryRun {
			writeMacosUserAgentEnvFiles(paths.WorkspaceHomeState(o.Workspace), channel)
		}
		// The launch's fate is known: this backend runs it from here (launchrecord.go). A dry
		// run starts nothing, and Run's deferred record says so.
		if !o.DryRun {
			o.recordLaunchOutcome(launchStarted, -1)
		}
		// THE GUEST'S PORTS GO FREE HERE, and no earlier (servedaddresses.go, NC-D69): the
		// sandbox's supervisor binds the ports this launch reserved for the daemons it runs,
		// and every listener of this launch's own (the host services' fronts, the doorways and
		// launch-owned services, which were handed theirs) is bound by now.
		o.releaseReservedPorts()
		// THE HERDR PANE, registered as late as this arm can (herdragent.go): it has no signal
		// arm, so a registration made before its config prompt outlived a Ctrl-C there.
		o.registerHerdrAgent(staged.packs, injectedArgs)
		// Composed LAST, after every endpoint variable has landed on launchEnv (the live
		// path's handles, or a dry run's placeholder), since the daemons dial those files.
		return o.MacosUserRun(cfg, o.Workspace, config.SelectedAgents(cfg), agentArgv,
			repoRoot, staged.root, homeOverlay, ctxDelivery.ctx, o.DryRun,
			launchEnv, packload.BlockedTools(staged.packs),
			channel.guestJailDaemons(guestDaemons, launchEnv))
	}
	// AUTO-CAPTURE, the last host-side act before the container arm starts anything
	// (OQ-PD18, install-capture.md slice 7). Every selected pack's `via: "installer"`
	// program that this machine has never recorded is captured now, in a throwaway jail
	// of its own, so that this workspace and every later one materialize the install
	// instead of downloading it.
	//
	// HERE, and the placement carries three decisions:
	//
	//   - BELOW the macos-user return, which is what makes it container-only. See
	//     autocapture.go for why that backend is excluded — nothing there emits
	//     CapturesDirEnv (hand-off H4; slice 6's relocation rewrite, the second reason this
	//     said, landed as hand-off H2) — and why `yolo capture` stays available on it as an
	//     explicit act.
	//   - BELOW stageRunPacks, because the pack set is the input: the trigger asks the
	//     SELECTED packs what they install, through HonoredInstalls, so a pack the
	//     config dropped stops being captured and a fetched pack's refused installer
	//     never runs.
	//   - ABOVE runContainer, so the capture jail's own launch is the thing that builds
	//     and loads the image, and this launch reuses it. It is BLOCKING and it says so
	//     while it works: on a fresh machine the first launch grows by one installer
	//     download per uncaptured program (~205 MiB for claude). A detached child would
	//     keep the launch fast and is the obvious follow-up, but it buys invisible
	//     failures and a host-process lifecycle yolo does not have — a second step, on
	//     evidence that the wait hurts.
	//
	// It cannot fail this launch. Every outcome inside is a warning (autoCapture in
	// internal/cli), because nobody asked for this work and a machine that cannot do it
	// must still get its jail.
	//
	// Spanned because it is the pipeline's biggest HIDDEN cost: a fresh machine's
	// first launch grows by one installer download per uncaptured program, and
	// the very first nested --timing run measured 109 of its 125 seconds in
	// this call — every bit of it between two spans, pointing at nothing.
	sp := o.Perf.Span("launch.auto_capture")
	o.autoCaptureInstallerPrograms(staged.packs)
	sp.End()
	// THE FORK BUILDS are NOT in this slot, though they share its reasons (forkbuild.go): they run
	// in runContainer's fresh-launch path, below the attach decision, because a jail bakes its
	// fork decisions at boot and an attach could not deliver a build it waited for (FP-D14).
	return o.runContainer(cfg, rt, repoRoot, cname, staged, injectedArgs, channel, jailDaemons)
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
// place a DESELECTION is observed at all
// (docs/reference/loophole-system.md#retirement-what-happens-when-a-pack-goes-away, and see
// loopholeretire.go for why `yolo host apply` and the host-render archive sweep cannot see it).
// Never fatal: a bookkeeping failure over the host state dir must not cost the user a jail.
//
// It runs on EVERY invocation, attach included: config says the pack is gone, and a state dir
// holding a CA private key should not wait for the next fresh launch to be retired.
//
// NO LOCK IS TAKEN HERE ANY MORE. Staging writes a NEW pack tree of this launch's own
// (packtree.go), which no other launch knows the name of, so two launches of one workspace
// stage at once without touching each other's trees. The workspace launch lock used to open
// here, when staging rewrote one shared tree that the other launch was still reading; it now
// opens where each backend first touches what the workspace's launches still share
// (holdLaunchLock lists where).
//
// The tree is recorded as this launch's own (Options.packTree), so Run's deferred
// discardUnheldPackTree removes it at any return that did not hand it to a started container.
func (o *Options) stageRunPacks(cname string) (stagedPacks, bool) {
	root, packs, briefings, err := o.stagePacks(cname)
	if err != nil {
		o.pr(o.Stdout).printf("[bold red]%s[/bold red]", err.Error())
		return stagedPacks{}, false
	}
	o.packTree = root
	// NOT FOR A NARROWED SELECTION (a fork build, seal.go): the retirement pass reads a pack this
	// launch does not carry as one that LEFT `packs`, so a build carrying two packs would archive
	// the loophole state of every other pack the user selected.
	if o.OnlyPacks == nil {
		o.recordAndRetirePackLoopholes(packs)
	}
	return stagedPacks{root: root, packs: packs, briefings: briefings}, true
}

// discardUnheldPackTree removes this launch's own pack tree unless a started container holds it.
// Run defers it once staging has produced the tree: on a refusal, an attach (which reads the
// running jail's tree and needs its own staging only to compare), a macos-user launch (whose
// sandbox copied the tree at its bootstrap and whose host daemons, which run from it, stop before
// this runs) and a --dry-run, no container ever holds it. A fresh container launch marks the tree
// held just before the container starts; from then on it goes only once the runtime answers that
// the container is gone (forgetGoneContainer).
func (o *Options) discardUnheldPackTree(cname string) {
	if o.packTreeHeld {
		return
	}
	discardPackTree(cname, o.packTree)
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
// same way (docs/reference/loophole-system.md#the-crossing-enumeration and
// docs/reference/loophole-system.md#the-per-launch-disclosure). The classification is now exhaustive over
// packdecl.KnownKinds() by test.
//
// Host EXECUTION does NOT print here. It prints at the spawn boundary, BEFORE
// startLoopholes — see startLoopholesDisclosed. For a read, printing at the banner is
// cosmetic; for an exec it would be a notification that something already happened.
//
// channel is the launch's composed channel, whose served set (packChannel.served) is where the
// pack env pointers were composed: a pointer's `{listen}` prints as the served address the
// jail receives, as {state} prints resolved, rather than as the template (NC-D46). nil resolves
// nothing, which only a hand-built test passes.
//
// EVERY BACKEND PRINTS THE SAME CLAIMS. macos-user left its pack `mount` claims out while it
// delivered no pack `mount` (DP-B2: a disclosure of a read that does not happen is worse than
// silence); it delivers them by link since docs/design/context-mounts.md §4 step 4, and refuses
// the launch where it cannot, so the exception went with the gap.
func (o *Options) notePackHostAccess(loadedPacks []*packload.Pack, channel *packChannel) {
	served := packload.NothingServed()
	if channel != nil {
		served = channel.served
	}
	lines := disclosedClaimsServed(loadedPacks, disclosureRead, served)
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
// (docs/reference/providers.md#what-the-launch-checks-and-prints): one line per DISTINCT name in
// the channel's table, naming the packs that declare it and, for each agent keyed to it, the
// provider it resolved to and how that agent reaches it in this jail, then a warning for each
// agent the selection reaches nothing for or delivers no credential to. It reads the channel the
// jail receives (its table is the merge the env block emits, effectiveUseProfiles), so the line
// cannot describe a composition the jail did not get.
//
// "Reaches the agent" is what crosses for it: the channel's delivery to that agent
// (CredentialScope.DeliveredTo), and on a container the argv's `-e` pairs (argvPairs, nil
// elsewhere), never the environment yolo was launched from, which no backend forwards.
//
// The line is packload.ProfileDisclosures', which `yolo host` prints too (notch-convergence
// item 13): what each half means, and why the verb is never "honored", is stated there. A
// disclosure, so no quiet switch (OQ-RO3).
//
// AN ACTIVE SET (docs/design/active-provider-sets.md) adds to it: every entry's name gets its
// line, each set of more than one is named in order with the entry a session starts on
// (packload.ActiveSetLines), and a bare list — the config key's or a -p's — narrowed for agents
// whose packs declare no provider_sets says which agents ignore which entries (the channel's
// bareNote, OQ-AP3's one line).
func (o *Options) noteUseProfiles(channel *packChannel, loadedPacks []*packload.Pack,
	argvPairs map[string]string) {
	if channel == nil {
		return
	}
	out := o.pr(o.Stderr)
	sets := packload.ProfileSets(channel.profiles)
	for _, d := range packload.ProfileDisclosures(packload.ProfileDisclosureInput{
		Table:     packload.ProfileTable(channel.profiles),
		Sets:      sets,
		Packs:     loadedPacks,
		Resolved:  channel.resolvedProfiles,
		Providers: channel.providers,
		Scope:     channel.scope,
		Reaches: func(agent, name string) bool {
			if v, found := argvPairs[name]; found && v != "" {
				return true
			}
			_, ok := channel.scope.DeliveredTo(agent, name)
			return ok
		},
	}) {
		out.print("[dim]" + d.Head() + "[/dim] " + d.Detail())
		for _, w := range d.Warnings() {
			out.print("[yellow]" + w + "[/yellow]")
		}
	}
	for _, line := range packload.ActiveSetLines(sets) {
		out.print("[dim]" + line + "[/dim]")
	}
	if channel.bareNote != "" {
		out.print("[yellow]" + channel.bareNote + "[/yellow]")
	}
}

// ensureStorage wraps storage.EnsureGlobalStorage, wiring the v2 layout
// migration (audit 2026-07-18 §B#2: passing nil left the dangling-mise-symlink
// heal + layout-version stamp as dead code that never ran under the gate).
// canReclaim returns false — the conservative fail-safe (DEFER the heal
// when it can't confirm no live jail holds the store, leaving the marker
// unstamped to retry); the full live-container probe is the run-slice's concern,
// and declining is always safe. insideJail short-circuits (never scans /mise).
//
// It is a METHOD, and was a package-level func until the launch needed a stream:
// warnf wrote the PROCESS os.Stderr, so the migration's own messages bypassed the
// launch-log tee that AGENTS.md says everything printed goes through (attachLaunchLog
// sets o.Stderr above the call), and no test could capture them. Both facts are the
// same missing receiver.
//
// It returns EnsureGlobalStorage's error and nothing else. The legacy base-home refusal
// that used to follow it (noteLegacyBaseHome, and its YOLO_ALLOW_LEGACY_BASE_HOME hatch)
// is gone with the shared mount it guarded: no jail mounts <state>/home at /home/agent any
// more and no seed copies from it, so its legacy bytes are unmounted, unread, and nothing
// a launch has cause to refuse over (the maintainer's OQ-BH13 ruling,
// docs/design/base-home-legacy-state.md#10-decision-ledger). `yolo check` still reports
// them, as optional cleanup.
func (o *Options) ensureStorage() error {
	return storage.EnsureGlobalStorage(func() {
		insideJail := o.Getenv("YOLO_VERSION") != ""
		storage.MigrateStorageLayout(insideJail, func() bool { return false }, func(msg string) {
			fmt.Fprintln(o.Stderr, msg)
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
// and the credential pre-flight answers against it. jailDaemons is the same shape of
// value for the same reason — one composed YOLO_JAIL_DAEMONS payload per launch, which
// assembly serializes and the native arm declines (packservices.go's jailDaemonsFor).
func (o *Options) runContainer(cfg *jsonx.OrderedMap, rt, repoRoot, cname string, staged stagedPacks,
	injectedArgs []string, channel *packChannel, jailDaemons []loopholes.JailDaemonSpec) int {
	out := o.pr(o.Stdout)
	// Staged above the dispatch (see Run): this path consumes the result rather than
	// producing it. packStaging is this launch's own pack tree, the one /ctx/packs binds on a
	// fresh launch; loadedPacks is what the mount assembler reads declarations from. An attach
	// binds nothing and reads the running jail's tree instead (attachExisting).
	packStaging, loadedPacks := staged.root, staged.packs

	// Command construction (needed for both exec and run paths).
	//
	// The flags are already in injectedArgs: Run resolves them above the backend dispatch,
	// from the staged set and the launch's effective profile table, and this arm consumes
	// that one result rather than repeating the fold (see the note at the injection site).
	// An ATTACH into a jail whose packs differ from the configured ones injects its own
	// (runningJailPackView), since a running jail keeps the packs it booted with.
	fullCommand := append([]string{}, injectedArgs...)
	targetCmd := "bash"
	if len(fullCommand) > 0 {
		targetCmd = shquoteJoin(fullCommand)
	}

	// THE WORKSPACE LAUNCH LOCK, from here on both paths (holdLaunchLock): the attach decision,
	// the skills and briefing refresh into the workspace's shared staging, the attach's contract
	// gate, and on a fresh launch everything up to its container running. Not before staging any
	// more: the pack tree is this launch's own (packtree.go), so there is nothing shared to
	// protect until here. Before the orphan sweep, so a reaped orphan of THIS workspace leaves
	// its host-services dir to this relaunch (stopLoopholes' guard).
	// THE ARRIVAL, which may have to wait for the previous jail's keeper and then decide again
	// (docs/design/jail-lifetime-last-session-wins.md JL-D28, JL-D12): a keeper still ending this
	// workspace's jail is waited for with the launch lock RELEASED, since its teardown's guards take
	// that lock non-blocking and would otherwise back off and leak what they clean, and the
	// decision is then made again from the top, because another launch may have started a jail
	// meanwhile.
	for {
		o.holdLaunchLock(cname)

		// Sweep jails orphaned by an uncatchable kill before the attach decision.
		sp := o.Perf.Span("launch.reap_orphaned_jails")
		o.reapOrphanedJails(rt)
		sp.End()

		existingCID := ""
		if !o.NeverAttach {
			// TRI-STATE (PR-D8 of docs/design/podman-reboot-readiness.md): a runtime that could
			// not say whether this workspace's jail is running refuses the launch, rather than
			// starting a fresh one beside a jail that may be up.
			cid, known := o.probeRunningContainer(cname, rt, attachProbeTimeout)
			if !known {
				out.printf("[bold red]Refusing to launch: could not ask %s whether this workspace's "+
					"jail (%s) is already running.[/bold red]", rt, cname)
				out.printf("[dim]Launching fresh could start a second jail beside a running one. Run "+
					"`%s ps` to diagnose, then launch again.[/dim]", rt)
				return 1
			}
			existingCID = cid
		}

		if existingCID != "" {
			// A JAIL WHOSE KEEPER DIED is not entered (JL-D13, as OQ-JL7 ruled): its sessions run on
			// without host services, and `yolo stop` then a launch is the remedy.
			if o.refuseUnkeptJail(cname, rt) {
				return 1
			}
			// The attach's window over the workspace's shared staging ends inside attachExisting,
			// once its contract gate has passed (contracttags.go) and it has refreshed the skills
			// and briefing staging from the running jail's own pack tree; the exec reads none of
			// it back. Held across the attach session, the lock would make every other terminal in
			// the workspace wait for this one to exit. Held through the gate, because a gate that
			// restarts the jail continues below as a fresh launch, and the stopped jail's teardown
			// leaves its host-services dir alone only for a launch holding the lock.
			//
			// A launch that WAITED for the lock found this jail because the launch it waited for
			// started it, so it gets the raced banner, as it did when it waited further down.
			if rc, restarted := o.attachExisting(cname, rt, targetCmd, cfg, staged, channel, o.launchLockWaited, o.releaseLaunchLock); !restarted {
				return rc
			}
			// Restarted: the jail this entry could not use is stopped and gone, and this launch
			// is now a fresh one — the path below, lock still held. Or its count found the jail's
			// keeper ending it, which is waited for below.
		}
		// EVERY FRESH LAUNCH WAITS FOR AN OLD KEEPER (JL-D28 (1)): one keeper per container name at
		// every instant. The attach-skew restart has waited already, holding the lock (JL-D26).
		if probeKeeper(cname) == keeperGone {
			break
		}
		if !o.awaitPreviousKeeper(cname) {
			return 1
		}
	}

	// --- Fresh launch: config-change approval ---
	wsCfg, _ := config.LoadWorkspaceConfig(o.Workspace, false, func(string) {})
	if !o.checkConfigChanges(wsCfg, cfg, rt) {
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
	//
	// NORMALLY ALREADY HELD: this function took it above the attach decision (holdLaunchLock),
	// and this path uses that hold rather than taking the file a second time — which, in one
	// process, would wait on itself. The acquisition below is for a launch whose hold could not
	// open the lock file.
	lock := o.launchLock
	if lock == nil || lock.isClosed() {
		lockDir := filepath.Join(paths.GlobalStorage(), "locks")
		_ = os.MkdirAll(lockDir, 0o755)
		lockSpan := o.Perf.Span("launch.acquire_workspace_lock")
		var lerr error
		lock, lerr = acquireWorkspaceLock(filepath.Join(lockDir, cname+".lock"), o.Workspace,
			lockNotices{
				warn:    func(msg string) { out.printf("[dim]Warning: %s[/dim]", msg) },
				waiting: func(msg string) { out.printf("[bold cyan]%s[/bold cyan]", msg) },
			})
		lockSpan.End()
		if lerr != nil {
			out.printf("[bold red]%s[/bold red]", lerr.Error())
			return 1
		}
	}

	// Re-check after acquiring the lock — another process may have won.
	if !o.NeverAttach {
		if raced := o.findRunningContainer(cname, rt); raced != "" {
			if rc, restarted := o.attachExisting(cname, rt, targetCmd, cfg, staged, channel, true, lock.Close); !restarted {
				return rc
			}
		}
	}

	// Remove any stopped container left from an unclean shutdown.
	//
	// ⚠ A REMOVAL THAT FAILS MEANS THE CONTAINER IS NOT STALE, and ignoring that cost the
	// macOS nightly TestConcurrentLaunchesInOneWorkspace repeatedly (shard 9 of runs
	// 34995936829 and 35033142943). The return value was discarded here, so the launch went
	// on to CREATE and the runtime answered:
	//
	//	Error: creating container storage: the container name "yolo-002-…" is already in
	//	use by e1e5de83…
	//
	// rc 125, and the second launch never attached.
	//
	// THE WINDOW IS BETWEEN `created` AND `running`. The post-lock re-check above asks
	// findRunningContainer, which is deliberately narrow; a container the other launch has
	// created but not yet started is invisible to it, visible to findExistingContainer, and
	// refused by `rm` precisely because it is alive. Locally the first container is running
	// long before the second launch looks, which is why this passes on an unloaded machine
	// and fails on a loaded runner — the window is real, just usually too small to hit.
	//
	// So a failed removal is treated as what it is — evidence of a live container — and the
	// launch waits for it to become attachable rather than racing it to a name collision.
	if stale := o.findExistingContainer(cname, rt); stale != "" {
		o.pr(o.Stderr).printf("Removing stale container %s...", cname)
		if !o.removeStaleContainer(cname, rt) && !o.NeverAttach {
			if live := o.waitForRunningContainer(cname, rt); live != "" {
				if rc, restarted := o.attachExisting(cname, rt, targetCmd, cfg, staged, channel, true, lock.Close); !restarted {
					return rc
				}
			}
		}
	}

	// No container of this name will be attached to from here: this is a fresh launch. So the
	// shared staging tree a jail launched before per-launch pack trees bound can go, once the
	// runtime answers that no container of the name exists (packtree.go).
	o.retireLegacyPackStaging(cname, rt)

	// THE FORK BUILDS (forkbuild.go; OQ-FP4, eager at the notch's readiness act): every selected
	// fork this machine holds no build of at its pin is built now, in a sealed jail of its own, and
	// this jail is handed each fork's store key or the reason it has none. A hit builds nothing, and
	// no outcome fails this launch (§9). HERE, below every attach site, rather than beside
	// auto-capture above the dispatch (FP-D14): a running jail read its decisions once at boot, so
	// a build an attach waited for would reach no jail, and an auto-capture differs in exactly that
	// a running jail's launchers read the store lazily. Under the launch lock, as the image load
	// is: a second terminal in this workspace waits for this jail and then attaches to it.
	forkSpan := o.Perf.Span("launch.fork_builds")
	o.forkDelivered = o.forkDeliveriesFor(rt)
	forkSpan.End()

	// Refresh the per-jail skills + AGENTS/CLAUDE staging from this launch's own pack tree. An
	// attach refreshes from the running jail's tree instead, inside attachExisting, so this
	// runs only once no attach site has taken the launch.
	// THE DURABLE DIR, made here because this is a fresh launch and every attach site above
	// has declined: before the briefing that leads with it and the argv that exports it
	// (durabledir.go). A failure is one printed line, never a refusal.
	o.ensureDurableDir(rt, cfg)

	sp := o.Perf.Span("launch.refresh_jail_briefings")
	agentsPath, err := o.refreshJailBriefings(cname, cfg, rt, staged,
		appliedIOPriority(rt, o.IsMacOS, cfgMap(cfg, "resources")))
	sp.End()
	if err != nil {
		out.printf("[bold red]%s[/bold red]", err.Error())
		lock.Close()
		return 1
	}

	// Retire jail-made workspace venvs from the old shared-store model.
	o.retireJailMadeVenv(cfg)

	// yolo's OWN binaries, and the flake bundle beside them. They are BIND-MOUNTED
	// into the jail rather than baked into the image (jailprefix.go): that is what
	// keeps `goSrc` out of the image derivation, so a commit under cmd/ or
	// internal/ costs neither an image rebuild nor a `podman load`.
	//
	// Resolved BEFORE the image, not after, for two reasons. A live checkout has to
	// compile them, and finding that out after streaming a multi-gigabyte image
	// would put the cheap failure behind the expensive success. And this is the
	// mount whose absence means the container has no pid1 to exec — a launch that
	// cannot produce it must refuse before it starts making a container at all.
	sp = o.Perf.Span("launch.resolve_jail_prefix")
	jailPrefix, prefixOK := o.resolveJailPrefix(repoRoot, rt)
	sp.End()
	if !prefixOK {
		lock.Close()
		return 1
	}
	o.pr(o.Stderr).printf("[dim]Jail binaries: %s[/dim]", describeJailPrefix(jailPrefix))

	// C4/C5: settle where this launch's packages come from BEFORE the image build, since
	// the answer changes what is built. The store-mounted term is the assembler's own
	// predicate rather than a second reading of it, so a launch cannot promise store
	// delivery and then omit the mount (storepackages.go).
	storePkgs, storePkgsOK := o.planStorePackages(cfg, rt, repoRoot, o.hostNixMounted(rt))
	if storePkgsOK {
		// C5 rides the same plan: the extras profile is appended BEHIND the workspace's
		// own packages, so the farm's first-wins rule reproduces the precedence a baked
		// image already gives them.
		storePkgs, storePkgsOK = o.addImageExtras(storePkgs, repoRoot)
	}
	if !storePkgsOK {
		lock.Close()
		return 1
	}

	// Image build/load. The result carries the REF of the image it made ready —
	// content-addressed on the normal path (C2), the legacy :latest tag on a
	// degraded fallback that has no store path to hash. Everything downstream
	// that names an image reads it from assembleInput.imageRef below; nothing
	// re-derives it.
	//
	// This span replaces the old bare `timingStart` stopwatch (which began
	// here, mid-pipeline, and produced the single Total): the report's zero of
	// time is now collector construction at the top of Run, so the Total covers
	// the probes and staging this call used to exclude.
	sp = o.Perf.Span("launch.auto_load_image")
	loadedImage := o.autoLoadImage(cfg, rt, repoRoot, storePkgs)
	sp.End()
	if !loadedImage.OK {
		lock.Close()
		return 1
	}

	// THIS WORKSPACE'S CURRENT IMAGE, recorded before anything can reap
	// (OQ-LS3). It is the retention evidence that replaced `--keep-images`: the
	// reaper keeps the union of these pointers and whatever `podman ps` says is
	// running, so a launch that does not record one leaves its own image
	// unprotected the moment the jail stops. currentimage.go carries the window
	// that survives and what a failed write costs.
	o.recordCurrentImage(loadedImage, cname)

	// LEDGER C USED TO BE REAPED HERE, before the container started, and it
	// MOVED into the post-launch housekeeping slot (OQ-BF5, housekeeping.go).
	// The property that justified this placement — this launch's own image is
	// already in the load sentinel AddLoadedPath just wrote — holds in the slot
	// too, and holds MORE strongly there: by then the container exists, so the
	// reap's own `podman ps` guard sees it directly. What changes is that a
	// first pass over a backlog stops holding the launch: fourteen multi-GB
	// `rmi` in front of a jail start, on the one machine that had never run it.

	// ws_state overlay prep.
	sp = o.Perf.Span("launch.prepare_ws_state")
	wsState := o.prepareWsState(cfg, loadedPacks, rt)
	sp.End()

	// yolo-user-env.sh (frozen writer) and the per-agent env files, both from the ONE
	// credential gate's answer (deliverChannel, OQ-CN6): the shared file carries what every
	// process may see, each profiled agent's file what only it receives. The map is the
	// channel's hydration, not a second ResolveEnvSources pass: one walk, one set of
	// warnings, and the files cannot describe a channel the pre-flight below checked a
	// different copy of. These files are the channel's ONLY crossing (per-entry delivery,
	// the writer's doc): the argv carries none of it, so the container's frozen environment
	// holds no provider state for a later exec to inherit, and this same write is what an
	// attach performs to deliver a different profile into a running jail.
	userEnv := channel.userEnv
	deliverChannel(wsState, rt, channel)
	o.noteCredentialScope(channel)
	// What this launch's jail-daemon payload left out because no profile selects it (OQ-CN7
	// (b)). Here, on the fresh path, because only a fresh launch starts daemons: an attach's
	// selection starts none, and settles a daemon it needs and the jail lacks as skew instead.
	o.noteUnstartedProfileDaemons()
	// And what it set aside because a later pack declares the same service name (NC-D59).
	o.noteShadowedServices()

	// Broker singleton + relay: ensure BEFORE building the argv (the sockets-dir
	// mount + broker env are emitted by the assembler when the socket exists).
	//
	// GATED ON THE LOOPHOLE RECORD (docs/reference/loophole-system.md OQ-A11), which
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
	//
	// THE HOST-SERVICES DIR IS NOT CREATED HERE, and it used to be, on both arms of this
	// gate. startLoopholesMatching's first statement creates it, broker or no broker, and
	// that call precedes the container start, so the bind source still exists when podman
	// needs it. What the second creation here added was a leak: the refusals between this
	// line and the spawn (a host_files collision, the skeleton, the provider pre-flights…)
	// never reach stopLoopholes, so each refused launch left an empty
	// /tmp/yolo-host-services-<8hex> behind (TestARefusedFreshLaunchLeavesNoHostServicesDir,
	// and TestOnlyTheSpawnCreatesTheHostServicesDir keeps the spawn the only creator).
	//
	// ON THIS PATH THE SPAWN IS THE KEEPER'S (keeper.go's run, through startPlannedLoopholes),
	// and this process never creates the dir. The keeper starts at startKeeper, below the
	// reclaim prompt (maybeOfferReclaim), so Ctrl-C at that prompt finds no dir to leave
	// behind. Once the keeper has made it, the keeper's teardown removes it (stopLoopholes,
	// through teardownAfterExit from endJail, or from unwindUnstarted, under stopLoopholes'
	// container checks), and this launch dying before the jail is ready reaches that teardown
	// too: the keeper hears the lifeline close and ends the jail (beforeReady). Only a keeper
	// that dies without its teardown (SIGKILL, OOM) leaves the dir in place
	// (docs/reference/jail-home.md).
	socketsDir := hostServiceSocketsDir(cname, o.IsMacOS)
	// Not under the seal (seal.go): a fork build starts no host service.
	if rt != "container" && brokerLoopholeActive(cfg) && !o.Sealed {
		o.brokerEnsure()
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
	// A sealed build prunes nothing: its /mise is its own, and it has no reason to (seal.go).
	if !o.inJail() && !o.Sealed {
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
	//
	// UNDER THE SEAL there are none, and none of the rest of this block's host crossings either —
	// the host-CAS alias, host_files, the machine-scope pack dirs (seal.go): a fork build's
	// ~/.cache is its own workspace's, and it gets no host file of any kind.
	var relocations []config.CacheRelocation
	var relErr error
	if !o.Sealed {
		relocations, relErr = config.LoadCacheRelocations(func(msg string) {
			out.printf("[yellow]Warning: %s[/yellow]", msg)
		})
	}
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

	// L9's host-CAS alias (disk-levers-and-backfill.md OQ-BF10), decided and
	// provisioned in the same window and for the same two reasons as the
	// relocations above: a missing bind source kills the container, and a
	// destination whose mountpoint does not exist gets a root-owned one invented
	// for it.
	//
	// NO ERROR RETURN AND NO REFUSAL, which is the difference from the block
	// above. A relocation that silently does not take sends the user's 185 GiB
	// back onto the filesystem they moved it off, so it is fatal; an alias that
	// does not take leaves the jail pooling its own copy — the behaviour of every
	// yolo that shipped before this — so refusing a launch over it would trade a
	// working jail for a preference about where bytes live. Every decline is
	// disclosed instead (noteHostCASAlias, at the banner).
	var hostCAS []hostcas.Disposition
	if !o.Sealed {
		hostCAS = prepareHostCASAlias(o.planHostCASAlias(rt, relocations))
	}

	// User host_files (docs/reference/composed-file-permissions.md). Read with the same
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
	var hostFiles []config.HostFileEntry
	var hfErr error
	if !o.Sealed {
		hostFiles, hfErr = config.LoadHostFiles(cfg, func(msg string) {
			out.printf("[yellow]Warning: %s[/yellow]", msg)
		}, !o.inJail())
	}
	if hfErr != nil {
		out.printf("[yellow]Warning: host_files: %s — no host files staged[/yellow]", hfErr.Error())
		hostFiles = nil
	}
	// TWO WRITERS FOR ONE FILE IS FATAL, and this is the first point it is detectable: config
	// validation cannot resolve a CONFIGURED pack's surfaces (it would need the pack store), so
	// the reservation there covers embedded packs only. Here the packs are loaded.
	//
	// Fatal rather than a warning, unlike every other host_files failure above it, and the
	// asymmetry is the ruling (OQ-LM6, docs/research/local-model-endpoints.md): a missing SOURCE
	// degrades to the defaults layer, which is the feature working; two writers for one
	// destination is a config that cannot be satisfied, and picking a winner quietly is how a
	// working :ro-mounted file gets overwritten.
	if cols := config.SurfaceCollisions(hostFiles, packSurfacePaths(staged.packs), cfg, o.configSources); len(cols) > 0 {
		for _, c := range cols {
			out.printf("[bold red]%s[/bold red]", c)
		}
		return 1
	}
	// Provision each destination's writable staging BEFORE the argv is assembled:
	// a missing bind source kills the whole container, and the symlink hatch must
	// exist in the home skeleton (below) before the :ro home root is applied.
	if rt != "container" {
		printReplacedLinks(out, wsState,
			prepareHostFiles(wsState, hostFiles, loadedPacks, config.WritableHomeDirs(cfg, loadedPacks)))
	}
	// The machine-scope shared dirs' bind SOURCES, for THIS launch's selection and on both
	// container backends, since both bind them from the machine store (shareddirsources.go).
	// storage.EnsureGlobalStorage creates only the shipped packs' ones, before any config is
	// loaded, so a CONFIGURED pack's shared dir had no source and podman refused the whole
	// container with a bare statfs error. Fatal for that reason, and before the skeleton, so
	// the refusal leaves nothing behind.
	if !o.Sealed { // the seal binds none of them (seal.go)
		if err := ensureSharedDirSources(loadedPacks); err != nil {
			out.printf("[bold red]%s[/bold red]", err.Error())
			lock.Close()
			return 1
		}
	}
	// A SEALED BUILD'S ~/.cache AND /mise ARE ITS OWN: private directories of its workspace, made
	// before the argv names them (seal.go). Every other launch binds the machine's shared two.
	cacheDir, miseStore := paths.GlobalCache(), jailMiseStoreDir(o.inJail())
	if o.Sealed {
		var serr error
		if cacheDir, miseStore, serr = sealedStores(o.Workspace); serr != nil {
			out.printf("[bold red]could not make the sealed build's own cache and mise store: %s[/bold red]",
				serr.Error())
			lock.Close()
			return 1
		}
	}

	// --- Assemble the ordered argv ---
	// THE SCRATCH VOLUMES' NAMES, minted once for this launch and read by both the argv and
	// the teardown (scratchremoval.go), so the remover deletes exactly what was mounted.
	// Podman only: Apple Container's scratch dirs are always tmpfs.
	scratchID := newScratchLaunchID()
	if rt != "container" { // parity: NotApplicable — Apple Container's scratch dirs are always tmpfs (appleContainerBaseMounts), so it has no volumes to name or remove
		o.scratchVolumes = ScratchVolumeNames(cfgStr(cfg, "ephemeral_storage"), cname, scratchID)
		o.scratchRemovalOnce = &sync.Once{}
	}
	in := &assembleInput{
		cfg:              cfg,
		rt:               rt,
		cname:            cname,
		scratchID:        scratchID,
		imageRef:         loadedImage.Ref,
		jailPrefix:       jailPrefix,
		packs:            loadedPacks,
		jailDaemons:      jailDaemons,
		agentsPath:       agentsPath,
		packStaging:      packStaging,
		capturesDir:      o.CapturesDir(),
		forkDeliveries:   o.forkDelivered,
		wsState:          wsState,
		durableDir:       o.durableJailPath(),
		miseStore:        miseStore,
		cacheDir:         cacheDir,
		sealed:           o.Sealed,
		hostTZ:           detectHostTZ(),
		yoloVersion:      o.yoloVersion(repoRoot),
		mountTargets:     BindMountTargets(),
		storePruneOK:     storePruneOK,
		storePackages:    storePkgs,
		cacheRelocations: relocations,
		hostCASAlias:     hostCAS,
		writableHomeDirs: config.WritableHomeDirs(cfg, loadedPacks),
		hostFiles:        hostFiles,
		userEnv:          userEnv,
		channel:          channel,
	}
	// THE HOME SKELETON (homeskeleton.go): a NEW per-jail directory, bound :ro at
	// /home/agent, holding only the mountpoints and links this launch's binds need. Here,
	// and only here, because this is the first point where all three of its inputs exist
	// (the selected packs, the config and the resolved host_files entries), the attach
	// decision has been made twice, and the workspace flock is held. An attach never
	// reaches this line: the running jail keeps the skeleton it booted with, and nothing
	// ever edits one (the design's OQ-BH10 ruling). It goes once its container is known gone
	// (forgetGoneContainer, OQ-BH16), or with the reaper for a launch that died untorn.
	//
	// Built from the assembly input and written straight back into it, so the skeleton and
	// the argv binding it come from one value; TestRunContainerBuildsTheSkeletonOnTheFreshPath
	// pins that flow. Apple Container builds none: it binds wsState whole at /home/agent.
	if rt != "container" { // parity: HonoredBy — Apple Container binds this workspace's own wsState read-write at /home/agent, which needs no mountpoints and holds no other workspace's
		sk, err := buildHomeSkeleton(paths.HomeSkeletonRoot(cname), in.packs, in.cfg, in.hostFiles)
		if err != nil {
			out.printf("[bold red]%s[/bold red]", err.Error())
			lock.Close()
			return 1
		}
		// The best-effort entries that could not be made, each naming its path: the jail
		// still boots, and this line is the only place the missing mountpoint is said
		// (TestTheSkeletonsWarningsArePrinted).
		for _, w := range sk.warnings {
			out.printf("[yellow]Warning: %s[/yellow]", w)
		}
		in.homeSkeleton = sk.dir
		o.launchGuard.noteSkeleton(sk.dir)
	}
	// FROM HERE TO THE CONTAINER START, EVERY RETURN DISCARDS THE SKELETON: no container ever
	// held it, so leaving it would be one more directory for the reaper from a launch that
	// never ran (discardUnheldSkeleton; TestNoReturnAfterTheSkeletonLeaksIt pins each one).
	sp = o.Perf.Span("launch.assemble_argv")
	runCmd := o.assembleRunCmd(in)
	sp.End()

	// Every bind source against the macOS Podman Machine's share list, now that the argv
	// names them all: a workspace on /Volumes, a `mounts` entry or a `host_files` folder
	// the VM cannot see would otherwise fail inside it as `statfs …` at rc 125. The
	// prefix sources were already checked in resolveJailPrefix, on the same memoized
	// read; silent off macOS Podman and when the list cannot be read (machineshares.go).
	if msg := o.unsharedBindSources(rt, bindSources(runCmd, in.imageRef)); msg != "" {
		o.pr(o.Stderr).print(msg)
		discardUnheldSkeleton(cname, in.homeSkeleton)
		lock.Close()
		return 1
	}

	// THE SEVENTH bespoke pre-flight (docs/reference/providers.md#the-credential-preflight, #pv-oq-13), at the
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
	// THE NINTH, immediately before it and at every one of its three call sites
	// (envoverrides.go): the same composed channel answers both, and a jail carrying a
	// pack's variable beside something that pack declares overrides it is a wrong answer
	// rather than a missing one, so it is refused first. It has no escape hatch, which is
	// why there is no verdict to weigh here.
	if lines := o.checkEnvOverrides(cfg, rt, loadedPacks, channel, envPairs(runCmd)); len(lines) > 0 {
		o.printProviderRefusal(lines)
		discardUnheldSkeleton(cname, in.homeSkeleton)
		lock.Close()
		return 1
	}
	o.notePlatformSwitchConflicts(loadedPacks, channel)
	if lines, refuse := o.checkProviderCredentials(cfg, loadedPacks, channel, envPairs(runCmd)); len(lines) > 0 {
		o.printProviderRefusal(lines)
		lock.Close()
		// A set escape hatch turns the refusal into a loud continuation, so the verdict —
		// not the presence of output — is what ends the launch.
		if refuse {
			discardUnheldSkeleton(cname, in.homeSkeleton)
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
	if o.Sealed {
		// A sealed build reaches no host service through a forward (seal.go).
		forwardHostPorts = nil
	} else if appliedNetMode(rt, o.resolveNetMode(cfg), o.inContainer()) == "bridge" {
		// Disclose BEFORE merging, while the declared list is still separable from the
		// implicit one — after the merge there is no way to tell which ports the user wrote
		// (OQ-PC2). This is the only disclosure site: assembleRunCmd performs the same merge
		// for the container argv and must not print a second copy.
		discloseImplicitProviderForwards(func(msg string) { out.printf("[dim]%s[/dim]", msg) },
			forwardHostPorts, channel.localProviderForwardSources)
		forwardHostPorts = mergeHostForwards(forwardHostPorts, channel.localProviderForwards)
	}
	var portSocketDir string
	if len(forwardHostPorts) > 0 && (rt == "container" || !o.IsMacOS) {
		portSocketDir = o.fwdSocketDir(cname)
	}

	// Tracking + window title. The tracking file is removed again by forgetGoneContainer, at the
	// keeper's end, once the container is known gone. The OWNER-PID FILE is the keeper's to write,
	// not this launch's: it names the jail's owner, and that is the keeper, for the jail's whole
	// life (docs/design/jail-lifetime-last-session-wins.md JL-D18). The launch guard is told first,
	// so a signal from here to the keeper's spawn takes both records back too.
	o.launchGuard.noteRecorded()
	_ = runtimeWriteTracking(cname, o.Workspace)
	// THE PACK TREE CHANGES HANDS here: from now on the container holds it and the keeper owns it,
	// so Run's deferred discard leaves it, and it goes with the tracking file once the container is
	// known gone. The live-tree record names it for a later attach, which takes the launch lock this
	// launch hands its keeper, so no attach can find the container without the record.
	if err := writeLivePackTree(cname, packStaging); err != nil {
		out.printf("[yellow]Warning: could not record the pack tree this jail boots from (%s); "+
			"an attach to it will compose from the configured packs instead[/yellow]", err.Error())
	}
	o.packTreeHeld = true

	// THE HOST-EXECUTION DISCLOSURE, in this terminal and BEFORE THE SPAWN (§4.3 G4; JL-D6). The
	// keeper starts the host services, and runs exactly the plan this was printed from: it refuses
	// a daemon the plan does not name (keeper.go's checkPlan). A keeper that printed its own
	// disclosures would print them after its spawn, which is a notification rather than a
	// disclosure (the design's §9.6 warning).
	//
	// NEVER UNDER THE SEAL (seal.go): a fork build starts no loophole and no host service, and
	// registers no credential view, so nothing is disclosed, the plan names no service and the keeper
	// is told it is sealed. A sealed launch still spawns its keeper, which holds only the container,
	// its records and its teardown (FP-D15). The services come from the one function the keeper
	// checks the plan with, which plans none under the seal in either process.
	//
	// jailDaemons is the payload this launch's argv already carries (assembleInput.jailDaemons),
	// so the jail daemons the disclosure names are the ones the jail's supervisor will run.
	if !o.Sealed {
		o.discloseLoopholes(rt, cfg, loadedPacks, jailDaemons)
	}
	services := o.plannedLoopholeNames(rt, cfg)
	var forwards []PortForward
	if portSocketDir != "" {
		forwards = o.planPortForwards(forwardHostPorts)
	}

	// THE MAIN PROCESS'S TAIL: the hold form of the entrypoint's argv, carrying the
	// provisioning stage for the first session to run on its terminal (entrypoint/jailmain.go).
	// No session's command is here any more: the first session's is its exec's
	// (firstSessionExecCmd, below). Its timing branch is on timingReporting(), not the
	// recording gate: the in-container timing block is PRINT-ONLY (the jail appends to
	// ~/.yolo-perf.log with or without it), so a silently-recording launch must not switch it
	// on — it is the second half of the noise D12 removes, and the larger half on a fast host.
	//
	// The host services' endpoint pairs are not in it yet: the keeper inserts them before the image
	// once it has started them (insertHostServiceEnv), exactly where this launch used to.
	runCmd = append(runCmd, entrypoint.HoldMainArg, o.provisionStage())
	// THE FIRST SESSION IS NAMED IN THE JAIL, as an attach's is (sessionhangup.go), so its arm hangs
	// up its own processes and nothing else (JL-D4, as OQ-JL8 ruled).
	sessionID := newSessionID()
	firstExec := o.firstSessionExecCmd(rt, cname, o.sessionCmd(targetCmd), sessionID)

	if o.Getenv("YOLO_DEBUG") != "" {
		// Write RAW (not via the rich-stripping printer): the argv contains
		// literal bracket sequences (e.g. the grep block_flags "-*[rR]*", the
		// "[path]" suggestion) that the rich-tag regex would eat.
		fmt.Fprintln(o.Stderr, shquoteJoinDebug(runCmd))
		fmt.Fprintln(o.Stderr, shquoteJoinDebug(firstExec))
	}

	// THE OFFERED TIER (§5.3's trigger), before the container attaches and while
	// the terminal is still ours. It never walks anything — it reads what the
	// LAST launch's slot measured, which is what "measure late, offer early"
	// means. A non-TTY launch gets one printed line and no prompt.
	reclaimConsent := o.maybeOfferReclaim()

	// Fresh-launch line (with resource parts) to stderr for log capture (audit
	// §B#4. The version and platform came earlier, from the startup banner every
	// subcommand prints.
	o.emitLaunchBanner(rt, cname, resPartsFor(cfg, rt), "")

	// The empty-packs notice rides immediately behind the banner: this is the LAST
	// host-side output before the container takes the terminal, so it is the only spot
	// where the message is still on screen when the agent (or the fallback bash)
	// starts. Printed any earlier it scrolls away behind the nix build.
	o.warnIfNoPacks()

	// Beside it, for the same reason: a declared disk I/O priority this backend cannot pass,
	// or that the disk under the workspace ignores, is a declaration doing nothing, and this
	// is where a user reads what the launch will not do (docs/design/io-priority.md §5.2).
	o.noteIOPriority(rt, ioprio.FromResources(cfgMap(cfg, "resources")))

	// Right behind that: what each loaded pack READS from the host this launch. A fetched
	// pack CAN read the host now (with approval), so the effective host access must be
	// visible every launch, not just recorded in a lockfile — the transparency half of the
	// approval model.
	//
	// The READ half only. Host EXECUTION was disclosed above, before the keeper's spawn, and
	// deliberately not repeated here.
	o.notePackHostAccess(loadedPacks, channel)

	// And beside it, for the same reason: a WRITABLE bind of the host user's own
	// cache is host access, so L9's decision is disclosed at every launch that
	// made one rather than recorded somewhere (hostcasalias.go). A launch on a
	// machine with no recognised host store prints nothing — there was no happy
	// path to degrade from.
	o.noteHostCASAlias(hostCAS)

	// Right behind that: where the launch's profile selections landed. Same stderr, same
	// dim register, same reason — a selected profile is part of the effective
	// environment, and the human reading the launch should see the name every pack's
	// derive is about to receive rather than infer it from an env var.
	o.noteUseProfiles(channel, loadedPacks, envPairs(runCmd))

	// THE PLAN (keeperplan.go, JL-D20): everything above that the keeper runs, as one value.
	plan, err := o.keeperPlanFor(cfg, rt, cname, staged, services, jailDaemons, forwards,
		portSocketDir, socketsDir, runCmd, in)
	if err != nil {
		out.printf("[bold red]Refusing to launch: %s[/bold red]", err.Error())
		o.unwindUnspawned(cname, rt, packStaging, in.homeSkeleton)
		return 1
	}
	// THE KEEPER IS DISCLOSED, last before its spawn (JL-D21): a process that outlives this
	// terminal is exactly what a launch's disclosures exist to name.
	o.pr(o.Stderr).print("[dim]" + richtext.Escape(o.keeperLine(cname, services, len(forwards), rt)) + "[/dim]")

	// THIS LAUNCH IS A SESSION OF THE JAIL IT STARTS, counted before the keeper exists and while
	// the launch lock is held (sessionlock.go, JL-D16), so the keeper can never see zero sessions
	// before the first one has begun, and no orphan sweep can find the jail running with nobody
	// counted in it.
	o.holdSessionLock(cname)
	// A FIRST SESSION THE COUNT DOES NOT HOLD (holdSessionLock warned, or found a drain it cannot
	// be in) makes the count's zero a lie: its keeper is told, and never drains on it (JL-P3).
	plan.Uncounted = o.sessionLock == nil
	if plan.Uncounted {
		out.printf("[yellow]This jail's keeper cannot count this session, so it will not end the jail when "+
			"its sessions leave; %s ends it.[/yellow]", stopRemedy(rt, cname))
	}

	// THE KEEPER, handed the plan, the progress pipe, the lifeline and the launch lock (JL-D31),
	// and from here the owner of the jail's host services and of the jail's life (§9). The whole
	// child window — the keeper's start, the boot it relays and the first session — under one span.
	sp = o.Perf.Span("launch.run_with_proxy")
	kp, err := o.startKeeper(plan)
	if errors.Is(err, errLaunchEnded) {
		select {} // the launch guard is ending this launch, with what it made; never race it
	}
	if err != nil {
		sp.End()
		out.printf("[bold red]%s[/bold red]", err.Error())
		o.unwindUnspawned(cname, rt, packStaging, in.homeSkeleton)
		return 1
	}
	// ONE SIGNAL ARM FOR THE WHOLE WINDOW (keeperspawn.go). Until ready a signal ends this launch
	// alone, which closes the lifeline and so has the keeper unwind; from ready on it is the
	// session's arm, retargeted rather than replaced, so no signal falls between two arms. It takes
	// over from the launch guard, which goes once it is installed (armstack.go: the innermost arm
	// alone acts).
	arm := armLaunchSignals(o.keeperPreReadyTeardown(kp, cname, rt))
	if !o.retireLaunchGuard() {
		// The launch guard is ending this launch, through its keeper: it keeps every later signal,
		// so the arm just installed never runs the same teardown beside it. Never race it.
		popLaunchArm(arm)
		select {}
	}
	// THE HERDR PANE, registered now that the arm covers it (herdragent.go): every teardown the
	// arm runs releases it, and so does this launch before each disarm below.
	o.registerHerdrAgent(loadedPacks, injectedArgs)
	keeperStarted := false
	ready := relayKeeper(kp.progress, o.Stdout, o.Stderr, os.Stdout, os.Stderr, keeperEvents{
		started: func(pid int) {
			keeperStarted = true
			o.pr(o.Stderr).printf("[dim]keeper: started, pid %d[/dim]", pid)
		},
		spawned: func() {
			o.Perf.Mark("jail_main.spawned")
			// The launch's fate is known: its runtime is spawned (launchrecord.go).
			o.recordLaunchOutcome(launchStarted, -1)
		},
		// THE HOUSEKEEPING SLOT (OQ-BF5), which stays in this launch (JL-D10): after the launch
		// lock is released and the container is visible, so a reap can never be looking at this
		// launch's image before its container exists, and on its own goroutine, so nothing here
		// delays the jail. It dies at this launch's exit, restartable by design.
		running: func() {
			housekeepingSlots.Go(func() { safeRun(func() { o.runHousekeeping(rt, reclaimConsent, cname) }) })
		},
	})
	_ = kp.progress.Close()
	if !ready {
		// The keeper ended before the jail was ready: a boot that refused, a runtime that would
		// not start it, a service or the container that failed to start, or this launch's own
		// signal. It unwound what it started, and its status is the launch's, as the container's
		// always was. Its exit, not the pipe's end, is the evidence (JL-D29).
		<-kp.exited
		sp.End()
		o.releaseHerdrAgent()
		if !arm.disarm() {
			select {} // the signal arm is ending this launch; never race it
		}
		kp.closeLifeline()
		// A keeper that died before it even said it started said nothing else either: its own
		// stderr is /dev/null, so this line is the only account of it.
		if !keeperStarted && kp.exitCode != 0 {
			out.printf("[bold red]This jail's keeper ended (status %d) before it started; its log: %s[/bold red]",
				kp.exitCode, keeperLogPath(cname))
			o.unwindUnspawned(cname, rt, packStaging, in.homeSkeleton)
		}
		o.emitTimingReport(kp.exitCode, cname, rt)
		return kp.exitCode
	}
	// READY: from here this launch is an ordinary session of its jail (§9.3). The lifeline is done:
	// the keeper stops reading it at ready, since a first session's end is one session ending.
	if !arm.retarget(o.attachTeardown(rt, cname, sessionID)) {
		select {} // the signal arm is ending this launch; never race it
	}
	kp.closeLifeline()
	sessionStart := o.Now()
	logFrom := keeperLogSize(cname)
	rc, execErr := runArmedSession(firstExec, arm, o)
	if !arm.detach() {
		select {} // the signal arm is ending this launch; never race it
	}
	// The agent has left this pane: herdr is told while the arm still covers the call, and
	// before the jail's teardown, which this session may wait out, is streamed.
	o.releaseHerdrAgent()
	if !arm.disarm() {
		select {} // the signal arm is ending this launch; never race it
	}
	sp.End()
	if execErr != nil {
		out.printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", rt)
		out.print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		rc = 1
	}
	// ITS QUIT IS ANY SESSION'S (endSession): why the jail ended under it when it did, then its
	// lock, then one line while others remain, or the keeper's teardown streamed when it was the
	// last. The prompt comes back as soon as the agent is gone.
	rc = o.endSession(cname, rt, rc, sessionStart, logFrom, true)
	o.emitTimingReport(rc, cname, rt)
	return rc
}

// teardownAfterExit is a jail's shutdown chain once its container is stopped: the KEEPER'S, run in
// the keeper after its stop (keeper.go's endJail), and the reap's, run by an unkept jail's last
// session or `yolo stop` (reapUnkeptJail). It was the fresh launch's normal-exit tail until the
// keeper took the jail's host services over, and moved unchanged. Its span call sites are
// unit-reachable on purpose: the timing surface this accompanies was call-site-unpinned for its
// whole life (the old single-Total block's comment recorded the debt), and a chain this repo cannot
// unit-test is a chain whose spans can silently stop being emitted. Deleting any span below fails
// TestTeardownChainEmitsShutdownSpans.
func (o *Options) teardownAfterExit(socatProcs []*forwardProc, portSocketDir string,
	hostServices []loopholeDaemon, socketsDir, cname, rt, skeleton string, rc int) {
	// FIRST: the scratch volumes' deletion, started detached and never awaited
	// (scratchremoval.go). The proxy has returned, so the child is reaped and the termios
	// restored; this spawn is what moved a 32 s delete out of the client that held the
	// terminal, and starting it first gives it the most head start on a relaunch.
	o.startScratchRemoval(rt)
	sp := o.Perf.Span("shutdown.cleanup_port_forwarding")
	cleanupPortForwarding(socatProcs, portSocketDir)
	sp.End()
	sp = o.Perf.Span("shutdown.stop_loopholes")
	o.stopLoopholes(hostServices, socketsDir, cname, rt)
	sp.End()
	// The tracking file, once the runtime says the `--rm` container is gone. Before this a
	// normal exit left it, so prune.PruneOrphanAgentStaging kept the jail's AGENTS_DIR entry,
	// and every skeleton in it, for as long as the file lasted (trackingcleanup.go).
	sp = o.Perf.Span("shutdown.clear_tracking")
	o.forgetGoneContainer(cname, rt, skeleton)
	sp.End()
	// E3: the container is `--rm` and now gone, so fold this session's in-jail config
	// edits into their overlay sidecars from the host side, before anyone can ask
	// `yolo config diff` and get last session's answer.
	sp = o.Perf.Span("shutdown.capture_config")
	o.captureConfigOnTerminate(rt)
	sp.End()
	// Only while it still names this process (JL-D28 (4)): the keeper's, when the keeper runs this
	// chain. A teardown that ran late would otherwise take the next jail's keeper's file away.
	clearOwnerPIDIf(cname, o.Getpid())
	sp = o.Perf.Span("shutdown.oom_check")
	o.maybeWarnAboutOOMKiller(rc, rt)
	sp.End()
	// LAST in the chain, and that position is load-bearing: the query passes
	// --until now, which has to be later than any cleanup event conmon wrote
	// (D9). It records on the RECORDING gate, so a quiet `perf_logging: true`
	// launch gets Window A's cost in its file — the number is unpredictable
	// enough that "type --timing next time" was never a real answer.
	o.recordWindowA(cname, rt)
}

// emitTimingReport ends the launch's timing surface — in one of two ways, and
// which one is D12's whole question.
//
// A launch that RECORDED but was never asked to report (the persistent opt-ins:
// `perf_logging: true`, an exported YOLO_TIMING/YOLO_VERBOSE) prints ONE dim
// line naming the file it just wrote, and nothing else: the events are already
// on disk, and a ~25-line table at every jail quit scrolls away whatever the
// user was reading, which is the noise this split removes. A launch whose user
// typed --timing or --verbose gets the full report — they asked for it, now.
//
// The full report's other halves: the YOLO_JAIL_TIMING=1 env pair assemble.go puts
// on the container argv (pinned by timingenv_test.go) and the entrypoint's own
// perf log, which the in-container branch prints. Called from the normal-exit
// tail and from INSIDE onTerminate — never later, because the signal arm
// os.Exits past anything after it.
//
// The Window A attribution line renders only when a child.exited mark exists
// (the fresh-launch and attach arms both produce one) and podman's event log
// answers — every failure mode is silence, and the attribution's own run is
// spanned so it can never become a mystery itself. It is part of the REPORT, so
// the quiet path never runs that query at all.
func (o *Options) emitTimingReport(rc int, cname, rt string) {
	// The Once is created by initPerf exactly when a collector is, so a nil one
	// IS "this launch recorded nothing" — the recording gate, read off the state
	// it produced rather than re-evaluated here.
	if o.perfReportOnce == nil {
		return
	}
	o.perfReportOnce.Do(func() {
		if !o.timingReporting() {
			o.noteTimingLogLocation()
			return
		}
		o.emitTimingReportLocked(rc, cname, rt)
	})
}

// noteTimingLogLocation is the quiet path's entire output: one dim line, so a
// launch that recorded silently is still DISCOVERABLE — the maintainer who
// turned `perf_logging` on months ago has to be able to find the file without
// re-reading the config reference.
func (o *Options) noteTimingLogLocation() {
	o.pr(o.Stderr).printf("[dim]yolo: timings recorded in %s (--timing prints them)[/dim]",
		filepath.Join(paths.WorkspaceStateDir(o.Workspace), HostPerfLogName))
}

func (o *Options) emitTimingReportLocked(rc int, cname, rt string) {
	// ATTRIBUTION ALREADY RAN, in whichever teardown arm reached this — and that
	// is what keeps its own `podman events` span, and the shutdown.window_a it
	// records, INSIDE the table about to be rendered. Querying from HERE left
	// both in the FILE and missing from the print on every launch: the one
	// number the reader is looking at was the one that could not appear in it
	// (measured on a real host 2026-09-08, first fixed by ordering the two calls,
	// then fixed properly by moving the query onto the recording gate, where it
	// runs for launches that never print at all).
	//
	// The line still prints BELOW the table, where it belongs: it says more than
	// the row does — the cleanup event's own offset, or why there is no row.
	attribution, why := o.windowA.line, o.windowA.reason
	o.pr(o.Stderr).printf("[bold cyan]--- Host-side timing (rc %d) ---[/bold cyan]", rc)
	o.Perf.Report(o.Stderr, time.Now())
	switch {
	case attribution != "":
		o.pr(o.Stderr).printf("[dim]  %s[/dim]", attribution)
	case why != "":
		o.pr(o.Stderr).printf("[dim]  %s[/dim]", why)
	}
	o.pr(o.Stderr).printf("[dim]  host file: %s[/dim]",
		filepath.Join(paths.WorkspaceStateDir(o.Workspace), HostPerfLogName))
	o.pr(o.Stderr).printf("[dim]  jail half: %s[/dim]",
		filepath.Join(paths.WorkspaceHomeState(o.Workspace), "yolo-perf.log"))
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

// hostServiceLaunchEnvVar names the variable a NATIVE launch carries for one started host
// service — the macos-user counterpart of the `-e` pair insertHostServiceEnv splices into a
// container argv.
//
// THE HANDLE'S OWN NAME WHEN IT HAS ONE, because the spelling describes the VALUE: a
// loopback-TLS service publishes an endpoint file and a socket-transport one publishes a
// socket, and hostServiceEnvVar/hostServiceSocketEnvVar exist so a client cannot read the
// wrong kind of path out of the right-looking variable.
//
// AN EMPTY NAME IS THE HOST-SCOPED FRONT (startHostSingleton), which carries none because the
// container path emits its variable much earlier, at argv-assembly time
// (hostServicesMountArgs), optimistically and before the front has published. This backend
// assembles no argv and has no in-jail reachability witness to refuse a broken promise, so the
// handle is both the only source it has and the honest one: the variable is emitted for a
// service that really did publish. Falling back to hostServiceEnvVar rather than skipping is
// what keeps the credential daemons — the broker and the OpenAI service, both host-scoped —
// from being the two this arm silently omits.
func hostServiceLaunchEnvVar(h loopholeDaemon) string {
	if h.envVarName != "" {
		return h.envVarName
	}
	return hostServiceEnvVar(h.name)
}

// startedLoophole reports whether the lifecycle returned a handle for one service, which is
// the only evidence a caller has that it published: every start path warns and returns no
// handle rather than failing, so "did this one come up" is a question about the returned
// slice and never about the config.
func startedLoophole(handles []loopholeDaemon, name string) bool {
	for _, h := range handles {
		if h.name == name {
			return true
		}
	}
	return false
}

// attachExisting runs the exec-into-existing-container branch (and the
// raced-attach twin). raced selects the second banner text.
//
// staged and channel are the pieces Run composed above the backend dispatch — the same
// values the fresh path consumes — because an attach is an ENTRY: it delivers the
// per-entry channel (deliverChannelOnAttach below), not nothing.
//
// BUT THE JAIL KEEPS THE PACKS IT BOOTED WITH (OQ-PK2 (c), packtree.go). staged is this launch's
// own staging of the config, which nothing binds, so it is what a RESTART continues with and
// nothing else: every host-side reader of the attach reads the running jail's own tree
// (runningJailPackView) — the channel it delivers, the command it execs, the skills and briefing
// it refreshes, the loophole record behind the briefing — and this attach writes nothing into
// that tree. When the configured packs differ from the jail's, it says so and names the restart.
// When the jail's tree will not load, or its packs cannot serve what this entry selects, the
// attach takes the contract gate's disposition (settleAttachSkew) before anything is written.
// Only a tree it cannot FIND leaves it composing from the configured packs, with a warning.
//
// release ends the caller's hold on the workspace launch lock. It runs once the attach has
// settled on going ahead, before the exec, and on a refusal; it does NOT run when the contract
// gate restarts the jail. restarted is true exactly then: the jail is stopped and gone, nothing
// was exec'd, and the caller continues into the fresh launch still holding the lock.
func (o *Options) attachExisting(cname, rt, targetCmd string, cfg *jsonx.OrderedMap,
	staged stagedPacks, channel *packChannel, raced bool, release func()) (rc int, restarted bool) {
	out := o.pr(o.Stdout)
	// The moment this entry began: a stop record older than it explains some earlier end, never
	// this session's (stopreason.go).
	attachStart := o.Now()
	released := false
	releaseLock := func() {
		if !released && release != nil {
			released = true
			release()
		}
	}
	// ONE inspect serves the banner's baked version and the contract gate's tags — the
	// container's whole frozen env, read once.
	envLines := o.inspectContainerEnv(rt, cname)
	// Launch line to stderr — surfaces the jail's BAKED version so a host CLI
	// upgrade attaching to a pre-upgrade container (stale shims/mounts/entrypoint)
	// is visible at a glance (audit §B#4.
	baked, _ := runtime.BakedYoloVersionFromInspectEnv(envLines)
	o.emitLaunchBanner(rt, cname, nil, baked)
	// THE RUNNING JAIL'S PACKS, before the gate: what this entry delivers is composed over
	// them, and the gate asks whether the jail can receive what this entry delivers. A jail
	// whose tree will not load, or whose packs cannot serve what this entry selects, is a known
	// difference, and takes the gate's own disposition before anything is written: the
	// acknowledgment, a restart, or a refusal (settleAttachSkew). Never the configured packs in
	// its place: those are what OQ-PK2 (c) keeps from a running jail.
	view, packSkew := o.runningJailPackView(cname, rt, cfg, staged, channel, targetCmd)
	deliver := true
	// THE SKEW THIS ENTRY ACKNOWLEDGED, when it went ahead under AllowAttachSkewEnv: the briefing
	// this attach refreshes names it for the session it starts (SK-D15). At most one: the first
	// acknowledgment withholds the whole channel, and nothing after it asks again.
	var acked *attachSkew
	if packSkew != nil {
		skew := o.packSkew(baked, packSkew)
		switch o.settleAttachSkew(cname, rt, skew) {
		case skewRestarted:
			return 0, true
		case skewAcknowledged:
			// The same degradation as a missing contract: nothing of this entry's channel is
			// written. The command still carries the jail's own launch flags, where its packs
			// could be read.
			acked = &skew
			deliver = false
			if !view.unreadable {
				view.targetCmd = o.injectLaunchFlagsForAttach(view.staged.packs, o.Args, targetCmd)
			}
		default:
			releaseLock()
			return 1, false
		}
	}
	channel, targetCmd = view.channel, view.targetCmd
	// A PROFILE-SERVED DAEMON this entry's selection needs and the running jail never started
	// (OQ-CN7 (b)): its launch selected no profile it serves, and an attach starts no daemon, so
	// the pointer this entry would deliver points at nothing. The attach-skew disposition, as
	// for any jail that cannot take what an entry delivers: a restart, a refusal, or the hatch.
	// entryDaemons is what this entry's selection runs in the jail, kept for the launch check
	// below: it decides which host services this entry's agents reach.
	var entryDaemons []loopholes.JailDaemonSpec
	if deliver && !view.unreadable {
		entryDaemons = o.jailDaemonsFor(cfg, rt, view.staged.packs)
		if missing := missingProfileServedDaemons(envLines,
			entryDaemons, view.staged.packs); len(missing) > 0 {
			skew := profileDaemonSkew(missing)
			switch o.settleAttachSkew(cname, rt, skew) {
			case skewRestarted:
				return 0, true
			case skewAcknowledged:
				acked = &skew
				deliver = false
			default:
				releaseLock()
				return 1, false
			}
		}
	}
	// THE RUNNING JAIL'S CALLER TOKENS (callertokens.go, WB-D18). This process minted fresh ones
	// when it composed, and the jail's service daemon demands the ones its own launch minted: it
	// read them once at boot. So the attach delivers those, recomposing when they differ, and a
	// jail launched before caller tokens keeps delivering none it could check.
	if deliver && channel != nil {
		rekeyed, err := o.rekeyChannelForAttach(paths.WorkspaceHomeState(o.Workspace),
			view.staged.packs, channel, func(packs []*packload.Pack) (*packChannel, error) {
				return o.composePackChannel(cfg, packs, channel.userEnv)
			})
		if err != nil {
			o.printProviderRefusal([]string{"Refusing to attach: " + err.Error()})
			releaseLock()
			return 1, false
		}
		channel = rekeyed
	}
	// THE CONTRACT GATE (contracttags.go). What this entry would deliver decides the tags it
	// needs; a tag the jail lacks means the jail's binaries cannot receive it, and the attach
	// never proceeds on its own then: the acknowledgment, a restart, or a refusal. An entry
	// that already delivers nothing asks nothing of the jail's binaries.
	if deliver {
		contract := attachContractFor(channel, envLines)
		deliver = !contract.standIn
		if missing := contract.missingFrom(envLines); len(missing) > 0 {
			skew := o.contractSkew(baked, missing)
			switch o.settleAttachSkew(cname, rt, skew) {
			case skewRestarted:
				return 0, true
			case skewAcknowledged:
				// The host-side degradation: nothing of this entry's channel is written, so the
				// jail keeps what its last entry gave it. A partial write would be worse than
				// none — the shared half alone strips every scoped value the jail now holds.
				acked = &skew
				deliver = false
			default:
				releaseLock()
				return 1, false
			}
		} else {
			// THE SKEW LINE, for a jail that is older but can take everything this entry
			// delivers. The banner prints the baked version; this prints the DIFFERENCE, which
			// is the actionable half: since flake-bundle generations an old jail keeps WORKING
			// across a host install, so nothing else would tell the user their session is
			// running last week's yolo-entrypoint (attachskew.go). A jail missing a contract
			// got the gate's fuller account above instead.
			o.warnIfJailIsOlderThanTheLauncher(rt, cname, baked)
		}
	}
	// THIS ENTRY IS A SESSION OF THE JAIL, counted while the launch lock is held and before the
	// exec (sessionlock.go), so an orphan sweep never stops a jail with this session in it, and its
	// keeper never drains under it. A count that had to wait was waiting on a sweep that held the
	// lock to stop this very jail: when the jail is gone after it, this launch is a fresh one, lock
	// still held, before anything below discards the pack tree the fresh path boots from. A count
	// that found the jail's KEEPER holding the lock is arriving during a drain (JL-D28): it never
	// counts itself into a jail being stopped, and the caller waits for that keeper instead.
	if o.holdSessionLock(cname) && (o.keeperDrainSeen || o.findRunningContainer(cname, rt) == "") {
		o.releaseSessionLock()
		return 0, true
	}
	if o.sessionLock == nil {
		// Uncounted, which only its own terminal can be told: the keeper decides the jail's end on
		// the count, and cannot see this session in it.
		o.pr(o.Stderr).print("[yellow]The jail's keeper cannot see this session, so it may end the jail " +
			"under it once the sessions it counts have left.[/yellow]")
	}
	// Going ahead. The host-side readers switch to the jail's own packs, and the skills and
	// briefing staging the jail binds is refreshed from them, still under the lock: another
	// launch of the workspace writes the same staging. This launch's own pack tree has done its
	// job (the comparison and the pre-flights) and goes; nothing ever bound it.
	//
	// A tree that would not load refreshes nothing: its packs are unknown here, and the
	// configured ones are what the jail must not be handed. Its skills and briefing stay as its
	// last entry left them, which the acknowledgment said.
	if view.unfound == "" && !view.unreadable {
		adoptPackRecords(view.staged.packs)
	}
	var refreshErr error
	if !view.unreadable {
		sp := o.Perf.Span("launch.refresh_jail_briefings")
		// The briefing's I/O priority is the one this jail's processes hold, from its frozen
		// environment, never the current config's (refreshJailBriefings says why). So is its
		// durable dir: the one its launch exported, which this attach inherits.
		o.durable = o.attachDurableDir(envLines, cfg)
		// An acknowledged skew is named in the briefing too (SK-D15): the session this attach
		// starts reads it, where the stderr account above has scrolled away.
		o.attachSkewNotice = o.attachSkewBriefing(acked, baked, rt, cname)
		_, refreshErr = o.refreshJailBriefings(cname, cfg, rt, view.staged, o.launchedIOPriority(rt, envLines))
		o.attachSkewNotice = nil
		sp.End()
	}
	discardPackTree(cname, o.packTree)
	if refreshErr != nil {
		o.pr(o.Stderr).printf("[bold red]%s[/bold red]", refreshErr.Error())
		releaseLock()
		return 1, false
	}
	// And the acknowledgment's last line: where else this difference is recorded, or that
	// nowhere else is.
	if acked != nil {
		o.noteAttachSkewBriefing(rt, view)
	}
	releaseLock()
	if raced {
		out.printf("[bold cyan]Attaching to jail started by another process [dim](%s)[/dim]...[/bold cyan]", cname)
	} else {
		out.printf("[bold cyan]Attaching to existing jail [dim](%s)[/dim]...[/bold cyan]", cname)
	}
	// The launch's fate is known: it attaches (launchrecord.go).
	o.recordLaunchOutcome(launchAttached, -1)
	// What this attach did NOT deliver: the configured packs, when they differ from the ones
	// the jail booted with (OQ-PK2 (c)'s notice).
	o.noteBootedPackSetDiffers(rt, cname, view)
	// And what it did not APPLY: a host-wide daemon's settings the config has changed since it
	// started. Reported, never restarted, from an attach (noteSingletonSettingsDrift).
	o.noteSingletonSettingsDrift(cfg)
	// And what its keeper recorded DOWN since the jail started (keeperwatch.go, JL-D19): the keeper
	// restarts nothing, and this session was not in when it went, so its keeper's line never
	// reached this terminal.
	o.noteServicesDown(cname, rt)
	// Attach gets the notice too, and that is not symmetry for its own sake: once a
	// jail is up, attaching is how a user re-enters it, so a fresh-launch-only notice
	// is one a user with a long-lived jail may never see.
	o.warnIfNoPacks()
	// The disk I/O priority line, for the same reason, graded against the value this attach's
	// shell receives: the one the jail was launched with (attachIOPriority).
	o.noteIOPriority(rt, o.attachIOPriority(rt, cfg, envLines))
	// THE PER-ENTRY CHANNEL, delivered before the exec: the same write the fresh
	// path performs, checked and disclosed the same way. This is the §4.3 half the
	// attach branch never had — until it did, 'yolo -p <name> -- claude' against a
	// running jail parsed and validated the selection, composed the channel, and
	// dropped it, silently.
	if deliver {
		// What the container's frozen environment already carries is yolo's, not the user's
		// (inheritedValues, OQ-CN8): the agent files override it rather than defer to it.
		if channel != nil {
			channel.bootEnv = envLinesMap(envLines)
		}
		if rc := o.deliverChannelOnAttach(cname, rt, cfg, view.staged, channel); rc != 0 {
			return rc, false
		}
		// THE LAUNCH CHECK, asked of the services the running jail's launch started, through the
		// fronts it still owns (runAttachLaunchChecks): a session that lapsed since that launch
		// is warned about here, before this entry's agent's first request finds out. Only for an
		// entry that delivers its channel, the one whose agents are handed the pointers.
		o.runAttachLaunchChecks(cname, rt, cfg, entryDaemons)
	}
	// NOTHING TO HEAL HERE ANY MORE, and the absence is worth a note because the
	// call this replaces was deliberate. An attach used to re-ensure the per-jail
	// broker relay (behind the same gate the launch path uses, OQ-A11), because the
	// relay was a separate process that could have died since launch. The jail's
	// half of the credential path is now a front owned by the jail's KEEPER, the process
	// the launch that started it spawned; a different process attaching cannot heal it,
	// and starting a second front over the same endpoint file would hand the jail a
	// credential its terminator never asked for. A jail whose launcher is gone is relaunched,
	// not attached-and-repaired, and the launcher that counts is the keeper: an entry into a
	// jail whose keeper died is refused (refuseUnkeptJail), and `yolo stop` then a launch is
	// the remedy.

	execFlags := []string{"-i"}
	if o.IsTTYStdout() {
		execFlags = append(execFlags, "-t")
	}
	// No key sequence detaches this client from its session (runtime.DetachKeysArgs, JL-D27):
	// a detached client returns as if the session had ended, and leaves its agent running
	// headless in the jail.
	execFlags = append(execFlags, runtime.DetachKeysArgs(rt)...)
	// The container's environment is the one it was LAUNCHED with, so this invocation's
	// NO_COLOR rides the exec itself — and a NO_COLOR the launch froze is cleared when
	// this invocation has none (attachNoColorEnvArgs says why).
	execFlags = append(execFlags, o.attachNoColorEnvArgs(envLines)...)
	// THIS SESSION'S NAME in the jail, which its signal arm hands back to end its processes
	// there (sessionhangup.go); none for a jail that cannot hang a session up.
	sessionID := attachSessionID(envLines)
	execFlags = append(execFlags, sessionEnvArgs(sessionID)...)
	runCmd := append([]string{rt, "exec"}, execFlags...)
	// The absolute path into the mounted install prefix, matching the fresh-launch
	// argv. `podman exec <cname> yolo-entrypoint` would resolve on the CONTAINER's
	// PATH through /bin/yolo-entrypoint, which is now a symlink into the mount —
	// it works in a running jail (the mount is there, or it would not be running),
	// but the two spellings would then differ for no reason, and the one that is
	// harder to get wrong is the one that names the file.
	runCmd = append(runCmd, cname, JailEntrypointPath, targetCmd)

	// The attach arm's child window. An attach session has almost no teardown
	// of its own (the jail keeps running), so if the 30-second symptom
	// reproduces HERE the delay is inside the runtime's exec — a different
	// suspect list than the fresh-launch arm's, and the spans say which arm
	// you are in.
	//
	// ITS SIGNAL ARM ENDS THIS SESSION AND NOTHING ELSE (OQ-JL8, JL-D4): a closed terminal or
	// pane, or a kill, hangs up this session's processes in the jail before the exec client is
	// killed, since killing the client alone left them running there with no terminal. Armed
	// just before the exec and disarmed once it returns, with or without a terminal.
	arm := o.attachSignalArm(rt, cname, sessionID)
	// It takes over from the launch guard, whose pack tree this attach discarded above.
	if !o.retireLaunchGuard() {
		// The guard is ending this launch: it keeps every later signal. Never race it.
		popLaunchArm(arm)
		select {}
	}
	// THE HERDR PANE, registered under the arm (herdragent.go), against the packs the running jail
	// booted from, as every host-side reader on an attach reads them; released once the exec
	// returns, before the arm lets go.
	o.registerHerdrAgent(view.staged.packs, o.Args)
	// What the keeper records from here on is this session's to be shown at its quit (JL-D19).
	logFrom := keeperLogSize(cname)
	sp := o.Perf.Span("attach.exec")
	rc, err := runArmedSession(runCmd, arm, o)
	sp.End()
	o.releaseHerdrAgent()
	if !arm.disarm() {
		select {} // the signal arm is ending this launch; never race it
	}
	if err != nil {
		out.printf("[bold red]Configured runtime '%s' not found on PATH.[/bold red]", rt)
		out.print("[dim]Run `yolo check` to validate runtime availability before restarting.[/dim]")
		return 1, false
	}
	// THE POST-MORTEM, on stderr and only when the exec itself failed. The
	// runtime has already printed which path it could not stat; this says what
	// that means and what to do, for the one cause that produces it — the jail's
	// mounted binaries deleted out from under a running container. Silent for
	// every other rc, and silent when it cannot prove the shape (brokenprefix.go).
	if msg := o.diagnoseBrokenPrefix(rt, cname, rc); msg != "" {
		o.pr(o.Stderr).print(msg)
	}
	// ITS QUIT, which is every session's (endSession, keeperspawn.go): WHY THE JAIL ENDED UNDER IT,
	// when it did (the status a jail's end gives an exec, a runtime that says the jail is gone, and
	// the record whatever ended it wrote, stopreason.go; a recorded stop explains a 137, so the OOM
	// hint is left out then); then its session lock; then one line while other sessions remain, the
	// keeper's teardown streamed when it was the last, or the reap of a jail whose keeper died.
	rc = o.endSession(cname, rt, rc, attachStart, logFrom, false)
	o.emitTimingReport(rc, cname, rt)
	return rc, false
}

// deliverChannelOnAttach delivers this entry's provider/profile channel into the
// RUNNING jail. The mechanism is the fresh path's own: deliverChannel over the channel,
// into the live-mounted yolo-user-env.sh and agent env files — the binds show the rewrite
// inside the jail instantly, and the exec'd yolo-entrypoint re-runs the boot, whose FIRST
// step (hydrate) applies the plain-form channel lines over whatever the entry's environment
// holds. Per-session by construction: an already-running session's processes keep the env
// they started with, and each new entry reads the file as written for it.
//
// WHETHER THE JAIL CAN TAKE IT is not decided here any more. attachExisting's contract gate
// (contracttags.go) runs first and calls this only for a jail that has every contract tag the
// delivery needs. It replaced two splits that lived here, both keyed on whether a selection
// was TYPED: the pre-change jail's (a frozen YOLO_PROVIDERS, OQ-CS6) and the pre-gate jail's
// (no per-agent env marker, CN-D18). Each refused a typed '-p', and for a config-only
// selection warned and proceeded without delivery. That second half was the silent
// ride-along the attach-skew ruling forbids (OQ-SK1), so both arms now take the gate's
// disposition, typed or not: the acknowledgment, a restart prompt, or a refusal. The plain
// re-entry into a pre-change jail with its own selection, or none, is the gate's standIn: no
// delivery, no refusal, as before.
//
// On a jail that can take it the delivery runs unless a pre-flight refuses it: the
// env-override check, then the CREDENTIAL PRE-FLIGHT — the same checks the fresh
// path runs (§6.2, OQ-13), the second of whose attach exemption existed because
// "attaching to a running jail delivers no environment". Both run BEFORE the write,
// so a refused attach leaves the live file as the previous entry wrote it.
// A session that would start with a base URL and no token is the
// mysterious-first-API-call failure the gate refuses elsewhere;
// YOLO_ALLOW_MISSING_PROVIDERS=1 remains the loud hatch.
func (o *Options) deliverChannelOnAttach(cname, rt string, cfg *jsonx.OrderedMap,
	staged stagedPacks, channel *packChannel) int {
	// The two pre-flights run BEFORE the write, not after it. The file is the RUNNING
	// jail's, live-mounted: every new shell and agent process in it sources what was last
	// written. A refusal after the write would have told this entry "no" while already
	// handing the live jail the refused channel — for the override check, the pack's
	// variable beside what overrides it; for the credential check, a provider with no
	// token. Refusing first leaves the file as the previous entry wrote it.
	if lines := o.checkEnvOverrides(cfg, rt, staged.packs, channel, nil); len(lines) > 0 {
		o.printProviderRefusal(lines)
		return 1
	}
	o.notePlatformSwitchConflicts(staged.packs, channel)
	if lines, refuse := o.checkProviderCredentials(cfg, staged.packs, channel, nil); len(lines) > 0 {
		o.printProviderRefusal(lines)
		if refuse {
			return 1
		}
	}
	// The SAME write the fresh path performs (run.go's lifecycle phase): one
	// composition, one writer (deliverChannel), the shared file the boot hydrates and
	// every shell sources plus each agent's own env file its launcher sources. What
	// this entry did not compose is revoked by the rewrite — including a previous
	// entry's shape vars and a deselected agent's whole file, which an override-only
	// channel would have left behind. Both binds are live; no argv changes.
	deliverChannel(paths.WorkspaceHomeState(o.Workspace), rt, channel)
	o.noteCredentialScope(channel)
	// WHERE THE SELECTIONS LANDED, on this arm too — the disclosure line the fresh
	// path prints beside its banner. An attach that delivers a profile owes the same
	// sentence; providers.md#pv-oq-10's rule (never "honored") travels with it.
	o.noteUseProfiles(channel, staged.packs, nil)
	return 0
}

// envLineValue returns the value of KEY= in a container-inspect env listing, or "".
func envLineValue(envLines []string, key string) string {
	for _, l := range envLines {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	return ""
}

// frozenUseProfiles decodes the YOLO_USE_PROFILES a PRE-CHANGE jail froze into its
// container environment at launch — this entry's launch-time selection, the baseline
// the attach's own table is compared against. nil when absent or unparseable (an
// empty table and no table both compare as "selects nothing").
func frozenUseProfiles(envLines []string) *jsonx.OrderedMap {
	raw := envLineValue(envLines, "YOLO_USE_PROFILES")
	if raw == "" {
		return nil
	}
	v, err := jsonx.Decode([]byte(raw))
	if err != nil {
		return nil
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// profileTablesEqual compares two selection tables as KEY→string sets: the tables
// cross as JSON objects whose ORDER is writer-dependent and meaning-free, so byte
// equality would refuse equal tables over a reordered key. A nil side selects
// nothing, same as an empty one.
func profileTablesEqual(a, b *jsonx.OrderedMap) bool {
	sizes := func(m *jsonx.OrderedMap) int {
		if m == nil {
			return 0
		}
		return m.Len()
	}
	if sizes(a) != sizes(b) {
		return false
	}
	if a == nil || b == nil {
		return sizes(a) == sizes(b)
	}
	for _, k := range a.Keys() {
		av, _ := a.Get(k)
		bv, ok := b.Get(k)
		if !ok {
			return false
		}
		// Compared as ACTIVE SETS (docs/design/active-provider-sets.md), so two lists are equal
		// only entry for entry, in order, and a list of one equals the string it canonicalizes to.
		as, aok := packload.ProfileSetValue(av)
		bs, bok := packload.ProfileSetValue(bv)
		if aok != bok || strings.Join(as, ",") != strings.Join(bs, ",") {
			return false
		}
	}
	return true
}

// detectHostTZ resolves the host timezone for the TZ env (or "").
func detectHostTZ() string {
	if tz, ok := storage.DetectHostTimezone(); ok {
		return tz
	}
	return ""
}

// runtimeWriteTracking wraps runtime.WriteContainerTracking with the resolved
// workspace path.
func runtimeWriteTracking(cname, workspace string) error {
	resolved := resolvePath(workspace)
	return writeTracking(cname, resolved)
}

// emitLaunchBanner writes the launch line to stderr (audit §B#4), continuing the
// startup banner internal/cli's dispatch already wrote. jailVersion is the
// container's baked YOLO_VERSION (attach path only, else "").
//
// YOLO_NO_BANNER does NOT silence this line. The hatch turns off the startup
// banner — the thing this change added to every command — and a user who set it
// to keep a wrapper's stderr clean did not ask to lose the container name a
// launch has always printed.
func (o *Options) emitLaunchBanner(rt, cname string, resParts []string, jailVersion string) {
	// Resolve the repo root via the shared method (o.RepoRoot → reporoot.Resolve),
	// so the version this compares jailVersion against matches run/check and
	// describes the yolo-jail repo, not whatever repo the cwd happens to sit in.
	// "" → version.Get falls back to the baked stamp / "unknown".
	repoRoot := ""
	if o.RepoRoot != nil {
		if rr, ok := o.RepoRoot(); ok {
			repoRoot = rr.Root
		}
	}
	banner := LaunchBanner(rt, cname, version.Get(repoRoot), jailVersion, resParts)
	// Fprintln, not Fprint: LaunchBanner returns no trailing newline, so the old
	// Fprint left the cursor mid-line and whatever printed next was glued onto the
	// banner ("…pids=32768No packs are configured…", observed in a nested launch).
	// It was invisible before only because the next writer happened to be the
	// container's own output, which opens with its own newline.
	fmt.Fprintln(o.Stderr, banner)
}

// inspectContainerEnv reads a running container's whole frozen environment via
// `<rt> inspect`, one entry per line, or nil when the inspect cannot run. The
// attach path's single source for both the banner's baked version and the contract
// gate's tags (contracttags.go).
//
// APPLE CONTAINER'S INSPECT TAKES NO --format: it answers a JSON document, which
// internal/cli's ps and check already read that way. The podman template used to go to
// every runtime, so on Apple Container this read nothing, and the gate, which reads an
// empty listing as "cannot prove, treat as current", let an older jail there receive a
// scoped delivery its launchers never source. runtime.EnvFromContainerInspectJSON reads the
// measured payload (docs/plans/setup-support-gaps.md §5.1 row 5) into the same lines.
func (o *Options) inspectContainerEnv(rt, cname string) []string {
	if o.Exec == nil {
		return nil
	}
	if rt == "container" { // parity: Honored — AC's inspect answers JSON and takes no --format; the same env lines come back
		res := o.Exec([]string{"container", "inspect", cname}, "", nil, 3*time.Second)
		if !res.Ran || res.RC != 0 {
			return nil
		}
		env, _ := runtime.EnvFromContainerInspectJSON(res.Stdout)
		return env
	}
	res := o.Exec([]string{rt, "inspect", "--format", "{{range .Config.Env}}{{println .}}{{end}}", cname}, "", nil, 3*time.Second)
	if !res.Ran || res.RC != 0 {
		return nil
	}
	return strings.Split(res.Stdout, "\n")
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

// refuseUnbuiltNotch stops a launch whose notch is anything but `jail`, and reports the
// exit code and whether it did.
//
// TWO INPUTS, ONE JUDGEMENT. The notch is the config's `confinement` key, overridden by
// `--at <notch>` as typed on this launch (Options.Notch). Both were accepted and neither
// was acted on: DP-B16 is the config half, DP-B22 the flag half, and they are one gate
// because a launch that refused the key while ignoring the flag would still let
// `yolo --at guest -- <cmd>` run in a container.
//
// WHAT IT FIXES (docs/design/declaration-parity.md DP-B16, ruled by OQ-DP3): `guest` and
// `host` were ACCEPTED here, validated by config.validateConfinement, printed by `yolo
// describe` — and never dispatched on. A container started regardless, and then the
// briefing told the agent the opposite of what had happened: at `guest`, *"a restricted
// account on the real machine, NOT a disposable container … your home is real and
// persists"* (jailcontent.confinementHeader), every sentence of it false of the container
// it was rendered into. `yolo apply` refused the identical value with rc 1 the whole time,
// so one config key meant two things depending on which verb read it.
//
// REFUSE, NOT HONOR, and not a warning either. Honoring the notch IS env-manager plan
// Phase 7 (the LSM-confined backend) — this gate is the ~10 lines that stop the notch
// LOOKING built while that is unwritten. A warning was the weaker option and was not
// taken: OQ-BP-3 (docs/design/backend-parity.md) is live and says *"a warning people learn
// to skip is worse than none"*, and a warned launch still hands the agent the contradicting
// briefing.
//
// ⚠ WHY REFUSING A CONFIG KEY IS SAFE HERE, when docs/design/declaration-parity.md §10
// explicitly declines to refuse keys a mechanism has always tolerated (`gpu` on macOS, and
// the rest): `confinement` is not a mechanism-varying key. It resolves to the same value on
// every platform and every backend and is enforced by nothing anywhere, so a shared config
// carried between a Linux box and a Mac loses nothing to this refusal.
//
// The guest sentence is render.NotchUnbuilt's, VERBATIM — `yolo apply --at guest` has
// printed it since Phase 2 and the two must not drift (see that function).
func refuseUnbuiltNotch(o *Options, cfg *jsonx.OrderedMap) (int, bool) {
	notch := config.ResolveConfinement(cfg)
	if o.Notch != "" {
		// VALIDATED HERE, not in the parser: config.ResolveConfinement answers `jail`
		// for a value it does not know (validateConfinement is what reports it), so a
		// typo'd `--at gest` would silently launch a jail — an override that failed
		// OPEN, which is the shape `yolo apply --at` already refuses with rc 2. Same
		// code, same vocabulary, so the two spellings of the flag agree.
		asked := config.Confinement(o.Notch)
		known := false
		for _, k := range config.KnownConfinements {
			if asked == k {
				known = true
			}
		}
		if !known {
			o.pr(o.Stderr).printf("[bold red]yolo: --at %q is not a confinement level "+
				"(jail|guest|host)[/bold red]", o.Notch)
			return 2, true
		}
		notch = asked
	}
	// WHERE THE VALUE CAME FROM, said in the refusal. A user who typed `--at guest` and
	// is told to edit `confinement` will go looking for a key they never wrote; a user
	// whose config carries it and is told to drop a flag will not find the flag. The
	// remedy has to name the thing the reader can actually change.
	source := "`confinement` in your config"
	fix := "Set it to \"jail\" (the default), or leave the key out entirely"
	if o.Notch != "" {
		source = "the `--at " + o.Notch + "` you typed"
		fix = "Drop the flag (or pass `--at jail`) to launch this workspace's jail"
	}
	switch notch {
	case config.ConfinementGuest:
		o.pr(o.Stderr).printf("[bold red]Refusing to launch: %s[/bold red]",
			render.NotchUnbuilt("launch"))
		o.pr(o.Stderr).printf("[dim]The notch is %s, and yolo validates it — which is why "+
			"this reads as a refusal rather than a typo. %s; `yolo describe` prints what "+
			"each notch would compose.[/dim]", source, fix)
		return 1, true
	case config.ConfinementHost:
		// The host notch is BUILT — it simply is not something a launch does. `yolo host
		// -- <cmd>` is its exec verb and `yolo apply --at host` renders into the real
		// home, so this refusal names them instead of borrowing the guest sentence: a
		// notch with two working verbs is not "not built yet".
		o.pr(o.Stderr).printf("[bold red]Refusing to launch: the host notch (%s) means no "+
			"jail around this process, and a launch is the one verb that cannot honor "+
			"it — every backend puts the command inside a sandbox.[/bold red]", source)
		o.pr(o.Stderr).printf("[dim]The host notch has its own verbs: `yolo host -- <cmd>` "+
			"runs the command on the real machine with the composed environment, and "+
			"`yolo apply --at host` renders your config into your real home. %s.[/dim]", fix)
		return 1, true
	}
	return 0, false
}

// packSurfacePaths is every destination the loaded packs compose, for the two-writers refusal.
//
// A pack whose surfaces do not resolve contributes NOTHING rather than failing the launch: its own
// problems are reported on their own path, and a pack that cannot say what it composes cannot be
// shown to collide with anything. Erring the other way would turn an unrelated pack defect into a
// host_files error.
func packSurfacePaths(packs []*packload.Pack) []string {
	var out []string
	for _, p := range packs {
		if p == nil {
			continue
		}
		surfaces, probs := p.Surfaces()
		if len(probs) > 0 {
			continue
		}
		for _, sf := range surfaces {
			out = append(out, sf.Path)
		}
	}
	return out
}

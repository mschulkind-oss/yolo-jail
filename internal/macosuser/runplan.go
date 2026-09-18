package macosuser

import (
	"sort"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/provision"
	"github.com/mschulkind-oss/yolo-jail/internal/runtime"
)

// RunPlan is the fully-resolved, ordered artifacts + commands for one session.
// real gate rather than a pretty-printer.
type RunPlan struct {
	Workspace   string
	Cname       string
	ProfilePath string
	Seatbelt    string
	// StagedDir is the root-owned state dir; StagedYolo is the staged yolo
	// binary the sandbox self-execs. StageCommands stage that binary
	// (fresh-inode copy).
	StagedDir     string
	StagedYolo    string
	StageCommands [][]string
	// PackRoot is the root-owned staged pack tree this session's bootstrap renders
	// from (YOLO_PACK_ROOT in BootstrapArgv), or "" when the launch staged no packs.
	PackRoot string
	// CtxRoot is the root-owned staged CONTEXT tree this session's bootstrap reads host
	// bytes out of (YOLO_CTX_ROOT in BootstrapArgv), or "" when the host CLI composed
	// none. Absence is the honest way to say "this launch carried no host bytes"; it is
	// also what makes the host-layer report say `unsupported`, so the two can never
	// disagree about whether this backend delivered.
	CtxRoot       string
	BootstrapArgv []string
	// ProvisionArgv is the CONFINED provisioning stage, run between the bootstrap and
	// the agent — nil when this config gives it nothing to do (ProvisionNeeded), which
	// is what makes `yolo -- bash` in a tool-less workspace pay nothing for it.
	// ProvisionScriptPath is the generated script that argv execs; both are "" together.
	ProvisionArgv       []string
	ProvisionScriptPath string
	LaunchArgv          []string
	// EnvFile is the per-session, root-owned 0600 file carrying everything this launch
	// COMPOSED — git identity, TERM, the profile/provider channel, the hydrated
	// env_sources. The three sandboxed argvs above name it and read it; none of them
	// carries its values (envfile.go states why, and PlanInvariants checks it).
	// EnvFileContent is what to write into it; the two are "" together.
	//
	// EnvFileCommands prepare the 0700 directory and MUST run BEFORE the write;
	// EnvFileGrantCommands add the sandbox account's read ACE and MUST run after;
	// EnvFileRemoveCommands sweep it when the session ends.
	EnvFile               string
	EnvFileContent        string
	EnvFileCommands       [][]string
	EnvFileGrantCommands  [][]string
	EnvFileRemoveCommands [][]string
	GitIdentity           *jsonx.OrderedMap
	OffendingHome         string // "" when on neutral ground
	OffendingHomeSet      bool   // true when a home contains the workspace
	DarwinPathPrefix      []string
	DarwinEnv             *jsonx.OrderedMap
	DarwinSkipped         []string
	DarwinMaterialized    bool
}

// HostContext is what the HOST CLI composed for this launch's `/ctx` delivery: the tree
// of host bytes, and the record of what went into it. It is the macos-user answer to
// "the bytes cross on a /ctx mount and this backend has no mounts" — the mechanism is a
// COPY (docs/design/declaration-parity.md DP-L1 / §6.1, ruled by OQ-DP4).
//
// ⚠ EVERY FIELD IS COMPOSED BY THE CALLER AND NEVER BY THIS PACKAGE, and that is a
// credential-boundary constraint rather than a layering preference. Filling it means
// reading the invoking user's own config (config.LoadHostFiles reads ~/.config/yolo-jail
// DIRECTLY, which is what makes a source-bearing entry user-scope-only) and stat'ing
// paths in the invoking user's home. The plan builder is PURE — the dry-run plan must
// touch no disk — so the read lives in the host CLI's run pipeline
// (internal/cli/run/macosctxtree.go) and only its RESULT crosses here. OQ-DP4's ledger
// row states this in as many words and the ruling does not override it.
//
// The zero value is a launch that carried no host bytes, which is exactly the state
// every macos-user launch was in before DP-L1: no tree to stage, no YOLO_CTX_ROOT, and
// a host-layer report of `unsupported`.
type HostContext struct {
	// Tree is the host-side root the caller composed, laid out at the /ctx-relative
	// paths the jail reads (`host-<staged slug>/<basename>` for a pack `reads-host`
	// grant, `host-user/<slug>` for a source-bearing `host_files` entry). "" means the
	// caller composed nothing.
	//
	// It crosses as a TREE rather than as a mapping for macoshomeoverlay.go's reason:
	// the container path's mapping lives in its mount list, and re-sending it as data
	// would put one mapping in two implementations. Laying it out by DESTINATION makes
	// delivery one `cp -R` and one env var — the paths ARE the manifest.
	Tree string
	// Delivered is the /ctx destination (packload.CtxPath) of every pack `reads-host`
	// grant whose bytes are in Tree. It becomes the launcher's half of the jail's
	// FAIL-CLOSED host-layer read (packload.HostLayerReport): the jail cannot tell "the
	// user has no such file" from "it did not arrive", so the launcher says which.
	//
	// It is the caller's list rather than a walk of Tree on purpose. A walk would make
	// the jail's check tautological — it would re-derive the same answer from the same
	// bytes — and the bug that read is for is precisely a host side that wrote the
	// right file at the WRONG path (measured 2026-09-05, a pack whose staged directory
	// name was escaped).
	Delivered []string
	// HostFiles are the user's SOURCE-BEARING `host_files` entries this launch RESOLVED —
	// additive to the source-less ones the plan builder reads from the merged config,
	// which is the only half a pure function may see.
	//
	// ⚠ RESOLVED, not "delivered", and the difference is deliberate. An entry whose host
	// file does not exist yet belongs here: the destination then renders from its
	// `defaults`/`content` layers, which is what every other backend does (the container
	// emits the entry and skips only the bind). Only the BYTES are conditional on the
	// source existing; the declaration crosses either way.
	//
	// Directory-shaped entries are NOT here and must not be: a copy does not scale to
	// an arbitrary user-named tree, which is why the directory-shaped cells stayed with
	// DP-D15 (refuse) rather than joining DP-L1 (deliver).
	HostFiles []config.HostFileEntry
}

// Darwin carries the already-materialized native `packages:` result threaded
// into a RunPlan
// plan builder stays pure — the nix build happened in the caller). A nil
// *Darwin means "not materialized".
type Darwin struct {
	PathPrefix []string
	Env        *jsonx.OrderedMap
	Skipped    []string
	// System is the nix system double the materialization actually resolved
	// against (e.g. "aarch64-darwin"). Carried on the RESULT rather than read
	// from a constant here so the skip message names the real target: on an
	// Intel Mac a skip is an x86_64-darwin fact, and this package must not
	// acquire a darwinpkg import to say so — the whole point of the injected
	// MaterializeDarwin seam is that macosuser stays free of that dependency.
	System string
	// ProfilePath is the buildEnv store out path (PathPrefix is <it>/bin). The
	// GC-rooted closure the agent's tools come from.
	ProfilePath string
}

// darwinSystemLabel is the system double for a skip message, falling back to the
// generic word when the materializer did not report one — a message reading "no
// build" is honest where one reading "no aarch64-darwin build" on an Intel Mac is
// not, so an unset System degrades rather than guessing.
func darwinSystemLabel(d *Darwin) string {
	if d == nil || d.System == "" {
		return "native"
	}
	return d.System
}

// DarwinBootstrapArgv returns the self-exec bootstrap argv (J2 §3): run the
// staged yolo binary AS the sandbox user via `sudo --user=<sb> /usr/bin/env -i
// K=V… <stagedYolo> internal darwin-bootstrap`.
//
// The env is baked onto the argv the same way LaunchArgv bakes the launch env
// (env -i K=V…; secrets normally ride ${VAR} placeholders).
// HOME/JAIL_HOME point the entrypoint generators at the sandbox
// home; the generator contract (git identity + YOLO_*) and the three
// YOLO_DARWIN_* extras (workspace, macos-log, login-path) ride verbatim. No
// --set-home: the subcommand self-sets HOME/JAIL_HOME, and env -i controls the
// environment precisely.
func DarwinBootstrapArgv(stagedYolo, home string, bootstrapEnv *jsonx.OrderedMap, user string) []string {
	if user == "" {
		user = SandboxUser
	}
	if home == "" {
		home = SandboxHome()
	}
	protected := map[string]struct{}{"HOME": {}, "JAIL_HOME": {}}
	envPairs := []string{
		"HOME=" + home,
		"JAIL_HOME=" + home,
	}
	if bootstrapEnv != nil {
		for _, k := range bootstrapEnv.Keys() {
			if _, ok := protected[k]; ok {
				continue
			}
			v, _ := bootstrapEnv.Get(k)
			envPairs = append(envPairs, k+"="+asStr(v))
		}
	}
	out := []string{
		"sudo",
		"--user=" + user,
		"/usr/bin/env",
		"-i",
	}
	out = append(out, envPairs...)
	out = append(out, stagedYolo, "internal", "darwin-bootstrap")
	return out
}

// BuildRunPlan assembles the full RunPlan (pure — no shelling out). `config` is
// the loaded jail config; `sandboxEnv` is the fully-resolved launch env;
// `selfExe` is the running yolo binary (os.Executable()) staged for the sandbox
// to self-exec as the bootstrap; `hostPackRoot` is the host-side staged pack tree
// the run pipeline produced before dispatching here (""=no packs); `hostHomeOverlay`
// is the host-side composed CONTENT tree — skills and briefings, already laid out at
// their home-relative destinations (""=nothing to deliver); `hostCtx` is the host-side
// composed CONTEXT tree and the record of what the host CLI put in it (HostContext, and
// see it for why this package may not compose one itself); `blockedTools` are the
// selected packs' own blocked-tool declarations, merged with the config's security
// section (core blocks nothing by default). `darwin` may be nil.
func BuildRunPlan(workspace string, cfg *jsonx.OrderedMap, agents, agentArgv []string, selfExe, hostPackRoot, hostHomeOverlay string, hostCtx HostContext, sandboxEnv *jsonx.OrderedMap, darwin *Darwin, blockedTools []packload.BlockedTool) RunPlan {
	// SYMLINK-RESOLVED ONCE, HERE, BECAUSE THE KERNEL RESOLVES BEFORE THE POLICY IS CONSULTED.
	// Measured on hardware 2026-09-13 (declaration-parity.md §6.1's probe 2): a profile denying
	// `(subpath "/tmp")` does not stop `touch /tmp/canary`, while one denying
	// `(subpath "/private/tmp")` does. So an SBPL rule naming an unresolved path matches
	// NOTHING, and `SeatbeltProfile` used to be handed `workspace` raw while `YOLO_HOST_DIR`
	// (:341) and `MISE_TRUSTED_CONFIG_PATHS` (orchestrator.go) both resolved it. A workspace
	// reached through a symlink therefore got a profile whose workspace rules were dead — no
	// writes, no reads, fail-closed and confusing.
	//
	// ⚠ AND THE SAME LINE CLOSES A POLICY BYPASS, which is why it is one assignment rather than
	// a fix at the profile call site. `HomeContaining` below decides the neutral-ground refusal
	// (DP-D15: the agent shares only neutral ground, never a path inside your home), and it is
	// only as good as the spelling it is given: `/Users/Shared/yolo/link` → `/Users/matt/proj`
	// passed it, measured through a real `--dry-run`. The dead profile was all that stood
	// between that and a live grant into the invoking user's home, so resolving for the profile
	// ALONE would have unmasked it. Both consumers read this one value.
	//
	// Go's os.Getwd honours $PWD when it stats to the same inode, so the launcher really does
	// receive a shell's logical spelling — this is reachable from an ordinary `cd`, not only
	// from an argument somebody constructed.
	workspace = resolvePathAbs(workspace)
	darwinPrefix := []string{}
	darwinEnv := jsonx.NewOrderedMap()
	darwinSkipped := []string{}
	if darwin != nil {
		darwinPrefix = append([]string{}, darwin.PathPrefix...)
		if darwin.Env != nil {
			for _, k := range darwin.Env.Keys() {
				v, _ := darwin.Env.Get(k)
				darwinEnv.Set(k, v)
			}
		}
		darwinSkipped = append([]string{}, darwin.Skipped...)
	}
	// Merge non-PATH darwin build vars into the launch env (the store PATH rides
	// the separate path_prefix channel); darwin vars win on conflict.
	if darwinEnv.Len() > 0 {
		merged := jsonx.NewOrderedMap()
		if sandboxEnv != nil {
			for _, k := range sandboxEnv.Keys() {
				v, _ := sandboxEnv.Get(k)
				merged.Set(k, v)
			}
		}
		for _, k := range darwinEnv.Keys() {
			v, _ := darwinEnv.Get(k)
			merged.Set(k, v)
		}
		sandboxEnv = merged
	}

	// THE SAME TWO LSP INSTALL LISTS, into the launch env — which is how they reach the
	// CONFINED PROVISIONING STAGE, the process that actually runs the install.
	//
	// ⚠ TWO ENV LISTS, NOT ONE, and setting either alone is a green-looking launch that
	// still installs nothing: the readers live in different processes. The generated
	// script's install loop (entrypoint/shell.go) runs in the STAGE, and the stage's
	// environment is the session env file (ProvisionArgv carries only the identity quartet
	// and the file's name); the catalog and refresh readers run under the BOOTSTRAP env
	// buildBootstrapEnv composes. Both are resolved from one `config.LSPInstalls(cfg)` per
	// channel, so the stage cannot be told to install a different set than the bootstrap
	// was told to expect — and PlanInvariants fails if one of the two crossings is deleted.
	//
	// THE ENV FILE RATHER THAN THE STAGE ARGV, because these are values COMPOSED from the
	// user's config and that is the closed rule this backend's argvs are under
	// (SandboxArgvEnvProblems' allowlist: a new composed variable fails that check by
	// existing, which is what keeps the next secret off a world-readable command line).
	// Set only when there is something to install, so a workspace that declares no LSP
	// server still composes nothing and still pays for no env file at all.
	if npm, goPkgs := config.LSPInstalls(cfg); npm != "" || goPkgs != "" {
		merged := jsonx.NewOrderedMap()
		if sandboxEnv != nil {
			for _, k := range sandboxEnv.Keys() {
				v, _ := sandboxEnv.Get(k)
				merged.Set(k, v)
			}
		}
		// Both names, together, even when one list is empty: they are a pair, and a launch
		// that carried one of them would be the half-fix this comment exists to prevent.
		merged.Set("YOLO_LSP_NPM_INSTALL", npm)
		merged.Set("YOLO_LSP_GO_INSTALL", goPkgs)
		sandboxEnv = merged
	}

	cname := cnameFor(workspace)
	profilePath := SessionProfilePath(cname, "")

	// Git identity = the sandbox-env keys prefixed YOLO_GIT.
	gitIdentity := jsonx.NewOrderedMap()
	if sandboxEnv != nil {
		for _, k := range sandboxEnv.Keys() {
			if strings.HasPrefix(k, "YOLO_GIT") {
				v, _ := sandboxEnv.Get(k)
				gitIdentity.Set(k, v)
			}
		}
	}

	// THE TWO STAGED TREES the bootstrap renders from, resolved here from their host-side
	// counterparts and handed to buildBootstrapEnv, which turns each into the env var that
	// tells the bootstrap it exists. Each is named only when the launch actually staged
	// one, so a launch that staged nothing says so by ABSENCE rather than by naming a
	// directory that is not there — see buildBootstrapEnv for what each does, and
	// StageCommands below for the copies that put them there.
	packRoot := ""
	if hostPackRoot != "" {
		packRoot = StagedPackRoot(cname, "")
	}
	homeOverlay := ""
	if hostHomeOverlay != "" {
		homeOverlay = StagedHomeOverlay(cname, "")
	}
	// AND THE THIRD, on the same rule: the CONTEXT tree (DP-L1). The host CLI composed it
	// — it is the only half that may read the invoking user's config and home — and this
	// resolves where it will land. "" when the caller composed nothing, which is what the
	// host-layer report below turns into `unsupported`, so a launch that delivered no host
	// bytes and a backend that cannot deliver them remain the same statement.
	ctxRoot := ""
	if hostCtx.Tree != "" {
		ctxRoot = StagedCtxRoot(cname, "")
	}
	// THE WORKSPACE SIDECAR — <workspace>/.yolo/home, the same directory the podman argv
	// binds the jail home's per-workspace dirs from (paths.WorkspaceHomeState, one spelling
	// for both backends). Naming it is what turns the tier collapse off: the bootstrap
	// symlinks the account home's per-workspace dirs into it
	// (entrypoint.InstallDarwinHomeLayout). A capture passes none — see the parameter.
	bootstrapEnv := buildBootstrapEnv(workspace, cfg, gitIdentity, sandboxEnv, packRoot,
		homeOverlay, ctxRoot, hostCtx, paths.WorkspaceHomeState(workspace), SandboxHome(),
		darwinPrefix, blockedTools)

	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(workspace, "")

	// THE PROVISIONING STAGE (docs/design/macos-user-provisioning.md half two), composed
	// here so the dry-run plan shows it and PlanInvariants can check it — the two things
	// a step assembled inside the orchestrator would be invisible to.
	//
	// Skipped ENTIRELY, argv and all, when the config declares nothing for it. Absence is
	// the honest representation: a plan carrying a stage the launch will not run describes
	// a launch nobody performs, and every invariant below is written to say nothing about
	// an empty argv rather than to demand one.
	// THE SESSION ENV FILE, resolved before the two argvs that read it. It is named
	// whenever this launch composed anything at all — a workspace with no env_sources, no
	// profile and no git identity composes an empty map, and then there is no file, no
	// directory to prepare and no wrapper on either argv.
	envFile := ""
	envFileContent := SandboxEnvFileContent(sandboxEnv)
	if envFileContent != "" {
		envFile = SandboxEnvFile(cname, "")
	}

	var provisionArgv []string
	provisionScriptPath := ""
	if ProvisionNeeded(cfg) {
		provisionScriptPath = ProvisionBootstrapScript(workspace)
		provisionArgv = ProvisionArgv(ProvisionScript(workspace, provisionScriptPath),
			profilePath, envFile, workspace, "", "", darwinPrefix)
	}

	stageCommands := append([][]string{}, StageBinaryCommands(selfExe, "")...)
	stageCommands = append(stageCommands, StagePackCommands(hostPackRoot, cname, "")...)
	stageCommands = append(stageCommands, StageHomeOverlayCommands(hostHomeOverlay, cname, "")...)
	stageCommands = append(stageCommands, StageCtxCommands(hostCtx.Tree, cname, "")...)
	stageCommands = append(stageCommands, endpointGrantCommands(sandboxEnv)...)

	return RunPlan{
		Workspace:   workspace,
		Cname:       cname,
		ProfilePath: profilePath,
		Seatbelt:    SeatbeltProfile(workspace, SandboxHome(), cfgStrList(cfg, "workspace_readonly")),
		StagedDir:   stateDir,
		StagedYolo:  stagedYolo,
		// Binary first, then the pack trees, then the content overlay, then the context
		// tree: all four are prerequisites of the bootstrap the caller runs immediately
		// after this list, and the binary is the one that fails most cheaply.
		StageCommands:       stageCommands,
		PackRoot:            packRoot,
		CtxRoot:             ctxRoot,
		BootstrapArgv:       DarwinBootstrapArgv(stagedYolo, SandboxHome(), bootstrapEnv, ""),
		ProvisionArgv:       provisionArgv,
		ProvisionScriptPath: provisionScriptPath,
		LaunchArgv:          LaunchArgv(agentArgv, profilePath, envFile, workspace, "", "", darwinPrefix),

		EnvFile:               envFile,
		EnvFileContent:        envFileContent,
		EnvFileCommands:       SandboxEnvDirCommands(envFile, ""),
		EnvFileGrantCommands:  SandboxEnvGrantCommands(envFile, ""),
		EnvFileRemoveCommands: SandboxEnvRemoveCommands(envFile),

		GitIdentity:        gitIdentity,
		OffendingHome:      offendingHome,
		OffendingHomeSet:   offendingSet,
		DarwinPathPrefix:   darwinPrefix,
		DarwinEnv:          darwinEnv,
		DarwinSkipped:      darwinSkipped,
		DarwinMaterialized: darwin != nil,
	}
}

// endpointGrantCommands is the cross-uid grant for EVERY published host-service endpoint this
// launch carries, read off the launch env rather than named one service at a time.
//
// IT USED TO NAME ONE VARIABLE, `YOLO_SERVICE_OPENAI_AUTH_BROKER_ENDPOINT`, because the
// macos-user arm started exactly one host service. The arm goes through the whole lifecycle
// now (internal/cli/run/run.go), so a hardcoded name would deliver the credential broker's
// endpoint and silently withhold every other one — and a withheld grant is not a missing
// feature but a service the sandbox is TOLD about and cannot open, which is the worst of the
// three states available.
//
// THE ENV IS THE MANIFEST, for macoshomeoverlay.go's reason one layer over: the run pipeline
// already decided which services published and wrote one variable per published endpoint, so
// re-deriving the set here would be a second selector over the same launch. paths' own prefix
// and suffix are the discriminator, never a literal — the producer (hostServiceEnvVar) and
// this consumer must not be able to drift apart by a re-typing.
//
// ⚠ ENDPOINT FILES ONLY, never the `_SOCKET` spelling, and the exclusion is about the ACE
// rather than about tidiness: a Unix socket needs WRITE to connect(2) and EndpointGrantCommands
// grants READ (loophole-transport.md OQ-T5), so a socket handed to it would be granted an
// access that cannot be used. No shipped loophole reaches that state on a Mac — the one
// socket-transport service left is the cgroup delegate, which is Linux-only — so this is a
// boundary being stated before it is crossed, not one being worked around.
//
// DEDUPED ON THE WHOLE COMMAND, because every endpoint of one launch lives in the SAME
// per-jail services dir, so the directory's search ACE repeats once per service. A stage
// command's failure is FATAL to the launch (orchestrator.go), so a repeated `chmod +a` is not
// a cosmetic duplicate. Deduping the emitted argv rather than the parent path is what keeps
// this from re-deriving what EndpointGrantCommands decided to emit.
func endpointGrantCommands(sandboxEnv *jsonx.OrderedMap) [][]string {
	if sandboxEnv == nil {
		return nil
	}
	var out [][]string
	seen := map[string]bool{}
	for _, k := range sandboxEnv.Keys() {
		if !isServiceEndpointVar(k) {
			continue
		}
		value, _ := sandboxEnv.Get(k)
		endpoint, ok := value.(string)
		if !ok || endpoint == "" {
			continue
		}
		for _, cmd := range EndpointGrantCommands(endpoint, "") {
			key := strings.Join(cmd, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, cmd)
		}
	}
	return out
}

// isServiceEndpointVar reports whether a variable name is a host service's ENDPOINT-FILE
// variable — the producer's own two halves (paths.ServiceEnvVarPrefix/Suffix), never a
// literal, so the grant here and the emission in internal/cli/run cannot drift apart by a
// re-typing.
//
// The length guard rejects the one degenerate name the two halves overlap on
// (`YOLO_SERVICE_ENDPOINT`), which no service can produce and which would otherwise be read
// as an endpoint with an empty name.
func isServiceEndpointVar(key string) bool {
	return len(key) > len(paths.ServiceEnvVarPrefix)+len(paths.ServiceEnvVarSuffix) &&
		strings.HasPrefix(key, paths.ServiceEnvVarPrefix) &&
		strings.HasSuffix(key, paths.ServiceEnvVarSuffix)
}

// stageCommandsNameEnvValue reports whether some staged command names the exact value the
// rendered env file exports for key.
//
// It offers each ARGUMENT back to the file's own renderer rather than cutting a value out of
// the file, so the question is "did the thing the sandbox will read get staged" asked through
// one notion of the file's syntax — the reason SandboxEnvFileSets exists at all.
func stageCommandsNameEnvValue(cmds [][]string, content, key string) bool {
	for _, cmd := range cmds {
		for _, arg := range cmd {
			if SandboxEnvFileSets(content, key, arg) {
				return true
			}
		}
	}
	return false
}

// buildBootstrapEnv composes the env baked onto the `yolo internal darwin-bootstrap` self-exec
// argv: the generator contract the entrypoint reads
// (YOLO_HOST_DIR/BLOCK_CONFIG/MISE_TOOLS/LSP/MCP/HOST_FILES/PACK_ROOT), the git identity, the
// two provider/profile wire tables, and the four YOLO_DARWIN_* extras the subcommand consumes
// (workspace, macos-log mode, login-rc PATH, home overlay). Reuses the container-side resolvers.
//
// `home` is the home the bootstrap will generate INTO, and it is a parameter rather than
// SandboxHome() because an install capture bootstraps a THROWAWAY STAGING HOME instead
// (capture.go): it needs the same launchers, shims and pack surfaces a launch would produce —
// the capture runs the generated launcher, not a second implementation of the install — but
// generated against the home the capture is about to run in. It is used for the login-rc PATH;
// the HOME/JAIL_HOME pair is baked by DarwinBootstrapArgv, which takes the same value.
//
// `packRoot`, `homeOverlay` and `ctxRoot` are the ALREADY-STAGED destinations
// (StagedPackRoot, StagedHomeOverlay, StagedCtxRoot), not their host-side sources, and ""
// means the caller staged nothing of that kind. They are resolved by the caller rather than
// here because the caller is also what emits the commands that stage them, and the two must
// not be able to disagree.
//
// `hostCtx` is the caller's RECORD of what went into that tree (HostContext). Two variables
// read it — the host-layer report and the source-bearing half of YOLO_HOST_FILES — and both
// are statements about delivery, so neither may be derived from the config: a wire naming a
// host file nobody staged is the silent-wrong-composition this whole path exists to end.
//
// `homeSidecar` is <workspace>/.yolo/home, the per-workspace tier the bootstrap symlinks the
// account home into. "" means LAY NO LAYOUT, and the caller that passes it is the install
// capture: its home is a throwaway staging tree whose whole contract is that everything an
// installer writes lands under it, and its delta walk does not follow symlinks. A launch
// always names it.
//
// `blockedTools` are the selected packs' blocked-tool declarations. They are a PARAMETER
// because core blocks nothing by default since the guardrails pack took the rules over — the
// config's security section alone would render an empty YOLO_BLOCK_CONFIG and the generated
// home would carry no blockers at all.
func buildBootstrapEnv(workspace string, cfg, gitIdentity, sandboxEnv *jsonx.OrderedMap,
	packRoot, homeOverlay, ctxRoot string, hostCtx HostContext, homeSidecar, home string,
	darwinPrefix []string, blockedTools []packload.BlockedTool) *jsonx.OrderedMap {
	bootstrapEnv := jsonx.NewOrderedMap()
	bootstrapEnv.Set("YOLO_HOST_DIR", resolvePathAbs(workspace))
	blockJSON, _ := jsonx.DumpsCompact(config.NormalizeBlockedToolsWith(securitySection(cfg), blockedTools))
	bootstrapEnv.Set("YOLO_BLOCK_CONFIG", blockJSON)
	miseJSON, _ := jsonx.DumpsCompact(orderedMapToAny(config.MergeMiseTools(cfg)))
	bootstrapEnv.Set("YOLO_MISE_TOOLS", miseJSON)
	lspJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "lsp_servers"))
	bootstrapEnv.Set("YOLO_LSP_SERVERS", lspJSON)
	// THE TWO INSTALL LISTS BESIDE THE TABLE THAT RENDERS THEM, and the pair is the point:
	// YOLO_LSP_SERVERS is what writes an agent's LSP config, while these two are what puts
	// the servers on disk. This backend set the first and neither of the other two until
	// 2026-09-13, so a workspace declaring `lsp_servers` got config naming servers that
	// were never installed — the confined stage execed the generated script, its install
	// loop iterated an empty list, and the stage exited 0 (docs/design/
	// macos-user-provisioning.md §10.6). BuildRunPlan carries the SAME two values into the
	// session env file, which is how the stage gets them; here they reach the generators
	// that read the launch's declared set — catalog.go's orphan finders and
	// serverrefresh.go's refresh set both ask the environment what THIS launch asked for.
	//
	// Emitted on every launch, empty included, on the wire tables' rule below: the
	// container emits both `-e` lines unconditionally (internal/cli/run/assemble.go), and
	// an empty value is what the readers already treat as "install nothing".
	lspNPM, lspGo := config.LSPInstalls(cfg)
	bootstrapEnv.Set("YOLO_LSP_NPM_INSTALL", lspNPM)
	bootstrapEnv.Set("YOLO_LSP_GO_INSTALL", lspGo)
	mcpSrvJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "mcp_servers"))
	bootstrapEnv.Set("YOLO_MCP_SERVERS", mcpSrvJSON)
	mcpPresetsJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyList(cfg, "mcp_presets"))
	bootstrapEnv.Set("YOLO_MCP_PRESETS", mcpPresetsJSON)
	// The `agent_updates` policy. macos-user is the backend where missing this hides
	// least: it bakes no image, so the launchers ARE the delivery.
	//
	// ⚠ It used to add "and the one whose single machine-wide home makes the install-prefix
	// lock they carry reachable". Since the home-tier layout the install prefixes
	// (~/.npm-global, ~/.local) are symlinks into <workspace>/.yolo/home, so the lock is
	// per-workspace here exactly as it is on every other backend — which is not a loss: two
	// workspaces now update two different installs, so there is nothing left for a
	// machine-wide lock to serialise.
	bootstrapEnv.Set(entrypoint.AgentUpdatesEnv, config.AgentUpdatesWire())
	// git identity rides verbatim (the subcommand's Env.Vars carries it into
	// configureGit).
	for _, k := range gitIdentity.Keys() {
		v, _ := gitIdentity.Get(k)
		bootstrapEnv.Set(k, v)
	}
	// host_files, IN TWO HALVES THAT COME FROM DIFFERENT PLACES, and the split is the
	// credential boundary rather than a structure.
	//
	// The SOURCE-LESS half is read from the config map handed in, never via
	// config.LoadHostFiles: the plan builder is pure, and a source-less entry is legal at
	// any scope, so the merged map is the right source for exactly that subset.
	//
	// The SOURCE-BEARING half cannot be read here AT ALL — it lives in the user config
	// and its bytes live in the user's home, which is what makes it user-scope-only — so
	// it arrives as hostCtx.HostFiles, already RESOLVED by the host CLI. That is DP-L1:
	// until 2026-09-13 this backend DROPPED the source-bearing half outright, because the
	// bytes cross on a /ctx mount and there were no mounts; they now cross by COPY into
	// the root-owned tree ctxRoot names, so the entries cross with them.
	//
	// ⚠ RESOLVED, not "staged": an entry is on the wire whether or not its host file
	// EXISTS. An absent dotfile is a normal state, the destination then renders from its
	// `defaults`/`content` layers, and that is what the container path does too — it
	// emits the entry and skips only the bind. Gating the wire on the copy would leave
	// this backend answering differently in exactly that state.
	if wire := hostFilesWire(cfg, hostCtx); wire != "" {
		bootstrapEnv.Set("YOLO_HOST_FILES", wire)
	}

	// YOLO_CTX_ROOT — where the host bytes actually landed. Both of the entrypoint's /ctx
	// readers resolve through it (entrypoint.ctxRoot for a pack's `readsHost` surface,
	// entrypoint.hostUserPath for the user's own host_files), which is the SAME seam
	// Apple Container already uses for the same reason: a backend that cannot present
	// /ctx at the constant path is TOLD the path instead of assuming one.
	//
	// Set only when the launch staged a tree, so a launch carrying no host bytes says so
	// by ABSENCE — a variable naming a directory that is not there would make an empty
	// delivery indistinguishable from a broken one, which is YOLO_PACK_ROOT's rule and
	// the reason the report below reads off the same condition.
	if ctxRoot != "" {
		bootstrapEnv.Set("YOLO_CTX_ROOT", ctxRoot)
	}

	// YOLO_HOST_LAYERS — the host-layer report (packload.HostLayerReport), and since
	// DP-L1 this backend can answer `supported` like every other one.
	//
	// ⚠ THE CARVE-OUT THAT STOOD HERE IS RETIRED, and retiring it is the whole point
	// rather than a side effect. It read: a `readsHost` surface's bytes cross on a /ctx
	// mount, this backend has no mounts, so every host layer is missing by construction —
	// and since the jail's read fails CLOSED (OQ-CO10), reporting `supported` would have
	// refused every launch selecting the claude or pi pack on a Mac with a settings.json.
	// The premise is now false: the bytes cross by COPY into ctxRoot, so a delivered file
	// is a file the jail can really open, and `unsupported` would be the lie.
	//
	// WHAT THE REPORT IS GATED ON IS DELIVERY, NOT THE PLATFORM. `supported` the moment
	// the host CLI composed a tree; `unsupported` when it composed none, which is both the
	// pre-DP-L1 state and the state of a caller that cannot compose one (a capture, a unit
	// test). Keeping the two readings on ONE condition — ctxRoot, which is itself derived
	// from hostCtx.Tree — is what stops a launch from claiming a delivery mechanism it did
	// not use, and OQ-R3's rule survives intact for the `unsupported` case: a backend that
	// did not deliver is still not REFUSED for it.
	//
	// Delivered is the launcher's own list, never a walk of the staged tree: a witness
	// that re-derives its expectation from the evidence is not a witness.
	bootstrapEnv.Set(packload.HostLayerEnvVar, hostLayerWire(ctxRoot, hostCtx))

	// YOLO_PACK_ROOT — the same generator-contract variable the container entrypoint
	// reads off its /ctx/packs mount, pointed at the root-owned staged copy. Without it
	// RunDarwinBootstrap's LoadJailPacks returns nothing and every pack loop below it
	// (ConfigurePackSurfaces, RunPackHooks) iterates an empty list — silently, since
	// "no packs mounted" is a legitimate state that renders nothing rather than failing
	// (B-0). The caller passes "" when the launch staged nothing, so a genuinely pack-less
	// launch still says so by ABSENCE rather than by naming a directory that is not there.
	if packRoot != "" {
		bootstrapEnv.Set("YOLO_PACK_ROOT", packRoot)
	}

	// YOLO_DARWIN_HOME_OVERLAY — the composed CONTENT tree (skills + briefings) the
	// bootstrap copies over the home it is generating into. Set only when there is content
	// to deliver, on the same reasoning as YOLO_PACK_ROOT: absence is the honest way to
	// say "nothing to install", and a variable naming a directory that is not there
	// would make an empty delivery indistinguishable from a broken one.
	//
	// It carries a PATH, not a mapping. The tree is already laid out at the
	// home-relative destinations the container path would have mounted, so installing
	// it is one recursive copy and the bootstrap needs no table to interpret.
	if homeOverlay != "" {
		bootstrapEnv.Set("YOLO_DARWIN_HOME_OVERLAY", homeOverlay)
	}

	// YOLO_DARWIN_HOME_SIDECAR — the per-workspace tier's location, and the switch that
	// decides whether the bootstrap lays the home layout at all. Absence is meaningful here
	// in a way it is not above: the two vars above say "there is nothing staged to install",
	// this one says "this home is not a workspace's" (the capture staging home), and a
	// launch that dropped it would fall silently back to the shared account home — the tier
	// collapse, restored by an omission.
	if homeSidecar != "" {
		bootstrapEnv.Set(entrypoint.DarwinHomeSidecarEnv, homeSidecar)
	}

	// THE TWO PROVIDER/PROFILE WIRE TABLES, relayed from the launch env into the
	// bootstrap env. The container boot reads both out of the jail environment
	// (ConfigurePackSurfaces resolves the profile table into every pack's config patch;
	// the derives read the provider table through the prism), and the native bootstrap
	// runs the SAME generators — so without this relay a `-p` launch would compose the
	// variant's env correctly and still render every pack surface as if no variant were
	// selected, which is the silent half of the same defect. Read out of sandboxEnv by
	// name, the way git identity above is read out of it by prefix: the launch env is
	// the ONE place the channel lands, and the bootstrap is a consumer of it, not a
	// second composition site.
	//
	// Always relayed when present, including the empty `{}`: an absent variable and an
	// empty table mean the same thing to the readers, but the container emits the empty
	// table explicitly, so the two backends' bootstraps see the same input shape.
	for _, wire := range []string{"YOLO_PROVIDERS", "YOLO_USE_PROFILES"} {
		if v, ok := sandboxEnv.Get(wire); ok {
			bootstrapEnv.Set(wire, v)
		}
	}

	// MISE_DATA_DIR, named rather than defaulted — the bootstrap's Env resolves
	// $HOME/.local/share/mise when it is unset (entrypoint.NewEnv), and ~/.local is a
	// symlink into the workspace sidecar under the home-tier layout, so the default would
	// put the tool store in the per-workspace tier. The launch env carries the same value
	// from the same function (sandboxEnvPairs), so the store the generated .bashrc names
	// and the store the agent's PATH resolves through cannot disagree.
	bootstrapEnv.Set("MISE_DATA_DIR", SandboxMiseData(home))

	// Darwin extras consumed by `yolo internal darwin-bootstrap`.
	bootstrapEnv.Set("YOLO_DARWIN_WORKSPACE", workspace)
	bootstrapEnv.Set("YOLO_DARWIN_MACOS_LOG", macosLogMode(cfg))
	bootstrapEnv.Set(entrypoint.DarwinLoginPathEnv, SandboxPath(home, darwinPrefix))
	return bootstrapEnv
}

// PlanInvariants returns static-check violation messages over a RunPlan (all
// ordering.
func PlanInvariants(plan RunPlan) []string {
	var problems []string

	// B2 (Go): the staged yolo binary must live under the root-owned state dir,
	// and the bootstrap argv must self-exec THAT staged path — never the host
	// checkout (unreadable to the sandbox uid) or a bare "yolo" off PATH.
	if !strings.HasPrefix(plan.StagedYolo, plan.StagedDir+"/") {
		problems = append(problems,
			"staged yolo "+plan.StagedYolo+" is not under the root-owned state dir "+
				plan.StagedDir+"; the sandbox could rewrite its own launch binary")
	}
	if !containsArg(plan.BootstrapArgv, plan.StagedYolo) {
		problems = append(problems,
			"bootstrap argv does not self-exec the staged yolo ("+plan.StagedYolo+
				"); it would run an unstaged/unreadable binary")
	}
	// The stage step must have a real source binary to copy — an empty selfExe
	// (os.Executable failed) would stage nothing and the self-exec would fail.
	if stageCopySourceEmpty(plan.StageCommands) {
		problems = append(problems,
			"no source yolo binary resolved to stage (os.Executable failed); "+
				"the sandbox would have no bootstrap binary to exec")
	}

	// B3 (Go): the stage commands must produce a FRESH inode (copy-to-temp + mv),
	// not overwrite in place — macOS caches Mach-O signatures per vnode, so an
	// in-place overwrite gets the next exec SIGKILLed.
	if !stageCommandsUseFreshInode(plan.StageCommands) {
		problems = append(problems,
			"stage commands overwrite the staged binary in place; macOS signature "+
				"caching requires a fresh inode (copy-to-temp then mv)")
	}

	// The workspace must be neutral ground — never inside a user's home.
	if plan.OffendingHomeSet {
		problems = append(problems,
			"workspace "+plan.Workspace+" is inside the home directory "+
				plan.OffendingHome+"; the macos-user backend shares only "+
				"neutral ground. Move it under "+SharedRootDefault()+".")
	}

	// Git identity must reach the BOOTSTRAP env (baked onto the self-exec argv).
	bootStr := strings.Join(plan.BootstrapArgv, " ")
	for _, k := range plan.GitIdentity.Keys() {
		if !strings.Contains(bootStr, k) {
			problems = append(problems, "git identity "+k+" not baked into the bootstrap env")
		}
	}

	// The pack tree must be STAGED and must reach the BOOTSTRAP env, or the bootstrap
	// renders zero pack surfaces while reporting success — B-0, which survived because
	// nothing on this path ever asserted that the pack root arrived. Both halves are
	// checked because either one alone is silently useless: an env var naming a tree
	// nothing copied is as empty as a copied tree the bootstrap is never told about.
	if plan.PackRoot != "" {
		if !strings.HasPrefix(plan.PackRoot, plan.StagedDir+"/") {
			problems = append(problems,
				"staged pack root "+plan.PackRoot+" is not under the root-owned state dir "+
					plan.StagedDir+"; the sandbox could rewrite a pack manifest and grant "+
					"itself host access on the next launch")
		}
		if !stagesTreeAt(plan.StageCommands, plan.PackRoot) {
			problems = append(problems,
				"nothing stages the pack tree at "+plan.PackRoot+
					"; the bootstrap would render zero pack surfaces")
		}
		if !containsArg(plan.BootstrapArgv, "YOLO_PACK_ROOT="+plan.PackRoot) {
			problems = append(problems,
				"YOLO_PACK_ROOT="+plan.PackRoot+" is not baked into the bootstrap env; "+
					"LoadJailPacks would find no packs and every surface/hook loop would "+
					"iterate an empty list")
		}
	}

	// THE CONTEXT TREE, on the pack tree's rule and for a sharper failure (DP-L1). Three
	// halves, because any one alone is silently useless or actively wrong:
	//
	//   - staged somewhere the sandbox cannot REWRITE, or an agent edits the very bytes
	//     its own config is composed from on the next launch — the same argument that
	//     makes the pack root root-owned, one step closer to the credential;
	//   - actually COPIED, or the bootstrap opens an empty directory;
	//   - NAMED to the bootstrap, or the entrypoint's readers stay pointed at the literal
	//     /ctx, which does not exist on macOS at all, and every surface silently composes
	//     from its defaults layer while the launch reports a delivery.
	if plan.CtxRoot != "" {
		if !strings.HasPrefix(plan.CtxRoot, plan.StagedDir+"/") {
			problems = append(problems,
				"staged context root "+plan.CtxRoot+" is not under the root-owned state dir "+
					plan.StagedDir+"; the sandbox could rewrite the host bytes its own config "+
					"surfaces are composed from")
		}
		if !stagesTreeAt(plan.StageCommands, plan.CtxRoot) {
			problems = append(problems,
				"nothing stages the context tree at "+plan.CtxRoot+
					"; every `reads-host` surface and every source-bearing host_files entry "+
					"would compose from an empty directory")
		}
		if !containsArg(plan.BootstrapArgv, "YOLO_CTX_ROOT="+plan.CtxRoot) {
			problems = append(problems,
				"YOLO_CTX_ROOT="+plan.CtxRoot+" is not baked into the bootstrap env; the "+
					"entrypoint would read host layers from the literal /ctx, which does not "+
					"exist on macOS, and every surface would compose from its defaults layer")
		}
	}

	// THE REPORT AND THE TREE ARE ONE FACT, checked against each other rather than each
	// against itself. The jail's read fails CLOSED (OQ-CO10), so a report claiming
	// `supported` with nothing staged refuses every host layer on the machine, and a
	// report claiming `unsupported` while a tree IS staged silently un-delivers bytes the
	// launch really did copy. Both are reachable by editing one of the two lines in
	// buildBootstrapEnv, and neither shows up in any rendered artifact.
	if got, ok := argvEnvValue(plan.BootstrapArgv, packload.HostLayerEnvVar); !ok {
		problems = append(problems,
			packload.HostLayerEnvVar+" is not baked into the bootstrap env; the jail's "+
				"fail-closed host-layer read would have no disposition and would silently "+
				"compose every `readsHost` surface without the user's own file")
	} else if report, parsed := packload.ParseHostLayerReport(got); !parsed {
		problems = append(problems,
			packload.HostLayerEnvVar+"="+got+" is not the report shape the jail parses; it "+
				"would be read as UNKNOWN, restoring the fail-open behaviour OQ-CO10 ended")
	} else if (report.Delivery == packload.HostLayersSupported) != (plan.CtxRoot != "") {
		problems = append(problems,
			"the host-layer report says delivery is "+report.Delivery+" while the staged "+
				"context root is "+quoteOrNone(plan.CtxRoot)+"; the two are one fact and the "+
				"jail refuses a launch that claims a delivery it did not make")
	}

	// AND THE WIRE MUST BE DECODABLE BY THE HALF THAT READS IT. A YOLO_HOST_FILES the
	// entrypoint cannot parse makes ConfigureHostFiles skip EVERY entry at once — the
	// user's whole `host_files` key, silently, from one malformed value — and nothing else
	// in the plan shows it, because the wire is a JSON blob on an argv.
	//
	// ⚠ THERE IS DELIBERATELY NO "a source-bearing entry must have been staged" CHECK, and
	// an earlier cut of this block had one that was WRONG. A source that does not exist yet
	// is a normal state (a dotfile the user has not written), the entry still crosses so the
	// destination renders from its `defaults`/`content` layers, and that is exactly what the
	// container path does — it emits the entry and skips only the bind. Refusing the launch
	// for it would have made this backend answer differently in the one state nobody would
	// think to test. The defect that check was reaching for — bytes staged and the root not
	// named — is caught above, against the tree rather than against an entry.
	if wire, ok := argvEnvValue(plan.BootstrapArgv, "YOLO_HOST_FILES"); ok {
		if _, err := config.UnmarshalHostFiles(wire); err != nil {
			problems = append(problems,
				"YOLO_HOST_FILES is not decodable by the entrypoint ("+err.Error()+
					"); every declared host_files entry would be skipped at once")
		}
	}

	// THE WORKSPACE TIER crosses as exactly one env var, and nothing downstream reports its
	// absence: a bootstrap that is not told the sidecar lays no layout, every pack `state`
	// dir stays in the shared account home, and the launch looks perfectly healthy — which
	// is the defect docs/design/macos-user-home-tiers.md exists to end, reachable again by
	// deleting one line in BuildRunPlan. Checked against the workspace's own sidecar path
	// rather than "some value", because a layout pointed at another workspace's sidecar is
	// the collapse with extra steps.
	if want := entrypoint.DarwinHomeSidecarEnv + "=" + paths.WorkspaceHomeState(plan.Workspace); !containsArg(plan.BootstrapArgv, want) {
		problems = append(problems,
			want+" is not baked into the bootstrap env; the sandbox home would keep every "+
				"workspace's agent state in one place (/Users/_yolojail)")
	}

	// The two provider/profile wire tables must reach BOTH the launch env and the
	// BOOTSTRAP env. The launch env alone composes the agent's process env; the bootstrap
	// env is what renders the pack surfaces and the derives — so a launch that carries
	// YOLO_USE_PROFILES while the bootstrap does not would run the selected variant's
	// environment against config written as if no variant were selected. Relayed by name
	// in BuildRunPlan; this is what fails if that relay is deleted, which no test on the
	// launch env alone can see.
	for _, wire := range []string{"YOLO_PROVIDERS", "YOLO_USE_PROFILES"} {
		for _, a := range plan.LaunchArgv {
			if !strings.HasPrefix(a, wire+"=") {
				continue
			}
			if !containsArg(plan.BootstrapArgv, a) {
				problems = append(problems,
					wire+" is in the launch env but not baked into the bootstrap env "+
						"("+a+"); the pack surfaces and derives would render as if no "+
						"profile were selected")
			}
		}
	}

	// THE TWO LSP INSTALL LISTS MUST REACH BOTH ENVIRONMENTS — and "both" is the whole
	// invariant, because either one alone is a launch that reports success and installs
	// nothing. The bootstrap env is what the generators read (catalog.go's orphan finders,
	// serverrefresh.go's refresh set); the session env file is what the CONFINED
	// PROVISIONING STAGE reads, and the stage is the process that runs `npm install`. This
	// backend carried NEITHER until 2026-09-13 while still setting YOLO_LSP_SERVERS, so an
	// agent's config named servers that were never on disk — a silent failure no test on the
	// rendered config could see, which is why the check is here and not on the table.
	//
	// An EMPTY bootstrap value asks nothing of the file: empty and absent are the same
	// instruction to every reader, and BuildRunPlan deliberately composes no env file for a
	// workspace that declared no LSP server.
	for _, key := range []string{"YOLO_LSP_NPM_INSTALL", "YOLO_LSP_GO_INSTALL"} {
		want, ok := argvEnvValue(plan.BootstrapArgv, key)
		if !ok {
			problems = append(problems,
				key+" is not baked into the bootstrap env; the launch's declared LSP set "+
					"would be invisible to every generator that reads it — orphan detection "+
					"would report the servers it installed as unowned, and the evergreen "+
					"refresh would skip them")
			continue
		}
		if want == "" {
			continue
		}
		if !SandboxEnvFileSets(plan.EnvFileContent, key, want) {
			problems = append(problems,
				key+"="+want+" reached the bootstrap env but not the session env file; the "+
					"provisioning stage reads its environment from that file, so it would "+
					"exec the generated script, find an empty install list and exit 0 having "+
					"installed no LSP server — while the agent's config names them")
		}
	}

	// EVERY PUBLISHED ENDPOINT THE LAUNCH CARRIES MUST HAVE ITS CROSS-UID GRANT STAGED, and
	// this is the check that survives the generalisation of the host-service lifecycle.
	// Before it, one variable was named by hand in BuildRunPlan and one service started on
	// this arm; now the arm starts every loophole the machine supports
	// (internal/cli/run/run.go), so the failure to guard against is a NEW service whose
	// endpoint reaches the env file with no ACE behind it.
	//
	// That state is worse than an absent service and is invisible everywhere else: the
	// sandbox is TOLD where the endpoint is, the file is 0600 in a 0700 directory owned by
	// the invoking user, and the client fails with a permission error naming a path that
	// plainly exists — on the one backend whose entire point is the uid boundary. Nothing
	// else in the plan shows it, because both halves look present.
	//
	// ASKED THROUGH THE RENDERED FILE (SandboxEnvFileSets), never by parsing a value out of
	// it: each staged ARGUMENT is offered back to the writer's own renderer, so this and the
	// env file cannot disagree about quoting rather than about the value.
	for _, key := range SandboxEnvFileKeys(plan.EnvFileContent) {
		if !isServiceEndpointVar(key) {
			continue
		}
		if !stageCommandsNameEnvValue(plan.StageCommands, plan.EnvFileContent, key) {
			problems = append(problems,
				key+" carries a published endpoint that no stage command grants the sandbox "+
					"account; the file is 0600 under the invoking user, so the sandbox would "+
					"be told exactly where its service is and be unable to open it")
		}
	}

	// ⚠ NO `--login` ON THE LAUNCH ARGV EITHER, since 2026-09-12. The stage's copy of this
	// check (below) has always been here; the launch's absence was the fix that let a
	// forwarded command survive at all. `sudo -i` concatenates and backslash-escapes the
	// command it is given, leaving `$` for an intermediate login shell — so every `$var` in a
	// user's own `yolo -- bash -lc …` arrived empty and every newline was removed, silently
	// and with exit 0. It cost runbook item 6 a measurement (nine blank lines) and made this
	// backend the only one that rewrites the argv it is handed.
	if containsArg(plan.LaunchArgv, "--login") {
		problems = append(problems,
			"the launch argv carries `sudo --login`, which does not execve the command it "+
				"is given: it concatenates and backslash-escapes it, dropping newlines and "+
				"letting an intermediate login shell expand every `$var` against an empty "+
				"environment. A forwarded command would be silently rewritten and would "+
				"still exit 0 — see docs/design/macos-user-provisioning.md §1.1")
	}

	// THE AGENT'S OWN ARGV MUST BE CONFINED, UNDER THIS SESSION'S PROFILE — the check
	// that was missing while its two siblings were here.
	//
	// The provisioning stage (below) and the capture driver (CapturePlanInvariants) have
	// each been pinned to `sandbox-exec -f <profile>` since they were written, on the
	// argument that they run vendor code. The AGENT was not, and the agent is the process
	// this whole backend exists to confine: on macos-user the Seatbelt profile IS the
	// trust boundary — there is no container, no uid boundary between the agent and its
	// own tools, and nothing else in a launch that says "this may not read /Users".
	// Deleting `"/usr/bin/sandbox-exec", "-f", profilePath` from LaunchArgv (macosuser.go)
	// left every test in this repo green, which is the callee-pinned/call-site-unpinned
	// shape AGENTS.md names — one level up, because the callee here is the ARGV BUILDER
	// and the unpinned call site is the one place its output is trusted.
	//
	// THE THREE WORDS ARE CHECKED CONSECUTIVELY (containsArgPair) rather than as three
	// memberships: `-f` and the profile path both appear elsewhere on a real argv — the
	// path is also inside the env file's own vicinity and `-f` is a common flag — so three
	// independent membership tests would pass for an argv that named all three in
	// unrelated positions and confined nothing.
	//
	// Silent for an EMPTY LaunchArgv, which is a plan that launches nothing (a capture's
	// plan builder, a unit fixture); a launch that reaches the orchestrator always has one.
	if len(plan.LaunchArgv) > 0 &&
		!containsArgPair(plan.LaunchArgv, "/usr/bin/sandbox-exec", "-f", plan.ProfilePath) {
		problems = append(problems,
			"the agent launch argv does not run under `sandbox-exec -f "+plan.ProfilePath+
				"`; the agent would run unconfined as "+SandboxUser+", and on this backend "+
				"that profile is the whole trust boundary — nothing else denies it /Users, "+
				"the keychains or another process's command line")
	}

	// Acceptance-bar guard: darwin store bin dirs must reach the launch PATH.
	launchStr := strings.Join(plan.LaunchArgv, " ")
	for _, storeBin := range plan.DarwinPathPrefix {
		if !strings.Contains(launchStr, storeBin) {
			problems = append(problems,
				"darwin package bin dir "+storeBin+" did not reach the launch "+
					"PATH — declared tools would be silently missing")
		}
	}

	// AND THE BOOTSTRAP'S OWN COPY OF THAT PATH, which is a different reader with a
	// different failure. $YOLO_DARWIN_LOGIN_PATH is what entrypoint.agentPath
	// returns, and three generators ask it "will the agent have this binary?"
	// before writing anything:
	//
	//   • GenerateShims — a blocked tool declaring a `replacement` is only blocked
	//     when the replacement is on that PATH, so a floor missing from it silently
	//     UN-BLOCKS `grep` and `find` instead of failing;
	//   • launchercollision's imageProbePath — a pack's `program` launcher is
	//     suppressed for a name the environment already provides, so a floor
	//     missing from it lets a pack shadow git or node;
	//   • AssertRequiredBins — a pack's `requires` entry warns as absent.
	//
	// All three answer WRONG rather than failing, and all three run in the
	// bootstrap, which the launch argv's PATH never reaches. So the launch guard
	// above cannot see this: a plan can put the store on the agent's PATH and still
	// generate the agent's environment as if the store were not there.
	for _, storeBin := range plan.DarwinPathPrefix {
		if !strings.Contains(bootStr, storeBin) {
			problems = append(problems,
				"darwin package bin dir "+storeBin+" did not reach "+
					entrypoint.DarwinLoginPathEnv+" in the bootstrap env — the "+
					"generators would decide what to write against a PATH that "+
					"does not have the floor on it")
		}
	}

	// THE PROVISIONING STAGE, when there is one. Every check below is silent for an
	// EMPTY ProvisionArgv, because "this config asked for no tools" is a legitimate plan
	// and the skip is the feature (ProvisionNeeded).
	if len(plan.ProvisionArgv) > 0 {
		// ⚠ NO `--login`. This is the invariant this file exists to carry, and it guards a
		// MEASURED defect rather than a style: `sudo -i` does not execve its argv — it
		// concatenates and backslash-escapes the command, leaving `$` unescaped for an
		// intermediate login shell to expand. The stage script is dense with `$_prc`,
		// `${PIPESTATUS[0]}` and `$(date …)`, and the failure is silent: the wrong command
		// runs and exits 0. A plan that grew the flag would provision nothing and report
		// success, which is precisely what the startup log cannot then record.
		if containsArg(plan.ProvisionArgv, "--login") {
			problems = append(problems,
				"the provisioning stage argv carries `sudo --login`, which does not execve "+
					"the command it is given: it concatenates and backslash-escapes it, and "+
					"leaves `$` for the login shell to expand. The stage script would be "+
					"mangled and would exit 0 having provisioned nothing — see "+
					"docs/design/macos-user-provisioning.md §1.1")
		}
		// CONFINED, under THIS session's profile. The stage runs vendor code (npm
		// postinstall hooks, mise plugins), which the container runs inside its jail; the
		// bootstrap's unconfined argv is tolerable only because it runs yolo's own code
		// (the design's P4). A stage that lost its sandbox-exec would be the one step
		// running third-party installers on a naked macOS account.
		if !containsArg(plan.ProvisionArgv, "/usr/bin/sandbox-exec") {
			problems = append(problems,
				"the provisioning stage is not run under sandbox-exec; it executes vendor "+
					"install code (npm postinstall hooks, mise plugins) and would be the "+
					"only unconfined step in the launch")
		}
		if !containsArg(plan.ProvisionArgv, plan.ProfilePath) {
			problems = append(problems,
				"the provisioning stage does not name this session's Seatbelt profile ("+
					plan.ProfilePath+"); it would be confined by some other launch's policy "+
					"or none")
		}
		// IT MUST EXEC THE SCRIPT THE BOOTSTRAP WRITES. Two halves, and either alone is
		// silently useless: a stage naming a path nothing generates fails on its first
		// line, and a script generated somewhere the stage never looks is a file nobody
		// runs. Both sides resolve through ProvisionBootstrapScript, so this is what
		// fails if one of them starts spelling the path itself.
		if want := ProvisionBootstrapScript(plan.Workspace); plan.ProvisionScriptPath != want {
			problems = append(problems,
				"the provisioning stage execs "+plan.ProvisionScriptPath+", but the bootstrap "+
					"generates the script at "+want)
		}
		if !argvMentions(plan.ProvisionArgv, plan.ProvisionScriptPath) {
			problems = append(problems,
				"the provisioning stage argv never names the generated bootstrap script ("+
					plan.ProvisionScriptPath+"); the stage would install nothing")
		}
		// AND IT MUST RECORD ITS FAILURE WHERE THE BRIEFING READS IT. The reader
		// (jailcontent.ReadProvisioningFailed) resolves the log from the HOST's workspace;
		// this backend has no bind to make a second path name the same file, so an emitter
		// rooted anywhere else leaves a failed provision reported as healthy.
		if !argvMentions(plan.ProvisionArgv, provision.StartupLog(plan.Workspace)) {
			problems = append(problems,
				"the provisioning stage does not write "+provision.StartupLog(plan.Workspace)+
					"; a failure would never reach the agent's briefing")
		}
	}

	// A LAUNCH THAT MATERIALIZED MUST HAVE SOMETHING TO SHOW FOR IT. Since the
	// floor landed there is no such thing as an empty native closure: even with an
	// empty `packages:` the profile holds mise, node, git and the rest, so an empty
	// prefix means the build returned a store path nothing derived a bin dir from.
	// Without this the two loops above are vacuously true in exactly that case —
	// they iterate an empty list and report a healthy plan.
	if plan.DarwinMaterialized && len(plan.DarwinPathPrefix) == 0 {
		problems = append(problems,
			"the native package closure was materialized but contributed no bin dir "+
				"to PATH; the floor (mise, node, git, ripgrep, …) would be absent "+
				"from the sandbox — see docs/design/macos-user-provisioning.md")
	}

	// NO COMPOSED VALUE ON A COMMAND LINE, AND THE FILE ACTUALLY READ. The two halves of
	// envfile.go's contract, checked together because either alone passes for a plan that is
	// wrong in the other way: an argv with no pairs and no reader is a sandbox with no
	// credentials, and an argv that reads the file and still carries the pairs has moved
	// nothing. Both are applied to every argv the sandbox user runs UNDER SEATBELT — the
	// bootstrap is excluded, and SandboxArgvEnvProblems says why.
	for _, pair := range [][2]any{
		{"launch", plan.LaunchArgv},
		{"provisioning stage", plan.ProvisionArgv},
	} {
		label, argv := pair[0].(string), pair[1].([]string)
		if len(argv) == 0 {
			continue
		}
		problems = append(problems, SandboxArgvEnvProblems(label, argv)...)
		if !SandboxArgvReadsEnvFile(plan.EnvFile, argv) {
			problems = append(problems,
				"the "+label+" argv never reads the session env file ("+plan.EnvFile+
					"); it would run with the identity quartet and nothing this launch "+
					"composed — no provider credentials, no env_sources, no git identity")
		}
	}
	// The file itself must sit under the root-owned state dir, for the staged binary's
	// reason: a file the sandbox could REWRITE is a sandbox that chooses its own
	// environment, and this one is sourced by the shell that execs the agent.
	if plan.EnvFile != "" && !strings.HasPrefix(plan.EnvFile, plan.StagedDir+"/") {
		problems = append(problems,
			"session env file "+plan.EnvFile+" is not under the root-owned state dir "+
				plan.StagedDir+"; the sandbox could rewrite the environment it is launched with")
	}

	return problems
}

// argvMentions reports whether any argv element CONTAINS sub — the substring test the
// stage checks need, because the script is one argv element and the paths it must name
// are embedded in it rather than being arguments of their own.
func argvMentions(argv []string, sub string) bool {
	if sub == "" {
		return false
	}
	for _, a := range argv {
		if strings.Contains(a, sub) {
			return true
		}
	}
	return false
}

// argvEnvValue returns the value of the `K=V` word naming key on an argv, and whether it
// is there at all. The two answers are different facts and both are load-bearing: an
// ABSENT variable is a crossing that was never written, while an EMPTY one is a launch
// that legitimately asked for nothing, so a helper returning only the string would make
// the two indistinguishable at exactly the check that has to tell them apart.
func argvEnvValue(argv []string, key string) (string, bool) {
	for _, a := range argv {
		if v, ok := strings.CutPrefix(a, key+"="); ok {
			return v, true
		}
	}
	return "", false
}

// containsArg reports whether argv contains the exact arg.
func containsArg(argv []string, arg string) bool {
	for _, a := range argv {
		if a == arg {
			return true
		}
	}
	return false
}

// stagesTreeAt reports whether the stage commands finish by moving a tree INTO dest —
// the last command each of the three tree stagers emits (StagePackCommands,
// StageHomeOverlayCommands, StageCtxCommands). Checking the destination of the final `mv`
// rather than merely "dest appears somewhere" is what makes the invariant meaningful: the
// path also appears in the preceding `rm -rf`, so a substring test would pass for a plan
// that deleted the tree and staged nothing.
//
// It was stagesPackRoot until the context tree joined the list. One predicate for all
// three deliberately: they share a shape, and a per-tree copy is three places for the
// `rm -rf` confusion above to be reintroduced one at a time.
func stagesTreeAt(cmds [][]string, dest string) bool {
	for _, c := range cmds {
		if len(c) >= 4 && c[0] == mvBin && c[len(c)-1] == dest {
			return true
		}
	}
	return false
}

// quoteOrNone renders a path for a problem message, or the word for its absence — so a
// mismatch message reads "the staged context root is none" rather than trailing an empty
// pair of quotes the reader has to interpret.
func quoteOrNone(p string) string {
	if p == "" {
		return "none"
	}
	return p
}

// stageCommandsUseFreshInode reports whether the stage commands end with an
// `mv` (the atomic rename that guarantees a fresh inode) rather than a bare
// in-place `cp` to the final path — the macOS signature-caching guard (J2 §3).
func stageCommandsUseFreshInode(cmds [][]string) bool {
	for _, c := range cmds {
		if len(c) > 0 && c[0] == mvBin {
			return true
		}
	}
	return false
}

// stageCopySourceEmpty reports whether the cp stage command has an empty source
// argument (StageBinaryCommands built from an empty selfExe) — i.e. nothing to
// stage. The cp argv is {cp, -f, <src>, <tmp>}, so the source is arg index 2.
func stageCopySourceEmpty(cmds [][]string) bool {
	for _, c := range cmds {
		if len(c) >= 4 && c[0] == cpBin && c[2] == "" {
			return true
		}
	}
	return false
}

func cnameFor(workspace string) string {
	return cnameFn(workspace)
}

// cnameFn is a package var so the run orchestrator can share a single naming
// definition; defaults to runtime.FromWorkspace.
var cnameFn = runtime.FromWorkspace

// --- config accessors (thin adapters over jsonx.OrderedMap) -----------------
// securitySection returns config["security"] as an OrderedMap, or nil.
// cfgSection is securitySection generalized to any top-level object key. Added for the
// backend-inert warnings in the orchestrator, which need `resources` and
// `cache_relocations` and would otherwise each grow their own copy of this five-line
// nil-and-type dance.
func cfgSection(cfg *jsonx.OrderedMap, key string) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	v, ok := cfg.Get(key)
	if !ok {
		return nil
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

func securitySection(cfg *jsonx.OrderedMap) *jsonx.OrderedMap {
	if cfg == nil {
		return nil
	}
	v, ok := cfg.Get("security")
	if !ok {
		return nil
	}
	m, _ := v.(*jsonx.OrderedMap)
	return m
}

// getSectionOrEmptyMap returns config[key] as an OrderedMap, or an empty one.
// If the value is present but not a map, returns empty.
func getSectionOrEmptyMap(cfg *jsonx.OrderedMap, key string) any {
	if cfg != nil {
		if v, ok := cfg.Get(key); ok {
			if _, isMap := v.(*jsonx.OrderedMap); isMap {
				return v
			}
		}
	}
	return jsonx.NewOrderedMap()
}

// getSectionOrEmptyList returns config[key] as a list, or an empty list.
func getSectionOrEmptyList(cfg *jsonx.OrderedMap, key string) any {
	if cfg != nil {
		if v, ok := cfg.Get(key); ok {
			if _, isList := v.([]any); isList {
				return v
			}
		}
	}
	return []any{}
}

// macosLogMode returns config["macos_log"] as a string. An ABSENT key resolves to "off"
// here, which is why a jail whose config predates the key and one that sets it off are the
// same jail.
//
// It deliberately does not enum-check what it finds. config.validateMacosLog judges the
// value on the host (the key is in the schema since 2026-09-16 — before that every config
// declaring it was refused outright), and MacosLogWrapperScript rewrites anything it does
// not recognise to "off" downstream. A third check here would only shadow whichever of
// those two was wrong.
func macosLogMode(cfg *jsonx.OrderedMap) string {
	if cfg != nil {
		if v, ok := cfg.Get("macos_log"); ok {
			if s, ok := v.(string); ok {
				return s
			}
			// Non-string config value — rare; fall back to off, but
			// the container path only ever writes strings here.
		}
	}
	return "off"
}

// orderedMapToAny returns the OrderedMap as an `any` so jsonx.DumpsCompact
// encodes it (it accepts *OrderedMap directly).
func orderedMapToAny(m *jsonx.OrderedMap) any { return m }

// sourceLessHostFilesWire renders the merged config's SOURCE-LESS host_files
// entries as the YOLO_HOST_FILES wire string, or "" when there are none. The
// source-bearing half is deliberately excluded — it is unreachable from a pure
// function; see hostFilesWire.
func sourceLessHostFilesWire(cfg *jsonx.OrderedMap) string {
	wire, err := config.MarshalHostFiles(config.SourceLessHostFilesFrom(cfg))
	if err != nil {
		return ""
	}
	return wire
}

// hostFilesWire renders the WHOLE YOLO_HOST_FILES wire for this launch: the source-less
// entries this package can read from the merged config, plus the source-bearing entries
// the host CLI resolved (DP-L1), whose bytes it staged into the context tree when they
// existed.
//
// THE TWO HALVES CANNOT COME FROM ONE PLACE, and that is the feature. A source-less entry
// copies nothing from the host, so it is legal at any scope and the merged map is its
// proper source. A source-bearing entry names a host path — it can forward
// ~/.ssh/id_ed25519 — so it is read from the USER config directly and is inexpressible at
// workspace scope (config.SourceBearing). Reading the second half here would put a
// credential-boundary read inside a function whose contract is purity.
//
// SOURCE-BEARING WINS A DESTINATION COLLISION, which is config.LoadHostFiles' own rule
// for the same pair: it is the more specific declaration and it is the one the user wrote
// in their own config. Without the dedupe the same destination would be staged twice and
// the LAST loop iteration would decide, silently.
//
// Sorted by destination Path, like every other producer of this wire, so the bootstrap
// argv is deterministic.
func hostFilesWire(cfg *jsonx.OrderedMap, hostCtx HostContext) string {
	byPath := map[string]config.HostFileEntry{}
	var order []string
	for _, e := range config.SourceLessHostFilesFrom(cfg) {
		if _, seen := byPath[e.Path]; !seen {
			order = append(order, e.Path)
		}
		byPath[e.Path] = e
	}
	for _, e := range hostCtx.HostFiles {
		if !e.SourceBearing() {
			// Not reachable from the shipped caller, and dropped rather than trusted:
			// this field's contract is "entries whose SOURCE this launch staged", and a
			// source-less entry in it would be a second, unordered path to the same
			// destination the merged config already owns.
			continue
		}
		if _, seen := byPath[e.Path]; !seen {
			order = append(order, e.Path)
		}
		byPath[e.Path] = e
	}
	if len(order) == 0 {
		return ""
	}
	sort.Strings(order)
	entries := make([]config.HostFileEntry, 0, len(order))
	for _, p := range order {
		entries = append(entries, byPath[p])
	}
	wire, err := config.MarshalHostFiles(entries)
	if err != nil {
		return ""
	}
	return wire
}

// hostLayerWire is this launch's host-layer report, as the environment carries it.
//
// ONE PRODUCER FOR BOTH ANSWERS, keyed on the staged context root, because the two are a
// single fact seen from two sides: a launch that staged a tree DELIVERS host layers and
// must list what it put there, and a launch that staged none delivers nothing and must
// say the backend did not. Splitting them across two call sites is how a launch comes to
// claim `supported` while staging nothing — and the jail's read fails CLOSED on exactly
// that combination, so it would refuse every launch on this backend.
//
// Error-free for packload.HostLayersUnsupportedWire's reason: the report is a string and
// a []string, which cannot fail to marshal.
func hostLayerWire(ctxRoot string, hostCtx HostContext) string {
	if ctxRoot == "" {
		return packload.HostLayersUnsupportedWire()
	}
	wire, _ := packload.HostLayerReport{
		Delivery:  packload.HostLayersSupported,
		Delivered: hostCtx.Delivered,
	}.Marshal()
	return wire
}

// cfgStrList reads config[key] as a list of strings, dropping non-string
// entries. The container path has its own copy (internal/cli/run/cfgval.go);
// duplicating four lines is cheaper than exporting a config accessor across a
// package boundary that otherwise shares nothing.
func cfgStrList(cfg *jsonx.OrderedMap, key string) []string {
	v, ok := cfg.Get(key)
	if !ok || v == nil {
		return nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil
	}
	var out []string
	for _, e := range list {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// envFile and envFileCommands make a RunPlan a sandboxEnvPlan (envfile.go).
func (p RunPlan) envFile() (string, string) { return p.EnvFile, p.EnvFileContent }
func (p RunPlan) envFileCommands() ([][]string, [][]string) {
	return p.EnvFileCommands, p.EnvFileGrantCommands
}

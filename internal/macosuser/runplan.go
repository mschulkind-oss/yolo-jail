package macosuser

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
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
	PackRoot           string
	BootstrapArgv      []string
	LaunchArgv         []string
	GitIdentity        *jsonx.OrderedMap
	OffendingHome      string // "" when on neutral ground
	OffendingHomeSet   bool   // true when a home contains the workspace
	DarwinPathPrefix   []string
	DarwinEnv          *jsonx.OrderedMap
	DarwinSkipped      []string
	DarwinMaterialized bool
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
// their home-relative destinations (""=nothing to deliver); `blockedTools` are the
// selected packs' own blocked-tool declarations, merged with the config's security
// section (core blocks nothing by default). `darwin` may be nil.
func BuildRunPlan(workspace string, cfg *jsonx.OrderedMap, agents, agentArgv []string, selfExe, hostPackRoot, hostHomeOverlay string, sandboxEnv *jsonx.OrderedMap, darwin *Darwin, blockedTools []packload.BlockedTool) RunPlan {
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
	bootstrapEnv := buildBootstrapEnv(workspace, cfg, gitIdentity, sandboxEnv, packRoot,
		homeOverlay, SandboxHome(), darwinPrefix, blockedTools)

	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(workspace, "")

	return RunPlan{
		Workspace:   workspace,
		Cname:       cname,
		ProfilePath: profilePath,
		Seatbelt:    SeatbeltProfile(workspace, SandboxHome(), cfgStrList(cfg, "workspace_readonly")),
		StagedDir:   stateDir,
		StagedYolo:  stagedYolo,
		// Binary first, then the pack trees, then the content overlay: all three are
		// prerequisites of the bootstrap the caller runs immediately after this list,
		// and the binary is the one that fails most cheaply.
		StageCommands: append(append(StageBinaryCommands(selfExe, ""),
			StagePackCommands(hostPackRoot, cname, "")...),
			StageHomeOverlayCommands(hostHomeOverlay, cname, "")...),
		PackRoot:           packRoot,
		BootstrapArgv:      DarwinBootstrapArgv(stagedYolo, SandboxHome(), bootstrapEnv, ""),
		LaunchArgv:         LaunchArgv(agentArgv, profilePath, sandboxEnv, workspace, "", "", darwinPrefix),
		GitIdentity:        gitIdentity,
		OffendingHome:      offendingHome,
		OffendingHomeSet:   offendingSet,
		DarwinPathPrefix:   darwinPrefix,
		DarwinEnv:          darwinEnv,
		DarwinSkipped:      darwinSkipped,
		DarwinMaterialized: darwin != nil,
	}
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
// `packRoot` and `homeOverlay` are the ALREADY-STAGED destinations (StagedPackRoot,
// StagedHomeOverlay), not their host-side sources, and "" means the caller staged nothing of
// that kind. They are resolved by the caller rather than here because the caller is also what
// emits the commands that stage them, and the two must not be able to disagree.
//
// `blockedTools` are the selected packs' blocked-tool declarations. They are a PARAMETER
// because core blocks nothing by default since the guardrails pack took the rules over — the
// config's security section alone would render an empty YOLO_BLOCK_CONFIG and the generated
// home would carry no blockers at all.
func buildBootstrapEnv(workspace string, cfg, gitIdentity, sandboxEnv *jsonx.OrderedMap,
	packRoot, homeOverlay, home string, darwinPrefix []string,
	blockedTools []packload.BlockedTool) *jsonx.OrderedMap {
	bootstrapEnv := jsonx.NewOrderedMap()
	bootstrapEnv.Set("YOLO_HOST_DIR", resolvePathAbs(workspace))
	blockJSON, _ := jsonx.DumpsCompact(config.NormalizeBlockedToolsWith(securitySection(cfg), blockedTools))
	bootstrapEnv.Set("YOLO_BLOCK_CONFIG", blockJSON)
	miseJSON, _ := jsonx.DumpsCompact(orderedMapToAny(config.MergeMiseTools(cfg)))
	bootstrapEnv.Set("YOLO_MISE_TOOLS", miseJSON)
	lspJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "lsp_servers"))
	bootstrapEnv.Set("YOLO_LSP_SERVERS", lspJSON)
	mcpSrvJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "mcp_servers"))
	bootstrapEnv.Set("YOLO_MCP_SERVERS", mcpSrvJSON)
	mcpPresetsJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyList(cfg, "mcp_presets"))
	bootstrapEnv.Set("YOLO_MCP_PRESETS", mcpPresetsJSON)
	// The `agent_updates` policy. macos-user is the backend where missing this hides
	// least — it bakes no image, so the launchers ARE the delivery — and the one whose
	// single machine-wide home makes the install-prefix lock they carry reachable.
	bootstrapEnv.Set(entrypoint.AgentUpdatesEnv, config.AgentUpdatesWire())
	// git identity rides verbatim (the subcommand's Env.Vars carries it into
	// configureGit).
	for _, k := range gitIdentity.Keys() {
		v, _ := gitIdentity.Get(k)
		bootstrapEnv.Set(k, v)
	}
	// host_files: SOURCE-LESS entries only (config.SourceLessHostFiles). There is
	// no /ctx/host-user mount on this backend — there are no bind mounts at all —
	// so a source-bearing entry would render with an empty host layer and silently
	// serve its defaults instead of the host file the user named. Filtering them
	// out here keeps that an explicit, recorded deficiency
	// (docs/plans/host-file-staging.md "macos-user — accepted deficiencies")
	// rather than a half-working surprise.
	//
	// Read from the config map handed in, NOT via config.LoadHostFiles: the plan
	// builder is pure, and a source-less entry is legal at any scope so the merged
	// map is the right source for exactly this subset.
	if wire := sourceLessHostFilesWire(cfg); wire != "" {
		bootstrapEnv.Set("YOLO_HOST_FILES", wire)
	}

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

	// Darwin extras consumed by `yolo internal darwin-bootstrap`.
	bootstrapEnv.Set("YOLO_DARWIN_WORKSPACE", workspace)
	bootstrapEnv.Set("YOLO_DARWIN_MACOS_LOG", macosLogMode(cfg))
	bootstrapEnv.Set("YOLO_DARWIN_LOGIN_PATH", SandboxPath(home, darwinPrefix))
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
		if !stagesPackRoot(plan.StageCommands, plan.PackRoot) {
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

	// Acceptance-bar guard: darwin store bin dirs must reach the launch PATH.
	launchStr := strings.Join(plan.LaunchArgv, " ")
	for _, storeBin := range plan.DarwinPathPrefix {
		if !strings.Contains(launchStr, storeBin) {
			problems = append(problems,
				"darwin package bin dir "+storeBin+" did not reach the launch "+
					"PATH — declared tools would be silently missing")
		}
	}

	return problems
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

// stagesPackRoot reports whether the stage commands finish by moving a tree INTO
// packRoot — the last command StagePackCommands emits. Checking the destination of
// the final `mv` rather than merely "packRoot appears somewhere" is what makes the
// invariant meaningful: the path also appears in the preceding `rm -rf`, so a
// substring test would pass for a plan that deleted the tree and staged nothing.
func stagesPackRoot(cmds [][]string, packRoot string) bool {
	for _, c := range cmds {
		if len(c) >= 4 && c[0] == mvBin && c[len(c)-1] == packRoot {
			return true
		}
	}
	return false
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

// macosLogMode returns config["macos_log"] as a string, defaulting to "off".
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
// source-bearing half is deliberately excluded — see the call site.
func sourceLessHostFilesWire(cfg *jsonx.OrderedMap) string {
	wire, err := config.MarshalHostFiles(config.SourceLessHostFilesFrom(cfg))
	if err != nil {
		return ""
	}
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

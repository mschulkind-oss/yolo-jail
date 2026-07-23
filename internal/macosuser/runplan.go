package macosuser

import (
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
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
	StagedDir          string
	StagedYolo         string
	StageCommands      [][]string
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
// to self-exec as the bootstrap. `darwin` may be nil.
func BuildRunPlan(workspace string, cfg *jsonx.OrderedMap, agents, agentArgv []string, selfExe string, sandboxEnv *jsonx.OrderedMap, darwin *Darwin) RunPlan {
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

	// The bootstrap env baked onto the self-exec argv: the generator contract
	// the entrypoint reads (YOLO_HOST_DIR/BLOCK_CONFIG/MISE_TOOLS/LSP/MCP), the
	// git identity, YOLO_AGENTS (which agents to configure), and the three
	// YOLO_DARWIN_* extras the darwin-bootstrap subcommand consumes (workspace,
	// macos-log mode, and the login-rc PATH). Reuses the container-side resolvers.
	bootstrapEnv := jsonx.NewOrderedMap()
	bootstrapEnv.Set("YOLO_HOST_DIR", resolvePathAbs(workspace))
	blockJSON, _ := jsonx.DumpsCompact(config.NormalizeBlockedTools(securitySection(cfg)))
	bootstrapEnv.Set("YOLO_BLOCK_CONFIG", blockJSON)
	miseJSON, _ := jsonx.DumpsCompact(orderedMapToAny(config.MergeMiseTools(cfg)))
	bootstrapEnv.Set("YOLO_MISE_TOOLS", miseJSON)
	lspJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "lsp_servers"))
	bootstrapEnv.Set("YOLO_LSP_SERVERS", lspJSON)
	mcpSrvJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyMap(cfg, "mcp_servers"))
	bootstrapEnv.Set("YOLO_MCP_SERVERS", mcpSrvJSON)
	mcpPresetsJSON, _ := jsonx.DumpsCompact(getSectionOrEmptyList(cfg, "mcp_presets"))
	bootstrapEnv.Set("YOLO_MCP_PRESETS", mcpPresetsJSON)
	// YOLO_AGENTS = compact JSON list, matching the container's -e contract.
	agentsAny := make([]any, len(agents))
	for i, a := range agents {
		agentsAny[i] = a
	}
	agentsJSON, _ := jsonx.DumpsCompact(agentsAny)
	bootstrapEnv.Set("YOLO_AGENTS", agentsJSON)
	// git identity rides verbatim (the subcommand's Env.Vars carries it into
	// configureGit).
	for _, k := range gitIdentity.Keys() {
		v, _ := gitIdentity.Get(k)
		bootstrapEnv.Set(k, v)
	}
	// Darwin extras consumed by `yolo internal darwin-bootstrap`.
	bootstrapEnv.Set("YOLO_DARWIN_WORKSPACE", workspace)
	bootstrapEnv.Set("YOLO_DARWIN_MACOS_LOG", macosLogMode(cfg))
	bootstrapEnv.Set("YOLO_DARWIN_LOGIN_PATH", SandboxPath(SandboxHome(), darwinPrefix))

	stagedYolo := StagedYoloPath("")
	offendingHome, offendingSet := HomeContaining(workspace, "")

	return RunPlan{
		Workspace:          workspace,
		Cname:              cname,
		ProfilePath:        profilePath,
		Seatbelt:           SeatbeltProfile(workspace, SandboxHome()),
		StagedDir:          stateDir,
		StagedYolo:         stagedYolo,
		StageCommands:      StageBinaryCommands(selfExe, ""),
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

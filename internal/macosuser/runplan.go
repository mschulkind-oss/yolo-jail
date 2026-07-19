package macosuser

import (
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/naming"
)

// RunPlan is the fully-resolved, ordered artifacts + commands for one session.
// real gate rather than a pretty-printer.
type RunPlan struct {
	Workspace          string
	Cname              string
	ProfilePath        string
	Seatbelt           string
	Interp             string // "" when unresolved
	InterpResolved     bool   // Python's interp is not None
	InterpCandidates   []string
	StagedDir          string
	StageCommands      [][]string
	Bootstrap          string
	BootstrapPath      string
	BootstrapArgv      []string
	LaunchArgv         []string
	GitIdentity        *jsonx.OrderedMap
	OffendingHome      string // "" when on neutral ground
	OffendingHomeSet   bool   // Python's offending_home is not None
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

// BootstrapArgv returns `sudo --user=<sandbox> <interp> <boot>` — run the
// bootstrap as the sandbox. No --login/--set-home.
func BootstrapArgv(interp, bootPath, user string) []string {
	if user == "" {
		user = SandboxUser
	}
	return []string{"sudo", "--user=" + user, interp, bootPath}
}

// BuildRunPlan assembles the full RunPlan (pure — no shelling out). `config` is
// the loaded jail config; `sandboxEnv` is the fully-resolved launch env; interp
// is the resolved python3 ("" + interpResolved=false if none found). `darwin`
// may be nil.
func BuildRunPlan(workspace string, cfg *jsonx.OrderedMap, agents, agentArgv []string, repoSrc string, sandboxEnv *jsonx.OrderedMap, interp string, interpResolved bool, darwin *Darwin) RunPlan {
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
	// the separate path_prefix channel). Python: sandbox_env = {**sandbox_env,
	// **darwin_env}.
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
	bootstrapPath := filepath.Join(stateDir, "bootstrap-"+cname+".py")

	// git_identity = {k:v for k,v in sandbox_env if k startswith YOLO_GIT/YOLO_JJ}
	gitIdentity := jsonx.NewOrderedMap()
	if sandboxEnv != nil {
		for _, k := range sandboxEnv.Keys() {
			if strings.HasPrefix(k, "YOLO_GIT") || strings.HasPrefix(k, "YOLO_JJ") {
				v, _ := sandboxEnv.Get(k)
				gitIdentity.Set(k, v)
			}
		}
	}

	// A concrete interpreter string for the argv even when unresolved
	// (Python: interp or _PYTHON_CANDIDATES[-1]).
	interpStr := interp
	if !interpResolved {
		interpStr = pythonCandidates[len(pythonCandidates)-1]
	}

	// Config-derived env the entrypoint generators read
	// _entrypoint_preflight block). Reuses the container-side resolvers.
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

	boot := EntrypointBootstrapScript(
		repoSrc,
		workspace,
		SandboxHome(),
		agents,
		macosLogMode(cfg),
		gitIdentity,
		bootstrapEnv,
		darwinPrefix,
		"",
	)

	offendingHome, offendingSet := HomeContaining(workspace, "")

	return RunPlan{
		Workspace:          workspace,
		Cname:              cname,
		ProfilePath:        profilePath,
		Seatbelt:           SeatbeltProfile(workspace, SandboxHome()),
		Interp:             interp,
		InterpResolved:     interpResolved,
		InterpCandidates:   PythonCandidates(),
		StagedDir:          stateDir,
		StageCommands:      StageEntrypointCommands(repoSrc, ""),
		Bootstrap:          boot,
		BootstrapPath:      bootstrapPath,
		BootstrapArgv:      BootstrapArgv(interpStr, bootstrapPath, ""),
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

	// B2: a real interpreter must resolve, and never the bare stub first.
	if !plan.InterpResolved {
		problems = append(problems,
			"no python3 interpreter resolved for the sandbox user "+
				"(install Command Line Tools or a Homebrew/Nix python3)")
	}
	if len(plan.InterpCandidates) > 0 && plan.InterpCandidates[0] == "/usr/bin/python3" {
		problems = append(problems,
			"/usr/bin/python3 (the xcode-select stub risk) must not be the "+
				"first interpreter candidate")
	}

	// B3: the bootstrap must import from the root-owned staged dir.
	if !strings.Contains(plan.Bootstrap, StagedEntrypointDirParent(plan.StagedDir)) {
		problems = append(problems,
			"bootstrap does not import entrypoint from the staged state dir "+
				"("+plan.StagedDir+"); it would fail to import from a 0750 home")
	}

	// The workspace must be neutral ground — never inside a user's home.
	if plan.OffendingHomeSet {
		problems = append(problems,
			"workspace "+plan.Workspace+" is inside the home directory "+
				plan.OffendingHome+"; the macos-user backend shares only "+
				"neutral ground. Move it under "+SharedRootDefault()+" (or set "+
				"config `macos_shared_root` to another non-home path).")
	}

	// Git identity must reach the BOOTSTRAP env.
	for _, k := range plan.GitIdentity.Keys() {
		if !strings.Contains(plan.Bootstrap, k) {
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

// StagedEntrypointDirParent returns staged_entrypoint_dir(sd).parent, which is
// sd itself (used by the B3 invariant, matching the Python
// `str(staged_entrypoint_dir(...).parent) not in plan.bootstrap`).
func StagedEntrypointDirParent(sd string) string {
	if sd == "" {
		sd = stateDir
	}
	return sd
}

func cnameFor(workspace string) string {
	return cnameFn(workspace)
}

// cnameFn is a package var so the run orchestrator can share a single naming
// definition; defaults to naming.FromWorkspace.
var cnameFn = naming.FromWorkspace

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

// getSectionOrEmptyMap returns config[key] as an OrderedMap, or an empty one
// ). If the value is not a map, returns empty.
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

// getSectionOrEmptyList returns config[key] as a list, or [] (config.get(key, [])).
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

// macosLogMode returns str(config.get("macos_log", "off")).
func macosLogMode(cfg *jsonx.OrderedMap) string {
	if cfg != nil {
		if v, ok := cfg.Get("macos_log"); ok {
			if s, ok := v.(string); ok {
				return s
			}
			// str(x) of a non-string config value — rare; fall back to off, but
			// the container path only ever writes strings here.
		}
	}
	return "off"
}

// orderedMapToAny returns the OrderedMap as an `any` so jsonx.DumpsCompact
// encodes it (it accepts *OrderedMap directly).
func orderedMapToAny(m *jsonx.OrderedMap) any { return m }

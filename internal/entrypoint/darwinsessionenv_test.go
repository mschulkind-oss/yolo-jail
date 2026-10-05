package entrypoint

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// darwinsessionenv_test.go pins hydrate_session_env: the macos-user bootstrap reads the
// session env file the launch named on its argv (DarwinSessionEnvFileEnv) into its generator
// Env, so the MCP requires_env gate sees the environment the agent will have.

// writeSessionEnvFile writes body as a session env file and returns its path.
func writeSessionEnvFile(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(resolvedDir(t), "ws.env")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// gatedServerBootstrap runs the REAL bootstrap over every shipped pack with one workspace MCP
// server gated on GITHUB_TOKEN, the variable reaching the launch only through env_sources,
// and returns the rendered ~/.claude.json and the terminal. sessionEnv "" names no file.
func gatedServerBootstrap(t *testing.T, sessionEnv string) (claudeJSON, term string) {
	t.Helper()
	home, ws := resolvedDir(t), resolvedDir(t)
	vars := map[string]string{
		"JAIL_HOME":             home,
		"YOLO_PACK_ROOT":        stageShippedPacks(t),
		"YOLO_MCP_SERVERS":      `{"gh":{"command":"gh-mcp","requires_env":["GITHUB_TOKEN"]}}`,
		"YOLO_MCP_PRESETS":      `[]`,
		"YOLO_DARWIN_WORKSPACE": ws,
	}
	if sessionEnv != "" {
		vars[DarwinSessionEnvFileEnv] = writeSessionEnvFile(t, sessionEnv)
	}
	e := DarwinEnvFrom(vars, home)
	var out strings.Builder
	e.Stderr = &out
	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})
	raw, err := os.ReadFile(e.ClaudeJSONPath())
	if err != nil {
		t.Fatalf("the bootstrap rendered no ~/.claude.json, so nothing below is a result: %v\n%s",
			err, out.String())
	}
	return string(raw), out.String()
}

// THE MEASURED DEFECT, through the production entry: a server gated on a variable the launch
// carries only in the session env file is kept when the bootstrap is told about the file, and
// dropped (with the gate's notice) when it is not. Before hydrate_session_env the first case
// was the second: GITHUB_TOKEN reached the agent and not the gate.
//
// MUTATION: delete the hydrate_session_env step and the first half goes red.
func TestTheDarwinBootstrapKeepsAnMCPServerGatedOnASessionVariable(t *testing.T) {
	kept, term := gatedServerBootstrap(t, "export GITHUB_TOKEN='ghp_fake'\n")
	if !strings.Contains(kept, `"gh"`) {
		t.Errorf("the session env file carries GITHUB_TOKEN, and the gated server is still "+
			"missing from ~/.claude.json:\n%s\nterminal:\n%s", kept, term)
	}
	if strings.Contains(term, "'gh' skipped") {
		t.Errorf("the gate reported the variable missing although the session file sets it:\n%s", term)
	}

	dropped, term := gatedServerBootstrap(t, "")
	if strings.Contains(dropped, `"gh"`) {
		t.Errorf("with no session env file the gated server was rendered anyway:\n%s", dropped)
	}
	if !strings.Contains(term, "notice: MCP server 'gh' skipped — required env not set: GITHUB_TOKEN") {
		t.Errorf("the gate dropped the server without its notice:\n%s", term)
	}
}

// The bootstrap's own contract wins where both name a key, and nothing reaches the process
// environment: YOLO_VERSION there would make config.InJail answer true for an unconfined
// process, and a credential there would reach every child the bootstrap spawns.
func TestTheSessionEnvFileFillsOnlyTheGeneratorEnv(t *testing.T) {
	for _, k := range []string{"GITHUB_TOKEN", "SESSION_ONLY_PROBE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	before := os.Getenv("YOLO_VERSION")
	path := writeSessionEnvFile(t, strings.Join([]string{
		"export GITHUB_TOKEN='ghp_fake'",
		"export YOLO_MCP_SERVERS='{}'",
		"export YOLO_VERSION='9.9.9'",
		`export SESSION_ONLY_PROBE='it'\''s here'`,
		"# a comment, and a line that is not an export",
		"echo nope",
	}, "\n")+"\n")
	contract := `{"gh":{"command":"gh-mcp","requires_env":["GITHUB_TOKEN"]}}`
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": resolvedDir(t), "YOLO_MCP_SERVERS": contract, DarwinSessionEnvFileEnv: path,
	}, resolvedDir(t))
	e.Stderr = &strings.Builder{}

	hydrateEnvFromSessionEnvFile(e)

	if got := e.Getenv("GITHUB_TOKEN"); got != "ghp_fake" {
		t.Errorf("GITHUB_TOKEN = %q in the generator Env, want the session file's value", got)
	}
	if got := e.Getenv("SESSION_ONLY_PROBE"); got != "it's here" {
		t.Errorf("the session file's single-quote escape was not reversed: %q", got)
	}
	if got := e.Getenv("YOLO_MCP_SERVERS"); got != contract {
		t.Errorf("the session file overrode the bootstrap's own YOLO_MCP_SERVERS: %q", got)
	}
	if os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("SESSION_ONLY_PROBE") != "" {
		t.Error("hydration exported a session variable into the bootstrap's process environment")
	}
	if os.Getenv("YOLO_VERSION") != before {
		t.Error("hydration changed the process's YOLO_VERSION, the jail marker config.InJail reads")
	}
	if got := e.Getenv("YOLO_VERSION"); got != "" {
		t.Errorf("YOLO_VERSION = %q in the generator Env: a YOLO_ key is the launcher's, never the file's", got)
	}
	if !sharedMCPNames(e)["gh"] {
		t.Error("the gated server is not in the shared MCP table after hydration")
	}
}

// sharedMCPNames is the jail-wide MCP table as a set of server names.
func sharedMCPNames(e *Env) map[string]bool {
	out := map[string]bool{}
	tables := loadMCPTables(e)
	for _, k := range tables.shared.Keys() {
		out[k] = true
	}
	return out
}

// A file the launch named and the bootstrap cannot read is said, with the path: the gate then
// answers without the composed environment for this launch, and the line says what that costs.
func TestAnUnreadableSessionEnvFileIsSaid(t *testing.T) {
	missing := filepath.Join(resolvedDir(t), "gone.env")
	e := DarwinEnvFrom(map[string]string{"JAIL_HOME": resolvedDir(t), DarwinSessionEnvFileEnv: missing}, resolvedDir(t))
	var term strings.Builder
	e.Stderr = &term
	hydrateEnvFromSessionEnvFile(e)
	if !strings.Contains(term.String(), "could not read the session env file "+missing) ||
		!strings.Contains(term.String(), "Relaunch") {
		t.Errorf("an unreadable session env file was not reported with its path and next step:\n%s", term.String())
	}
}

// The container boot never runs the step: its composed environment is yolo-user-env.sh's.
func TestTheContainerBootDoesNotReadASessionEnvFile(t *testing.T) {
	s := mustBootStep(t, "hydrate_session_env")
	if s.excludedFrom(bootContainer) == "" {
		t.Error("hydrate_session_env runs on the container boot")
	}
	if s.excludedFrom(bootDarwin) != "" {
		t.Error("hydrate_session_env does not run on the macos-user bootstrap")
	}
	assertStepBefore(t, bootDarwin, "hydrate_session_env", "configure_pack_surfaces",
		"the requires_env gate in the surface render asks the hydrated environment")
}

// A COMPOSED FILE SETS NO LAUNCHER CONTRACT KEY. The session env file carries env_sources,
// the workspace's included, and the agent can write a dotenv file the workspace already lists,
// with no config prompt. So a key the bootstrap's argv lacks because the LAUNCHER did not set
// it must stay unset: an `export YOLO_PROGRAMS_AUTOPRUNE='1'` there turned on the destructive
// autoprune that only the user's own config may turn on, and the bootstrap deleted the orphan.
// Every YOLO_ name is the launcher's (YOLO_PACK_ROOT would load a pack tree of the agent's
// choosing outside the sandbox, YOLO_DARWIN_HOME_OVERLAY copy a tree of its choosing over the
// account home), and none is taken from the file.
//
// MUTATION: drop the launcherContractKey skip in hydrateEnvFromSessionEnvFile and the orphan
// is deleted.
func TestTheSessionEnvFileCannotTurnOnAutoprune(t *testing.T) {
	home, packRoot := catalogHome(t)
	ws, sidecar, _ := darwinSidecarFixture(t)
	pkg := filepath.Join(sidecar, "npm-global", "lib", "node_modules", "leftover-agent")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join(resolvedDir(t), "agent-overlay")
	if err := os.MkdirAll(filepath.Join(overlay, ".zshrc.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	session := writeSessionEnvFile(t, strings.Join([]string{
		"export " + OrphanAutopruneEnv + "='1'",
		"export YOLO_DARWIN_HOME_OVERLAY='" + overlay + "'",
		"export GITHUB_TOKEN='ghp_fake'",
	}, "\n")+"\n")
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot, "YOLO_DARWIN_WORKSPACE": ws,
		DarwinHomeSidecarEnv: sidecar, DarwinSessionEnvFileEnv: session,
	}, home)
	var term strings.Builder
	e.Stderr = &term

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	if _, err := os.Stat(pkg); err != nil {
		t.Errorf("a session env file turned autoprune on, and the orphan was deleted (err=%v)\n%s",
			err, term.String())
	}
	if strings.Contains(term.String(), autoprunePrefix) {
		t.Errorf("autoprune ran with no relay from the launcher:\n%s", term.String())
	}
	for _, k := range []string{OrphanAutopruneEnv, "YOLO_DARWIN_HOME_OVERLAY"} {
		if got := e.Getenv(k); got != "" {
			t.Errorf("%s = %q in the generator Env, taken from the session env file", k, got)
		}
	}
	if e.Getenv("GITHUB_TOKEN") != "ghp_fake" {
		t.Error("the launcher-key skip also dropped an ordinary env_sources value")
	}
}

// THE CONTAINER'S READER HAD THE SAME HOLE: an env_sources line is a def-form default, and a
// default sets a key the launch did not set — YOLO_PROGRAMS_AUTOPRUNE included, from a dotenv
// file the workspace lists. A def-form YOLO_ line is now skipped, in the boot's Env and in its
// process environment alike; the session's shell still sources the file for itself. A
// plain-form line is the launcher's own per-entry channel and still applies, its wire tables
// included.
//
// MUTATION: drop the launcherContractKey skip in hydrateEnvFromUserEnvFile and the orphan is
// deleted.
func TestAnEnvSourcesDefaultCannotTurnOnTheContainerAutoprune(t *testing.T) {
	t.Setenv(OrphanAutopruneEnv, "")
	os.Unsetenv(OrphanAutopruneEnv)
	t.Setenv("YOLO_USE_PROFILES", "")
	t.Setenv("ENV_SOURCES_PROBE", "")
	home, packRoot := catalogHome(t)
	seedNpm(t, home, "leftover-agent")
	writeTestUserEnv(t, home, strings.Join([]string{
		"export " + OrphanAutopruneEnv + "=${" + OrphanAutopruneEnv + ":-'1'}",
		"export ENV_SOURCES_PROBE=${ENV_SOURCES_PROBE:-'kept'}",
		EntryChannelSectionHeader,
		`export YOLO_USE_PROFILES='{"claude":"codex"}'`,
	}, "\n")+"\n")
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": packRoot})
	var term strings.Builder
	e.Stderr = &term

	hydrateEnvFromUserEnvFile(e)
	CatalogInstalledOrphans(e)

	if _, err := os.Stat(filepath.Join(home, ".npm-global", "lib", "node_modules", "leftover-agent")); err != nil {
		t.Errorf("an env_sources default turned autoprune on, and the orphan was deleted (err=%v)\n%s",
			err, term.String())
	}
	if e.Getenv(OrphanAutopruneEnv) != "" || os.Getenv(OrphanAutopruneEnv) != "" {
		t.Errorf("an env_sources default set %s (Env %q, process %q)", OrphanAutopruneEnv,
			e.Getenv(OrphanAutopruneEnv), os.Getenv(OrphanAutopruneEnv))
	}
	if e.Getenv("ENV_SOURCES_PROBE") != "kept" {
		t.Error("the launcher-key skip also dropped an ordinary env_sources default")
	}
	if got := e.Getenv("YOLO_USE_PROFILES"); got != `{"claude":"codex"}` {
		t.Errorf("the launcher's own plain-form channel line no longer applies: YOLO_USE_PROFILES = %q", got)
	}
}

// A VALUE THE CREDENTIAL GATE SCOPED TO ONE AGENT STAYS THAT AGENT'S IN THE MCP TABLES. On
// macos-user the session env file is the LAUNCHED agent's environment, so it carries that
// agent's scoped values too — the ones the launch also writes to its per-agent file
// (~/.config/yolo-agent-env/<agent>.sh). Hydrated into the shared view, a server gated on one
// was written into every agent's config and the "configured only for" notice was lost. Such a
// key leaves the shared view, and the agents whose own file sets it still get the server. Both
// of the writer's line shapes for a value nothing shares are covered: the def-form default, and
// the `case` each of two agents' files takes when both profiles compose the name (each lists
// the other's value). pi's file names something else, so pi stands for every agent without the
// value. A shared value one profile also composes is the other side of this rule
// (TestTheMacosUserMCPGateAnswersAsTheContainerDoes).
//
// MUTATION: make loadMCPTables ask e.Lookup instead of the scoped view, and zai is shared.
func TestASessionValueScopedToOneAgentStaysInItsMCPTable(t *testing.T) {
	for _, form := range []struct {
		name   string
		files  map[string]string
		holder string
	}{
		{"def-form", map[string]string{
			"claude": "export ZAI_API_KEY=${ZAI_API_KEY:-'k'}",
		}, "claude"},
		{"case-form", map[string]string{
			"claude": `case "${ZAI_API_KEY-}" in ''|'c') export ZAI_API_KEY='k' ;; esac`,
			"codex":  `case "${ZAI_API_KEY-}" in ''|'k') export ZAI_API_KEY='c' ;; esac`,
		}, "claude, codex"},
	} {
		t.Run(form.name, func(t *testing.T) {
			home := resolvedDir(t)
			for agent, line := range form.files {
				writeAgentEnvFile(t, home, agent, line+"\n")
			}
			writeAgentEnvFile(t, home, "pi", "export OTHER=${OTHER:-'o'}\n")
			session := writeSessionEnvFile(t, "export ZAI_API_KEY='k'\nexport GITHUB_TOKEN='ghp_fake'\n")
			e := DarwinEnvFrom(map[string]string{
				"JAIL_HOME": home,
				"YOLO_MCP_SERVERS": `{"zai":{"command":"zai-mcp","requires_env":["ZAI_API_KEY"]},` +
					`"gh":{"command":"gh-mcp","requires_env":["GITHUB_TOKEN"]}}`,
				DarwinSessionEnvFileEnv: session,
			}, home)
			var term strings.Builder
			e.Stderr = &term

			hydrateEnvFromSessionEnvFile(e)
			tables := loadMCPTables(e)

			if _, ok := tables.shared.Get("zai"); ok {
				t.Errorf("a server gated on a scoped value is in the shared MCP table\n%s", term.String())
			}
			if _, ok := tables.perAgent["pi"].Get("zai"); ok {
				t.Errorf("pi's table carries a server gated on another agent's scoped value\n%s", term.String())
			}
			for agent := range form.files {
				if _, ok := tables.perAgent[agent].Get("zai"); !ok {
					t.Errorf("%s's own table lost the server its file's value satisfies\n%s", agent, term.String())
				}
			}
			if !strings.Contains(term.String(), "notice: MCP server 'zai' configured only for "+form.holder+" ") {
				t.Errorf("the per-agent notice does not name %s:\n%s", form.holder, term.String())
			}
			if _, ok := tables.shared.Get("gh"); !ok {
				t.Errorf("the shared env_sources value no agent file names was dropped from the shared table\n%s",
					term.String())
			}
		})
	}
}

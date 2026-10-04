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
	for _, k := range []string{"GITHUB_TOKEN", "YOLO_SESSION_ONLY_PROBE"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	before := os.Getenv("YOLO_VERSION")
	path := writeSessionEnvFile(t, strings.Join([]string{
		"export GITHUB_TOKEN='ghp_fake'",
		"export YOLO_MCP_SERVERS='{}'",
		"export YOLO_VERSION='9.9.9'",
		`export YOLO_SESSION_ONLY_PROBE='it'\''s here'`,
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
	if got := e.Getenv("YOLO_SESSION_ONLY_PROBE"); got != "it's here" {
		t.Errorf("the session file's single-quote escape was not reversed: %q", got)
	}
	if got := e.Getenv("YOLO_MCP_SERVERS"); got != contract {
		t.Errorf("the session file overrode the bootstrap's own YOLO_MCP_SERVERS: %q", got)
	}
	if os.Getenv("GITHUB_TOKEN") != "" || os.Getenv("YOLO_SESSION_ONLY_PROBE") != "" {
		t.Error("hydration exported a session variable into the bootstrap's process environment")
	}
	if os.Getenv("YOLO_VERSION") != before {
		t.Error("hydration changed the process's YOLO_VERSION, the jail marker config.InJail reads")
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

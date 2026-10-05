package entrypoint

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// darwinmcpview_test.go pins scopedMCPView: on macos-user the MCP requires_env gate reads the
// session env file, which is the LAUNCHED agent's environment, and must still answer for every
// table as the container boot does from the shared composition. The per-agent files below are
// written in their writer's grammar (internal/cli/run's agentEnvFileContent): a def-form line for
// an env_sources value or a composed value whose name yolo set nowhere else, and a `case` over
// the values yolo set elsewhere when it did.

// mcpViewScenario is one composition, as each backend's boot receives it.
type mcpViewScenario struct {
	name string
	// files are the per-agent env files the launch wrote, by agent.
	files map[string]string
	// container is the container boot's environment for K: the shared composition, which
	// hydrate_user_env reads from yolo-user-env.sh. "" leaves K unset.
	container string
	// session is K's value in the macos-user session env file: launchEnv of the launched program,
	// the shared composition plus that program's own delivery. "" leaves K out, unless
	// sessionEmpty writes it empty.
	session      string
	sessionEmpty bool
	// want is whether the server gated on K is in each table: "shared" and every agent with a file.
	want map[string]bool
	// darwinDiffers marks a scenario where macos-user is known to withhold what the container
	// grants (the one shape scopedMCPView documents); want is then the container's answer.
	darwinDiffers []string
}

const mcpViewServers = `{"s":{"command":"s-mcp","requires_env":["K"]}}`

// mcpViewTables runs the gate over one backend's boot and reports, per table, whether it holds
// the server.
func mcpViewTables(t *testing.T, sc mcpViewScenario, darwin bool) map[string]bool {
	t.Helper()
	home := resolvedDir(t)
	for agent, body := range sc.files {
		writeAgentEnvFile(t, home, agent, body+"\n")
	}
	var e *Env
	if darwin {
		vars := map[string]string{"JAIL_HOME": home, "YOLO_MCP_SERVERS": mcpViewServers}
		body := "export UNRELATED='x'\n"
		if sc.session != "" || sc.sessionEmpty {
			body += "export K='" + sc.session + "'\n"
		}
		vars[DarwinSessionEnvFileEnv] = writeSessionEnvFile(t, body)
		e = DarwinEnvFrom(vars, home)
		e.Stderr = &strings.Builder{}
		hydrateEnvFromSessionEnvFile(e)
	} else {
		vars := map[string]string{"JAIL_HOME": home, "YOLO_MCP_SERVERS": mcpViewServers}
		if sc.container != "" {
			vars["K"] = sc.container
		}
		e = NewEnv(vars)
		e.Stderr = &strings.Builder{}
	}
	tables := loadMCPTables(e)
	got := map[string]bool{}
	_, got["shared"] = tables.shared.Get("s")
	for agent, table := range tables.perAgent {
		_, got[agent] = table.Get("s")
	}
	return got
}

// THE MACOS-USER GATE ANSWERS AS THE CONTAINER'S DOES, table by table, over one composition per
// shape the writer produces. The first scenario is the review's probe: a shared env_sources
// value that one agent's profile also composes, which the writer spells as a `case` line in that
// agent's file. Before scopedMCPView read the files' grammar it dropped any session key some
// file named, so that server left every agent's config on macos-user while every agent's
// environment carried the variable.
//
// MUTATION: make sharedValueInAgentFiles return ("", named) without reading the `case` lines,
// and the shared scenarios go red on macos-user; make agentEnvLookup skip `case` lines, and the
// per-agent answers go red on both backends.
func TestTheMacosUserMCPGateAnswersAsTheContainerDoes(t *testing.T) {
	for _, sc := range []mcpViewScenario{
		{
			name: "a shared value one profile also composes, launching another agent",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'us-east-1') export K='us-west-2' ;; esac`,
			},
			container: "us-east-1", session: "us-east-1",
			want: map[string]bool{"shared": true, "claude": true},
		},
		{
			name: "a shared value one profile also composes, launching that agent",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'shared') export K='mine' ;; esac`,
			},
			container: "shared", session: "mine",
			want: map[string]bool{"shared": true, "claude": true},
		},
		{
			// The view holds the shared value itself, not the session's: here the launched
			// agent's own value is empty, which the gate reads as unset.
			name: "a profile that composes an empty value over a shared name, launching that agent",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'shared') export K='' ;; esac`,
			},
			container: "shared", sessionEmpty: true,
			want: map[string]bool{"shared": true, "claude": false},
		},
		{
			name: "a value the gate scoped to the launched agent alone",
			files: map[string]string{
				"claude": `export K=${K:-'k'}`,
				"codex":  `export OTHER=${OTHER:-'o'}`,
			},
			session: "k",
			want:    map[string]bool{"shared": false, "claude": true, "codex": false},
		},
		{
			name: "a name two profiles compose and nothing shares",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'c') export K='k' ;; esac`,
				"codex":  `case "${K-}" in ''|'k') export K='c' ;; esac`,
				"pi":     `export OTHER=${OTHER:-'o'}`,
			},
			session: "k",
			want:    map[string]bool{"shared": false, "claude": true, "codex": true, "pi": false},
		},
		{
			name: "a profile that removes a shared name",
			files: map[string]string{
				"claude": `case "${K-}" in 'shared') unset K ;; esac`,
			},
			container: "shared", session: "shared",
			want: map[string]bool{"shared": true, "claude": false},
		},
		{
			name: "a shared name two profiles compose, launching a third",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'p'|'shared') export K='c' ;; esac`,
				"pi":     `case "${K-}" in ''|'c'|'shared') export K='p' ;; esac`,
			},
			container: "shared", session: "shared",
			want: map[string]bool{"shared": true, "claude": true, "pi": true},
		},
		{
			name: "a shared name two profiles compose, launching one of them",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'p'|'shared') export K='c' ;; esac`,
				"pi":     `case "${K-}" in ''|'c'|'shared') export K='p' ;; esac`,
			},
			container: "shared", session: "c",
			want: map[string]bool{"shared": true, "claude": true, "pi": true},
		},
		{
			// The one shape the files cannot settle: both profiles compose the shared value
			// itself, so each `case` lists only a value the other agent's file sets too. The
			// view withholds, never grants: the agents whose own file sets K keep the server.
			name: "every listed value is also another agent's own",
			files: map[string]string{
				"claude": `case "${K-}" in ''|'v') export K='v' ;; esac`,
				"pi":     `case "${K-}" in ''|'v') export K='v' ;; esac`,
			},
			container: "v", session: "v",
			want:          map[string]bool{"shared": true, "claude": true, "pi": true},
			darwinDiffers: []string{"shared"},
		},
	} {
		t.Run(sc.name, func(t *testing.T) {
			container := mcpViewTables(t, sc, false)
			darwin := mcpViewTables(t, sc, true)
			for _, table := range sortedTables(sc.want) {
				if container[table] != sc.want[table] {
					t.Errorf("container: %s holds the server = %v, want %v", table, container[table], sc.want[table])
				}
				want := sc.want[table]
				if contains(sc.darwinDiffers, table) {
					want = false
				}
				if darwin[table] != want {
					t.Errorf("macos-user: %s holds the server = %v, want %v (container %v)",
						table, darwin[table], want, container[table])
				}
			}
		})
	}
}

// sortedTables is m's keys, sorted, so a failure lists the tables in one order.
func sortedTables(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// THROUGH THE PRODUCTION ENTRY: the review's probe, run as the real bootstrap over the shipped
// claude and codex packs. AWS_REGION is a shared env_sources value, and claude's profile also
// composes it, so claude's file holds the writer's `case` line. Launching codex, the session env
// file carries the shared value. Both configs must name the server: codex's from the shared
// table, claude's from its own.
func TestTheDarwinBootstrapKeepsAServerGatedOnASharedNameAProfileComposes(t *testing.T) {
	home, ws := resolvedDir(t), resolvedDir(t)
	writeAgentEnvFile(t, home, "claude",
		`case "${AWS_REGION-}" in ''|'us-east-1') export AWS_REGION='us-west-2' ;; esac`+"\n")
	e := DarwinEnvFrom(map[string]string{
		"JAIL_HOME":             home,
		"YOLO_PACK_ROOT":        stageShippedPacks(t),
		"YOLO_MCP_SERVERS":      `{"aws":{"command":"aws-mcp","requires_env":["AWS_REGION"]}}`,
		"YOLO_MCP_PRESETS":      `[]`,
		"YOLO_DARWIN_WORKSPACE": ws,
		DarwinSessionEnvFileEnv: writeSessionEnvFile(t, "export AWS_REGION='us-east-1'\n"),
	}, home)
	var term strings.Builder
	e.Stderr = &term

	_ = RunDarwinBootstrap(e, DarwinBootstrapOptions{MacosLog: "off"})

	claudeJSON, err := os.ReadFile(e.ClaudeJSONPath())
	if err != nil {
		t.Fatalf("the bootstrap rendered no ~/.claude.json: %v\n%s", err, term.String())
	}
	if !strings.Contains(string(claudeJSON), `"aws"`) {
		t.Errorf("claude's environment carries AWS_REGION, and its config does not name the server:\n%s\n%s",
			claudeJSON, term.String())
	}
	codexCfg, err := os.ReadFile(filepath.Join(e.CodexDir(), "config.toml"))
	if err != nil {
		t.Fatalf("the bootstrap rendered no codex config: %v\n%s", err, term.String())
	}
	if !strings.Contains(string(codexCfg), "[mcp_servers.aws]") {
		t.Errorf("codex's environment carries the shared AWS_REGION, and its config does not name "+
			"the server:\n%s\n%s", codexCfg, term.String())
	}
	if strings.Contains(term.String(), "'aws' skipped") || strings.Contains(term.String(), "'aws' configured only") {
		t.Errorf("the gate reported the shared variable missing for some agent:\n%s", term.String())
	}
}

// agentEnvLookup applies the writer's `case` lines: an export overrides an empty value or one the
// line lists and keeps any other (the user's), an unset removes only a listed value, and a
// pattern's escaped single quote is read back.
func TestAgentEnvLookupAppliesTheCaseLines(t *testing.T) {
	home := resolvedDir(t)
	e := &Env{Home: home, Vars: map[string]string{
		"INHERITED": "yolo-set", "USERS": "my-own", "DROP": "yolo-set", "KEEP": "my-own", "QUOTED": "it's",
	}}
	writeAgentEnvFile(t, home, "a", strings.Join([]string{
		`case "${EMPTY-}" in ''|'yolo-set') export EMPTY='profile' ;; esac`,
		`case "${INHERITED-}" in ''|'yolo-set') export INHERITED='profile' ;; esac`,
		`case "${USERS-}" in ''|'yolo-set') export USERS='profile' ;; esac`,
		`case "${DROP-}" in 'yolo-set') unset DROP ;; esac`,
		`case "${KEEP-}" in 'yolo-set') unset KEEP ;; esac`,
		`case "${QUOTED-}" in ''|'it'\''s') export QUOTED='a'\''b' ;; esac`,
		`case "${MISMATCH-}" in '') export OTHER='no' ;; esac`,
	}, "\n")+"\n")
	lookup := agentEnvLookup(e, "a")
	for key, want := range map[string]string{
		"EMPTY": "profile", "INHERITED": "profile", "USERS": "my-own", "KEEP": "my-own", "QUOTED": "a'b",
	} {
		if got, ok := lookup(key); !ok || got != want {
			t.Errorf("%s = %q (%v), want %q", key, got, ok, want)
		}
	}
	for _, key := range []string{"DROP", "OTHER"} {
		if v, ok := lookup(key); ok {
			t.Errorf("%s = %q, want it unset", key, v)
		}
	}
}

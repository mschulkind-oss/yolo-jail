package entrypoint

// mcpserverlauncher_test.go pins what a lazy launcher owes a program whose STDOUT IS A PROTOCOL.
// A pack's `mcp` server (packdecl.KindMCP) runs its program through that program's launcher, and
// an MCP client reads the server's stdout as JSON-RPC from its first byte: an install's chatter
// there ("added 1 package in 966ms", measured through the chrome-devtools pack's launcher on a
// cold home) is two non-JSON lines ahead of the initialize response. The same bytes break any
// pipeline a launched program sits in, so the rule is every launcher's and not the MCP case's: an
// install writes to stderr, and stdout carries the program's output alone.
//
// And it pins the transitive refresh's half (program-delivery.md §3.5, OQ-PD12a): a server takes
// the trigger of the agent that connects to it, so an agent's launcher installs or refreshes a
// pack server's npm program BEFORE it execs, and the server's own launcher, which the agent starts
// at connect time, moves nothing on its own invocation.

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// splitRun runs script with env, returning its stdout and stderr apart: the measurement is which
// stream each byte reached, which CombinedOutput cannot say.
func splitRun(t *testing.T, script string, env []string, args ...string) (string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(script, args...)
	cmd.Env = env
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\nstdout:\n%s\nstderr:\n%s", filepath.Base(script), err, stdout.String(), stderr.String())
	}
	return stdout.String(), stderr.String()
}

// chattyFakeNpm makes the probe's fake npm say what a real `npm install` says, on BOTH streams, before
// it installs; `npm view` stays quiet on stdout, since the launcher reads its answer from there.
func chattyFakeNpm(t *testing.T, dir string) {
	t.Helper()
	real := filepath.Join(dir, "npm.real")
	if err := os.Rename(filepath.Join(dir, "npm"), real); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(dir, "npm"), `#!/bin/bash
if [ "${1:-}" = install ]; then
    echo "added 1 package in 966ms"
    echo "npm warn deprecated something" >&2
fi
exec "`+real+`" "$@"
`)
	if err := os.Chmod(filepath.Join(dir, "npm"), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestALaunchersInstallKeepsStdoutForTheProgram(t *testing.T) {
	t.Run("npm program, cold home", func(t *testing.T) {
		p := newNpmProbe(t, "probetool")
		chattyFakeNpm(t, p.fakeBin)
		script := filepath.Join(p.home, "probetool")
		body := npmAgentLauncher("probe", &packdecl.Install{Kind: "npm", Bin: "probetool", Package: "probetool"},
			filepath.Join(p.home, "stamps"), p.receiptsPath, true, launcherServers{}, nil)
		writeTestFile(t, script, body)
		if err := os.Chmod(script, 0o755); err != nil {
			t.Fatal(err)
		}
		env := []string{"HOME=" + p.home, "PATH=" + p.fakeBin + ":" + os.Getenv("PATH")}
		stdout, stderr := splitRun(t, script, env, "--stdio")
		if stdout != "RAN --stdio\n" {
			t.Errorf("stdout = %q, want the program's output alone; the install wrote to it", stdout)
		}
		if !strings.Contains(stderr, "added 1 package") {
			t.Errorf("npm's output is lost rather than moved to stderr:\n%s", stderr)
		}

		// THE HOURLY UPDATE takes the same install, and must keep the same stream.
		p.agePastInterval(t, "probetool")
		stdout, stderr = splitRun(t, script, append(env, "FAKE_LATEST=9.9.9"), "--stdio")
		if stdout != "RAN --stdio\n" {
			t.Errorf("after an evergreen update, stdout = %q, want the program's output alone", stdout)
		}
		if !strings.Contains(stderr, "Updating probetool") || !strings.Contains(stderr, "added 1 package") {
			t.Errorf("the update did not run, or its output is lost:\n%s", stderr)
		}
	})

	t.Run("installer program, cold home", func(t *testing.T) {
		if _, err := exec.LookPath("curl"); err != nil {
			t.Skip("curl not found")
		}
		home := t.TempDir()
		url := serveBody(t, 200, "text/x-shellscript", `#!/bin/sh
echo "installer: downloading probetool"
echo "installer: a warning" >&2
mkdir -p "$HOME/.local/bin"
printf '#!/bin/sh\necho RAN "$@"\n' > "$HOME/.local/bin/probetool"
chmod +x "$HOME/.local/bin/probetool"
`)
		body := nativeAgentLauncher("probe", &packdecl.Install{Kind: "native", Bin: "probetool", InstallerURL: url},
			filepath.Join(home, "stamps"), filepath.Join(home, "receipts.jsonl"), "", true, launcherServers{}, nil)
		script := filepath.Join(home, "launcher")
		writeTestFile(t, script, body)
		if err := os.Chmod(script, 0o755); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := splitRun(t, script, []string{"HOME=" + home, "PATH=" + os.Getenv("PATH")}, "--stdio")
		if stdout != "RAN --stdio\n" {
			t.Errorf("stdout = %q, want the program's output alone; the installer wrote to it", stdout)
		}
		if !strings.Contains(stderr, "installer: downloading probetool") {
			t.Errorf("the installer's output is lost rather than moved to stderr:\n%s", stderr)
		}
	})

	t.Run("pnpm, cold home", func(t *testing.T) {
		p := newNpmProbe(t, "pnpm")
		chattyFakeNpm(t, p.fakeBin)
		e := NewEnv(map[string]string{"JAIL_HOME": p.home, "YOLO_WORKSPACE": filepath.Join(p.home, "ws")})
		if err := GeneratePackageManagerLaunchers(e); err != nil {
			t.Fatal(err)
		}
		stdout, stderr := splitRun(t, filepath.Join(e.LaunchDir(), "pnpm"),
			[]string{"HOME=" + p.home, "PATH=" + p.fakeBin + ":" + os.Getenv("PATH")}, "--stdio")
		if stdout != "RAN --stdio\n" {
			t.Errorf("stdout = %q, want pnpm's output alone; the install wrote to it", stdout)
		}
		if !strings.Contains(stderr, "added 1 package") {
			t.Errorf("npm's output is lost rather than moved to stderr:\n%s", stderr)
		}
	})
}

// --- the transitive refresh -----------------------------------------------------------------

// chromeServerEnv is a jail home with every shipped pack staged and the composed table a launch
// hands the jail, with the user's mcp_servers (JSON, or "" for none) over the chrome-devtools
// pack's entry; wire false is a launch that composed no table at all.
func chromeServerEnv(t *testing.T, user string, wire bool) *Env {
	t.Helper()
	home := resolvedDir(t)
	vars := map[string]string{"JAIL_HOME": home, "YOLO_PACK_ROOT": stageShippedPacks(t)}
	if wire {
		var u *jsonx.OrderedMap
		if user != "" {
			v, err := jsonx.Decode([]byte(user))
			if err != nil {
				t.Fatal(err)
			}
			u = v.(*jsonx.OrderedMap)
		}
		vars["YOLO_MCP_SERVERS"] = composedChromeWire(t, home, u)
	}
	return NewEnv(vars)
}

// THE SET: a pack server's npm program joins the presets' packages when the composed table holds
// the server, and not when your null removed it or no table names it; a preset and a pack naming
// one package install it once.
func TestTheServerSetCarriesAPackServersNpmProgram(t *testing.T) {
	jailPacks := func(t *testing.T, e *Env) []*packload.Pack {
		t.Helper()
		packs, err := LoadJailPacks(e)
		if err != nil {
			t.Fatal(err)
		}
		return packs
	}
	for _, tc := range []struct {
		name, user string
		wire       bool
		want       string
	}{
		{"held", "", true, "chrome-devtools-mcp"},
		{"your override keeps it", `{"chrome-devtools":{"env":{"X":"1"}}}`, true, "chrome-devtools-mcp"},
		{"your null removes it", `{"chrome-devtools":null}`, true, ""},
		// Not an entry, so the render starts no server under the name (validation refuses it on
		// the host; a jail renders what it is handed).
		{"a value of yours that is no entry", `{"chrome-devtools":"oops"}`, true, ""},
		{"no composed table", "", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := chromeServerEnv(t, tc.user, tc.wire)
			if got := ServerRefreshSpecs(e, jailPacks(t, e)).npm; got != tc.want {
				t.Errorf("the refresh set = %q, want %q", got, tc.want)
			}
		})
	}
	t.Run("a preset of the same package", func(t *testing.T) {
		e := chromeServerEnv(t, "", true)
		e.Vars["YOLO_MCP_PRESETS"] = `["chrome-devtools"]`
		if got := ServerRefreshSpecs(e, jailPacks(t, e)).npm; got != "chrome-devtools-mcp" {
			t.Errorf("the refresh set = %q, want the one package once", got)
		}
	})
}

// Only an npm program with no install flags is the refresh's: the baked list carries a spec and
// nothing else, so an installer program, or an npm one whose pack declares install flags, is left
// to its own launcher, as is a server whose `bin` no pack installs.
func TestTheServerSetLeavesWhatItCannotInstallToTheProgramsLauncher(t *testing.T) {
	dir := resolvedDir(t)
	writeTestFile(t, filepath.Join(dir, "pack.json"), `{"name":"acme","contributes":[
		{"kind":"program","bin":"plain-mcp","via":"npm","package":"plain-mcp-pkg@1.2.3"},
		{"kind":"program","bin":"flagged-mcp","via":"npm","package":"flagged-mcp-pkg","flags":["--legacy-peer-deps"]},
		{"kind":"program","bin":"native-mcp","via":"installer","url":"https://example.invalid/i.sh"},
		{"kind":"mcp","name":"plain","bin":"plain-mcp","config":{"command":"plain-mcp"}},
		{"kind":"mcp","name":"flagged","bin":"flagged-mcp","config":{"command":"flagged-mcp"}},
		{"kind":"mcp","name":"native","bin":"native-mcp","config":{"command":"native-mcp"}},
		{"kind":"mcp","name":"elsewhere","bin":"nobody-installs-me","config":{"command":"x"}}]}`)
	p, problems := packload.LoadDir(dir, "acme")
	if len(problems) > 0 || p == nil {
		t.Fatalf("loading the fixture: %v", problems)
	}
	packs := []*packload.Pack{p}
	e := NewEnv(map[string]string{"JAIL_HOME": dir,
		"YOLO_MCP_SERVERS": composedWire(t, packload.ComposeMCPServers(nil, packs, dir))})
	if got := ServerRefreshSpecs(e, packs).npm; got != "plain-mcp-pkg@1.2.3" {
		t.Errorf("the refresh set = %q, want the plain npm program's declared spec alone", got)
	}
}

func composedWire(t *testing.T, table *jsonx.OrderedMap) string {
	t.Helper()
	wire, err := jsonx.DumpsCompact(table)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

// THE CALL SITE: the launchers GenerateAgentLaunchers writes bake that set — every agent's carries
// the server's program, so it is installed before the agent execs — and the server's own launcher
// carries no refresh and no update of its own. With your null, nothing changes from a jail with no
// server: the program's launcher keeps its own trigger.
func TestTheLaunchersBakeAPackServersProgramIntoTheAgentsRefresh(t *testing.T) {
	line := func(name, value string) string { return "\n" + name + "=" + shquote.Quote(value) + "\n" }
	for _, tc := range []struct {
		name, user                     string
		agentServers, serverOwnUpdates string
	}{
		{"held", "", "chrome-devtools-mcp", "0"},
		{"your null removes it", `{"chrome-devtools":null}`, "", "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := chromeServerEnv(t, tc.user, true)
			runLaunchDirPasses(t, e)
			enabled := "1"
			if tc.agentServers == "" {
				enabled = "0"
			}
			// One npm agent and one installer agent: the two templates an agent takes.
			for _, agent := range []string{"opencode", "claude"} {
				body := readFileString(t, filepath.Join(e.LaunchDir(), agent))
				for _, want := range []string{line("SERVERS_NPM", tc.agentServers), line("SERVERS_ENABLED", enabled)} {
					if !strings.Contains(body, want) {
						t.Errorf("%s's launcher lacks %q", agent, want)
					}
				}
			}
			server := readFileString(t, filepath.Join(e.LaunchDir(), "chrome-devtools-mcp"))
			for _, want := range []string{line("SERVERS_ENABLED", "0")} {
				if !strings.Contains(server, want) {
					t.Errorf("the server's launcher lacks %q", want)
				}
			}
			if want := "\nUPDATES_ENABLED=" + shquote.Quote(tc.serverOwnUpdates) + "\n"; !strings.Contains(server, want) {
				t.Errorf("the server's launcher lacks %q: a server moves on its agent's trigger only "+
					"while an agent's refresh carries it", want)
			}
		})
	}
}

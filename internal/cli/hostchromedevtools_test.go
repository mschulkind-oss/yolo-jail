package cli

// hostchromedevtools_test.go pins the chrome-devtools pack at the HOST notch
// (docs/design/host-computed-layer.md HC-D26 to HC-D28, revising HC-D6 and HC-D16): the real
// `yolo host apply --assert` composes the pack's `mcp` entry under your mcp_servers, joined to
// your real home, into claude's, copilot's and codex's files; writes the wrapper the entry runs;
// provisions chrome-devtools-mcp into yolo's floor from a fake Node distribution; and a run of the
// wrapper it wrote reaches the floor's copy with none of the jail's chrome flags. A `yolo host --
// <agent>` launch ensures the program too, saying what happens when it cannot. Every test goes
// through a real front door in a temp HOME, with no network.

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

const hostChromeWrapperRel = ".local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper"

// chromeHostConfig selects three agents and the chrome-devtools pack, with only the latter's
// program in the floor (the agents are stubs on PATH), plus extra keys. It declares
// `host_management: "own"`: an unset key is "none" since the `assert` retirement (OQ-CO14), and
// nothing renders into the home under it.
func chromeHostConfig(extra string) string {
	return `{"packs":["claude","copilot","codex","chrome-devtools"],"host_management":"own",` +
		`"host_floor":{"*":false,"chrome-devtools":true}` + extra + `}`
}

func hostApplyAssert(t *testing.T) string {
	t.Helper()
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"apply", "--assert"}, &out, &errw, false, strings.NewReader("y\ny\ny\n")); rc != 0 {
		t.Fatalf("yolo host apply --assert rc=%d\n%s%s", rc, out.String(), errw.String())
	}
	return out.String() + errw.String()
}

func TestYoloHostApplyWritesTheChromeDevtoolsServerForEveryAgent(t *testing.T) {
	home := hostComputedHome(t, chromeHostConfig(""))
	dist := withTestFloor(t)
	dist.Publish("chrome-devtools-mcp", "1.2.3", "bin=chrome-devtools-mcp")
	verboseReport(t) // the composition's detail line, which names what the pack carried
	report := hostApplyAssert(t)
	wrapper := filepath.Join(home, hostChromeWrapperRel)

	claude, _ := readJSONAt(t, home, ".claude.json")["mcpServers"].(map[string]any)
	copilot, _ := readJSONAt(t, home, ".copilot/mcp-config.json")["mcpServers"].(map[string]any)
	for agent, servers := range map[string]map[string]any{"claude": claude, "copilot": copilot} {
		entry, _ := servers["chrome-devtools"].(map[string]any)
		args, _ := entry["args"].([]any)
		if entry["command"] != "/bin/sh" || len(args) != 1 || args[0] != wrapper {
			t.Errorf("%s's MCP file does not run the pack's wrapper in your home: %v\n%s", agent, servers, report)
		}
	}
	codex, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil || !strings.Contains(string(codex), "chrome-devtools") || !strings.Contains(string(codex), wrapper) {
		t.Errorf("codex's config.toml does not carry the server (err %v):\n%s", err, codex)
	}
	for _, file := range []string{".claude.json", ".copilot/mcp-config.json", ".codex/config.toml"} {
		raw, _ := os.ReadFile(filepath.Join(home, file))
		for _, jailOnly := range []string{"--no-sandbox", "--headless", "/home/agent"} {
			if strings.Contains(string(raw), jailOnly) {
				t.Errorf("%s carries the jail-only %q at the host", file, jailOnly)
			}
		}
	}
	if _, err := os.Stat(wrapper); err != nil {
		t.Errorf("the wrapper the entry runs was not written into your home: %v", err)
	}
	if !strings.Contains(report, "chrome-devtools (pack chrome-devtools)") {
		t.Errorf("the report does not name the server the pack carried:\n%s", report)
	}

	// THE FLOOR PROVISIONED THE SERVER, and the wrapper the apply wrote reaches it — by the
	// floor's fixed path, with a scrubbed PATH, as an MCP client starts it — with none of the jail
	// posture's chrome flags.
	floorCopy := filepath.Join(paths.HostFloorDir(), "bin", "chrome-devtools-mcp")
	if _, err := os.Stat(floorCopy); err != nil {
		t.Fatalf("the floor holds no chrome-devtools-mcp: %v\n%s", err, report)
	}
	if calls := dist.NpmCalls("install"); len(calls) != 1 || !strings.Contains(calls[0], "chrome-devtools-mcp") {
		t.Errorf("npm installs = %v, want chrome-devtools-mcp's alone", calls)
	}
	cmd := exec.Command("/bin/sh", wrapper, "--from-the-agent")
	cmd.Env = []string{"HOME=" + home, "PATH=" + t.TempDir(), "YOLO_CHROME_DEVTOOLS_ROOT=" + t.TempDir()}
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the wrapper did not start the floor's copy: %v\n%s", err, out)
	}
	ran := string(out)
	if !strings.Contains(ran, "node:") || !strings.Contains(ran, "--from-the-agent") {
		t.Errorf("the floor's chrome-devtools-mcp did not run under the floor's node: %q", ran)
	}
	for _, flag := range []string{"--no-sandbox", "--headless", "--isolated", "--disable-gpu"} {
		if strings.Contains(ran, flag) {
			t.Errorf("the host start carries the jail-only %s: %q", flag, ran)
		}
	}
}

// Your null removes the pack's server from every agent's file, and the apply that removes it
// clears the entry it wrote before.
func TestANullInYourMCPServersRemovesThePacksServerAtTheHost(t *testing.T) {
	home := hostComputedHome(t, chromeHostConfig(""))
	withTestFloor(t).Publish("chrome-devtools-mcp", "1.2.3", "bin=chrome-devtools-mcp")
	hostApplyAssert(t)
	if servers, _ := readJSONAt(t, home, ".claude.json")["mcpServers"].(map[string]any); servers["chrome-devtools"] == nil {
		t.Fatalf("premise: the first apply wrote no chrome-devtools entry: %v", servers)
	}
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		chromeHostConfig(`,"mcp_servers":{"chrome-devtools":null}`))
	report := hostApplyAssert(t)
	for _, file := range []string{".claude.json", ".copilot/mcp-config.json"} {
		servers, _ := readJSONAt(t, home, file)["mcpServers"].(map[string]any)
		if _, ok := servers["chrome-devtools"]; ok {
			t.Errorf("%s still carries chrome-devtools after your null:\n%s", file, report)
		}
	}
	if raw, _ := os.ReadFile(filepath.Join(home, ".codex", "config.toml")); strings.Contains(string(raw), "chrome-devtools") {
		t.Errorf("codex's config.toml still carries chrome-devtools after your null:\n%s", raw)
	}
}

// The preset's omission line names the pack as its next step, and says the pack's server is what
// is written when it is selected.
func TestThePresetLineNamesTheChromeDevtoolsPack(t *testing.T) {
	hostComputedHome(t, `{"packs":["claude"],"host_management":"own","host_floor":false,"mcp_presets":["chrome-devtools"]}`)
	var out, errw bytes.Buffer
	hostMain([]string{"apply"}, &out, &errw, false, strings.NewReader(""))
	if report := out.String() + errw.String(); !strings.Contains(report,
		"mcp_presets chrome-devtools is not written at the host: its command is a wrapper only a jail "+
			"writes — pack chrome-devtools ships that server for the host too: add \"chrome-devtools\" to `packs`") {
		t.Errorf("the preset line does not name the pack:\n%s", report)
	}

	hostComputedHome(t, `{"packs":["claude","chrome-devtools"],"host_management":"own","host_floor":false,"mcp_presets":["chrome-devtools"]}`)
	out.Reset()
	errw.Reset()
	hostMain([]string{"apply"}, &out, &errw, false, strings.NewReader(""))
	if report := out.String() + errw.String(); !strings.Contains(report,
		"pack chrome-devtools's chrome-devtools server is written there instead") {
		t.Errorf("with the pack selected the preset line does not say its server is written:\n%s", report)
	}
}

// `yolo config render --at host` previews the entry the apply writes.
func TestConfigRenderAtHostShowsThePacksMCPEntry(t *testing.T) {
	home := hostComputedHome(t, chromeHostConfig(""))
	rc, out, errs := runConfigVerb(t, "render", "claude/config", "--at", "host")
	if rc != 0 {
		t.Fatalf("config render --at host rc=%d\n%s%s", rc, out, errs)
	}
	if body := previewBody(t, out); !strings.Contains(body, `"chrome-devtools"`) ||
		!strings.Contains(body, filepath.Join(home, hostChromeWrapperRel)) {
		t.Errorf("the host preview of claude/config does not show the pack's server:\n%s", out)
	}
}

// A FETCHED pack's entry is left out at the host and named, with the step that runs it there; a
// pack yolo ships, and one at a path on this machine, compose.
func TestAFetchedPacksMCPEntryIsNamedAndLeftOutAtTheHost(t *testing.T) {
	decl := func(name string) *packload.Pack {
		p := &packload.Pack{Name: name}
		for _, shipped := range packload.Embedded() {
			if shipped.Name == "chrome-devtools" {
				p.Decl = shipped.Decl
			}
		}
		return p
	}
	fetched, official, local := decl("fetched"), decl("official"), decl("local")
	official.Official, local.Local = true, true
	composed, omitted := hostMCPPacks([]*packload.Pack{fetched, official, local})
	if len(composed) != 2 || composed[0] != official || composed[1] != local {
		t.Errorf("composed = %v, want the official and the local pack", composed)
	}
	if len(omitted) != 1 || !strings.Contains(omitted[0], "mcp chrome-devtools (pack fetched) is not written at the host") ||
		!strings.Contains(omitted[0], "write the entry under `mcp_servers`") {
		t.Errorf("omitted = %v", omitted)
	}
}

// --- `yolo host -- <agent>` ensures the server's program (HC-D28) --------------------------------

// agentWithChromeFixture is a host whose selection is a fixture agent pack (floorcli via npm) and
// the chrome-devtools pack, with the test floor and floorcli published. extra joins the config.
func agentWithChromeFixture(t *testing.T, extra string) *floortest.Dist {
	t.Helper()
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	pack := filepath.Join(floortest.ResolvedTemp(t), "floorpack")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"floorpack","contributes":[`+
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"floorpack"},"chrome-devtools"]`+extra+`}`)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	dist := withTestFloor(t)
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	return dist
}

func TestAHostAgentLaunchInstallsTheProgramItsMCPServerRuns(t *testing.T) {
	dist := agentWithChromeFixture(t, "")
	dist.Publish("chrome-devtools-mcp", "1.2.3", "bin=chrome-devtools-mcp")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", "chrome-devtools-mcp")); err != nil {
		t.Errorf("the agent's launch did not install the MCP server's program: %v\n%s", err, errw.String())
	}
	if !strings.Contains(errw.String(), "installing chrome-devtools-mcp into yolo's floor") {
		t.Errorf("the install was not said:\n%s", errw.String())
	}
}

// A failed install, and a program the floor may not hold, each cost the server and never the
// agent, and each says so with its next step.
func TestAHostAgentLaunchSaysWhenItsMCPServersProgramCannotBeInstalled(t *testing.T) {
	for _, tc := range []struct {
		name, extra, want string
	}{
		{"install fails", "", "could not install chrome-devtools-mcp, which MCP server chrome-devtools runs, " +
			"into yolo's floor"},
		{"floor leaves it out", `,"host_floor":{"chrome-devtools":false}`,
			"yolo has no copy of chrome-devtools-mcp on this machine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dist := agentWithChromeFixture(t, tc.extra)
			dist.Publish("chrome-devtools-mcp", "1.2.3", "bin=chrome-devtools-mcp", "fail=1")
			got := captureHostExec(t)
			var errw bytes.Buffer
			if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
				t.Fatalf("the agent did not start: rc=%d execed=%v\n%s", rc, got.execed, errw.String())
			}
			if !strings.Contains(errw.String(), tc.want) {
				t.Errorf("no line %q:\n%s", tc.want, errw.String())
			}
			if tc.name == "install fails" && !strings.Contains(errw.String(), "`yolo host apply --assert` installs it") {
				t.Errorf("the failure names no next step:\n%s", errw.String())
			}
		})
	}
}

// --- a FETCHED pack's server, at the production call sites (HC-D26's second half, MP-D4) --------

// fetchedMCPPackSource is a FETCHED pack (git+file://, a real fetch with no network) shipping one
// `mcp` server, acme-mcp, whose `bin` is acme-mcp-bin, and nothing else, so `yolo pack install`
// asks no approval: neither kind reaches the host in a jail. It returns the pack's `packs` element,
// named "gp", and clears YOLO_PACK_ROOT so a jail's staged tree cannot answer for it.
func fetchedMCPPackSource(t *testing.T) string {
	t.Helper()
	t.Setenv("YOLO_PACK_ROOT", "")
	repo := gitPackRepoWith(t, map[string]string{"pack.json": `{"contributes": [{"kind": "mcp", ` +
		`"name": "acme-mcp", "bin": "acme-mcp-bin", "config": {"command": "acme-mcp-bin", "args": ["--stdio"]}}]}`})
	return `{"source": "git+file://` + repo + `?ref=main", "name": "gp"}`
}

// THE REAL `yolo host apply --assert` writes a fetched pack's server into none of the agents' files
// in your home, where its command would run unconfined as you whenever the agent starts, and the
// report names it with the step that runs it here. Pinned at composeHostInputs' call of
// hostMCPPacks: composing every pack's entry, or dropping its omission lines, fails here.
func TestYoloHostApplyLeavesAFetchedPacksMCPServerOutOfEveryAgentsFile(t *testing.T) {
	home := hostComputedHome(t, `{"packs":["claude","copilot","codex",`+fetchedMCPPackSource(t)+`],`+
		`"host_management":"own","host_floor":false,"mcp_servers":{"mine":{"command":"/usr/local/bin/mine"}}}`)
	installGitPack(t)
	report := hostApplyAssert(t)
	claude, _ := readJSONAt(t, home, ".claude.json")["mcpServers"].(map[string]any)
	copilot, _ := readJSONAt(t, home, ".copilot/mcp-config.json")["mcpServers"].(map[string]any)
	for agent, servers := range map[string]map[string]any{"claude": claude, "copilot": copilot} {
		if servers["mine"] == nil {
			t.Errorf("premise: %s's MCP file lacks your own server, so this apply wrote no table: %v\n%s",
				agent, servers, report)
		}
		if _, ok := servers["acme-mcp"]; ok {
			t.Errorf("%s's MCP file carries the fetched pack's server: %v", agent, servers)
		}
	}
	codex, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil || !strings.Contains(string(codex), "mine") {
		t.Errorf("premise: codex's config.toml lacks your own server (err %v):\n%s", err, codex)
	}
	if strings.Contains(string(codex), "acme-mcp") {
		t.Errorf("codex's config.toml carries the fetched pack's server:\n%s", codex)
	}
	if !strings.Contains(report, "mcp acme-mcp (pack gp) is not written at the host: the pack was fetched") ||
		!strings.Contains(report, "write the entry under `mcp_servers`") {
		t.Errorf("the report does not name the server it left out, with the step that runs it:\n%s", report)
	}
}

// A `yolo host -- <agent>` launch installs no program for a fetched pack's server, even one a
// selected local pack declares and the floor would hold: the server is not written at the host,
// so nothing starts it. Pinned at ensureMCPPrograms' call of hostMCPPacks.
func TestAHostAgentLaunchInstallsNothingForAFetchedPacksMCPServer(t *testing.T) {
	home := floortest.ResolvedTemp(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	t.Chdir(floortest.ResolvedTemp(t))
	pack := filepath.Join(floortest.ResolvedTemp(t), "floorpack")
	writeFile(t, filepath.Join(pack, "pack.json"), `{"name":"floorpack","contributes":[`+
		`{"kind":"program","bin":"floorcli","via":"npm","package":"floorcli-pkg"},`+
		`{"kind":"program","bin":"acme-mcp-bin","via":"npm","package":"acme-mcp-pkg"}]}`)
	writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
		`{"packs":[{"source":"file://`+pack+`","name":"floorpack"},`+fetchedMCPPackSource(t)+`]}`)
	installGitPack(t)
	orig := prepareOpenAIAuthHost
	prepareOpenAIAuthHost = func(hostPrelaunch, io.Writer) (managedOpenAIHostLaunch, error) { return nil, nil }
	t.Cleanup(func() { prepareOpenAIAuthHost = orig })
	dist := withTestFloor(t)
	dist.Publish("floorcli-pkg", "1.0.0", "bin=floorcli")
	dist.Publish("acme-mcp-pkg", "1.0.0", "bin=acme-mcp-bin")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", "floorcli")); err != nil {
		t.Fatalf("premise: the agent itself was not installed into the floor: %v\n%s", err, errw.String())
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", "acme-mcp-bin")); err == nil {
		t.Errorf("the launch installed the program of a server it does not write:\n%s", errw.String())
	}
	for _, call := range dist.NpmCalls("install") {
		if strings.Contains(call, "acme-mcp-pkg") {
			t.Errorf("npm was asked to install the fetched pack's server program: %q", call)
		}
	}
	if strings.Contains(errw.String(), "acme-mcp") {
		t.Errorf("the launch spoke of a server it does not write:\n%s", errw.String())
	}
}

// The agent's own program may have no floor copy (here `host_floor` leaves its pack out): the
// launch runs the agent from your PATH, and still installs the program its MCP server runs, since
// the agent starts that server whichever copy of the agent runs.
func TestAHostAgentLaunchWithNoFloorCopyStillInstallsItsMCPServersProgram(t *testing.T) {
	dist := agentWithChromeFixture(t, `,"host_floor":{"floorpack":false}`)
	dist.Publish("chrome-devtools-mcp", "1.2.3", "bin=chrome-devtools-mcp")
	stubBins(t, "floorcli")
	got := captureHostExec(t)
	var errw bytes.Buffer
	if rc := hostExec(nil, []string{"floorcli"}, io.Discard, &errw, nil); rc != 0 || !got.execed {
		t.Fatalf("rc=%d execed=%v\n%s", rc, got.execed, errw.String())
	}
	if !strings.Contains(errw.String(), "yolo has no copy of floorcli") {
		t.Fatalf("premise: the agent had a floor copy after all:\n%s", errw.String())
	}
	if _, err := os.Stat(filepath.Join(paths.HostFloorDir(), "bin", "chrome-devtools-mcp")); err != nil {
		t.Errorf("an agent with no floor copy left its MCP server's program uninstalled: %v\n%s", err, errw.String())
	}
}

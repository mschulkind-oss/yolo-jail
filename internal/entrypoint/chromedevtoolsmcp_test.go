package entrypoint

// chromedevtoolsmcp_test.go pins the jail half of the chrome-devtools pack
// (docs/design/mcp-presets-removal.md §13 steps 1-2): the composed table the host hands a jail
// replaces a same-named preset (the interim coexistence that needs no rule), the staged-tree
// composer the macos-user plan asks, claude's real render of the entry, the jail launcher that
// carries the posture's chrome flags, and the wrapper's run-time resolution of the server and the
// browser — run as the entry runs it, `/bin/sh <file>`, against stand-ins that log their argv.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

const chromeWrapperRel = ".local/share/yolo-chrome-devtools/chrome-devtools-mcp-wrapper"

// composedChromeWire is the YOLO_MCP_SERVERS a launch hands a jail at home with the pack selected.
func composedChromeWire(t *testing.T, home string, user *jsonx.OrderedMap) string {
	t.Helper()
	table := packload.ComposeMCPServers(user, []*packload.Pack{materializedPack(t, "chrome-devtools")}, home)
	wire, err := jsonx.DumpsCompact(table)
	if err != nil {
		t.Fatal(err)
	}
	return wire
}

// INTERIM COEXISTENCE: presets expand first and the composed table overrides by name, so the
// pack's entry replaces the preset's while both are on (mcpServersWith's merge order).
func TestAPackMCPEntryReplacesASameNamedPreset(t *testing.T) {
	home := resolvedDir(t)
	e := NewEnv(map[string]string{"JAIL_HOME": home, "YOLO_MCP_PRESETS": `["chrome-devtools"]`,
		"YOLO_MCP_SERVERS": composedChromeWire(t, home, nil)})
	servers := e.LoadMCPServers()
	v, ok := servers.Get("chrome-devtools")
	if !ok {
		t.Fatalf("no chrome-devtools entry: %v", servers)
	}
	entry := v.(*jsonx.OrderedMap)
	if cmd, _ := entry.Get("command"); cmd != "/bin/sh" {
		t.Errorf("command = %v: the preset's mcp-wrappers node won over the pack's entry", cmd)
	}
	args, _ := entry.Get("args")
	if list, _ := args.([]any); len(list) != 1 || list[0] != filepath.Join(home, chromeWrapperRel) {
		t.Errorf("args = %v, want the pack's wrapper under the jail's home", args)
	}
}

// The staged-tree composer macos-user's plan asks: the pack's entry at the home it names, the
// user's table over it, and a tree it cannot read is an error, not an empty table.
func TestMCPServersAtComposesTheStagedTree(t *testing.T) {
	root := stageShippedPacks(t)
	user := jsonx.NewOrderedMap()
	mine := jsonx.NewOrderedMap()
	mine.Set("command", "/usr/local/bin/mine")
	user.Set("mine", mine)
	got, err := MCPServersAt(root, user, "/Users/yolo-sandbox")
	if err != nil {
		t.Fatal(err)
	}
	v, _ := got.Get("chrome-devtools")
	entry, _ := v.(*jsonx.OrderedMap)
	if entry == nil {
		t.Fatalf("no chrome-devtools entry from the staged tree: %v", got)
	}
	args, _ := entry.Get("args")
	if list, _ := args.([]any); len(list) != 1 || list[0] != "/Users/yolo-sandbox/"+chromeWrapperRel {
		t.Errorf("args = %v, want the wrapper under the sandbox home", args)
	}
	if _, ok := got.Get("mine"); !ok {
		t.Errorf("the user's own entry did not survive: %v", got)
	}
	if empty, err := MCPServersAt("", user, "/h"); err != nil || empty.Len() != 1 {
		t.Errorf("no pack root: %v, %v — want the user's table alone", empty, err)
	}
	file := filepath.Join(resolvedDir(t), "not-a-dir")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := MCPServersAt(file, user, "/h"); err == nil {
		t.Error("a pack root that is a file composed silently")
	}
}

// THROUGH CLAUDE'S OWN DERIVE: the composed entry reaches ~/.claude.json with no core branch
// naming either pack, and a user null removes it there too.
func TestClaudesRenderCarriesThePacksChromeDevtoolsEntry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		user   string
		wanted bool
	}{{"selected", `{}`, true}, {"nulled", `{"chrome-devtools": null}`, false}} {
		t.Run(tc.name, func(t *testing.T) {
			home := resolvedDir(t)
			u, err := jsonx.Decode([]byte(tc.user))
			if err != nil {
				t.Fatal(err)
			}
			e := NewEnv(map[string]string{"JAIL_HOME": home,
				"YOLO_MCP_SERVERS": composedChromeWire(t, home, u.(*jsonx.OrderedMap))})
			var term strings.Builder
			e.Stderr = &term
			ConfigurePackSurfaces(e, []*packload.Pack{materializedPack(t, "claude")})
			raw, err := os.ReadFile(e.ClaudeJSONPath())
			if err != nil {
				t.Fatalf("no ~/.claude.json: %v\n%s", err, term.String())
			}
			var doc struct {
				MCPServers map[string]struct {
					Command string   `json:"command"`
					Args    []string `json:"args"`
				} `json:"mcpServers"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			got, ok := doc.MCPServers["chrome-devtools"]
			if ok != tc.wanted {
				t.Fatalf("chrome-devtools in ~/.claude.json = %v, want %v:\n%s", ok, tc.wanted, raw)
			}
			if ok && (got.Command != "/bin/sh" || len(got.Args) != 1 ||
				got.Args[0] != filepath.Join(home, chromeWrapperRel)) {
				t.Errorf("claude's entry = %+v", got)
			}
		})
	}
}

// The jail's launcher for chrome-devtools-mcp bakes the AUTONOMOUS posture's chrome flags in, so
// every start of the server in a jail gets them, however the client reached it.
func TestTheJailLauncherCarriesTheChromeSandboxFlags(t *testing.T) {
	home := resolvedDir(t)
	e := NewEnv(map[string]string{"JAIL_HOME": home, AgentUpdatesEnv: "false",
		"YOLO_PACK_ROOT": stageShippedPacks(t)})
	runLaunchDirPasses(t, e)
	body := readFileString(t, filepath.Join(e.LaunchDir(), "chrome-devtools-mcp"))
	for _, flag := range []string{"--headless", "--isolated", "--chrome-arg=--no-sandbox",
		"--chrome-arg=--disable-setuid-sandbox", "--chrome-arg=--disable-gpu"} {
		if !strings.Contains(body, flag) {
			t.Errorf("the jail's chrome-devtools-mcp launcher does not carry %s", flag)
		}
	}
}

// --- the wrapper, run as the entry runs it ------------------------------------------------

// wrapperWorld is a home holding the pack's wrapper where the `files` contribution puts it, read
// only and with no exec bit (what a bare-name pack's tree gives), and an empty filesystem root for
// the standard browser paths.
type wrapperWorld struct {
	home, wrapper, root, log string
}

func newWrapperWorld(t *testing.T) wrapperWorld {
	t.Helper()
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	body, err := officialpacks.FS.ReadFile("chrome-devtools/bin/chrome-devtools-mcp-wrapper")
	if err != nil {
		t.Fatal(err)
	}
	w := wrapperWorld{home: resolvedDir(t), root: resolvedDir(t)}
	w.wrapper = filepath.Join(w.home, chromeWrapperRel)
	w.log = filepath.Join(w.home, "argv.log")
	if err := os.MkdirAll(filepath.Dir(w.wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(w.wrapper, body, 0o444); err != nil {
		t.Fatal(err)
	}
	return w
}

// server puts a stand-in chrome-devtools-mcp at dir that logs its argv.
func (w wrapperWorld) server(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	argvLogger(t, dir, "chrome-devtools-mcp", w.log, "")
	return filepath.Join(dir, "chrome-devtools-mcp")
}

// exe writes an executable stand-in at path.
func exe(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// run runs `/bin/sh <wrapper> args…` with exactly env (the scrubbed environment an MCP client
// hands its servers): stdout, stderr and the exit code.
func (w wrapperWorld) run(t *testing.T, env []string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("/bin/sh", append([]string{w.wrapper}, args...)...)
	cmd.Env = append(env, "YOLO_CHROME_DEVTOOLS_ROOT="+w.root)
	var out, errw strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errw
	err := cmd.Run()
	rc := 0
	if ee, ok := err.(*exec.ExitError); ok {
		rc = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the wrapper: %v", err)
	}
	return out.String(), errw.String(), rc
}

// With a PATH that holds neither (the scrubbed environment), the jail's launcher dir is where the
// server is, and a browser on PATH is handed over by path.
func TestTheWrapperFindsTheJailLauncherAndABrowserOnPath(t *testing.T) {
	w := newWrapperWorld(t)
	w.server(t, filepath.Join(w.home, ".yolo", "bin", "launch"))
	browsers := resolvedDir(t)
	chromium := exe(t, filepath.Join(browsers, "chromium"))
	_, errs, rc := w.run(t, []string{"HOME=" + w.home, "PATH=" + browsers}, "--extra")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if got := strings.Join(logLines(t, w.log), " "); got != "--executablePath "+chromium+" --extra" {
		t.Errorf("the server ran with %q", got)
	}
	if !strings.Contains(errs, "with browser "+chromium) {
		t.Errorf("the start line does not name the browser:\n%s", errs)
	}
}

// PATH comes first: the server and the browser a caller's PATH finds are the ones run, as the
// host floor's bin/, last on a `yolo host` child's PATH, defers to a copy of your own.
func TestTheWrapperPrefersWhatPathFinds(t *testing.T) {
	w := newWrapperWorld(t)
	w.server(t, filepath.Join(w.home, ".yolo", "bin", "launch"))
	mine := resolvedDir(t)
	other := filepath.Join(w.home, "other.log")
	argvLogger(t, mine, "chrome-devtools-mcp", other, "")
	chrome := exe(t, filepath.Join(mine, "google-chrome"))
	exe(t, filepath.Join(w.root, "usr", "bin", "chromium"))
	if _, errs, rc := w.run(t, []string{"HOME=" + w.home, "PATH=" + mine}); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if len(logLines(t, w.log)) != 0 {
		t.Error("the launcher-dir copy ran although PATH has one")
	}
	if got := strings.Join(logLines(t, other), " "); got != "--executablePath "+chrome {
		t.Errorf("PATH's server ran with %q, want PATH's browser", got)
	}
}

// At the host with a scrubbed PATH: yolo's floor copy, at its fixed path under the home, and the
// macOS app bundle among the standard install paths.
func TestTheWrapperFallsBackToTheHostFloorAndTheMacAppBundle(t *testing.T) {
	w := newWrapperWorld(t)
	w.server(t, filepath.Join(w.home, ".local", "share", "yolo-jail", "host-floor", "bin"))
	app := exe(t, filepath.Join(w.root, "Applications", "Google Chrome.app", "Contents", "MacOS", "Google Chrome"))
	if _, errs, rc := w.run(t, []string{"HOME=" + w.home, "PATH=" + resolvedDir(t)}); rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, errs)
	}
	if got := strings.Join(logLines(t, w.log), " "); got != "--executablePath "+app {
		t.Errorf("the floor's server ran with %q, want the app bundle's Chrome", got)
	}
}

// No browser anywhere: no path is handed, chrome-devtools-mcp looks for Chrome itself, and the
// start line says so; a browser the caller's own arguments name is never overridden.
func TestTheWrapperLeavesTheBrowserToChromeDevtoolsMcpOrTheCaller(t *testing.T) {
	w := newWrapperWorld(t)
	w.server(t, filepath.Join(w.home, ".yolo", "bin", "launch"))
	env := []string{"HOME=" + w.home, "PATH=" + resolvedDir(t)}
	_, errs, rc := w.run(t, env)
	if rc != 0 || len(logLines(t, w.log)) != 0 {
		t.Fatalf("rc=%d argv=%v\n%s", rc, logLines(t, w.log), errs)
	}
	if !strings.Contains(errs, "chrome-devtools-mcp looks for Chrome itself") {
		t.Errorf("no line saying the browser is left to chrome-devtools-mcp:\n%s", errs)
	}
	exe(t, filepath.Join(w.root, "usr", "bin", "chromium"))
	if _, errs, rc := w.run(t, env, "--browserUrl=http://127.0.0.1:9222"); rc != 0 ||
		strings.Join(logLines(t, w.log), " ") != "--browserUrl=http://127.0.0.1:9222" {
		t.Errorf("a browser the caller named was overridden: rc=%d argv=%v\n%s", rc, logLines(t, w.log), errs)
	}
	// --autoConnect attaches to the Chrome you are already running (chrome-devtools-mcp's route to
	// your own browser), and chrome-devtools-mcp 1.10.1 refuses it beside --executablePath, so it
	// names the caller's browser too, in each spelling its parser takes.
	for _, arg := range []string{"--autoConnect", "--autoConnect=true", "--auto-connect", "--auto-connect=true"} {
		w := newWrapperWorld(t)
		w.server(t, filepath.Join(w.home, ".yolo", "bin", "launch"))
		exe(t, filepath.Join(w.root, "usr", "bin", "chromium"))
		if _, errs, rc := w.run(t, []string{"HOME=" + w.home, "PATH=" + resolvedDir(t)}, arg); rc != 0 ||
			strings.Join(logLines(t, w.log), " ") != arg {
			t.Errorf("%s: the server ran with %v, want the caller's argument alone (no --executablePath, "+
				"which chrome-devtools-mcp refuses beside it): rc=%d\n%s", arg, logLines(t, w.log), rc, errs)
		}
	}
}

// Nothing installed: exit 127 with the next step at each notch; and --check reports both finds
// on stdout, exiting 1 with the server missing and 0 once it is there.
func TestTheWrapperReportsWhatItFinds(t *testing.T) {
	w := newWrapperWorld(t)
	env := []string{"HOME=" + w.home, "PATH=" + resolvedDir(t)}
	_, errs, rc := w.run(t, env)
	if rc != 127 || !strings.Contains(errs, "`yolo host apply --assert`") ||
		!strings.Contains(errs, `"chrome-devtools" is in your packs`) {
		t.Errorf("a missing server: rc=%d, want 127 and both notches' next steps:\n%s", rc, errs)
	}
	out, _, rc := w.run(t, env, "--check")
	if rc != 1 || !strings.Contains(out, "chrome-devtools-mcp: not found") ||
		!strings.Contains(out, "browser: none found") {
		t.Errorf("--check with nothing: rc=%d\n%s", rc, out)
	}
	server := w.server(t, filepath.Join(w.home, ".npm-global", "bin"))
	app := exe(t, filepath.Join(w.root, "Applications", "Chromium.app", "Contents", "MacOS", "Chromium"))
	out, _, rc = w.run(t, env, "--check")
	if rc != 0 || !strings.Contains(out, "chrome-devtools-mcp: "+server) ||
		!strings.Contains(out, "browser: "+app) {
		t.Errorf("--check with both: rc=%d\n%s", rc, out)
	}
	if len(logLines(t, w.log)) != 0 {
		t.Error("--check started the server")
	}
}

// An MCP client may hand no HOME at all: the wrapper takes the home from where it sits.
func TestTheWrapperFindsItsHomeWithoutHOME(t *testing.T) {
	w := newWrapperWorld(t)
	w.server(t, filepath.Join(w.home, ".yolo", "bin", "launch"))
	if _, errs, rc := w.run(t, []string{"PATH=" + resolvedDir(t)}, "--ran"); rc != 0 ||
		strings.Join(logLines(t, w.log), " ") != "--ran" {
		t.Errorf("with no HOME the server under the wrapper's own home did not run: rc=%d\n%s", rc, errs)
	}
}

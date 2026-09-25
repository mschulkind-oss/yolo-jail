package footer_test

// The adapters: each agent pack's statusLine default, run the way its agent runs it, against
// the real renderer. These are the tests that fail if a pack's wiring is deleted or its
// command drifts from the flags the renderer reads — the renderer's own rules are pinned in
// footer_test.go.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/footer"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packdecl"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	officialpacks "github.com/mschulkind-oss/yolo-jail/packs"
)

// asYolo turns this test binary into a stand-in `yolo` for a child process: a symlink named
// `yolo` on the child's PATH points here, and with this variable set the binary answers
// `yolo internal footer …` through footer.Main and refuses every other argv, so a pack
// command naming the subcommand wrongly fails rather than passing.
const asYolo = "YOLO_FOOTER_TEST_AS_YOLO"

func TestMain(m *testing.M) {
	if os.Getenv(asYolo) == "1" {
		args := os.Args[1:]
		if len(args) < 2 || args[0] != "internal" || args[1] != "footer" {
			fmt.Fprintf(os.Stderr, "stand-in yolo: not an `internal footer` call: %q\n", args)
			os.Exit(3)
		}
		os.Exit(footer.Main(args[2:], os.Stdin, os.Stdout, nil))
	}
	os.Exit(m.Run())
}

// shippedPacks materializes the embedded packs once per test.
func shippedPacks(t *testing.T) []*packload.Pack {
	t.Helper()
	packs, problems := packload.MaterializeEmbedded(officialpacks.FS, t.TempDir())
	if len(problems) > 0 {
		t.Fatalf("materializing embedded packs: %v", problems)
	}
	return packs
}

func packNamed(t *testing.T, packs []*packload.Pack, name string) *packload.Pack {
	t.Helper()
	for _, p := range packs {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no shipped pack named %q", name)
	return nil
}

func surfaceOf(t *testing.T, p *packload.Pack, agent, name string) manifest.Surface {
	t.Helper()
	surfaces, problems := p.Surfaces()
	if len(problems) > 0 {
		t.Fatalf("pack %s surfaces: %v", p.Name, problems)
	}
	for _, s := range surfaces {
		if s.Agent == agent && s.Name == name {
			return s
		}
	}
	t.Fatalf("pack %s declares no %s/%s surface", p.Name, agent, name)
	return manifest.Surface{}
}

// statusLineDefault is the adapter: the statusLine object in the surface's DEFAULTS layer,
// the lowest one (OQ-FT1).
func statusLineDefault(t *testing.T, s manifest.Surface) map[string]any {
	t.Helper()
	sl, ok := s.DefaultsMap()["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("%s/%s defaults carry no statusLine object: %#v", s.Agent, s.Name, s.DefaultsMap()["statusLine"])
	}
	return sl
}

func keys(m map[string]any) string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// runStatusCommand runs command as an agent runs a status-line command — through /bin/sh,
// with the agent's env and its JSON on stdin — with PATH holding only the stand-in yolo and
// the system directories.
func runStatusCommand(t *testing.T, command, home, stdin string, env map[string]string) string {
	t.Helper()
	bin := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(self, filepath.Join(bin, "yolo")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, asYolo + "=1"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("status command failed: %v\ncommand: %s\nstderr: %s", err, command, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("status command wrote to stderr: %q", stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n")
}

// A launch's three tables with the bedrock profile selected for one agent, as this jail's
// own env carries them.
func bedrockTables(agent string) map[string]string {
	return map[string]string{
		"YOLO_USE_PROFILES": `{"` + agent + `": "bedrock"}`,
		"YOLO_PROFILES":     `{"bedrock": {"provider": "bedrock"}, "codex": {"provider": "openai-codex"}}`,
		"YOLO_PROVIDERS":    `{"bedrock": {"region": "us-east-1"}, "openai-codex": {"endpoints": {"anthropic": {"base_url": "http://127.0.0.1:8215"}}}}`,
	}
}

func with(m map[string]string, kv ...string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i]] = kv[i+1]
	}
	return out
}

// claudeStatus is a trimmed sample of the JSON Claude pipes to its status-line command.
const claudeStatus = `{"model": {"id": "claude-opus-4", "display_name": "Opus"}, "workspace": {"current_dir": "/workspace"}, "cost": {"total_cost_usd": 0.01}}`

// TestClaudeAdapter is build step 3's claude half: the claude pack's settings default runs
// the renderer and reads as the design's examples do.
func TestClaudeAdapter(t *testing.T) {
	s := surfaceOf(t, packNamed(t, shippedPacks(t), "claude"), "claude", "settings")
	sl := statusLineDefault(t, s)
	// Only the two keys every user statusLine sets, so a user's own replaces all of it (§3).
	if got := keys(sl); got != "command,type" {
		t.Errorf("claude statusLine default keys = %s, want command,type", got)
	}
	if sl["type"] != "command" {
		t.Errorf("claude statusLine type = %v, want command", sl["type"])
	}
	command, _ := sl["command"].(string)
	home := t.TempDir()
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"a bedrock profile in a jail", with(bedrockTables("claude"), "YOLO_VERSION", "0.10.0", "CLAUDE_CODE_USE_BEDROCK", "1"),
			"Opus · yolo: Bedrock · jail"},
		{"Bedrock switched on outside yolo's profiles", with(bedrockTables("pi"), "YOLO_VERSION", "0.10.0", "CLAUDE_CODE_USE_BEDROCK", "1"),
			"Opus · yolo: Bedrock (env) · jail"},
		{"the codex profile, which claude reaches through the bridge", with(bedrockTables("x"), "YOLO_USE_PROFILES", `{"claude": "codex"}`, "YOLO_VERSION", "0.10.0"),
			"Opus · yolo: codex (bridge) · jail"},
		{"no profile and no switch, on the host", map[string]string{},
			"Opus · yolo: Claude subscription · host"},
		{"a switch Claude counts as off", with(bedrockTables("pi"), "YOLO_VERSION", "1", "CLAUDE_CODE_USE_BEDROCK", "0"),
			"Opus · yolo: Claude subscription · jail"},
		// Claude 2.1.282 tests its provider switches bedrock, foundry, anthropicAws,
		// anthropicGoogleCloud, mantle, vertex — so with two on, the earlier one is the route.
		{"Claude's own switch order decides between two", with(bedrockTables("pi"), "YOLO_VERSION", "1",
			"CLAUDE_CODE_USE_VERTEX", "true", "CLAUDE_CODE_USE_FOUNDRY", "1"),
			"Opus · yolo: Microsoft Foundry (env) · jail"},
		{"a switch yolo has no provider for is still named", with(bedrockTables("pi"), "YOLO_VERSION", "1",
			"CLAUDE_CODE_USE_VERTEX", "yes"),
			"Opus · yolo: Vertex AI (env) · jail"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := runStatusCommand(t, command, home, claudeStatus, c.env); got != c.want {
				t.Errorf("claude footer = %q, want %q", got, c.want)
			}
		})
	}
}

// TestAgyAdapter is build step 3's agy half: yolo's line stacks with agy's own
// (stack_with_default, OQ-FT12), and repeats nothing agy's line already shows.
func TestAgyAdapter(t *testing.T) {
	s := surfaceOf(t, packNamed(t, shippedPacks(t), "agy"), "agy", "settings")
	sl := statusLineDefault(t, s)
	if got := keys(sl); got != "command,stack_with_default,type" {
		t.Errorf("agy statusLine default keys = %s, want command,stack_with_default,type", got)
	}
	if sl["stack_with_default"] != true {
		t.Errorf("agy statusLine stack_with_default = %v, want true: without it any statusLine REPLACES agy's own line (DIR-FT2)",
			sl["stack_with_default"])
	}
	if sl["type"] != "command" {
		t.Errorf("agy statusLine type = %v, want command", sl["type"])
	}
	command, _ := sl["command"].(string)
	got := runStatusCommand(t, command, t.TempDir(), claudeStatus, with(bedrockTables("claude"), "YOLO_VERSION", "0.10.0"))
	if want := "yolo: Google AI subscription · jail"; got != want {
		t.Errorf("agy footer = %q, want %q", got, want)
	}
}

// copilotScript is where the copilot pack delivers its footer script, home-relative. It sits
// in a directory of yolo's own under ~/.copilot rather than beside copilot's state, so the
// pack-files mountpoint migration, which scans the directory holding each single-file target,
// never scans copilot's own files (internal/cli/run's copilot footer test pins that half).
const copilotScript = ".copilot/yolo/footer.sh"

// copilotAdapter returns the copilot pack's statusLine command and the bytes of the script
// its files contribution delivers at copilotScript.
func copilotAdapter(t *testing.T) (command string, script []byte) {
	t.Helper()
	p := packNamed(t, shippedPacks(t), "copilot")
	sl := statusLineDefault(t, surfaceOf(t, p, "copilot", "config"))
	command, _ = sl["command"].(string)
	src := ""
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.Into == copilotScript {
			src = filepath.Join(p.Root, filepath.FromSlash(c.From))
		}
	}
	if src == "" {
		t.Fatalf("the copilot pack delivers nothing at %s, which its statusLine command runs", copilotScript)
	}
	body, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	return command, body
}

// homeWithCopilotScript is a home holding the copilot script where the files contribution
// lands it, read-only as the jail's :ro mount delivers it.
func homeWithCopilotScript(t *testing.T, body []byte) string {
	t.Helper()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, filepath.Dir(copilotScript)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, copilotScript), body, 0o444); err != nil {
		t.Fatal(err)
	}
	return home
}

// TestCopilotAdapter is build step 5: copilot's config default names a pack-shipped script
// by a path that never changes, the copilot pack delivers that script at that path, and the
// script runs the renderer. No experimental flag is set (OQ-FT7).
func TestCopilotAdapter(t *testing.T) {
	p := packNamed(t, shippedPacks(t), "copilot")
	s := surfaceOf(t, p, "copilot", "config")
	sl := statusLineDefault(t, s)
	if got := keys(sl); got != "command,type" {
		t.Errorf("copilot statusLine default keys = %s, want command,type", got)
	}
	command, body := copilotAdapter(t)
	if !strings.Contains(command, "sh ~/"+copilotScript) {
		t.Errorf("copilot statusLine command = %q, want it to run `sh ~/%s`: the value frozen into an "+
			"edited-in-place file names a fixed path, run by sh because an embedded pack's file carries "+
			"no exec bit", command, copilotScript)
	}
	// Copilot 1.0.88 expands env references in the command itself (envResolveVars over its own
	// process env) before a shell sees it, so a `$` in the frozen value means whatever copilot
	// makes of it, not what sh would.
	if strings.Contains(command, "$") {
		t.Errorf("copilot statusLine command = %q carries a `$`, which copilot expands before sh runs it", command)
	}
	for k, v := range s.DefaultsMap() {
		if strings.Contains(strings.ToLower(k), "experiment") {
			t.Errorf("copilot config defaults set %s=%v: OQ-FT7 rules that yolo neither sets nor checks the gate", k, v)
		}
	}
	got := runStatusCommand(t, command, homeWithCopilotScript(t, body), `{"cwd": "/workspace"}`,
		with(bedrockTables("claude"), "YOLO_VERSION", "0.10.0"))
	if want := "yolo: Copilot subscription · jail"; got != want {
		t.Errorf("copilot footer = %q, want %q", got, want)
	}
}

// TestCopilotCommandIsSilentWithoutItsScript: the frozen command must not fail where the
// script is absent. macos-user renders copilot's config but delivers no `files` contribution,
// so its home has the command and not the script; a later backend or release may drop the
// file too, and the value in the user's file can never be fixed after the first fill (§3).
// Copilot 1.0.88 treats a non-zero exit as a failure and warns "Status line command failed",
// so the command has to exit 0 there, with nothing on either stream.
func TestCopilotCommandIsSilentWithoutItsScript(t *testing.T) {
	command, _ := copilotAdapter(t)
	if got := runStatusCommand(t, command, t.TempDir(), `{"cwd": "/workspace"}`,
		with(bedrockTables("claude"), "YOLO_VERSION", "0.10.0")); got != "" {
		t.Errorf("copilot footer with no script in the home = %q, want nothing", got)
	}
}

// TestCopilotScriptIsSilentWithoutYolo: "turned off, the script prints nothing" (§3). With
// no yolo on PATH the script exits 0 with no output, so copilot shows an empty item rather
// than an error.
func TestCopilotScriptIsSilentWithoutYolo(t *testing.T) {
	command, body := copilotAdapter(t)
	// A PATH holding only sh: the system directories are no good here, since a jail's /bin
	// carries yolo itself.
	bin := t.TempDir()
	if err := os.Symlink("/bin/sh", filepath.Join(bin, "sh")); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-c", command)
	cmd.Env = []string{"PATH=" + bin, "HOME=" + homeWithCopilotScript(t, body)}
	out, err := cmd.CombinedOutput()
	if err != nil || len(out) != 0 {
		t.Errorf("with no yolo on PATH: err=%v output=%q, want exit 0 and nothing", err, out)
	}
}

// extensionAdapter is an agent whose footer takes a keyed status entry from an extension (pi's
// and omp's `ctx.ui.setStatus`) rather than a status-line command: the pack that ships the
// extension file, the CLI name the profile table is keyed by, and the home-relative path the
// pack's `files` contribution delivers the file to, which is the agent's own extension
// discovery directory.
type extensionAdapter struct{ pack, agent, into string }

var extensionAdapters = []extensionAdapter{
	{"pi", "pi", ".pi/agent/extensions/yolo-footer.js"},
	// A subdirectory's index.js, which omp's native discovery loads, so the file sits in a
	// directory only yolo uses (~/.oh-omp is workspace state; see the extension's header).
	{"omp", "oh-omp", ".oh-omp/agent/extensions/yolo-footer/index.js"},
}

// extensionSource is the file the pack's `files` contribution delivers at into, read from the
// materialized pack. It fails when no contribution delivers there: a file in the pack that
// nothing delivers is dead documentation.
func extensionSource(t *testing.T, p *packload.Pack, into string) []byte {
	t.Helper()
	for _, c := range p.Decl.Contributions() {
		if c.Kind == packdecl.KindFiles && c.Into == into {
			body, err := os.ReadFile(filepath.Join(p.Root, filepath.FromSlash(c.From)))
			if err != nil {
				t.Fatal(err)
			}
			return body
		}
	}
	t.Fatalf("the %s pack delivers nothing at %s, the agent's extension directory", p.Name, into)
	return nil
}

// footerArgsRE finds the renderer's argument array in an extension file. The files keep it as
// JSON (double-quoted strings, no trailing comma) so that this is the whole parser.
var footerArgsRE = regexp.MustCompile(`(?s)const FOOTER_ARGS = (\[.*?\]);`)

// extensionArgs is the argv an extension hands `yolo`: `internal footer` and the flags.
func extensionArgs(t *testing.T, source []byte) []string {
	t.Helper()
	m := footerArgsRE.FindSubmatch(source)
	if m == nil {
		t.Fatalf("no `const FOOTER_ARGS = [...];` in the extension:\n%s", source)
	}
	var args []string
	if err := json.Unmarshal(m[1], &args); err != nil {
		t.Fatalf("FOOTER_ARGS is not a JSON string array (%v): %s", err, m[1])
	}
	if len(args) < 2 || args[0] != "internal" || args[1] != "footer" {
		t.Fatalf("FOOTER_ARGS = %q, want it to start `internal footer`", args)
	}
	return args
}

// runFooterArgv runs `yolo <args>` through the stand-in yolo, with env and nothing else, and
// returns its stdout. It fails on a non-zero exit or any stderr, which the renderer never gives.
func runFooterArgv(t *testing.T, args []string, env map[string]string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + t.TempDir(), asYolo + "=1"}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil || stderr.Len() != 0 {
		t.Fatalf("yolo %q: err=%v stderr=%q", args, err, stderr.String())
	}
	return strings.TrimRight(stdout.String(), "\n")
}

// extensionHarness loads an extension the way pi and omp do (a default-exported factory handed
// the extension API), fires every session_start handler it registered with a context whose
// hasUI is $HAS_UI, and prints what the extension registered and called as JSON.
const extensionHarness = `
import register from "./extension.mjs";
const handlers = {};
const calls = [];
register({ on(event, fn) { (handlers[event] ??= []).push(fn); } });
const ctx = {
	hasUI: process.env.HAS_UI === "1",
	ui: {
		setStatus(key, text) { calls.push(["setStatus", key, text]); },
		setFooter() { calls.push(["setFooter"]); },
	},
};
for (const fn of handlers["session_start"] ?? []) {
	await fn({ type: "session_start", reason: "startup" }, ctx);
}
console.log(JSON.stringify({ events: Object.keys(handlers).sort(), calls }));
`

type extensionRun struct {
	Events []string   `json:"events"`
	Calls  [][]string `json:"calls"`
}

// requireNode returns node's path, or SKIPS the calling test — naming what went unexercised —
// when node is not on PATH. A skip, not a failure: node is a runtime of the agents these
// adapters ship for, not of yolo's own build, and a from-source `go test -short ./...` on a
// machine without it must not go red over an absent interpreter. What keeps the skip from
// hiding a regression is that CI's two short-suite jobs (check-go on ubuntu-latest,
// check-macos on macos-latest, .github/workflows/ci.yml) both run with node on PATH — neither
// installs it; each runner image ships it, and internal/entrypoint's
// pi_openai_auth_extension_test.go, which execs node unconditionally, is green on both.
//
// Called AFTER a test's static assertions, never at the top, so the manifest and source
// checks that need no interpreter still run without one.
func requireNode(t *testing.T, what string) string {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skipf("node is not on PATH (%v), so %s did not run; install node to run "+
			"this test — CI's short-suite jobs have it", err, what)
	}
	return node
}

// runExtension runs the shipped extension file under node through extensionHarness. withYolo
// puts the stand-in yolo on the extension's PATH; without it the PATH holds node alone.
func runExtension(t *testing.T, source []byte, hasUI, withYolo bool, env map[string]string) extensionRun {
	t.Helper()
	node := requireNode(t, "the pi and omp extensions")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "extension.mjs"), source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "harness.mjs"), []byte(extensionHarness), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	if err := os.Symlink(node, filepath.Join(bin, "node")); err != nil {
		t.Fatal(err)
	}
	if withYolo {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(self, filepath.Join(bin, "yolo")); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, "harness.mjs")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + bin, "HOME=" + t.TempDir(), asYolo + "=1", "HAS_UI=0"}
	if hasUI {
		cmd.Env[len(cmd.Env)-1] = "HAS_UI=1"
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the extension under node: %v\n%s", err, out)
	}
	var run extensionRun
	if err := json.Unmarshal(bytes.TrimSpace(out), &run); err != nil {
		t.Fatalf("harness output is not the expected JSON (%v): %s", err, out)
	}
	return run
}

// TestExtensionAdaptersSetOneYoloStatus is build step 4: pi's and omp's packs each deliver an
// extension into the agent's own extension directory, and on session_start it sets exactly one
// status entry, keyed "yolo", with the renderer's line. It never calls setFooter, which would
// replace the agent's stock footer (DIR-FT2). With no UI it spawns nothing, and with no yolo on
// PATH it sets nothing and does not throw.
func TestExtensionAdaptersSetOneYoloStatus(t *testing.T) {
	packs := shippedPacks(t)
	codexTables := func(agent string) map[string]string {
		return map[string]string{
			"YOLO_VERSION":      "0.10.0",
			"YOLO_USE_PROFILES": `{"` + agent + `": "codex"}`,
			"YOLO_PROFILES":     `{"codex": {"provider": "openai-codex"}}`,
			"YOLO_PROVIDERS":    `{"openai-codex": {"endpoints": {"openai-responses": {"base_url": "https://chatgpt.com/backend-api/codex"}}}}`,
		}
	}
	cases := []struct {
		adapter extensionAdapter
		env     map[string]string
		want    string
	}{
		// The design's own example (§1): pi's native route to the ChatGPT subscription, under
		// the pi pack's profile, which is named unlike its provider.
		{extensionAdapters[0], codexTables("pi"), "yolo: ChatGPT subscription (profile codex) · jail"},
		{extensionAdapters[0], map[string]string{}, "yolo: pi's own login · host"},
		// omp speaks no Responses wire, so it reaches openai-codex at the bridge's anthropic
		// address (TestBridgedRoutesAreMarked pins the list this case relies on).
		{extensionAdapters[1], codexTables("oh-omp"), "yolo: codex (bridge) · jail"},
		{extensionAdapters[1], map[string]string{"YOLO_VERSION": "0.10.0"}, "yolo: omp's own login · jail"},
	}
	for _, c := range cases {
		t.Run(c.adapter.pack+"/"+c.want, func(t *testing.T) {
			source := extensionSource(t, packNamed(t, packs, c.adapter.pack), c.adapter.into)
			if regexp.MustCompile(`setFooter\s*\(`).Match(source) {
				t.Errorf("the %s extension calls setFooter, which replaces the agent's stock footer (DIR-FT2)",
					c.adapter.pack)
			}
			run := runExtension(t, source, true, true, c.env)
			if strings.Join(run.Events, ",") != "session_start" {
				t.Errorf("events registered = %q, want only session_start", run.Events)
			}
			want := [][]string{{"setStatus", "yolo", c.want}}
			if got, _ := json.Marshal(run.Calls); string(got) != mustJSON(t, want) {
				t.Errorf("calls = %s, want %s", got, mustJSON(t, want))
			}
		})
	}
	for _, a := range extensionAdapters {
		source := extensionSource(t, packNamed(t, packs, a.pack), a.into)
		if run := runExtension(t, source, false, true, codexTables(a.agent)); len(run.Calls) != 0 {
			t.Errorf("%s extension with no UI called %v, want nothing", a.pack, run.Calls)
		}
		if run := runExtension(t, source, true, false, codexTables(a.agent)); len(run.Calls) != 0 {
			t.Errorf("%s extension with no yolo on PATH called %v, want nothing", a.pack, run.Calls)
		}
	}
}

// opencodeTUIConfig is the file the opencode pack lists its plugin in, home-relative, and
// opencodePluginSpec is the spec it puts in that list. opencode resolves a relative spec
// against the directory of the config file that lists it.
//
// It is tui.JSONC, not tui.json, and the difference is a user's theme. opencode 1.18.32 reads
// both files in the global config dir and concatenates their `plugin` lists, but its one-time
// migration moves `theme`, `keybinds` and `tui` out of opencode.json into tui.json ONLY while
// tui.json does not exist (docs/design/agent-footer.md §2.1). A tui.json yolo created first
// would strand those keys in opencode.json, where the TUI no longer reads them.
const (
	opencodeTUIConfig  = ".config/opencode/tui.jsonc"
	opencodePluginSpec = "./yolo/footer.js"
)

// opencodeSlotModes is every host slot opencode 1.18.32 renders, with the mode it renders it
// in, read from the binary (`L(t.Slot,{name:"…",mode:"…"})`). A slot rendered with no mode is
// `append`, the Slot component's default in @opentui/solid 0.4.5. Only `append` keeps the
// host's own content: `replace` shows plugin output instead of it, and `single_winner` shows
// one plugin's (DIR-FT2).
var opencodeSlotModes = map[string]string{
	"app": "append", "app_bottom": "append", "home_bottom": "append",
	"home_prompt_right": "append", "session_prompt_right": "append", "sidebar_content": "append",
	"home_logo": "replace", "home_prompt": "replace", "session_prompt": "replace",
	"home_footer": "single_winner", "sidebar_title": "single_winner", "sidebar_footer": "single_winner",
}

// opencodeHarness loads a TUI plugin the way opencode 1.18.32 does (the module's default export,
// `{ id, tui }`), calls tui() with an API whose slots.register records what it is handed, and
// renders every registered slot with a slot context whose theme answers $MUTED (or has no
// theme at all when it is empty). It prints the module's shape and the rendered elements.
const opencodeHarness = `
import plugin from "./plugin.mjs";
const registered = [];
const api = { slots: { register(p) { registered.push(p); return "yolo-footer"; } } };
await plugin.tui(api, undefined, { id: "yolo-footer", source: "file", state: "first" });
const ctx = process.env.MUTED ? { theme: { current: { textMuted: process.env.MUTED } } } : {};
console.log(JSON.stringify({
	keys: Object.keys(plugin).sort(),
	id: plugin.id,
	tui: typeof plugin.tui,
	registered: registered.map((p) => ({
		keys: Object.keys(p).sort(),
		slots: Object.fromEntries(Object.entries(p.slots).map(([name, render]) => [name, render(ctx, {})])),
	})),
}));
`

// opencodeJSXStub stands in for `@opentui/solid/jsx-runtime`, which opencode maps to the copy in
// its own binary: it returns what the plugin asked for, so the test can read it.
const opencodeJSXStub = `export function jsx(type, props) { return { type, props }; }
export const jsxs = jsx;
`

type opencodeRun struct {
	Keys       []string `json:"keys"`
	ID         string   `json:"id"`
	TUI        string   `json:"tui"`
	Registered []struct {
		Keys  []string `json:"keys"`
		Slots map[string]struct {
			Type  string         `json:"type"`
			Props map[string]any `json:"props"`
		} `json:"slots"`
	} `json:"registered"`
}

// runOpencodePlugin runs the shipped plugin file under node through opencodeHarness, with the
// stand-in yolo on PATH when withYolo is set.
func runOpencodePlugin(t *testing.T, source []byte, withYolo bool, muted string, env map[string]string) opencodeRun {
	t.Helper()
	node := requireNode(t, "the opencode plugin")
	dir := t.TempDir()
	stub := filepath.Join(dir, "node_modules", "@opentui", "solid")
	if err := os.MkdirAll(stub, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		filepath.Join(dir, "plugin.mjs"):  string(source),
		filepath.Join(dir, "harness.mjs"): opencodeHarness,
		filepath.Join(stub, "package.json"): `{"name": "@opentui/solid", "type": "module", ` +
			`"exports": {"./jsx-runtime": "./jsx-runtime.js"}}`,
		filepath.Join(stub, "jsx-runtime.js"): opencodeJSXStub,
	} {
		if err := os.WriteFile(name, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bin := t.TempDir()
	if err := os.Symlink(node, filepath.Join(bin, "node")); err != nil {
		t.Fatal(err)
	}
	if withYolo {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(self, filepath.Join(bin, "yolo")); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command(node, "harness.mjs")
	cmd.Dir = dir
	cmd.Env = []string{"PATH=" + bin, "HOME=" + t.TempDir(), asYolo + "=1", "MUTED=" + muted}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("running the opencode plugin under node: %v\n%s", err, out)
	}
	var run opencodeRun
	if err := json.Unmarshal(bytes.TrimSpace(out), &run); err != nil {
		t.Fatalf("harness output is not the expected JSON (%v): %s", err, out)
	}
	return run
}

// TestOpencodeAdapter is build step 7's opencode half: the opencode pack lists its plugin in
// tui.jsonc's `plugin` through the tui surface's DEFAULTS (OQ-FT1), delivers the plugin where
// that spec resolves, and the plugin, loaded as opencode loads a file plugin, adds the notch
// in append slots only, never over opencode's own content (DIR-FT2).
func TestOpencodeAdapter(t *testing.T) {
	p := packNamed(t, shippedPacks(t), "opencode")
	s := surfaceOf(t, p, "opencode", "tui")
	if s.Path != "~/"+opencodeTUIConfig {
		t.Errorf("opencode tui surface path = %q, want ~/%s: a global TUI config opencode reads, and not "+
			"tui.json, whose existence stops opencode moving a theme out of opencode.json", s.Path, opencodeTUIConfig)
	}
	// Only the plugin list, so a tui.jsonc of the user's gains nothing else of yolo's.
	if got, want := mustJSON(t, s.DefaultsMap()), `{"plugin":["`+opencodePluginSpec+`"]}`; got != want {
		t.Errorf("opencode tui defaults = %s, want %s", got, want)
	}
	// The spec resolves, against tui.jsonc's own directory, to where the files contribution
	// delivers the plugin — and not into plugin/ or plugins/, which opencode's SERVER scans.
	into := path.Join(path.Dir(opencodeTUIConfig), opencodePluginSpec)
	source := extensionSource(t, p, into)
	for _, seg := range strings.Split(path.Dir(into), "/") {
		if seg == "plugin" || seg == "plugins" {
			t.Errorf("the opencode plugin lands at %s, in a directory opencode's server loader scans", into)
		}
	}
	if args := extensionArgs(t, source); len(args) == 0 {
		t.Fatal("no renderer arguments")
	}

	run := runOpencodePlugin(t, source, true, "MUTED", map[string]string{"YOLO_VERSION": "0.10.0"})
	// A file plugin default-exports { id, tui }: opencode refuses a path plugin with no id and
	// a module exporting server() too.
	if strings.Join(run.Keys, ",") != "id,tui" || run.ID == "" || run.TUI != "function" {
		t.Fatalf("opencode plugin default export: keys=%v id=%q tui=%s, want exactly { id, tui() }", run.Keys, run.ID, run.TUI)
	}
	if len(run.Registered) != 1 {
		t.Fatalf("the plugin registered %d slot plugins, want 1", len(run.Registered))
	}
	reg := run.Registered[0]
	// A slot plugin carries no id of its own: opencode's API assigns one (TuiSlotPlugin's
	// `id?: never`).
	if strings.Join(reg.Keys, ",") != "slots" {
		t.Errorf("registered slot plugin keys = %v, want only slots", reg.Keys)
	}
	if len(reg.Slots) == 0 {
		t.Fatal("the plugin registered no slot")
	}
	for name, el := range reg.Slots {
		if mode := opencodeSlotModes[name]; mode != "append" {
			t.Errorf("the plugin fills slot %q, which opencode renders as %q: only an append slot keeps "+
				"opencode's own content (DIR-FT2)", name, mode)
		}
		if el.Type != "text" || mustJSON(t, el.Props) != `{"children":"yolo: jail","fg":"MUTED"}` {
			t.Errorf("slot %s renders %s %s, want a text element `yolo: jail` in the theme's muted color",
				name, el.Type, mustJSON(t, el.Props))
		}
	}

	t.Run("at the host", func(t *testing.T) {
		run := runOpencodePlugin(t, source, true, "MUTED", nil)
		for name, el := range run.Registered[0].Slots {
			if el.Props["children"] != "yolo: host" {
				t.Errorf("slot %s at the host renders %v, want yolo: host", name, el.Props["children"])
			}
		}
	})
	t.Run("a theme with no muted color leaves the default", func(t *testing.T) {
		run := runOpencodePlugin(t, source, true, "", map[string]string{"YOLO_VERSION": "0.10.0"})
		for name, el := range run.Registered[0].Slots {
			if got := mustJSON(t, el.Props); got != `{"children":"yolo: jail"}` {
				t.Errorf("slot %s with no theme renders props %s, want the text alone", name, got)
			}
		}
	})
	t.Run("no yolo on PATH registers nothing", func(t *testing.T) {
		if run := runOpencodePlugin(t, source, false, "MUTED", map[string]string{"YOLO_VERSION": "0.10.0"}); len(run.Registered) != 0 {
			t.Errorf("with no yolo on PATH the plugin registered %d slot plugins, want none", len(run.Registered))
		}
	})
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestBridgedRoutesAreMarked: the claude and copilot adapters mark `(bridge)` on exactly the
// shipped providers the agent reaches through an adapter's address rather than one of the
// provider's own (§1.1: the pack marks its bridged routes, yolo does not infer them). The set
// is computed here from the shipped manifests with the launch's own resolver, so a provider
// pack added later with no endpoint of the agent's protocol fails this until the adapter
// lists it. That matters most for claude, whose command is frozen into a host file on the
// first `yolo host apply` (§3).
//
// The profile is named unlike its provider on purpose: a user's own profile over a bridged
// provider is bridged too, so the mark has to follow the provider id, not a profile name.
func TestBridgedRoutesAreMarked(t *testing.T) {
	packs := shippedPacks(t)
	composed, err := packload.ComposeProviders(nil, packs)
	if err != nil {
		t.Fatalf("composing the shipped providers: %v", err)
	}
	var providers []packdecl.ProviderContribution
	for _, p := range packs {
		providers = append(providers, p.Decl.Providers()...)
	}
	if len(providers) == 0 {
		t.Fatal("no shipped provider to check")
	}
	_, copilotBody := copilotAdapter(t)
	copilotHome := homeWithCopilotScript(t, copilotBody)
	// A status-line adapter is run the way its agent runs it; an extension adapter's argument
	// array is run against the renderer directly, since what the extension adds around it (the
	// spawn and the setStatus call) is TestExtensionAdaptersSetOneYoloStatus's to pin.
	statusLine := func(pack, surface, home string) func(map[string]string) string {
		command, _ := statusLineDefault(t, surfaceOf(t, packNamed(t, packs, pack), pack, surface))["command"].(string)
		return func(env map[string]string) string { return runStatusCommand(t, command, home, claudeStatus, env) }
	}
	extension := func(a extensionAdapter) func(map[string]string) string {
		args := extensionArgs(t, extensionSource(t, packNamed(t, packs, a.pack), a.into))
		return func(env map[string]string) string { return runFooterArgv(t, args, env) }
	}

	for _, agent := range []struct {
		bin, pack string
		render    func(map[string]string) string
		// expectBridged says a shipped provider reaches this agent only through an adapter
		// today, so a run that saw none checked nothing. pi speaks only OpenAI wires and the
		// shipped adapters all convert TO anthropic, so pi has no bridged route to mark.
		expectBridged bool
	}{
		{"claude", "claude", statusLine("claude", "settings", t.TempDir()), true},
		{"copilot", "copilot", statusLine("copilot", "config", copilotHome), true},
		{"pi", "pi", extension(extensionAdapters[0]), false},
		{"oh-omp", "omp", extension(extensionAdapters[1]), true},
	} {
		spoken := packNamed(t, packs, agent.pack).Decl.SpokenProtocols(agent.bin)
		if len(spoken) == 0 {
			t.Fatalf("the %s pack declares no protocols, so no route of its can be told bridged", agent.pack)
		}
		sawBridged, sawResolved := false, false
		for _, prov := range providers {
			v, _ := composed.Get(prov.Name)
			entry, _ := v.(*jsonx.OrderedMap)
			res, err := packload.ResolveProtocol(agent.bin, spoken, prov.Name, entry, nil)
			if err != nil {
				continue // the agent cannot use this provider at all: nothing to mark
			}
			_, native := prov.Endpoints[res.Protocol]
			bridged := res.Protocol != "" && !native
			sawBridged = sawBridged || bridged
			sawResolved = true

			env := map[string]string{
				"YOLO_VERSION":      "0.10.0",
				"YOLO_USE_PROFILES": `{"` + agent.bin + `": "mine"}`,
				"YOLO_PROFILES":     `{"mine": {"provider": "` + prov.Name + `"}}`,
				"YOLO_PROVIDERS":    `{"` + prov.Name + `": {}}`,
			}
			got := agent.render(env)
			if marked := strings.Contains(got, "mine (bridge)"); marked != bridged {
				t.Errorf("%s footer for a profile over %s = %q: bridged=%v (resolved %q, native endpoints %v), "+
					"want the `(bridge)` mark exactly when the route is bridged — the adapter's --bridged list "+
					"is out of step with the shipped providers", agent.bin, prov.Name, got, bridged,
					res.Protocol, sortedKeys(prov.Endpoints))
			}
		}
		if !sawResolved {
			t.Errorf("no shipped provider resolves for %s at all: this test checks nothing", agent.bin)
		}
		if agent.expectBridged && !sawBridged {
			t.Errorf("no shipped provider resolves through an adapter for %s: this test checks nothing", agent.bin)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestFooterIsOnlyEverADefault pins OQ-FT1's layer across every shipped pack: the footer
// is written in the owning pack's DEFAULTS and nowhere that outranks a user — no managed
// key (either autonomy posture) and no config-overlay — or a user's own statusLine would
// lose to yolo's (§3's table).
func TestFooterIsOnlyEverADefault(t *testing.T) {
	// The key each footer adapter writes: statusLine for the status-line agents, on any
	// surface, and opencode's TUI plugin list.
	footerKey := func(surface string) []string {
		if surface == "opencode/tui" {
			return []string{"statusLine", "plugin"}
		}
		return []string{"statusLine"}
	}
	for _, p := range shippedPacks(t) {
		for _, autonomous := range []bool{true, false} {
			surfaces, _ := p.SurfacesFor(autonomous)
			for _, s := range surfaces {
				for _, key := range footerKey(s.Agent + "/" + s.Name) {
					if _, set := s.ManagedMap()[key]; set {
						t.Errorf("pack %s manages %s/%s %s (autonomous=%v): the footer must stay a default",
							p.Name, s.Agent, s.Name, key, autonomous)
					}
				}
			}
		}
		for _, ov := range p.Decl.ConfigOverlayContributions() {
			var body map[string]any
			_ = json.Unmarshal(ov.Config, &body)
			for _, key := range footerKey(ov.Surface) {
				if walkHasKey(body, key) {
					t.Errorf("pack %s overlays a %s onto %s: an overlay outranks the user's host file", p.Name, key, ov.Surface)
				}
			}
		}
	}
}

func walkHasKey(v any, key string) bool {
	m, ok := v.(map[string]any)
	if !ok {
		return false
	}
	for k, child := range m {
		if k == key || walkHasKey(child, key) {
			return true
		}
	}
	return false
}

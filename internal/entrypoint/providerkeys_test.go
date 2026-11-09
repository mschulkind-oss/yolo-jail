package entrypoint

// providerkeys_test.go pins the PROVIDER KEYS at the jail boot (providerkeys.go,
// agentcfg/providerkeys.go): the settings claude's provider selection decides, which no captured
// in-jail edit may set. Through ConfigurePackSurfaces, the boot loop's entry, over the real
// embedded claude pack, two or three boots sharing one home and one capture store — so deleting
// the boot's hand-off of the keys (renderPlannedSurface's withProviderKeys), the render's drop,
// or the pack's declaration fails here.
//
// THE REPRODUCTION is the 2026-10-11 split, in its measured shape. The maintainer's config ran
// claude off Bedrock (`"profile": {"*": "bedrock", "claude": null}`); the boot said "4 keys from
// captured in-jail edits" for ~/.claude/settings.json; the status line said Bedrock while yolo
// delivered claude no AWS credentials; and the next boot, with claude back on Bedrock, said "2
// keys". The fixture: a Bedrock boot with a pack shaped like the maintainer's
// claude-bedrock-global (a config-overlay gated on the `bedrock` profile setting the region
// prefix and `model`), Claude Code toggling two settings of its own, then a boot with claude off
// Bedrock that has no render baseline (an adopting boot). Before the fix that boot captured FOUR
// keys — env (CLAUDE_CODE_USE_BEDROCK and the region prefix), model, and the two toggles — and
// the rendered file kept CLAUDE_CODE_USE_BEDROCK; the Bedrock boot after it narrowed the two
// Bedrock keys away, leaving two.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/capturelog"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

type providerKeyFixture struct {
	t        *testing.T
	home, ws string
	hostDir  string
	packs    []*packload.Pack
}

// newProviderKeyFixture is the embedded Bedrock pack set, plus extra packs written from their
// manifests (name → pack.json body).
func newProviderKeyFixture(t *testing.T, extra map[string]string) *providerKeyFixture {
	t.Helper()
	f := &providerKeyFixture{t: t, home: t.TempDir(), ws: t.TempDir()}
	for _, n := range bedrockPacks {
		p, err := embeddedPack(n)
		if err != nil {
			t.Fatal(err)
		}
		f.packs = append(f.packs, p)
	}
	root := t.TempDir()
	for name, body := range extra {
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "pack.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		p, problems := packload.LoadDir(dir, name)
		if p == nil || len(problems) > 0 {
			t.Fatalf("pack %s: %v", name, problems)
		}
		f.packs = append(f.packs, p)
	}
	f.hostDir = withCtxRoot(t, t.TempDir(), "claude")
	return f
}

// boot renders the pack set with claude on profile ("" for none) and returns the composed
// settings file and the boot's stderr.
func (f *providerKeyFixture) boot(profile string) (map[string]any, string) {
	t := f.t
	t.Helper()
	providers, err := packload.ComposeProviders(nil, f.packs)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := resolveShippedProfiles(f.packs, nil, providers)
	if err != nil {
		t.Fatal(err)
	}
	use := jsonx.NewOrderedMap()
	if profile != "" {
		use.Set("claude", profile)
	}
	errw := &bytes.Buffer{}
	e := &Env{Home: f.home, Workspace: f.ws, Stderr: errw, Vars: map[string]string{
		"YOLO_PROVIDERS":    mustCompactJSON(t, providers),
		"YOLO_PROFILES":     mustCompactJSON(t, packload.ProfilesWireTable(resolved)),
		"YOLO_USE_PROFILES": mustCompactJSON(t, use),
	}}
	ConfigurePackSurfaces(e, f.packs)
	if fails := e.GenFailures(); len(fails) != 0 {
		t.Fatalf("boot failed: %v\n%s", fails, errw)
	}
	return f.settings(), errw.String()
}

func (f *providerKeyFixture) settingsPath() string {
	return filepath.Join(f.home, ".claude", "settings.json")
}

func (f *providerKeyFixture) prism(name string) string {
	return filepath.Join(f.ws, ".yolo", "prism", name)
}

func (f *providerKeyFixture) settings() map[string]any {
	data, err := os.ReadFile(f.settingsPath())
	if err != nil {
		f.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		f.t.Fatalf("settings are not JSON: %v\n%s", err, data)
	}
	return m
}

// edit is the agent rewriting its own settings file: fn changes the decoded file.
func (f *providerKeyFixture) edit(fn func(m map[string]any)) {
	m := f.settings()
	fn(m)
	data, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(f.settingsPath(), data, 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *providerKeyFixture) overlay() map[string]any {
	data, err := os.ReadFile(f.prism("claude-settings.overlay.json"))
	if err != nil {
		f.t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		f.t.Fatal(err)
	}
	return m
}

func (f *providerKeyFixture) captureLog() string {
	data, _ := os.ReadFile(f.prism(capturelog.FileName))
	return string(data)
}

// bedrockGlobalPack is the shape of the maintainer's claude-bedrock-global pack: claude on the
// `bedrock` profile gets Bedrock's global inference profiles and starts on Opus 1M.
const bedrockGlobalPack = `{"name":"claude-bedrock-global","contributes":[{"kind":"config-overlay",
	"surface":"claude/settings","profile":"bedrock","config":{"managed":{
	"env":{"ANTHROPIC_BEDROCK_REGION_PREFIX":"global"},"model":"opus[1m]"}}}]}`

func envOf(m map[string]any) map[string]any {
	env, _ := m["env"].(map[string]any)
	return env
}

// THE REPRODUCTION (file header): an adopting boot off Bedrock, over a file a Bedrock boot
// wrote, must not keep claude on Bedrock.
func TestTheSplitBrainReproductionNoLongerSwitchesClaudeOntoBedrock(t *testing.T) {
	f := newProviderKeyFixture(t, map[string]string{"claude-bedrock-global": bedrockGlobalPack})
	got, boot1 := f.boot("bedrock")
	if envOf(got)["CLAUDE_CODE_USE_BEDROCK"] != "1" || got["model"] != "opus[1m]" {
		t.Fatalf("the Bedrock boot did not render the Bedrock settings the fixture needs: %v\n%s", got, boot1)
	}
	// Claude Code toggles two settings of its own in the Bedrock session.
	f.edit(func(m map[string]any) { m["autoMemoryEnabled"] = true; m["ultracode"] = true })
	// The next boot has no render baseline — an adoption, the state any lost or untrusted
	// last_render leaves — and selects claude's subscription.
	if err := os.Remove(f.prism("claude-settings.last_render")); err != nil {
		t.Fatal(err)
	}
	got, stderr := f.boot("")
	if v, set := envOf(got)["CLAUDE_CODE_USE_BEDROCK"]; set {
		t.Errorf("claude off Bedrock still renders CLAUDE_CODE_USE_BEDROCK=%v — Claude Code runs on "+
			"Bedrock with no credentials while yolo selected its subscription:\n%v\n%s", v, got, stderr)
	}
	ov := f.overlay()
	if env, _ := ov["env"].(map[string]any); env["CLAUDE_CODE_USE_BEDROCK"] != nil {
		t.Errorf("the switch survived in the capture overlay, to come back on every later boot: %v", ov)
	}
	if ov["autoMemoryEnabled"] != true || ov["ultracode"] != true {
		t.Errorf("the agent's own toggles must stay captured, got %v", ov)
	}
	for _, want := range []string{"dropped the captured in-jail edit env.CLAUDE_CODE_USE_BEDROCK",
		"provider selection decides that key", "yolo config reset claude"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the boot must name the drop (%q):\n%s", want, stderr)
		}
	}
	// Claude back on Bedrock (the boot after the incident): the two Bedrock keys are the
	// selection's and its overlay pack's, and the agent's two toggles are all the capture holds.
	f.boot("bedrock")
	if ov := f.overlay(); len(ov) != 2 || ov["autoMemoryEnabled"] != true || ov["ultracode"] != true {
		t.Errorf("back on Bedrock the capture must hold the agent's two toggles alone, got %v", ov)
	}
	// The capture log records the drop although neither sidecar ever held the key.
	if log := f.captureLog(); !strings.Contains(log, "claude/settings boot (adopting the file") ||
		!strings.Contains(log, "x env.CLAUDE_CODE_USE_BEDROCK = \"1\"") {
		t.Errorf("capture.log does not record the adopted switch and its drop:\n%s", log)
	}
}

// A captured switch an older yolo left in the sidecar (the state the maintainer's workspace was
// in at the 12:16 boot) is taken out on the next boot off the platform: steady state, not only
// adoption.
func TestACapturedSwitchInTheSidecarIsDroppedAtTheNextBoot(t *testing.T) {
	f := newProviderKeyFixture(t, nil)
	f.boot("")
	if err := os.WriteFile(f.prism("claude-settings.overlay.json"), []byte(
		`{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","AWS_REGION":"us-east-1","KEEP":"x"},`+
			`"model":"global.anthropic.claude-opus-5-5","autoMemoryEnabled":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, stderr := f.boot("")
	env := envOf(got)
	for _, k := range []string{"CLAUDE_CODE_USE_BEDROCK", "AWS_REGION"} {
		if _, set := env[k]; set {
			t.Errorf("captured %s reached claude's subscription session: %v\n%s", k, env, stderr)
		}
	}
	if _, set := got["model"]; set {
		t.Errorf("a Bedrock-only model id reached claude's subscription session: %v", got["model"])
	}
	if env["KEEP"] != "x" || got["autoMemoryEnabled"] != true {
		t.Errorf("captured edits that are no provider key must stay: %v", got)
	}
	log := f.captureLog()
	for _, want := range []string{"- env.CLAUDE_CODE_USE_BEDROCK (was \"1\"): dropped: claude's provider",
		"- model (was \"global.anthropic.claude-opus-5-5\")"} {
		if !strings.Contains(log, want) {
			t.Errorf("capture.log is missing %q:\n%s", want, log)
		}
	}
	// And the drop is persisted: the boot after says nothing about it again.
	_, stderr = f.boot("")
	if strings.Contains(stderr, "dropped the captured in-jail edit") {
		t.Errorf("a second boot repeated a drop the first one persisted:\n%s", stderr)
	}
}

// IN BOTH DIRECTIONS: on Bedrock the switch is the selection's (computed), a captured region is
// still not the user's to set, and a Bedrock model id they picked is theirs to keep; off it, a
// subscription model they picked stays too.
func TestProviderKeysFollowTheSelectionBothWays(t *testing.T) {
	f := newProviderKeyFixture(t, nil)
	f.boot("bedrock")
	f.edit(func(m map[string]any) {
		envOf(m)["AWS_REGION"] = "eu-west-1"
		m["model"] = "global.anthropic.claude-sonnet-5-5"
	})
	got, _ := f.boot("bedrock")
	if envOf(got)["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("claude on Bedrock lost its switch: %v", got)
	}
	if _, set := envOf(got)["AWS_REGION"]; set {
		t.Errorf("a captured AWS_REGION overrode the selection's region: %v", envOf(got))
	}
	if got["model"] != "global.anthropic.claude-sonnet-5-5" {
		t.Errorf("on Bedrock a Bedrock model the user picked is theirs, got %v", got["model"])
	}
	got, _ = f.boot("")
	if _, set := envOf(got)["CLAUDE_CODE_USE_BEDROCK"]; set {
		t.Errorf("off Bedrock the switch must be gone: %v", envOf(got))
	}
	// Off Bedrock the Bedrock-only id is gone, and the provider change writes its tier's word in
	// its place, which claude resolves on its own login (MM-D43).
	if got["model"] != "sonnet" {
		t.Errorf("off Bedrock the Bedrock-only model must give way to its tier word sonnet, got %v", got["model"])
	}
	f.edit(func(m map[string]any) { m["model"] = "opus[1m]" })
	got, _ = f.boot("")
	if got["model"] != "opus[1m]" {
		t.Errorf("a subscription model the user picked must stay theirs, got %v", got["model"])
	}
}

// PP-D13 (ruled 2026-10-11: "Inside of JAIL, if we're not already managing the host config, I
// would say a profile choice does override the host settings file being imported"): the user's
// own host ~/.claude/settings.json, folded into the jail as its host layer while host management
// is off, cannot put claude on a provider its selection did not pick. Its provider keys leave the
// bytes the jail composes — dropped when the selection sets none, replaced when it sets one —
// each named at boot with the host file, while every other host key reaches the jail as before
// and the host file itself is never written. Through ConfigurePackSurfaces, so deleting the drop
// in renderPlannedSurface fails here.
func TestAProfileChoiceOverridesTheImportedHostSettingsInAJail(t *testing.T) {
	f := newProviderKeyFixture(t, nil)
	host := filepath.Join(f.hostDir, "settings.json")
	const hostBody = `{"env":{"CLAUDE_CODE_USE_BEDROCK":"1","ANTHROPIC_BASE_URL":"http://127.0.0.1:9/gw",` +
		`"MY_OWN":"kept"},"model":"opus","theme":"dark"}`
	if err := os.WriteFile(host, []byte(hostBody), 0o644); err != nil {
		t.Fatal(err)
	}
	got, stderr := f.boot("codex")
	env := envOf(got)
	if _, set := env["CLAUDE_CODE_USE_BEDROCK"]; set {
		t.Errorf("the host file's Bedrock switch reached a claude on codex: %v\n%s", env, stderr)
	}
	if v := env["ANTHROPIC_BASE_URL"]; v == "http://127.0.0.1:9/gw" {
		t.Errorf("the host file's gateway reached a claude on codex: %v", env)
	}
	if env["MY_OWN"] != "kept" || got["theme"] != "dark" {
		t.Errorf("every other host key must still reach the jail: %v", got)
	}
	// The host's saved model is the host's, not an in-jail pick (OQ-SW1), so the selection's saved
	// model outranks it: codex's list has no Opus-tier model, so its default (MM-D43).
	if got["model"] != "gpt-6.1-sol" {
		t.Errorf("model = %v, want the codex list's default over the host's opus", got["model"])
	}
	for _, want := range []string{
		"claude/settings: your host ~/.claude/settings.json sets env.CLAUDE_CODE_USE_BEDROCK = \"1\"",
		"provider selection decides that key", "Your host file is unchanged",
		"choose a provider that sets it with `yolo active-provider"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the boot must name the override (%q):\n%s", want, stderr)
		}
	}
	if data, _ := os.ReadFile(host); string(data) != hostBody {
		t.Errorf("the user's host file was written (PP-D1): %s", data)
	}
	// Replaced: on Bedrock the switch is the selection's, and the gateway still is not.
	got, stderr = f.boot("bedrock")
	if envOf(got)["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Errorf("claude on Bedrock lost the selection's switch: %v", envOf(got))
	}
	if envOf(got)["ANTHROPIC_BASE_URL"] == "http://127.0.0.1:9/gw" {
		t.Errorf("the host gateway reached claude on Bedrock: %v", envOf(got))
	}
	if strings.Contains(stderr, "sets env.CLAUDE_CODE_USE_BEDROCK") {
		t.Errorf("a host key the selection sets to the same value changes nothing, so is not named:\n%s", stderr)
	}
}

// THE JAIL-NOTCH CHECK (notePlatformSwitchesOn): a switch that reaches the rendered file from a
// layer the provider keys do not reach — a pack's config-overlay — is named at boot, with the
// pack it came from.
func TestTheBootNamesASwitchTheRenderedFileCarries(t *testing.T) {
	f := newProviderKeyFixture(t, map[string]string{"switcher": `{"name":"switcher","contributes":[
	  {"kind":"config-overlay","surface":"claude/settings","config":{"managed":{"env":{"CLAUDE_CODE_USE_BEDROCK":"1"}}}}]}`})
	got, stderr := f.boot("codex")
	if envOf(got)["CLAUDE_CODE_USE_BEDROCK"] != "1" {
		t.Fatalf("the fixture needs the overlay's switch in the file, got %v", got)
	}
	for _, want := range []string{"claude: ~/.claude/settings.json, as this boot rendered it, sets " +
		"CLAUDE_CODE_USE_BEDROCK", `from pack "switcher"'s config-overlay`,
		"no \"aws-bedrock\" provider is selected"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("the boot must name the conflict (%q):\n%s", want, stderr)
		}
	}
	// On Bedrock the switch is served: no line.
	_, stderr = f.boot("bedrock")
	if strings.Contains(stderr, "as this boot rendered it") {
		t.Errorf("a served switch was named as a conflict:\n%s", stderr)
	}
}

// The capture log records an ordinary captured edit with its value, and never a credential.
func TestTheCaptureLogRecordsEveryOverlayChange(t *testing.T) {
	f := newProviderKeyFixture(t, nil)
	f.boot("")
	const secret = "sk-ant-oat01-0123456789abcdefghijABCDEFGHIJ0123456789"
	f.edit(func(m map[string]any) {
		m["theme"] = "dark"
		if envOf(m) == nil {
			m["env"] = map[string]any{}
		}
		envOf(m)["MY_SERVICE_TOKEN"] = secret
		envOf(m)["OPAQUE"] = secret
	})
	f.boot("")
	log := f.captureLog()
	if !strings.Contains(log, `claude/settings boot: + theme = "dark" (captured)`) {
		t.Errorf("capture.log does not record the captured key and value:\n%s", log)
	}
	if strings.Contains(log, secret) {
		t.Errorf("capture.log wrote a credential:\n%s", log)
	}
	if !strings.Contains(log, "+ env.MY_SERVICE_TOKEN = <redacted") ||
		!strings.Contains(log, "+ env.OPAQUE = <redacted") {
		t.Errorf("capture.log must name a redacted key and say it was redacted:\n%s", log)
	}
}

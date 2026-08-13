package entrypoint

// hostrmwcodec_test.go pins the CODEC-AWARENESS of the RMW mechanism, host notch first
// because that is where the absence of it destroyed real data.
//
// The reproduction, verbatim: a codex user with a valid ~/.codex/config.toml runs
// `yolo apply --host --assert`. Before this, the host RMW path read the file as JSON
// (unparseable => an empty object) and wrote it back as JSON, so `model` and `[tui]` — keys
// yolo never declares and RMW promises to preserve — were gone, and the file was no longer
// TOML. Both halves are tested: the read must SEE the user's keys, and the write must emit
// the surface's codec.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/tomlx"
)

// codexHostHome makes a temp home holding a ~/.codex/config.toml with the given content and
// returns (home, path).
func codexHostHome(t *testing.T, content string) (string, string) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return home, path
}

// renderCodexHost runs the shipped codex pack at the host notch (what `apply --host
// --assert` does) and returns the codex/config result.
func renderCodexHost(t *testing.T, home string, observe bool) HostRenderResult {
	t.Helper()
	codex, err := embeddedPack("codex")
	if err != nil {
		t.Fatalf("embedded codex: %v", err)
	}
	results, rerr := RenderHostPack(codex, home, observe, nil)
	if rerr != nil {
		t.Fatalf("RenderHostPack: %v", rerr)
	}
	for _, r := range results {
		if r.Surface == "codex/config" {
			return r
		}
	}
	t.Fatalf("no codex/config result: %+v", results)
	return HostRenderResult{}
}

// THE REPRODUCTION. A valid config.toml keeps every user key, gains yolo's managed keys, and
// is still TOML afterwards.
func TestHostRenderCodexTOMLRoundTrips(t *testing.T) {
	home, path := codexHostHome(t, "model = \"gpt-5\"\n\n[tui]\ntheme = \"dark\"\n")

	if got := renderCodexHost(t, home, false); got.Action != "rendered" {
		t.Fatalf("codex/config action = %q, want rendered", got.Action)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// Still TOML — the file the agent has to parse.
	decoded, derr := tomlx.Decode(raw)
	if derr != nil {
		t.Fatalf("the rendered file is not valid TOML (%v):\n%s", derr, raw)
	}
	// The user's own keys survive: this is what RMW promises and what was lost.
	if decoded["model"] != "gpt-5" {
		t.Errorf("the user's `model` key was lost (RMW must preserve what yolo does not "+
			"declare): %#v\n%s", decoded["model"], raw)
	}
	tui, isTable := decoded["tui"].(map[string]any)
	if !isTable || tui["theme"] != "dark" {
		t.Errorf("the user's [tui] table was lost: %#v\n%s", decoded["tui"], raw)
	}
	// yolo's guarded-posture managed keys are asserted.
	if decoded["approval_policy"] != "on-request" {
		t.Errorf("approval_policy = %#v, want on-request (managed)\n%s",
			decoded["approval_policy"], raw)
	}
	if decoded["sandbox_mode"] != "workspace-write" {
		t.Errorf("sandbox_mode = %#v, want workspace-write (managed)\n%s",
			decoded["sandbox_mode"], raw)
	}
	// And the file is not JSON. A cheap, direct check on the exact regression: the old path
	// wrote `{`-braced JSON here.
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		t.Errorf("a toml surface was written as JSON:\n%s", raw)
	}
}

// A second --assert must be byte-identical: the emitter is deterministic, so re-applying is
// a no-op rather than a churning diff (and rmwProvenance's sorted output stays stable).
func TestHostRenderCodexTOMLSecondApplyIsIdentical(t *testing.T) {
	home, path := codexHostHome(t, "model = \"gpt-5\"\n\n[tui]\ntheme = \"dark\"\n")

	renderCodexHost(t, home, false)
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	renderCodexHost(t, home, false)
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Errorf("a second apply changed the file:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
}

// A TOML file yolo cannot parse is REFUSED and left BYTE-FOR-BYTE untouched. This is the half
// that turns data loss into a safe no-op: a codec-correct writer over an empty read would
// still have destroyed the file.
func TestHostRenderRefusesUnparseableTOML(t *testing.T) {
	broken := "model = \"unterminated\n[tui\nthis is not toml at all ][\n"
	home, path := codexHostHome(t, broken)

	got := renderCodexHost(t, home, false)
	if !strings.HasPrefix(got.Action, "refused:") {
		t.Fatalf("action = %q, want a refusal for an unparseable file", got.Action)
	}
	if !strings.Contains(got.Action, "not valid TOML") {
		t.Errorf("the refusal must say WHY: %q", got.Action)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(after) != broken {
		t.Errorf("a refused surface must be untouched:\n--- before ---\n%s\n--- after ---\n%s",
			broken, after)
	}
}

// The OBSERVE posture reports the refusal too. A dry-run that promised "would render" for a
// file --assert will decline is a preview that lies about the only outcome that matters.
func TestHostRenderObserveReportsRefusal(t *testing.T) {
	home, _ := codexHostHome(t, "model = \"unterminated\n[tui\n][\n")
	got := renderCodexHost(t, home, true)
	if !strings.HasPrefix(got.Action, "refused:") {
		t.Errorf("observe action = %q, want the same refusal --assert would give", got.Action)
	}
}

// An ABSENT file is not a refusal — it is the ordinary first-apply case, and every key yolo
// writes is an add. (A refusal here would make `apply --host` unable to configure a tool the
// user has not run yet.)
func TestHostRenderCodexTOMLCreatesAbsentFile(t *testing.T) {
	home := t.TempDir()
	if got := renderCodexHost(t, home, false); got.Action != "rendered" {
		t.Fatalf("action = %q, want rendered for an absent file", got.Action)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".codex", "config.toml"))
	if err != nil {
		t.Fatalf("the file should have been created: %v", err)
	}
	decoded, derr := tomlx.Decode(raw)
	if derr != nil {
		t.Fatalf("created file is not valid TOML (%v):\n%s", derr, raw)
	}
	if decoded["approval_policy"] != "on-request" {
		t.Errorf("managed key missing from a freshly created file:\n%s", raw)
	}
}

// An EMPTY file is the same case as absent: nothing to preserve, so render rather than refuse.
func TestHostRenderCodexTOMLEmptyFileRenders(t *testing.T) {
	home, _ := codexHostHome(t, "")
	if got := renderCodexHost(t, home, false); got.Action != "rendered" {
		t.Errorf("action = %q, want rendered for an empty file", got.Action)
	}
}

// TOML VALUE TYPES survive the round-trip. The trap is integers: the RMW writer's value model
// carries them as jsonx integer literals, and lowering through jsonx.Plain (float64) would
// rewrite a user's `4096` as `4096.0` on every apply — a silent retype of a value yolo does
// not even declare.
func TestHostRenderCodexTOMLPreservesValueTypes(t *testing.T) {
	home, path := codexHostHome(t, strings.Join([]string{
		`model_max_output_tokens = 4096`,
		`temperature = 0.7`,
		`hide_agent_reasoning = true`,
		`profile = "work"`,
		`extra_args = ["--a", "--b"]`,
		``,
		`[mcp_servers.mine]`,
		`command = "/usr/local/bin/mine"`,
		`args = ["--serve"]`,
		``,
	}, "\n"))

	renderCodexHost(t, home, false)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "4096.0") {
		t.Errorf("an integer was retyped as a float:\n%s", raw)
	}
	decoded, derr := tomlx.Decode(raw)
	if derr != nil {
		t.Fatalf("not valid TOML (%v):\n%s", derr, raw)
	}
	if decoded["model_max_output_tokens"] != int64(4096) {
		t.Errorf("model_max_output_tokens = %#v (%T), want int64(4096)",
			decoded["model_max_output_tokens"], decoded["model_max_output_tokens"])
	}
	if decoded["temperature"] != 0.7 {
		t.Errorf("temperature = %#v, want 0.7", decoded["temperature"])
	}
	if decoded["hide_agent_reasoning"] != true {
		t.Errorf("hide_agent_reasoning = %#v, want true", decoded["hide_agent_reasoning"])
	}
	if decoded["profile"] != "work" {
		t.Errorf("profile = %#v, want work", decoded["profile"])
	}
	args, isArr := decoded["extra_args"].([]any)
	if !isArr || len(args) != 2 || args[0] != "--a" {
		t.Errorf("extra_args = %#v, want [--a --b]", decoded["extra_args"])
	}
	// The user's own MCP server: codex/config's mcp_servers IS a wholesale-managed table, so
	// with nothing declared in config the block is cleared. That is policy (announced through
	// EntryLosses), not a codec failure — assert the ANNOUNCEMENT, so the drop is never silent.
	if _, present := decoded["mcp_servers"]; present {
		if got := decoded["mcp_servers"]; len(got.(map[string]any)) != 0 {
			t.Errorf("mcp_servers should be regenerated from config (empty here): %#v", got)
		}
	}
}

// The user's undeclared MCP server IS reported as an entry loss on a TOML surface. Before,
// tableLosses read the file as JSON and so found nothing to report — the one-way-door
// confirmation gate could never fire for codex, the pack whose surface is TOML.
func TestHostRenderCodexTOMLReportsEntryLosses(t *testing.T) {
	home, _ := codexHostHome(t,
		"[mcp_servers.mine]\ncommand = \"/usr/local/bin/mine\"\n")
	got := renderCodexHost(t, home, true) // observe: the posture the gate consults
	if len(got.EntryLosses) == 0 {
		t.Fatalf("no EntryLosses for a user MCP server on a TOML surface: %+v", got)
	}
	joined := strings.Join(got.EntryLosses, "; ")
	if !strings.Contains(joined, "mcp_servers.mine") {
		t.Errorf("EntryLosses should name the server: %v", got.EntryLosses)
	}
	if !got.FirstApply {
		t.Errorf("FirstApply should be true in a home yolo has never asserted")
	}
}

// A managed key whose existing TOML value DIFFERS must be reported as an overwrite. Same
// gap as above: managedOverwrites read JSON, so the always-warn never fired for codex.
func TestHostRenderCodexTOMLReportsOverwrites(t *testing.T) {
	home, _ := codexHostHome(t, "approval_policy = \"never\"\nmodel = \"gpt-5\"\n")
	got := renderCodexHost(t, home, true)
	if len(got.Overwrites) == 0 {
		t.Fatalf("no Overwrites reported for a differing managed key on a TOML surface: %+v", got)
	}
	if !strings.Contains(strings.Join(got.Overwrites, ","), "approval_policy") {
		t.Errorf("Overwrites should name approval_policy: %v", got.Overwrites)
	}
	// A key yolo does not manage is never an overwrite.
	if strings.Contains(strings.Join(got.Overwrites, ","), "model") {
		t.Errorf("`model` is not managed and must not be reported: %v", got.Overwrites)
	}
}

// COMMENTS SURVIVE a host apply on a TOML surface — E4's `rmw` half, end to end over the
// shipped codex pack. `model` is a key yolo does not declare, so both its comments are
// untouched by the render that asserts approval_policy/sandbox_mode beside them.
//
// This test used to assert the opposite (comments dropped, drop warned) and said so: "if
// this is now implemented (E4), drop the warning instead of the test".
func TestHostRenderCodexTOMLPreservesComments(t *testing.T) {
	home, path := codexHostHome(t,
		"# my carefully documented setting\nmodel = \"gpt-5\"  # the good one\n")

	if got := renderCodexHost(t, home, true); len(got.Formatting) != 0 {
		t.Errorf("nothing is lost here, so nothing should be reported: %v", got.Formatting)
	}
	renderCodexHost(t, home, false)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"carefully documented", "the good one"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("comment %q did not survive the render:\n%s", want, raw)
		}
	}
	decoded, derr := tomlx.Decode(raw)
	if derr != nil {
		t.Fatalf("re-attaching comments produced invalid TOML (%v):\n%s", derr, raw)
	}
	if decoded["model"] != "gpt-5" {
		t.Errorf("the VALUE must survive alongside its comment:\n%s", raw)
	}
	if decoded["approval_policy"] != "on-request" {
		t.Errorf("the managed key must still be asserted:\n%s", raw)
	}
}

// RULE ① end to end: the comment above a key yolo OVERRIDES is dropped rather than left
// lying about a value that is no longer there, and the drop is reported BEFORE the write.
func TestHostRenderCodexTOMLReportsOverriddenComment(t *testing.T) {
	home, path := codexHostHome(t,
		"# I want to be prompted for everything\napproval_policy = \"never\"\n"+
			"# unrelated\nmodel = \"gpt-5\"\n")

	got := renderCodexHost(t, home, true) // observe: the report must precede the write
	if len(got.Formatting) == 0 {
		t.Fatalf("an overridden key's comment loss must be reported: %+v", got)
	}
	if !strings.Contains(strings.Join(got.Formatting, " "), "approval_policy") {
		t.Errorf("the report must name the key whose comment is dropped: %v", got.Formatting)
	}

	renderCodexHost(t, home, false)
	raw := string(mustRead(t, path))
	if strings.Contains(raw, "prompted for everything") {
		t.Errorf("the comment above an overridden key must not survive to lie:\n%s", raw)
	}
	if !strings.Contains(raw, "# unrelated") {
		t.Errorf("an untouched key's comment must survive the same render:\n%s", raw)
	}
}

// A SECOND assert on a commented file is byte-identical: comment re-attachment must not
// churn the file, or every apply produces a diff.
func TestHostRenderCodexTOMLCommentedSecondApplyIsIdentical(t *testing.T) {
	home, path := codexHostHome(t,
		"# preamble\n\n# about model\nmodel = \"gpt-5\"  # inline\n\n[tui]\ntheme = \"dark\"\n")
	renderCodexHost(t, home, false)
	first := string(mustRead(t, path))
	renderCodexHost(t, home, false)
	second := string(mustRead(t, path))
	if first != second {
		t.Errorf("a second apply churned a commented file:\n--- first ---\n%s\n--- second ---\n%s",
			first, second)
	}
}

// No comments, no report: the line only appears when there is a real loss, or it stops
// being read.
func TestHostRenderCodexTOMLQuietWithoutComments(t *testing.T) {
	home, _ := codexHostHome(t, "model = \"gpt-5\"\n")
	if got := renderCodexHost(t, home, true); len(got.Formatting) != 0 {
		t.Errorf("an uncommented file must produce no formatting warning: %v", got.Formatting)
	}
}

// A JSON surface never warns about comments (JSON has no comment syntax).
func TestHostRenderJSONSurfaceNeverWarnsComments(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte(`{"preferences":{"theme":"dark"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	results, rerr := RenderHostPack(claude, home, true, nil)
	if rerr != nil {
		t.Fatal(rerr)
	}
	for _, r := range results {
		if len(r.Formatting) != 0 {
			t.Errorf("%s: json surfaces have no comments to lose: %v", r.Surface, r.Formatting)
		}
	}
}

// An unparseable JSON file is refused for the same reason an unparseable TOML one is: the
// read cannot see the keys, so the write cannot preserve them. This was the JSON half of the
// same bug — loadObject returned {} for a truncated ~/.claude/settings.json and the render
// replaced the whole file.
func TestHostRenderRefusesUnparseableJSON(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	broken := `{"preferences":{"theme":"dark"` // truncated
	if err := os.WriteFile(settings, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	claude, err := embeddedPack("claude")
	if err != nil {
		t.Fatal(err)
	}
	results, rerr := RenderHostPack(claude, home, false, nil)
	if rerr != nil {
		t.Fatal(rerr)
	}
	var found bool
	for _, r := range results {
		if r.Surface == "claude/settings" {
			found = true
			if !strings.HasPrefix(r.Action, "refused:") {
				t.Errorf("action = %q, want a refusal for unparseable JSON", r.Action)
			}
		}
	}
	if !found {
		t.Fatalf("no claude/settings result: %+v", results)
	}
	after, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Errorf("a refused surface must be untouched:\nbefore %s\nafter  %s", broken, after)
	}
}

// ONE refused surface must not abort the pack: every other surface still renders. A refusal
// is a deliberate non-write, so treating it as a pack-level error would cost the user the
// surfaces yolo CAN render because of one file it cannot parse. copilot is the case with three
// JSON surfaces, so there is something left to render.
func TestHostRenderRefusalDoesNotAbortThePack(t *testing.T) {
	home := t.TempDir()
	broken := filepath.Join(home, ".copilot", "config.json")
	if err := os.MkdirAll(filepath.Dir(broken), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte(`{"yolo": tru`), 0o644); err != nil {
		t.Fatal(err)
	}
	copilot, err := embeddedPack("copilot")
	if err != nil {
		t.Fatal(err)
	}
	results, rerr := RenderHostPack(copilot, home, false, nil)
	if rerr != nil {
		t.Fatalf("a refusal must not surface as a pack-level error: %v", rerr)
	}
	var refused, rendered int
	for _, r := range results {
		switch {
		case strings.HasPrefix(r.Action, "refused:"):
			refused++
		case r.Action == "rendered":
			rendered++
		}
	}
	if refused != 1 {
		t.Errorf("want exactly the one broken surface refused, got %d: %+v", refused, results)
	}
	if rendered == 0 {
		t.Errorf("one refused surface aborted the whole pack: %+v", results)
	}
}

// A KEYLESS codec is refused BY NAME rather than approximated. `lines`/`raw` decode to a list
// or a string, so RMW — "assert these keys, keep the rest" — has nothing to assert; the only
// implementable version would replace the file wholesale, which is the opposite of the mode.
func TestRMWRefusesKeylessCodecs(t *testing.T) {
	for _, c := range []string{"raw", "lines"} {
		s := manifest.Surface{Agent: "acme", Name: "list", Codec: c,
			Path: "~/.acme/list", Mode: manifest.ModeRMW}
		refusal := rmwCodecRefusal(s)
		if refusal == nil {
			t.Fatalf("codec %q must be refused for RMW", c)
		}
		if !strings.Contains(refusal.Reason(), c) {
			t.Errorf("the refusal must name the codec: %q", refusal.Reason())
		}
		if !strings.Contains(refusal.Reason(), "not implemented") {
			t.Errorf("the refusal should say it is unimplemented at this notch: %q",
				refusal.Reason())
		}
	}
	// The two OBJECT codecs are accepted.
	for _, c := range []string{"json", "toml"} {
		s := manifest.Surface{Agent: "acme", Name: "cfg", Codec: c,
			Path: "~/.acme/cfg", Mode: manifest.ModeRMW}
		if refusal := rmwCodecRefusal(s); refusal != nil {
			t.Errorf("codec %q must be accepted for RMW, got %q", c, refusal.Reason())
		}
	}
}

// A keyless RMW surface must write NOTHING — not even the parent directory. The refusal is
// checked before the mkdir precisely so a declined surface leaves no trace.
func TestRMWKeylessSurfaceWritesNothing(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	s := manifest.Surface{Agent: "acme", Name: "list", Codec: "raw",
		Path: "~/.acme/list", Mode: manifest.ModeRMW, Managed: "whole file"}
	err := renderSurfaceRMWSurface(e, s, nil, nil)
	if _, isRefusal := asRMWRefusal(err); !isRefusal {
		t.Fatalf("want a refusal, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(e.Home, ".acme")); statErr == nil {
		t.Errorf("a refused surface created its parent directory")
	}
}

// THE JAIL PATH: a refusal is a WARNING, not an A12-fatal boot failure. A corrupt
// agent-owned ~/.claude.json is something the agent itself can produce by crashing
// mid-write; if that stopped the jail from starting, the user could not launch the jail to
// fix the file inside it. The file is untouched and the reason is on stderr.
func TestJailRenderRMWRefusalWarnsRatherThanFailingBoot(t *testing.T) {
	var errw bytes.Buffer
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}, Stderr: &errw}
	withCtxRoot(t, t.TempDir(), "none")

	// claude/config is the jail's rmw surface, and ~/.claude.json is corrupt.
	broken := `{"projects": {`
	path := filepath.Join(e.Home, ".claude.json")
	if err := os.WriteFile(path, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatalf("a refused rmw surface must not fail the render: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != broken {
		t.Errorf("the corrupt file was rewritten:\nbefore %s\nafter  %s", broken, after)
	}
	report := errw.String()
	for _, want := range []string{"claude/config", "not valid JSON", "NOT modified"} {
		if !strings.Contains(report, want) {
			t.Errorf("the refusal warning must contain %q:\n%s", want, report)
		}
	}
}

// The jail's rmw surfaces still render normally — the refusal path must not have cost the
// ordinary case. (The byte-level version of this is TestRenderFingerprintStable; this is the
// behavioral one: a user key survives an rmw render, which is the mode's whole promise.)
func TestJailRenderRMWPreservesUserKeys(t *testing.T) {
	e := &Env{Home: t.TempDir(), Workspace: t.TempDir(), Vars: map[string]string{}}
	withCtxRoot(t, t.TempDir(), "none")
	path := filepath.Join(e.Home, ".claude.json")
	if err := os.WriteFile(path, []byte(`{"userOwnedKey":"keep me","numTokens":7}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ConfigurePackByName(e, "claude"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("not valid JSON (%v):\n%s", err, raw)
	}
	if got["userOwnedKey"] != "keep me" {
		t.Errorf("an rmw render dropped a user key:\n%s", raw)
	}
	// An integer must not be retyped on a JSON surface either.
	if !strings.Contains(string(raw), `"numTokens": 7`) {
		t.Errorf("integer 7 was retyped:\n%s", raw)
	}
}

// tomlHasComments must be quote-aware: a `#` inside a string is not a comment, and a
// spurious warning on every apply is how a warning stops being read.
func TestTOMLHasComments(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{"bare comment", "# hi\nk = 1\n", true},
		{"trailing comment", "k = 1 # hi\n", true},
		{"hash in a basic string", "url = \"https://x/#frag\"\n", false},
		{"hash in a literal string", "p = 'a#b'\n", false},
		{"hash in a multiline basic string", "s = \"\"\"a\n#b\n\"\"\"\n", false},
		{"hash in a multiline literal string", "s = '''a\n#b\n'''\n", false},
		{"escaped quote then comment", "k = \"a\\\"b\" # c\n", true},
		{"no comment at all", "k = 1\n\n[t]\nj = 2\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := tomlHasComments([]byte(tc.src)); got != tc.want {
			t.Errorf("%s: tomlHasComments(%q) = %v, want %v", tc.name, tc.src, got, tc.want)
		}
	}
}

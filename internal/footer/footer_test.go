package footer

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// env is a getenv over a fixed map, so a billing case states its whole environment.
func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// The tables as a launch emits them (packload.ProfilesWireTable's shape for YOLO_PROFILES,
// the composed table for YOLO_PROVIDERS), trimmed to what the billing rule reads.
const (
	profilesTable  = `{"bedrock": {"provider": "bedrock"}, "codex": {"provider": "openai-codex"}, "everything": {"provider": "bedrock"}, "zed": {"provider": "zai"}, "ghost": {"provider": "nowhere"}}`
	providersTable = `{"bedrock": {"region": "us-east-1"}, "openai-codex": {"endpoints": {"anthropic": {"base_url": "http://127.0.0.1:8215"}}}, "zai": {}}`
)

// claudeOpts mirrors the claude pack's command: its switches in Claude's order, Claude's
// truthiness, its words, and its bridged route.
func claudeOpts() Options {
	return ParseArgs([]string{
		"--agent", "claude", "--login", "Claude subscription",
		"--truthy", "1,true,yes,on",
		"--switch", "CLAUDE_CODE_USE_BEDROCK=bedrock",
		"--switch", "CLAUDE_CODE_USE_VERTEX=vertex",
		"--words", "bedrock=Bedrock",
		"--words", "openai-codex=ChatGPT subscription",
		"--words", "vertex=Vertex AI",
		"--bridged", "everything",
	})
}

// TestBillingRoute is §1.1's rule, one row per clause.
func TestBillingRoute(t *testing.T) {
	tables := func(use string, extra map[string]string) map[string]string {
		m := map[string]string{
			"YOLO_USE_PROFILES": use,
			"YOLO_PROFILES":     profilesTable,
			"YOLO_PROVIDERS":    providersTable,
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	cases := []struct {
		name string
		opts Options
		env  map[string]string
		want string
	}{
		{"1. a selected profile names its provider in the pack's words",
			claudeOpts(), tables(`{"claude": "bedrock"}`, nil), "Bedrock"},
		{"1. the agent's own switch agreeing with the profile adds nothing",
			claudeOpts(), tables(`{"claude": "bedrock"}`, map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}), "Bedrock"},
		{"1. a switch naming a different provider is appended, marked (env)",
			claudeOpts(), tables(`{"claude": "bedrock"}`, map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}), "Bedrock ≠ Vertex AI (env)"},
		{"1. a profile named unlike its provider follows the words",
			claudeOpts(), tables(`{"claude": "codex"}`, nil), "ChatGPT subscription (profile codex)"},
		{"1. a provider the pack has no words for shows its id",
			claudeOpts(), tables(`{"claude": "zed"}`, nil), "zai (profile zed)"},
		{"1. a bridged route shows the profile and (bridge), never an upstream",
			claudeOpts(), tables(`{"claude": "everything"}`, nil), "everything (bridge)"},
		{"1. a route is bridged by its provider id too",
			ParseArgs([]string{"--agent", "claude", "--bridged", "openai-codex"}), tables(`{"claude": "codex"}`, nil), "codex (bridge)"},
		{"1. a bridged route still says when the agent's switch disagrees",
			claudeOpts(), tables(`{"claude": "everything"}`, map[string]string{"CLAUDE_CODE_USE_VERTEX": "yes"}), "everything (bridge) ≠ Vertex AI (env)"},
		{"2. no profile, a switch on: that provider, marked (env)",
			claudeOpts(), tables(`{}`, map[string]string{"CLAUDE_CODE_USE_BEDROCK": "1"}), "Bedrock (env)"},
		{"2. the first switch that is on wins, in the pack's order",
			claudeOpts(), tables(`{}`, map[string]string{"CLAUDE_CODE_USE_VERTEX": "1", "CLAUDE_CODE_USE_BEDROCK": "true"}), "Bedrock (env)"},
		{"2. a switch is tested the agent's way: trimmed and case-insensitive",
			claudeOpts(), tables(`{}`, map[string]string{"CLAUDE_CODE_USE_BEDROCK": " ON "}), "Bedrock (env)"},
		{"2. a value outside the agent's truthy set is off",
			claudeOpts(), tables(`{}`, map[string]string{"CLAUDE_CODE_USE_BEDROCK": "0", "CLAUDE_CODE_USE_VERTEX": "enabled"}), "Claude subscription"},
		{"2. with no --truthy, any non-empty value is on",
			ParseArgs([]string{"--agent", "x", "--switch", "X_USE=bedrock", "--words", "bedrock=Bedrock"}), tables(`{}`, map[string]string{"X_USE": "0"}), "Bedrock (env)"},
		{"3. neither: the pack's words for the agent's login",
			claudeOpts(), tables(`{}`, nil), "Claude subscription"},
		{"3. a profile selected for ANOTHER agent is not this agent's",
			claudeOpts(), tables(`{"pi": "codex"}`, nil), "Claude subscription"},
		{"3. no --agent never reads a profile",
			ParseArgs([]string{"--login", "L"}), tables(`{"": "bedrock", "claude": "bedrock"}`, nil), "L"},
		{"3. no login words is nothing, never a guess",
			ParseArgs([]string{"--agent", "claude"}), tables(`{}`, nil), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Billing(c.opts, env(c.env)); got != c.want {
				t.Errorf("Billing = %q, want %q", got, c.want)
			}
		})
	}
}

// TestBillingDegenerateInputs is §1.2's list: absent or malformed tables read as empty, and
// a profile naming an unknown provider renders the profile name alone.
func TestBillingDegenerateInputs(t *testing.T) {
	o := claudeOpts()
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"every table absent", map[string]string{}, "Claude subscription"},
		{"malformed YOLO_USE_PROFILES", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": `, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}, "Claude subscription"},
		{"YOLO_USE_PROFILES not an object", map[string]string{
			"YOLO_USE_PROFILES": `["claude"]`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}, "Claude subscription"},
		{"a null selection is no selection", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": null}`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}, "Claude subscription"},
		{"a non-string selection is no selection", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": 7}`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}, "Claude subscription"},
		{"a malformed table still lets a switch speak", map[string]string{
			"YOLO_USE_PROFILES": `nope`, "CLAUDE_CODE_USE_BEDROCK": "1"}, "Bedrock (env)"},
		{"malformed YOLO_PROFILES: the profile name alone", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": "bedrock"}`, "YOLO_PROFILES": `{`, "YOLO_PROVIDERS": providersTable}, "bedrock"},
		{"malformed YOLO_PROVIDERS: the profile name alone", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": "bedrock"}`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": `[]`}, "bedrock"},
		{"a profile the table does not hold: the profile name alone", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": "mystery"}`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}, "mystery"},
		{"a profile naming an unknown provider: the profile name alone", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": "ghost"}`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}, "ghost"},
		{"an unknown provider with a switch on still says so", map[string]string{
			"YOLO_USE_PROFILES": `{"claude": "ghost"}`, "YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable,
			"CLAUDE_CODE_USE_BEDROCK": "1"}, "ghost ≠ Bedrock (env)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Billing(o, env(c.env)); got != c.want {
				t.Errorf("Billing = %q, want %q", got, c.want)
			}
		})
	}
}

// TestNotch is §1.2: the marker a container launch sets, asked through config.InJail. An
// EMPTY marker is the case the copies of the test disagree about, and it reads as host.
func TestNotch(t *testing.T) {
	t.Setenv("YOLO_VERSION", "0.10.0")
	if got := Notch(); got != NotchJail {
		t.Errorf("YOLO_VERSION set: Notch = %q, want %q", got, NotchJail)
	}
	t.Setenv("YOLO_VERSION", "")
	if got := Notch(); got != NotchHost {
		t.Errorf("YOLO_VERSION empty: Notch = %q, want %q", got, NotchHost)
	}
	os.Unsetenv("YOLO_VERSION") // restored by the t.Setenv cleanup above
	if got := Notch(); got != NotchHost {
		t.Errorf("YOLO_VERSION absent: Notch = %q, want %q", got, NotchHost)
	}
}

// TestNoCopyOfTheJailProbe fails if this package grows its own answer to "am I in a
// jail?". The rule is that the renderer asks config.InJail and keeps no copy of that test
// (§1.2), and a faithful copy would pass TestNotch — so what is pinned here is that the
// marker's NAME never appears in the renderer's source, which is the only way a copy can be
// written.
func TestNoCopyOfTheJailProbe(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := false
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, needle := range []string{"YOLO_VERSION", "LookupEnv"} {
			if bytes.Contains(src, []byte(needle)) {
				t.Errorf("%s mentions %s: the notch must come from config.InJail, not a second probe", f, needle)
			}
		}
		calls = calls || bytes.Contains(src, []byte("config.InJail()"))
	}
	if !calls {
		t.Error("no non-test file calls config.InJail(): the notch has lost its one source")
	}
}

// TestRenderTemplate covers the template rules of §2: yolo facts, stdin paths, a missing
// field rendering empty, and no literal {…} reaching the screen.
func TestRenderTemplate(t *testing.T) {
	t.Setenv("YOLO_VERSION", "x")
	e := env(map[string]string{"YOLO_USE_PROFILES": `{"claude": "bedrock"}`,
		"YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable})
	doc := map[string]any{"model": map[string]any{"display_name": "Opus", "ctx": []any{"a", "b"}}}
	stdin := func() any { return doc }
	cases := []struct{ tmpl, want string }{
		{"{stdin.model.display_name} · yolo: {yolo.billing} · {yolo.notch}", "Opus · yolo: Bedrock · jail"},
		{DefaultTemplate, "yolo: Bedrock · jail"},
		{"{ yolo.notch }", "jail"},
		{"[{stdin.model.missing}]", "[]"},
		{"[{stdin.model}]", "[]"},
		{"[{stdin.model.ctx}]", "[]"},
		{"{stdin.model.ctx.1}", "b"},
		{"[{stdin.model.ctx.9}]", "[]"},
		{"[{stdin.model.display_name.deeper}]", "[]"},
		{"[{yolo.version}]", "[]"},
		{"[{env.HOME}]", "[]"},
		{"a {b {yolo.notch}", "a b jail"},
		{"{{yolo.notch}}", "jail}"},
		{"tail {yolo.notch", "tail yolo.notch"},
		{"one\ntwo\r\nthree", "one two three"},
		{"  padded  ", "padded"},
		{"", ""},
	}
	for _, c := range cases {
		o := claudeOpts()
		o.Template = c.tmpl
		if got := Render(o, e, stdin); got != c.want {
			t.Errorf("Render(%q) = %q, want %q", c.tmpl, got, c.want)
		}
	}
}

// TestRenderOptionalPrefix pins the `{prefix|field}` form: the text before the last `|` is
// printed only when the field has a value, so an optional field never leaves its separator
// behind. It is how claude's footer puts the session's effort level next to the model.
func TestRenderOptionalPrefix(t *testing.T) {
	t.Setenv("YOLO_VERSION", "x")
	e := env(map[string]string{"YOLO_USE_PROFILES": `{"claude": "bedrock"}`,
		"YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable})
	withEffort := map[string]any{"model": map[string]any{"display_name": "Opus"}, "effort": map[string]any{"level": "high"}}
	noEffort := map[string]any{"model": map[string]any{"display_name": "Opus"}}
	const tmpl = "{stdin.model.display_name}{ · |stdin.effort.level} · yolo: {yolo.billing}"
	cases := []struct {
		name string
		tmpl string
		doc  any
		want string
	}{
		{"present", tmpl, withEffort, "Opus · high · yolo: Bedrock"},
		{"absent", tmpl, noEffort, "Opus · yolo: Bedrock"},
		{"no stdin at all", tmpl, nil, "· yolo: Bedrock"},
		{"the prefix keeps its own spaces", "[{  -  |stdin.effort.level}]", withEffort, "[  -  high]"},
		{"only the last bar splits", "[{a|b|stdin.effort.level}]", withEffort, "[a|bhigh]"},
		{"an empty prefix is a plain optional field", "[{|stdin.effort.level}]", withEffort, "[high]"},
		{"a yolo fact takes a prefix too", "[{at |yolo.notch}]", noEffort, "[at jail]"},
		{"an empty field name prints nothing", "[{x|}]", withEffort, "[]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			o := claudeOpts()
			o.Template = c.tmpl
			doc := c.doc
			if got := Render(o, e, func() any { return doc }); got != c.want {
				t.Errorf("Render(%q) = %q, want %q", c.tmpl, got, c.want)
			}
		})
	}
}

// TestRenderNeverShowsABraceField pins "a literal {…} never reaches the screen" over
// templates built to leave one behind.
func TestRenderNeverShowsABraceField(t *testing.T) {
	for _, tmpl := range []string{"{", "{}", "{{}}", "{a}{b}", "{yolo.billing", "x{y{z}}", "{stdin.}", "}{"} {
		o := claudeOpts()
		o.Template = tmpl
		got := Render(o, env(nil), func() any { return nil })
		if strings.Contains(got, "{") {
			t.Errorf("Render(%q) = %q, which still holds a {", tmpl, got)
		}
	}
}

// TestRenderReadsStdinOnlyWhenAsked keeps a template with no stdin field from touching the
// agent's pipe at all, and reads it once however many fields ask.
func TestRenderReadsStdinOnlyWhenAsked(t *testing.T) {
	calls := 0
	stdin := func() any { calls++; return map[string]any{"a": "1", "b": "2"} }
	o := ParseArgs([]string{"--template", "yolo: {yolo.notch}"})
	Render(o, env(nil), stdin)
	if calls != 0 {
		t.Errorf("a template with no stdin field read stdin %d times", calls)
	}
	o.Template = "{stdin.a}{stdin.b}"
	if got := Render(o, env(nil), stdin); got != "12" || calls != 1 {
		t.Errorf("Render = %q after %d reads, want \"12\" after 1", got, calls)
	}
}

// TestRenderScrubsValues: the agent's data can neither break the line nor carry a terminal
// escape through a field.
func TestRenderScrubsValues(t *testing.T) {
	doc := map[string]any{"name": "Op\x1b[31mus\nX\u0085Y\x7f", "bad": "a\xffb"}
	o := ParseArgs([]string{"--template", "{stdin.name}|{stdin.bad}"})
	got := Render(o, env(nil), func() any { return doc })
	if want := "Op [31mus X Y |ab"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestRenderNeverPrintsASwitchValue: a switch is compared and never printed, so a secret
// set in a switch's variable — or anywhere in the env — cannot reach the footer.
func TestRenderNeverPrintsASwitchValue(t *testing.T) {
	t.Setenv("YOLO_VERSION", "x")
	const secret = "sk-ant-api03-SECRET"
	o := ParseArgs([]string{"--agent", "claude", "--switch", "ANTHROPIC_API_KEY=anthropic-api",
		"--template", "{yolo.billing} {yolo.notch} {env.ANTHROPIC_API_KEY} {ANTHROPIC_API_KEY} {stdin.key}"})
	got := Render(o, env(map[string]string{"ANTHROPIC_API_KEY": secret}), func() any { return nil })
	if strings.Contains(got, "SECRET") {
		t.Fatalf("Render printed a credential value: %q", got)
	}
	if want := "anthropic-api (env) jail"; got != want {
		t.Errorf("Render = %q, want %q", got, want)
	}
}

// TestParseArgsTolerates: every flag takes one value, an unknown flag is skipped with its
// value, and nothing malformed is an error — the property that lets a command frozen into
// an edited-in-place file outlive the flags it was written with.
func TestParseArgsTolerates(t *testing.T) {
	o := ParseArgs([]string{
		"positional",
		"--from-the-future", "its value",
		"--agent=claude",
		"--switch", "NOEQUALS",
		"--switch", "=bedrock",
		"--switch", "V=",
		"--switch", "A=b=c",
		"--words", "noequals",
		"--words", "bedrock=Bed=rock",
		"--bridged", "",
		"--truthy", " ON, ,Yes ",
		"--template",
	})
	if o.Agent != "claude" {
		t.Errorf("Agent = %q, want claude (the --name=value form)", o.Agent)
	}
	if len(o.Switches) != 1 || o.Switches[0] != (Switch{Var: "A", Provider: "b=c"}) {
		t.Errorf("Switches = %#v, want only {A b=c}", o.Switches)
	}
	if len(o.Words) != 1 || o.Words["bedrock"] != "Bed=rock" {
		t.Errorf("Words = %#v, want only bedrock=Bed=rock", o.Words)
	}
	if len(o.Bridged) != 0 {
		t.Errorf("Bridged = %#v, want empty", o.Bridged)
	}
	if strings.Join(o.Truthy, ",") != "on,yes" {
		t.Errorf("Truthy = %#v, want [on yes]", o.Truthy)
	}
	if o.Template != DefaultTemplate {
		t.Errorf("a trailing --template with no value changed the template to %q", o.Template)
	}
}

// TestMainNeverFailsAndNeverWritesStderr: whatever the arguments and whatever is on
// stdin, exit 0 and silence on stderr (§1.2's degenerate inputs).
func TestMainNeverFailsAndNeverWritesStderr(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USE_PROFILES", "{broken")
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stderr
	os.Stderr = w
	var out bytes.Buffer
	codes := []int{
		Main(nil, nil, &out, nil),
		Main([]string{"--template"}, strings.NewReader("not json"), &out, nil),
		Main([]string{"--agent", "claude", "--login", "L", "--template", "{stdin.a} {yolo.billing} {yolo.notch}"},
			strings.NewReader(`{"a": 1.50}`), &out, nil),
		Main([]string{"--bogus", "--", "{", "}"}, strings.NewReader(strings.Repeat("x", stdinLimit+10)), &out, nil),
	}
	os.Stderr = saved
	w.Close()
	stderr, _ := io.ReadAll(r)
	for i, c := range codes {
		if c != 0 {
			t.Errorf("call %d exited %d, want 0", i, c)
		}
	}
	if len(stderr) != 0 {
		t.Errorf("wrote to stderr: %q", stderr)
	}
	if want := "yolo:  · host\nyolo:  · host\n1.50 L host\nyolo:  · host\n"; out.String() != want {
		t.Errorf("stdout = %q, want %q", out.String(), want)
	}
}

// TestReadJSON bounds the one read another process controls.
func TestReadJSON(t *testing.T) {
	if got := ReadJSON(strings.NewReader(`{"n": 1e3, "s": "x"}`)); got == nil {
		t.Error("a JSON object read as nil")
	} else if lookup(got, "n") != "1e3" {
		t.Errorf("a number rendered as %q, want it as written (1e3)", lookup(got, "n"))
	}
	for name, r := range map[string]io.Reader{
		"empty":     strings.NewReader(""),
		"malformed": strings.NewReader("{"),
		"oversize":  strings.NewReader(`"` + strings.Repeat("x", stdinLimit) + `"`),
	} {
		if got := ReadJSON(r); got != nil {
			t.Errorf("%s stdin read as %v, want nil", name, got)
		}
	}
	// A character device (a terminal, /dev/null) is never read: an interactive stdin would
	// block the footer on a human.
	if f, err := os.Open(os.DevNull); err == nil {
		if got := ReadJSON(f); got != nil {
			t.Errorf("%s read as %v, want nil", os.DevNull, got)
		}
		f.Close()
	}
	// A pipe that never closes costs the field, not the line: ReadJSON gives up.
	pr, pw := io.Pipe()
	defer pw.Close()
	start := time.Now()
	if got := ReadJSON(pr); got != nil {
		t.Errorf("an unclosed pipe read as %v, want nil", got)
	}
	if d := time.Since(start); d > 5*stdinTimeout {
		t.Errorf("an unclosed pipe held ReadJSON for %v", d)
	}
}

// TestHostTables is OQ-FT6's rule for the tables: at the host, env first, else the caller's
// composition from the user config; in a jail, never anything but the env.
func TestHostTables(t *testing.T) {
	selected := Tables{UseProfiles: `{"claude": "bedrock"}`, Profiles: profilesTable, Providers: providersTable}
	calls := 0
	host := func() Tables { calls++; return selected }
	billing := func(getenv func(string) string) string { return Billing(claudeOpts(), getenv) }

	t.Run("at the host with no selection in the env, the host tables answer", func(t *testing.T) {
		calls = 0
		t.Setenv("YOLO_VERSION", "")
		getenv := WithHostTables(env(map[string]string{}), host)
		if got := billing(getenv); got != "Bedrock" {
			t.Errorf("billing = %q, want Bedrock from the user config's selection", got)
		}
		_ = billing(getenv)
		if calls != 1 {
			t.Errorf("host composed %d times over two renders, want once", calls)
		}
	})
	t.Run("an env switch still disagrees with the config's selection", func(t *testing.T) {
		t.Setenv("YOLO_VERSION", "")
		getenv := WithHostTables(env(map[string]string{"CLAUDE_CODE_USE_VERTEX": "1"}), host)
		if got := billing(getenv); got != "Bedrock ≠ Vertex AI (env)" {
			t.Errorf("billing = %q, want the config's profile with the differing switch", got)
		}
	})
	t.Run("at the host, a selection in the env wins and nothing is composed", func(t *testing.T) {
		calls = 0
		t.Setenv("YOLO_VERSION", "")
		getenv := WithHostTables(env(map[string]string{"YOLO_USE_PROFILES": `{"claude": "codex"}`,
			"YOLO_PROFILES": profilesTable, "YOLO_PROVIDERS": providersTable}), host)
		if got := billing(getenv); got != "ChatGPT subscription (profile codex)" {
			t.Errorf("billing = %q, want the env's own selection", got)
		}
		if calls != 0 {
			t.Errorf("host composed %d times, want none: env first", calls)
		}
	})
	t.Run("in a jail nothing is composed, so no file is read", func(t *testing.T) {
		calls = 0
		t.Setenv("YOLO_VERSION", "0.10.0")
		if got := billing(WithHostTables(env(map[string]string{}), host)); got != "Claude subscription" {
			t.Errorf("billing = %q, want the login: a jail's env had no selection", got)
		}
		if calls != 0 {
			t.Errorf("host composed %d times in a jail, want none", calls)
		}
	})
	t.Run("a template that renders no billing composes nothing", func(t *testing.T) {
		calls = 0
		t.Setenv("YOLO_VERSION", "")
		o := claudeOpts()
		o.Template = "{yolo.notch}"
		if got := Render(o, WithHostTables(env(map[string]string{}), host), nil); got != "host" {
			t.Errorf("render = %q, want host", got)
		}
		if calls != 0 {
			t.Errorf("host composed %d times for a notch-only template, want none", calls)
		}
	})
	t.Run("a host composition that panics reads as no tables", func(t *testing.T) {
		t.Setenv("YOLO_VERSION", "")
		getenv := WithHostTables(env(map[string]string{}), func() Tables { panic("boom") })
		if got := billing(getenv); got != "Claude subscription" {
			t.Errorf("billing = %q, want the login", got)
		}
	})
	t.Run("a nil host is the env alone", func(t *testing.T) {
		t.Setenv("YOLO_VERSION", "")
		if got := billing(WithHostTables(env(map[string]string{}), nil)); got != "Claude subscription" {
			t.Errorf("billing = %q, want the login", got)
		}
	})
}

package run

// launchcontext_test.go pins the launch log's archaeology records (launchcontext.go): the
// invocation, the attach-or-fresh decision, each agent's profile and its source, the config and
// pack changes since the previous launch, and the captured edits the boot re-applies. The cells
// that drive Run are the call-site pins: each fails if its record's production call is deleted.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/agentcfg/manifest"
	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/hostfloor/floortest"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/macosuser"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	yoloruntime "github.com/mschulkind-oss/yolo-jail/internal/runtime"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// A value whose NAME says it may be a secret is redacted from the command; everything else is
// kept as typed.
func TestRedactCommandArgsHidesOnlyWhatANameMarksSecret(t *testing.T) {
	got := RedactCommandArgs([]string{"env", "ANTHROPIC_API_KEY=sk-1", "AWS_PROFILE=dev", "claude",
		"--token", "sk-2", "--api-key=sk-3", "--resume", "-p", "hello"})
	want := []string{"env", "ANTHROPIC_API_KEY=<redacted>", "AWS_PROFILE=dev", "claude",
		"--token", "<redacted>", "--api-key=<redacted>", "--resume", "-p", "hello"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("RedactCommandArgs =\n  %q\nwant\n  %q", got, want)
	}
}

// yolo's own tokens are kept whole (a provider list names providers, not secrets); the command
// is split off whether or not a `--` came before it.
func TestFormatInvocationSplitsYolosTokensFromTheCommand(t *testing.T) {
	for _, c := range []struct {
		argv, cmd []string
		want      string
	}{
		{[]string{"-p", "bedrock", "--with-credentials", "bedrock", "--", "claude", "--token", "x"},
			[]string{"claude", "--token", "x"},
			"yolo -p bedrock --with-credentials bedrock -- claude --token '<redacted>'"},
		{[]string{"run", "claude", "--resume"}, []string{"claude", "--resume"}, "yolo run claude --resume"},
		{[]string{}, nil, "yolo"},
		{nil, nil, "(none typed: a launch yolo started itself)"},
	} {
		if got := formatInvocation(c.argv, c.cmd); got != c.want {
			t.Errorf("formatInvocation(%q, %q) = %q, want %q", c.argv, c.cmd, got, c.want)
		}
	}
}

// The machine-wide host log keeps the program's base name and argument count, never the
// arguments (OQ-PR3).
func TestFormatHostInvocationKeepsNoArgument(t *testing.T) {
	got := formatHostInvocation([]string{"host", "-p", "bedrock", "--", "/opt/bin/claude", "secret-prompt"})
	if want := "yolo host -p bedrock -- claude <1 argument>"; got != want {
		t.Errorf("formatHostInvocation = %q, want %q", got, want)
	}
}

// Each CLI's entry is named with its source as the fold decided it: the key's named entry, the
// key's "*", a bare -p, a -p pair, and nothing.
func TestProfileRecordNamesEachEntrysSource(t *testing.T) {
	recv := config.ProfileReceivers{Bins: []string{"claude", "pi", "codex", "acme"}}
	key := config.ProfileSelection{Default: []string{"bedrock"}, Named: map[string][]string{"claude": nil}}
	o := &Options{}
	keyOnly := config.FoldProfiles(recv, key, o.ProfileFlags())
	got := ProfileRecord(keyOnly, recv.Bins, key, o.jailProfileFlag)
	want := `claude=none (config profile.claude), pi=bedrock (config profile."*"), ` +
		`codex=bedrock (config profile."*"), acme=bedrock (config profile."*")`
	if got != want {
		t.Errorf("key alone:\n got %s\nwant %s", got, want)
	}

	o = &Options{ProfileName: "bedrock", UseProfiles: map[string]string{"codex": "codex"}}
	flagged := config.FoldProfiles(recv, key, o.ProfileFlags())
	got = ProfileRecord(flagged, recv.Bins, key, o.jailProfileFlag)
	want = `claude=bedrock (-p bedrock), pi=bedrock (-p bedrock), codex=codex (-p codex=codex), ` +
		`acme=bedrock (-p bedrock)`
	if got != want {
		t.Errorf("with -p:\n got %s\nwant %s", got, want)
	}

	none := config.FoldProfiles(recv, config.ProfileSelection{}, config.ProfileSelection{})
	if got := ProfileRecord(none, []string{"claude"}, config.ProfileSelection{}, o.jailProfileFlag); got != "claude=none (nothing selects one)" {
		t.Errorf("no selection: %s", got)
	}
}

// A diff line whose key's name says its value may be a credential loses the value.
func TestRedactDiffLineHidesASensitiveKeysValue(t *testing.T) {
	for in, want := range map[string]string{
		`+        "TAVILY_API_KEY": "tvly-123",`: `+        "TAVILY_API_KEY": "<redacted>",`,
		`-  "apiKeyHelper": "/bin/key"`:          `-  "apiKeyHelper": "<redacted>"`,
		`   "theme": "dark",`:                    `   "theme": "dark",`,
		`+  "env": {`:                            `+  "env": {`,
		`+++ this launch`:                        `+++ this launch`,
	} {
		if got := redactDiffLine(in); got != want {
			t.Errorf("redactDiffLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A captured edit is named by its key path; only a boolean or a deletion shows its value.
func TestCapturedEditRecordsNameKeysNotValues(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("claude-settings.overlay.json", `{"ultracode": true, "apiKeyHelper": "/bin/sk-secret", "theme": null, "permissions": {"ask": ["Bash(rm:*)"]}}`)
	write("pi-settings.overlay.json", `{}`)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	surfaces := []manifest.Surface{{Agent: "claude", Name: "settings", Path: "~/.claude/settings.json"},
		{Agent: "pi", Name: "settings", Path: "~/.pi/agent/settings.json"}}
	got := capturedEditRecords(root, surfaces, "re-applied on every boot",
		func(slug string) string { return "yolo config reset " + slug })
	if len(got) != 1 {
		t.Fatalf("records = %q, want one (pi's overlay is empty)", got)
	}
	want := "captured edits: claude/settings (~/.claude/settings.json), re-applied on every boot: " +
		"ultracode=true, apiKeyHelper (string), theme (removed), permissions.ask (list); " +
		"`yolo config reset claude/settings` discards them"
	if got[0] != want {
		t.Errorf("record:\n got %s\nwant %s", got[0], want)
	}
	if strings.Contains(got[0], "sk-secret") {
		t.Errorf("a captured string value reached the record: %s", got[0])
	}
	if got := capturedEditRecords(nil, surfaces, "x", nil); len(got) != 1 || !strings.Contains(got[0], "none") {
		t.Errorf("no capture store: %q", got)
	}
}

// THE CALL-SITE PIN for the invocation, profile, config, packs and captured-edit records: two
// real Runs of one workspace. The first has no previous launch; the second was given -p and a
// changed user config, and both re-apply a captured edit.
func TestTheLaunchLogRecordsTheLaunchsArchaeology(t *testing.T) {
	home := packHome(t)
	profileLaunchLocalPack(t, home)
	userCfg := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	writeCfg := func(body string) {
		if err := os.WriteFile(userCfg, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeCfg(`{"profile": {"*": "bedrock", "acme": null}}`)
	// RESOLVED like the home packHome mints: the record lists config files sorted by path, so a
	// workspace under a symlinked TMPDIR (darwin's /var) would sort apart from the home.
	ws := floortest.ResolvedTemp(t)
	// The capture store beside the home this launch binds (named-setups-plan.md SUP-D35): a fresh
	// workspace's setup home, so the record reads that setup's store, not the legacy .yolo/prism.
	prism := filepath.Join(filepath.Dir(HomeFor(ws, setup.MustBuiltIn(testSetupForRuntime("macos-user")))), "prism")
	if err := os.MkdirAll(prism, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(prism, "mise-config.overlay.json"),
		[]byte(`{"settings": {"experimental": true}, "env": {"API_TOKEN": "sk-secret-value"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	launch := func(mutate func(*Options)) string {
		var stdout, stderr bytes.Buffer
		o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
		o.Args = []string{"acme"}
		o.MacosUserRun = func(*jsonx.OrderedMap, string, []string, []string, string, string, macosuser.HomeOverlay, macosuser.HostContext, bool, *jsonx.OrderedMap, []packload.BlockedTool, macosuser.JailDaemons) int {
			return 0
		}
		mutate(o)
		if rc := Run(*o); rc != 0 {
			t.Fatalf("Run() = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout.String(), stderr.String())
		}
		if strings.Contains(stdout.String()+stderr.String(), "profile: acme=") {
			t.Errorf("a record reached the terminal:\n%s%s", stdout.String(), stderr.String())
		}
		return readLaunchLog(t, ws)
	}

	first := launch(func(o *Options) { o.Invocation = []string{"--", "acme"} })
	for _, want := range []string{
		"  invocation: yolo -- acme\n",
		"  jail: " + yoloruntime.FromWorkspace(ws) + "  runtime=macos-user",
		"  profile: acme=none (config profile.acme)",
		"  config: no previous launch recorded\n",
		"  config files: " + userCfg + " (modified ",
		"  packs: ",
		"(no previous launch recorded)\n",
		"  captured edits: mise/config",
		"settings.experimental=true, env.API_TOKEN (string)",
		"`yolo config reset mise/config` discards them",
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the first launch's log lacks %q:\n%s", want, first)
		}
	}
	if strings.Contains(first, "sk-secret-value") {
		t.Errorf("a captured value reached the log:\n%s", first)
	}
	for _, f := range []string{LastLaunchConfigName, LastLaunchFactsName} {
		if _, err := os.Stat(filepath.Join(ws, ".yolo", f)); err != nil {
			t.Errorf("the launch left no %s for the next one: %v", f, err)
		}
	}

	time.Sleep(10 * time.Millisecond) // a distinct mtime for the edited config file
	writeCfg(`{"profile": {"*": "bedrock", "acme": null}, "perf_logging": false}`)
	second := launch(func(o *Options) {
		o.Invocation = []string{"-p", "bedrock", "--", "acme"}
		o.ProfileName = "bedrock"
	})
	block := second[strings.LastIndex(second, launchRunPrefix):]
	for _, want := range []string{
		"  invocation: yolo -p bedrock -- acme\n",
		"  profile: acme=bedrock (-p bedrock)",
		"  config: effective config changed since the previous launch (",
		`  +  "perf_logging": false`,
		" CHANGED, was (",
		"  packs: unchanged since the previous launch (",
		"  captured edits: mise/config",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("the second launch's block lacks %q:\n%s", want, block)
		}
	}
}

// THE CALL-SITE PIN for the attach decision: an attach through Run records it.
func TestAnAttachRecordsItsDecisionInTheLaunchLog(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	attachThroughRun(t, ws, nil)
	want := "  decision: attach to the running jail " + yoloruntime.FromWorkspace(ws) + "\n"
	if got := readLaunchLog(t, ws); !strings.Contains(got, want) {
		t.Errorf("the launch log lacks %q:\n%s", want, got)
	}
}

// THE CALL-SITE PIN for the fresh decision: a container launch that finds no running jail
// records it before anything else of the fresh path, the config approval included.
func TestAFreshContainerLaunchRecordsItsDecisionInTheLaunchLog(t *testing.T) {
	home := packHome(t)
	writeUserPacks(t, home, `["claude"]`)
	ws := t.TempDir()
	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "podman", &stdout, &stderr, nil)
	o.Exec = func(argv []string, _ string, _ []string, _ time.Duration) ExecResult {
		return ExecResult{Ran: true, RC: 0}
	}
	_ = Run(*o)
	got := readLaunchLog(t, ws)
	want := "  decision: fresh launch of " + yoloruntime.FromWorkspace(ws) + " (no running jail to attach to)\n"
	if !strings.Contains(got, want) {
		t.Errorf("the launch log lacks %q:\nstdout:\n%s\nstderr:\n%s\nlog:\n%s", want, stdout.String(), stderr.String(), got)
	}
	if strings.Contains(got, "decision: attach") {
		t.Errorf("a fresh launch recorded an attach:\n%s", got)
	}
}

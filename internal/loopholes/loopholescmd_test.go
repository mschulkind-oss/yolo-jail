package loopholes

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

// TestSetEnabledGlobalPrintsTheConfigKeyAndWritesNothing: `enable` and `disable` are retired
// (docs/reference/loophole-grants.md LG-D8), `--global` included: for a loophole declared
// in config (no pack ships it) the refusal says so and names the file that declares it, writes
// nothing, and exits 1.
func TestSetEnabledGlobalPrintsTheConfigKeyAndWritesNothing(t *testing.T) {
	isolateDirs(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct {
		enabled bool
		verb    string
		value   string
	}{{true, "enable", "true"}, {false, "disable", "false"}} {
		var out, errBuf bytes.Buffer
		// myhole is installed (inline, in the user config): `--global` refuses a name nothing
		// installs, as the per-workspace form does.
		deps := Deps{Out: &out, Err: &errBuf, Cwd: home, LoadUserConfig: func() *jsonx.OrderedMap {
			m, err := json5.Decode([]byte(`{"loopholes": {"myhole": {"command": ["/bin/true"]}}}`))
			if err != nil {
				t.Fatal(err)
			}
			return m.(*jsonx.OrderedMap)
		}}
		rc := CmdSetEnabled(deps, "myhole", tc.enabled, SetEnabledOptions{Workspace: home, Global: true})
		if rc != 1 {
			t.Errorf("%s rc = %d, want 1 — the command did not do what was asked", tc.verb, rc)
		}
		if entries, _ := os.ReadDir(filepath.Join(home, ".config", "yolo-jail")); len(entries) != 0 {
			t.Errorf("%s --global wrote %v", tc.verb, entries)
		}
		got := errBuf.String()
		for _, want := range []string{"yolo loopholes " + tc.verb + " is retired", "myhole", "declared in config", "loopholes.myhole"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: message does not mention %q:\n%s", tc.verb, want, got)
			}
		}
		// The instruction must point at the USER config
		// (docs/reference/loophole-system.md#selection-and-discovery, the
		// `yolo loopholes enable|disable` paragraph). It used to direct people
		// at the weaker, agent-editable workspace scope; the workspace may still be
		// MENTIONED, but only as the weaker option.
		if out.Len() != 0 {
			t.Errorf("%s: wrote %q to stdout — a refusal belongs on stderr", tc.verb, out.String())
		}
		// OQ-A9's trap, and the single most likely way to get the rename wrong. The
		// MANIFEST's `enabled` became `default_enabled`; the CONFIG's `loopholes.<name>.
		// enabled` did NOT, because they were never the same key — one is the pack
		// author's default, the other is the user's answer. A well-meaning sweep that
		// renamed both would leave this command instructing users to write a key
		// internal/config does not validate and discovery never reads, so the
		// instruction would be silently inert.
		if strings.Contains(got, "default_enabled") {
			t.Errorf("%s: the command offers the MANIFEST key `default_enabled`; the user's "+
				"switch is the CONFIG key `loopholes.%s.enabled` and the rename did not touch "+
				"it:\n%s", tc.verb, "myhole", got)
		}
	}
}

// TestSetEnabledNeverWritesAManifest is the other half of the same ruling: whatever
// `yolo loopholes enable` grows into, it must not reach for a manifest file.
//
// The command's ORIGINAL mechanism was exactly that — rewrite `enabled` in a
// hand-placed manifest — and OQ-A9 rules the fix is to write CONFIG instead. The
// mechanism is already gone (SetEnabled was deleted with the directory it served,
// OQ-LP10), so what this pins is that it stays gone: a manifest.jsonc sitting exactly
// where the retired loader used to look is untouched by both verbs.
//
// Pinned by the FILE's bytes rather than by asserting the absence of a function,
// because the property is about effect, not about which symbol performs it.
func TestSetEnabledNeverWritesAManifest(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Its own temp dir, never TestMain's known-absent path: populating that one left a
	// /tmp/yolo-retired-absent* dir behind on every `go test` run, since TestMain exits
	// without cleanup.
	retired := t.TempDir()
	t.Cleanup(withRetiredDir(retired))
	mod := filepath.Join(retired, "myhole")
	must(t, os.MkdirAll(mod, 0o755))
	manifestPath := filepath.Join(mod, loopholedecl.ManifestName)
	const body = `{"name": "myhole", "transport": "none", "default_enabled": false}`
	must(t, os.WriteFile(manifestPath, []byte(body), 0o644))

	for _, enabled := range []bool{true, false} {
		var out, errBuf bytes.Buffer
		for _, global := range []bool{false, true} {
			CmdSetEnabled(Deps{Out: &out, Err: &errBuf, Cwd: home}, "myhole", enabled,
				SetEnabledOptions{Workspace: home, Global: global})
		}
	}
	after, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Errorf("the manifest was rewritten:\n before: %s\n  after: %s\n"+
			"Enable/disable state belongs in config for EVERY source — a manifest is the "+
			"binary's content (bundled) or the pack's (pack-shipped), and after OQ-A9 it "+
			"carries the AUTHOR's default rather than the user's answer at all.",
			body, after)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// isolateDirs clears the process-wide pack-module record, so no real manifest (and no
// real doctor_cmd) can leak into a test.
//
// It used to point BUNDLED discovery at a throwaway dir. With that channel retired
// (docs/reference/loophole-system.md OQ-BP4) the leak this guards against comes from the
// recorded pack modules — including the LAZY RESOLVER, which reads this machine's real
// user config — so the record is what has to be emptied.
func isolateDirs(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { ResetPackModules() })
	SetPackModuleResolver(nil)
	ResetPackModules()
}

// cmdDeps builds Deps whose config loaders return the given JSONC bodies
// ("" => no config).
func cmdDeps(t *testing.T, out, errBuf *bytes.Buffer, userJSON, wsJSON string) Deps {
	t.Helper()
	parse := func(s string) *jsonx.OrderedMap {
		if s == "" {
			return nil
		}
		v, err := json5.Decode([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		m, ok := v.(*jsonx.OrderedMap)
		if !ok {
			t.Fatalf("not an object: %s", s)
		}
		return m
	}
	return Deps{
		Out:                 out,
		Err:                 errBuf,
		Cwd:                 t.TempDir(),
		LoadUserConfig:      func() *jsonx.OrderedMap { return parse(userJSON) },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return parse(wsJSON) },
	}
}

func TestStatusDoctorsUseCorrectedUserSettingsInTextAndJSON(t *testing.T) {
	isolateDirs(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	stateRoot := t.TempDir()
	previousStateDir := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	t.Cleanup(func() { StateDirFor = previousStateDir })

	const name = "status-settings-fixture"
	const corrected = "svet-corrected-profile"
	daemonStarted := filepath.Join(t.TempDir(), "daemon-started")
	moduleRoot := t.TempDir()
	manifest := map[string]any{
		"name": name, "description": "status current settings fixture", "transport": "loopback-tls",
		"settings": map[string]any{"value": map[string]any{"type": "string", "scope": "user", "default": "manifest-default"}},
		"host_daemon": map[string]any{
			"cmd": []string{"/bin/sh", "-c", `touch ` + shquote.Quote(daemonStarted), "daemon", "--socket", "{socket}", "--settings", "{settings}"},
			"settings_check": []string{"/bin/sh", "-c",
				`if grep -q ` + corrected + ` "$1"; then exit 0; fi; printf '%s' '{"reason":"missing current profile","remedy":"set the user-scope profile"}'; exit 9`,
				"validator", "{settings}"},
			"publishes": "socket",
		},
		"doctor_cmd": []string{"/bin/sh", "-c",
			`if grep -q ` + corrected + ` "$1"; then echo 'OK: doctor used current profile'; else echo 'FAIL: doctor saw stale profile'; exit 1; fi`,
			"doctor", "{settings}"},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(moduleRoot, name)
	if err := os.MkdirAll(module, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	SetPackModules([]PackModule{{Dir: module, HostExecApproved: true}})

	stable := SettingsFileFor(name)
	if err := os.MkdirAll(filepath.Dir(stable), 0o700); err != nil {
		t.Fatal(err)
	}
	stableBytes := []byte(`{"value":"last-launch-profile"}` + "\n")
	if err := os.WriteFile(stable, stableBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	userJSON := `{"loopholes":{"` + name + `":{"settings":{"value":"invalid-current-profile"}}}}`
	parse := func() *jsonx.OrderedMap {
		m, err := json5.Decode([]byte(userJSON))
		if err != nil {
			t.Fatal(err)
		}
		return m.(*jsonx.OrderedMap)
	}
	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: t.TempDir(),
		LoadUserConfig: parse, LoadWorkspaceConfig: func(string) *jsonx.OrderedMap { return nil }}
	if Status(deps) != 0 || !strings.Contains(out.String(), "missing current profile") ||
		!strings.Contains(out.String(), "set the user-scope profile") || strings.Contains(out.String(), "doctor used current profile") {
		t.Fatalf("invalid current user settings were not honestly reported before the doctor: %s", out.String())
	}
	out.Reset()
	deps.Format = "json"
	if Status(deps) != 0 {
		t.Fatalf("invalid-settings status JSON returned nonzero: %s", errBuf.String())
	}
	var refused struct {
		Entries []struct {
			State  string `json:"state"`
			Output string `json:"output"`
		} `json:"loopholes"`
	}
	if err := json.Unmarshal(out.Bytes(), &refused); err != nil || len(refused.Entries) != 1 || refused.Entries[0].State != "fail" ||
		!strings.Contains(refused.Entries[0].Output, "missing current profile") || strings.Contains(refused.Entries[0].Output, "doctor used current profile") {
		t.Fatalf("invalid current settings were not reported consistently in JSON: err=%v report=%+v output=%s", err, refused.Entries, out.String())
	}
	userJSON = `{"loopholes":{"` + name + `":{"settings":{"value":"` + corrected + `"}}}}`

	for _, format := range []string{"text", "json"} {
		out.Reset()
		errBuf.Reset()
		deps.Format = format
		if Status(deps) != 0 {
			t.Fatalf("status %s returned nonzero: %s", format, errBuf.String())
		}
		if format == "text" {
			if !strings.Contains(out.String(), "doctor used current profile") || strings.Contains(out.String(), "doctor saw stale profile") {
				t.Errorf("text status did not pass corrected current settings to the doctor: %s", out.String())
			}
		} else {
			var report struct {
				Entries []struct {
					State  string `json:"state"`
					Output string `json:"output"`
				} `json:"loopholes"`
			}
			if err := json.Unmarshal(out.Bytes(), &report); err != nil {
				t.Fatalf("decode status JSON: %v\n%s", err, out.String())
			}
			if len(report.Entries) != 1 || report.Entries[0].State != "ok" || !strings.Contains(report.Entries[0].Output, "doctor used current profile") {
				t.Errorf("JSON status did not report the doctor against corrected current settings: %+v", report.Entries)
			}
		}
	}
	got, err := os.ReadFile(stable)
	if err != nil || string(got) != string(stableBytes) {
		t.Fatalf("status changed persistent last-launch settings: got=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(stable))
	if err != nil || len(entries) != 1 || entries[0].Name() != SettingsFileName {
		t.Fatalf("status settings snapshot was not cleaned up: entries=%v err=%v", entries, err)
	}
	if _, err := os.Stat(daemonStarted); !os.IsNotExist(err) {
		t.Fatalf("read-only status started the host daemon: %v", err)
	}
}

// Regression for the evil-doctor case. The finding is in no doc — it did not
// graduate into docs/reference/loophole-system.md; it is "The finding" in
// `git show 9190a4d1^:docs/design/loophole-packaging.md`, and this test is what
// keeps it closed:
// the loophole commands read config with NO schema pass, so a workspace entry
// carrying only description+doctor_cmd — which `yolo check` REJECTS (no
// command, and doctor_cmd is workspace-illegal) — was still honored by
// `yolo loopholes list`, and Status would have executed its doctor_cmd on the
// host. An entry that fails validation must be dropped with a printed reason
// and never reach RunDoctorChecks.
func TestEvilDoctorWorkspaceEntryIsRefused(t *testing.T) {
	isolateDirs(t)
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf, "",
		`{"loopholes": {"evil-doctor": {"description": "helpful", "doctor_cmd": ["/tmp/evil", "--own"]}}}`)

	got := loopholesWithConfig(deps, true).All()
	for _, lp := range got {
		if lp.Name == "evil-doctor" {
			t.Fatalf("an entry yolo check rejects was honored: %+v", lp)
		}
	}
	reason := errBuf.String()
	if !strings.Contains(reason, "evil-doctor") || !strings.Contains(reason, "command") {
		t.Errorf("refusal must print the entry and the reason, got %q", reason)
	}

	// And List does not show it either.
	errBuf.Reset()
	if rc := List(deps); rc != 0 {
		t.Fatalf("List rc = %d", rc)
	}
	if strings.Contains(out.String(), "evil-doctor") {
		t.Errorf("List output still shows the refused entry: %q", out.String())
	}
}

// A workspace INLINE entry (command) is refused host-side by the two-verb rule
// (docs/reference/loophole-system.md#the-two-verbs): install is user-scope only — and
// kept in-jail, where the same violation is only a warning (the launch path honors it
// there too).
func TestWorkspaceInlineEntryRefusedOnHostKeptInJail(t *testing.T) {
	isolateDirs(t)
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf, "",
		`{"loopholes": {"wsd": {"command": ["/bin/true"]}}}`)

	got := loopholesWithConfig(deps, true).All()
	for _, lp := range got {
		if lp.Name == "wsd" {
			t.Fatalf("a workspace-installed daemon was honored host-side: %+v", lp)
		}
	}
	if !strings.Contains(errBuf.String(), "user-scope only") {
		t.Errorf("refusal reason = %q, want the scope ruling named", errBuf.String())
	}

	deps.InJail = true
	errBuf.Reset()
	got = loopholesWithConfig(deps, true).All()
	found := false
	for _, lp := range got {
		if lp.Name == "wsd" {
			found = true
		}
	}
	if !found {
		t.Errorf("in-jail the entry must stay honored (the violation is a warning there)")
	}
}

// Control: a USER-config inline entry is the legal install and stays listed.
func TestUserInlineEntryStillListed(t *testing.T) {
	isolateDirs(t)
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf,
		`{"loopholes": {"mine": {"description": "ok", "command": ["/bin/true"], "doctor_cmd": ["/bin/true"]}}}`, "")

	got := loopholesWithConfig(deps, true).All()
	found := false
	for _, lp := range got {
		if lp.Name == "mine" && lp.Source == SourceConfig {
			found = true
		}
	}
	if !found || errBuf.String() != "" {
		t.Errorf("user install must be honored silently; found=%v err=%q", found, errBuf.String())
	}
}

// The refusal must name the file the entry is actually in. deps.LoadWorkspaceConfig
// collapses yolo-jail.jsonc and yolo-jail.local.jsonc, so the command group used to
// blame the tracked file for a violation living only in the local one — sending a
// human to edit a file that does not contain the entry.
func TestWorkspaceRefusalNamesTheLocalFileWhenThatIsWhereTheEntryIs(t *testing.T) {
	isolateDirs(t)
	ws := t.TempDir()
	must(t, os.WriteFile(filepath.Join(ws, config.WorkspaceLocalConfigName),
		[]byte(`{"loopholes": {"wsd": {"command": ["/bin/true"]}}}`), 0o644))

	var out, errBuf bytes.Buffer
	deps := Deps{
		Out: &out, Err: &errBuf, Cwd: ws,
		LoadUserConfig: func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(cwd string) *jsonx.OrderedMap {
			m, err := config.LoadWorkspaceConfig(cwd, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			return m
		},
	}

	loopholesWithConfig(deps, true)
	reason := errBuf.String()
	if !strings.Contains(reason, config.WorkspaceLocalConfigName) {
		t.Errorf("refusal %q does not name %s, which is where the entry is",
			reason, config.WorkspaceLocalConfigName)
	}
	if strings.Contains(reason, filepath.Join(ws, config.WorkspaceConfigName)) {
		t.Errorf("refusal %q blames %s, which does not exist here",
			reason, config.WorkspaceConfigName)
	}
}

// With no file naming the entry, the refusal falls back to the workspace config — the one the
// loader reads (config.ResolveWorkspaceConfigPath), so `yolo-jail.json` where that is the file
// rather than a `yolo-jail.jsonc` the workspace does not have.
func TestWorkspaceRefusalFallsBackToTheConfigFileTheLoaderReads(t *testing.T) {
	isolateDirs(t)
	ws := t.TempDir()
	must(t, os.WriteFile(filepath.Join(ws, "yolo-jail.json"), []byte(`{}`), 0o644))

	var out, errBuf bytes.Buffer
	deps := Deps{
		Out: &out, Err: &errBuf, Cwd: ws,
		LoadUserConfig: func() *jsonx.OrderedMap { return nil },
		LoadWorkspaceConfig: func(string) *jsonx.OrderedMap {
			m, err := jsonx.Decode([]byte(`{"loopholes": {"wsd": {"command": ["/bin/true"]}}}`))
			if err != nil {
				t.Fatal(err)
			}
			return m.(*jsonx.OrderedMap)
		},
	}

	loopholesWithConfig(deps, true)
	reason := errBuf.String()
	if want := "(from " + filepath.Join(ws, "yolo-jail.json") + ")"; !strings.Contains(reason, want) {
		t.Errorf("refusal %q does not name the workspace's yolo-jail.json (want %q)", reason, want)
	}
}

// TestSetEnabledGlobalNamesTheLiveUserConfigFile: the file `--global` names is the one the
// reader reads (TOML when present or when none exists, JSONC only when that is the live file),
// and the instruction is a verb that works on either format, never a JSONC snippet a TOML
// config refuses.
func TestSetEnabledGlobalNamesTheLiveUserConfigFile(t *testing.T) {
	for _, tc := range []struct {
		name, existing, want, notWant string
	}{
		{"toml only", "config.toml", "config.toml", "config.jsonc"},
		{"jsonc only", "config.jsonc", "config.jsonc", "config.toml"},
		{"neither", "", "config.toml", "config.jsonc"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateDirs(t)
			home := t.TempDir()
			t.Setenv("HOME", home)
			dir := filepath.Join(home, ".config", "yolo-jail")
			if tc.existing != "" {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, tc.existing), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			var out, errBuf bytes.Buffer
			deps := Deps{Out: &out, Err: &errBuf, Cwd: home, LoadUserConfig: func() *jsonx.OrderedMap {
				m, err := json5.Decode([]byte(`{"loopholes": {"myhole": {"command": ["/bin/true"]}}}`))
				if err != nil {
					t.Fatal(err)
				}
				return m.(*jsonx.OrderedMap)
			}}
			if rc := CmdSetEnabled(deps, "myhole", true, SetEnabledOptions{Workspace: home, Global: true}); rc != 1 {
				t.Fatalf("rc = %d, want 1", rc)
			}
			got := errBuf.String()
			if strings.Contains(got, `"loopholes": {`) || strings.Contains(got, "yolo config set") {
				t.Errorf("prints a snippet or verb a %s home refuses:\n%s", tc.name, got)
			}
			if !strings.Contains(got, "is retired") || !strings.Contains(got, tc.want) || strings.Contains(got, tc.notWant) {
				t.Errorf("does not name the live user config %s (and not %s):\n%s", tc.want, tc.notWant, got)
			}
		})
	}
}

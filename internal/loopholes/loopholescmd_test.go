package loopholes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// TestSetEnabledRefusesAndNamesTheConfigKey: after OQ-LP10 the command has no manifest
// to write (the hand-placed dir it served is retired), and the config-write rework is a
// separate change. What it must NOT do is silently succeed or silently no-op — it exits
// non-zero and prints the exact key, the exact file, and the exact value.
func TestSetEnabledRefusesAndNamesTheConfigKey(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, tc := range []struct {
		enabled bool
		verb    string
		value   string
	}{{true, "enable", "true"}, {false, "disable", "false"}} {
		var out, errBuf bytes.Buffer
		deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
		rc := CmdSetEnabled(deps, "myhole", tc.enabled)
		if rc != 1 {
			t.Errorf("%s rc = %d, want 1 — the command did not do what was asked", tc.verb, rc)
		}
		got := errBuf.String()
		for _, want := range []string{tc.verb, "loopholes", "myhole", "enabled", tc.value, "config.jsonc"} {
			if !strings.Contains(got, want) {
				t.Errorf("%s: message does not mention %q:\n%s", tc.verb, want, got)
			}
		}
		// §5.2: the instruction must point at the USER config. It used to direct people
		// at the weaker, agent-editable workspace scope; the workspace may still be
		// MENTIONED, but only as the weaker option.
		if !strings.Contains(got, filepath.Join(".config", "yolo-jail", "config.jsonc")) {
			t.Errorf("%s: the USER config is not named as the place to write it:\n%s", tc.verb, got)
		}
		if out.Len() != 0 {
			t.Errorf("%s: wrote %q to stdout — a refusal belongs on stderr", tc.verb, out.String())
		}
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// isolateDirs points bundled loophole discovery at a throwaway dir, so no real
// manifest (and no real doctor_cmd) can leak into a test.
func isolateDirs(t *testing.T) {
	t.Helper()
	t.Cleanup(withBundledDir(t.TempDir()))
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

// Regression for loophole-packaging.md §4.1 finding 2 (the evil-doctor case):
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

// A workspace INLINE entry (command) is refused host-side per §4.3b — install
// is user-scope only — and kept in-jail, where the same violation is only a
// warning (the launch path honors it there too).
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

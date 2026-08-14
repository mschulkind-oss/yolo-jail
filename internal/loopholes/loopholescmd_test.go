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

func TestSetEnabledMissingUserLoophole(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
	rc := CmdSetEnabled(deps, "nonexistent", true)
	if rc != 1 {
		t.Errorf("rc = %d, want 1", rc)
	}
	if !strings.Contains(errBuf.String(), "No user-installed loophole at") {
		t.Errorf("err = %q", errBuf.String())
	}
	// §5.2: the fallback instruction must point at the USER config — today's
	// CLI used to direct people at the weaker, agent-editable workspace scope.
	if !strings.Contains(errBuf.String(), "user config") ||
		!strings.Contains(errBuf.String(), "config.jsonc") {
		t.Errorf("err = %q, want the USER config named as the place to toggle", errBuf.String())
	}
	if strings.Contains(errBuf.String(), "workspace") {
		t.Errorf("err = %q still points at the workspace config", errBuf.String())
	}
}

func TestSetEnabledRoundTrip(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	// Create a user-installed loophole manifest.
	userDir := UserLoopholesDir()
	lhDir := filepath.Join(userDir, "myhole")
	must(t, os.MkdirAll(lhDir, 0o755))
	must(t, os.WriteFile(filepath.Join(lhDir, "manifest.jsonc"),
		[]byte(`{"name": "myhole", "description": "test", "transport": "none", "enabled": true}`), 0o644))

	var out, errBuf bytes.Buffer
	deps := Deps{Out: &out, Err: &errBuf, Cwd: home}
	if rc := CmdSetEnabled(deps, "myhole", false); rc != 0 {
		t.Fatalf("disable rc = %d, err=%q", rc, errBuf.String())
	}
	if out.String() != "disabled myhole\n" {
		t.Errorf("disable output = %q", out.String())
	}
	// Manifest now has enabled:false.
	data, _ := os.ReadFile(filepath.Join(lhDir, "manifest.jsonc"))
	if !strings.Contains(string(data), "false") {
		t.Errorf("manifest not updated: %s", data)
	}
	out.Reset()
	if rc := CmdSetEnabled(deps, "myhole", true); rc != 0 {
		t.Fatalf("enable rc = %d", rc)
	}
	if out.String() != "enabled myhole\n" {
		t.Errorf("enable output = %q", out.String())
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// isolateDirs points bundled/user loophole discovery at throwaway dirs, so no
// real manifest (and no real doctor_cmd) can leak into a test.
func isolateDirs(t *testing.T) {
	t.Helper()
	emptyBundled, emptyUser := t.TempDir(), t.TempDir()
	oldBundled, oldUser := BundledLoopholesDir, UserLoopholesDir
	BundledLoopholesDir = func() string { return emptyBundled }
	UserLoopholesDir = func() string { return emptyUser }
	t.Cleanup(func() {
		BundledLoopholesDir = oldBundled
		UserLoopholesDir = oldUser
	})
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

	got := loopholesWithConfig(deps, true)
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

	got := loopholesWithConfig(deps, true)
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
	got = loopholesWithConfig(deps, true)
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

	got := loopholesWithConfig(deps, true)
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

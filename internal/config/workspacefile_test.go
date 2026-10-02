package config

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// workspacefile_test.go covers the per-workspace file (workspacefile.go;
// docs/design/boundary-broker.md OQ-BB12, BB-D53 to BB-D57) through the call sites that read it:
// LoadConfig, which every launch, `yolo check` and `yolo config dump` compose through,
// ValidateConfig, which refuses for the launch and `yolo check`, and SetWorkspaceLoophole, the
// writer `yolo loopholes enable|disable` calls. The command itself is
// cli.TestLoopholesEnableWritesThePerWorkspaceFileAndNothingElse, and the launch's broker set
// over it run.TestOnlyAWorkspaceSwitchedOnStartsTheBroker.

// workspaceFileHome is an isolated, resolved HOME with an empty user config folder, and a
// resolved workspace folder named name beside it.
func workspaceFileHome(t *testing.T, name string) (home, userCfg, ws string) {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(UserLayerEnv, "")
	userCfg = filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(userCfg), 0o755); err != nil {
		t.Fatal(err)
	}
	ws = filepath.Join(home, "code", name)
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, userCfg, ws
}

// expectedWorkspaceFile spells the file's name from its documented rule, independently of
// WorkspaceFilePath: the folder beside config.jsonc, the slug, and the first 12 hex digits of
// the SHA-256 of the resolved workspace path.
func expectedWorkspaceFile(home, slug, resolvedWS string) string {
	sum := sha256.Sum256([]byte(resolvedWS))
	return filepath.Join(home, ".config", "yolo-jail", "workspaces",
		slug+"-"+hex.EncodeToString(sum[:])[:12]+".jsonc")
}

// TestTheWorkspaceFileIsNamedByTheResolvedWorkspace: one file per workspace, beside the user
// config, named by the folder and the hash of its resolved path, so a link to the workspace
// names the same file, and a name a file system would mangle is spelled safely.
func TestTheWorkspaceFileIsNamedByTheResolvedWorkspace(t *testing.T) {
	home, _, ws := workspaceFileHome(t, "app")
	want := expectedWorkspaceFile(home, "app", ws)
	if got := WorkspaceFilePath(ws); got != want {
		t.Fatalf("WorkspaceFilePath(%s) = %s, want %s", ws, got, want)
	}
	link := filepath.Join(home, "app-link")
	if err := os.Symlink(ws, link); err != nil {
		t.Fatal(err)
	}
	if got := WorkspaceFilePath(link); got != want {
		t.Errorf("the workspace through a symlink = %s, want the same file %s", got, want)
	}
	if got := WorkspaceFilePath(filepath.Join(home, "code")); got == want {
		t.Errorf("the workspace's parent named the workspace's own file %s", got)
	}
	for base, slug := range map[string]string{
		"my app (v2)": "my-app-v2", ".hidden": "hidden", "日本": "workspace",
		strings.Repeat("a", 60): strings.Repeat("a", 40),
	} {
		if got := workspaceFileSlug(base); got != slug {
			t.Errorf("workspaceFileSlug(%q) = %q, want %q", base, got, slug)
		}
	}
}

// TestLoadConfigMergesTheWorkspaceFileLast: the file's switch wins over the user config AND the
// workspace's own config files, for its workspace alone; LoadConfigWithoutWorkspaceFile (what a
// jail inherits) leaves it out; and a refusal of a key it wrote names the file.
func TestLoadConfigMergesTheWorkspaceFileLast(t *testing.T) {
	_, userCfg, ws := workspaceFileHome(t, "app")
	write(t, userCfg, `{"loopholes": {"audio": {"enabled": false, "settings": {"x": 1}}}}`)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"loopholes": {"audio": {"enabled": false}}}`)
	path, err := SetWorkspaceLoophole(ws, "audio", true)
	if err != nil {
		t.Fatal(err)
	}

	cfg, src, err := LoadConfigWithSources(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := asMap(getOr(cfg, "loopholes", nil))
	if v, set := LoopholeEnabledOverride(block, "audio"); !set || !v {
		t.Fatalf("loopholes.audio.enabled = %v (set %v), want true: the per-workspace file merges "+
			"last, over both config files", v, set)
	}
	entry, _ := asMap(getOr(block, "audio", nil))
	if _, kept := entry.Get("settings"); !kept {
		t.Errorf("the merge dropped the user config's other keys for the loophole: %v", entry.Keys())
	}
	if got := src.AnnotateOne("config.loopholes.audio.enabled: x"); !strings.Contains(got, filepath.Base(path)) {
		t.Errorf("a message about the switch is located at %q, want the per-workspace file %s", got, path)
	}

	without, err := LoadConfigWithoutWorkspaceFile(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	wb, _ := asMap(getOr(without, "loopholes", nil))
	if v, _ := LoopholeEnabledOverride(wb, "audio"); v {
		t.Errorf("LoadConfigWithoutWorkspaceFile carried the per-workspace switch into what a jail inherits")
	}

	other := filepath.Join(filepath.Dir(ws), "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	oc, err := LoadConfig(other, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	ob, _ := asMap(getOr(oc, "loopholes", nil))
	if v, _ := LoopholeEnabledOverride(ob, "audio"); v {
		t.Errorf("another workspace got this workspace's switch")
	}
}

// TestAWorkspaceFileNamingAnotherWorkspaceIsIgnoredAndWarned: the `workspace` field is
// authoritative, so a file copied under this workspace's name applies nothing, and the launch's
// validation warns naming both and the next step.
func TestAWorkspaceFileNamingAnotherWorkspaceIsIgnoredAndWarned(t *testing.T) {
	_, _, ws := workspaceFileHome(t, "app")
	write(t, WorkspaceFilePath(ws), `{"workspace": "/somewhere/else", "loopholes": {"audio": {"enabled": true}}}`)
	cfg, err := LoadConfig(ws, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := asMap(getOr(cfg, "loopholes", nil))
	if _, set := LoopholeEnabledOverride(block, "audio"); set {
		t.Fatal("a file naming another workspace applied its switch")
	}
	errs, warns := ValidateConfig(cfg, ws, fakeResolver{"audio": {Name: "audio"}})
	if len(errs) != 0 {
		t.Errorf("errors %v, want none", errs)
	}
	joined := strings.Join(warns, "\n")
	for _, want := range []string{"/somewhere/else", ws, "nothing in it applies", "yolo loopholes enable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the warning does not name %q:\n%s", want, joined)
		}
	}
}

// TestTheWorkspaceFileRefusesWhatItDoesNotHold: an unknown key, a loophole key other than
// `enabled` and a non-boolean switch are refused by the launch's validation, located in the
// file, each with a next step; the well-formed switches beside them still read.
func TestTheWorkspaceFileRefusesWhatItDoesNotHold(t *testing.T) {
	_, _, ws := workspaceFileHome(t, "app")
	path := WorkspaceFilePath(ws)
	write(t, path, `{
  "workspace": "`+ws+`",
  "packs": ["github"],
  "loopholes": {
    "a": {"enabled": true},
    "b": {"enabled": "yes"},
    "c": {"enabled": true, "settings": {}}
  }
}`)
	wf := ReadWorkspaceFile(ws)
	if wf == nil || !wf.Applies {
		t.Fatalf("ReadWorkspaceFile = %+v, want a file that applies", wf)
	}
	if got := wf.Switches(); got != "a on" {
		t.Errorf("Switches() = %q, want only the well-formed one", got)
	}
	errs, _ := ValidateConfig(jsonx.NewOrderedMap(), ws, nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{
		"~/.config/yolo-jail/workspaces/" + filepath.Base(path) + ":3:",
		`'packs' is not a key a per-workspace file holds`,
		`"enabled" must be true or false`, "`yolo loopholes enable b`",
		`'settings' is not a key a per-workspace file holds for a loophole`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the refusals do not say %q:\n%s", want, joined)
		}
	}
}

// TestSetWorkspaceLoopholeWritesOnlyItsOwnFile: the writer creates the folder 0700 and the file
// 0600, keeps the file's other switches, names the workspace, carries the header that says who
// writes it, never touches config.jsonc, and refuses a file it cannot read whole rather than
// writing over it.
func TestSetWorkspaceLoopholeWritesOnlyItsOwnFile(t *testing.T) {
	home, userCfg, ws := workspaceFileHome(t, "app")
	const userBody = "// my hand-written config\n{\n  \"packs\": [\"github\"], // keep me\n}\n"
	write(t, userCfg, userBody)

	path, err := SetWorkspaceLoophole(ws, "github-broker", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := expectedWorkspaceFile(home, "app", ws); path != want {
		t.Fatalf("wrote %s, want %s", path, want)
	}
	if _, err := SetWorkspaceLoophole(ws, "journal", false); err != nil {
		t.Fatal(err)
	}
	if _, err := SetWorkspaceLoophole(ws, "github-broker", true); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(filepath.Dir(path)); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("the folder's mode = %v (%v), want 0700", fi.Mode().Perm(), err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("the file's mode = %v (%v), want 0600", fi.Mode().Perm(), err)
	}
	data, _ := os.ReadFile(path)
	body := string(data)
	for _, want := range []string{"`yolo loopholes enable`", `"workspace": "` + ws + `"`,
		`"github-broker": {`, `"journal": {`} {
		if !strings.Contains(body, want) {
			t.Errorf("the file does not hold %q:\n%s", want, body)
		}
	}
	wf := ReadWorkspaceFile(ws)
	if got := wf.Switches(); got != "github-broker on, journal off" {
		t.Errorf("Switches() = %q, want both switches in the order written", got)
	}
	if after, _ := os.ReadFile(userCfg); string(after) != userBody {
		t.Errorf("config.jsonc changed:\n%s", after)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
		t.Errorf("a temporary file was left behind: %v", left)
	}

	// A hand edit that broke the file is not written over.
	write(t, path, `{"workspace": `)
	if _, err := SetWorkspaceLoophole(ws, "github-broker", false); err == nil ||
		!strings.Contains(err.Error(), "fix it, or delete it") {
		t.Errorf("an unreadable file was written over, or the refusal names no next step: %v", err)
	}
	if after, _ := os.ReadFile(path); string(after) != `{"workspace": ` {
		t.Errorf("the unreadable file changed: %s", after)
	}
}

// TestABrokeredLoopholeIsSwitchedOnlyByTheWorkspaceFile: MANUAL-ONLY (OQ-BB13, BB-D55). A
// brokered loophole's switch in the user config is refused in both directions, as is one in the
// workspace's own config files, each naming `yolo loopholes enable|disable`; the per-workspace
// file's is accepted, and in a jail the workspace file's is a warning as every workspace scope
// rule is.
func TestABrokeredLoopholeIsSwitchedOnlyByTheWorkspaceFile(t *testing.T) {
	resolver := fakeResolver{"github-broker": {Name: "github-broker", HasHostDaemon: true,
		Brokered: &loopholedecl.Brokered{Source: "github", RemoteHost: "github.com"}}}
	validate := func(ws string) (string, string) {
		t.Helper()
		cfg, err := LoadConfig(ws, true, nil)
		if err != nil {
			t.Fatal(err)
		}
		errs, warns := ValidateConfig(cfg, ws, resolver)
		return strings.Join(errs, "\n"), strings.Join(warns, "\n")
	}

	t.Run("user config", func(t *testing.T) {
		for _, v := range []string{"true", "false"} {
			_, userCfg, ws := workspaceFileHome(t, "app")
			write(t, userCfg, `{"loopholes": {"github-broker": {"enabled": `+v+`}}}`)
			errs, _ := validate(ws)
			for _, want := range []string{"config.jsonc:", "config.loopholes.github-broker.enabled",
				"may not switch it", "`yolo loopholes enable github-broker`"} {
				if !strings.Contains(errs, want) {
					t.Errorf("enabled: %s: the refusal does not say %q:\n%s", v, want, errs)
				}
			}
		}
	})
	t.Run("workspace config", func(t *testing.T) {
		for _, name := range []string{WorkspaceConfigName, WorkspaceLocalConfigName} {
			_, _, ws := workspaceFileHome(t, "app")
			write(t, filepath.Join(ws, name), `{"loopholes": {"github-broker": {"enabled": false}}}`)
			errs, warns := validate(ws)
			for _, want := range []string{name, "`yolo loopholes disable github-broker`", "agent can edit"} {
				if !strings.Contains(errs, want) {
					t.Errorf("%s: the refusal does not say %q:\n%s", name, want, errs)
				}
			}
			if strings.Contains(warns, "disabled by") {
				t.Errorf("%s: the refused switch was also disclosed as if it decided something:\n%s", name, warns)
			}
			t.Setenv("YOLO_VERSION", "9.9.9-test")
			jerrs, jwarns := validate(ws)
			if strings.Contains(jerrs, "agent can edit") || !strings.Contains(jwarns, "agent can edit") {
				t.Errorf("%s in a jail: errors %q warnings %q, want the refusal as a warning", name, jerrs, jwarns)
			}
		}
	})
	t.Run("per-workspace file", func(t *testing.T) {
		_, _, ws := workspaceFileHome(t, "app")
		if _, err := SetWorkspaceLoophole(ws, "github-broker", true); err != nil {
			t.Fatal(err)
		}
		if errs, _ := validate(ws); errs != "" {
			t.Errorf("the per-workspace file's switch was refused:\n%s", errs)
		}
	})
}

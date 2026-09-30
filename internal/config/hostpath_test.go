package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// hostpath_test.go covers the `host_path` key (docs/design/host-launch-environment.md §2.2): read
// from user scope only, `~/` expanded, written order kept, every refused entry left out by the
// reader, and refused at workspace scope. Validation goes through ValidateConfig, the call site
// `yolo check` and a launch reach.

// TestHostPathFoldersIsTheUserConfigsListAndNothingElse: absent, empty and unparseable read as no
// folders; a list keeps its order with `~/` expanded against the home; an entry validation refuses
// never reaches the list; and a workspace value is never read — a cloned repository must not add
// a folder to a host agent's PATH.
func TestHostPathFoldersIsTheUserConfigsListAndNothingElse(t *testing.T) {
	userCfg := hostFloorHome(t)
	home := paths.Home()
	ws := t.TempDir()
	t.Chdir(ws)
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_path": ["/from/the/workspace"]}`)
	if got := HostPathFolders(); len(got) != 0 {
		t.Errorf("with no user config = %q, want none; a workspace value must never be read", got)
	}
	write(t, userCfg, `{"host_path": ["/opt/x/bin"`)
	if got := HostPathFolders(); len(got) != 0 {
		t.Errorf("an unparseable user config = %q, want none", got)
	}
	write(t, userCfg, `{"host_path": []}`)
	if got := HostPathFolders(); len(got) != 0 {
		t.Errorf("an empty list = %q, want none", got)
	}
	write(t, userCfg, `{"host_path": ["~/.cargo/bin", "/opt/homebrew/bin/", "relative/bin",
	  "~other/bin", "$HOME/bin", "/a:/b", "", "~", 7, "/usr/local/go/bin"]}`)
	want := []string{filepath.Join(home, ".cargo", "bin"), "/opt/homebrew/bin", "/usr/local/go/bin"}
	if got := HostPathFolders(); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("HostPathFolders = %q, want %q: written order, `~/` expanded, refused entries left out",
			got, want)
	}
}

// TestValidateHostPathRefusesEachBadEntryAndAWorkspaceValue goes through ValidateConfig: each of
// the grammar's refusals is named with its entry, a list of good folders passes, a non-list is a
// type error, and a workspace spelling is refused by name with the file it belongs in.
func TestValidateHostPathRefusesEachBadEntryAndAWorkspaceValue(t *testing.T) {
	hostFloorHome(t)
	clean := t.TempDir()
	for _, ok := range []string{`{"host_path": []}`, `{"host_path": ["~/.cargo/bin", "/opt/homebrew/bin"]}`} {
		errs, _ := ValidateConfig(decode(t, ok), clean, nil)
		for _, e := range errs {
			if strings.Contains(e, "host_path") {
				t.Errorf("%s refused: %s", ok, e)
			}
		}
	}
	for bad, want := range map[string]string{
		`{"host_path": "~/.cargo/bin"}`: "expected a list of folders",
		`{"host_path": [3]}`:            "entry 0: expected a folder string",
		`{"host_path": ["bin"]}`:        "it is relative",
		`{"host_path": ["./bin"]}`:      "it is relative",
		`{"host_path": ["~matt/bin"]}`:  "`~user/` is not expanded",
		`{"host_path": ["$HOME/bin"]}`:  "carries a `$`",
		`{"host_path": ["~/${X}/bin"]}`: "carries a `$`",
		`{"host_path": ["/a/bin:/b"]}`:  "carries a `:`",
		`{"host_path": [""]}`:           "names no folder",
		`{"host_path": ["/ok", "rel"]}`: "entry 1 'rel'",
		`{"host_path": ["~"]}`:          "`~/<folder>`",
	} {
		errs, _ := ValidateConfig(decode(t, bad), clean, nil)
		joined := strings.Join(errs, "\n")
		if !strings.Contains(joined, "config.host_path: ") || !strings.Contains(joined, want) {
			t.Errorf("%s: errors lack %q:\n%s", bad, want, joined)
		}
	}

	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"host_path": ["/opt/x/bin"]}`)
	errs, _ := ValidateConfig(decode(t, `{"host_path": ["/opt/x/bin"]}`), ws, nil)
	joined := strings.Join(errs, "\n")
	for _, want := range []string{"config.host_path: user-scope only", paths.UserConfigPath()} {
		if !strings.Contains(joined, want) {
			t.Errorf("a workspace host_path: errors lack %q:\n%s", want, joined)
		}
	}
	if _, known := knownTopLevelConfigKeys["host_path"]; !known {
		t.Error("host_path is missing from knownTopLevelConfigKeys")
	}
}

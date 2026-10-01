package check

// refusallocation_test.go pins `yolo check` naming WHERE a refused key was written
// (userguide/reference/configuration.md#the-config-files). The Merged
// Configuration section validates a config composed from the user config, its includes, any
// --user-layer, and the workspace config and its local override; a refusal that names only
// the key leaves the user to search every one of them. Each case drives Check() itself, so
// the location has to come through the section that prints it.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// locatedCheck writes the user config files (name → body, under ~/.config/yolo-jail) and the
// workspace config, runs `yolo check`, and returns its plain output.
func locatedCheck(t *testing.T, userFiles map[string]string, wsConfig string) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_VERSION", "")
	t.Setenv("YOLO_USER_LAYER", "")
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range userFiles {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Host files a host_files entry may name, so a missing source is not what is refused.
	for _, dotfile := range []string{".tool-a.conf", ".tool-b.conf"} {
		if err := os.WriteFile(filepath.Join(home, dotfile), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	opts := baseOptions(t, &out)
	if wsConfig != "" {
		if err := os.WriteFile(filepath.Join(opts.Workspace, "yolo-jail.jsonc"), []byte(wsConfig), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts.PathExists = func(p string) bool { _, err := os.Stat(p); return err == nil }
	if exit := Check(opts); exit == 0 {
		t.Fatalf("check passed a config it must refuse:\n%s", out.String())
	}
	return stripANSI(out.String())
}

// The retired key in a file the user config includes: check names that file and line.
func TestCheckRefusalNamesTheIncludedFileAndLine(t *testing.T) {
	got := locatedCheck(t, map[string]string{
		"config.jsonc":   `{"include_if_found": ["profiles.jsonc"]}`,
		"profiles.jsonc": "{\n  \"use_profiles\": {\"claude\": \"zai\"}\n}\n",
	}, "")
	want := "~/.config/yolo-jail/profiles.jsonc:2:19: config.use_profiles: RENAMED"
	if !strings.Contains(got, want) {
		t.Errorf("check does not say where use_profiles was written (want %q):\n%s", want, got)
	}
}

// A list key two files write is merged into one list, and a refusal naming an index of the
// merged list names the file that entry came from — here the include's first entry, which is
// the merged list's second.
func TestCheckRefusalNamesTheFileAListEntryCameFrom(t *testing.T) {
	got := locatedCheck(t, map[string]string{
		"config.jsonc": `{"packages": ["strace"], "include_if_found": ["extra.jsonc"]}`,
		"extra.jsonc":  "{\n  \"packages\": [\n    \"bad name!\"\n  ]\n}\n",
	}, "")
	want := "~/.config/yolo-jail/extra.jsonc:3:5: config.packages[1]: invalid package name"
	if !strings.Contains(got, want) {
		t.Errorf("check does not name the file the bad entry came from (want %q):\n%s", want, got)
	}
}

// The workspace host_files refusal counts entries in the WORKSPACE file, not in the merged
// list, so it must name the workspace file: the merged list's entry at the same index is the
// user config's, which is allowed.
func TestCheckWorkspaceHostFilesRefusalNamesTheWorkspaceFile(t *testing.T) {
	got := locatedCheck(t, map[string]string{
		"config.jsonc": `{"host_files": ["~/.tool-a.conf"]}`,
	}, "{\n  \"host_files\": [\"~/.tool-b.conf\"]\n}\n")
	var line string
	for _, l := range strings.Split(got, "\n") {
		if strings.Contains(l, "an entry that names a host source is user-scope only") {
			line = l
		}
	}
	if line == "" {
		t.Fatalf("no workspace host_files refusal:\n%s", got)
	}
	if !strings.Contains(line, "yolo-jail.jsonc:2:18: config.host_files[0]") {
		t.Errorf("the refusal does not name the workspace entry's line:\n%s", line)
	}
	if strings.Contains(line, "~/.config/yolo-jail/config.jsonc:1:") {
		t.Errorf("the refusal names the user config, where the entry at that index is allowed:\n%s", line)
	}
}

package check

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckValidatesAProjectsRequestScopeSetting: `yolo check` hands a loophole's settings
// validator the settings a launch hands its daemon, so a project's request-scope setting under
// `[requests.<pack>]` replaces the user's value for this project (docs/reference/loophole-grants.md
// LG-D15, docs/reference/config/rulings.md LC-D17). The validator accepts only the project's
// value; with loopholes.WithProjectRequestSettings deleted from the check, it sees the user's and
// refuses.
func TestCheckValidatesAProjectsRequestScopeSetting(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	const name = "acme-request"
	manifest := map[string]any{
		"name": name, "description": "request-scope settings fixture", "transport": "loopback-tls",
		"settings": map[string]any{"value": map[string]any{"type": "string", "scope": "request", "default": "default-value"}},
		"host_daemon": map[string]any{
			"cmd": []string{"/bin/sh", "-c", "exit 0", "--socket", "{socket}", "--settings", "{settings}"},
			"settings_check": []string{"/bin/sh", "-c",
				`if grep -q project-value "$1"; then exit 0; fi; printf '%s' '{"reason":"the project value did not reach the validator","remedy":"none"}'; exit 9`,
				"validator", "{settings}"},
			"publishes": "socket",
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeLoopholeManifest(t, moduleRoot, name, string(data[1:len(data)-1]))
	writeUserConfigForTest(t, `{"loopholes":{"`+name+`":{"settings":{"value":"user-value"}}}}`)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"),
		[]byte(`{"requests":{"`+name+`":{"value":"project-value"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r, out := runCheckLoopholes(t, workspace)
	if r.failed != 0 || !strings.Contains(out, "loophole "+name+": settings accepted") {
		t.Fatalf("the validator did not see the project's request-scope value: failed=%d\n%s", r.failed, out)
	}
}

// TestCheckDoctorReadsAProjectsRequestScopeSetting is the same for a loophole with no settings
// validator, whose doctor_cmd is handed the settings snapshot: the project's value, not the user's.
func TestCheckDoctorReadsAProjectsRequestScopeSetting(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	const name = "acme-request-doctor"
	manifest := map[string]any{
		"name": name, "description": "request-scope settings fixture", "transport": "loopback-tls",
		"settings": map[string]any{"value": map[string]any{"type": "string", "scope": "request", "default": "default-value"}},
		"host_daemon": map[string]any{
			"cmd":       []string{"/bin/sh", "-c", "exit 0", "--socket", "{socket}", "--settings", "{settings}"},
			"publishes": "socket",
		},
		"doctor_cmd": []string{"/bin/sh", "-c",
			`if grep -q project-value "$1"; then echo 'OK: the doctor saw the project value'; else echo 'FAIL: the doctor saw another value'; exit 1; fi`,
			"doctor", "{settings}"},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeLoopholeManifest(t, moduleRoot, name, string(data[1:len(data)-1]))
	writeUserConfigForTest(t, `{"loopholes":{"`+name+`":{"settings":{"value":"user-value"}}}}`)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"),
		[]byte(`{"requests":{"`+name+`":{"value":"project-value"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, out := runCheckLoopholes(t, workspace)
	if !strings.Contains(out, "the doctor saw the project value") {
		t.Fatalf("the doctor did not see the project's request-scope value:\n%s", out)
	}
}

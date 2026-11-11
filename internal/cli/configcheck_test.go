package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/configdoc"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/testconfig"
)

func TestConfigCheckDispatchValidInvalidAndJSON(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "[machine.resources]\ncpus = 2\n")
	user := testconfig.UserFormat(t, configdoc.TOML, "")
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)

	var text bytes.Buffer
	if rc := configRunW([]string{"check"}, &text, &bytes.Buffer{}); rc != 0 {
		t.Fatalf("valid config check rc=%d:\n%s", rc, text.String())
	}
	if !strings.Contains(text.String(), "Config Check") || strings.Contains(text.String(), "Container Runtime") || strings.Contains(text.String(), "Disk usage") {
		t.Fatalf("config check ran the broad readiness check:\n%s", text.String())
	}

	var jsonOut bytes.Buffer
	if rc := configRunW([]string{"check", "--format", "json"}, &jsonOut, &bytes.Buffer{}); rc != 0 {
		t.Fatalf("valid JSON config check rc=%d:\n%s", rc, jsonOut.String())
	}
	var report struct {
		Mode  string `json:"mode"`
		Scope struct {
			Sources    string `json:"sources"`
			NotChecked []struct {
				Area   string `json:"area"`
				Reason string `json:"reason"`
				Verb   string `json:"verb"`
			} `json:"not_checked"`
			NotReached []string `json:"not_reached"`
		} `json:"scope"`
		Passed   int `json:"passed"`
		Warned   int `json:"warned"`
		Failed   int `json:"failed"`
		Skipped  int `json:"skipped"`
		Findings []struct {
			Section string `json:"section"`
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(jsonOut.Bytes(), &report); err != nil {
		t.Fatalf("config check JSON: %v\n%s", err, jsonOut.String())
	}
	if report.Mode != "config" || report.Scope.Sources != "user+workspace" || len(report.Scope.NotChecked) == 0 || len(report.Findings) == 0 {
		t.Fatalf("JSON mode/scope/findings = %q/%+v/%d", report.Mode, report.Scope, len(report.Findings))
	}
	textReport := text.String()
	for _, entry := range report.Scope.NotChecked {
		want := entry.Area + ": " + entry.Reason + " — " + entry.Verb
		if !strings.Contains(textReport, want) {
			t.Errorf("JSON scope entry %q is absent from text report", want)
		}
	}
	for _, finding := range report.Findings {
		if !strings.Contains(textReport, finding.Section) || !strings.Contains(textReport, finding.Message) {
			t.Errorf("JSON finding %q in %q is absent from the text report", finding.Message, finding.Section)
		}
	}
	for _, tc := range []struct {
		badge, status string
		count         int
	}{{"[PASS]", "pass", report.Passed}, {"[WARN]", "warn", report.Warned},
		{"[FAIL]", "fail", report.Failed}, {"[SKIP]", "skip", report.Skipped}} {
		if got := strings.Count(textReport, tc.badge); got != tc.count {
			t.Errorf("text %s count=%d, JSON %s count=%d", tc.badge, got, tc.status, tc.count)
		}
	}

	workspace = testconfig.WorkspaceFormat(t, configdoc.TOML, "unknown_config_key = true\n")
	withConfigEditCWD(t, workspace.Root)
	var invalid bytes.Buffer
	if rc := configRunW([]string{"check"}, &invalid, &bytes.Buffer{}); rc != 1 || !strings.Contains(invalid.String(), "unknown_config_key") {
		t.Fatalf("invalid config check rc=%d output=%q", rc, invalid.String())
	}
}

func TestConfigCheckSkipsUnavailableFetchedPackAndKeepsOtherFailures(t *testing.T) {
	workspace := testconfig.WorkspaceFormat(t, configdoc.JSONC, `{"unknown_config_key":true}`)
	user := testconfig.UserFormat(t, configdoc.JSONC, `{"packs":["git+https://example.invalid/acme/packs//tool?ref=v1"]}`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	var out, errw bytes.Buffer
	rc := configRunW([]string{"check"}, &out, &errw)
	if rc != 1 || !strings.Contains(out.String(), "[SKIP] Selected pack tool declaration checks skipped") ||
		!strings.Contains(out.String(), "[FAIL]") || !strings.Contains(out.String(), "unknown_config_key") ||
		!strings.Contains(out.String(), "yolo pack install") {
		t.Fatalf("unavailable pack did not skip independently of unrelated config FAIL (rc=%d):\n%s\n%s", rc, out.String(), errw.String())
	}
	store := filepath.Join(user.Root, ".local", "share", "yolo-jail", "packs")
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Errorf("read-only config check created the fetched-pack store %s (stat err %v)", store, err)
	}
}

func TestConfigCheckDoesNotEnsureStorageOrProbeRuntime(t *testing.T) {
	packload.ReleaseEmbedded()
	workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
	user := testconfig.UserFormat(t, configdoc.JSONC, `{"profile":{"no_such_cli":"default"}}`)
	t.Setenv("HOME", user.Root)
	t.Setenv("YOLO_VERSION", "")
	withConfigEditCWD(t, workspace.Root)
	storageRoot := filepath.Join(user.Root, ".local", "share", "yolo-jail")
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"check"}, &out, &errw); rc != 1 || !strings.Contains(out.String(), "no_such_cli") {
		t.Fatalf("config check rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
	if _, err := os.Stat(storageRoot); !os.IsNotExist(err) {
		t.Errorf("config check created global storage at %s (stat err %v)", storageRoot, err)
	}
	if packload.EmbeddedLoaded() {
		root, fallback := packload.EmbeddedLocation()
		t.Errorf("config check materialized embedded cache at %q (fallback=%t)", root, fallback)
	}
	if strings.Contains(out.String(), "\nContainer Runtime\n") || strings.Contains(out.String(), "\nNix\n") {
		t.Errorf("config check entered machine readiness sections:\n%s", out.String())
	}
}

func TestConfigCheckDoesNotMaterializeEmbeddedCacheForConfigDiagnostics(t *testing.T) {
	cases := []struct {
		name, user string
	}{
		{"profile namespace", `{"profile":{"no_such_cli":"default"}}`},
		{"former pack name", `{"packs":["openai-auth"]}`},
		{"host import surface", `{"host_import_exclusions":{"claude/settings":["/hooks"]}}`},
		{"selected login loophole", `{"packs":["aws-auth"],"loopholes":{"aws-auth":{"enabled":true}}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			packload.ReleaseEmbedded()
			workspace := testconfig.WorkspaceFormat(t, configdoc.TOML, "")
			user := testconfig.UserFormat(t, configdoc.JSONC, tc.user)
			t.Setenv("HOME", user.Root)
			t.Setenv("YOLO_VERSION", "")
			withConfigEditCWD(t, workspace.Root)
			storageRoot := filepath.Join(user.Root, ".local", "share", "yolo-jail")
			var out, errw bytes.Buffer
			configRunW([]string{"check"}, &out, &errw)
			if _, err := os.Stat(storageRoot); !os.IsNotExist(err) {
				t.Errorf("config check created global storage at %s (stat err %v)", storageRoot, err)
			}
			if packload.EmbeddedLoaded() {
				root, fallback := packload.EmbeddedLocation()
				t.Errorf("config check materialized embedded cache at %q (fallback=%t): %s", root, fallback, out.String())
			}
		})
	}
}

func TestConfigCheckRejectsReadinessFlagsAndHelpDoesNoWork(t *testing.T) {
	for _, args := range [][]string{{"check", "--build"}, {"check", "--no-build"}, {"check", "--accept-config-changes"}, {"check", "--format", "yaml"}} {
		var out, errw bytes.Buffer
		if rc := configRunW(args, &out, &errw); rc != 2 || out.Len() != 0 {
			t.Errorf("configRunW(%q) = %d stdout=%q stderr=%q; want usage error before validation", args, rc, out.String(), errw.String())
		}
	}
	var out, errw bytes.Buffer
	if rc := configRunW([]string{"check", "--help"}, &out, &errw); rc != 0 || out.Len() == 0 || errw.Len() != 0 {
		t.Fatalf("config check help rc=%d stdout=%q stderr=%q", rc, out.String(), errw.String())
	}
}

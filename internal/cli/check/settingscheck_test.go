package check

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func settingsCheckPack(t *testing.T, root, name, validator, doctorSentinel string) string {
	t.Helper()
	return settingsCheckPackArgv(t, root, name,
		[]string{"/bin/sh", "-c", validator, "validator", "{settings}"}, doctorSentinel)
}

func settingsCheckPackArgv(t *testing.T, root, name string, argv []string, doctorSentinel string, daemonArgv ...[]string) string {
	t.Helper()
	daemon := []string{"/bin/sh", "-c", "exit 0", "--socket", "{socket}", "--settings", "{settings}"}
	if len(daemonArgv) > 0 {
		daemon = daemonArgv[0]
	}
	manifest := map[string]any{
		"name": name, "description": "settings check fixture", "transport": "loopback-tls", "default_enabled": true,
		"settings": map[string]any{"value": map[string]any{"type": "string", "scope": "workspace", "default": "good"}},
		"host_daemon": map[string]any{
			"cmd":            daemon,
			"settings_check": argv,
			"publishes":      "socket",
		},
		"doctor_cmd": touchAndSay(doctorSentinel, "OK: unrelated health"),
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return writeLoopholeManifest(t, root, name, string(data[1:len(data)-1]))
}

func TestCheckLoopholesValidatesCurrentSnapshotAndSkipsOnlyRefusingDoctor(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	badDoctor := filepath.Join(t.TempDir(), "bad-doctor-ran")
	otherDoctor := filepath.Join(t.TempDir(), "other-doctor-ran")
	validator := `if grep -q invalid "$1"; then printf '%s' '{"reason":"missing narrowing","remedy":"edit user settings"}'; exit 9; fi; exit 0`
	module := settingsCheckPack(t, moduleRoot, "acme-validator", validator, badDoctor)
	selfCheckModule(t, moduleRoot, "other-health", touchAndSay(otherDoctor, "OK: unrelated health"))
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"), []byte(
		`{"loopholes":{"acme-validator":{"settings":{"value":"invalid"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(loopholes.StateDirFor("acme-validator"), loopholes.SettingsFileName)
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	staleBytes := []byte(`{"value":"good"}` + "\n")
	if err := os.WriteFile(stale, staleBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	r, out := runCheckLoopholes(t, workspace)
	if r.failed == 0 || !strings.Contains(out, "missing narrowing") || !strings.Contains(out, "edit user settings") {
		t.Fatalf("current invalid snapshot was not diagnosed: failed=%d\n%s", r.failed, out)
	}
	if _, err := os.Stat(badDoctor); !os.IsNotExist(err) {
		t.Fatalf("refusing service's doctor still ran: %v\n%s", err, out)
	}
	if _, err := os.Stat(otherDoctor); err != nil {
		t.Fatalf("unrelated service doctor did not run: %v\n%s", err, out)
	}
	got, err := os.ReadFile(stale)
	if err != nil || string(got) != string(staleBytes) {
		t.Fatalf("check changed stable daemon settings: got=%q err=%v", got, err)
	}
	if _, err := os.Stat(module); err != nil {
		t.Fatal(err)
	}
}

func TestCheckLoopholesRunsDoctorAfterSettingsValidationPasses(t *testing.T) {
	moduleRoot := isolatedModuleDir(t)
	validatorDoctor := filepath.Join(t.TempDir(), "validator-doctor-ran")
	otherDoctor := filepath.Join(t.TempDir(), "other-doctor-ran")
	settingsPath := loopholes.SettingsFileFor("acme-validator")
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Fatalf("fixture stable settings path should be absent before check: %v", err)
	}
	validator := `grep -q good "$1"; exit $?`
	settingsCheckPack(t, moduleRoot, "acme-validator", validator, validatorDoctor)
	selfCheckModule(t, moduleRoot, "other-health", touchAndSay(otherDoctor, "OK: unrelated health"))
	r, out := runCheckLoopholes(t, t.TempDir())
	if _, err := os.Stat(validatorDoctor); err != nil {
		t.Fatalf("valid settings check did not preserve the service doctor: %v\n%s", err, out)
	}
	if _, err := os.Stat(otherDoctor); err != nil {
		t.Fatalf("unrelated doctor did not run: %v\n%s", err, out)
	}
	if r.failed != 0 || !strings.Contains(out, "settings accepted") {
		t.Fatalf("valid settings check was not accepted: failed=%d\n%s", r.failed, out)
	}
	if _, err := os.Stat(settingsPath); !os.IsNotExist(err) {
		t.Errorf("check published stable settings despite validation being read-only: %v", err)
	}
}

func TestCheckLoopholesAnnouncesSelectedPackValidatorBeforeExecution(t *testing.T) {
	root := t.TempDir()
	moduleRoot := filepath.Join(root, "loopholes")
	marker := filepath.Join(t.TempDir(), "validator-ran")
	daemonStarted := filepath.Join(t.TempDir(), "daemon-started")
	validator := []string{"/bin/sh", "-c", `grep -q private-check-profile "$1" && printf x >> ` + shquote.Quote(marker), "validator", "{settings}"}
	daemon := []string{"/bin/sh", "-c", `printf x > ` + shquote.Quote(daemonStarted), "daemon", "{socket}", "{settings}"}
	module := settingsCheckPackArgv(t, moduleRoot, "acme-validator", validator,
		filepath.Join(t.TempDir(), "doctor-ran"), daemon)
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"validator-pack","contributes":[{"kind":"loophole","from":"loopholes/acme-validator"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(root, "validator-pack")
	if len(problems) != 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, HostExecApproved: true}})
	t.Cleanup(loopholes.ResetPackModules)
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "yolo-jail.jsonc"),
		[]byte(`{"loopholes":{"acme-validator":{"settings":{"value":"private-check-profile"}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("YOLO_V1_PRIVATE_ENV", "private-check-environment")

	writer := &validatorAnnouncementWriter{marker: marker}
	r := newReporter(writer, false)
	o := &Options{Workspace: workspace, Getenv: func(string) string { return "" },
		selectedPacks: []*packload.Pack{pack}, selectedPacksKnown: true}
	fillDefaults(o)
	o.checkLoopholes(r)
	out := writer.String()
	if !writer.seen || !writer.before {
		t.Fatalf("selected pack validator was not announced before execution (seen=%v before=%v):\n%s", writer.seen, writer.before, out)
	}
	for _, want := range []string{"validator-pack", "acme-validator", "settings validator"} {
		if !strings.Contains(out, want) {
			t.Errorf("validator announcement lacks %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{marker, "printf x", "{settings}", "private-check-profile", "private-check-environment"} {
		if strings.Contains(out, secret) {
			t.Errorf("validator announcement disclosed argv/private details %q:\n%s", secret, out)
		}
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "x" {
		t.Fatalf("check did not execute admitted validator exactly once on current bytes: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(daemonStarted); !os.IsNotExist(err) {
		t.Fatalf("check started the selected service daemon: %v", err)
	}
}

type validatorAnnouncementWriter struct {
	strings.Builder
	marker string
	seen   bool
	before bool
}

func (w *validatorAnnouncementWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "settings validator") {
		w.seen = true
		_, err := os.Stat(w.marker)
		w.before = os.IsNotExist(err)
	}
	return w.Builder.Write(p)
}

func TestCheckLoopholesDoesNotRunUnapprovedOrDisabledSettingsValidator(t *testing.T) {
	for _, tc := range []struct {
		name     string
		approved bool
		disabled bool
	}{
		{name: "unapproved", approved: false},
		{name: "disabled", approved: true, disabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			moduleRoot := isolatedModuleDir(t)
			marker := filepath.Join(t.TempDir(), "validator-ran")
			module := settingsCheckPack(t, moduleRoot, "acme-validator",
				`printf x >> `+shquote.Quote(marker)+`; exit 0`, filepath.Join(t.TempDir(), "doctor-ran"))
			recordPackModule(t, module, tc.approved)
			if tc.disabled {
				t.Setenv("XDG_CONFIG_HOME", filepath.Join(os.Getenv("HOME"), ".config"))
				path := paths.UserConfigPath()
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(`{"loopholes":{"acme-validator":{"enabled":false}}}`), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			r, out := runCheckLoopholes(t, t.TempDir())
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("%s validator executed despite its admission gate: %v\n%s", tc.name, err, out)
			}
			if !tc.approved && r.warned == 0 {
				t.Fatalf("unapproved validator was not reported as withheld:\n%s", out)
			}
		})
	}
}

func TestCheckLoopholesNamesValidatorOutcomeAndSkipsOnlyItsDoctor(t *testing.T) {
	cases := []struct {
		name string
		argv []string
		want string
	}{
		{"refused", []string{"/bin/sh", "-c", `printf '%s' '{"reason":"candidate rejected","remedy":"edit config"}'; exit 7`, "validator", "{settings}"}, "settings validation refused"},
		{"timeout", []string{"/bin/sh", "-c", `sleep 5`, "validator", "{settings}"}, "settings validator timed out"},
		{"start failure", []string{"/no/such/settings-validator", "{settings}"}, "settings validator could not start"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			moduleRoot := isolatedModuleDir(t)
			badDoctor := filepath.Join(t.TempDir(), "refused-doctor-ran")
			otherDoctor := filepath.Join(t.TempDir(), "other-doctor-ran")
			settingsCheckPackArgv(t, moduleRoot, "acme-validator", tc.argv, badDoctor)
			selfCheckModule(t, moduleRoot, "other-health", touchAndSay(otherDoctor, "OK: unrelated health"))
			r, out := runCheckLoopholes(t, t.TempDir())
			if !strings.Contains(out, "[FAIL] loophole acme-validator: "+tc.want) {
				t.Fatalf("check did not distinguish %s in its outcome label: want %q\n%s", tc.name, tc.want, out)
			}
			if _, err := os.Stat(badDoctor); !os.IsNotExist(err) {
				t.Errorf("failed validation still ran its own doctor: %v\n%s", err, out)
			}
			if _, err := os.Stat(otherDoctor); err != nil {
				t.Errorf("failed validation prevented unrelated doctor: %v\n%s", err, out)
			}
			if r.failed == 0 {
				t.Errorf("failed validator was not graded as a check failure:\n%s", out)
			}
		})
	}
}

func TestCheckLoopholesDoesNotRunInactiveOrAgentEditableSettingsValidator(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses a Linux shell validator")
	}
	for _, tc := range []struct {
		name        string
		platform    string
		superseded  bool
		agentPlaced bool
	}{
		{name: "unsupported platform", platform: "darwin"},
		{name: "superseded", superseded: true},
		{name: "agent-editable module", agentPlaced: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			moduleRoot := isolatedModuleDir(t)
			workspace := t.TempDir()
			parent := moduleRoot
			if tc.agentPlaced {
				parent = filepath.Join(workspace, "packs", "fixture", "loopholes")
			}
			marker := filepath.Join(t.TempDir(), "validator-ran")
			manifest := map[string]any{
				"name": "acme-validator", "description": "settings validation fixture",
				"transport": "loopback-tls", "default_enabled": true,
				"settings": map[string]any{"value": map[string]any{"type": "string", "scope": "workspace", "default": "good"}},
				"host_daemon": map[string]any{
					"cmd":            []string{"/bin/sh", "-c", "exit 0", "{settings}"},
					"settings_check": []string{"/bin/sh", "-c", `printf x >> ` + shquote.Quote(marker), "validator", "{settings}"},
					"publishes":      "socket",
				},
			}
			if tc.platform != "" {
				manifest["platforms"] = []string{tc.platform}
			}
			if tc.superseded {
				manifest["serves"] = []string{"acme-settings-validation"}
				restore := loopholes.SnapshotPackSupersessions()
				loopholes.SetPackSupersessions([]loopholes.PackSupersession{{Pack: "replacement", Capability: "acme-settings-validation"}})
				t.Cleanup(restore)
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			module := writeLoopholeManifest(t, parent, "acme-validator", string(data[1:len(data)-1]))
			if tc.agentPlaced {
				restore := loopholes.SnapshotPackModules()
				loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, HostExecApproved: true}})
				t.Cleanup(restore)
			}
			r, out := runCheckLoopholes(t, workspace)
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("inactive or agent-editable validator executed: err=%v\n%s", err, out)
			}
			if r.failed != 0 {
				t.Fatalf("inert validator should not make check fail: failed=%d\n%s", r.failed, out)
			}
		})
	}
}

func TestCheckLoopholesDoesNotRunUnselectedPackSettingsValidator(t *testing.T) {
	_ = isolatedModuleDir(t)
	marker := filepath.Join(t.TempDir(), "unselected-validator-ran")
	unselected := settingsCheckPack(t, t.TempDir(), "unselected-validator",
		`printf x >> `+shquote.Quote(marker)+`; exit 0`, filepath.Join(t.TempDir(), "doctor-ran"))
	if _, err := os.Stat(filepath.Join(unselected, "manifest.jsonc")); err != nil {
		t.Fatal(err)
	}
	r, out := runCheckLoopholes(t, t.TempDir())
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("check ran a validator from a pack outside its resolved selection: %v\n%s", err, out)
	}
	if r.failed != 0 || strings.Contains(out, "unselected-validator") {
		t.Fatalf("an unselected validator entered the check's selected-module walk: failed=%d\n%s", r.failed, out)
	}
}

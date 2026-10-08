package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

func TestAWSUnnarrowedRoutineDisclosureDoesNotSuppressOtherPackDisclosures(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)

	awsPath := filepath.Join("..", "..", "..", "packs", "aws-auth", "loopholes", "aws-auth", "manifest.jsonc")
	awsManifest, err := loopholedecl.LoadDir(filepath.Dir(awsPath))
	if err != nil {
		t.Fatal(err)
	}
	settingsMap := jsonx.NewOrderedMap()
	for _, setting := range awsManifest.Settings {
		decl := jsonx.NewOrderedMap()
		decl.Set("type", setting.Type)
		decl.Set("scope", setting.Scope)
		decl.Set("default", setting.Default)
		if setting.Description != "" {
			decl.Set("description", setting.Description)
		}
		if setting.Disclose != "" {
			decl.Set("disclose", setting.Disclose)
		}
		settingsMap.Set(setting.Key, decl)
	}
	settingsJSON, err := jsonx.DumpsCompact(settingsMap)
	if err != nil {
		t.Fatal(err)
	}
	awsPack := writeRealLoopholePack(t, "aws-auth", "aws-auth", fmt.Sprintf(
		`{"name":"aws-auth","transport":"none","default_enabled":false,"settings":%s,
		"host_daemon":{"cmd":[%q,"-front-upstream-child","line","{socket}"],"publishes":"socket"}}`,
		settingsJSON, os.Args[0]))
	otherPack := writeRealLoopholePack(t, "independent", "independent", fmt.Sprintf(`{
		"name":"independent","transport":"none","default_enabled":true,
		"settings":{"notice":{"type":"bool","scope":"user","default":false,
			"disclose":"an independent pack setting is enabled"}},
		"host_daemon":{"cmd":[%q,"-front-upstream-child","line","{socket}"],"publishes":"socket"}
	}`, os.Args[0]))
	startingLoopholePacks(awsPack, otherPack)

	configAWSSettings := jsonx.NewOrderedMap()
	configAWSSettings.Set("profile", "fixture-profile")
	configAWSSettings.Set("unnarrowed", true)
	awsEntry := jsonx.NewOrderedMap()
	awsEntry.Set("enabled", true)
	awsEntry.Set("settings", configAWSSettings)
	otherSettings := jsonx.NewOrderedMap()
	otherSettings.Set("notice", true)
	otherEntry := jsonx.NewOrderedMap()
	otherEntry.Set("enabled", true)
	otherEntry.Set("settings", otherSettings)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set("aws-auth", awsEntry)
	loopCfg.Set("independent", otherEntry)
	cfg := newConfig()
	cfg.Set("loopholes", loopCfg)
	set := loopholes.NewHostSet(loopCfg)
	if enabled := set.Enabled(); len(enabled) != 2 {
		t.Fatalf("enabled fixture loopholes = %+v, want AWS and independent", enabled)
	}

	cname := "yolo-aws-disclosure-regression-" + t.Name()
	var output strings.Builder
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stdout, o.Stderr = &output, &output
	o.PathExists = func(string) bool { return false }
	o.ServiceReadyTimeout = time.Second
	handles, _ := o.startLoopholesDisclosed(cname, "podman", cfg,
		[]*packload.Pack{awsPack, otherPack}, nil)
	if len(handles) != 2 {
		t.Fatalf("started %d services, want AWS and independent fixtures: %s", len(handles), output.String())
	}
	t.Cleanup(func() { o.stopLoopholes(handles, hostServiceSocketsDir(cname, false), cname, "podman") })

	got := output.String()
	if !strings.Contains(got, "This launch runs pack code on your machine") {
		t.Errorf("removing the AWS mode notice also removed unrelated selected-pack trust banners:\n%s", got)
	}
	if !strings.Contains(got, "an independent pack setting is enabled") {
		t.Errorf("removing AWS's routine notice suppressed an unrelated pack disclosure:\n%s", got)
	}
	for _, forbidden := range []string{"UN-NARROWED", "serving UN-NARROWED", "unnarrowed is true"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("a valid explicit AWS permission-set choice produced a routine launch warning %q:\n%s", forbidden, got)
		}
	}
}

func TestHostSingletonStartupRefusalReachesSelectedLaunch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	oldSingletonDir := paths.HostSingletonDir
	paths.HostSingletonDir = t.TempDir()
	t.Cleanup(func() { paths.HostSingletonDir = oldSingletonDir })

	script := `import json, os, socket, struct
record = {"version":1,"service":"host-singleton-fixture","attempt":os.environ["YOLO_HOST_SERVICE_ATTEMPT"],"class":"configuration","reason":"The host singleton rejected this setting.","remedy":"Correct the user-scoped setting and retry."}
data = json.dumps(record, separators=(",", ":")).encode()
s = socket.socket(fileno=3)
s.sendall(struct.pack(">I", len(data)) + data)
`
	manifest := `{
		"name":"host-singleton-fixture", "default_enabled":true, "transport":"loopback-tls",
		"host_daemon":{"cmd":["python3","{loophole_dir}/daemon.py","--socket","{socket}"],
			"scope":"host","publishes":"socket","startup_reason":true}
	}`
	p := writeRealLoopholePack(t, "fixture", "host-singleton-fixture", manifest)
	program := filepath.Join(p.Root, "loopholes", "host-singleton-fixture", "daemon.py")
	if err := os.WriteFile(program, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	startingLoopholePacks(p)

	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set("host-singleton-fixture", entry)
	cfg := newConfig()
	cfg.Set("loopholes", loopCfg)

	cname := "yolo-host-startup-diagnostic-" + t.Name()
	var output bytes.Buffer
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stderr, o.Stdout = &output, &output
	o.PathExists = func(string) bool { return false }
	o.ServiceReadyTimeout = 250 * time.Millisecond
	handles, refused := o.startLoopholesDisclosed(cname, "podman", cfg, []*packload.Pack{p}, nil)
	if refused == nil || refused.startup == nil {
		t.Fatalf("host singleton refusal was not returned through the selected launch: handles=%v output=%s",
			handles, output.String())
	}
	output.WriteString(refused.markup("Refusing this launch"))
	got := output.String()
	for _, want := range []string{"The host singleton rejected this setting.", "Correct the user-scoped setting and retry."} {
		if !strings.Contains(got, want) {
			t.Errorf("host singleton launch output lacks attempt-specific %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "did not bind its socket") || strings.Contains(got, "Expected "+paths.HostSingletonSocket("host-singleton-fixture")) {
		t.Errorf("typed refusal was replaced by the derived socket symptom:\n%s", got)
	}
}

// This crosses the selected pack's real startLoopholesDisclosed boundary. The fixture
// reports only this spawn's refusal on the declared startup channel; a contradictory
// shared historical log is deliberately irrelevant.
func TestAWSStartupRefusalRendersSafeCauseWithoutProfileOrPolicyValues(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)
	profile := "SENTINEL_AWS_PROFILE_STARTUP_DO_NOT_RENDER"
	policyKey := "SENTINEL_AWS_POLICY_KEY_STARTUP_DO_NOT_RENDER"
	policyValue := "SENTINEL_AWS_POLICY_VALUE_STARTUP_DO_NOT_RENDER"
	policy := `{"Statement":[{"Effect":"Allow","Action":["` + policyKey + `"],"Resource":"` + policyValue + `"}]}`
	stateFile := filepath.Join(t.TempDir(), "state.json")
	manifest := fmt.Sprintf(`{
		"name":"aws-auth", "default_enabled":true, "transport":"none",
		"settings":{
			"profile":{"type":"string","scope":"user","default":""},
			"role_arn":{"type":"string","scope":"user","default":""},
			"session_policy":{"type":"string","scope":"user","default":""},
			"unnarrowed":{"type":"bool","scope":"user","default":false}},
		"host_daemon":{"cmd":[%q,"-aws-auth-refusal-child","--socket","{socket}",
			"--state-file",%q,"--settings","{settings}"],"publishes":"socket","startup_reason":true}
	}`, os.Args[0], stateFile)
	p := writeRealLoopholePack(t, "aws-auth", "aws-auth", manifest)
	startingLoopholePacks(p)
	settings := jsonx.NewOrderedMap()
	settings.Set("profile", profile)
	settings.Set("role_arn", "")
	settings.Set("session_policy", policy)
	settings.Set("unnarrowed", false)
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	entry.Set("settings", settings)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set("aws-auth", entry)
	cfg := newConfig("loopholes", loopCfg)
	cname := "yolo-aws-safe-startup-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var output bytes.Buffer
	o := &Options{Workspace: t.TempDir()}
	fillDefaults(o)
	o.Stderr, o.Stdout = &output, &output
	o.PathExists = func(string) bool { return false }
	o.ServiceReadyTimeout = 500 * time.Millisecond
	handles, refused := o.startLoopholesDisclosed(cname, "podman", cfg, []*packload.Pack{p}, nil)
	if refused == nil || refused.startup == nil || len(handles) != 0 {
		t.Fatalf("actual AWS daemon refusal did not reach the launch caller: refusal=%v handles=%v output=%s",
			refused, handles, output.String())
	}
	output.WriteString(refused.markup("Refusing this launch"))
	rendered := output.String()
	for _, want := range []string{"A session policy is configured without a role to assume.",
		"loopholes.aws-auth.settings.role_arn", "Refusing this launch"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendered AWS startup cause lacks %q:\n%s", want, rendered)
		}
	}
	for _, secret := range []string{profile, policyKey, policyValue} {
		if strings.Contains(rendered, secret) {
			t.Errorf("rendered AWS launch refusal leaked %q:\n%s", secret, rendered)
		}
	}
	if strings.Contains(rendered, "did not bind its socket") {
		t.Errorf("AWS typed refusal was replaced by a generic readiness symptom:\n%s", rendered)
	}
}

func TestSelectedServiceStartupRefusalReachesLaunchOutput(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	script := `import json, os, socket, struct
record = {"version":1,"service":"diagnostic-fixture","attempt":os.environ["YOLO_HOST_SERVICE_ATTEMPT"],"class":"configuration","reason":"The requested credential scope is missing.","remedy":"Set the credential scope in user config."}
data = json.dumps(record, separators=(",", ":")).encode()
s = socket.socket(fileno=3)
s.sendall(struct.pack(">I", len(data)) + data)
`
	manifest := `{
		"name":"diagnostic-fixture", "default_enabled":true, "transport":"none",
		"settings":{"value":{"type":"string","scope":"user","default":"valid"}},
		"host_daemon":{"cmd":["python3","{loophole_dir}/daemon.py","--settings","{settings}"],
			"settings_check":["/bin/test","-s","{settings}"],
			"publishes":"socket","startup_reason":true}
	}`
	p := writeRealLoopholePack(t, "fixture", "diagnostic-fixture", manifest)
	program := filepath.Join(p.Root, "loopholes", "diagnostic-fixture", "daemon.py")
	if err := os.WriteFile(program, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	startingLoopholePacks(p)

	logPath := filepath.Join(home, ".local", "share", "yolo-jail", "logs", "host-service-diagnostic-fixture.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("OLD CONTRADICTORY FAILURE: unrelated historic attempt\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cname := "yolo-startup-diagnostic-" + t.Name()
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	var output bytes.Buffer
	o := &Options{}
	fillDefaults(o)
	o.Stderr, o.Stdout = &output, &output
	o.PathExists = func(string) bool { return false }
	o.ServiceReadyTimeout = 250 * time.Millisecond
	loopCfg := jsonx.NewOrderedMap()
	settings := jsonx.NewOrderedMap()
	settings.Set("value", "fixture-only")
	entry := jsonx.NewOrderedMap()
	entry.Set("enabled", true)
	entry.Set("settings", settings)
	loopCfg.Set("diagnostic-fixture", entry)
	cfg := newConfig("loopholes", loopCfg)
	set := loopholes.NewHostSet(loopCfg)
	allow := func(name string) bool { return name == "diagnostic-fixture" }
	o.discloseSettingsCheckHostExec([]*packload.Pack{p}, set, cfg, allow)
	if !o.prepareLoopholeSettingsForStart(set, cfg, allow) {
		t.Fatalf("generic fixture settings check refused: %v", o.startupRefusal)
	}
	handles, refused := o.startLoopholesDisclosed(cname, "podman", cfg, []*packload.Pack{p}, nil)
	if refused == nil || refused.startup == nil {
		t.Fatalf("selected service refusal was not returned through the start boundary: handles=%v", handles)
	}
	output.WriteString(refused.markup("Refusing this launch"))

	got := output.String()
	for _, want := range []string{"The requested credential scope is missing.", "Set the credential scope in user config."} {
		if !strings.Contains(got, want) {
			t.Errorf("launch output lacks attempt-specific %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "OLD CONTRADICTORY FAILURE") {
		t.Fatalf("a historical shared log changed the current launch result:\n%s", got)
	}
}

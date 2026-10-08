package run

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/shquote"
)

func TestHostDoorwaysStartPropagatesSettingsRefusalBeforeStartingDoorway(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	packRoot := t.TempDir()
	module := filepath.Join(packRoot, "loopholes", "doorway-refusal")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(packRoot, "pack.json"), []byte(`{"name":"fixture-pack","contributes":[{"kind":"loophole","from":"loopholes/doorway-refusal"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(packRoot, "fixture-pack")
	if len(problems) != 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	manifest := fmt.Sprintf(`{
  "name": "doorway-refusal",
  "default_enabled": true,
  "settings": {"profile": {"type": "string", "scope": "user", "default": "default-profile"}},
  "host_daemon": {
    "cmd": [%q, "-front-upstream-child", "line", "{socket}", "--settings", "{settings}"],
    "settings_check": [%q, "-settings-refusal-child", "{settings}"],
    "publishes": "socket"
  }
}`, os.Args[0], os.Args[0])
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	set := loopholes.NewSet(loopholes.DiscoverOptions{
		PackModules: []loopholes.PackModule{{Dir: module, HostExecApproved: true}},
	})
	plan := &launchservice.Plan{Declared: launchservice.Declared{Service: "doorway-refusal", Pack: "fixture"}}
	d := &HostDoorways{plans: []*launchservice.Plan{plan}, set: set, packs: []*packload.Pack{pack}}
	settings := jsonx.NewOrderedMap()
	settings.Set("profile", "sentinel-profile")
	entry := jsonx.NewOrderedMap()
	entry.Set("settings", settings)
	loopCfg := jsonx.NewOrderedMap()
	loopCfg.Set("doorway-refusal", entry)
	cfg := newConfig("loopholes", loopCfg)
	var output bytes.Buffer
	startCalled := false
	_, stop, _, err := d.Start(cfg, t.TempDir(), "pi", &output,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			startCalled = true
			return nil, errors.New("fixture must not start after settings refusal")
		})
	if stop != nil {
		stop()
	}
	if err == nil || startCalled {
		t.Fatalf("host doorway started despite refusing settings: err=%v startCalled=%v", err, startCalled)
	}
	for _, want := range []string{"fixture-pack", "doorway-refusal", "settings validator", "The AWS profile is not usable.", "Run aws sso login --profile <profile>."} {
		if !strings.Contains(output.String()+err.Error(), want) {
			t.Errorf("host doorway refusal lacks %q: output=%s err=%v", want, output.String(), err)
		}
	}
	// A refusal at Start's own preflight comes before the daemon's exec disclosure, which names
	// a daemon this launch will not start. Without that preflight the lower one in
	// startLoopholesMatching still refuses, but only after this disclosure: the line is what
	// tells the two apart.
	if strings.Contains(output.String(), "This launch runs pack code on your machine") {
		t.Errorf("the refused doorway start disclosed the daemon it will not run:\n%s", output.String())
	}
	for _, secret := range []string{"sentinel-profile", "-settings-refusal-child", "{settings}"} {
		if strings.Contains(output.String()+err.Error(), secret) {
			t.Errorf("host doorway refusal disclosed validator input/argv %q: output=%s err=%v", secret, output.String(), err)
		}
	}
}

type validatorAnnouncementBuffer struct {
	bytes.Buffer
	marker string
}

func (w *validatorAnnouncementBuffer) Write(p []byte) (int, error) {
	if strings.Contains(string(p), "settings validator") {
		if err := os.WriteFile(w.marker, []byte("announced"), 0o600); err != nil {
			return 0, err
		}
	}
	return w.Buffer.Write(p)
}

func TestSettingsCheckArgvChangeReachesLaunchFootprintConsumer(t *testing.T) {
	const oldArgv = `"settings_check": ["/bin/sh","-c","true","{settings}"]`
	const newArgv = `"settings_check": ["/bin/sh","-c","validate current bytes","{settings}"]`
	p := writeRealLoopholePack(t, "validator-footprint", "footprint-validator",
		`{"name":"footprint-validator","description":"fixture","transport":"none",`+
			`"default_enabled":true,"settings":{"profile":{"type":"string","scope":"user","default":"default-profile"}},`+
			`"host_daemon":{"cmd":["/bin/true","{settings}"],"settings_check": ["/bin/sh","-c","true","{settings}"],"publishes":"socket"},`+
			`"doctor_cmd":["/bin/true"]}`)
	manifestPath := filepath.Join(p.Root, "loopholes", "footprint-validator", "manifest.jsonc")
	render := func() string {
		var out bytes.Buffer
		o := &Options{Stderr: &out}
		fillDefaults(o)
		o.notePackHostExec([]*packload.Pack{p}, everyLoopholeStarts)
		return out.String()
	}
	before := render()
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(string(data), oldArgv, newArgv, 1)
	if updated == string(data) {
		t.Fatalf("fixture validator argv was not found in %s", manifestPath)
	}
	if err := os.WriteFile(manifestPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	after := render()
	oldClaim := shquote.Join([]string{"/bin/sh", "-c", "true", "{settings}"})
	newClaim := shquote.Join([]string{"/bin/sh", "-c", "validate current bytes", "{settings}"})
	if !strings.Contains(before, oldClaim) || !strings.Contains(after, newClaim) {
		t.Fatalf("actual launch footprint consumer did not render each validator argv: before=%q after=%q", before, after)
	}
	if strings.Replace(before, oldClaim, "<settings-check>", 1) != strings.Replace(after, newClaim, "<settings-check>", 1) {
		t.Fatalf("changing only settings_check changed more than that claim at the launch consumer:\nbefore=%s\nafter=%s", before, after)
	}
}

func TestHostDoorwaysAnnouncesValidatorThenKeepsDaemonDisclosure(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	root := t.TempDir()
	module := filepath.Join(root, "loopholes", "doorway-valid")
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	announced := filepath.Join(t.TempDir(), "validator-announced")
	validated := filepath.Join(t.TempDir(), "validator-ran")
	validator := `grep -q current-profile "$1" && test -f ` + shquote.Quote(announced) + ` && printf x >> ` + shquote.Quote(validated)
	manifest := fmt.Sprintf(`{
  "name":"doorway-valid", "default_enabled":true,
  "settings":{"profile":{"type":"string","scope":"user","default":"default-profile"}},
  "host_daemon":{
    "cmd":[%q,"-front-upstream-child","line","{socket}","--settings","{settings}"],
    "settings_check":["/bin/sh","-c",%q,"validator","{settings}"],
    "publishes":"socket"
  }
}`, os.Args[0], validator)
	if err := os.WriteFile(filepath.Join(module, "manifest.jsonc"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"name":"fixture-pack","contributes":[{"kind":"loophole","from":"loopholes/doorway-valid"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	pack, problems := packload.LoadDir(root, "fixture-pack")
	if len(problems) != 0 {
		t.Fatalf("fixture pack: %v", problems)
	}
	packs := []*packload.Pack{pack}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: packLoopholeModules(packs)})
	plan := &launchservice.Plan{Declared: launchservice.Declared{Service: "doorway-valid", Pack: "fixture-pack"}}
	d := &HostDoorways{plans: []*launchservice.Plan{plan}, set: set, packs: packs}
	cfg := newConfig("loopholes", newConfig("doorway-valid", func() *jsonx.OrderedMap {
		entry := jsonx.NewOrderedMap()
		entry.Set("settings", newConfig("profile", "current-profile"))
		return entry
	}()))
	output := &validatorAnnouncementBuffer{marker: announced}
	started := false
	_, stop, _, err := d.Start(cfg, t.TempDir(), "pi", output,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			started = true
			return nil, errors.New("stop after confirming the service disclosures")
		})
	if stop != nil {
		stop()
	}
	if err == nil || !started {
		t.Fatalf("doorway start did not reach its post-service boundary: started=%v err=%v\n%s", started, err, output.String())
	}
	if data, readErr := os.ReadFile(validated); readErr != nil || string(data) != "x" {
		t.Fatalf("admitted validator did not run exactly once on the current start: data=%q err=%v", data, readErr)
	}
	out := output.String()
	for _, want := range []string{"fixture-pack", "doorway-valid", "settings validator", "This launch runs pack code on your machine", "/bin/sh -c"} {
		if !strings.Contains(out, want) {
			t.Errorf("launch output lacks %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "settings validator") > strings.Index(out, "This launch runs pack code on your machine") {
		t.Errorf("validator preflight was not announced before complete daemon disclosure:\n%s", out)
	}
}

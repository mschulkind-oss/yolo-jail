package run

// hostdoorwaysets_test.go pins withoutClientlessPlatforms over an ACTIVE SET
// (docs/design/active-provider-sets.md; the ordered list of profiles one agent runs on, a term
// that doc coins): the doorways a `yolo host` launch plans (PlanHostDoorways) are asked over the
// agent's whole set, so an entry on a platform the agent has no client of is blanked, as a
// primary on one is (HS-D23), and the entries it does have a client of keep asking.
//
// It also pins hostdoorwaysets.go's two answers for `yolo host apply`'s notch line: the
// doorways HostDoorwayLoopholes names are the ones PlanHostDoorways composes, its two stated
// differences aside (the switch and the selection filter), and HostInlineLoopholes names an
// enabled inline loophole and nothing else.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestWithoutClientlessPlatformsBlanksAClientlessEntryOfASet(t *testing.T) {
	// pi binds aws-bedrock (its own client); copilot binds none.
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "copilot"), officialPack(t, "bedrock")}
	sel := packload.GateSelection{
		Profiles:     map[string]string{"pi": "zai", "copilot": "zai"},
		Sets:         map[string][]string{"pi": {"zai", "bedrock"}, "copilot": {"zai", "bedrock"}},
		SetPlatforms: map[string][]string{"pi": {"", "aws-bedrock"}, "copilot": {"", "aws-bedrock"}},
	}
	out, clientless := withoutClientlessPlatforms(packs, sel)
	if !reflect.DeepEqual(clientless, map[string]string{"copilot": "aws-bedrock"}) {
		t.Errorf("clientless = %v, want copilot's aws-bedrock alone", clientless)
	}
	if got := out.SetPlatforms["pi"]; !reflect.DeepEqual(got, []string{"", "aws-bedrock"}) {
		t.Errorf("pi's entries = %v, want its Bedrock entry kept: pi has a client of it", got)
	}
	if got := out.SetPlatforms["copilot"]; !reflect.DeepEqual(got, []string{"", ""}) {
		t.Errorf("copilot's entries = %v, want its Bedrock entry blanked", got)
	}
	if !reflect.DeepEqual(out.Sets, sel.Sets) {
		t.Errorf("the sets themselves must be kept: %v", out.Sets)
	}
}

// doorwayPacks is pi on Bedrock's selected set: pi, the bedrock provider pack and the two packs
// it and pi join (aws-auth, openai-auth), each shipping one credential loophole with a doorway.
func doorwayPacks(t *testing.T) []*packload.Pack {
	t.Helper()
	return []*packload.Pack{officialPack(t, "pi"), officialPack(t, "bedrock"),
		officialPack(t, "aws-auth"), officialPack(t, "openai-auth")}
}

// loopholesConfig is a user config whose `loopholes` block is the given JSON object.
func loopholesConfig(t *testing.T, block string) *jsonx.OrderedMap {
	t.Helper()
	v, err := jsonx.Decode([]byte(block))
	if err != nil {
		t.Fatal(err)
	}
	return newConfig("loopholes", v)
}

// TestHostDoorwayLoopholesIsThePlansOwnSet: for every doorway a launch's selection asks the plan
// for, the apply's notch line names it as delivered at launch exactly when PlanHostDoorways
// composes it. A plan that runs no process (opens=false) still composes them, and says "only a
// launch that runs the agent opens" of exactly those; every other reason means the doorway was not
// composed. The two differences hostdoorwaysets.go's header states are pinned below it.
func TestHostDoorwayLoopholesIsThePlansOwnSet(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	packs := doorwayPacks(t)
	sel := packload.GateSelection{Profiles: map[string]string{"pi": "bedrock"},
		Platforms: map[string]string{"pi": "aws-bedrock"}}
	const composedWhy = "which only a launch that runs the agent opens"

	cfg := loopholesConfig(t, `{"aws-auth": {"enabled": true}}`)
	plan, err := PlanHostDoorways(cfg, packs, sel, false, "yolo host -- pi")
	if err != nil {
		t.Fatal(err)
	}
	got := HostDoorwayLoopholes(cfg, packs)
	asked := 0
	for name, why := range plan.notOpened {
		asked++
		if composed := strings.HasPrefix(why, composedWhy); composed != got[name] {
			t.Errorf("%s: the plan composes it = %v (%q), HostDoorwayLoopholes names it = %v — "+
				"the notch line and the launch disagree", name, composed, why, got[name])
		}
	}
	if asked == 0 || !got["aws-auth"] {
		t.Fatalf("fixture bug: pi on Bedrock asked the plan for no doorway, or aws-auth's is not "+
			"one (%v, %v)", plan.notOpened, got)
	}
	// THE SELECTION FILTER is one difference: codex's refresh adapter is a doorway too, though no
	// selection asks the plan for it (its pointer is ungated, HS-D22); `yolo host -- codex`
	// serves that URL from the managed launch instead (HS-D20).
	if !got["openai-auth-broker"] {
		t.Errorf("openai-auth-broker's doorway is not named: %v", got)
	}
	if len(got) != 2 {
		t.Errorf("want exactly the two credential doorways the shipped packs declare, got %v", got)
	}

	// The SWITCH is the other difference, on purpose: a loophole the user left off is still one
	// the host notch delivers once it is on, and the launch that does not open it names the
	// switch. The plan over the same config says disabled.
	off := loopholesConfig(t, `{"aws-auth": {"enabled": false}}`)
	plan, err = PlanHostDoorways(off, packs, sel, false, "yolo host -- pi")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.notOpened["aws-auth"], "is disabled") {
		t.Fatalf("fixture bug: the plan over a disabled aws-auth says %q", plan.notOpened["aws-auth"])
	}
	if !HostDoorwayLoopholes(off, packs)["aws-auth"] {
		t.Error("a disabled aws-auth is named as having no doorway at the host, which every " +
			"notch's switch, not the host's, decides")
	}
	if !HostDoorwayLoopholes(newConfig(), packs)["aws-auth"] {
		t.Error("aws-auth off by default (its manifest's default_enabled) is named as having no doorway")
	}
}

// TestHostDoorwayLoopholesAppliesTheAdmissionGate: a doorway whose pack yolo does not ship is
// refused by launchservice.AdmitDoorways (OQ-HS4, HS-D15), so the host opens none for it and the
// notch line must not name it as delivered.
func TestHostDoorwayLoopholesAppliesTheAdmissionGate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	root := t.TempDir()
	mod := filepath.Join(root, "loopholes", "acme-cred")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(`{
		"name": "acme-cred", "description": "d", "version": 1, "default_enabled": true,
		"transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "acme", "--socket", "{socket}"],
		                "publishes": "socket", "scope": "host"},
		"jail_daemon": {"cmd": ["yolo-jaild", "acme-adapter", "--listen", "{listen}"],
		                "listen": "127.0.0.1:1999", "restart": "on-failure", "caller_token": true,
		                "host_cmd": ["yolo", "internal", "daemon", "acme-adapter", "--listen", "{listen}"]}}`),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"contributes":[
		{"kind":"loophole","from":"loopholes/acme-cred"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("the local pack fixture does not load: %v", probs)
	}
	packs := []*packload.Pack{p}
	set := loopholes.NewSet(loopholes.DiscoverOptions{PackModules: packLoopholeModules(packs)})
	if lp, ok := set.Lookup("acme-cred"); !ok || lp.JailDaemon == nil || len(lp.JailDaemon.HostCmd) == 0 {
		t.Fatalf("fixture bug: the local loophole did not load as a doorway (%v, %+v)", ok, lp)
	}
	if got := HostDoorwayLoopholes(newConfig(), packs); got["acme-cred"] {
		t.Errorf("a local pack's doorway is named as delivered at the host, but only an official "+
			"pack's opens there (OQ-HS4): %v", got)
	}
}

// TestHostInlineLoopholesNamesTheEnabledOnesWithADaemon: an inline loophole is a config entry
// that names no selected pack's loophole and declares a `command`. One without a command runs
// nothing (an override of a loophole nothing ships), a disabled one runs nothing, and an entry
// naming a pack's loophole is that loophole's override, so none of the three is named.
func TestHostInlineLoopholesNamesTheEnabledOnesWithADaemon(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	cfg := loopholesConfig(t, `{
		"mydaemon": {"command": ["mydaemon", "--socket", "{socket}"]},
		"second":   {"command": ["second-daemon"], "enabled": true},
		"sleeping": {"command": ["sleeper"], "enabled": false},
		"switch":   {"enabled": true},
		"aws-auth": {"enabled": true}}`)
	got := HostInlineLoopholes(cfg, doorwayPacks(t))
	if want := []string{"mydaemon", "second"}; !reflect.DeepEqual(got, want) {
		t.Errorf("HostInlineLoopholes = %v, want %v", got, want)
	}
	if got := HostInlineLoopholes(newConfig(), doorwayPacks(t)); got != nil {
		t.Errorf("a config with no loopholes block names %v", got)
	}
}

// TestTheApplysDoorwaySurveyAndALaunchsPlanDiscoverAtOnce is the production pair behind the
// loopholes package's locked once rule (warnf): `yolo host --` with `host_apply_on_launch` on runs
// the apply's observe pass, HostDoorwayLoopholes included, on a goroutine the launch gate abandons
// after its budget, and the launch carries on into PlanHostDoorways. Both discover, and a module
// that does not load makes both warn. `go test -race` is this test's detector: before the lock it
// reported the two warnf calls reading and writing one map, which the Go runtime may answer by
// killing the launch ("concurrent map read and map write").
func TestTheApplysDoorwaySurveyAndALaunchsPlanDiscoverAtOnce(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	packs := doorwayPacks(t)
	removed := false
	for _, p := range packs {
		if p.Name == "openai-auth" {
			// A module dir that is gone, so discovery warns on both goroutines.
			if err := os.RemoveAll(filepath.Join(p.Root, "loopholes", "openai-auth-broker")); err != nil {
				t.Fatal(err)
			}
			removed = true
		}
	}
	if !removed {
		t.Fatal("fixture bug: no openai-auth pack to break, so neither discovery warns")
	}
	sel := packload.GateSelection{Profiles: map[string]string{"pi": "bedrock"},
		Platforms: map[string]string{"pi": "aws-bedrock"}}
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_ = HostDoorwayLoopholes(nil, packs)
	}()
	go func() {
		defer wg.Done()
		<-start
		if d, err := PlanHostDoorways(nil, packs, sel, false, "yolo host --"); err == nil {
			d.Release()
		}
	}()
	close(start)
	wg.Wait()
}

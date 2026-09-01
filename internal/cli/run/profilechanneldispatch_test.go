package run

// profilechanneldispatch_test.go pins the profile/provider CHANNEL at the backend
// dispatch — the third B-0 hoist, after the pack trees (packstagedispatch_test.go) and
// the launch flags (launchflagsdispatch_test.go, profilelaunch_test.go).
//
// The channel used to be composed entirely inside the container arm: the profile table
// and the pack env fold in assembleRunCmd, the composed provider table and the env_shape
// vars in commonEnvBlock. The macos-user branch returns before reaching any of it, so
// that backend parsed and validated `-p zai` (checkProfileTargets sits in stagePacks,
// above the dispatch) and then delivered NOTHING — no variant env, no env_shape relay, no
// provider table for its derives, no credential pre-flight. Nothing failed; the launch
// just ran with no profile.
//
// Every test here therefore drives Run() and reads what the macos-user HANDLER is handed,
// or whether it is handed anything at all. A test on the composer (composePackChannel) or
// on packload's fold would stay green if Run stopped passing the result to the backend,
// which is exactly the defect. The container half of the channel is pinned at the argv by
// providersenv_test.go, providershapeenv_test.go and zaipack_test.go; the launches here
// are the SAME acceptance story (`-p zai` over the shipped claude + zai packs), driven
// through the other arm.

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// zaiNativeLaunch drives Run() to the macos-user arm with the shipped claude + zai packs
// selected and `-p zai`, and hands the caller the launch env the backend receives. The
// invoking shell carries ZAI_API_KEY when withKey is set, which is the channel the env
// shape's {key} placeholder may draw on — the same relay the container argv gets.
func zaiNativeLaunch(t *testing.T, withKey bool) (*Options, *bytes.Buffer) {
	t.Helper()
	home := packHome(t)
	writeUserPacks(t, home, `["claude", "zai"]`)
	ws := t.TempDir()

	var stdout, stderr bytes.Buffer
	o := dispatchOptions(t, ws, "macos-user", &stdout, &stderr, nil)
	o.Args = []string{"claude"}
	o.ProfileName = "zai"
	o.Getenv = func(name string) string {
		if name == "YOLO_RUNTIME" {
			return "macos-user"
		}
		if name == "ZAI_API_KEY" && withKey {
			return "tok-shell"
		}
		return ""
	}
	return o, &stderr
}

// envAt reads one variable out of a launch-env map, "" when absent.
func envAt(env *jsonx.OrderedMap, key string) string {
	if env == nil {
		return ""
	}
	v, _ := env.Get(key)
	s, _ := v.(string)
	return s
}

// The macos-user arm must receive the channel the launch composed: the env_shape alias
// for the protocol the profiled agent speaks (with the credential relayed from the
// invoking shell) and the two wire tables its bootstrap renders from.
func TestProfileChannelReachesTheMacosUserBackend(t *testing.T) {
	o, stderr := zaiNativeLaunch(t, true)

	var got *jsonx.OrderedMap
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ bool,
		packEnv *jsonx.OrderedMap) int {
		got = packEnv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstderr:\n%s", rc, stderr.String())
	}
	if got == nil {
		t.Fatal("Run() never handed the macos-user backend a launch environment")
	}
	if gotAt := envAt(got, "YOLO_PACK_PROFILES"); gotAt != `{"claude": "zai"}` {
		t.Errorf("macos-user launch env YOLO_PACK_PROFILES = %q, want the global -p folded "+
			"onto the CLI the selected agent pack installs", gotAt)
	}
	for key, want := range map[string]string{
		"ANTHROPIC_BASE_URL":   "https://api.z.ai/api/anthropic",
		"ANTHROPIC_AUTH_TOKEN": "tok-shell",
	} {
		if gotAt := envAt(got, key); gotAt != want {
			t.Errorf("macos-user launch env %s = %q, want %q — the env_shape relay never "+
				"reached this backend, and nothing downstream recovers it: the sandbox env "+
				"is baked onto the launch argv", key, gotAt, want)
		}
	}
	providers := envAt(got, "YOLO_PROVIDERS")
	if !strings.Contains(providers, `"zai"`) ||
		!strings.Contains(providers, `"api_key_env_name": "ZAI_API_KEY"`) {
		t.Errorf("macos-user launch env YOLO_PROVIDERS = %s, want the composed zai entry — "+
			"its derives would see no provider at all", providers)
	}
}

// The credential pre-flight must run on this arm too. It used to live only in
// runContainer, below the return, so a native launch with a provider pack and no key
// started a sandbox that failed its first API call and said nothing. Refusal is asserted
// at the dispatch — the handler must never be reached — because that is the property the
// container arm has always had and the one this backend was missing.
func TestProfileChannelPreflightRefusesTheMacosUserLaunch(t *testing.T) {
	o, stderr := zaiNativeLaunch(t, false)

	reached := false
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ bool,
		_ *jsonx.OrderedMap) int {
		reached = true
		return 0
	}
	if rc := Run(*o); rc != 1 {
		t.Fatalf("Run() = %d, want 1: a selected pack whose credential never arrived must "+
			"refuse the native launch too\nstderr:\n%s", rc, stderr.String())
	}
	if reached {
		t.Error("the refused launch still reached the macos-user handler — the pre-flight " +
			"must run BEFORE the backend is dispatched, not after it started")
	}
	if got := stderr.String(); !strings.Contains(got, "ZAI_API_KEY") {
		t.Errorf("the refusal must name the variable it is missing:\n%s", got)
	}

	// The escape hatch is the same one the container arm names, and it is a loud
	// continuation rather than a second refusal.
	o, stderr = zaiNativeLaunch(t, false)
	o.Getenv = func(name string) string {
		if name == "YOLO_RUNTIME" {
			return "macos-user"
		}
		if name == paths.AllowMissingProvidersEnv {
			return "1"
		}
		return ""
	}
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ bool,
		_ *jsonx.OrderedMap) int {
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d with the hatch set, want 0\nstderr:\n%s", rc, stderr.String())
	}
	if got := stderr.String(); !strings.Contains(got, paths.AllowMissingProvidersEnv) {
		t.Errorf("the hatch must say what it is suppressing:\n%s", got)
	}
}

// An unprofiled launch carries the catalog and NO composed env: the packs still ship
// their provider facts (the derives read the table either way), but presence is not
// selection, so no env_shape var may appear and the profile table must be empty. The
// container half of this claim is TestZaiPackComposesNoEnvWithoutTheProfile.
func TestUnprofiledNativeLaunchStillCarriesTheEmptyWireTables(t *testing.T) {
	o, stderr := zaiNativeLaunch(t, true)
	o.ProfileName = "" // the packs, no selection

	var got *jsonx.OrderedMap
	o.MacosUserRun = func(_ *jsonx.OrderedMap, _ string, _, _ []string, _, _ string, _ bool,
		packEnv *jsonx.OrderedMap) int {
		got = packEnv
		return 0
	}
	if rc := Run(*o); rc != 0 {
		t.Fatalf("Run() = %d, want 0\nstderr:\n%s", rc, stderr.String())
	}
	if gotAt := envAt(got, "YOLO_PACK_PROFILES"); gotAt != "{}" {
		t.Errorf("an unprofiled launch must carry an empty profile table, got %q", gotAt)
	}
	if gotAt := envAt(got, "YOLO_PROVIDERS"); !strings.Contains(gotAt, `"zai"`) {
		t.Errorf("an unprofiled launch still carries the composed provider catalog the "+
			"derives read, got %q", gotAt)
	}
	if envAt(got, "ANTHROPIC_AUTH_TOKEN") != "" {
		t.Error("an unprofiled launch must compose no env_shape vars — presence is not " +
			"selection, and a quiet reroute is the failure §7 names")
	}
}

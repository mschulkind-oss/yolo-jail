package run

// servedaddresses_test.go pins the jail notches' half of served-address composition
// (docs/plans/notch-convergence.md §4 item 2): the channel composes only what this launch's
// notch serves. A container launch composes the bridge's adapter address and delivers codex's
// refresh URL. macos-user declines the bridge's jail daemon, so a bridged pairing runs through the
// service's host half (macosuserservices_test.go) or refuses naming why; and since OQ-DP8/DP9 its
// guest runs the OpenAI adapter, so codex's refresh URL is delivered there too, at a picked port. MEASURED
// before this (plan §3.3 C1): macos-user composed claude on cerebras against 127.0.0.1:8214,
// which nothing on it serves. Through the real composePackChannel with the runtime Run
// resolved, so deleting servedDaemons' use there fails it.

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// A service macos-user cannot run refuses the pairing, naming why: here a wire-bridge pack that is
// neither the one yolo ships nor a local one, a fetched pack, whose host half never runs (OQ-HS4).
// The shipped pack's host half is planned and composed instead (macosuserservices_test.go), and so
// is a local copy's (HS-D27), which the second half pins.
func TestMacosUserRefusesAPairingThroughAServiceItCannotStart(t *testing.T) {
	packs := bridgedPacks(t)
	o, cfg, channel, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(), selectCerebras)
	shared, _ := deliveredFiles(t, channel)
	if !strings.Contains(shared, "127.0.0.1:8214") {
		t.Fatalf("the container launch lost the bridge's adapter address, so this proves nothing:\n%s", shared)
	}

	packs[2].Official = false
	o.runtime = "macos-user"
	o.launchServices = nil
	_, err := o.composePackChannel(cfg, packs, cerebrasKey())
	var unserved *packload.UnservedAdapterError
	if !errors.As(err, &unserved) {
		t.Fatalf("macos-user composed claude on cerebras through a bridge it cannot start (err %v)", err)
	}
	for _, want := range []string{"nothing serves it here", `cannot start the "wire-bridge" service's host half`,
		"its pack was fetched", "not one yolo ships"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}

	// The same pack as a LOCAL one, the user's own copy: its host half is planned, and claude is
	// composed against the port this launch picked for it.
	packs[2].Local = true
	o.launchServices = nil
	channel, err = o.composePackChannel(cfg, packs, cerebrasKey())
	if err != nil {
		t.Fatalf("macos-user refused a pairing through a local pack's bridge: %v", err)
	}
	if len(o.launchServices) != 1 || o.launchServices[0].Service != "wire-bridge" || !o.launchServices[0].Local {
		t.Fatalf("launch services = %+v, want the local pack's wire bridge", o.launchServices)
	}
	picked := o.launchServices[0].Moved["127.0.0.1:8214"]
	shared, _ = deliveredFiles(t, channel)
	if picked == "" || strings.Contains(shared, "127.0.0.1:8214") {
		t.Errorf("claude was not moved off the declared 8214 to the local bridge's picked port (%q)", picked)
	}
}

// ON MACOS-USER CODEX'S REFRESH POINTER IS SERVED (OQ-DP8/DP9): the guest runs the OpenAI
// refresh adapter, confined, so the channel delivers CODEX_REFRESH_TOKEN_URL_OVERRIDE — at a
// port this launch PICKED, because the sandbox shares the Mac's loopback and a second launch
// would otherwise find the declared 1460 held — and the payload hands the adapter that same
// port. Until 2026-09-28 the pointer was withheld and named here (NC-D16). Through the real
// composePackChannel and jailDaemonsFor, so deleting servedDaemons' split or the settle fails it.
func TestMacosUserServesCodexsRefreshPointerAtThePortItsAdapterBinds(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "codex"), officialPack(t, "openai-auth")}
	withModules := func(o *Options, _ *jsonx.OrderedMap) {
		loopholes.SetPackModules(packLoopholeModules(packs))
	}
	o, cfg, channel, _ := attachFixture(t, currentJailEnv, packs, emptyEnv(), withModules)
	if _, ok := channel.scope.DeliveredPackEnv("CODEX_REFRESH_TOKEN_URL_OVERRIDE"); !ok {
		t.Fatal("a container launch running the OpenAI adapter lost codex's refresh URL")
	}

	o.runtime = "macos-user"
	o.served = servedAddressState{}
	macos, err := o.composePackChannel(cfg, packs, emptyEnv())
	if err != nil {
		t.Fatal(err)
	}
	v, ok := macos.scope.DeliveredPackEnv("CODEX_REFRESH_TOKEN_URL_OVERRIDE")
	if !ok {
		t.Fatal("macos-user withholds CODEX_REFRESH_TOKEN_URL_OVERRIDE, though its guest runs the adapter")
	}
	var adapter string
	for _, s := range o.jailDaemonsFor(cfg, "macos-user", packs) {
		if s.Name == "openai-auth-broker" {
			adapter = s.Listen
		}
	}
	if adapter == "" || adapter == "127.0.0.1:1460" {
		t.Fatalf("the adapter's listen address %q was not moved off the declared port on a "+
			"shared loopback", adapter)
	}
	if !strings.Contains(v, "http://"+adapter+"/") {
		t.Errorf("codex is pointed at %s, the adapter binds %s", v, adapter)
	}
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.noteCredentialScope(macos)
	if strings.Contains(stderr.String(), "CODEX_REFRESH_TOKEN_URL_OVERRIDE") {
		t.Errorf("the launch still names the served pointer as withheld:\n%s", stderr.String())
	}
}

// viaFixture is pi routed through the wire bridge by a user profile: pi, zai and the bridge
// selected, `pz` = zai via wire-bridge, active for pi. userEnv carries zai's key.
func viaFixture(t *testing.T) ([]*packload.Pack, *jsonx.OrderedMap, func(*Options, *jsonx.OrderedMap)) {
	t.Helper()
	packs := []*packload.Pack{officialPack(t, "pi"), officialPack(t, "zai"), officialPack(t, "wire-bridge")}
	userEnv := jsonx.NewOrderedMap()
	userEnv.Set("ZAI_API_KEY", "zai-test-key")
	tune := func(o *Options, _ *jsonx.OrderedMap) {
		writeUserConfig(t, os.Getenv("HOME"), `{"profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}}}`)
		o.UseProfiles = map[string]string{"pi": "pz"}
	}
	return packs, userEnv, tune
}

// ON MACOS-USER A VIA IS CLEARED AND NAMED (notch convergence item 2, NC-D16). A container
// launch runs the wire bridge, so pi's `pz` keeps its via; macos-user's guest declines the
// bridge's jail daemon, so the channel clears the via, pi keeps its own client, and the launch names the profile. Through
// the real composePackChannel, so deleting its ViaServedAt call fails this.
func TestMacosUserClearsAnUnservedViaAndNamesTheProfile(t *testing.T) {
	packs, userEnv, tune := viaFixture(t)
	o, cfg, channel, _ := attachFixture(t, currentJailEnv, packs, userEnv, tune)
	if channel.resolvedProfiles["pz"].ViaBase == "" {
		t.Fatalf("the container launch lost pz's via, so this proves nothing: %+v", channel.resolvedProfiles)
	}
	if len(channel.unservedVias) != 0 {
		t.Fatalf("the container launch serves the bridge but named %v unserved", channel.unservedVias)
	}

	o.runtime = "macos-user"
	macos, err := o.composePackChannel(cfg, packs, userEnv)
	if err != nil {
		t.Fatal(err)
	}
	if base := macos.resolvedProfiles["pz"].ViaBase; base != "" {
		t.Errorf("macos-user kept pz's via %q, a bridge no daemon of its serves", base)
	}
	if strings.Join(macos.unservedVias, ",") != "pz" {
		t.Errorf("unservedVias = %v, want [pz]", macos.unservedVias)
	}
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.noteCredentialScope(macos)
	for _, want := range []string{"Not set at this notch", "pz"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the launch does not name %s:\n%s", want, stderr.String())
		}
	}
}

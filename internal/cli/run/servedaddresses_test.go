package run

// servedaddresses_test.go pins the jail notches' half of served-address composition
// (docs/plans/notch-convergence.md §4 item 2): the channel composes only what this launch's
// notch serves. A container launch composes the bridge's adapter address and delivers codex's
// refresh URL; macos-user, which runs no jail daemon, composes neither at a jail daemon's address:
// a bridged pairing runs through the service's host half (macosuserservices_test.go) or refuses
// naming why, and the pointer is withheld and named. MEASURED
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
// not the one yolo ships, whose host half never runs (OQ-HS4). The shipped pack's host half is
// planned and composed instead (macosuserservices_test.go).
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
		"not one yolo ships"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not say %q: %v", want, err)
		}
	}
}

func TestMacosUserWithholdsCodexsRefreshPointerAndSaysSo(t *testing.T) {
	packs := []*packload.Pack{officialPack(t, "codex"), officialPack(t, "openai-auth")}
	withModules := func(o *Options, _ *jsonx.OrderedMap) {
		loopholes.SetPackModules(packLoopholeModules(packs))
	}
	o, cfg, channel, _ := attachFixture(t, currentJailEnv, packs, emptyEnv(), withModules)
	if _, ok := channel.scope.DeliveredPackEnv("CODEX_REFRESH_TOKEN_URL_OVERRIDE"); !ok {
		t.Fatal("a container launch running the OpenAI adapter lost codex's refresh URL")
	}

	o.runtime = "macos-user"
	macos, err := o.composePackChannel(cfg, packs, emptyEnv())
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := macos.scope.DeliveredPackEnv("CODEX_REFRESH_TOKEN_URL_OVERRIDE"); ok {
		t.Errorf("macos-user delivers CODEX_REFRESH_TOKEN_URL_OVERRIDE=%s, a port no daemon of its binds", v)
	}
	var stderr bytes.Buffer
	o.Stderr = &stderr
	o.noteCredentialScope(macos)
	for _, want := range []string{"Not set at this notch", "CODEX_REFRESH_TOKEN_URL_OVERRIDE", `"openai-auth-broker"`} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("the launch does not name %s:\n%s", want, stderr.String())
		}
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
// launch runs the wire bridge, so pi's `pz` keeps its via; macos-user runs no jail daemon, so
// the channel clears the via, pi keeps its own client, and the launch names the profile. Through
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

package run

// servedaddresses_test.go pins the jail notches' half of served-address composition
// (docs/plans/notch-convergence.md §4 item 2): the channel composes only what this launch's
// notch serves. A container launch composes the bridge's adapter address and delivers codex's
// refresh URL; macos-user, which runs no jail daemon, composes neither — a bridged pairing
// refuses naming why, as the host's does, and the pointer is withheld and named. MEASURED
// before this (plan §3.3 C1): macos-user composed claude on cerebras against 127.0.0.1:8214,
// which nothing on it serves. Through the real composePackChannel with the runtime Run
// resolved, so deleting servedDaemons' use there fails it.

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestMacosUserComposesNoBridgedAddressAndRefusesThePairing(t *testing.T) {
	packs := bridgedPacks(t)
	o, cfg, channel, _ := attachFixture(t, currentJailEnv, packs, cerebrasKey(), selectCerebras)
	shared, _ := deliveredFiles(t, channel)
	if !strings.Contains(shared, "127.0.0.1:8214") {
		t.Fatalf("the container launch lost the bridge's adapter address, so this proves nothing:\n%s", shared)
	}

	o.runtime = "macos-user"
	_, err := o.composePackChannel(cfg, packs, cerebrasKey())
	var unserved *packload.UnservedAdapterError
	if !errors.As(err, &unserved) {
		t.Fatalf("macos-user composed claude on cerebras (err %v); it must refuse the pairing only "+
			"the bridge's unserved address resolves", err)
	}
	if !strings.Contains(err.Error(), "nothing serves it here") {
		t.Errorf("the refusal does not say why: %v", err)
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

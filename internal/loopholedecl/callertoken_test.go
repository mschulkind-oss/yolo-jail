package loopholedecl_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// `jail_daemon.caller_token` (docs/plans/notch-convergence.md §2.3, NC-D3): the credential
// adapters that listen on a loopback port declare it, so the launcher mints them a caller
// token; the Claude OAuth terminator, whose client cannot carry one and which authenticates by
// refresh-token match instead, does not.
func TestTheShippedCredentialAdaptersDeclareACallerToken(t *testing.T) {
	for name, want := range map[string]bool{
		"openai-auth-broker":  true,
		"aws-auth":            true,
		"claude-oauth-broker": false,
	} {
		m, err := loopholedecl.Decode(shippedManifest(t, name), "/loopholes/"+name)
		if err != nil {
			t.Fatal(err)
		}
		if m.JailDaemon == nil {
			t.Fatalf("%s declares no jail_daemon", name)
		}
		if m.JailDaemon.CallerToken != want {
			t.Errorf("%s: jail_daemon.caller_token = %v, want %v", name, m.JailDaemon.CallerToken, want)
		}
	}
}

func TestCallerTokenMustBeABoolean(t *testing.T) {
	manifest := func(v string) []byte {
		return []byte(`{"name": "x", "version": 1, "transport": "none", "lifecycle": "spawned",
			"jail_daemon": {"cmd": ["yolo-jaild", "x"], "caller_token": ` + v + `}}`)
	}
	if _, err := loopholedecl.Decode(manifest(`"yes"`), "/loopholes/x"); err == nil ||
		!strings.Contains(err.Error(), "caller_token") {
		t.Errorf("a string caller_token decoded: %v", err)
	}
	m, err := loopholedecl.Decode(manifest(`true`), "/loopholes/x")
	if err != nil {
		t.Fatal(err)
	}
	if !m.JailDaemon.CallerToken {
		t.Error("caller_token: true did not decode")
	}
}

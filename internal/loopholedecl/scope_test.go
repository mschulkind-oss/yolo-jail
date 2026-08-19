package loopholedecl_test

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// hostScopedManifest is a minimal `scope: "host"` declaration, so each test below
// varies exactly the field it is about.
func hostScopedManifest(overrides map[string]any) map[string]any {
	hd := map[string]any{
		"cmd":       []any{"d", "{socket}"},
		"publishes": "socket",
		"scope":     "host",
	}
	for k, v := range overrides {
		if v == nil {
			delete(hd, k)
			continue
		}
		hd[k] = v
	}
	return map[string]any{"name": "scoped", "description": "x", "host_daemon": hd}
}

// TestHostDaemonScopeDefaultsToJail is the whole reason `scope` could be added to a
// shipped schema at all: every manifest that already exists declares nothing, and
// must keep meaning "one daemon per jail, spawned and reaped with it".
//
// The default lives in the DECODER, not at the readers, for `preamble`'s reason —
// that is what makes "no manifest declares anything to keep working" literally true
// rather than true of the one reader somebody checked.
func TestHostDaemonScopeDefaultsToJail(t *testing.T) {
	m, err := decodeMap(t, "scoped", map[string]any{
		"name": "scoped", "description": "x",
		"host_daemon": map[string]any{"cmd": []any{"d", "{endpoint}"}},
	})
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if m.HostDaemon.Scope != loopholedecl.ScopeJail {
		t.Errorf("Scope = %q, want the default %q — a manifest that says nothing must not "+
			"become a host-wide singleton, and must not decode to \"\" either, because the "+
			"run pipeline's ScopeHost comparison would then hide the difference",
			m.HostDaemon.Scope, loopholedecl.ScopeJail)
	}
}

// TestHostDaemonScopeRoundTrips is the anti-overreach control: both declared values
// survive the decode, so the default above is a default rather than the only answer.
func TestHostDaemonScopeRoundTrips(t *testing.T) {
	for _, tc := range []struct{ scope, publishes string }{
		{loopholedecl.ScopeJail, "endpoint"},
		{loopholedecl.ScopeJail, "socket"},
		{loopholedecl.ScopeHost, "socket"},
	} {
		m, err := decodeMap(t, "scoped", map[string]any{
			"name": "scoped", "description": "x",
			"host_daemon": map[string]any{
				"cmd":       []any{"d", "{socket}"},
				"publishes": tc.publishes,
				"scope":     tc.scope,
			},
		})
		if err != nil {
			t.Fatalf("decode of scope=%q publishes=%q: %v", tc.scope, tc.publishes, err)
		}
		if m.HostDaemon.Scope != tc.scope {
			t.Errorf("Scope = %q, want %q", m.HostDaemon.Scope, tc.scope)
		}
	}
}

// TestHostDaemonScopeRefusesAnUnknownValue: the vocabulary is CLOSED, like every
// other enum here. A typo'd scope must not silently select per-jail — that is the
// direction in which the broker gets spawned twice and burns a refresh token.
func TestHostDaemonScopeRefusesAnUnknownValue(t *testing.T) {
	for _, bad := range []string{"machine", "singleton", "Host", "global"} {
		_, err := decodeMap(t, "scoped", hostScopedManifest(map[string]any{"scope": bad}))
		if err == nil {
			t.Fatalf("decode accepted scope=%q", bad)
		}
		if !strings.Contains(err.Error(), "host_daemon.scope") {
			t.Errorf("error does not name the key: %s", err.Error())
		}
		if !strings.Contains(err.Error(), "'host'") || !strings.Contains(err.Error(), "'jail'") {
			t.Errorf("error does not list the accepted values: %s", err.Error())
		}
	}
}

// TestHostScopeRequiresPublishesSocket is the CREDENTIAL rule, refused at load
// rather than documented.
//
// A host-wide daemon serving every jail cannot publish its own endpoint file: an
// endpoint file carries ONE jail's bearer token (svcendpoint mints it per
// publication), so a single publisher either hands every jail the same credential
// or hands all but one of them a credential minted for somebody else. Under
// `publishes: "socket"` the daemon binds one socket and yolo runs a front — and a
// fresh token — per jail, which is the only shape in which "one daemon, N jails"
// and "one credential per jail" are both true.
//
// The DEFAULT is covered as well as the explicit spelling, because the default is
// "endpoint": a manifest that declares `scope: "host"` and nothing else is exactly
// the mistake this refuses.
func TestHostScopeRequiresPublishesSocket(t *testing.T) {
	for _, tc := range []struct {
		name      string
		publishes any
	}{
		{"explicit endpoint", "endpoint"},
		{"omitted (defaults to endpoint)", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// {endpoint} in the argv, because a publishes:"endpoint" daemon names the
			// file it writes — the same argv the refusal has to be reachable through.
			man := hostScopedManifest(map[string]any{
				"publishes": tc.publishes,
				"cmd":       []any{"d", "{endpoint}"},
			})
			_, err := decodeMap(t, "scoped", man)
			if err == nil {
				t.Fatal("decode accepted a host-scoped daemon that publishes its own endpoint; " +
					"every jail would then read one jail's bearer token out of one file")
			}
			if !strings.Contains(err.Error(), "host_daemon.scope") ||
				!strings.Contains(err.Error(), "socket") {
				t.Errorf("error does not name the key and the fix: %s", err.Error())
			}
		})
	}
}

// TestHostDaemonScopeTypoIsUnknown: `scop`, `host_scope`, `singleton` are UNKNOWN
// KEYS, so the strict decode reports them.
//
// Without the key being in hostDaemonKeys this would be the silent failure mode
// instead: a manifest carefully declaring a misspelling of it decodes clean, the
// run pipeline spawns the daemon per jail, and the only symptom is N brokers
// contending for one flock.
func TestHostDaemonScopeTypoIsUnknown(t *testing.T) {
	for _, key := range []string{"scop", "scopes", "host_scope", "singleton"} {
		man := hostScopedManifest(map[string]any{"scope": nil, key: "host"})
		if _, err := decodeMap(t, "scoped", man); err == nil {
			t.Errorf("strict decode accepted the unknown host_daemon key %q", key)
		}
	}
}

// TestValidScopesIsTheClosedSet keeps the accessor honest: it is what the "not in
// [...]" message renders and what a future `yolo pack lint` hint would read.
func TestValidScopesIsTheClosedSet(t *testing.T) {
	got := loopholedecl.ValidScopes()
	if len(got) != 2 || got[0] != loopholedecl.ScopeHost || got[1] != loopholedecl.ScopeJail {
		t.Errorf("ValidScopes() = %v, want [host jail] (sorted, so the error strings are "+
			"deterministic)", got)
	}
	// A copy, not the package's own slice — the accessor exists so a caller cannot
	// mutate the vocabulary.
	got[0] = "mutated"
	if loopholedecl.ValidScopes()[0] != loopholedecl.ScopeHost {
		t.Error("ValidScopes() hands out the package's own slice")
	}
}

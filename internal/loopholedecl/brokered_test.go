package loopholedecl

import (
	"reflect"
	"strings"
	"testing"
)

func decodeBrokered(t *testing.T, body string) (*Manifest, error) {
	t.Helper()
	return Decode([]byte(`{"name": "gb", "transport": "loopback-tls", "lifecycle": "spawned",`+body+`}`), "/packs/x/loopholes/gb")
}

func TestBrokeredBlockDecodes(t *testing.T) {
	m, err := decodeBrokered(t, `
		"host_daemon": {"cmd": ["yolo", "internal", "daemon", "gb", "--socket", "{socket}",
			"--scope-file", "{repository_scope}"], "publishes": "socket"},
		"brokered": {"source": "github", "remote_host": "github.com",
			"credential_paths": ["$GH_CONFIG_DIR", "$XDG_CONFIG_HOME/gh", "~/.config/gh"]}`)
	if err != nil {
		t.Fatal(err)
	}
	want := &Brokered{Source: "github", RemoteHost: "github.com",
		CredentialPaths: []string{"$GH_CONFIG_DIR", "$XDG_CONFIG_HOME/gh", "~/.config/gh"}}
	if !reflect.DeepEqual(m.Brokered, want) {
		t.Fatalf("brokered %+v", m.Brokered)
	}
	// The pack-shipped subset has nothing to refuse in a fence.
	if probs := m.PackShippedProblems("/x/manifest.jsonc"); len(probs) != 0 {
		t.Fatalf("pack-shipped problems %v", probs)
	}
}

func TestBrokeredBlockAndTokenAreHeldTogether(t *testing.T) {
	for name, c := range map[string]struct{ body, want string }{
		"block without token": {`"host_daemon": {"cmd": ["d", "{socket}"], "publishes": "socket"},
			"brokered": {"source": "github", "remote_host": "github.com"}`, "never names"},
		"token without block": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"}`,
			"declares no 'brokered'"},
		"token in doctor_cmd": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"doctor_cmd": ["d", "{repository_scope}"],
			"brokered": {"source": "github", "remote_host": "github.com"}`, "resolves only in 'host_daemon.cmd'"},
		"bad source": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"brokered": {"source": "Git Hub", "remote_host": "github.com"}`, "brokered.source"},
		"bad host": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"brokered": {"source": "github", "remote_host": "https://github.com"}`, "brokered.remote_host"},
		"bad credential path": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"brokered": {"source": "github", "remote_host": "github.com", "credential_paths": ["relative/gh"]}`,
			"credential_paths"},
		"unknown key": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"brokered": {"source": "github", "remote_host": "github.com", "sets": []}`, "brokered.sets"},
		// MANUAL-ONLY (docs/design/boundary-broker.md OQ-BB13): a default of on would turn a
		// brokered loophole on in every workspace, which no switch does.
		"default on": {`"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"default_enabled": true,
			"brokered": {"source": "github", "remote_host": "github.com"}`,
			"`yolo loopholes enable gb` run in that workspace, so 'default_enabled' cannot be true"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeBrokered(t, c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to mention %q", err, c.want)
			}
		})
	}
}

// An unknown key inside the block is refused by the TOLERANT decoder too: a fence key a
// build does not know is a fence that does not exist.
func TestBrokeredUnknownKeyRefusedTolerantly(t *testing.T) {
	_, _, err := DecodeTolerant([]byte(`{"name": "gb", "host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"],
		"publishes": "socket"}, "brokered": {"source": "github", "remote_host": "github.com", "later": 1}}`),
		"/packs/x/loopholes/gb")
	if err == nil {
		t.Fatal("tolerant decode accepted an unknown fence key")
	}
}

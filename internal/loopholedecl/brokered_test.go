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
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeBrokered(t, c.body)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to mention %q", err, c.want)
			}
		})
	}
}

func TestBrokeredReadableCapabilityMetadata(t *testing.T) {
	m, err := decodeBrokered(t, `
		"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
		"brokered": {"source": "git", "remote_host": "*", "grants": ["create-branch", "branch"],
			"read_capability": "fetch", "kind_capabilities": {"create-branch": "new_branches", "branch": "push"}, "pending_capabilities": []}`)
	if err != nil {
		t.Fatal(err)
	}
	want := &Brokered{Source: "git", RemoteHost: "*", Grants: []string{"create-branch", "branch"},
		ReadCapability: "fetch", KindCapabilities: map[string]string{"create-branch": "new_branches", "branch": "push"}}
	if !reflect.DeepEqual(m.Brokered, want) {
		t.Fatalf("brokered %+v, want %+v", m.Brokered, want)
	}

	pending, err := decodeBrokered(t, `
		"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
		"brokered": {"source": "github", "remote_host": "github.com", "read_capability": "read",
			"kind_capabilities": {"create-pr": "create_pr", "create-issue": "create_issue"},
			"pending_capabilities": ["create_pr", "create_issue"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Brokered.ReadCapability != "read" || len(pending.Brokered.Grants) != 0 || !reflect.DeepEqual(pending.Brokered.PendingCapabilities, []string{"create_pr", "create_issue"}) {
		t.Fatalf("pending-only metadata %+v", pending.Brokered)
	}
}

func TestBrokeredReadableCapabilityMetadataRefusesPartialAndAmbiguous(t *testing.T) {
	for name, brokered := range map[string]string{
		"missing read":               `"kind_capabilities": {"create-branch": "new_branches"}`,
		"missing grant mapping":      `"read_capability": "fetch", "kind_capabilities": {}`,
		"unknown grant kind":         `"read_capability": "fetch", "kind_capabilities": {"push": "push"}`,
		"reserved read name":         `"read_capability": "repos", "kind_capabilities": {}`,
		"reserved capability":        `"read_capability": "fetch", "kind_capabilities": {"create-branch": "always"}`,
		"collision":                  `"read_capability": "fetch", "kind_capabilities": {"create-branch": "fetch", "branch": "push"}`,
		"duplicate capability":       `"read_capability": "fetch", "kind_capabilities": {"create-branch": "write", "branch": "write"}`,
		"pending not mapped":         `"read_capability": "read", "kind_capabilities": {"create-pr": "create_pr"}, "pending_capabilities": ["missing"]`,
		"unmarked pending-only kind": `"read_capability": "read", "kind_capabilities": {"create-pr": "create_pr"}`,
		"non-string":                 `"read_capability": "fetch", "kind_capabilities": {"create-branch": 3, "branch": "push"}`,
	} {
		t.Run(name, func(t *testing.T) {
			grants := `"grants": ["create-branch", "branch"],`
			if name == "pending not mapped" || name == "unmarked pending-only kind" {
				grants = ""
			}
			_, err := decodeBrokered(t, `
				"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
				"brokered": {"source": "git", "remote_host": "*", `+grants+brokered+`}`)
			if err == nil || !strings.Contains(err.Error(), "brokered.") {
				t.Fatalf("err %v, want brokered metadata refusal", err)
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

// A source on any host declares `"remote_host": "*"` (docs/design/git-broker-plan.md ID-2): the
// whole token alone, never a partial glob.
func TestBrokeredRemoteHostWildcard(t *testing.T) {
	m, err := decodeBrokered(t, `
		"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
		"brokered": {"source": "git", "remote_host": "*"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Brokered == nil || m.Brokered.RemoteHost != "*" {
		t.Fatalf("brokered %+v", m.Brokered)
	}
	for _, host := range []string{"*.com", "*.*", "git*", " *"} {
		_, err := decodeBrokered(t, `
			"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"brokered": {"source": "git", "remote_host": "`+host+`"}`)
		if err == nil || !strings.Contains(err.Error(), "brokered.remote_host") {
			t.Errorf("remote_host %q: err %v, want a refusal naming brokered.remote_host", host, err)
		}
	}
}

// A source names the grant kinds it defines (docs/design/git-broker-plan.md ID-10, BB-D96): each a
// lowercase name, as a workspace grant and the user's `grant_kinds` spell it.
func TestBrokeredGrants(t *testing.T) {
	m, err := decodeBrokered(t, `
		"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
		"brokered": {"source": "git", "remote_host": "*", "grants": ["create-branch"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Brokered == nil || !reflect.DeepEqual(m.Brokered.Grants, []string{"create-branch"}) {
		t.Fatalf("brokered %+v, want grants [create-branch]", m.Brokered)
	}
	for _, grants := range []string{`["Create Branch"]`, `"create-branch"`, `[1]`, `["create-branch", "create-branch"]`} {
		_, err := decodeBrokered(t, `
			"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},
			"brokered": {"source": "git", "remote_host": "*", "grants": `+grants+`}`)
		if err == nil || !strings.Contains(err.Error(), "brokered.grants") {
			t.Errorf("grants %s: err %v, want a refusal naming brokered.grants", grants, err)
		}
	}
}

// A source declares that its unqualified request lines are for origin only; the value is the one
// it takes, and a source on any host has no one origin to narrow to.
func TestBrokeredUnqualifiedRequestsDecodesOnlyOriginOnOneHost(t *testing.T) {
	hostDaemon := `"host_daemon": {"cmd": ["d", "{socket}", "{repository_scope}"], "publishes": "socket"},`
	m, err := decodeBrokered(t, hostDaemon+`
		"brokered": {"source": "github", "remote_host": "github.com", "unqualified_requests": "origin"}`)
	if err != nil {
		t.Fatal(err)
	}
	if m.Brokered.UnqualifiedRequests != UnqualifiedRequestsOrigin {
		t.Fatalf("unqualified_requests decoded as %q", m.Brokered.UnqualifiedRequests)
	}
	for name, c := range map[string]struct{ body, want string }{
		"any other value": {`"remote_host": "github.com", "unqualified_requests": "all"`, "brokered.unqualified_requests"},
		"not a string":    {`"remote_host": "github.com", "unqualified_requests": true`, "brokered.unqualified_requests"},
		"any host":        {`"remote_host": "*", "unqualified_requests": "origin"`, "remote_host \"*\""},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := decodeBrokered(t, hostDaemon+`"brokered": {"source": "git", `+c.body+`}`)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err %v, want it to mention %q", err, c.want)
			}
		})
	}
}

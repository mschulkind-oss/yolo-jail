package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// usergrant_test.go covers the user config's read grant for every workspace
// (docs/design/boundary-broker.md BB-D105, BB-D107): how the gate shows and records it, and the
// advice every broker refusal names.

// The user config's read grant joins each workspace's scope at the config-change gate, never
// unseen: NewScopeCheck (the call a launch, `yolo config apply` and `yolo check` share) carries
// it, the gate's block shows it on its own line marked as the user config's, and only an
// approval records it, where a launch hands it to the broker.
func TestTheUserReadGrantIsShownAtTheGate(t *testing.T) {
	h := newBrokeredHost(t)
	write(t, filepath.Join(h.ws, ".git", "config"), "[remote \"origin\"]\n\turl = git@github.com:o/r.git\n")
	brokers := []ScopeBroker{{Source: "git", Label: "git-broker", RemoteHost: "*", GrantKinds: []string{"create-branch"}}}
	check := func() *ScopeCheck {
		t.Helper()
		read, err := ReadWorkspaceForGate(h.ws)
		if err != nil {
			t.Fatal(err)
		}
		return NewScopeCheck(h.ws, brokers, read)
	}
	if err := RecordApproval(h.ws, decode(t, `{}`), check()); err != nil {
		t.Fatal(err)
	}
	write(t, h.userCfg, `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`)
	c := check()
	read := brokerscope.Grant{Kind: brokerscope.KindRead, Repo: brokerscope.AnyRepo}
	if got := c.Sources[0].Grants; len(got) != 1 || got[0] != read {
		t.Fatalf("the gate's grants %v, want the user's read grant", got)
	}
	r, err := ScopeChanges(h.ws, c)
	if err != nil || !r.ScopeChanged {
		t.Fatalf("the user's read grant is no change at the gate: %+v, %v", r, err)
	}
	block := strings.Join(r.ScopeBlock, "\n")
	if !strings.Contains(block, "+ grant read *  added (from your user config, for every workspace)") {
		t.Fatalf("the block has no line for the user's read grant:\n%s", block)
	}
	if got := ApprovedGrants(h.ws, "git"); len(got) != 0 {
		t.Fatalf("the grant was recorded before any approval: %v", got)
	}
	if err := RecordApproval(h.ws, decode(t, `{}`), c); err != nil {
		t.Fatal(err)
	}
	if got := c.ApprovedGrants()["git-broker"]; len(got) != 1 || got[0] != read {
		t.Fatalf("the approved grants a launch hands the broker: %v", got)
	}
	if got := ApprovedGrants(h.ws, "git"); len(got) != 1 || got[0] != read {
		t.Fatalf("the approval record's grants %v", got)
	}

	// A workspace naming the same read grant adds nothing: the gate holds it once.
	write(t, h.userCfg, `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}], "grant_kinds": ["read"]}}}`)
	write(t, filepath.Join(h.ws, WorkspaceConfigName), `{"brokered": {"git": {"grants": [{"kind": "read", "repo": "*"}]}}}`)
	if got := check().Sources[0].Grants; len(got) != 1 || got[0] != read {
		t.Fatalf("the user's and the workspace's same grant: %v, want it once", got)
	}
}

// Every broker refusal for want of a grant names the next step and the host step that approves
// it, through the source's name alone: to a legacy JSONC user, the conversion and then the verb
// (never a hand edit of `brokered.<source>.grants`); the legacy key only where the source
// declares no read capability, so no verb exists.
func TestBrokerGrantAdviceNamesTheGrantAndTheApply(t *testing.T) {
	h := newBrokeredHost(t)
	if err := os.WriteFile(h.userCfg, []byte(`{"packs": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := BrokerNoReadRefusal("github", "read", "github.com/o/r", true)
	for _, want := range []string{"no read grant on github.com/o/r", "`yolo config migrate`",
		"`yolo allow github read`", "yolo config apply", "takes the grant at its next call"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "brokered.github.grants") {
		t.Errorf("a JSONC user's refusal names the legacy key to hand-edit:\n%s", got)
	}
	// With no declared read capability there is no verb to name, so the legacy key stands.
	got = BrokerNoReadRefusal("github", "", "github.com/o/r", true)
	for _, want := range []string{"`brokered.github.grants`", `{"kind": "read", "repo": "*"}`, "yolo config apply"} {
		if !strings.Contains(got, want) {
			t.Errorf("the refusal lacks %q:\n%s", want, got)
		}
	}
	if got := BrokerNoReadRefusal("github", "read", "github.com/o/r", false); !strings.Contains(got, "restart the jail") {
		t.Errorf("a broker that takes no live scope does not say to restart:\n%s", got)
	}
}

// A TOML user, and one with no user config yet (a first write makes it TOML), is sent by the
// broker's refusal and by validation's broker-switch messages to the readable grant,
// `yolo allow <source> <read capability>`; with no declared read capability there is no verb to
// name, so the legacy advice stands.
func TestBrokerGrantAdviceNamesYoloAllowToATOMLUser(t *testing.T) {
	gitBroker := loopholedecl.Brokered{Source: "git", ReadCapability: "fetch"}
	check := func(t *testing.T, what, got string) {
		t.Helper()
		if !strings.Contains(got, "`yolo allow git fetch`") || strings.Contains(got, "brokered.git.grants") {
			t.Errorf("%s does not name `yolo allow git fetch`:\n%s", what, got)
		}
	}
	for _, state := range []string{"config.toml", "no user config"} {
		t.Run(state, func(t *testing.T) {
			h := newMountsHost(t)
			if state == "config.toml" {
				h.userTOML(t, "packs = []\n")
			}
			check(t, "the broker's refusal", BrokerNoReadRefusal("git", "fetch", "git@h:o/r", true))
			entry := jsonx.NewOrderedMap()
			entry.Set("enabled", true)
			block := jsonx.NewOrderedMap()
			block.Set("git-broker", entry)
			check(t, "the user switch refusal", brokeredUserSwitchRefusalWithSources("git-broker", gitBroker, block, nil))
			check(t, "the workspace switch refusal", brokeredWorkspaceSwitchRefusal("git-broker", gitBroker, "yolo-jail.toml", entry))
			if got := BrokerNoReadRefusal("git", "", "git@h:o/r", true); !strings.Contains(got, "brokered.git.grants") {
				t.Errorf("with no read capability the refusal is not the legacy advice:\n%s", got)
			}
		})
	}
}

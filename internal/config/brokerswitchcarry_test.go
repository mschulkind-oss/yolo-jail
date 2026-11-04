package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/brokerscope"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// TestAStaleBrokerSwitchIsRemovedAndKeepsItsReadGrant is LC-D7 and OQ-LC2: a github-broker switch
// an older yolo's `yolo loopholes enable` wrote into the per-workspace file is taken out of that
// file by the validation the launch, `yolo check` and `yolo config apply` all run, through the
// writer `yolo loopholes disable` uses, and said in one line naming the file; the read grant the
// switch gave is carried in the workspace's approval record, so the gate's scope for that
// workspace holds it with no command and no user-config edit, under a legacy JSONC user config and
// a TOML one alike. Another workspace gains nothing.
func TestAStaleBrokerSwitchIsRemovedAndKeepsItsReadGrant(t *testing.T) {
	resolver := fakeResolver{"github-broker": {Name: "github-broker", HasHostDaemon: true,
		Brokered: &loopholedecl.Brokered{Source: "github", RemoteHost: "github.com", ReadCapability: "read"}}}
	broker := readableScopeBroker("github", "github", "github-broker", "github.com", "read", nil, nil)
	for _, tc := range []struct{ name, userFile, userBody string }{
		{"legacy user config", "config.jsonc", `{"packs": ["github"]}`},
		{"TOML user config", "config.toml", "packs = [\"github\"]\n\n[allow.github]\non_request = []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, userCfg, ws := workspaceFileHome(t, "app")
			if err := os.Remove(userCfg); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			write(t, filepath.Join(home, ".config", "yolo-jail", tc.userFile), tc.userBody)
			writeReadableScopeRemote(t, ws, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
			path, err := SetWorkspaceLoophole(ws, "github-broker", true)
			if err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadConfig(ws, true, nil)
			if err != nil {
				t.Fatal(err)
			}
			errs, warns := ValidateConfig(cfg, ws, resolver)
			if len(errs) != 0 {
				t.Fatalf("the old switch refused the config: %v", errs)
			}
			line := "Removed the old github-broker switch from " + path + "; this workspace keeps the read grant it gave"
			if len(containing(warns, line)) != 1 || len(containing(warns, "yolo loopholes disable")) != 0 {
				t.Fatalf("warnings = %v, want the one line %q and no command", warns, line)
			}
			if wf := ReadWorkspaceFile(ws); wf != nil {
				if _, set := wf.LoopholeSwitch("github-broker"); set {
					t.Fatalf("the old switch is still in %s", wf.Path)
				}
			}
			if got := CarriedBrokerGrants(ws, "github"); len(got) != 1 || got[0] != carriedBrokerReadGrant {
				t.Fatalf("the record carries %v, want the read grant", got)
			}
			// The record's carried key is no pack's recorded grants.
			if _, listed := RecordedLoopholeGrants(ws)["github.grants"]; listed {
				t.Errorf("the carried broker grant reads as a pack's grants: %v", RecordedLoopholeGrants(ws))
			}

			read, err := ReadWorkspaceForGate(ws)
			if err != nil {
				t.Fatal(err)
			}
			check := NewScopeCheck(ws, []ScopeBroker{broker}, read)
			if check == nil || len(check.Sources) != 1 {
				t.Fatalf("no scope check for the broker: %+v", check)
			}
			s := check.Sources[0]
			if !brokerscope.HoldsRead(s.Grants) || s.Dormant {
				t.Fatalf("the gate's scope holds no read grant for the workspace the switch gave one: %+v", s)
			}
			r, err := ScopeChanges(ws, check)
			if err != nil {
				t.Fatal(err)
			}
			block := strings.Join(r.ScopeBlock, "\n")
			if !strings.Contains(block, carriedBrokerGrantOrigin) || strings.Contains(block, "for every workspace") {
				t.Errorf("the read grant's gate line does not say it is carried for this workspace:\n%s", block)
			}

			// yolo check's up-front note reads the carried grant for this workspace, and only here.
			notes := strings.Join(UngrantedBrokerNotes(resolver, ws), "\n")
			if strings.Contains(notes, "grants nothing") || !strings.Contains(notes, "runs on the read grant this workspace's approval record carries") {
				t.Errorf("the up-front note for the workspace that keeps the grant: %q", notes)
			}

			// A second validation says it again (a launch validates more than once) and writes nothing.
			_, warns = ValidateConfig(cfg, ws, resolver)
			if len(containing(warns, line)) != 1 {
				t.Errorf("a second validation in the process lost the line: %v", warns)
			}

			// Another workspace with the same remote holds nothing from it.
			other := filepath.Join(home, "code", "other")
			writeReadableScopeRemote(t, other, "[remote \"origin\"]\nurl = https://github.com/acme/repo.git\n")
			oread, err := ReadWorkspaceForGate(other)
			if err != nil {
				t.Fatal(err)
			}
			if oc := NewScopeCheck(other, []ScopeBroker{broker}, oread); oc != nil && len(oc.Sources) == 1 &&
				brokerscope.HoldsRead(oc.Sources[0].Grants) {
				t.Errorf("another workspace gained the carried read grant: %+v", oc.Sources[0])
			}
			if notes := strings.Join(UngrantedBrokerNotes(resolver, other), "\n"); !strings.Contains(notes, "grants nothing") {
				t.Errorf("another workspace's note does not say the broker grants it nothing: %q", notes)
			}
		})
	}
}

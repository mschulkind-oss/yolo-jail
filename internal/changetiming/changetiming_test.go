package changetiming

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
)

func TestEveryConfigKeyHasAnExplicitTimingRow(t *testing.T) {
	for _, key := range config.TopLevelConfigKeys() {
		for _, backend := range []Backend{Podman, AppleContainer, MacOSUser, Host} {
			if _, err := Classify(key, Context{Backend: backend, PackageDelivery: BakedImage}); err != nil {
				t.Errorf("Classify(%q, %q): %v", key, backend, err)
			}
		}
	}
}

func TestTimingBackendAndAppliedPredicates(t *testing.T) {
	cases := []struct {
		key  string
		ctx  Context
		want Outcome
	}{
		{"mounts", Context{Backend: Podman, LiveMounts: true}, AppliedNow},
		{"mounts", Context{Backend: Podman}, AtNextRestart},
		{"mounts", Context{Backend: AppleContainer, LiveMounts: true}, AtNextRestart},
		{"mounts", Context{Backend: MacOSUser}, NextSession},
		{"brokered", Context{Backend: Podman, LiveScope: true}, AppliedNow},
		{"brokered", Context{Backend: AppleContainer, LiveScope: true}, AtNextRestart},
		{"brokered", Context{Backend: MacOSUser, LiveScope: true}, AppliedNow},
		{"brokered", Context{Backend: MacOSUser}, NextSession},
		{"agents_md_extra", Context{Backend: Podman}, NextSession},
		{"agents_md_extra", Context{Backend: AppleContainer}, AtNextRestart},
		{"providers", Context{Backend: AppleContainer}, NextSession},
		{"resources", Context{Backend: Podman}, AtNextRestart},
		{"resources", Context{Backend: Podman, LiveResources: true}, AppliedNow},
		{"resources", Context{Backend: AppleContainer, LiveResources: true}, AtNextRestart},
		{"resources", Context{Backend: MacOSUser, LiveResources: true}, NextSession},
		{"packages", Context{Backend: Podman, PackageDelivery: BakedImage, Rebuild: true}, RestartAndRebuild},
		{"packages", Context{Backend: Podman, PackageDelivery: StoreDelivered, Rebuild: true}, AtNextRestart},
		{"mounts", Context{Backend: Podman, UserScopeOnly: true}, UserScopeOnly},
		{"loopholes", Context{Backend: Podman, HostSwitch: true}, HostSwitch},
	}
	for _, tc := range cases {
		row, err := Classify(tc.key, tc.ctx)
		if err != nil {
			t.Errorf("Classify(%q): %v", tc.key, err)
			continue
		}
		if row.Outcome != tc.want {
			t.Errorf("Classify(%q, %+v).Outcome = %q, want %q", tc.key, tc.ctx, row.Outcome, tc.want)
		}
	}
}

func TestUnknownKeyFailsExplicitly(t *testing.T) {
	if _, err := Classify("future_key", Context{Backend: Podman}); err == nil || !strings.Contains(err.Error(), "future_key") {
		t.Fatalf("Classify unknown key error = %v, want key-specific error", err)
	}
}

func TestHostCommandNamesWorkspaceAndCommand(t *testing.T) {
	got := HostCommand("yolo config apply", "/home/ada/code/app", false)
	for _, want := range []string{"yolo config apply", "/home/ada/code/app", "host"} {
		if !strings.Contains(got, want) {
			t.Errorf("HostCommand() = %q, missing %q", got, want)
		}
	}
	if got := HostCommand("yolo stop", "/home/ada/code/app", true); !strings.Contains(got, "ends the jail's sessions") {
		t.Errorf("HostCommand ending sessions = %q, want session-ending disclosure", got)
	}
}

func TestHostRowsMatchTheHostInputs(t *testing.T) {
	for key, want := range map[string]Outcome{
		"providers": AppliedNow, "profiles": AppliedNow, "profile": AppliedNow,
		"model_facts": AppliedNow, "mcp_servers": AppliedNow, "lsp_servers": AppliedNow,
		"packs": AppliedNow, "agents_md_extra": AppliedNow,
		"mounts": NotApplicable, "packages": NotApplicable, "network": NotApplicable,
		"brokered": NotApplicable, "loopholes": NotApplicable,
		"env_sources": NextSession,
	} {
		row, err := Classify(key, Context{Backend: Host})
		if err != nil || row.Outcome != want {
			t.Errorf("Classify(%q, host) = %+v, %v; want %q", key, row, err, want)
		}
	}
}

func TestHostApplyConsumedKeysAreHonoredByHostMetadata(t *testing.T) {
	for key := range hostApplyConsumedKeys {
		if disposition, _ := render.HostFields().ConfigKey(key); disposition != render.KeyHonored {
			t.Errorf("host apply key %q has HostFields disposition %v, want honored", key, disposition)
		}
	}
}

func TestHostSwitchOnlyChangesLoopholeTiming(t *testing.T) {
	row, err := Classify("loopholes", Context{Backend: Podman, HostSwitch: true})
	if err != nil || row.Outcome != HostSwitch {
		t.Fatalf("loopholes = %+v, %v; want host switch", row, err)
	}
	row, err = Classify("prune", Context{Backend: Podman, HostSwitch: true})
	if err != nil || row.Outcome != AtNextRestart {
		t.Fatalf("prune = %+v, %v; HostSwitch must not alter planner timing", row, err)
	}
}

// A grant change reaches a running mediated loophole's daemon at its next request, since `yolo
// config apply` rewrites the grant file it reads; an unmediated loophole's grant is container
// argv, decided when the jail starts (docs/reference/loophole-grants.md §4.6, LG-D7).
func TestAMediatedGrantIsLiveAndAnUnmediatedOneWaitsForRestart(t *testing.T) {
	cases := []struct {
		name string
		ctx  Context
		want Outcome
	}{
		{"mediated, live file", Context{Backend: Podman, Grant: MediatedGrant, LiveGrants: true}, AppliedNow},
		{"mediated, no live file", Context{Backend: Podman, Grant: MediatedGrant}, AtNextRestart},
		{"unmediated", Context{Backend: Podman, Grant: UnmediatedGrant, LiveGrants: true}, AtNextRestart},
		{"mediated on Apple Container", Context{Backend: AppleContainer, Grant: MediatedGrant, LiveGrants: true}, AppliedNow},
		{"unmediated on Apple Container", Context{Backend: AppleContainer, Grant: UnmediatedGrant}, AtNextRestart},
		{"mediated in a native session", Context{Backend: MacOSUser, Grant: MediatedGrant, LiveGrants: true}, AppliedNow},
		{"unmediated in a native session", Context{Backend: MacOSUser, Grant: UnmediatedGrant}, NextSession},
		{"no grant: the requests key alone", Context{Backend: Podman}, AtNextRestart},
	}
	for _, tc := range cases {
		row, err := Classify("requests", tc.ctx)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if row.Outcome != tc.want {
			t.Errorf("%s: Classify(requests, %+v) = %q, want %q", tc.name, tc.ctx, row.Outcome, tc.want)
		}
		if row.Outcome == AtNextRestart && tc.ctx.Grant != NoGrant && row.Reason == "" {
			t.Errorf("%s: a grant waiting for a restart says no reason", tc.name)
		}
	}
}

// LA-S4a (docs/design/live-config-apply.md): the MCP, LSP, blocked-tool and mise inputs ride the
// per-entry channel, so a jail that proves it reads them there takes a change at its next
// session; one launched before that froze them and waits for its restart, saying why.
func TestSessionInputsReachTheNextSessionOfAJailThatReadsThem(t *testing.T) {
	for _, key := range []string{"mcp_servers", "lsp_servers", "security", "mise_tools"} {
		for _, backend := range []Backend{Podman, AppleContainer} {
			row, err := Classify(key, Context{Backend: backend, SessionInputs: true})
			if err != nil || row.Outcome != NextSession {
				t.Errorf("Classify(%q, %s, session inputs) = %+v, %v; want next session", key, backend, row, err)
			}
			row, err = Classify(key, Context{Backend: backend})
			if err != nil || row.Outcome != AtNextRestart || row.Reason == "" {
				t.Errorf("Classify(%q, %s, older jail) = %+v, %v; want at next restart with a reason", key, backend, row, err)
			}
		}
		if row, err := Classify(key, Context{Backend: MacOSUser}); err != nil || row.Outcome != NextSession {
			t.Errorf("Classify(%q, macos-user) = %+v, %v; want next session", key, row, err)
		}
	}
	// The capability moves nothing else.
	if row, _ := Classify("network", Context{Backend: Podman, SessionInputs: true}); row.Outcome != AtNextRestart {
		t.Errorf("network with session inputs = %q, want at next restart", row.Outcome)
	}
}

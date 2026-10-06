package packload

import "testing"

// A LAUNCH WITH NO PROVIDERS TABLE wants no list. Every launch with no profile selected composes a
// nil table, and ListWants once ranged over its keys unguarded, so every such launch panicked
// (the 2026-10-06 landing gate: TestAgentToolsAvailableDirect and 17 more).
func TestALaunchWithNoProvidersTableWantsNoList(t *testing.T) {
	if got := ListWants(nil, nil, nil, &CredentialScope{}); len(got) != 0 {
		t.Fatalf("ListWants with no providers table = %v, want none", got)
	}
}

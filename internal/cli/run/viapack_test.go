package run

import (
	"bytes"
	"strings"
	"testing"
)

// viapack_test.go pins the `via` half of the selection closure (docs/design/
// wire-bridge-gateway.md OQ-WG6/WG7 (c)): a selected profile whose `via` names a service
// pack adds that pack like a need, so a pi-only jail gets the bridge its profile routes
// through.

func loadedNames(t *testing.T, o *Options, cname string) []string {
	t.Helper()
	_, loaded, _, err := o.stagePacks(cname)
	if err != nil {
		t.Fatalf("stagePacks: %v", err)
	}
	var names []string
	for _, p := range loaded {
		names = append(names, p.Name)
	}
	return names
}

// TestAViaProfileBringsItsServicePack: pi alone selects no pack that needs the bridge,
// and a -p selecting a via profile for pi adds it, disclosing why.
func TestAViaProfileBringsItsServicePack(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}}}`)
	var errBuf bytes.Buffer
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: &errBuf,
		UseProfiles: map[string]string{"pi": "pz"}}
	names := loadedNames(t, o, "yolo-test-via-joined")
	if !hasName(names, "wire-bridge") {
		t.Fatalf("a via profile did not bring its service pack: loaded = %v", names)
	}
	if got := errBuf.String(); !strings.Contains(got, "+ wire-bridge (via of profile pz, active for pi)") {
		t.Errorf("the launch must disclose why the pack joined:\n%s", got)
	}
}

// TestNoViaProfileLeavesThePackSetAlone is the control: the same config with the profile
// not selected (or no via on it) stages no bridge.
func TestNoViaProfileLeavesThePackSetAlone(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pz": {"provider": "zai", "via": "wire-bridge"}, "plain": {"provider": "zai"}}}`)
	for name, use := range map[string]map[string]string{
		"unselected": nil,
		"no via":     {"pi": "plain"},
	} {
		o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(), UseProfiles: use}
		if names := loadedNames(t, o, "yolo-test-via-"+strings.ReplaceAll(name, " ", "-")); hasName(names, "wire-bridge") {
			t.Errorf("%s: the bridge joined without a selected via profile: %v", name, names)
		}
	}
}

// TestAViaNamingAPackThatServesNoViaRouteIsRefused: a via must name a service pack that
// declares a via_address; claude is embedded but serves none, so the launch refuses.
func TestAViaNamingAPackThatServesNoViaRouteIsRefused(t *testing.T) {
	home := packHome(t)
	writeUserConfig(t, home, `{"packs": ["pi"],
	  "profiles": {"pz": {"provider": "zai", "via": "claude"}}}`)
	o := &Options{Workspace: t.TempDir(), Stdout: discardBuf(), Stderr: discardBuf(),
		UseProfiles: map[string]string{"pi": "pz"}}
	_, _, _, err := o.stagePacks("yolo-test-via-noservice")
	if err == nil || !strings.Contains(err.Error(), "declares no service with a via_address") {
		t.Fatalf("err = %v, want a refusal naming the missing via_address", err)
	}
}

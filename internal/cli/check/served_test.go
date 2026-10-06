package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

// THE PREDICTION SERVES A BOUND LOOPHOLE WHERE THE LAUNCH DOES (docs/design/loophole-packaging.md
// LP-D1): a loophole with a host bind and no jail daemon is served by name on a container
// runtime, where the argv binds it, and not on macos-user, which binds nothing. So a pack env
// pointer `served_by` one (packs/audio's PULSE_SERVER) is predicted delivered or withheld exactly
// as the launch delivers or withholds it. Deleting predictedServed's JailBoundNames fails the
// container half; deleting loopholes.JailBoundNames's macos-user arm fails the other.
func TestCheckPredictsABoundLoopholeServedWhereItsBindsGo(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	moduleRoot := isolatedModuleDir(t)
	sock := filepath.Join(t.TempDir(), "native")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeLoopholeManifest(t, moduleRoot, "snd-like",
		`"name":"snd-like","description":"d","transport":"none","default_enabled":true,`+
			`"host_bind_mounts":[{"host":"`+sock+`","container":"/run/snd-like/native","readonly":true}]`)
	for _, tc := range []struct {
		runtime string
		served  bool
	}{
		{"", true},
		{"podman", true},
		{"macos-user", false},
	} {
		merged := jsonx.NewOrderedMap()
		if tc.runtime != "" {
			merged.Set("runtime", tc.runtime)
		}
		if got := (&Options{}).predictedServed(merged, nil).Serves("snd-like"); got != tc.served {
			t.Errorf("runtime %q: predicted the bound loophole served = %v, want %v", tc.runtime, got, tc.served)
		}
	}
}

// On macos-user the prediction carries the launch's mark too (packload.ServedDaemons
// MountsNothing), so a gate composed over it words a withheld audio pointer as the launch does:
// the sandbox binds nothing. Deleting predictedServed's macos-user mark fails this.
func TestCheckPredictsTheMacosUserWordingForABoundLoophole(t *testing.T) {
	var audio *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "audio" {
			audio = p
		}
	}
	if audio == nil {
		t.Fatal("no embedded audio pack")
	}
	merged := jsonx.NewOrderedMap()
	merged.Set("runtime", "macos-user")
	served := (&Options{}).predictedServed(merged, []*packload.Pack{audio})
	scope, err := packload.ScopeCredentials(packload.ScopeInput{
		Packs: []*packload.Pack{audio}, NoDerives: true, Served: &served})
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(packload.UnservedLines(scope, nil, nil), "\n")
	if !strings.Contains(lines, "which the macos-user sandbox does not have") {
		t.Errorf("the predicted macos-user set does not word the audio pointers as the launch does:\n%s", lines)
	}
}

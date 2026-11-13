package check

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
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
		`"name":"snd-like","description":"d","transport":"none",`+
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
		if got := optionsOnRuntime(tc.runtime).predictedServed(merged, nil).Serves("snd-like"); got != tc.served {
			t.Errorf("runtime %q: predicted the bound loophole served = %v, want %v", tc.runtime, got, tc.served)
		}
	}
}

// THE GIT REWRITE IS PREDICTED SERVED WHEREVER ITS BROKER RUNS (docs/reference/git-broker.md
// ID-6; docs/design/boundary-broker.md BB-D104): with packs ["git"] on podman the env the
// prediction composes carries the rewrite whatever a config switch of git-broker says, since a
// broker has none; on Apple Container, which runs no host services, it lacks GIT_CONFIG_COUNT
// and the Not-set line says why. Deleting predictedServed's FrontedHostNames fails the podman
// half.
func TestGitRewriteServedWhereverItsBrokerRuns(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	wd, _ := os.Getwd()
	module := filepath.Join(wd, "..", "..", "..", "packs", "git", "loopholes", "git-broker")
	if _, err := os.Stat(filepath.Join(module, "loophole.toml")); err != nil {
		t.Fatalf("the git pack's loophole module: %v", err)
	}
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, HostExecApproved: true}})
	t.Cleanup(loopholes.ResetPackModules)
	t.Setenv("HOME", t.TempDir())
	var git *packload.Pack
	for _, p := range packload.Embedded() {
		if p.Name == "git" {
			git = p
		}
	}
	if git == nil {
		t.Fatal("no embedded git pack")
	}
	for _, c := range []struct {
		runtime string
		enabled bool
	}{{"podman", false}, {"podman", true}, {"container", true}} {
		enabled, served := c.enabled, c.runtime == "podman"
		merged := jsonx.NewOrderedMap()
		lh, entry := jsonx.NewOrderedMap(), jsonx.NewOrderedMap()
		entry.Set("enabled", enabled)
		lh.Set("git-broker", entry)
		merged.Set("loopholes", lh)
		predicted := optionsOnRuntime(c.runtime).predictedServed(merged, []*packload.Pack{git})
		scope, err := packload.ScopeCredentials(packload.ScopeInput{
			Packs: []*packload.Pack{git}, NoDerives: true, Served: &predicted})
		if err != nil {
			t.Fatal(err)
		}
		count, hasCount := scope.DeliveredPackEnv("GIT_CONFIG_COUNT")
		key0, _ := scope.DeliveredPackEnv("GIT_CONFIG_KEY_0")
		switch {
		case !served && hasCount:
			t.Errorf("%s: the predicted env carries GIT_CONFIG_COUNT=%s", c.runtime, count)
		case served && key0 != "url.yolo::ssh://.insteadOf":
			t.Errorf("%s, switch %v: GIT_CONFIG_KEY_0 = %q, want url.yolo::ssh://.insteadOf", c.runtime, enabled, key0)
		}
		// The withheld rewrite is named in the launch's words (loopholes.Set.FrontedHostWhy):
		// deleting predictedServed's WithNotServedWhy fails this.
		lines := strings.Join(packload.UnservedLines(scope, nil, nil), "\n")
		if !served && !strings.Contains(lines, "Apple Container cannot serve") {
			t.Errorf("%s: the predicted Not-set line does not say why:\n%s", c.runtime, lines)
		}
		if strings.Contains(lines, "yolo loopholes enable") {
			t.Errorf("%s: the predicted Not-set line names a switch the broker does not have:\n%s", c.runtime, lines)
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
	served := optionsOnRuntime("macos-user").predictedServed(merged, []*packload.Pack{audio})
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

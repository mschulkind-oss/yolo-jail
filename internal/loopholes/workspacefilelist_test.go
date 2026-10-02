package loopholes

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/json5"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// workspacefilelist_test.go: `yolo loopholes list` and `status` read the per-workspace file
// (config/workspacefile.go; docs/design/boundary-broker.md BB-D54) as the launch does.

// brokeredModule records one pack loophole declaring `brokered`, off by default as a brokered
// manifest must be (OQ-BB13), and one ordinary loophole, also off.
func brokeredModule(t *testing.T) {
	t.Helper()
	isolateModules(t)
	parent := t.TempDir()
	gb := filepath.Join(parent, "gb")
	must(t, os.MkdirAll(gb, 0o755))
	must(t, os.WriteFile(filepath.Join(gb, "manifest.jsonc"), []byte(`{"name": "gb",
	  "transport": "loopback-tls", "lifecycle": "spawned",
	  "host_daemon": {"cmd": ["/bin/true", "{repository_scope}"], "publishes": "socket"},
	  "brokered": {"source": "gbsrc", "remote_host": "github.com"}}`), 0o644))
	plain := filepath.Join(parent, "plain")
	must(t, os.MkdirAll(plain, 0o755))
	must(t, os.WriteFile(filepath.Join(plain, "manifest.jsonc"),
		[]byte(`{"name": "plain", "transport": "none", "lifecycle": "external"}`), 0o644))
	SetPackModules([]PackModule{{Dir: gb, HostExecApproved: true}, {Dir: plain, HostExecApproved: true}})
}

// listedEnabled is each listed loophole's switch.
func listedEnabled(t *testing.T, deps Deps) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, lp := range loopholesWithConfig(deps, true).All() {
		out[lp.Name] = lp.Enabled
	}
	return out
}

// TestLoopholesListReadsThePerWorkspaceFileLast: the per-workspace file is the listing's last
// scope, as it is the launch's, so a loophole it switches lists as the launch will run it, over
// the workspace config's own switch; and a user-config switch of a brokered loophole is refused
// here as the launch refuses it, naming the command that switches it per project.
func TestLoopholesListReadsThePerWorkspaceFileLast(t *testing.T) {
	unsetJail(t)
	brokeredModule(t)
	t.Setenv("HOME", t.TempDir())
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf, `{"loopholes": {"gb": {"enabled": true}}}`,
		`{"loopholes": {"plain": {"enabled": false}}}`)
	if got := listedEnabled(t, deps); got["gb"] || got["plain"] {
		t.Fatalf("with no per-workspace file: %v, want both off (the user switch of gb refused)", got)
	}
	if !strings.Contains(errBuf.String(), "`yolo loopholes enable gb`") {
		t.Errorf("the refused user-config switch of a brokered loophole was not reported:\n%s", errBuf.String())
	}

	for _, name := range []string{"gb", "plain"} {
		if _, err := config.SetWorkspaceLoophole(deps.Cwd, name, true); err != nil {
			t.Fatal(err)
		}
	}
	deps.LoadWorkspaceFile = config.ReadWorkspaceFile
	if got := listedEnabled(t, deps); !got["gb"] || !got["plain"] {
		t.Errorf("with the per-workspace file switching both on: %v, want both on, over the "+
			"workspace config's own switch", got)
	}
}

// TestInAJailABrokeredLoopholeListsAsTheHostLaunchedIt: in a jail the per-workspace file is never
// readable, so a broker the jail is using reads as on from the config the host launched it with;
// nothing that is not brokered takes its switch from there.
func TestInAJailABrokeredLoopholeListsAsTheHostLaunchedIt(t *testing.T) {
	unsetJail(t)
	brokeredModule(t)
	var out, errBuf bytes.Buffer
	deps := cmdDeps(t, &out, &errBuf, "", "")
	launched, err := json5.Decode([]byte(`{"loopholes": {"gb": {"enabled": true}, "plain": {"enabled": true}}}`))
	if err != nil {
		t.Fatal(err)
	}
	deps.LaunchConfig = func(string) (*jsonx.OrderedMap, bool) { return launched.(*jsonx.OrderedMap), true }
	if got := listedEnabled(t, deps); got["gb"] {
		t.Fatalf("on the host the launch config was read: %v", got)
	}
	deps.InJail = true
	got := listedEnabled(t, deps)
	if !got["gb"] {
		t.Errorf("in the jail the broker its host launched reads as off: %v", got)
	}
	if got["plain"] {
		t.Errorf("a loophole that is not brokered took its switch from the launch config: %v", got)
	}
}

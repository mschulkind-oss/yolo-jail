package run

// hostdoorwaydisclosure_test.go pins the `yolo host` call site of the exec disclosure
// (HostDoorways.Start): the "runs pack code on your machine" block names the loopholes whose
// doorways this launch opens, the set its spawn is handed, and not another loophole the same
// pack ships. TestDisclosureNamesOnlyTheLoopholesThisLaunchStarts pins the jail launch's call
// site (discloseLoopholes); this call site passes its own predicate, so it needs its own pin.

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/launchservice"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/packload"
)

func TestHostDoorwayDisclosureNamesOnlyTheDoorwaysItOpens(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	emptyLoopholeDirs(t)
	isolatePackModules(t)

	// ONE pack shipping two loopholes: the doorway this launch opens, and another whose daemon
	// it does not start. Neither is switched on, so the spawn below starts nothing and the
	// assertion is on the disclosure alone.
	root := t.TempDir()
	for name, body := range map[string]string{
		"acme-door": `{"name": "acme-door", "transport": "none",
			"host_daemon": {"cmd": ["python3", "{loophole_dir}/door-daemon.py"], "publishes": "socket"}}`,
		"acme-other": `{"name": "acme-other", "transport": "none",
			"host_daemon": {"cmd": ["python3", "{loophole_dir}/other-daemon.py"], "publishes": "socket"}}`,
	} {
		mod := filepath.Join(root, "loopholes", name)
		if err := os.MkdirAll(mod, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(mod, "manifest.jsonc"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "pack.json"), []byte(`{"contributes":[
		{"kind":"loophole","from":"loopholes/acme-door"},
		{"kind":"loophole","from":"loopholes/acme-other"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	p, probs := packload.LoadDir(root, "acme")
	if len(probs) > 0 {
		t.Fatalf("the two-loophole pack fixture does not load: %v", probs)
	}
	packs := []*packload.Pack{p}

	d := &HostDoorways{
		plans: []*launchservice.Plan{launchservice.PlanAt(launchservice.Declared{
			Service: "acme-door", Pack: "acme", Cmd: []string{"yolo", "doorway"},
		}, "127.0.0.1:1", "YOLO_TEST_DOOR_TOKEN", "token", nil)},
		set:   loopholes.NewSet(loopholes.DiscoverOptions{PackModules: packLoopholeModules(packs)}),
		packs: packs,
	}
	var buf bytes.Buffer
	_, stop, _, err := d.Start(newConfig(), t.TempDir(), "pi", &buf,
		func(*launchservice.Plan, map[string]string) (*launchservice.Running, error) {
			return nil, errors.New("not started in a unit test")
		})
	if stop != nil {
		stop()
	}
	if err == nil {
		t.Fatal("the stub start refused the doorway, so Start should have returned its error")
	}

	got := buf.String()
	if !strings.Contains(got, "runs pack code on your machine") || !strings.Contains(got, "door-daemon.py") {
		t.Errorf("the disclosure does not name the loophole whose doorway this launch opens:\n%s", got)
	}
	if strings.Contains(got, "other-daemon.py") {
		t.Errorf("the disclosure names a loophole this launch opens no doorway for and starts no "+
			"daemon of, because its pack also ships the doorway:\n%s", got)
	}
}

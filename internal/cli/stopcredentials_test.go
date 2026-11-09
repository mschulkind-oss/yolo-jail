package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/entrypoint"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// savedFinishStop is the production teardown (run.FinishStop), taken before any test swaps it.
var savedFinishStop = finishStop

// A STOP OF A JAIL THAT IS NOT THE SELECTED SETUP'S TAKES ITS CREDENTIAL FILES AWAY TOO (review
// M3): `yolo stop` ends the legacy jail (and any other setup's) through run.FinishStop, whose
// teardown removes the jail's --with-credentials grant file only for the jail it was told it is
// ending. Before the fix it asked the selected setup's name, so the legacy jail's grant file, which
// carries provider keys, outlived it. Driven from the front door (runStop) through the real
// FinishStop, so deleting FinishStop's naming of the jail fails this.
func TestAStopOfTheLegacyJailRemovesItsGrantFile(t *testing.T) {
	cname, _ := stopWorkspace(t, "podman-self")
	legacy := strings.TrimSuffix(cname, "-podman-self")
	// The real teardown, not stopWorkspace's stand-in.
	finishStop = savedFinishStop
	// The teardown's own runtime questions go to a podman that knows no container, so the jail is
	// known gone and nothing outside the test is asked.
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "podman"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	ws, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	grant := filepath.Join(paths.LegacyHomeState(ws), "config", filepath.Base(entrypoint.JailGrantFileRel))
	if err := os.MkdirAll(filepath.Dir(grant), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(grant, []byte("export PROVIDER_KEY=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The legacy jail was started by a keeper-era yolo whose keeper is gone (an owner PID that no
	// process holds), so this stop runs the teardown itself (the unkept reap).
	owners := filepath.Join(paths.GlobalStorage(), "owners")
	if err := os.MkdirAll(owners, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(owners, legacy), []byte("999999\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	saved := stopLegacyJailKnown
	t.Cleanup(func() { stopLegacyJailKnown = saved })
	stopLegacyJailKnown = func(string) bool { return true }
	running := map[string]bool{cname: false, legacy: true}
	useStopExec(t, func(argv []string, _ time.Duration) (string, bool, int) {
		target := argv[len(argv)-1]
		switch {
		case argv[1] == "inspect" && running[target]:
			return "true\n", true, 0
		case argv[1] == "inspect":
			return "Error: no such container", true, 125
		case argv[1] == "container" && argv[2] == "exists" && running[target]:
			return "", true, 0
		case argv[1] == "container" && argv[2] == "exists":
			return "", true, 1
		case argv[1] == "stop" || argv[1] == "rm":
			running[target] = false
			return "", true, 0
		}
		t.Errorf("unexpected argv %v", argv)
		return "", false, 1
	})
	var rc int
	stdout, stderr := captureBoth(t, func() { rc = runStop([]string{"stop"}) })
	if rc != 0 {
		t.Fatalf("runStop = %d\nstdout:\n%s\nstderr:\n%s", rc, stdout, stderr)
	}
	if running[legacy] {
		t.Fatalf("the legacy jail was not stopped:\n%s", stdout)
	}
	if _, err := os.Lstat(grant); !os.IsNotExist(err) {
		t.Errorf("the legacy jail's grant file %s outlived it (%v)\nstdout:\n%s\nstderr:\n%s", grant, err, stdout, stderr)
	}
}

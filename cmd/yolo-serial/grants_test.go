package main

// grants_test.go pins the client's half of the loophole grants
// (docs/reference/loophole-grants.md §5; plan T9, PD-8): an inert loophole says so before
// any endpoint is tried, a daemon's grant refusal reaches the person verbatim with its exit
// code, and the fallback for a bridge that is not there names a verb, not a config edit.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/serialdaemon"
)

// TestYoloSerialPrintsTheInertLine: with the launch's inert variable set, every subcommand
// prints §5's inert line and exits 64 without touching the endpoint, which here does not exist.
func TestYoloSerialPrintsTheInertLine(t *testing.T) {
	t.Setenv(inertEnv, "this backend does not admit host services")
	t.Setenv(paths.SerialEndpointEnv, filepath.Join(t.TempDir(), "absent.endpoint"))
	want := "yolo-serial: the serial loophole does nothing on this setup (this backend does not admit host services); nothing is missing to install.\n"
	for _, args := range [][]string{
		{"list"},
		{"read", "/dev/ttyUSB0"},
		{"write", "/dev/ttyUSB0", "x"},
		{"monitor", "/dev/ttyUSB0"},
		{"pty", "/dev/ttyUSB0"},
	} {
		var rc int
		stdout, stderr := captureStdio(t, func() { rc = run(args) })
		if rc != 64 || stderr != want || stdout != "" {
			t.Errorf("%v: rc %d, stdout %q, stderr %q; want 64 and %q", args, rc, stdout, stderr, want)
		}
	}
}

// TestTheClientPrintsTheDaemonsGrantRefusal: the daemon is the source of the refusal when it is
// reachable; the client prints its text and exits with its code.
func TestTheClientPrintsTheDaemonsGrantRefusal(t *testing.T) {
	t.Setenv(inertEnv, "")
	b := loopholegrant.Block{
		Pack: "serial", Workspace: "/home/u/proj", PID: os.Getpid(),
		Kinds: map[string]loopholegrant.Kind{"devices": {Standing: loopholegrant.Requestable}},
	}
	data, err := loopholegrant.Merge(nil, b)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	endpoint := frontFor(t, serialdaemon.BuildHandler(serialdaemon.LoadSettings(path)))

	want := loopholegrant.Refusal{
		Client: "yolo-serial", Pack: "serial", Access: "open host serial devices", Kind: "devices",
		ValueNoun: "device globs", Standing: loopholegrant.Requestable,
	}.Text()
	var rc int
	_, stderr := captureStdio(t, func() { rc = run([]string{"list", "--endpoint", endpoint}) })
	if rc != 64 || stderr != want {
		t.Errorf("rc %d, stderr:\n%s\nwant 64 and:\n%s", rc, stderr, want)
	}
}

// TestNoEndpointNamesTheVerbs: with no bridge wired up, the fallback names selecting the pack
// and requesting the grant, and no longer the retired `enabled` switch.
func TestNoEndpointNamesTheVerbs(t *testing.T) {
	var b strings.Builder
	noEndpointMsg(&b)
	msg := b.String()
	for _, want := range []string{"yolo pack add serial", "yolo request serial devices <device globs>", "yolo config apply"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the no-endpoint message lacks %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "enabled") || strings.Contains(msg, "config.jsonc") {
		t.Errorf("the no-endpoint message still names the switch:\n%s", msg)
	}
}

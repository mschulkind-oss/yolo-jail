package loopholes

// doorway_test.go pins the DOORWAY half of guestrun.go (docs/design/host-notch-services.md
// HS-D15; "doorway" is that ruling's word for the thin adapter an agent's client talks to): a
// loophole jail daemon that declares `jail_daemon.host_cmd` opens outside the macos-user guest,
// as a launch-owned listener, and stays in the served set.

import (
	"path/filepath"
	"strings"
	"testing"
)

// A DOORWAY WITH A DECLARED HOST ARGV opens outside the macos-user guest: the guest declines its
// jail daemon with that reason, DoorwaysOutside lists it, and the served set still counts it at
// its listen address. Every other runtime runs it in the jail and opens nothing outside. Deleting
// the doorway case from macosUserGuestDecline, or the doorways from ServedJailDaemons, fails this.
func TestADoorwayOpensOutsideTheMacosUserGuestAndStaysServed(t *testing.T) {
	door := JailDaemonSpec{Name: "openai-auth-broker", CallerToken: true, Listen: "127.0.0.1:1460",
		Cmd:     []string{"yolo-jaild", "openai-auth-adapter", "--listen", "{listen}"},
		HostCmd: []string{"yolo", "internal", "daemon", "openai-auth-adapter", "--listen", "{listen}"}}
	plain := JailDaemonSpec{Name: "acme", Cmd: []string{"yolo-jaild", "acme"}}
	specs := []JailDaemonSpec{door, plain}
	for _, rt := range []string{"podman", "container"} {
		if out := DoorwaysOutside(rt, specs); len(out) != 0 {
			t.Errorf("runtime %s opens %v outside its jail", rt, out)
		}
		if runs, _ := JailDaemonsRunIn(rt, specs); len(runs) != 2 {
			t.Errorf("runtime %s runs %v, want both in the jail", rt, runs)
		}
	}
	runs, declined := JailDaemonsRunIn("macos-user", specs)
	if len(runs) != 1 || runs[0].Name != "acme" {
		t.Errorf("the macos-user guest runs %v, want only the daemon with no host argv", runs)
	}
	if len(declined) != 1 || declined[0].Spec.Name != "openai-auth-broker" ||
		!strings.Contains(declined[0].Why, "its doorway opens outside the sandbox") {
		t.Errorf("declined %+v, want the doorway's jail daemon with the doorway's reason", declined)
	}
	outside := DoorwaysOutside("macos-user", specs)
	if len(outside) != 1 || outside[0].Name != "openai-auth-broker" {
		t.Errorf("DoorwaysOutside = %v", outside)
	}
	names, listen := ServedJailDaemonNames("macos-user", specs)
	if strings.Join(names, ",") != "openai-auth-broker,acme" || listen["openai-auth-broker"] != "127.0.0.1:1460" {
		t.Errorf("ServedJailDaemonNames = %v %v, want the doorway served at its listen address", names, listen)
	}
	door.Listen = "127.0.0.1:40000"
	if got := strings.Join(door.ResolvedHostCmd(), " "); got !=
		"yolo internal daemon openai-auth-adapter --listen 127.0.0.1:40000" {
		t.Errorf("ResolvedHostCmd = %q, want {listen} at the served address", got)
	}
}

// A PACK SERVICE OR AN INTERCEPTING DAEMON IS NEVER A DOORWAY, whatever it carries: each is
// declined for its own reason.
func TestOnlyALoopholeDaemonIsADoorway(t *testing.T) {
	host := []string{"yolo", "internal", "daemon", "x", "{listen}"}
	specs := []JailDaemonSpec{
		{Name: "svc", Cmd: []string{"yolo-jaild", "svc"}, Service: true, HostCmd: host},
		{Name: "term", Cmd: []string{"yolo-jaild", "term"}, Intercepts: true, HostCmd: host},
	}
	if out := DoorwaysOutside("macos-user", specs); len(out) != 0 {
		t.Errorf("DoorwaysOutside = %v, want none", out)
	}
}

// THE HOST ARGV IS THE MANIFEST'S, carried from `jail_daemon.host_cmd` through the load and the
// composer with its token unresolved. Deleting either copy (load.go's resolve, runtime.go's
// jailDaemonSpecs) fails this.
func TestTheComposerCarriesTheHostArgvFromTheManifest(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "doorway"))
	writeManifest(t, mod, map[string]any{
		"name": "doorway", "description": "x", "transport": TransportLoopbackTLS,
		"jail_daemon": map[string]any{
			"cmd":    []any{"yolo-jaild", "adapter", "--listen", "{listen}"},
			"listen": "127.0.0.1:1999", "caller_token": true,
			"host_cmd": []any{"yolo", "internal", "daemon", "adapter", "--listen", "{listen}"},
		},
	})
	set := approvedSetFrom(md)
	found := false
	for _, s := range set.JailDaemons(set.Enabled(), "macos-user", nil) {
		if s.Name != "doorway" {
			continue
		}
		found = true
		if got := strings.Join(s.HostCmd, " "); got != "yolo internal daemon adapter --listen {listen}" {
			t.Errorf("the spec's HostCmd = %q, want the manifest's", got)
		}
	}
	if !found {
		t.Fatal("the doorway loophole composed no jail daemon")
	}
}

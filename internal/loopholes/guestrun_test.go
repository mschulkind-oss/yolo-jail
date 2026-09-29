package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// JailDaemonsRunIn is the one split the served set, `yolo check`'s prediction and the macos-user
// arm read (guestrun.go). A container runs everything; macos-user runs the rest and declines
// exactly the three declared shapes, each with its reason.
func TestJailDaemonsRunInSplitsOnlyOnMacosUser(t *testing.T) {
	specs := []JailDaemonSpec{
		{Name: "openai-auth-broker", Cmd: []string{"yolo-jaild", "openai-auth-adapter", "--listen", "{listen}"}, Listen: "127.0.0.1:1460", CallerToken: true},
		{Name: "aws-auth", Cmd: []string{"yolo-jaild", "aws-credential-adapter"}, Listen: "127.0.0.1:1461"},
		{Name: "claude-oauth-broker", Cmd: []string{"yolo-jaild", "oauth-terminator"}, Intercepts: true},
		{Name: "wire-bridge", Cmd: []string{"yolo-jaild", "wire-bridge"}, Service: true},
		{Name: "hello-daemon", Cmd: []string{loopholedecl.JailLoopholeDir("hello-daemon") + "/bin/hello"}},
	}
	for _, rt := range []string{"podman", "container", ""} {
		runs, declined := JailDaemonsRunIn(rt, specs)
		if len(runs) != len(specs) || len(declined) != 0 {
			t.Errorf("runtime %q declined %v", rt, declined)
		}
	}
	runs, declined := JailDaemonsRunIn("macos-user", specs)
	var ran []string
	for _, s := range runs {
		ran = append(ran, s.Name)
	}
	if strings.Join(ran, ",") != "openai-auth-broker,aws-auth" {
		t.Errorf("macos-user runs %v, want the two credential adapters", ran)
	}
	want := map[string]string{
		"claude-oauth-broker": "--add-host",
		"wire-bridge":         "host half",
		"hello-daemon":        "loophole mount",
	}
	if len(declined) != len(want) {
		t.Fatalf("declined %v", declined)
	}
	for _, d := range declined {
		if !strings.Contains(d.Why, want[d.Spec.Name]) {
			t.Errorf("%s declined as %q, want a reason naming %q", d.Spec.Name, d.Why, want[d.Spec.Name])
		}
	}
	names, listen := ServedJailDaemonNames("macos-user", specs)
	if strings.Join(names, ",") != "openai-auth-broker,aws-auth" || listen["aws-auth"] != "127.0.0.1:1461" {
		t.Errorf("ServedJailDaemonNames = %v %v", names, listen)
	}
}

// THE INTERCEPT FLAG IS THE MANIFEST'S. The composer copies it from the loophole's `intercepts`
// list, so the macos-user decline keys on the declaration (the same fact Apple Container's skip
// keys on), never on a loophole name. Deleting the copy in jailDaemonSpecs fails this.
func TestTheComposerCarriesTheInterceptFlagFromTheManifest(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "broker"))
	if err := os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("-----FAKE CA-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mod, map[string]any{
		"name": "broker", "description": "x", "transport": TransportLoopbackTLS,
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"broker_ip":  "127.0.0.1", "ca_cert": "ca.crt",
		"jail_daemon": map[string]any{"cmd": []any{"yolo-jaild", "oauth-terminator"}},
	})
	plain := mkdir(t, filepath.Join(md, "plain"))
	writeManifest(t, plain, map[string]any{
		"name": "plain", "description": "x", "transport": TransportLoopbackTLS,
		"jail_daemon": map[string]any{"cmd": []any{"yolo-jaild", "acme"}},
	})
	set := approvedSetFrom(md)
	got := map[string]bool{}
	for _, s := range set.JailDaemons(set.Enabled(), "podman", nil) {
		got[s.Name] = s.Intercepts
	}
	if v, ok := got["broker"]; !ok || !v {
		t.Errorf("the intercepting loophole's spec does not carry Intercepts: %v", got)
	}
	if v, ok := got["plain"]; !ok || v {
		t.Errorf("a plain loophole's spec carries Intercepts: %v", got)
	}
	runs, _ := JailDaemonsRunIn("macos-user", set.JailDaemons(set.Enabled(), "macos-user", nil))
	for _, s := range runs {
		if s.Name == "broker" {
			t.Error("macos-user runs an intercepting loophole's jail daemon")
		}
	}
}

package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// moduleProgram writes a module directory holding bin/hello with body, mode 0755, and returns
// the directory.
func moduleProgram(t *testing.T, body []byte) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "hello"), body, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// moduleSpec is a loophole daemon whose argv is `{jail_loophole_dir}/bin/hello`, as the composer
// builds it: Cmd at the container mount, ModuleCmd with the token, ModuleDir dir.
func moduleSpec(name, dir string) JailDaemonSpec {
	return JailDaemonSpec{Name: name, Cmd: []string{loopholedecl.JailLoopholeDir(name) + "/bin/hello"},
		ModuleDir: dir, ModuleCmd: []string{loopholedecl.TokenJailLoopholeDir + "/bin/hello"}}
}

// JailDaemonsRunIn is the one split the served set, `yolo check`'s prediction and the macos-user
// arm read (guestrun.go). A container runs everything; macos-user runs the rest and declines
// exactly the declared shapes, each with its reason. A daemon naming its module directory runs
// when the composer recorded the directory and the program is not a Linux executable.
func TestJailDaemonsRunInSplitsOnlyOnMacosUser(t *testing.T) {
	script := moduleProgram(t, []byte("#!/bin/sh\necho hello\n"))
	elf := moduleProgram(t, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0})
	specs := []JailDaemonSpec{
		{Name: "openai-auth-broker", Cmd: []string{"yolo-jaild", "openai-auth-adapter", "--listen", "{listen}"}, Listen: "127.0.0.1:1460", CallerToken: true},
		{Name: "aws-auth", Cmd: []string{"yolo-jaild", "aws-credential-adapter"}, Listen: "127.0.0.1:1461"},
		{Name: "claude-oauth-broker", Cmd: []string{"yolo-jaild", "oauth-terminator"}, Intercepts: true},
		{Name: "wire-bridge", Cmd: []string{"yolo-jaild", "wire-bridge"}, Service: true, HostHalf: true,
			ServesAdaptation: true, Endpoint: "wire-bridge.endpoint"},
		moduleSpec("hello-daemon", script),
		{Name: "hello-unplaced", Cmd: []string{loopholedecl.JailLoopholeDir("hello-unplaced") + "/bin/hello"}},
		moduleSpec("hello-elf", elf),
		{Name: "tool", Cmd: []string{loopholedecl.JailBinaryPath("tool", "toold")}},
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
	if strings.Join(ran, ",") != "openai-auth-broker,aws-auth,hello-daemon" {
		t.Errorf("macos-user runs %v, want the two credential adapters and the module-dir script", ran)
	}
	want := map[string]string{
		"claude-oauth-broker": "--add-host",
		"wire-bridge":         "host half",
		"hello-unplaced":      "loophole mount",
		"hello-elf":           "Linux executable",
		"tool":                "binary the launch mounts",
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
	if strings.Join(names, ",") != "openai-auth-broker,aws-auth,hello-daemon" || listen["aws-auth"] != "127.0.0.1:1461" {
		t.Errorf("ServedJailDaemonNames = %v %v", names, listen)
	}
}

// THE ONE GUEST RULE FOR A PACK SERVICE (docs/design/jail-daemon-on-macos-user-plan.md JD-9,
// under OQ-DP8: "if you would have run it in the jail container, you run it on the guest"). Its
// daemon is declined only when its admitted host half serves an adaptation instead, or when it
// publishes an endpoint file at a container path. A service with no host half, one whose host
// half the launch refused (HostHalf cleared), a pure worker (no endpoint) serving no adaptation, and a worker
// declaring both halves all run in the guest.
func TestTheGuestRunsAServiceThatHasNoHostHalf(t *testing.T) {
	specs := []JailDaemonSpec{
		{Name: "bridged", Cmd: []string{"yolo-jaild", "wire-bridge"}, Service: true, HostHalf: true, ServesAdaptation: true},
		{Name: "plain", Cmd: []string{"acme-svc"}, Service: true},
		{Name: "refused-host", Cmd: []string{"acme-adapt"}, Service: true, ServesAdaptation: true},
		{Name: "publishing", Cmd: []string{"acme-pub"}, Service: true, Endpoint: "acme-pub.endpoint"},
		{Name: "worker", Cmd: []string{"acme-worker"}, Service: true},
		{Name: "worker-both", Cmd: []string{"acme-worker"}, Service: true, HostHalf: true},
	}
	runs, declined := JailDaemonsRunIn("macos-user", specs)
	var ran []string
	for _, s := range runs {
		ran = append(ran, s.Name)
	}
	if strings.Join(ran, ",") != "plain,refused-host,worker,worker-both" {
		t.Errorf("the guest runs %v, want every service but the bridged one and the publishing one", ran)
	}
	why := map[string]string{}
	for _, d := range declined {
		why[d.Spec.Name] = d.Why
	}
	if len(why) != 2 {
		t.Fatalf("declined %v", declined)
	}
	if !strings.Contains(why["bridged"], "host half") {
		t.Errorf("a service whose host half serves its adaptation is declined as %q", why["bridged"])
	}
	if !strings.Contains(why["publishing"], "/run/yolo-services/acme-pub.endpoint") {
		t.Errorf("a service publishing a container endpoint is declined as %q", why["publishing"])
	}
}

// THE COMPOSER RECORDS WHERE A LOOPHOLE'S MODULE DIRECTORY IS, and the argv before it is placed:
// ModuleDir from the loaded record's Path, ModuleCmd with `{jail_loophole_dir}` put back, while
// Cmd keeps the container mount point load resolved, so a container's payload is unchanged.
// Deleting either copy in jailDaemonSpecs fails this.
func TestTheComposerRecordsTheModuleDirAndItsUnplacedArgv(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "hello"))
	writeManifest(t, mod, map[string]any{
		"name": "hello", "description": "x", "transport": TransportNone,
		"jail_daemon": map[string]any{"cmd": []any{"{jail_loophole_dir}/bin/hello", "--conf={jail_loophole_dir}/hello.conf"}},
	})
	set := approvedSetFrom(md)
	specs := set.JailDaemons(set.Enabled(), "podman", nil)
	if len(specs) != 1 {
		t.Fatalf("composed %+v, want the one daemon", specs)
	}
	s := specs[0]
	lp := set.Enabled()[0]
	if s.ModuleDir == "" || s.ModuleDir != lp.Path {
		t.Errorf("ModuleDir = %q, want the record's Path %q", s.ModuleDir, lp.Path)
	}
	if got := strings.Join(s.ModuleCmd, " "); got != "{jail_loophole_dir}/bin/hello --conf={jail_loophole_dir}/hello.conf" {
		t.Errorf("ModuleCmd = %q, want the declared argv with the token left in", got)
	}
	if got := strings.Join(s.Cmd, " "); got != "/etc/yolo-jail/loopholes/hello/bin/hello --conf=/etc/yolo-jail/loopholes/hello/hello.conf" {
		t.Errorf("Cmd = %q, want the container mount point load resolved", got)
	}
	payload, err := jsonx.DumpsCompact(JailDaemonPayload(specs))
	if err != nil {
		t.Fatal(err)
	}
	if want := `[{"name": "hello", "cmd": ["/etc/yolo-jail/loopholes/hello/bin/hello", "--conf=/etc/yolo-jail/loopholes/hello/hello.conf"], "restart": "on-failure"}]`; payload != want {
		t.Errorf("the container payload changed:\n got %s\nwant %s", payload, want)
	}
}

// InGuest places the module directory at the guest's copy, from ModuleCmd: every mention of the
// token, mid-word included, and nothing else. A spec naming no module directory is unchanged.
func TestInGuestPlacesTheModuleDirAtTheGuestCopy(t *testing.T) {
	s := JailDaemonSpec{Name: "hello",
		Cmd:       moduleDirTestCmd("/etc/yolo-jail/loopholes/hello"),
		ModuleDir: "/host/tree/local/loopholes/hello",
		ModuleCmd: moduleDirTestCmd(loopholedecl.TokenJailLoopholeDir)}
	got := s.InGuest("/var/yolo-jail/packs/c/local/loopholes/hello")
	if want := moduleDirTestCmd("/var/yolo-jail/packs/c/local/loopholes/hello"); strings.Join(got.Cmd, " ") != strings.Join(want, " ") {
		t.Errorf("InGuest Cmd = %v, want %v", got.Cmd, want)
	}
	if strings.Join(s.Cmd, " ") != strings.Join(moduleDirTestCmd("/etc/yolo-jail/loopholes/hello"), " ") {
		t.Errorf("InGuest edited its receiver's Cmd: %v", s.Cmd)
	}
	plain := JailDaemonSpec{Name: "relay", Cmd: []string{"yolo-jaild", "relay"}, ModuleDir: "/host/x",
		ModuleCmd: []string{"yolo-jaild", "relay"}}
	if out := plain.InGuest("/var/elsewhere"); strings.Join(out.Cmd, " ") != "yolo-jaild relay" {
		t.Errorf("a spec naming no module dir was rewritten: %v", out.Cmd)
	}
	if (JailDaemonSpec{Cmd: s.Cmd, ModuleCmd: s.ModuleCmd}).NamesModuleDir() {
		t.Error("a spec with no ModuleDir claims to name one")
	}
}

func moduleDirTestCmd(dir string) []string {
	return []string{dir + "/bin/hello", "--conf=" + dir + "/hello.conf", "plain"}
}

// moduleDirArgv inverts exactly load's substitution: the loophole's own mount point, where it ends
// a path segment, becomes the token again; a sibling loophole's mount whose name starts with this
// one's is left alone, so the guest declines it as a container path.
func TestModuleDirArgvPutsBackOnlyThisLoopholesMount(t *testing.T) {
	root := loopholedecl.JailLoopholeDir("hello")
	got := moduleDirArgv("hello", []string{root, root + "/bin/x", "a=" + root + "/c", root + "-extra/bin", "/usr/bin/env"})
	want := []string{"{jail_loophole_dir}", "{jail_loophole_dir}/bin/x", "a={jail_loophole_dir}/c", root + "-extra/bin", "/usr/bin/env"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("moduleDirArgv = %v, want %v", got, want)
	}
	argv := []string{root + "/run", root + "-extra/bin"}
	sibling := JailDaemonSpec{Name: "hello", Cmd: argv, ModuleDir: "/host/x", ModuleCmd: moduleDirArgv("hello", argv)}
	if _, declined := JailDaemonsRunIn("macos-user", []JailDaemonSpec{sibling}); len(declined) != 1 {
		t.Errorf("a daemon naming a sibling loophole's mount runs in the guest")
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

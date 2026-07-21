package loopholes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func argsFor(root string, runtime string) []string {
	loaded := Discover(DiscoverOptions{Root: root, RootSet: true, IncludeBundled: false})
	return RuntimeArgsFor(loaded, runtime)
}

func joinArgs(args []string) string { return strings.Join(args, " ") }

func containsArg(args []string, want string) bool { return containsStr(args, want) }

func countArg(args []string, want string) int {
	n := 0
	for _, a := range args {
		if a == want {
			n++
		}
	}
	return n
}

func TestRuntimeArgsInterceptAndCA(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "broker"))
	ca := filepath.Join(mod, "ca.crt")
	if err := os.WriteFile(ca, []byte("-----FAKE CA-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mod, map[string]any{
		"name": "broker", "description": "x",
		"intercepts": []any{map[string]any{"host": "example.test"}, map[string]any{"host": "api.example.test"}},
		"broker_ip":  "10.0.0.1",
		"ca_cert":    "ca.crt",
		"jail_env":   map[string]any{"FOO": "bar"},
	})
	args := argsFor(md, "")
	if countArg(args, "--add-host") != 2 {
		t.Errorf("want 2 --add-host, got %d in %v", countArg(args, "--add-host"), args)
	}
	if !containsArg(args, "example.test:10.0.0.1") || !containsArg(args, "api.example.test:10.0.0.1") {
		t.Errorf("add-host targets missing: %v", args)
	}
	resolvedCA := resolvePath(ca)
	if !containsArg(args, resolvedCA+":/etc/yolo-jail/loopholes/broker/ca.crt:ro") {
		t.Errorf("CA mount missing: %v", args)
	}
	if !containsArg(args, "FOO=bar") {
		t.Errorf("jail_env missing: %v", args)
	}
	found := false
	for _, a := range args {
		if strings.HasPrefix(a, "NODE_EXTRA_CA_CERTS=") {
			found = true
		}
	}
	if !found {
		t.Errorf("NODE_EXTRA_CA_CERTS missing: %v", args)
	}
}

// TestRuntimeArgsAbsoluteCACert is the regression for the filepath.Join-vs-
// pathlib trap: an ABSOLUTE ca_cert must be used verbatim (pathlib `module_path
// / abs` discards module_path), NOT concatenated as "<module>/<abs>". A bogus
// concatenated path would fail HasCA() and silently drop the CA mount + env.
func TestRuntimeArgsAbsoluteCACert(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "broker"))
	// The CA lives OUTSIDE the module dir, referenced by an absolute path.
	caDir := t.TempDir()
	absCA := filepath.Join(caDir, "shared-ca.crt")
	if err := os.WriteFile(absCA, []byte("-----FAKE CA-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mod, map[string]any{
		"name": "broker", "description": "x",
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"broker_ip":  "10.0.0.1",
		"ca_cert":    absCA,
	})
	args := argsFor(md, "")
	// Host side of the CA mount is the absolute path verbatim (resolved); the
	// container side is always the fixed name ca.crt ("{dir}/ca.crt").
	want := resolvePath(absCA) + ":/etc/yolo-jail/loopholes/broker/ca.crt:ro"
	if !containsArg(args, want) {
		t.Errorf("absolute CA mount missing (join trap?): want %q in %v", want, args)
	}
	// And the concatenated-path bug must NOT appear.
	bogus := resolvePath(filepath.Join(mod, absCA))
	for _, a := range args {
		if strings.HasPrefix(a, bogus+":") {
			t.Errorf("found concatenated <module>/<abs> path (the bug): %q", a)
		}
	}
}

func TestRuntimeArgsNoCANoEnv(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "plain"))
	writeManifest(t, mod, map[string]any{
		"name": "plain", "description": "x",
		"intercepts": []any{map[string]any{"host": "plain.test"}},
	})
	got := argsFor(md, "")
	want := []string{"--add-host", "plain.test:host-gateway"}
	if !equalStrings(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRuntimeArgsSkipTLSOnAppleContainer(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "broker"))
	if err := os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("-----FAKE CA-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mod, map[string]any{
		"name": "broker", "description": "x",
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"broker_ip":  "10.0.0.1", "ca_cert": "ca.crt", "jail_env": map[string]any{"FOO": "bar"},
	})
	podman := argsFor(md, "podman")
	if !containsArg(podman, "--add-host") || !containsArg(podman, "FOO=bar") {
		t.Errorf("podman should wire fully: %v", podman)
	}
	ac := argsFor(md, "container")
	if len(ac) != 0 {
		t.Errorf("apple container should skip tls-intercept entirely, got %v", ac)
	}
}

func TestRuntimeArgsSkipConfigBacked(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	cfg := orderedFromPairs("journal", map[string]any{"description": "x"})
	loaded := Discover(DiscoverOptions{Root: filepath.Join(md, "empty"), RootSet: true, LoopholesConfig: cfg})
	if got := RuntimeArgsFor(loaded, ""); len(got) != 0 {
		t.Errorf("config-backed should emit nothing, got %v", got)
	}
}

func TestMultipleLoopholesMergeCAPaths(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	for _, name := range []string{"a", "b"} {
		mod := mkdir(t, filepath.Join(md, name))
		if err := os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("ca-for-"+name), 0o644); err != nil {
			t.Fatal(err)
		}
		writeManifest(t, mod, map[string]any{"name": name, "description": "x", "ca_cert": "ca.crt"})
	}
	args := argsFor(md, "")
	nodeCA := ""
	for _, a := range args {
		if strings.HasPrefix(a, "NODE_EXTRA_CA_CERTS=") {
			nodeCA = a
		}
	}
	if !strings.Contains(nodeCA, "/etc/yolo-jail/loopholes/a/ca.crt") ||
		!strings.Contains(nodeCA, "/etc/yolo-jail/loopholes/b/ca.crt") {
		t.Errorf("NODE_EXTRA_CA_CERTS = %q", nodeCA)
	}
}

func TestRuntimeArgsMountsDirForJailDaemon(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "jd-mod"))
	if err := os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("ca"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "jail.py"), []byte("# jail daemon impl"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mod, map[string]any{
		"name": "jd-mod", "description": "x",
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"broker_ip":  "127.0.0.1", "ca_cert": "ca.crt",
		"jail_daemon": map[string]any{"cmd": []any{"python3", "/etc/yolo-jail/loopholes/jd-mod/jail.py"}, "restart": "on-failure"},
	})
	args := argsFor(md, "")
	// A dir mount for the loophole dir; CA rides that mount (single -v).
	mountLines := 0
	for _, a := range args {
		if strings.Contains(a, "loopholes/jd-mod") && strings.HasSuffix(a, ":ro") {
			mountLines++
		}
	}
	if mountLines == 0 {
		t.Errorf("expected a :ro dir mount: %v", args)
	}
	jdEnv := ""
	for _, a := range args {
		if strings.HasPrefix(a, "YOLO_JAIL_DAEMONS=") {
			jdEnv = strings.TrimPrefix(a, "YOLO_JAIL_DAEMONS=")
		}
	}
	if jdEnv == "" {
		t.Fatalf("YOLO_JAIL_DAEMONS missing: %v", args)
	}
	// Payload must be exactly one daemon spec (compare semantically).
	wantPayload := `[{"name": "jd-mod", "cmd": ["python3", "/etc/yolo-jail/loopholes/jd-mod/jail.py"], "restart": "on-failure"}]`
	if jdEnv != wantPayload {
		t.Errorf("payload = %q, want %q", jdEnv, wantPayload)
	}
	nodeCA := ""
	for _, a := range args {
		if strings.HasPrefix(a, "NODE_EXTRA_CA_CERTS=") {
			nodeCA = a
		}
	}
	if !strings.Contains(nodeCA, "/etc/yolo-jail/loopholes/jd-mod/ca.crt") {
		t.Errorf("CA via dir mount missing: %q", nodeCA)
	}
}

func TestNoFileOverlayOnDirMount(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "has-jail"))
	writeManifest(t, mod, map[string]any{
		"name": "has-jail", "description": "x",
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"broker_ip":  "127.0.0.1", "ca_cert": "{state}/ca.crt",
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	// Fake a state dir with a ca.crt so state mount + CA both apply.
	stateRoot := t.TempDir()
	stateDir := mkdir(t, filepath.Join(stateRoot, "has-jail"))
	if err := os.WriteFile(filepath.Join(stateDir, "ca.crt"), []byte("ca"), 0o644); err != nil {
		t.Fatal(err)
	}
	orig := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	defer func() { StateDirFor = orig }()

	args := argsFor(md, "")
	mountSources := 0
	for _, a := range args {
		if strings.Contains(a, ":/etc/yolo-jail/loopholes/has-jail") {
			mountSources++
		}
	}
	if mountSources != 1 {
		t.Errorf("expected 1 dir mount, got %d: %v", mountSources, args)
	}
	nodeCA := ""
	for _, a := range args {
		if strings.HasPrefix(a, "NODE_EXTRA_CA_CERTS=") {
			nodeCA = a
		}
	}
	if nodeCA != "NODE_EXTRA_CA_CERTS=/var/lib/yolo-jail/loopholes/has-jail/ca.crt" {
		t.Errorf("NODE_EXTRA_CA_CERTS = %q", nodeCA)
	}
}

func TestHostBindMountsEmittedAndReadonly(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	tmp := t.TempDir()
	rw := filepath.Join(tmp, "rw-sock")
	ro := filepath.Join(tmp, "ro-file")
	os.WriteFile(rw, nil, 0o644)
	os.WriteFile(ro, nil, 0o644)
	mod := mkdir(t, filepath.Join(md, "audio-like"))
	writeManifest(t, mod, map[string]any{
		"name": "audio-like", "description": "x", "transport": "none",
		"host_bind_mounts": []any{
			map[string]any{"host": rw, "container": "/run/pulse/native", "readonly": false},
			map[string]any{"host": ro, "container": "/etc/some-config", "readonly": true},
		},
	})
	args := argsFor(md, "")
	joined := joinArgs(args)
	if !strings.Contains(joined, rw+":/run/pulse/native") {
		t.Errorf("rw mount missing: %v", args)
	}
	if !strings.Contains(joined, ro+":/etc/some-config:ro") {
		t.Errorf("ro mount missing: %v", args)
	}
	if strings.Contains(joined, rw+":/run/pulse/native:ro") {
		t.Errorf("rw mount must not carry :ro: %v", args)
	}
}

func TestHostBindMountsSkipWhenSourceMissing(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	gone := filepath.Join(t.TempDir(), "gone.sock")
	mod := mkdir(t, filepath.Join(md, "audio-like"))
	writeManifest(t, mod, map[string]any{
		"name": "audio-like", "description": "x", "transport": "none",
		"host_bind_mounts": []any{map[string]any{"host": gone, "container": "/run/pulse/native"}},
	})
	args := argsFor(md, "")
	if strings.Contains(joinArgs(args), gone+":/run/pulse/native") {
		t.Errorf("missing source should be skipped: %v", args)
	}
}

func TestHostDevicesEmittedAndSkipped(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	ok := mkdir(t, filepath.Join(md, "snd-like"))
	writeManifest(t, ok, map[string]any{
		"name": "snd-like", "description": "x", "transport": "none",
		"host_devices": []any{"/dev/null"},
	})
	args := argsFor(md, "")
	if !hasPair(args, "--device", "/dev/null") {
		t.Errorf("expected --device /dev/null: %v", args)
	}

	md2 := modsDir(t)
	bad := mkdir(t, filepath.Join(md2, "snd-like"))
	writeManifest(t, bad, map[string]any{
		"name": "snd-like", "description": "x", "transport": "none",
		"host_devices": []any{"/dev/this-does-not-exist-xyz"},
	})
	if containsArg(argsFor(md2, ""), "--device") {
		t.Errorf("missing device node should be skipped")
	}
}

func TestHostDevicesMustBeNonEmptyStrings(t *testing.T) {
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "bad-devs"))
	writeManifest(t, mod, map[string]any{"name": "bad-devs", "description": "x", "host_devices": []any{""}})
	entries := ValidateLoopholes(md, true, false)
	if len(entries) != 1 || entries[0].Loophole != nil || !contains(entries[0].Err, "host_devices[0]") {
		t.Errorf("expected host_devices[0] error, got %+v", entries)
	}
}

func TestHostDevicesSkippedInJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test-1.0.0")
	md := modsDir(t)
	tmp := t.TempDir()
	sock := filepath.Join(tmp, "pulse-native")
	os.WriteFile(sock, nil, 0o644)
	mod := mkdir(t, filepath.Join(md, "audio-like"))
	writeManifest(t, mod, map[string]any{
		"name": "audio-like", "description": "x", "transport": "none",
		"host_devices":     []any{"/dev/null"},
		"host_bind_mounts": []any{map[string]any{"host": sock, "container": "/tmp", "readonly": false}},
		"jail_env":         map[string]any{"PULSE_SERVER": "unix:/run/pulse/native"},
	})
	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, IncludeBundled: false})
	if !loaded[0].Active() {
		t.Fatalf("should be active in-jail (container path /tmp exists)")
	}
	args := RuntimeArgsFor(loaded, "")
	if containsArg(args, "--device") {
		t.Errorf("device passthrough must be skipped in-jail: %v", args)
	}
	if !hasPair(args, "-v", sock+":/tmp") {
		t.Errorf("bind mount should still wire: %v", args)
	}
	if !hasPair(args, "-e", "PULSE_SERVER=unix:/run/pulse/native") {
		t.Errorf("jail_env should still wire: %v", args)
	}
}

func TestInactiveLoopholesSkippedInRuntimeArgs(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "inactive-mod"))
	os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("ca"), 0o644)
	writeManifest(t, mod, map[string]any{
		"name": "inactive-mod", "description": "x",
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"ca_cert":    "ca.crt",
		"requires":   map[string]any{"command_on_path": "xyz-definitely-missing"},
	})
	if got := argsFor(md, ""); len(got) != 0 {
		t.Errorf("inactive loophole should emit nothing, got %v", got)
	}
}

func TestManifestHostDaemonSpecs(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "with-hd"))
	writeManifest(t, mod, map[string]any{
		"name": "with-hd", "description": "the broker host daemon",
		"host_daemon": map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
	})
	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, IncludeBundled: false})
	specs := ManifestHostDaemonSpecs(loaded)
	if specs.Len() != 1 {
		t.Fatalf("want 1 spec, got %d", specs.Len())
	}
	got, _ := jsonxDump(specs)
	want := `{"with-hd": {"command": ["daemon", "--socket", "{socket}"], "description": "the broker host daemon"}}`
	if got != want {
		t.Errorf("specs = %s, want %s", got, want)
	}
}

func TestDoctorChecks(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mkAndWrite := func(name string, extra map[string]any) {
		mod := mkdir(t, filepath.Join(md, name))
		data := map[string]any{"name": name, "description": "x"}
		for k, v := range extra {
			data[k] = v
		}
		writeManifest(t, mod, data)
	}
	mkAndWrite("nocmd", nil)
	mkAndWrite("truecmd", map[string]any{"doctor_cmd": []any{"true"}})
	mkAndWrite("falsecmd", map[string]any{"doctor_cmd": []any{"false"}})
	mkAndWrite("missing", map[string]any{"doctor_cmd": []any{"/no/such/binary/anywhere"}})

	loaded := Discover(DiscoverOptions{Root: md, RootSet: true, IncludeBundled: false})
	results := RunDoctorChecks(loaded, 0)
	byName := map[string]DoctorResult{}
	for _, r := range results {
		byName[r.Loophole.Name] = r
	}
	if byName["nocmd"].RC != nil {
		t.Errorf("nocmd RC should be nil")
	}
	if byName["truecmd"].RC == nil || *byName["truecmd"].RC != 0 {
		t.Errorf("truecmd RC = %v", byName["truecmd"].RC)
	}
	if byName["falsecmd"].RC == nil || *byName["falsecmd"].RC != 1 {
		t.Errorf("falsecmd RC = %v", byName["falsecmd"].RC)
	}
	if byName["missing"].RC != nil {
		t.Errorf("missing binary RC should be nil, got %v", byName["missing"].RC)
	}
}

// helpers ------------------------------------------------------------------

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func hasPair(args []string, a, b string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == a && args[i+1] == b {
			return true
		}
	}
	return false
}

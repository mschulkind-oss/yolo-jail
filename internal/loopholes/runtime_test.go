package loopholes

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// argsFor builds the container argv for every ENABLED loophole module under root.
//
// THE UNRESTRICTED LOADER PLUS AN APPROVING GATE, and both halves are deliberate.
//
// Unrestricted (LoadLoophole, not LoadPackLoophole) because this file's subject is the
// ARGV BUILDER over a resolved record — every field it can emit, `jail_env`, an absolute
// `ca_cert`, a writable bind, `publishes: "endpoint"` included. Packs are the only module
// source discovery has left since `bundled_loopholes/` was retired
// (docs/reference/loophole-system.md OQ-BP4), and the pack-shipped subset refuses several of
// those, so routing these through discovery would silently turn "does RuntimeArgsFor emit
// the device flag" into "does the subset permit a device" — a question packshipped_test.go
// already owns, and one whose failure looks identical here (the loophole vanishes).
//
// Approving gate because LoadLoophole's fail-safe default label is SourcePack, which the
// ungated package-level RuntimeArgsFor withholds outright: without it every assertion below
// would pass against an empty argv.
func argsFor(root string, runtime string) []string {
	set := approvedSetFrom(root)
	return set.RuntimeArgsFor(set.Enabled(), runtime)
}

// approvedSetFrom loads root's module dirs through the unrestricted loader and hands the
// result a gate that approves each one. Unloadable modules are skipped, matching
// discovery's warn-and-continue contract.
func approvedSetFrom(root string) Set {
	var all []*Loophole
	gate := map[string]bool{}
	for _, m := range moduleDirsUnder(root) {
		lp, err := LoadLoophole(m.Dir)
		if err != nil {
			continue
		}
		all = append(all, lp)
		gate[lp.Path] = true
	}
	return Set{all: all, gate: gate}
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

// TestAppleContainerSkipKeysOnIntercepts: Apple Container has no --add-host
// (apple/container#673), so a loophole that needs one is skipped whole. The key is
// the INTERCEPT LIST — the thing that produces the --add-host flags — not the
// transport string, which is how "tls-intercept" could retire. Both manifests here
// declare the one surviving daemon transport; only the intercepting one is
// skipped.
func TestAppleContainerSkipKeysOnIntercepts(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "broker"))
	if err := os.WriteFile(filepath.Join(mod, "ca.crt"), []byte("-----FAKE CA-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mod, map[string]any{
		"name": "broker", "description": "x", "transport": TransportLoopbackTLS,
		"intercepts": []any{map[string]any{"host": "example.test"}},
		"broker_ip":  "10.0.0.1", "ca_cert": "ca.crt", "jail_env": map[string]any{"FOO": "bar"},
	})
	podman := argsFor(md, "podman")
	if !containsArg(podman, "--add-host") || !containsArg(podman, "FOO=bar") {
		t.Errorf("podman should wire fully: %v", podman)
	}
	ac := argsFor(md, "container")
	if len(ac) != 0 {
		t.Errorf("apple container should skip an intercepting loophole entirely, got %v", ac)
	}

	// Same transport, no intercepts: NOT skipped, or the re-key would have turned
	// "skip what needs --add-host" into "skip every loophole".
	plain := mkdir(t, filepath.Join(md, "plain"))
	writeManifest(t, plain, map[string]any{
		"name": "plain", "description": "x", "transport": TransportLoopbackTLS,
		"jail_env": map[string]any{"BAR": "baz"},
	})
	if ac := argsFor(md, "container"); !containsArg(ac, "BAR=baz") {
		t.Errorf("a non-intercepting loophole must survive apple container, got %v", ac)
	}
}

func TestRuntimeArgsSkipConfigBacked(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	cfg := orderedFromPairs("journal", map[string]any{"description": "x"})
	loaded := discoverWithConfig(filepath.Join(md, "empty"), false, cfg)
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

// TestStateFilesNarrowsTheStateMount is the mechanism half of issue #33: with
// `state_files` declared, the whole-directory state mount is replaced by one
// :ro file mount per declared entry, and everything else in the state dir —
// notably a private key — never crosses.
func TestStateFilesNarrowsTheStateMount(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "narrowed"))
	writeManifest(t, mod, map[string]any{
		"name": "narrowed", "description": "x",
		"intercepts":  []any{map[string]any{"host": "example.test"}},
		"broker_ip":   "127.0.0.1",
		"ca_cert":     "{state}/ca.crt",
		"state_files": []any{"ca.crt", "server.crt", "server.key"},
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	stateRoot := t.TempDir()
	stateDir := mkdir(t, filepath.Join(stateRoot, "narrowed"))
	for _, name := range []string{"ca.crt", "ca.key", "ca.srl", "server.crt", "server.key"} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	orig := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	defer func() { StateDirFor = orig }()

	args := argsFor(md, "")
	// The state DIRECTORY itself is no longer a mount source.
	if containsArg(args, stateDir+":/var/lib/yolo-jail/loopholes/narrowed:ro") {
		t.Errorf("whole state dir still mounted: %v", args)
	}
	for _, name := range []string{"ca.crt", "server.crt", "server.key"} {
		want := filepath.Join(stateDir, name) + ":/var/lib/yolo-jail/loopholes/narrowed/" + name + ":ro"
		if !containsArg(args, want) {
			t.Errorf("declared state file not mounted: want %q in %v", want, args)
		}
	}
	for _, undeclared := range []string{"ca.key", "ca.srl"} {
		if strings.Contains(joinArgs(args), undeclared) {
			t.Errorf("undeclared state file %q crossed into the jail: %v", undeclared, args)
		}
	}
	// The CA still resolves through the state mount, so NODE_EXTRA_CA_CERTS is
	// unchanged by the narrowing.
	nodeCA := ""
	for _, a := range args {
		if strings.HasPrefix(a, "NODE_EXTRA_CA_CERTS=") {
			nodeCA = a
		}
	}
	if nodeCA != "NODE_EXTRA_CA_CERTS=/var/lib/yolo-jail/loopholes/narrowed/ca.crt" {
		t.Errorf("NODE_EXTRA_CA_CERTS = %q", nodeCA)
	}
}

// A declared state file that does not exist host-side must be SKIPPED, never
// emitted as a -v: the container runtime materializes a missing bind source as
// an empty DIRECTORY, which would shadow the file the jail daemon waits for.
func TestStateFilesSkipMissingSource(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "narrowed"))
	writeManifest(t, mod, map[string]any{
		"name": "narrowed", "description": "x", "transport": "none",
		"state_files": []any{"server.crt", "not-generated-yet.crt"},
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	stateRoot := t.TempDir()
	stateDir := mkdir(t, filepath.Join(stateRoot, "narrowed"))
	if err := os.WriteFile(filepath.Join(stateDir, "server.crt"), []byte("crt"), 0o600); err != nil {
		t.Fatal(err)
	}
	orig := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	defer func() { StateDirFor = orig }()

	args := argsFor(md, "")
	if !containsArg(args, filepath.Join(stateDir, "server.crt")+":/var/lib/yolo-jail/loopholes/narrowed/server.crt:ro") {
		t.Errorf("present state file should still mount: %v", args)
	}
	if strings.Contains(joinArgs(args), "not-generated-yet.crt") {
		t.Errorf("missing state file must be skipped, not emitted: %v", args)
	}
}

// An empty/absent state_files keeps the historical whole-directory mount, so
// external manifests that never heard of the key do not change meaning.
func TestNoStateFilesKeepsWholeDirMount(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "wide"))
	writeManifest(t, mod, map[string]any{
		"name": "wide", "description": "x", "transport": "none",
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	stateRoot := t.TempDir()
	stateDir := mkdir(t, filepath.Join(stateRoot, "wide"))
	orig := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	defer func() { StateDirFor = orig }()

	args := argsFor(md, "")
	if !containsArg(args, stateDir+":/var/lib/yolo-jail/loopholes/wide:ro") {
		t.Errorf("whole state dir mount missing: %v", args)
	}
}

// A state_files list that FORGETS the CA must not silently drop CA trust: the
// CA falls back to its own single-file mount under the module dir, and
// NODE_EXTRA_CA_CERTS follows it. Narrowing the mount can therefore never
// break TLS verification by omission — the failure mode an author is most
// likely to introduce.
func TestStateFilesOmittingCAStillTrustsIt(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	mod := mkdir(t, filepath.Join(md, "no-ca-declared"))
	writeManifest(t, mod, map[string]any{
		"name": "no-ca-declared", "description": "x",
		"intercepts":  []any{map[string]any{"host": "example.test"}},
		"broker_ip":   "127.0.0.1",
		"ca_cert":     "{state}/ca.crt",
		"state_files": []any{"server.crt", "server.key"},
		"jail_daemon": map[string]any{"cmd": []any{"true"}, "restart": "no"},
	})
	stateRoot := t.TempDir()
	stateDir := mkdir(t, filepath.Join(stateRoot, "no-ca-declared"))
	for _, name := range []string{"ca.crt", "ca.key", "server.crt", "server.key"} {
		if err := os.WriteFile(filepath.Join(stateDir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	orig := StateDirFor
	StateDirFor = func(name string) string { return filepath.Join(stateRoot, name) }
	defer func() { StateDirFor = orig }()

	args := argsFor(md, "")
	want := filepath.Join(stateDir, "ca.crt") + ":/etc/yolo-jail/loopholes/no-ca-declared/ca.crt:ro"
	if !containsArg(args, want) {
		t.Errorf("CA should fall back to its own mount: want %q in %v", want, args)
	}
	if !containsArg(args, "NODE_EXTRA_CA_CERTS=/etc/yolo-jail/loopholes/no-ca-declared/ca.crt") {
		t.Errorf("NODE_EXTRA_CA_CERTS should follow the fallback mount: %v", args)
	}
	// The private key is still nowhere near the jail.
	if strings.Contains(joinArgs(args), "ca.key") {
		t.Errorf("ca.key must not cross: %v", args)
	}
}

func TestStateFilesRejectsEscapes(t *testing.T) {
	for _, bad := range []any{"/etc/shadow", "../../.ssh/id_rsa", ""} {
		md := modsDir(t)
		mod := mkdir(t, filepath.Join(md, "bad-state"))
		writeManifest(t, mod, map[string]any{
			"name": "bad-state", "description": "x",
			"state_files": []any{bad},
		})
		entries := validateDir(md)
		if len(entries) != 1 || entries[0].Loophole != nil || !contains(entries[0].Err, "state_files[0]") {
			t.Errorf("state_files entry %q should be rejected, got %+v", bad, entries)
		}
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
	entries := validateDir(md)
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
	set := approvedSetFrom(md)
	loaded := set.Enabled()
	if !loaded[0].Active() {
		t.Fatalf("should be active in-jail (container path /tmp exists)")
	}
	args := set.RuntimeArgsFor(loaded, "")
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
	set := approvedSetFrom(md)
	specs := set.ManifestHostDaemonSpecs(set.Enabled())
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

	set := approvedSetFrom(md)
	results := set.RunDoctorChecks(set.Enabled(), 0)
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

// jailboundFixture writes four modules under one root: a BOUND loophole (binds, no jail
// daemon), a device-only one, one with a jail daemon beside its bind, and one that binds
// nothing, and returns the approved set over them.
func jailboundFixture(t *testing.T) Set {
	t.Helper()
	unsetJail(t)
	md := modsDir(t)
	sock := filepath.Join(t.TempDir(), "native")
	if err := os.WriteFile(sock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, mkdir(t, filepath.Join(md, "sound-like")), map[string]any{
		"name": "sound-like", "description": "x", "transport": "none",
		"host_bind_mounts": []any{map[string]any{"host": sock, "container": "/run/pulse/native", "readonly": true}},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "device-like")), map[string]any{
		"name": "device-like", "description": "x", "transport": "none",
		"host_devices": []any{"/dev/null"},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "daemon-like")), map[string]any{
		"name": "daemon-like", "description": "x", "transport": "none",
		"host_bind_mounts": []any{map[string]any{"host": sock, "container": "/run/daemon-like", "readonly": true}},
		"jail_daemon":      map[string]any{"cmd": []any{"yolo-jaild", "daemon-like"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "nothing-bound")), map[string]any{
		"name": "nothing-bound", "description": "x", "transport": "none",
	})
	return approvedSetFrom(md)
}

// A BOUND LOOPHOLE IS SERVED BY NAME WHERE THE ARGV BINDS IT (docs/design/loophole-packaging.md
// LP-D1): on a container runtime every active loophole with a bind or a device and no jail daemon,
// and nothing else — a jail daemon's record is served through its daemon, and a record that binds
// nothing has nothing a pointer could name.
func TestJailBoundNamesListsTheLoopholesTheArgvBinds(t *testing.T) {
	set := jailboundFixture(t)
	for _, rt := range []string{"podman", "container", ""} {
		got := set.JailBoundNames(set.Enabled(), rt)
		want := []string{"device-like", "sound-like"}
		if !reflect.DeepEqual(sortedCopy(got), want) {
			t.Errorf("runtime %q: JailBoundNames = %v, want %v", rt, got, want)
		}
		// THE PREDICATE IS THE ARGV'S: every name listed has its bind or device on the argv
		// the same set builds, so the served set and the argv cannot disagree.
		args := joinArgs(set.RuntimeArgsFor(set.Enabled(), rt))
		if !strings.Contains(args, ":/run/pulse/native:ro") || !strings.Contains(args, "--device /dev/null") {
			t.Errorf("runtime %q: the argv must carry what JailBoundNames serves:\n%s", rt, args)
		}
	}
}

// macos-user binds nothing into its Seatbelt sandbox, so it serves no bound loophole.
func TestJailBoundNamesIsNilOnMacosUser(t *testing.T) {
	set := jailboundFixture(t)
	if got := set.JailBoundNames(set.Enabled(), "macos-user"); got != nil {
		t.Errorf("JailBoundNames on macos-user = %v, want nil", got)
	}
}

// A DISABLED or inactive loophole binds nothing, so it is not served: the argv loop's own skip
// (admitsJailSideEffects), not a second rule.
func TestJailBoundNamesExcludesAnInactiveLoophole(t *testing.T) {
	set := jailboundFixture(t)
	lp, ok := set.Lookup("sound-like")
	if !ok {
		t.Fatal("fixture: sound-like not discovered")
	}
	lp.Enabled = false
	if got := set.JailBoundNames(set.All(), "podman"); !reflect.DeepEqual(got, []string{"device-like"}) {
		t.Errorf("with sound-like disabled, JailBoundNames = %v, want [device-like]", got)
	}
}

// A pack-shipped record reached with NO origin gate binds nothing (gateAdmitsCrossing), so it is
// not served either: the gate is the argv's, and a set assembled by hand carries none.
func TestJailBoundNamesHonorsTheOriginGate(t *testing.T) {
	set := jailboundFixture(t)
	ungated := SetOf(set.All())
	if got := ungated.JailBoundNames(ungated.Enabled(), "podman"); len(got) != 0 {
		t.Errorf("an ungated set served bound loopholes %v; a pack record it cannot gate binds nothing", got)
	}
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func TestCurrentSettingsDoctorSubstitutesOnlyDeclaredSettingsToken(t *testing.T) {
	unsetJail(t)
	root := redirectState(t)
	const name = "doctor-argv"
	stateDir := filepath.Join(root, name)
	stable := filepath.Join(stateDir, SettingsFileName)
	baseline := stable + ".backup"
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stableBefore := []byte("last-launch settings\n")
	baselineBefore := []byte("persistent baseline\n")
	if err := os.WriteFile(stable, stableBefore, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(baseline, baselineBefore, 0o600); err != nil {
		t.Fatal(err)
	}

	capture := filepath.Join(t.TempDir(), "argv.txt")
	settingsSeen := filepath.Join(t.TempDir(), "settings-seen.json")
	doctor := filepath.Join(t.TempDir(), "fake-doctor")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(capture) + "\n" +
		"cat \"$2\" > " + shellQuote(settingsSeen) + "\n"
	if err := os.WriteFile(doctor, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, module, map[string]any{
		"name": name, "transport": "none", "lifecycle": "external",
		"settings": map[string]any{"value": map[string]any{"type": "string", "default": "manifest-default"}},
		"doctor_cmd": []string{doctor, "--settings", "{settings}", "--settings-embedded", "prefix:{settings}/suffix",
			"--baseline", "{state}/settings.json.backup", "--literal", stable, "--embedded", "prefix:" + stable + "/suffix"},
	})
	lp, err := LoadLoophole(module)
	if err != nil {
		t.Fatal(err)
	}
	lp.Source = SourceConfig // The fixture supplies its own authority; no real pack is discovered.
	frozen, _, err := FrozenSettingsBytes(lp, supplied("value", "current profile"))
	if err != nil {
		t.Fatal(err)
	}
	originalDoctorCmd := append([]string(nil), lp.DoctorCmd...)
	set := SetOf([]*Loophole{lp})
	results := set.RunDoctorChecksWithSettings([]*Loophole{lp}, 5*time.Second, map[string][]byte{name: frozen}, nil)
	if len(results) != 1 || results[0].RC == nil || *results[0].RC != 0 {
		t.Fatalf("doctor result = %+v", results)
	}

	argvBytes, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	argv := strings.Split(strings.TrimSuffix(string(argvBytes), "\n"), "\n")
	if len(argv) != 10 {
		t.Fatalf("captured argv = %#v, want 10 arguments", argv)
	}
	if argv[0] != "--settings" || filepath.Dir(argv[1]) != stateDir || !strings.HasPrefix(filepath.Base(argv[1]), "settings-"+SettingsFileName+"-") {
		t.Errorf("declared {settings} did not name its private snapshot: %#v", argv[:2])
	}
	if argv[2] != "--settings-embedded" || argv[3] != "prefix:"+argv[1]+"/suffix" {
		t.Errorf("embedded declared {settings} did not name its private snapshot: %#v", argv[2:4])
	}
	if argv[4] != "--baseline" || argv[5] != baseline {
		t.Errorf("{state}-derived baseline changed: got %#v, want --baseline %q", argv[4:6], baseline)
	}
	if argv[6] != "--literal" || argv[7] != stable {
		t.Errorf("literal settings path changed: got %#v, want --literal %q", argv[6:8], stable)
	}
	if argv[8] != "--embedded" || argv[9] != "prefix:"+stable+"/suffix" {
		t.Errorf("embedded literal settings path changed: %#v", argv[8:])
	}
	if !reflect.DeepEqual(lp.DoctorCmd, originalDoctorCmd) {
		t.Errorf("runner mutated the loaded doctor command: got %#v, want %#v", lp.DoctorCmd, originalDoctorCmd)
	}
	settingsAfter, err := os.ReadFile(settingsSeen)
	if err != nil || !reflect.DeepEqual(settingsAfter, frozen) {
		t.Errorf("doctor read settings %q, err=%v; want current frozen bytes %q", settingsAfter, err, frozen)
	}
	stableAfter, stableErr := os.ReadFile(stable)
	baselineAfter, baselineErr := os.ReadFile(baseline)
	if stableErr != nil || !reflect.DeepEqual(stableAfter, stableBefore) {
		t.Errorf("persistent settings changed: %q, err=%v", stableAfter, stableErr)
	}
	if baselineErr != nil || !reflect.DeepEqual(baselineAfter, baselineBefore) {
		t.Errorf("persistent baseline changed: %q, err=%v", baselineAfter, baselineErr)
	}
	entries, err := os.ReadDir(stateDir)
	if err != nil || len(entries) != 2 {
		t.Errorf("doctor snapshot was not cleaned up: entries=%v err=%v", entries, err)
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }

// A FRONTED HOST LOOPHOLE IS SERVED BY NAME WHERE ITS HOST DAEMON SERVES THE JAIL
// (docs/design/git-broker-plan.md ID-6): an enabled loophole whose host_daemon answers over
// loopback-tls is listed on podman and macos-user, never on Apple Container, which runs no host
// services, and a disabled one, or one with no host daemon, never.
func TestFrontedHostNames(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	writeManifest(t, mkdir(t, filepath.Join(md, "fronted")), map[string]any{
		"name": "fronted", "description": "x", "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "fronted-off")), map[string]any{
		"name": "fronted-off", "description": "x", "transport": "loopback-tls", "lifecycle": "spawned",
		"default_enabled": false,
		"host_daemon":     map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "no-daemon")), map[string]any{
		"name": "no-daemon", "description": "x", "transport": "none",
	})
	// A record with a jail daemon too is served through that daemon (aws-auth's shape), so its
	// pointers follow the jail daemon's split, never its host daemon.
	writeManifest(t, mkdir(t, filepath.Join(md, "both-daemons")), map[string]any{
		"name": "both-daemons", "description": "x", "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
		"jail_daemon": map[string]any{"cmd": []any{"yolo-jaild", "both-daemons"}},
	})
	set := approvedSetFrom(md)
	for _, tc := range []struct {
		runtime string
		want    []string
	}{
		{"podman", []string{"fronted"}},
		{"macos-user", []string{"fronted"}},
		{"", []string{"fronted"}},
		{"container", nil},
	} {
		if got := set.FrontedHostNames(set.All(), tc.runtime); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("runtime %q: FrontedHostNames = %v, want %v", tc.runtime, got, tc.want)
		}
	}
	// The ORIGIN GATE is the argv's: a set assembled by hand serves nothing.
	ungated := SetOf(set.All())
	if got := ungated.FrontedHostNames(ungated.Enabled(), "podman"); len(got) != 0 {
		t.Errorf("an ungated set listed fronted host loopholes %v", got)
	}
}

// TestFrontedHostWhy: each fronted-shape record a launch leaves out gets a reason naming the
// next step, worded for why it is out here: off for the workspace, on Apple Container, or at
// `yolo host --`. A served one, and a record of another shape, gets none.
func TestFrontedHostWhy(t *testing.T) {
	unsetJail(t)
	md := modsDir(t)
	writeManifest(t, mkdir(t, filepath.Join(md, "fronted")), map[string]any{
		"name": "fronted", "description": "x", "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "fronted-off")), map[string]any{
		"name": "fronted-off", "description": "x", "transport": "loopback-tls", "lifecycle": "spawned",
		"default_enabled": false,
		"host_daemon":     map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
	})
	writeManifest(t, mkdir(t, filepath.Join(md, "both-daemons")), map[string]any{
		"name": "both-daemons", "description": "x", "transport": "loopback-tls", "lifecycle": "spawned",
		"host_daemon": map[string]any{"cmd": []any{"daemon", "--socket", "{socket}"}},
		"jail_daemon": map[string]any{"cmd": []any{"yolo-jaild", "both-daemons"}},
	})
	set := approvedSetFrom(md)

	podman := set.FrontedHostWhy("podman")
	if _, ok := podman["fronted"]; ok || len(podman) != 1 {
		t.Fatalf("podman: FrontedHostWhy = %v, want only fronted-off", podman)
	}
	if got := podman["fronted-off"]; !strings.Contains(got, "off for this workspace") ||
		!strings.Contains(got, "`yolo loopholes list`") || strings.Contains(got, "loopholes enable") {
		t.Errorf("podman, loophole off: %q does not name `yolo loopholes list`, or names the retired enable", got)
	}
	if got := set.FrontedHostWhy("macos-user")["fronted-off"]; got != podman["fronted-off"] {
		t.Errorf("macos-user, loophole off: %q, want podman's %q", got, podman["fronted-off"])
	}
	ac := set.FrontedHostWhy("container")
	for _, name := range []string{"fronted", "fronted-off"} {
		if got := ac[name]; !strings.Contains(got, "Apple Container") ||
			!strings.Contains(got, "runs no host services") {
			t.Errorf("container, %s: %q does not say Apple Container runs no host services", name, got)
		}
	}
	host := set.FrontedHostWhy("host")
	for _, name := range []string{"fronted", "fronted-off"} {
		if got := host[name]; !strings.Contains(got, "`yolo host --` does not front") ||
			!strings.Contains(got, "host's own credentials") {
			t.Errorf("host, %s: %q does not say the host agent uses the host's own credentials", name, got)
		}
	}
	if _, ok := host["both-daemons"]; ok {
		t.Errorf("a record with a jail daemon got a fronted reason: %q", host["both-daemons"])
	}
}

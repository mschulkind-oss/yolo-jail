package loopholedecl_test

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// manifestBytes renders a manifest from a Go map. json.Marshal escapes a control
// character as , which is exactly how a hostile manifest would carry one:
// legal JSON that decodes to a raw byte.
func manifestBytes(t *testing.T, data map[string]any) []byte {
	t.Helper()
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func decodeMap(t *testing.T, name string, data map[string]any) (*loopholedecl.Manifest, error) {
	t.Helper()
	return loopholedecl.Decode(manifestBytes(t, data), filepath.Join("/loopholes", name))
}

// TestPackageImportsOnlyMeasuredLeaves is the PLACEMENT RULE as a test
// (docs/design/loophole-packaging.md §3.2).
//
// This package exists because internal/packload cannot import internal/loopholes:
// loopholes -> config -> packload is a cycle, so the pack footprint — the screen
// that has to tell a user which argv a pack would run on their machine — had
// nothing to decode a manifest with. That only stays true while this package
// imports nothing but the measured leaf decoders. The day someone reaches for
// internal/paths here, the cycle comes back and the symptom is a build failure in
// a package nobody was editing, so the rule is pinned where it is broken.
//
// The equivalent shell check is `go list -deps ./internal/loopholedecl | rg
// yolo-jail`; this is the same assertion without a subprocess.
func TestPackageImportsOnlyMeasuredLeaves(t *testing.T) {
	allowed := map[string]bool{
		"github.com/mschulkind-oss/yolo-jail/internal/json5":  true,
		"github.com/mschulkind-oss/yolo-jail/internal/jsonx":  true,
		"github.com/mschulkind-oss/yolo-jail/internal/pytext": true,
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		for _, imp := range f.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/mschulkind-oss/yolo-jail/") {
				continue // stdlib is fine
			}
			if !allowed[path] {
				t.Errorf("%s imports %s — loopholedecl must stay a leaf (parse + static "+
					"validation only), or internal/packload cannot decode a manifest and the "+
					"pack footprint loses the daemon argv", name, path)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no non-test source files found; the placement guard checked nothing")
	}
}

func TestDecodeMinimalAppliesDefaults(t *testing.T) {
	m, err := decodeMap(t, "quiet", map[string]any{"name": "quiet", "description": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Transport != loopholedecl.TransportLoopbackTLS {
		t.Errorf("Transport = %q, want %q", m.Transport, loopholedecl.TransportLoopbackTLS)
	}
	if m.Lifecycle != "external" {
		t.Errorf("Lifecycle = %q, want external", m.Lifecycle)
	}
	if !m.Enabled {
		t.Error("Enabled = false, want true (absent means enabled)")
	}
	if m.BrokerIP != loopholedecl.DefaultBrokerIP {
		t.Errorf("BrokerIP = %q, want %q", m.BrokerIP, loopholedecl.DefaultBrokerIP)
	}
	if m.VersionSet {
		t.Errorf("VersionSet = true with no `version` key")
	}
	// Non-nil so a caller can iterate without a nil check, matching what the
	// runtime record has always relied on.
	if m.Intercepts == nil || m.JailEnv == nil {
		t.Errorf("Intercepts/JailEnv must be non-nil after a successful decode: %+v", m)
	}
}

// TestDecodedFieldsStayRAW is the load-bearing invariant of the schema/runtime
// split: every token and every $VAR survives decoding untouched.
//
// It matters in both directions. The footprint must report the argv the AUTHOR
// wrote (a substituted path is a different string on every machine, so a consent
// prompt built from it could never be compared or re-approved), and this package
// must not need to know where yolo keeps state or what the environment holds — the
// moment it does, it stats something, and a leaf that stats is a leaf that will
// grow an import.
func TestDecodedFieldsStayRAW(t *testing.T) {
	m, err := decodeMap(t, "raw", map[string]any{
		"name": "raw", "description": "x",
		"ca_cert":   "{state}/ca.crt",
		"transport": loopholedecl.TransportLoopbackTLS,
		"host_daemon": map[string]any{
			"cmd": []any{"python3", "{loophole_dir}/srv.py", "--socket", "{socket}"},
		},
		"jail_daemon": map[string]any{"cmd": []any{"{jail_loophole_dir}/agentd"}},
		"doctor_cmd":  []any{"{loophole_dir}/doctor.sh"},
		"host_bind_mounts": []any{
			map[string]any{"host": "${XDG_RUNTIME_DIR}/pulse/native", "container": "/run/pulse/native"},
		},
		"requires": map[string]any{"file_exists": "${XDG_RUNTIME_DIR}/pulse/native"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if m.CACert != "{state}/ca.crt" {
		t.Errorf("ca_cert = %q, want the token unresolved", m.CACert)
	}
	if got := m.HostDaemon.Cmd[1]; got != "{loophole_dir}/srv.py" {
		t.Errorf("host_daemon.cmd[1] = %q, want the token unresolved", got)
	}
	if got := m.HostDaemon.Cmd[3]; got != "{socket}" {
		t.Errorf("host_daemon.cmd[3] = %q; {socket} belongs to the run pipeline", got)
	}
	if got := m.JailDaemon.Cmd[0]; got != "{jail_loophole_dir}/agentd" {
		t.Errorf("jail_daemon.cmd[0] = %q, want the token unresolved", got)
	}
	if got := m.DoctorCmd[0]; got != "{loophole_dir}/doctor.sh" {
		t.Errorf("doctor_cmd[0] = %q, want the token unresolved", got)
	}
	if got := m.HostBindMounts[0].Host; got != "${XDG_RUNTIME_DIR}/pulse/native" {
		t.Errorf("bind mount host = %q, want the $VAR unexpanded", got)
	}
	if !m.HostBindMounts[0].Readonly {
		t.Error("bind mount readonly defaults to true")
	}
}

// TestStrictDecodeReportsUnknownKeys: today's loader has no unknown-key rejection
// at all, which is how `"version": 1` came to be declared by every bundled
// manifest, documented as the schema version, and read by nothing. For an AUTHOR
// that silence is the worst outcome: `host_deamon` reads as a loophole with no
// daemon, and the symptom arrives later as a missing endpoint.
func TestStrictDecodeReportsUnknownKeys(t *testing.T) {
	cases := []struct {
		name     string
		manifest map[string]any
		wantKey  string
	}{
		{"top-level", map[string]any{"host_deamon": map[string]any{}}, "host_deamon"},
		{"host-daemon", map[string]any{
			"host_daemon": map[string]any{"cmd": []any{"d"}, "publishs": "socket"},
		}, "host_daemon.publishs"},
		{"jail-daemon", map[string]any{
			"jail_daemon": map[string]any{"cmd": []any{"d"}, "restartt": "always"},
		}, "jail_daemon.restartt"},
		{"requires", map[string]any{
			"requires": map[string]any{"command_on_paths": "ps"},
		}, "requires.command_on_paths"},
		{"intercept", map[string]any{
			"intercepts": []any{map[string]any{"host": "a.test", "hosts": "b.test"}},
		}, "intercepts[0].hosts"},
		{"bind-mount", map[string]any{
			"host_bind_mounts": []any{
				map[string]any{"host": "/a", "container": "/b", "readonyl": true},
			},
		}, "host_bind_mounts[0].readonyl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := map[string]any{"name": "typo", "description": "x"}
			for k, v := range tc.manifest {
				manifest[k] = v
			}
			_, err := decodeMap(t, "typo", manifest)
			if err == nil {
				t.Fatalf("strict decode accepted the unknown key %q", tc.wantKey)
			}
			msg := err.Error()
			if !strings.Contains(msg, tc.wantKey) {
				t.Errorf("error does not name the key %q: %s", tc.wantKey, msg)
			}
			if !strings.Contains(msg, "unknown key") {
				t.Errorf("error does not say the key is unknown: %s", msg)
			}

			// The TOLERANT path must survive the same manifest — that is the whole
			// asymmetry: an author hears, a jail boots.
			m, skipped, terr := loopholedecl.DecodeTolerant(
				manifestBytes(t, manifest), filepath.Join("/loopholes", "typo"))
			if terr != nil {
				t.Fatalf("tolerant decode refused an unknown key: %v", terr)
			}
			if m == nil {
				t.Fatal("tolerant decode returned no manifest")
			}
			if len(skipped) != 1 || !strings.Contains(skipped[0], tc.wantKey) {
				t.Errorf("skipped = %v, want one note naming %q", skipped, tc.wantKey)
			}
			if !strings.Contains(skipped[0], "skew") {
				t.Errorf("skipped note does not explain itself as version skew: %s", skipped[0])
			}
		})
	}
}

// TestStrictDecodeReportsEveryUnknownKeyAtOnce: an author fixing typos one
// edit-check cycle at a time is the cost packdecl.Decode pays multi-problem
// reporting to avoid.
func TestStrictDecodeReportsEveryUnknownKeyAtOnce(t *testing.T) {
	_, err := decodeMap(t, "typos", map[string]any{
		"name": "typos", "description": "x",
		"versoin": 1, "enable": true,
	})
	if err == nil {
		t.Fatal("two unknown keys decoded cleanly")
	}
	le, ok := err.(*loopholedecl.Error)
	if !ok {
		t.Fatalf("error is %T, want *loopholedecl.Error", err)
	}
	problems := le.Problems()
	if len(problems) != 2 {
		t.Fatalf("Problems() = %v, want one per unknown key", problems)
	}
	joined := strings.Join(problems, "\n")
	for _, want := range []string{"versoin", "enable"} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems do not name %q: %s", want, joined)
		}
	}
	// And the joined form a warning would print carries both.
	if !strings.Contains(le.Error(), "versoin") || !strings.Contains(le.Error(), "enable") {
		t.Errorf("Error() drops a problem: %s", le.Error())
	}
}

// TestVersionIsKnownToBothDecoders pins the one key this extraction had to
// RECOGNIZE rather than reject: every bundled manifest declares it.
func TestVersionIsKnownToBothDecoders(t *testing.T) {
	manifest := map[string]any{"name": "versioned", "description": "x", "version": 1}
	m, err := decodeMap(t, "versioned", manifest)
	if err != nil {
		t.Fatalf("strict decode rejected `version`: %v", err)
	}
	if m.Version != 1 || !m.VersionSet {
		t.Errorf("Version = %d (set=%v), want 1", m.Version, m.VersionSet)
	}
	// A version this build has never heard of is SKEW, not a mistake: refusing it
	// would make a manifest a newer yolo shipped brick an older reader, which is
	// the failure the tolerant path exists to prevent — so it is not enum-checked
	// on either path.
	future := map[string]any{"name": "versioned", "description": "x", "version": 99}
	if _, err := decodeMap(t, "versioned", future); err != nil {
		t.Errorf("a future schema version was refused: %v", err)
	}
}

// TestTolerantDecodeStillRefusesStructure: tolerance is about keys this build does
// not know, never about a manifest both builds agree is broken.
func TestTolerantDecodeStillRefusesStructure(t *testing.T) {
	cases := []struct {
		name     string
		manifest map[string]any
		wantSub  string
	}{
		{"bad-transport", map[string]any{"transport": "carrier-pigeon"}, "transport="},
		{"bad-lifecycle", map[string]any{"lifecycle": "orbiting"}, "lifecycle="},
		{"escaping-state-file", map[string]any{"state_files": []any{"../../etc/shadow"}}, "state_files[0]"},
		{"empty-daemon-cmd", map[string]any{"host_daemon": map[string]any{"cmd": []any{}}}, "host_daemon.cmd"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := map[string]any{"name": "broken", "description": "x"}
			for k, v := range tc.manifest {
				manifest[k] = v
			}
			_, _, err := loopholedecl.DecodeTolerant(
				manifestBytes(t, manifest), "/loopholes/broken")
			if err == nil {
				t.Fatalf("tolerant decode accepted %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error %q does not name %q", err.Error(), tc.wantSub)
			}
		})
	}
}

// TestControlCharactersRefusedInClaimFields is the §3.2 sanitize-at-load
// requirement, one case per field that can reach an approval claim.
//
// The two exhibits are the ones the design names: a newline forging an extra claim
// line, and ESC erasing the ⚠ header. Both work because the prompt formats the
// line and THEN parses style tags over the result, so every byte the formatter
// produced is already in the output stream.
func TestControlCharactersRefusedInClaimFields(t *testing.T) {
	const forgedClaim = "srv.py\n      [dim]mount ~/Documents -> /ctx/docs[/dim]"
	const eraseHeader = "\x1b[2K\x1b[A--quick"

	cases := []struct {
		name     string
		dirName  string
		manifest map[string]any
		wantSub  string
	}{
		{"host-daemon-cmd-forges-a-claim", "evil", map[string]any{
			"host_daemon": map[string]any{"cmd": []any{"python3", forgedClaim}},
		}, "host_daemon.cmd"},
		{"doctor-cmd-erases-the-header", "evil", map[string]any{
			"doctor_cmd": []any{"/bin/true", eraseHeader},
		}, "doctor_cmd"},
		{"jail-daemon-cmd", "evil", map[string]any{
			"jail_daemon": map[string]any{"cmd": []any{"agentd\nfake"}},
		}, "jail_daemon.cmd"},
		{"intercept-host", "evil", map[string]any{
			"intercepts": []any{map[string]any{"host": "api.acme.com\nevil.test"}},
		}, "intercepts[].host"},
		{"bind-mount-host", "evil", map[string]any{
			"host_bind_mounts": []any{map[string]any{"host": "/a\n/b", "container": "/ctx/x"}},
		}, "host_bind_mounts[0].host"},
		{"bind-mount-container", "evil", map[string]any{
			"host_bind_mounts": []any{map[string]any{"host": "/a", "container": "/ctx/x\x1b[A"}},
		}, "host_bind_mounts[0].container"},
		{"device", "evil", map[string]any{
			"host_devices": []any{"/dev/snd\n/dev/mem"},
		}, "host_devices[0]"},
		{"ca-cert", "evil", map[string]any{"ca_cert": "{state}/ca.crt\x1b[2K"}, "ca_cert"},
		{"state-file", "evil", map[string]any{
			"state_files": []any{"ca.crt\nca.key"},
		}, "state_files[0]"},
		// The loophole's own name is every claim's TARGET, and a directory name may
		// legally contain a newline on Linux — so the name is checked before it is
		// compared to the directory.
		{"name-is-the-claim-target", "ev\nil", map[string]any{}, "'name'"},
		// A tab is refused too: it is not dangerous the way ESC is, it just breaks
		// the column layout the claim table is read in.
		{"tab", "evil", map[string]any{"host_devices": []any{"/dev/\tsnd"}}, "host_devices[0]"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := map[string]any{"name": tc.dirName, "description": "x"}
			for k, v := range tc.manifest {
				manifest[k] = v
			}
			data := manifestBytes(t, manifest)
			dir := filepath.Join("/loopholes", tc.dirName)

			_, err := loopholedecl.Decode(data, dir)
			if err == nil {
				t.Fatal("a control character in a claim field decoded cleanly")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("error does not name the field %q: %s", tc.wantSub, err.Error())
			}
			if !strings.Contains(err.Error(), "control character") {
				t.Errorf("error does not say what is wrong: %s", err.Error())
			}
			// A forged claim is not version skew: BOTH decoders refuse it, or the
			// tolerant path (which is what discovery and the in-jail reader use)
			// becomes the way to smuggle one in.
			if _, _, terr := loopholedecl.DecodeTolerant(data, dir); terr == nil {
				t.Error("the TOLERANT decoder accepted a control character — the refusal " +
					"must not be skippable, it is not a version-skew fact")
			}
		})
	}
}

// TestRawC1ByteCannotReachAClaimField closes the gap a rune-range check alone
// would leave: a LONE 0x9B byte is invalid UTF-8, so ranging over the string
// yields U+FFFD and the C1 test never sees it — while a terminal may still read
// the raw byte as CSI.
//
// Measured, and the answer is layered: json5.Decode already replaces an invalid
// byte with U+FFFD, so the dangerous byte is gone before this package sees the
// string, and the utf8 backstop in refuseControlChars only fires if that ever
// changes. Either outcome satisfies the requirement, so the test asserts the
// PROPERTY rather than the mechanism: no C1 byte survives into a claim field.
func TestRawC1ByteCannotReachAClaimField(t *testing.T) {
	// Written as raw bytes rather than through json.Marshal, which would refuse to
	// emit invalid UTF-8.
	data := []byte("{\"name\": \"evil\", \"description\": \"x\", \"host_devices\": [\"/dev/\x9bsnd\"]}")
	m, err := loopholedecl.Decode(data, "/loopholes/evil")
	if err != nil {
		return // refused outright, which is also fine
	}
	for i, b := range []byte(m.HostDevices[0]) {
		if b >= 0x80 && b <= 0x9f {
			t.Fatalf("host_devices[0] carries a raw C1 byte 0x%02X at %d (%q) — a terminal "+
				"reads that as an escape sequence in the approval prompt", b, i, m.HostDevices[0])
		}
	}
}

// TestLegitimateUnicodeAccepted: the refusal must be about CONTROL bytes, not
// about non-ASCII text. A multi-byte rune contains bytes in 0x80-0xBF, so a
// byte-wise C1 check would reject perfectly ordinary paths.
func TestLegitimateUnicodeAccepted(t *testing.T) {
	m, err := decodeMap(t, "unicode", map[string]any{
		"name": "unicode", "description": "café — naïve · 日本語 🎧",
		"host_bind_mounts": []any{
			map[string]any{"host": "/tmp/ünïcødé/påth", "container": "/ctx/x"},
		},
	})
	if err != nil {
		t.Fatalf("legitimate non-ASCII text was refused: %v", err)
	}
	if m.HostBindMounts[0].Host != "/tmp/ünïcødé/påth" {
		t.Errorf("host = %q", m.HostBindMounts[0].Host)
	}
}

// TestLoadDirIsTheSeam: R6's requirement, and the reason the package can be used
// by the pack footprint at all — a directory path in, a schema out, no runtime
// vocabulary in the signature.
func TestLoadDirIsTheSeam(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "acme-proxy")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := manifestBytes(t, map[string]any{
		"name": "acme-proxy", "description": "x", "version": 1,
		"host_daemon": map[string]any{
			"cmd":       []any{"python3", "{loophole_dir}/srv.py", "--socket", "{socket}"},
			"publishes": "socket",
		},
	})
	if err := os.WriteFile(loopholedecl.ManifestPath(dir), body, 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := loopholedecl.LoadDir(dir)
	if err != nil {
		t.Fatalf("LoadDir: %v", err)
	}
	if m.Name != "acme-proxy" {
		t.Errorf("Name = %q", m.Name)
	}
	if m.HostDaemon.Publishes != loopholedecl.PublishesSocket {
		t.Errorf("publishes = %q", m.HostDaemon.Publishes)
	}
	if got := m.HostDaemon.Cmd[1]; got != "{loophole_dir}/srv.py" {
		t.Errorf("cmd[1] = %q; LoadDir must not resolve anything", got)
	}

	tol, skipped, err := loopholedecl.LoadDirTolerant(dir)
	if err != nil || len(skipped) != 0 {
		t.Fatalf("LoadDirTolerant: %v, skipped %v", err, skipped)
	}
	if !reflect.DeepEqual(tol, m) {
		t.Errorf("LoadDir and LoadDirTolerant disagree on a clean manifest")
	}

	// The name/directory agreement is checked against the DIRECTORY, which is what
	// makes a manifest's identity un-spoofable by its own contents.
	other := filepath.Join(root, "renamed")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loopholedecl.ManifestPath(other), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loopholedecl.LoadDir(other); err == nil ||
		!strings.Contains(err.Error(), "disagrees with directory") {
		t.Errorf("name/dir mismatch error = %v", err)
	}

	// A directory with no manifest is a plain, skippable error naming the path.
	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err = loopholedecl.LoadDir(empty)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("missing manifest error = %v", err)
	}
	if !strings.Contains(err.Error(), loopholedecl.ManifestName) {
		t.Errorf("error does not name the file it looked for: %v", err)
	}
}

// TestWrongHalfTokensRefused: the host/jail token asymmetry is a STATIC fact about
// which field a token is legal in, so it belongs to the schema even though
// resolving either token does not.
func TestWrongHalfTokensRefused(t *testing.T) {
	cases := []struct {
		name     string
		manifest map[string]any
		wantHint string
	}{
		{"host-daemon-jail-token", map[string]any{
			"host_daemon": map[string]any{"cmd": []any{"{jail_loophole_dir}/srv", "{socket}"}},
		}, loopholedecl.TokenLoopholeDir},
		{"doctor-jail-token", map[string]any{
			"doctor_cmd": []any{"{jail_loophole_dir}/doctor.sh"},
		}, loopholedecl.TokenLoopholeDir},
		{"jail-daemon-host-token", map[string]any{
			"jail_daemon": map[string]any{"cmd": []any{"{loophole_dir}/agentd"}},
		}, loopholedecl.TokenJailLoopholeDir},
		{"bind-mount-jail-token", map[string]any{
			"host_bind_mounts": []any{
				map[string]any{"host": "{jail_loophole_dir}/data", "container": "/ctx/d"},
			},
		}, loopholedecl.TokenLoopholeDir},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := map[string]any{"name": "toked", "description": "x"}
			for k, v := range tc.manifest {
				manifest[k] = v
			}
			_, err := decodeMap(t, "toked", manifest)
			if err == nil {
				t.Fatal("a wrong-half token decoded cleanly")
			}
			if !strings.Contains(err.Error(), tc.wantHint) {
				t.Errorf("refusal does not name the right token %q: %s", tc.wantHint, err.Error())
			}
		})
	}
}

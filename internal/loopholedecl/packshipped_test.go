package loopholedecl_test

// The PACK-SHIPPED SUBSET (loophole-packaging.md §3.1 + §2.1's ruling).
//
// Every test here asserts the message NAMES THE FIX, not merely that an error
// occurred. A refusal whose message does not say what to do instead is a bug report
// addressed to nobody — and these refusals land on a pack AUTHOR who has no access
// to this repo's design docs, so the sentence is the whole interface.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

const packDir = "/packs/acme/loopholes/acme-proxy"

// decodePack decodes a manifest for the module dir the pack-shipped tests use, so
// `name` agreement is satisfied without every test repeating the basename.
func decodePack(t *testing.T, body string) *loopholedecl.Manifest {
	t.Helper()
	m, err := loopholedecl.Decode([]byte(`{"name": "acme-proxy", `+body+`}`), packDir)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return m
}

// packProblems decodes and applies the subset, returning the problems.
func packProblems(t *testing.T, body string) []string {
	t.Helper()
	return decodePack(t, body).PackShippedProblems(loopholedecl.ManifestPath(packDir))
}

// wantOneProblemWith asserts exactly one problem and that it carries every
// fragment — the fix, in the words the author needs.
func wantOneProblemWith(t *testing.T, problems []string, fragments ...string) string {
	t.Helper()
	if len(problems) != 1 {
		t.Fatalf("problems = %v, want exactly one", problems)
	}
	for _, want := range fragments {
		if !strings.Contains(problems[0], want) {
			t.Errorf("refusal does not carry %q:\n  %s", want, problems[0])
		}
	}
	return problems[0]
}

// R1. jail_env is refused, and the message hands back the author's OWN keys shaped
// as the `env` contribution kind — the fix, not a schema sketch. It also has to
// disclose the conditionality being traded away, because that difference is the one
// thing an audio-shaped loophole will actually notice.
func TestPackShippedRefusesJailEnvAndNamesTheEnvKind(t *testing.T) {
	problems := packProblems(t,
		`"jail_env": {"PULSE_SERVER": "unix:/run/pulse/native", "PIPEWIRE_REMOTE": "pipewire-0"}`)
	msg := wantOneProblemWith(t, problems,
		"'jail_env' is not available to a pack-shipped loophole",
		`"kind": "env"`,
		`'PULSE_SERVER': 'unix:/run/pulse/native'`,
		`'PIPEWIRE_REMOTE': 'pipewire-0'`,
		"UNCONDITIONAL",
		"OQ-LP5",
	)
	// The author's key ORDER survives into the hint: they will paste it, and a
	// reordered paste is a diff they have to reason about for nothing.
	if strings.Index(msg, "PULSE_SERVER") > strings.Index(msg, "PIPEWIRE_REMOTE") {
		t.Errorf("the env hint reordered the author's keys:\n  %s", msg)
	}
	if !strings.Contains(msg, loopholedecl.ManifestPath(packDir)) {
		t.Errorf("refusal does not name the manifest: %s", msg)
	}
}

// An EMPTY jail_env is not a declaration. Refusing it would refuse a manifest that
// asks for nothing, and `"jail_env": {}` is exactly what a decode of an absent key
// produces — so a subset that fired on it would refuse every manifest.
func TestPackShippedAllowsAbsentAndEmptyJailEnv(t *testing.T) {
	for _, body := range []string{`"description": "no env at all"`, `"jail_env": {}`} {
		if problems := packProblems(t, body); len(problems) != 0 {
			t.Errorf("body %s drew %v", body, problems)
		}
	}
}

// OQ-LP14, THE WITHDRAWAL, asserted in the direction that used to fail. An absolute
// host path and a ${XDG_RUNTIME_DIR} expansion are both LEGAL for a pack-shipped
// loophole now, and the second one is the whole argument: the old rule admitted
// everything under $HOME and refused a pulse socket, so it let through the thing
// worth protecting and blocked the thing that is not.
//
// This is a WIDENING of what a fetched pack may declare, so it is asserted
// deliberately rather than by the absence of a test. What replaced the rule is not
// another rule here: it is packload's claim enumeration plus the origin approval,
// pinned in internal/packload (every bind emits an approvable string, socket binds in
// their own read-write-IPC class).
func TestPackShippedAllowsAbsoluteAndEnvVarBindHosts(t *testing.T) {
	for _, host := range []string{
		"/run/user/1000/pulse/native",
		"${XDG_RUNTIME_DIR}/pulse/native",
		"${XDG_RUNTIME_DIR}/pipewire-0",
		"/var/run/docker.sock",
	} {
		problems := packProblems(t,
			`"host_bind_mounts": [{"host": "`+host+`", "container": "/ctx/x", "readonly": true}]`)
		if len(problems) != 0 {
			t.Errorf("host %q drew %v — the path rule was WITHDRAWN, and re-adding it "+
				"would make `audio` unshippable as a pack again", host, problems)
		}
	}
}

// What survives is a CORRECTNESS rule, not a gate: a declaration whose resolution can
// differ between the claim you approved and the mount yolo makes. Both messages have
// to say that rather than reciting a namespace the rule no longer has.
func TestPackShippedRefusesEscapingAndColonBindHosts(t *testing.T) {
	for _, tc := range []struct{ host, fragment string }{
		{"../../etc/shadow", "contains a '..' segment"},
		{"data:ro", "mount-option separator"},
	} {
		problems := packProblems(t,
			`"host_bind_mounts": [{"host": "`+tc.host+`", "container": "/ctx/x"}]`)
		wantOneProblemWith(t, problems, tc.fragment, "RESOLUTION")
	}
}

// The shapes a pack names in practice, none of which may draw a false positive: a
// false positive here refuses a working pack at every launch.
func TestPackShippedAllowsModuleDirAndHomeRelativeBindHosts(t *testing.T) {
	for _, host := range []string{
		"{loophole_dir}/asound.conf",
		"{loophole_dir}/sockets",
		"Documents/shared",
		"..hidden/x", // a ".." SUBSTRING is not a ".." segment
		"a..b/c",
	} {
		problems := packProblems(t,
			`"host_bind_mounts": [{"host": "`+host+`", "container": "/ctx/x"}]`)
		if len(problems) != 0 {
			t.Errorf("host %q drew %v", host, problems)
		}
	}
}

// `ca_cert` IS PATH-SCOPED for a pack, on the same axis as a bind host and for a
// sharper reason: the file is bind-mounted from the host AND joined into
// NODE_EXTRA_CA_CERTS, so an absolute path would let a pack pick any file on the
// machine and have every node client in the jail TRUST it as a certificate
// authority. A BUNDLED loophole keeps the wider vocabulary (the broker's own
// `{state}/ca.crt` is yolo's code publishing yolo's credential).
func TestPackShippedRefusesAnAbsoluteOrEnvVarCACert(t *testing.T) {
	for _, tc := range []struct{ caCert, fragment string }{
		{"/etc/ssl/certs/ca-certificates.crt", "is an absolute host path"},
		{"${HOME}/.acme/ca.crt", "expands an environment variable"},
		{"../../etc/ssl/ca.crt", "contains a '..' segment"},
		{"weird:name/ca.crt", "mount-option separator"},
	} {
		problems := packProblems(t, `"ca_cert": "`+tc.caCert+`"`)
		wantOneProblemWith(t, problems,
			"'ca_cert'",
			tc.fragment,
			"'ca.crt'",
			"{state}/ca.crt",
			"trusted by every node client in the jail",
			"has to be bundled with yolo",
		)
	}
}

// A token is stripped as a PREFIX and the remainder still has to be in scope, so a
// token cannot launder an escape: '{state}/../../etc/x' walks out of the state dir.
func TestPackShippedRefusesAnEscapeAfterTheStateToken(t *testing.T) {
	problems := packProblems(t, `"ca_cert": "{state}/../../etc/ssl/ca.crt"`)
	wantOneProblemWith(t, problems, "'ca_cert'", "contains a '..' segment")
}

// The two shapes a pack MAY name for its CA: content it ships, and its own
// name-keyed state dir (which yolo owns and which survives restaging — the thing
// that makes a pack-shipped CA possible at all).
func TestPackShippedAllowsModuleDirAndStateCACerts(t *testing.T) {
	for _, caCert := range []string{
		"{loophole_dir}/ca.crt",
		"ca.crt", // module-relative, the same namespace
		"{state}/ca.crt",
	} {
		if problems := packProblems(t, `"ca_cert": "`+caCert+`"`); len(problems) != 0 {
			t.Errorf("ca_cert %q drew %v", caCert, problems)
		}
	}
}

// `requires.file_exists` is PATH-SCOPED for a pack, on the same axis as the bind host
// and `ca_cert`, and this is the one of the three that is NOT a crossing.
//
// Nothing of it is mounted and nothing runs; it is a `stat` whose boolean decides
// whether the loophole is Active. Left unscoped, a fetched pack could `stat` any
// path on the machine — `$HOME/.ssh/id_ed25519`, a corporate VPN's socket — and read
// the answer out of `yolo loopholes list`, which prints the RESOLVED absolute path
// beside it. Scoping the field is the fix rather than hiding the message, because
// hiding it would leave the probe intact (the active/inactive label still answers it)
// while removing the diagnostic that makes an unmet requirement actionable.
//
// It is deliberately NOT given a host-access CLAIM: §3.3's rule is that a CROSSING
// must claim, and a stat crosses nothing. A claim here would put a line in the
// approval prompt for something that mounts nothing and runs nothing, which dilutes
// the prompt whose value is that every line in it is a real capability.
func TestPackShippedRefusesAnOutOfScopeFileExists(t *testing.T) {
	for _, tc := range []struct{ path, fragment string }{
		{"/home/someone/.ssh/id_ed25519", "is an absolute host path"},
		{"${XDG_RUNTIME_DIR}/pulse/native", "expands an environment variable"},
		{"../../etc/shadow", "contains a '..' segment"},
	} {
		problems := packProblems(t, `"requires": {"file_exists": "`+tc.path+`"}`)
		wantOneProblemWith(t, problems,
			"'requires.file_exists'",
			tc.fragment,
			"probes your host",
			"loopholes list",
			"command_on_path",
			"has to be bundled with yolo",
		)
	}
}

// The shapes a pack MAY probe: its own content and the user's home. `command_on_path`
// is untouched — it asks PATH a question about a program name, not the filesystem a
// question about a path.
func TestPackShippedAllowsInScopeRequires(t *testing.T) {
	for _, body := range []string{
		`"requires": {"file_exists": "{loophole_dir}/vendored/bin"}`,
		`"requires": {"file_exists": ".acme/credentials"}`,
		`"requires": {"command_on_path": "python3"}`,
		`"requires": {}`,
	} {
		if problems := packProblems(t, body); len(problems) != 0 {
			t.Errorf("body %s drew %v", body, problems)
		}
	}
}

// R3. readonly:false stays refused, and the message has to carry the MEASURED fact
// about sockets — otherwise the next reader re-derives it, badly, in whichever
// direction their intuition points.
func TestPackShippedRefusesAWritableBindAndStatesWhatItCovers(t *testing.T) {
	problems := packProblems(t,
		`"host_bind_mounts": [{"host": "{loophole_dir}/sock", "container": "/ctx/s", "readonly": false}]`)
	wantOneProblemWith(t, problems,
		"host_bind_mounts[0].readonly = false",
		"omit the key, which defaults to true",
		"fully connectable and",
		"bidirectional",
		"non-REG/DIR/LNK",
		"regular files and",
		"directories",
		"`host_daemon` that mediates",
	)
}

// One mount, two violations — an escaping path AND writable — reports both. They
// are independent declarations with independent fixes, and batching is the whole
// authoring win.
//
// The bind host is `../../var/run/docker.sock` rather than the absolute spelling it
// used to be: an absolute host is LEGAL since OQ-LP14, and the surviving bind rule is
// about resolution stability.
func TestPackShippedReportsEveryProblemAtOnce(t *testing.T) {
	problems := packProblems(t, `
		"jail_env": {"A": "1"},
		"host_bind_mounts": [{"host": "../../var/run/docker.sock", "container": "/ctx/d", "readonly": false}],
		"host_daemon": {"cmd": ["python3", "{loophole_dir}/srv.py", "--endpoint", "{endpoint}"]}`)
	if len(problems) != 4 {
		t.Fatalf("problems = %#v, want 4 (jail_env, bind host, bind readonly, publishes)", problems)
	}
	for _, want := range []string{"'jail_env'", "'..' segment", "readonly = false", "publishes"} {
		if len(containingAll(problems, want)) != 1 {
			t.Errorf("no single problem mentions %q; got %v", want, problems)
		}
	}
}

// R4. publishes:"socket" is the only legal value, and the message explains that the
// front does the security-critical work for you — the ruling's actual content.
func TestPackShippedRefusesPublishesEndpoint(t *testing.T) {
	problems := packProblems(t,
		`"host_daemon": {"cmd": ["python3", "{loophole_dir}/srv.py"], "publishes": "endpoint"}`)
	wantOneProblemWith(t, problems,
		"'host_daemon.publishes' is 'endpoint'",
		`"publishes": "socket"`,
		"transport belongs to the framework",
		"{socket}",
		"constant-time compare",
		"BUNDLED with yolo",
	)
}

// The DEFAULT is refused too. An absent `publishes` decodes to "endpoint", so a
// pack-shipped daemon that says nothing has declared the mode it may not have —
// and the fix is identical, which is why no declared-vs-defaulted bit is needed.
func TestPackShippedRefusesADefaultedPublishes(t *testing.T) {
	problems := packProblems(t,
		`"host_daemon": {"cmd": ["python3", "{loophole_dir}/srv.py"]}`)
	wantOneProblemWith(t, problems, `"publishes": "socket"`, "'host_daemon.publishes' is 'endpoint'")
}

// The legal pack-shipped daemon: publishes socket, names {socket}, lives in its own
// module dir.
func TestPackShippedAllowsPublishesSocket(t *testing.T) {
	problems := packProblems(t,
		`"host_daemon": {"cmd": ["python3", "{loophole_dir}/srv.py", "--socket", "{socket}"], "publishes": "socket"}`)
	if len(problems) != 0 {
		t.Fatalf("a socket-publishing daemon drew %v", problems)
	}
}

// A loophole with NO host daemon says nothing about publication, so it cannot
// violate the ruling — the `transport: none`, declarations-only shape (the audio
// example's, once its binds are home-relative).
func TestPackShippedAllowsNoHostDaemon(t *testing.T) {
	problems := packProblems(t, `"transport": "none",
		"host_bind_mounts": [{"host": "{loophole_dir}/asound.conf", "container": "/etc/asound.conf"}],
		"host_devices": ["/dev/snd"]`)
	if len(problems) != 0 {
		t.Fatalf("a daemonless loophole drew %v", problems)
	}
}

// Everything §3.1 lists as ALLOWED must pass in one manifest, or the subset is
// quietly narrower than the design says. host_devices, intercepts, broker_ip,
// ca_cert, state_files, requires, doctor_cmd, jail_daemon and platforms are all in.
func TestPackShippedAllowsEverythingElse(t *testing.T) {
	problems := packProblems(t, `
		"transport": "loopback-tls",
		"lifecycle": "spawned",
		"platforms": ["linux", "darwin/arm64"],
		"intercepts": [{"host": "api.acme.test"}],
		"broker_ip": "127.0.0.1",
		"ca_cert": "{state}/ca.crt",
		"state_files": ["ca.crt"],
		"requires": {"command_on_path": "python3"},
		"doctor_cmd": ["python3", "{loophole_dir}/doctor.py"],
		"host_devices": ["/dev/snd"],
		"host_bind_mounts": [{"host": "{loophole_dir}/conf", "container": "/etc/acme"}],
		"host_daemon": {"cmd": ["python3", "{loophole_dir}/srv.py", "--socket", "{socket}"],
			"publishes": "socket", "env": {"ACME_DEBUG": "1"}},
		"jail_daemon": {"cmd": ["python3", "{jail_loophole_dir}/agent.py"]}`)
	if len(problems) != 0 {
		t.Fatalf("an in-subset manifest drew %v", problems)
	}
}

// PackShippedError is nil (a TYPED nil, checked through the concrete type) for a
// clean manifest, and carries every problem for a dirty one.
func TestPackShippedErrorCarriesTheProblems(t *testing.T) {
	clean := decodePack(t, `"host_daemon": {"cmd": ["s", "{socket}"], "publishes": "socket"}`)
	if err := clean.PackShippedError(loopholedecl.ManifestPath(packDir)); err != nil {
		t.Fatalf("clean manifest: %v", err)
	}
	dirty := decodePack(t, `"jail_env": {"A": "1"}, "host_bind_mounts": [{"host": "../x", "container": "/y"}]`)
	err := dirty.PackShippedError(loopholedecl.ManifestPath(packDir))
	if err == nil {
		t.Fatal("dirty manifest returned no error")
	}
	if len(err.Problems()) != 2 {
		t.Errorf("Problems() = %v, want 2", err.Problems())
	}
	if !strings.Contains(err.Error(), "jail_env") || !strings.Contains(err.Error(), "'..' segment") {
		t.Errorf("Error() drops a problem: %s", err.Error())
	}
}

// THE ASYMMETRY IS THE POINT: these BUNDLED manifests violate the subset, and must
// keep working. `audio` names /run/user/<uid>/pulse through ${XDG_RUNTIME_DIR} with
// readonly:false and sets jail_env; the broker publishes its own endpoint. If a
// future change applied the subset unconditionally, this test is what says so.
//
// `host-processes` USED TO BE IN THIS LIST and deliberately is not any more — see
// TestBundledHostProcessesIsInsideThePackShippedSubset below. It left the list by
// declaring publishes:"socket", which was its only violation.
func TestBundledManifestsAreOutsideThePackShippedSubset(t *testing.T) {
	for _, name := range []string{"audio", "claude-oauth-broker"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("/loopholes", name)
			m, err := loopholedecl.Decode(bundledManifest(t, name), dir)
			if err != nil {
				t.Fatalf("strict decode: %v", err)
			}
			problems := m.PackShippedProblems(loopholedecl.ManifestPath(dir))
			if len(problems) == 0 {
				t.Fatalf("%s is inside the pack-shipped subset — if that is now true, this"+
					" test should assert it deliberately rather than by accident", name)
			}
		})
	}
}

// TestBundledHostProcessesIsInsideThePackShippedSubset is the deliberate positive
// the comment above asks for, rather than a name quietly dropped from a loop.
//
// The flip to publishes:"socket" removed this manifest's ONE subset violation, so
// it became shippable by a pack unchanged — and on 2026-08-18 it MOVED, into the
// official `host-processes` pack (bundledManifest now reads it from there).
//
// The assertion is kept, and it is worth more after the move than before: this is
// the property that made the move a file rename rather than a redesign, and it is
// what a change to either the subset or the manifest would have to break for the
// pack to start refusing at load — where a refused loophole does not error, it
// simply goes missing.
//
// `requires.command_on_path: "ps"` is part of what is being asserted: the subset
// scopes `requires.file_exists` and leaves `command_on_path` alone, because it asks
// PATH whether a PROGRAM NAME resolves rather than probing the user's files.
func TestBundledHostProcessesIsInsideThePackShippedSubset(t *testing.T) {
	dir := filepath.Join("/loopholes", "host-processes")
	m, err := loopholedecl.Decode(bundledManifest(t, "host-processes"), dir)
	if err != nil {
		t.Fatalf("strict decode: %v", err)
	}
	if problems := m.PackShippedProblems(loopholedecl.ManifestPath(dir)); len(problems) != 0 {
		t.Errorf("host-processes draws %v; after the publishes:\"socket\" flip it must draw none",
			problems)
	}
	if !m.Requires.CommandOnPathSet || m.Requires.CommandOnPath != "ps" {
		t.Errorf("requires.command_on_path = %q (set=%v), want \"ps\" — the subset does not"+
			" scope it, and the gate must survive the flip",
			m.Requires.CommandOnPath, m.Requires.CommandOnPathSet)
	}
}

// LoadDirPackShipped is the authoring seam: STRICT (so a typo is reported) plus the
// subset. Both refusals have to be reachable through it, or an authoring tool sees
// only half the problems.
func TestLoadDirPackShippedIsStrictAndAppliesTheSubset(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "acme-proxy")
	writeManifest(t, mod, `{"name": "acme-proxy", "host_deamon": {}}`)
	_, err := loopholedecl.LoadDirPackShipped(mod)
	if err == nil || !strings.Contains(err.Error(), "unknown key") {
		t.Fatalf("strict half: err = %v, want an unknown-key refusal", err)
	}

	writeManifest(t, mod, `{"name": "acme-proxy", "jail_env": {"A": "1"}}`)
	_, err = loopholedecl.LoadDirPackShipped(mod)
	if err == nil || !strings.Contains(err.Error(), `"kind": "env"`) {
		t.Fatalf("subset half: err = %v, want the jail_env refusal naming the env kind", err)
	}

	writeManifest(t, mod,
		`{"name": "acme-proxy", "host_daemon": {"cmd": ["s", "{socket}"], "publishes": "socket"}}`)
	m, err := loopholedecl.LoadDirPackShipped(mod)
	if err != nil {
		t.Fatalf("a legal pack-shipped manifest was refused: %v", err)
	}
	if m.Name != "acme-proxy" {
		t.Errorf("Name = %q", m.Name)
	}
}

// writeManifest creates dir and writes body as its manifest, overwriting any
// previous one so a test can walk a module through several states.
func writeManifest(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loopholedecl.ManifestPath(dir), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// containingAll returns the entries carrying every fragment.
func containingAll(list []string, fragments ...string) []string {
	var out []string
	for _, s := range list {
		match := true
		for _, f := range fragments {
			if !strings.Contains(s, f) {
				match = false
				break
			}
		}
		if match {
			out = append(out, s)
		}
	}
	return out
}

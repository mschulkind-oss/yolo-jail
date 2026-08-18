package loopholedecl_test

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

	bundledloopholes "github.com/mschulkind-oss/yolo-jail/bundled_loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
)

// bundledManifest reads one shipped manifest out of the embedded tree — the same
// source internal/loopholes reads through BundledLoopholesDir, so this test holds
// for an installed binary with no checkout, not just for this working copy.
func bundledManifest(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(bundledloopholes.FS, name+"/"+loopholedecl.ManifestName)
	if err != nil {
		t.Fatalf("embedded manifest for %s: %v", name, err)
	}
	return data
}

// TestBundledManifestsDecodeStrictly is the extraction's acceptance bar: all three
// manifests yolo SHIPS must decode through the new package with ZERO problems —
// including no unknown-key complaint about `"version": 1`, which every one of them
// declares and nothing reads. If the strict decoder rejected a shipped manifest,
// `yolo pack lint` would fail on yolo's own loopholes.
func TestBundledManifestsDecodeStrictly(t *testing.T) {
	// The declared default, PER MANIFEST, because after OQ-A9 they no longer agree —
	// and each disagreement is a ruling rather than an accident:
	//
	//   audio                 R4, "host access is never on by default". The one
	//                         behaviour change in the rename commit.
	//   claude-oauth-broker   OQ-A1. It must not gain a way to be silently off; a
	//                         jail-only claude user without it races the single-use
	//                         refresh token rather than merely losing a feature.
	//   host-processes        unchanged pending the pack conversion (§1.3 has it
	//                         ending at false, paid for by `packs:` selection). Its
	//                         capability is empty anyway until the workspace lists
	//                         names in `host_processes.visible` (§1.2a).
	//
	// A table rather than one blanket assertion because the blanket one — "every
	// bundled manifest declares enabled:true" — is what this change had to delete, and
	// a reader who flips a value here should have to say which ruling moved.
	wantDefaultEnabled := map[string]bool{
		"audio":               false,
		"claude-oauth-broker": true,
		"host-processes":      true,
	}
	for _, name := range []string{"audio", "claude-oauth-broker", "host-processes"} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("/loopholes", name)
			m, err := loopholedecl.Decode(bundledManifest(t, name), dir)
			if err != nil {
				t.Fatalf("strict decode: %v", err)
			}
			if m.Name != name {
				t.Errorf("Name = %q, want %q", m.Name, name)
			}
			if m.Version != 1 || !m.VersionSet {
				t.Errorf("Version = %d (set=%v), want 1 — `version` must be READ, not just tolerated",
					m.Version, m.VersionSet)
			}
			if m.DefaultEnabled != wantDefaultEnabled[name] {
				t.Errorf("DefaultEnabled = %v, want %v", m.DefaultEnabled, wantDefaultEnabled[name])
			}
			// The tolerant path must agree, key for key: these manifests cross the
			// version boundary into a jail whose baked entrypoint may be older.
			tol, skipped, err := loopholedecl.DecodeTolerant(bundledManifest(t, name), dir)
			if err != nil {
				t.Fatalf("tolerant decode: %v", err)
			}
			if len(skipped) != 0 {
				t.Errorf("tolerant decode skipped %v; a shipped manifest must be fully known", skipped)
			}
			if !reflect.DeepEqual(tol, m) {
				t.Errorf("strict and tolerant decodes disagree:\n strict = %+v\n tolerant = %+v", m, tol)
			}
		})
	}
}

// TestBundledAudioFields pins the shape of the one bundled loophole that is all
// declarations and no daemon: bind mounts, a device, jail env, and a `requires`
// gate — and every path still RAW, because ${XDG_RUNTIME_DIR} and {loophole_dir}
// resolve per machine.
func TestBundledAudioFields(t *testing.T) {
	m, err := loopholedecl.Decode(bundledManifest(t, "audio"), "/loopholes/audio")
	if err != nil {
		t.Fatal(err)
	}
	if m.Transport != loopholedecl.TransportNone {
		t.Errorf("Transport = %q, want %q", m.Transport, loopholedecl.TransportNone)
	}
	if m.Lifecycle != "external" {
		t.Errorf("Lifecycle = %q, want external", m.Lifecycle)
	}
	if m.HostDaemon != nil || m.JailDaemon != nil {
		t.Errorf("audio declares no daemon; got host=%+v jail=%+v", m.HostDaemon, m.JailDaemon)
	}
	if m.Requires.CommandOnPathSet {
		t.Errorf("audio gates on a FILE, not a command: %+v", m.Requires)
	}
	if !m.Requires.FileExistsSet || m.Requires.FileExists != "${XDG_RUNTIME_DIR}/pulse/native" {
		t.Errorf("requires.file_exists = %q (set=%v); the $VAR must survive decoding unexpanded",
			m.Requires.FileExists, m.Requires.FileExistsSet)
	}
	if len(m.HostBindMounts) != 3 {
		t.Fatalf("host_bind_mounts = %+v, want 3", m.HostBindMounts)
	}
	want := []loopholedecl.HostBindMount{
		{Host: "${XDG_RUNTIME_DIR}/pulse/native", Container: "/run/pulse/native", Readonly: false},
		{Host: "${XDG_RUNTIME_DIR}/pipewire-0", Container: "/run/pipewire/pipewire-0", Readonly: false},
		{Host: "{loophole_dir}/asound.conf", Container: "/etc/asound.conf", Readonly: true},
	}
	if !reflect.DeepEqual(m.HostBindMounts, want) {
		t.Errorf("host_bind_mounts =\n %+v\nwant\n %+v", m.HostBindMounts, want)
	}
	if !reflect.DeepEqual(m.HostDevices, []string{"/dev/snd"}) {
		t.Errorf("host_devices = %v, want [/dev/snd]", m.HostDevices)
	}
	if got, _ := m.JailEnv.Get("PULSE_SERVER"); got != "unix:/run/pulse/native" {
		t.Errorf("jail_env PULSE_SERVER = %q", got)
	}
	// Key ORDER is load-bearing: RuntimeArgsFor emits `-e K=V` in it.
	if got := m.JailEnv.Keys(); !reflect.DeepEqual(got, []string{"PULSE_SERVER", "PIPEWIRE_REMOTE"}) {
		t.Errorf("jail_env key order = %v; argv byte-stability depends on the manifest's order", got)
	}
}

// TestBundledBrokerFields pins the sharpest shipped manifest: a host daemon, a
// jail daemon, an intercept, a {state}-relative CA, and the state_files narrowing
// that keeps the CA's private key host-side (issue #33). Every one of these is a
// field the pack footprint has to report as a claim, so a decode that dropped one
// would silently shrink the consent string.
func TestBundledBrokerFields(t *testing.T) {
	m, err := loopholedecl.Decode(bundledManifest(t, "claude-oauth-broker"), "/loopholes/claude-oauth-broker")
	if err != nil {
		t.Fatal(err)
	}
	if m.Transport != loopholedecl.TransportLoopbackTLS || m.Lifecycle != "spawned" {
		t.Errorf("transport/lifecycle = %q/%q", m.Transport, m.Lifecycle)
	}
	if !reflect.DeepEqual(m.Intercepts, []loopholedecl.Intercept{{Host: "platform.claude.com"}}) {
		t.Errorf("intercepts = %+v", m.Intercepts)
	}
	if m.BrokerIP != "127.0.0.1" {
		t.Errorf("broker_ip = %q, want 127.0.0.1 (the declared value, not the default)", m.BrokerIP)
	}
	if !m.CACertSet || m.CACert != "{state}/ca.crt" {
		t.Errorf("ca_cert = %q (set=%v); {state} resolves at RUNTIME, so it must survive decoding",
			m.CACert, m.CACertSet)
	}
	if !reflect.DeepEqual(m.StateFiles, []string{"ca.crt", "server.crt", "server.key"}) {
		t.Errorf("state_files = %v; narrowing the state mount is what keeps ca.key host-side", m.StateFiles)
	}
	wantCmd := []string{"yolo", "internal", "daemon", "claude-oauth-broker", "--socket", "{socket}"}
	if m.HostDaemon == nil || !reflect.DeepEqual(m.HostDaemon.Cmd, wantCmd) {
		t.Fatalf("host_daemon = %+v, want cmd %v", m.HostDaemon, wantCmd)
	}
	if m.HostDaemon.Publishes != loopholedecl.PublishesEndpoint {
		t.Errorf("publishes = %q, want the default %q", m.HostDaemon.Publishes, loopholedecl.PublishesEndpoint)
	}
	if m.HostDaemon.RequestEnd != loopholedecl.RequestEndFramed {
		t.Errorf("request_end = %q, want the default %q", m.HostDaemon.RequestEnd, loopholedecl.RequestEndFramed)
	}
	if m.JailDaemon == nil || !reflect.DeepEqual(m.JailDaemon.Cmd, []string{"yolo-jaild", "oauth-terminator"}) {
		t.Fatalf("jail_daemon = %+v", m.JailDaemon)
	}
	if m.JailDaemon.Restart != "on-failure" {
		t.Errorf("restart = %q", m.JailDaemon.Restart)
	}
	if !m.DoctorCmdSet || len(m.DoctorCmd) != 5 {
		t.Errorf("doctor_cmd = %v (set=%v) — host execution the footprint must claim",
			m.DoctorCmd, m.DoctorCmdSet)
	}
	if !m.Requires.CommandOnPathSet || m.Requires.CommandOnPath != "claude" {
		t.Errorf("requires.command_on_path = %q (set=%v)", m.Requires.CommandOnPath, m.Requires.CommandOnPathSet)
	}
}

// TestBundledHostProcessesFields pins the third manifest: a host daemon that binds
// a plain AF_UNIX socket and lets yolo publish the endpoint file in front of it,
// and no intercepts at all.
//
// The {socket}/publishes pair is asserted TOGETHER because the two halves are one
// fact: parseHostDaemon REFUSES a {endpoint} argv under publishes:"socket", and a
// refused manifest does not surface as an error at launch — the loophole simply
// goes missing. So a half-applied edit here is silent everywhere except this test.
//
// `preamble` is asserted at its DEFAULT rather than declared in the manifest: the
// decoder is where the default lives, and this daemon is yolo's own code reading
// the preamble through hostservice.ServeFrontedUnix.
func TestBundledHostProcessesFields(t *testing.T) {
	m, err := loopholedecl.Decode(bundledManifest(t, "host-processes"), "/loopholes/host-processes")
	if err != nil {
		t.Fatal(err)
	}
	if m.Transport != loopholedecl.TransportLoopbackTLS {
		t.Errorf("transport = %q", m.Transport)
	}
	if len(m.Intercepts) != 0 {
		t.Errorf("intercepts = %+v, want none", m.Intercepts)
	}
	if m.BrokerIP != loopholedecl.DefaultBrokerIP {
		t.Errorf("broker_ip = %q, want the default %q", m.BrokerIP, loopholedecl.DefaultBrokerIP)
	}
	wantCmd := []string{"yolo", "internal", "daemon", "host-processes", "--socket", "{socket}"}
	if m.HostDaemon == nil || !reflect.DeepEqual(m.HostDaemon.Cmd, wantCmd) {
		t.Fatalf("host_daemon = %+v, want cmd %v", m.HostDaemon, wantCmd)
	}
	if m.HostDaemon.Publishes != loopholedecl.PublishesSocket {
		t.Errorf("publishes = %q, want %q — the daemon binds the socket and yolo fronts it",
			m.HostDaemon.Publishes, loopholedecl.PublishesSocket)
	}
	if !m.HostDaemon.Preamble {
		t.Errorf("preamble = false; this manifest declares nothing, so it must decode to the " +
			"default ON — the daemon reads the preamble and the access line's jail= depends on it")
	}
	if m.HostDaemon.RequestEnd != loopholedecl.RequestEndFramed {
		t.Errorf("request_end = %q, want the default %q — hostservice reads exactly one frame "+
			"and never to EOF", m.HostDaemon.RequestEnd, loopholedecl.RequestEndFramed)
	}
	if m.CACertSet || m.JailDaemon != nil || len(m.StateFiles) != 0 {
		t.Errorf("unexpected extras: ca=%v jail=%+v state=%v", m.CACertSet, m.JailDaemon, m.StateFiles)
	}
}

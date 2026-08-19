package loopholedecl_test

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

	bundledloopholes "github.com/mschulkind-oss/yolo-jail/bundled_loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// shippedManifestHome records WHICH EMBED each manifest yolo ships lives in, and it
// is a table rather than a probe because the answer is the sprint's whole subject:
// `bundled_loopholes/` is being emptied one conversion at a time
// (docs/design/broker-as-a-pack.md OQ-BP4), so a manifest that changes homes must
// change a line here rather than be found wherever it happens to be.
//
// An empty pack name means the bundled embed. When the last one goes, this table
// loses its second column and the bundled reader with it.
//
// `journal` is the odd one and is worth spotting here: it never lived in
// `bundled_loopholes/` at all, because it was not a bundled loophole — it was a
// BUILTIN SERVICE with no manifest anywhere, switched by a top-level config key. It
// enters this table straight into a pack (loophole-activation.md OQ-A6).
var shippedManifestHome = map[string]string{
	"claude-oauth-broker": "",
	"audio":               "audio",
	"host-processes":      "host-processes",
	"journal":             "journal",
}

// bundledManifest reads one shipped manifest out of the embedded tree — the same
// source internal/loopholes reads through BundledLoopholesDir, so this test holds
// for an installed binary with no checkout, not just for this working copy.
func bundledManifest(t *testing.T, name string) []byte {
	t.Helper()
	if pack := shippedManifestHome[name]; pack != "" {
		return packLoopholeManifest(t, pack, name)
	}
	data, err := fs.ReadFile(bundledloopholes.FS, name+"/"+loopholedecl.ManifestName)
	if err != nil {
		t.Fatalf("embedded manifest for %s: %v", name, err)
	}
	return data
}

// packLoopholeManifest reads a loophole manifest out of an OFFICIAL PACK's embed.
//
// The pack embed rather than the on-disk tree, for the same reason bundledManifest
// reads its own: an installed binary carries the packs and no checkout, so a test
// that walked packs/ would pass here and say nothing about what ships.
func packLoopholeManifest(t *testing.T, pack, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(packs.FS, pack+"/loopholes/"+name+"/"+loopholedecl.ManifestName)
	if err != nil {
		t.Fatalf("embedded manifest for pack %s loophole %s: %v", pack, name, err)
	}
	return data
}

// TestBundledManifestsDecodeStrictly is the extraction's acceptance bar: all three
// manifests yolo SHIPS must decode through the new package with ZERO problems —
// including no unknown-key complaint about `"version": 1`, which every one of them
// declares and nothing reads. If the strict decoder rejected a shipped manifest,
// `yolo pack lint` would fail on yolo's own loopholes.
//
// "Shipped" is deliberately not "bundled" any more: `host-processes` moved into an
// official pack on 2026-08-18 and is read through shippedManifestHome above. The
// acceptance bar is about what a release CARRIES, which both embeds are.
func TestBundledManifestsDecodeStrictly(t *testing.T) {
	// The declared default, PER MANIFEST, because after OQ-A9 they no longer agree —
	// and each disagreement is a ruling rather than an accident:
	//
	//   audio                 R4, "host access is never on by default". The one
	//                         behaviour change in the rename commit.
	//   claude-oauth-broker   OQ-A1. It must not gain a way to be silently off; a
	//                         jail-only claude user without it races the single-use
	//                         refresh token rather than merely losing a feature.
	//   host-processes        R4 as of the pack conversion (2026-08-18). §1.3's table
	//                         had it ending at false, paid for by `packs:` selection,
	//                         and this is the commit that pays: the pack is what turns
	//                         it back on, so the capability is no longer removed with
	//                         nothing to restore it.
	//   journal               false, and unlike the two above it takes nothing away:
	//                         the bridge was ALREADY off unless a top-level `journal`
	//                         key said otherwise, so this is the same answer written in
	//                         the new vocabulary. What the conversion adds is the pack
	//                         selection, not the switch.
	//
	// A table rather than one blanket assertion because the blanket one — "every
	// bundled manifest declares enabled:true" — is what this change had to delete, and
	// a reader who flips a value here should have to say which ruling moved.
	wantDefaultEnabled := map[string]bool{
		"audio":               false,
		"claude-oauth-broker": true,
		"host-processes":      false,
		"journal":             false,
	}
	for _, name := range []string{"audio", "claude-oauth-broker", "host-processes", "journal"} {
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

// TestBundledAudioFields pins the shape of the one shipped loophole that is all
// declarations and no daemon: bind mounts and a device — and every path still RAW,
// because ${XDG_RUNTIME_DIR} and {loophole_dir} resolve per machine.
//
// FOUR of its fields changed when the two audio loopholes merged into the official
// `audio` pack on 2026-08-18, and each is a decision rather than a port:
//
//	readonly: true on both sockets  the pack-shipped subset refuses `readonly: false`,
//	                                and for a socket that refusal costs nothing — the
//	                                kernel exempts non-REG/DIR/LNK inodes from the
//	                                read-only check, so the bind stays bidirectional.
//	no jail_env                     refused for a pack; PULSE_SERVER/PIPEWIRE_REMOTE
//	                                are the pack's `env` contribution now, and
//	                                therefore unconditional (OQ-LP5).
//	platforms instead of requires   `requires.file_exists` is still path-scoped for a
//	                                pack, and `platforms: ["linux"]` answers the
//	                                question the probe was really asking.
//	the conf.d destination          kept from the audio-alsa sibling, which is the
//	                                spelling measured working in this repo's jail.
//
// What did NOT change is the one that matters most: the `${XDG_RUNTIME_DIR}` bind
// hosts survive decoding unexpanded, which is what OQ-LP14's withdrawal made
// expressible for a pack at all.
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
	if m.Requires.CommandOnPathSet || m.Requires.FileExistsSet {
		t.Errorf("requires = %+v — the probe was replaced by `platforms`, and reinstating "+
			"it would draw a pack-shipped subset refusal (file_exists is still scoped)",
			m.Requires)
	}
	if !m.PlatformsSet || !reflect.DeepEqual(m.Platforms, []string{"linux"}) {
		t.Errorf("platforms = %v (set=%v), want [linux]", m.Platforms, m.PlatformsSet)
	}
	if len(m.HostBindMounts) != 3 {
		t.Fatalf("host_bind_mounts = %+v, want 3", m.HostBindMounts)
	}
	want := []loopholedecl.HostBindMount{
		{Host: "${XDG_RUNTIME_DIR}/pulse/native", Container: "/run/pulse/native", Readonly: true},
		{Host: "${XDG_RUNTIME_DIR}/pipewire-0", Container: "/run/pipewire/pipewire-0", Readonly: true},
		{Host: "{loophole_dir}/asound.conf",
			Container: "/etc/alsa/conf.d/50-yolo-audio-alsa.conf", Readonly: true},
	}
	if !reflect.DeepEqual(m.HostBindMounts, want) {
		t.Errorf("host_bind_mounts =\n %+v\nwant\n %+v", m.HostBindMounts, want)
	}
	if !reflect.DeepEqual(m.HostDevices, []string{"/dev/snd"}) {
		t.Errorf("host_devices = %v, want [/dev/snd]", m.HostDevices)
	}
	if m.JailEnv != nil && m.JailEnv.Len() != 0 {
		t.Errorf("jail_env = %v — refused for a pack-shipped loophole; the variables are "+
			"the pack's `env` contribution", m.JailEnv.Keys())
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
	wantCmd := []string{
		"yolo", "internal", "daemon", "host-processes",
		"--socket", "{socket}", "--settings", "{settings}",
	}
	if m.HostDaemon == nil || !reflect.DeepEqual(m.HostDaemon.Cmd, wantCmd) {
		t.Fatalf("host_daemon = %+v, want cmd %v", m.HostDaemon, wantCmd)
	}
	// The SETTINGS half, pinned together with the argv that consumes it, because the
	// two are one fact: the {settings} token resolves to a file yolo writes FROM these
	// declarations, so a manifest carrying the token with no declarations is refused at
	// load and a manifest carrying declarations no argv names writes a file nobody
	// reads. Both keys are `workspace` scope, which is what preserves the one thing
	// this loophole has always let a workspace do.
	if len(m.Settings) != 2 {
		t.Fatalf("settings = %+v, want the two declared keys (visible, fields)", m.Settings)
	}
	for _, want := range []struct {
		key, typ, scope string
		def             any
	}{
		{"visible", loopholedecl.SettingTypeStringList, loopholedecl.SettingScopeWorkspace,
			[]string{}},
		{"fields", loopholedecl.SettingTypeStringList, loopholedecl.SettingScopeWorkspace,
			[]string{"pid", "comm", "args", "etime", "%cpu", "%mem", "rss"}},
	} {
		got, ok := loopholedecl.SettingByKey(m.Settings, want.key)
		if !ok {
			t.Fatalf("settings has no %q", want.key)
		}
		if got.Type != want.typ || got.Scope != want.scope {
			t.Errorf("settings.%s = type %q scope %q, want %q/%q",
				want.key, got.Type, got.Scope, want.typ, want.scope)
		}
		if !reflect.DeepEqual(got.Default, want.def) {
			t.Errorf("settings.%s default = %#v, want %#v", want.key, got.Default, want.def)
		}
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

// TestShippedJournalFields pins the manifest that turned a BUILTIN SERVICE into a
// loophole, and pins the two declarations that carry the ruling rather than the port.
//
// `scope: "user"` on `full` IS OQ-K4's security half. `"journal": "full"` — read the
// whole host journal, every unit, every user — used to be settable from a workspace
// `yolo-jail.jsonc`, a file the agent inside the jail can rewrite, with no scope rule
// anywhere. Nothing in core enforces that any more; the manifest does, and a silent
// drift to `workspace` here would re-open the hole with no other test failing.
//
// The TYPE is the other one. The settings type set is closed and has no `enum`, so a
// `string` mode would be unvalidatable by core — and ParseRequest narrows on the exact
// literal "user", meaning every other spelling behaves as FULL. `bool` is what makes a
// config typo unable to widen host access.
//
// The {socket}/publishes pair is asserted TOGETHER because the two halves are one fact:
// parseHostDaemon REFUSES a {endpoint} argv under publishes:"socket", and a refused
// manifest does not surface as an error at launch — the loophole simply goes missing.
func TestShippedJournalFields(t *testing.T) {
	dir := filepath.Join("/loopholes", "journal")
	m, err := loopholedecl.Decode(bundledManifest(t, "journal"), dir)
	if err != nil {
		t.Fatal(err)
	}
	if m.Transport != loopholedecl.TransportLoopbackTLS {
		t.Errorf("transport = %q", m.Transport)
	}
	if !m.PlatformsSet || !reflect.DeepEqual(m.Platforms, []string{"linux"}) {
		t.Errorf("platforms = %v (set=%v), want [linux] — journalctl is Linux, and DECLARING "+
			"it is what tells a macOS reader there is nothing to install",
			m.Platforms, m.PlatformsSet)
	}
	// NO `requires` probe, and its absence is a decision (R3). A Linux host without
	// systemd should hear "journalctl not found on host" per request rather than watch
	// the loophole vanish from `yolo loopholes list` with no reason given.
	if m.Requires.CommandOnPathSet || m.Requires.FileExistsSet {
		t.Errorf("requires = %+v, want none — a missing program must fail loudly at spawn, "+
			"not make the loophole disappear from every surface", m.Requires)
	}
	wantCmd := []string{
		"yolo", "internal", "daemon", "journal",
		"--socket", "{socket}", "--settings", "{settings}",
	}
	if m.HostDaemon == nil || !reflect.DeepEqual(m.HostDaemon.Cmd, wantCmd) {
		t.Fatalf("host_daemon = %+v, want cmd %v", m.HostDaemon, wantCmd)
	}
	if m.HostDaemon.Publishes != loopholedecl.PublishesSocket {
		t.Errorf("publishes = %q, want %q — a pack-shipped loophole may not self-publish, "+
			"and the daemon's --endpoint flag is retired accordingly",
			m.HostDaemon.Publishes, loopholedecl.PublishesSocket)
	}
	if !m.HostDaemon.Preamble {
		t.Errorf("preamble = false; this manifest declares nothing, so it must decode to the " +
			"default ON — journald.ServeFrontedUnix consumes the frame, and a daemon that " +
			"did not would parse the preamble AS the request and answer every call " +
			"\"malformed request\"")
	}
	if m.HostDaemon.RequestEnd != loopholedecl.RequestEndFramed {
		t.Errorf("request_end = %q, want the default %q — the request is one newline-terminated "+
			"line and the daemon never reads to EOF",
			m.HostDaemon.RequestEnd, loopholedecl.RequestEndFramed)
	}
	if len(m.Settings) != 1 {
		t.Fatalf("settings = %+v, want exactly the one declared key (full)", m.Settings)
	}
	full, ok := loopholedecl.SettingByKey(m.Settings, "full")
	if !ok {
		t.Fatalf("settings has no %q; got %+v", "full", m.Settings)
	}
	if full.Type != loopholedecl.SettingTypeBool {
		t.Errorf("settings.full type = %q, want %q — a string mode is unvalidatable (no enum "+
			"in the closed type set) and every misspelling of it reads as FULL",
			full.Type, loopholedecl.SettingTypeBool)
	}
	if full.Scope != loopholedecl.SettingScopeUser {
		t.Errorf("settings.full scope = %q, want %q — this is OQ-K4's security half: reading "+
			"the whole host journal must not be settable from an agent-editable workspace file",
			full.Scope, loopholedecl.SettingScopeUser)
	}
	if full.Default != false {
		t.Errorf("settings.full default = %#v, want false — silence must not escalate", full.Default)
	}
	// A doctor_cmd would run on the HOST on every `yolo check`, and there is nothing
	// here it could ask that the jail's first request does not answer better.
	if m.DoctorCmdSet {
		t.Errorf("doctor_cmd = %v, want none", m.DoctorCmd)
	}
	if m.CACertSet || m.JailDaemon != nil || len(m.StateFiles) != 0 ||
		len(m.HostBindMounts) != 0 || len(m.HostDevices) != 0 {
		t.Errorf("unexpected extras: ca=%v jail=%+v state=%v binds=%v devices=%v",
			m.CACertSet, m.JailDaemon, m.StateFiles, m.HostBindMounts, m.HostDevices)
	}
}

package loopholedecl_test

import (
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/loopholedecl"
	"github.com/mschulkind-oss/yolo-jail/packs"
)

// shippedManifestHome records WHICH OFFICIAL PACK each manifest yolo ships lives in.
//
// IT USED TO RECORD WHICH EMBED, with an empty value meaning `bundled_loopholes/`. That
// was the sprint's whole subject: the channel was emptied one conversion at a time and
// `claude-oauth-broker` was the last one out on 2026-08-19
// (docs/design/broker-as-a-pack.md OQ-BP4), so every value now names a pack and the
// bundled reader is gone with the choice it recorded.
//
// The broker's row is the one to read twice: `claude`, the AGENT pack, not a
// `claude-oauth-broker` pack of its own (loophole-activation.md OQ-A10). The dependency is
// structural — the broker exists to serve claude — so selecting the claude pack IS the
// dependency, and a pack of its own would reinstate the second selection step that ruling
// deletes.
//
// `journal` is still worth spotting: it never lived in `bundled_loopholes/` at all,
// because it was not a bundled loophole — it was a BUILTIN SERVICE with no manifest
// anywhere, switched by a top-level config key. It entered this table straight into a pack
// (loophole-activation.md OQ-A6).
var shippedManifestHome = map[string]string{
	"claude-oauth-broker": "claude",
	"audio":               "audio",
	"host-processes":      "host-processes",
	"journal":             "journal",
	"cgroup-delegate":     "cgroup-delegate",
}

// TestShippedManifestHomeIsTotal is the forcing function the table above needs to be
// worth having: EVERY loophole in the pack embed must have a row.
//
// Without it the table is a whitelist of what somebody remembered, and the tests it
// drives — the strict/tolerant decode bar, the per-manifest field pins, the
// default_enabled census — all iterate the table, so a NEW shipped loophole is covered by
// none of them and nothing says so. Measured 2026-08-18: adding a directory to
// bundled_loopholes/ AND to its embed directive passed the entire unit gate, including
// that channel's own drift test, whose job was only to catch a directive that was NOT
// updated.
//
// Adding a loophole is not a defect, so the failure asks for a ROW rather than a revert.
func TestShippedManifestHomeIsTotal(t *testing.T) {
	found := map[string]string{}

	packDirs, err := fs.ReadDir(packs.FS, ".")
	if err != nil {
		t.Fatalf("reading the official pack embed: %v", err)
	}
	for _, p := range packDirs {
		if !p.IsDir() {
			continue
		}
		// Most official packs ship no loophole at all (the agent packs), so an
		// unreadable loopholes/ dir is the ordinary case rather than a fault.
		mods, err := fs.ReadDir(packs.FS, p.Name()+"/loopholes")
		if err != nil {
			continue
		}
		for _, m := range mods {
			if m.IsDir() {
				found[m.Name()] = p.Name()
			}
		}
	}

	if len(found) == 0 {
		t.Fatal("the pack embed yielded no loophole — this test would then pass over an " +
			"empty table, which is the one outcome it must not have")
	}
	for name, pack := range found {
		home, listed := shippedManifestHome[name]
		if !listed {
			t.Errorf("the loophole %q ships (from packs/%s/loopholes/) and shippedManifestHome "+
				"has no row for it. Every test in this file iterates that table, so an unlisted "+
				"loophole's manifest is never decoded, never field-checked and never in the "+
				"default_enabled census — add the row rather than deleting this check.",
				name, pack)
			continue
		}
		if home != pack {
			t.Errorf("shippedManifestHome[%q] = %q but the manifest is in packs/%s/loopholes/ — "+
				"the table is the record of WHICH PACK each one lives in, and a stale row sends "+
				"every reader of it to the wrong tree", name, home, pack)
		}
	}
	for name, home := range shippedManifestHome {
		if _, ok := found[name]; !ok {
			t.Errorf("shippedManifestHome lists %q (pack %q) but the embed does not carry it — a "+
				"row for a loophole that no longer ships makes shippedManifest fail with "+
				"'embedded manifest for pack ...' rather than saying the loophole was removed",
				name, home)
		}
	}
}

// shippedManifest reads one shipped manifest out of the OFFICIAL PACK embed.
//
// The embed rather than the on-disk tree: an installed binary carries the packs and no
// checkout, so a test that walked packs/ would pass here and say nothing about what ships.
func shippedManifest(t *testing.T, name string) []byte {
	t.Helper()
	pack, ok := shippedManifestHome[name]
	if !ok {
		t.Fatalf("no shippedManifestHome row for %q", name)
	}
	data, err := fs.ReadFile(packs.FS, pack+"/loopholes/"+name+"/"+loopholedecl.ManifestName)
	if err != nil {
		t.Fatalf("embedded manifest for pack %s loophole %s: %v", pack, name, err)
	}
	return data
}

// TestShippedManifestsDecodeStrictly is the extraction's acceptance bar: every
// manifest yolo SHIPS must decode through the new package with ZERO problems —
// including no unknown-key complaint about `"version": 1`, which every one of them
// declares and nothing reads. If the strict decoder rejected a shipped manifest,
// `yolo pack lint` would fail on yolo's own loopholes.
//
// "Shipped" is deliberately not "bundled" any more, and as of 2026-08-19 there is no
// bundled embed left to mean: every one of these is read out of the official pack embed
// through shippedManifestHome above. The acceptance bar is about what a release CARRIES.
func TestShippedManifestsDecodeStrictly(t *testing.T) {
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
	//   cgroup-delegate       R4/OQ-A4, and the only one whose flip has a STATED,
	//                         ACCEPTED COST rather than a migration: yolo-cglimit stops
	//                         working out of the box. It was PRESENCE-ACTIVATED — no
	//                         config key existed at all — so this value is the whole
	//                         switch rather than a default over one.
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
		"cgroup-delegate":     false,
	}
	for _, name := range []string{
		"audio", "claude-oauth-broker", "host-processes", "journal", "cgroup-delegate",
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join("/loopholes", name)
			m, err := loopholedecl.Decode(shippedManifest(t, name), dir)
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
			tol, skipped, err := loopholedecl.DecodeTolerant(shippedManifest(t, name), dir)
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

// TestShippedAudioFields pins the shape of the one shipped loophole that is all
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
func TestShippedAudioFields(t *testing.T) {
	m, err := loopholedecl.Decode(shippedManifest(t, "audio"), "/loopholes/audio")
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

// TestShippedBrokerFields pins the sharpest shipped manifest: a host daemon, a
// jail daemon, an intercept, a {state}-relative CA, and the state_files narrowing
// that keeps the CA's private key host-side (issue #33). Every one of these is a
// field the pack footprint has to report as a claim, so a decode that dropped one
// would silently shrink the consent string.
func TestShippedBrokerFields(t *testing.T) {
	m, err := loopholedecl.Decode(shippedManifest(t, "claude-oauth-broker"), "/loopholes/claude-oauth-broker")
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
	// THE THREE FIELDS THAT CARRY THE CREDENTIAL PATH, and they are asserted here
	// because a half-applied edit to any one of them is silent everywhere else.
	//
	// publishes:"socket" + scope:"host" is the pair that replaced the per-jail
	// broker relay (docs/design/broker-as-a-pack.md §7): the daemon binds ONE
	// host-wide socket and yolo runs a front per jail over it. Flipping `publishes`
	// back to the default would make yolo wait for an endpoint file this daemon
	// never writes; dropping `scope` would make the run pipeline SPAWN a second
	// broker per jail, which is the concurrent single-use-refresh-token race the
	// flock exists to prevent (agent-credentials.md §2.5) — and neither shows up as
	// an error, only as a jail that cannot refresh.
	//
	// `preamble` is asserted at its DEFAULT rather than declared: the daemon reads
	// yolo's connection preamble through hostservice.ServeFrontedUnix, and that is
	// the whole of what makes the jail_id on its audit line host-asserted now that
	// the relay's stamp is deleted (invariant I1). A manifest writing
	// `"preamble": false` here would blind it silently.
	if m.HostDaemon.Publishes != loopholedecl.PublishesSocket {
		t.Errorf("publishes = %q, want %q — the daemon binds the host-wide socket and yolo "+
			"fronts it per jail", m.HostDaemon.Publishes, loopholedecl.PublishesSocket)
	}
	if m.HostDaemon.Scope != loopholedecl.ScopeHost {
		t.Errorf("scope = %q, want %q — ONE broker per host serving every jail; a per-jail "+
			"scope means a second copy of the daemon holding the refresh flock",
			m.HostDaemon.Scope, loopholedecl.ScopeHost)
	}
	if !m.HostDaemon.Preamble {
		t.Errorf("preamble = false; this manifest declares nothing, so it must decode to the " +
			"default ON — the broker reads the frame, and its jail= is host-asserted only " +
			"because of it")
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
	// NO `requires` PROBE, and its absence is a ruling rather than an omission (R3, made
	// free by R6 — loophole-activation.md). It used to read
	// `requires.command_on_path: "claude"`, a HOST-side exec.LookPath standing in for "is
	// there a claude to refresh for". That is wrong for the product's main case in the
	// direction that costs a user their credentials: a jail-only user installs claude
	// INSIDE the jail via the lazy launcher and never on the host, so the probe read false,
	// the loophole went inactive on every surface with no reason given, and the refresh
	// serialization went with it. Selecting `packs: ["claude"]` is the dependency the
	// sniff approximated, and it is a declaration rather than a guess.
	//
	// Reinstating it would be silent: an inactive loophole is not an error anywhere.
	if m.Requires.CommandOnPathSet || m.Requires.FileExistsSet {
		t.Errorf("requires = %+v, want none — the host-side `claude` probe was deleted when "+
			"this manifest moved into packs/claude, because selecting the pack IS the "+
			"dependency and the probe read false for the jail-only user it matters most to",
			m.Requires)
	}
}

// TestShippedHostProcessesFields pins the third manifest: a host daemon that binds
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
func TestShippedHostProcessesFields(t *testing.T) {
	m, err := loopholedecl.Decode(shippedManifest(t, "host-processes"), "/loopholes/host-processes")
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
	m, err := loopholedecl.Decode(shippedManifest(t, "journal"), dir)
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

// TestShippedCgroupDelegateFields pins the manifest that is a SWITCH AND NOTHING ELSE,
// and the emptiness is the assertion rather than a shrug.
//
// The delegate is yolo's own in-process goroutine on an AF_UNIX socket — the last
// service in the tree not on loopback-tls, because its whole security model is
// SO_PEERCRED and a TCP hop carries no peer credential. So this manifest declares no
// host_daemon, no bind mount, no device, no jail_env and no settings; what it declares
// is `default_enabled: false`, which is the entire content of OQ-A4.
//
// A `host_daemon` appearing here later would not be an addition, it would be a second
// implementation: the spawn loop would start it BESIDE the in-process delegate, both
// answering to the same name.
func TestShippedCgroupDelegateFields(t *testing.T) {
	m, err := loopholedecl.Decode(shippedManifest(t, "cgroup-delegate"), "/loopholes/cgroup-delegate")
	if err != nil {
		t.Fatal(err)
	}
	if m.Transport != loopholedecl.TransportNone {
		t.Errorf("transport = %q, want %q — this manifest declares no daemon, and claiming "+
			"loopback-tls would advertise a publication that never happens (nothing writes "+
			"cgroup-delegate.endpoint; the jail reads the _SOCKET variable)",
			m.Transport, loopholedecl.TransportNone)
	}
	if m.HostDaemon != nil {
		t.Errorf("host_daemon = %+v — the delegate is an IN-PROCESS goroutine, so a declared "+
			"daemon would be spawned BESIDE it under the same name rather than instead of it",
			m.HostDaemon)
	}
	if !m.PlatformsSet || !reflect.DeepEqual(m.Platforms, []string{"linux"}) {
		t.Errorf("platforms = %v (set=%v), want [linux]", m.Platforms, m.PlatformsSet)
	}
	// The cgroup-v2 check deliberately did NOT move into the manifest: "Linux" is a
	// machine class, "this kernel delegates cgroup v2" is a runtime fact about one host,
	// and it stays in startCgroupDelegateInProc where it can be reported in terms an
	// operator can act on.
	if m.Requires.CommandOnPathSet || m.Requires.FileExistsSet {
		t.Errorf("requires = %+v, want none", m.Requires)
	}
	if len(m.Settings) != 0 {
		t.Errorf("settings = %+v, want none — what the delegate may do is not configurable; "+
			"the limits are arguments the jail passes per call", m.Settings)
	}
	if m.DoctorCmdSet || m.CACertSet || m.JailDaemon != nil ||
		len(m.StateFiles) != 0 || len(m.HostBindMounts) != 0 || len(m.HostDevices) != 0 {
		t.Errorf("unexpected extras: doctor=%v ca=%v jail=%+v state=%v binds=%v devices=%v",
			m.DoctorCmdSet, m.CACertSet, m.JailDaemon, m.StateFiles, m.HostBindMounts, m.HostDevices)
	}
}

package run

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/jailcontent"
	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholegrant"
	"github.com/mschulkind-oss/yolo-jail/internal/loopholes"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// loopholegrantlaunch_test.go drives the LAUNCH half of docs/reference/loophole-grants.md
// (§4.1, §4.4, §4.6, §5; plan T6) through the launch's own functions: its config-change gate
// (checkConfigChanges), its spawn (startLoopholesMatching), its argv (loopholesRuntimeArgs), its
// pack disclosure (notePackHostAccess) and its briefing list (briefingLoopholes). Each fixture is a
// grant-declaring pack the launch selects with no `loopholes.<name>.enabled` anywhere.

// grantLaunch is one fixture launch: its Options, its workspace and the stream it printed.
type grantLaunch struct {
	o   *Options
	ws  string
	out *bytes.Buffer
}

// newGrantLaunch selects one grant-declaring pack (name, its loophole.toml manifest) under a TOML
// user config holding userTOML and a workspace config holding wsTOML ("" writes none), and returns
// the launch, its gate not yet run.
func newGrantLaunch(t *testing.T, name, manifest, userTOML, wsTOML string) grantLaunch {
	t.Helper()
	redirectState(t)
	module := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(module, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "loophole.toml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, "asound.conf"), []byte("# fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	restore := loopholes.SnapshotPackModules()
	t.Cleanup(restore)
	loopholes.SetPackModules([]loopholes.PackModule{{Dir: module, Pack: name, HostExecApproved: true}})

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("YOLO_USER_LAYER", "")
	userDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userDir, paths.UserConfigTOMLName), []byte(userTOML), 0o600); err != nil {
		t.Fatal(err)
	}
	ws, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if wsTOML != "" {
		if err := os.WriteFile(filepath.Join(ws, config.WorkspaceTOMLConfigName), []byte(wsTOML), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var out bytes.Buffer
	o := &Options{Workspace: ws}
	fillDefaults(o)
	o.Stdout, o.Stderr = &out, &out
	o.IsTTYStdin = func() bool { return false }
	o.IsTTYStdout = func() bool { return false }
	o.IsTTYStderr = func() bool { return false }
	o.ServiceReadyTimeout = 2 * time.Second
	o.ServiceTermGrace = time.Second
	o.runtime = "podman"
	return grantLaunch{o: o, ws: ws, out: &out}
}

// gate runs the launch's config-change gate, answered as --accept-config-changes answers it.
func (g grantLaunch) gate(t *testing.T) {
	t.Helper()
	g.o.AcceptConfigChanges = true
	if !g.o.checkConfigChanges(jsonx.NewOrderedMap(), "podman") {
		t.Fatalf("the config-change gate stopped the fixture launch:\n%s", g.out.String())
	}
}

// journalShaped is a mediated grant-declaring manifest with no settings: a host daemon that records
// the grant file it was handed ($1) into marker ($2) and exits, as recordSettingsInput does.
func journalShaped(marker string) string {
	return fmt.Sprintf(`name = "journal"
description = "a journal-shaped fixture"
version = 1
transport = "loopback-tls"
access = "read the host's journal"

[host_daemon]
cmd = ["/bin/sh", "-c", %q, "daemon", "{settings}", %q, "{socket}"]
publishes = "socket"

[[grants]]
kind = "read"
text = "read your user journal"
requestable_by_default = true

[[grants]]
kind = "read_all"
text = "read the whole host journal"
`, recordSettingsInput+"; exit 3", marker)
}

// spawnAndReadGrantFile starts the launch's loopholes through the production spawn and returns the
// grant file the fixture daemon was handed, as loopholegrant.Read reads it, and its path.
func (g grantLaunch) spawnAndReadGrantFile(t *testing.T, marker string) (*loopholegrant.Block, map[string]any, string, string) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("spawns a host shell fixture and uses stat(1)")
	}
	cname := "yolo-grant-file-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	t.Cleanup(func() { _ = os.RemoveAll(hostServiceSocketsDir(cname, false)) })
	cfg := jsonx.NewOrderedMap()
	_ = g.o.startLoopholesMatching(loopholes.NewHostSet(nil), cname, "podman", cfg, g.o.loopholeAllow("podman", cfg))
	mode, path, body := readSnapshotMarker(t, marker)
	if mode != "600" {
		t.Errorf("grant file mode %s, want 600", mode)
	}
	copyPath := filepath.Join(t.TempDir(), "grant.json")
	if err := os.WriteFile(copyPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	settings, block, err := loopholegrant.Read(copyPath)
	if err != nil {
		t.Fatalf("the file the daemon was handed is no grant file: %v\n%s", err, body)
	}
	if block == nil {
		t.Fatalf("the file the daemon was handed carries no grants: %s", body)
	}
	return block, settings, path, cname
}

func TestASelectedLoopholePackRunsItsServiceAndGrantsNothing(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "daemon.marker")
	g := newGrantLaunch(t, "journal", journalShaped(marker), "", "")
	g.gate(t)
	block, _, _, cname := g.spawnAndReadGrantFile(t, marker)
	if block.Pack != "journal" || block.Workspace != g.ws || block.Container != cname ||
		block.PID != os.Getpid() || block.LaunchID == "" {
		t.Errorf("grant block identity %+v, want this launch's", block)
	}
	if block.Access != "read the host's journal" {
		t.Errorf("grant block access %q, want the manifest's", block.Access)
	}
	want := map[string]loopholegrant.Standing{"read": loopholegrant.Requestable, "read_all": loopholegrant.NotAllowed}
	if len(block.Kinds) != len(want) {
		t.Fatalf("grant kinds %+v, want %v", block.Kinds, want)
	}
	for kind, standing := range want {
		if got := block.Kinds[kind]; got.Standing != standing {
			t.Errorf("kind %s standing %q, want %q (nothing granted with no allow and no request)", kind, got.Standing, standing)
		}
	}
	if block.Kinds["read"].Text != "read your user journal" {
		t.Errorf("kind read text %q, want the manifest's line", block.Kinds["read"].Text)
	}
}

func TestAGrantFileIsWrittenForAGrantDeclaringLoopholeWithNoSettings(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "daemon.marker")
	g := newGrantLaunch(t, "journal", journalShaped(marker), "", "[requests.journal]\nread = true\n")
	g.gate(t)
	block, settings, path, _ := g.spawnAndReadGrantFile(t, marker)
	if len(settings) != 0 {
		t.Errorf("a loophole declaring no settings was handed settings %v", settings)
	}
	if got := block.Kinds["read"].Standing; got != loopholegrant.Granted {
		t.Errorf("the requested, approved kind stands %q, want granted", got)
	}
	if path == loopholes.SettingsFileFor("journal") || filepath.Dir(path) != loopholes.StateDirFor("journal") {
		t.Errorf("grant file %q is the stable settings path or outside the loophole's state dir", path)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the grant file outlived the daemon it was written for: %v", err)
	}
}

// audioShapedManifest is an unmediated grant-declaring manifest: two socket binds, a shipped file
// and a device, one kind `use`, no daemon.
const audioShapedManifest = `name = "audio"
description = "an audio-shaped fixture"
version = 1
transport = "none"
lifecycle = "external"
access = "use the host's microphone and speakers"
host_devices = ["/dev/null"]

[[host_bind_mounts]]
host = "${XDG_RUNTIME_DIR}/pulse/native"
container = "/run/pulse/native"
readonly = true

[[host_bind_mounts]]
host = "{loophole_dir}/asound.conf"
container = "/etc/alsa/conf.d/50-yolo-audio-alsa.conf"
readonly = true

[[grants]]
kind = "use"
text = "use your microphone and speakers"
`

// audioLaunch is newGrantLaunch over audioShapedManifest, outside any jail, with its socket present.
func audioLaunch(t *testing.T, userTOML, wsTOML string) grantLaunch {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	os.Unsetenv("YOLO_VERSION")
	rt := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", rt)
	if err := os.MkdirAll(filepath.Join(rt, "pulse"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rt, "pulse", "native"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return newGrantLaunch(t, "audio", audioShapedManifest, userTOML, wsTOML)
}

func TestTheLaunchDisclosesAnUngrantedUnmediatedLoophole(t *testing.T) {
	g := audioLaunch(t, "", "")
	g.gate(t)
	g.out.Reset()
	g.o.notePackHostAccess(nil, nil, nil)
	want := "  audio: selected, grants nothing here (not allowed on request by your config).\n" +
		"    To let this project ask:  yolo allow audio on_request use   then  yolo request audio use\n"
	if !strings.Contains(g.out.String(), want) {
		t.Fatalf("the pack disclosure does not state the standing:\n%s\nwant:\n%s", g.out.String(), want)
	}
	// And the argv carries none of its crossing.
	args := strings.Join(g.o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	if strings.Contains(args, "/run/pulse/native") || strings.Contains(args, "--device") {
		t.Errorf("an ungranted unmediated loophole crossed: %s", args)
	}
}

func TestTheLaunchBindsAGrantedUnmediatedLoopholeAndSaysSo(t *testing.T) {
	g := audioLaunch(t, "[allow.audio]\non_request = [\"use\"]\n", "[requests.audio]\nuse = true\n")
	g.gate(t)
	args := strings.Join(g.o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	if !strings.Contains(args, "/run/pulse/native") || !strings.Contains(args, "--device /dev/null") {
		t.Errorf("a granted unmediated loophole did not cross: %s", args)
	}
	g.out.Reset()
	g.o.notePackHostAccess(nil, nil, nil)
	if !strings.Contains(g.out.String(), "audio: grants for this workspace: use") {
		t.Errorf("the disclosure does not name the grant in force:\n%s", g.out.String())
	}
	if strings.Contains(g.out.String(), "grants nothing here") {
		t.Errorf("a granted loophole was disclosed as granting nothing:\n%s", g.out.String())
	}
}

// A project's `requests.<pack> = "off"` withholds the user's `always` grant here
// (docs/design/legacy-config-compat.md LC-D18): the loophole crosses nothing, and the disclosure
// and the briefing say the project turns it off and name the project's own line, not a
// `yolo allow` the user already ran.
func TestTheLaunchDisclosesAGrantTheProjectWithholds(t *testing.T) {
	g := audioLaunch(t, "[allow.audio]\nalways = [\"use\"]\n", "[requests]\naudio = \"off\"\n")
	g.gate(t)
	args := strings.Join(g.o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "podman", nil), " ")
	if strings.Contains(args, "/run/pulse/native") {
		t.Errorf("a withheld loophole crossed: %s", args)
	}
	g.out.Reset()
	g.o.notePackHostAccess(nil, nil, nil)
	want := "  audio: selected, grants nothing here (this project turns it off).\n" +
		"    To take it here:  remove this project's `requests.audio = \"off\"`"
	if !strings.Contains(g.out.String(), want) {
		t.Fatalf("the pack disclosure does not say the project withholds it:\n%s\nwant:\n%s", g.out.String(), want)
	}
	loops := briefingLoopholes(nil, g.o.loopholeGrantStandings("podman", nil))
	if len(loops) != 1 || !strings.Contains(loops[0].Standing, "which turns it off") ||
		strings.Contains(loops[0].Standing, "yolo allow") {
		t.Errorf("briefing standing %+v does not say the project turns it off", loops)
	}
}

func TestTheBriefingStatesEachLoopholesStanding(t *testing.T) {
	g := audioLaunch(t, "", "")
	g.gate(t)
	loops := briefingLoopholes(nil, g.o.loopholeGrantStandings("podman", nil))
	if len(loops) != 1 || loops[0].Name != "audio" {
		t.Fatalf("briefing loopholes %+v, want the selected audio fixture", loops)
	}
	if !strings.Contains(loops[0].Standing, "grants nothing in this project") ||
		!strings.Contains(loops[0].Standing, "yolo request audio use") {
		t.Errorf("briefing standing %q does not state the standing and the step", loops[0].Standing)
	}
	text := jailcontent.BriefingContent(jailcontent.BriefingInput{Loopholes: loops})
	if !strings.Contains(text, loops[0].Standing) {
		t.Errorf("the rendered briefing drops the standing:\n%s", text)
	}
}

// PD-8: a selected grant-declaring loophole this setup runs not at all tells its client so through
// the jail's environment, with the inert note's reason.
func TestAnInertGrantDeclaringLoopholeIsNamedInTheJailEnv(t *testing.T) {
	g := audioLaunch(t, "", "")
	args := g.o.loopholesRuntimeArgs(jsonx.NewOrderedMap(), "container", nil)
	name := paths.LoopholeInertEnv("audio")
	found := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-e" && strings.HasPrefix(args[i+1], name+"=") {
			found = strings.TrimPrefix(args[i+1], name+"=")
		}
	}
	if found == "" || !strings.Contains(found, "Apple Container") {
		t.Errorf("the inert loophole's variable %s = %q, want the backend's reason; argv %v", name, found, args)
	}
	if env := g.o.loopholeInertEnv("podman", nil); len(env) != 0 {
		t.Errorf("a loophole that runs here was named inert: %v", env)
	}
}

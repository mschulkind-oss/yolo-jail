package cli

// hostsessionhome_test.go pins `yolo host`'s per-launch SESSION HOME (docs/design/per-session-
// profiles.md §4.7, ruled by OQ-SP4; "session home" is a term coined there: a per-session copy of
// the one directory a pack's program reads its config from, its rendered files the session's own
// and everything else linked to the shared copy). Every cell runs hostMain to the exec, so the
// declaration, the render and the export are asked through the front door: deleting the call in
// hostLaunch fails every cell here.
//
// The variable's name is read from the shipped pi pack's own declaration, never spelled here.

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/yolo-jail/internal/packload"
	"github.com/mschulkind-oss/yolo-jail/internal/paths"
	"github.com/mschulkind-oss/yolo-jail/internal/prune"
	"github.com/mschulkind-oss/yolo-jail/internal/render"
	"golang.org/x/sys/unix"
)

// copyHostSessionPack makes a private selected-pack fixture so a test can change the manifest
// after an ended session was rendered without changing the shipped source tree.
func copyHostSessionPack(t *testing.T) string {
	t.Helper()
	source, err := filepath.Abs(shippedPiPackDir)
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "pi")
	err = filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		to := filepath.Join(destination, rel)
		if entry.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, to)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(to, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy shipped pi pack: %v", err)
	}
	return destination
}

// piSessionHomeEnv is the variable the shipped pi pack's session-home declaration names.
func piSessionHomeEnv(t *testing.T) string {
	t.Helper()
	m := decodePackManifestFile(t, packManifestFile(t, shippedPiPackDir))
	homes := m.SessionHomes()
	if len(homes) != 1 || homes[0].Env == "" {
		t.Fatalf("the shipped pi pack declares %d session homes, want one with a variable", len(homes))
	}
	return homes[0].Env
}

// ownedSessionHomeConfig puts cfg under host_management = own with apply-on-launch off: the two
// keys go ahead of a TOML fixture's first table, or into a legacy JSONC one's object.
func ownedSessionHomeConfig(cfg string) string {
	if !strings.HasPrefix(strings.TrimSpace(cfg), "{") {
		return "host_management = \"own\"\nhost_apply_on_launch = false\n" + cfg
	}
	return strings.Replace(cfg, `{"packs"`, `{"host_management":"own","host_apply_on_launch":false,"packs"`, 1)
}

// sessionCodexCfg is piSubagentsCodexCfg (hostlaunchcomputed_test.go) in TOML: openrouter as pi's
// provider, openai-auth's codex selectable by -p. OPENROUTER_API_KEY is the openrouter provider's
// key, so it lives in the private set (docs/reference/providers.md#the-two-env-sets-public-and-private). head is any top-level keys a test
// adds; they must come before the first table.
func sessionCodexCfg(head string) string {
	return head + `packs = ["claude", "openrouter", "codex-auth", "pi"]

[active_provider]
pi = "openrouter"

[env.secrets]
set = { OPENROUTER_API_KEY = "tok-router" }
`
}

// sessionAcmeCfg is piSubagentsAcmeCfg (hostlaunchcomputed_test.go) in TOML: one user provider
// with two variants over it whose role pins differ; acme-a is configured. ACME_KEY is acme's key,
// so it lives in the private set.
const sessionAcmeCfg = `packs = ["claude", "pi"]

[active_provider]
pi = "acme/acme-a"

[providers.acme]
api_key_env_name = "ACME_KEY"

[providers.acme.endpoints.openai]
base_url = "https://acme.example/v1"

[providers.acme.models]
default = "acme-large"
fast = "acme-mini"

[providers.acme.variants.acme-a]
model = "default"
subagents = { roles = { worker = "fast" } }

[providers.acme.variants.acme-b]
model = "default"
subagents = { default = "fast", roles = { worker = "default", scout = "fast" } }

[env.secrets]
set = { ACME_KEY = "tok-acme" }
`

// treeSnapshot is every regular file and link under root, with its bytes or target.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if d.Type()&fs.ModeSymlink != 0 {
			target, _ := os.Readlink(p)
			out[rel] = "link:" + target
			return nil
		}
		data, _ := os.ReadFile(p)
		out[rel] = string(data)
		return nil
	})
	return out
}

// hostCaptureStores are the real home's host capture, provenance and archive stores: a session
// render keeps its bookkeeping in its own throwaway home, so none of them may gain a file.
func hostCaptureStores(home string) []string {
	gs := paths.GlobalStorageUnder(home)
	return []string{filepath.Join(gs, "host-capture"), filepath.Join(gs, "host-provenance"),
		filepath.Join(gs, "archive")}
}

// THE RULING, END TO END: `yolo host -p pi=codex -- pi` over a configured openrouter profile hands
// pi a session home in yolo's state dir, its settings.json the -p's, auth.json a link to the real
// one, and leaves every byte of the real ~/.pi/agent and the real capture stores as it was.
func TestHostPiLaunchGetsItsOwnSessionHome(t *testing.T) {
	env := piSessionHomeEnv(t)
	// The user opted into host ownership, which is required for every command launch, not just
	// for an explicit profile selection.
	home := hostGateHome(t, ownedSessionHomeConfig(sessionCodexCfg("")), nil)
	unchanged := piSettingsFixture(t, home, `{"defaultProvider":"openrouter","theme":"dark"}`)
	shared := filepath.Join(home, ".pi", "agent")
	before := treeSnapshot(t, shared)
	// The lease that keeps the home from being reaped while pi runs, asked AT the exec: after the
	// stub returns, nothing references the lease's *os.File, and its finalizer may close it (and
	// drop the lock) at the next GC, which a loaded machine reaches first. A real exec replaces
	// the process, so the inherited descriptor is the program's from that instant.
	leaseAtExec, inherited := packload.LeaseAbsent, false
	run := hostSelectionRunAtExec(t, []string{"-p", "pi=codex"}, "pi", func(e map[string]string) {
		leaseDir := filepath.Dir(filepath.Dir(filepath.Dir(e[env])))
		var unlock func()
		leaseAtExec, unlock, _ = packload.ProbeLease(leaseDir)
		unlock()
		inherited = leaseFDInheritedAcrossExec(t, filepath.Join(leaseDir, packload.EmbeddedLeaseName))
	})
	unchanged()
	after := treeSnapshot(t, shared)
	for rel, data := range before {
		if after[rel] != data {
			t.Errorf("the real %s changed:\n%s", rel, after[rel])
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("the launch wrote %s into the real %s", rel, shared)
		}
	}
	for _, store := range hostCaptureStores(home) {
		if got := treeSnapshot(t, store); len(got) > 0 {
			t.Errorf("the session render wrote into the real store %s: %v", store, got)
		}
	}
	dir := run.env[env]
	root := filepath.Join(paths.GlobalStorageUnder(home), prune.HostSessionHomesLeaf)
	if !strings.HasPrefix(dir, root+string(filepath.Separator)) {
		t.Fatalf("pi got %s=%q, want a directory under %s\n%s", env, dir, root, run.errs)
	}
	var settings struct {
		DefaultProvider string `json:"defaultProvider"`
		Theme           string `json:"theme"`
	}
	data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatalf("the session home has no settings.json: %v\n%s", err, run.errs)
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("the session's settings.json is not JSON: %v\n%s", err, data)
	}
	if settings.DefaultProvider != "openai-codex" {
		t.Errorf("the session's defaultProvider is %q, want the -p's openai-codex:\n%s", settings.DefaultProvider, data)
	}
	if settings.Theme != "dark" {
		t.Errorf("the session's settings.json lost the user's own key (theme %q):\n%s", settings.Theme, data)
	}
	if fi, err := os.Lstat(filepath.Join(dir, "settings.json")); err != nil || fi.Mode()&fs.ModeSymlink != 0 {
		t.Errorf("the session's settings.json must be its own file, not a link")
	}
	if got, err := os.Readlink(filepath.Join(dir, "auth.json")); err != nil || got != filepath.Join(shared, "auth.json") {
		t.Errorf("auth.json must link to the real one: %q, %v", got, err)
	}
	if got, err := os.Readlink(filepath.Join(dir, "sessions")); err != nil || got != filepath.Join(shared, "sessions") {
		t.Errorf("sessions must link to the real directory: %q, %v", got, err)
	}
	if leaseAtExec != packload.LeaseHeld {
		t.Errorf("the launch's session home lease is %s at the exec, want held", leaseAtExec)
	}
	if !inherited {
		t.Errorf("no descriptor on the session home's lease is open without close-on-exec at the " +
			"exec, so pi would not hold the lease across it")
	}
}

// leaseFDInheritedAcrossExec reports whether this process has a descriptor open on lease with
// close-on-exec cleared: one an exec hands to the program it starts. /dev/fd lists them on Linux
// and darwin alike.
func leaseFDInheritedAcrossExec(t *testing.T, lease string) bool {
	t.Helper()
	fi, err := os.Stat(lease)
	if err != nil {
		t.Errorf("the session home has no lease at the exec: %v", err)
		return false
	}
	// Names only: os.ReadDir lstats each entry on darwin, and the entry for the descriptor the
	// listing itself held is gone by then ("lstat /dev/fd/4: bad file descriptor", check-macos,
	// 2026-11-04). A descriptor that vanished is skipped by the Fstat below.
	dir, err := os.Open("/dev/fd")
	if err != nil {
		t.Fatalf("opening /dev/fd: %v", err)
	}
	names, err := dir.Readdirnames(-1)
	dir.Close()
	if err != nil {
		t.Fatalf("listing /dev/fd: %v", err)
	}
	for _, name := range names {
		fd, err := strconv.Atoi(name)
		if err != nil {
			continue
		}
		var st syscall.Stat_t
		if syscall.Fstat(fd, &st) != nil {
			continue
		}
		if want := fi.Sys().(*syscall.Stat_t); st.Dev != want.Dev || st.Ino != want.Ino {
			continue
		}
		if flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0); err == nil && flags&unix.FD_CLOEXEC == 0 {
			return true
		}
	}
	return false
}

// UNDER `host_management: own` the session's render folds in the real capture store's captured
// edits, as the next `yolo host apply` would, and reads that store without writing a byte of it.
func TestHostPiSessionHomeFoldsTheCapturedEditsUnderOwn(t *testing.T) {
	env := piSessionHomeEnv(t)
	// The apply-on-launch gate is off: it renders the real home by design, and this cell asks
	// what the session render writes.
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	const settings = `{"defaultProvider":"openrouter"}`
	unchanged := piSettingsFixture(t, home, settings)
	real := render.Host(home, nil, render.OwnershipOwn)
	writeFile(t, real.OverlayPath("pi", "settings"), `{"theme":"captured"}`)
	writeFile(t, real.LastRenderPath("pi", "settings"), settings+"\n")
	stores := map[string]map[string]string{}
	for _, s := range hostCaptureStores(home) {
		stores[s] = treeSnapshot(t, s)
	}
	run := hostSelectionRun(t, home, []string{"-p", "pi=codex"}, "pi")
	unchanged()
	for s, before := range stores {
		after := treeSnapshot(t, s)
		if len(after) != len(before) {
			t.Errorf("the real store %s changed: %v -> %v", s, before, after)
		}
		for rel, data := range before {
			if after[rel] != data {
				t.Errorf("the real store's %s changed:\n%s", rel, after[rel])
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(run.env[env], "settings.json"))
	if err != nil {
		t.Fatalf("no session settings.json: %v\n%s", err, run.errs)
	}
	var got struct {
		DefaultProvider, Theme string
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.DefaultProvider != "openai-codex" || got.Theme != "captured" {
		t.Errorf("the session's settings.json must hold the -p's provider and the captured theme:\n%s", data)
	}
}

func TestHostPiSessionHomeCarriesTheProfilesChildPolicy(t *testing.T) {
	env := piSessionHomeEnv(t)
	home := hostGateHome(t, ownedSessionHomeConfig(sessionAcmeCfg), nil)
	unchanged := piSettingsFixture(t, home, `{"defaultProvider":"acme"}`)
	runB := hostSelectionRun(t, home, []string{"-p", "pi=acme/acme-b"}, "pi")
	// Typed, since the launch under host_management=own creates a fresh, profile-specific home.
	runA := hostSelectionRun(t, home, []string{"-p", "pi=acme/acme-a"}, "pi")
	unchanged()
	type policy struct {
		Subagents map[string]any `json:"subagents"`
	}
	read := func(dir string) policy {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "settings.json"))
		if err != nil {
			t.Fatalf("no settings.json in %s: %v", dir, err)
		}
		var p policy
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		if p.Subagents == nil {
			t.Fatalf("the session's settings.json in %s has no subagents block:\n%s", dir, data)
		}
		return p
	}
	if runA.env[env] == "" || runB.env[env] == "" || runA.env[env] == runB.env[env] {
		t.Fatalf("each launch needs its own session home: %q and %q\n%s", runA.env[env], runB.env[env], runB.errs)
	}
	a, b := read(runA.env[env]), read(runB.env[env])
	aj, _ := json.Marshal(a.Subagents)
	bj, _ := json.Marshal(b.Subagents)
	if string(aj) == string(bj) {
		t.Errorf("-p acme-b's child policy is the configured acme-a's: %s", bj)
	}
	if !strings.Contains(string(bj), "acme-mini") {
		t.Errorf("-p acme-b's child policy must default to its fast model: %s", bj)
	}
}

func TestHostPiSessionHomeYieldsToThePresetVariable(t *testing.T) {
	env := piSessionHomeEnv(t)
	own := t.TempDir()
	home := hostGateHome(t, ownedSessionHomeConfig(sessionCodexCfg("")), map[string]string{env: own})
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	run := hostSelectionRun(t, home, []string{"-p", "pi=codex"}, "pi")
	if run.env[env] != own {
		t.Errorf("pi got %s=%q, want the user's own %q", env, run.env[env], own)
	}
	if n := strings.Count(run.errs, env+" is already set"); n != 1 {
		t.Errorf("want one line naming the preset %s, got %d:\n%s", env, n, run.errs)
	}
	root := filepath.Join(paths.GlobalStorageUnder(home), prune.HostSessionHomesLeaf)
	if entries, _ := os.ReadDir(root); len(entries) > 0 {
		t.Errorf("a preset variable must make no session home, found %d in %s", len(entries), root)
	}
}

// SP-R1: NONE OR UNSET REFUSES EVERY HOST COMMAND LAUNCH before trace creation, profile
// composition, readiness, credentials/services, session-home creation, or the exec. The
// configuration-management verbs use a different hostMain route and remain outside this gate.
func TestHostCommandLaunchRequiresOwnBeforeAssistance(t *testing.T) {
	for _, mgmt := range []string{"none", ""} {
		name := mgmt
		if name == "" {
			name = "unset"
		}
		for _, flags := range [][]string{nil, {"-p", "pi=codex"}, {"--with-credentials", "all"}} {
			t.Run(name+"/"+strings.Join(flags, "-"), func(t *testing.T) {
				cfg := sessionCodexCfg("")
				if mgmt != "" {
					cfg = sessionCodexCfg("host_management = \"none\"\n")
				}
				home := hostGateHomeRaw(t, cfg, nil)
				bin := filepath.Join(t.TempDir(), "bin")
				writeFile(t, filepath.Join(bin, "pi"), "#!/bin/sh\nexit 0\n")
				if err := os.Chmod(filepath.Join(bin, "pi"), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				execs := 0
				orig := hostSyscallExec
				hostSyscallExec = func(string, []string, []string) error { execs++; return nil }
				t.Cleanup(func() { hostSyscallExec = orig })
				var out, errw bytes.Buffer
				argv := append(append([]string{}, flags...), "--", "pi")
				rc := hostMain(argv, &out, &errw, false, nil)
				if rc == 0 || execs != 0 || !strings.Contains(errw.String(), "host_management") ||
					!strings.Contains(errw.String(), "own") {
					t.Fatalf("host launch under %s must refuse before exec (rc=%d execs=%d):\n%s", name, rc, execs, errw.String())
				}
				if _, err := os.Stat(paths.GlobalStorageUnder(home)); !os.IsNotExist(err) {
					t.Errorf("refusal created yolo host state before launch assistance was allowed: %v", err)
				}
			})
		}
	}
}

// Under own, the configured profile applies to a bare launch and a typed profile overrides it.
func TestHostPiSessionHomeUsesConfiguredOrExplicitProfile(t *testing.T) {
	env := piSessionHomeEnv(t)
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	for _, tc := range []struct {
		flags []string
		want  string
	}{{nil, "openrouter"}, {[]string{"-p", "pi=codex"}, "openai-codex"}} {
		run := hostSelectionRun(t, home, tc.flags, "pi")
		var settings map[string]any
		data, err := os.ReadFile(filepath.Join(run.env[env], "settings.json"))
		if err != nil {
			t.Fatalf("read session settings: %v", err)
		}
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatal(err)
		}
		if got := settings["defaultProvider"]; got != tc.want {
			t.Errorf("profile settings defaultProvider=%v, want %q", got, tc.want)
		}
	}
}

// An owned resident session captures only user edits through the host overlay; computed profile
// values regenerate on the next selection, and the native settings file remains untouched.
func TestHostPiResidentSessionCapturesUserEditNormally(t *testing.T) {
	env := piSessionHomeEnv(t)
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	repointCodexUpstream(t, upstream.URL)
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	const original = `{"defaultProvider":"openrouter","theme":"system"}`
	unchanged := piSettingsFixture(t, home, original)
	durable := render.Host(home, nil, render.OwnershipOwn)
	writeFile(t, durable.LastRenderPath("pi", "settings"), original+"\n")
	writeFile(t, durable.OverlayPath("pi", "settings"), `{"existingCapturedSetting":"from-earlier-session"}`)
	bin := t.TempDir()
	agent := filepath.Join(bin, "claude")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf '%s\\n' '{\"customUserSetting\":\"saved-by-user\",\"existingCapturedSetting\":\"from-earlier-session\"}' > \"$PI_CODING_AGENT_DIR/settings.json\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { t.Error("the resident launch exec'd"); return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })
	var out, errw bytes.Buffer
	rc := hostMain([]string{"-p", "codex", "--", "claude"}, &out, &errw, false, nil)
	if rc != 0 {
		t.Fatalf("resident host launch rc=%d:\\n%s", rc, errw.String())
	}
	unchanged()
	overlayPath := render.Host(home, nil, render.OwnershipOwn).OverlayPath("pi", "settings")
	overlay, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatalf("resident session did not persist the normal host capture overlay: %v\\n%s", err, errw.String())
	}
	var captured map[string]any
	if err := json.Unmarshal(overlay, &captured); err != nil {
		t.Fatalf("capture overlay is invalid: %v (%s)", err, overlay)
	}
	if captured["customUserSetting"] != "saved-by-user" || captured["existingCapturedSetting"] != "from-earlier-session" || captured["defaultProvider"] != nil || captured["defaultModel"] != nil {
		t.Errorf("capture should accumulate user settings, not profile values: %s", overlay)
	}
	next := hostSelectionRun(t, home, []string{"-p", "pi=openrouter"}, "pi")
	var settings map[string]any
	data, err := os.ReadFile(filepath.Join(next.env[env], "settings.json"))
	if err != nil || json.Unmarshal(data, &settings) != nil {
		t.Fatalf("read next session settings: %v (%s)", err, data)
	}
	if settings["defaultProvider"] != "openrouter" || settings["customUserSetting"] != "saved-by-user" || settings["existingCapturedSetting"] != "from-earlier-session" {
		t.Errorf("next profile must regenerate its provider while keeping new and preexisting captured user settings: %s", data)
	}
}

// With no prior managed render or capture sidecar, the first own launch still adopts the real
// settings file as its session baseline, captures a later user edit, and carries it into the next
// profile without letting it replace computed profile values.
func TestHostPiFirstOwnedSessionCapturesUserEditWithoutDurableBaseline(t *testing.T) {
	testHostPiFirstOwnedSessionCapture(t, true)
}

// An explicitly disabled launch-time apply must still keep a first session's user edit by
// retaining that session's baseline in the normal host capture store, not by applying to the home.
func TestHostPiFirstOwnedSessionWithApplyDisabledCapturesUserEditWithoutDurableBaseline(t *testing.T) {
	testHostPiFirstOwnedSessionCapture(t, false)
}

func testHostPiFirstOwnedSessionCapture(t *testing.T, applyOnLaunch bool) {
	t.Helper()
	env := piSessionHomeEnv(t)
	upstream, _ := fakeUpstream(t)
	fakeHostBroker(t)
	apply := "host_management = \"own\"\n"
	if !applyOnLaunch {
		apply += "host_apply_on_launch = false\n"
	}
	repointCodexUpstream(t, upstream.URL)
	cfg := sessionCodexCfg(apply)
	home := hostGateHome(t, cfg, nil)
	const original = `{"defaultProvider":"openrouter","theme":"system"}`
	settingsPath := filepath.Join(home, ".pi", "agent", "settings.json")
	piSettingsFixture(t, home, original)
	durable := render.Host(home, nil, render.OwnershipOwn)
	if _, err := os.Stat(durable.LastRenderPath("pi", "settings")); !os.IsNotExist(err) {
		t.Fatalf("fresh-user fixture unexpectedly had a durable baseline: %v", err)
	}
	bin := t.TempDir()
	agent := filepath.Join(bin, "claude")
	if err := os.WriteFile(agent, []byte("#!/bin/sh\nprintf '%s\\n' '{\"customUserSetting\":\"saved-by-user\"}' > \"$PI_CODING_AGENT_DIR/settings.json\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	origExec := hostSyscallExec
	hostSyscallExec = func(string, []string, []string) error { t.Error("the resident launch exec'd"); return nil }
	t.Cleanup(func() { hostSyscallExec = origExec })
	var out, errw bytes.Buffer
	if rc := hostMain([]string{"-p", "codex", "--", "claude"}, &out, &errw, false, nil); rc != 0 {
		t.Fatalf("first resident host launch rc=%d:\n%s", rc, errw.String())
	}
	if data, err := os.ReadFile(settingsPath); err != nil || strings.Contains(string(data), "customUserSetting") {
		t.Fatalf("the session edit was written back into pi's native settings: %s (%v)", data, err)
	}
	if _, err := os.Stat(durable.LastRenderPath("pi", "settings")); err != nil {
		t.Fatalf("the normal first-owned-launch apply did not establish its durable baseline: %v", err)
	}
	data, err := os.ReadFile(durable.OverlayPath("pi", "settings"))
	if err != nil {
		t.Fatalf("first owned session did not persist its edit without a prior durable baseline: %v\n%s", err, errw.String())
	}
	var captured map[string]any
	if err := json.Unmarshal(data, &captured); err != nil {
		t.Fatalf("first capture overlay is invalid: %v (%s)", err, data)
	}
	if captured["customUserSetting"] != "saved-by-user" || captured["defaultProvider"] != nil {
		t.Errorf("first capture should keep the user edit without capturing profile output: %s", data)
	}
	next := hostSelectionRun(t, home, []string{"-p", "pi=openrouter"}, "pi")
	data, err = os.ReadFile(filepath.Join(next.env[env], "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings["defaultProvider"] != "openrouter" || settings["customUserSetting"] != "saved-by-user" {
		t.Errorf("next profile must regenerate its provider and retain the first-session user edit: %s", data)
	}
}

// Independent ended sessions accumulate baseline-relative edits over the latest durable sidecars.
// The unchanged oldest home is captured last to prove it cannot roll back newer captures.
func TestHostPiSessionCaptureAccumulatesCurrentOverlayAndListRecords(t *testing.T) {
	piSessionHomeEnv(t)
	home := hostGateHome(t, ownedSessionHomeConfig(sessionCodexCfg("")), nil)
	original := `{"defaultProvider":"openrouter","theme":"system","packages":["base","removed"]}`
	piSettingsFixture(t, home, original)
	durable := render.Host(home, nil, render.OwnershipOwn)
	writeFile(t, durable.LastRenderPath("pi", "settings"), original+"\n")
	writeFile(t, durable.OverlayPath("pi", "settings"), `{"preexisting":"durable","oldTombstone":null}`)
	writeFile(t, durable.ListCapturePath("pi", "settings"), `{"/packages":{"add":["previous"],"remove":["removed"]}}`)

	launch := composeHostLaunch("pi", "codex", nil, nil)
	if launch.err != nil {
		t.Fatal(launch.err)
	}
	makeSession := func() (*hostSessionHomes, string) {
		t.Helper()
		homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
		if err != nil || homes == nil || len(homes.vars) != 1 {
			t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
		}
		_, path, ok := strings.Cut(homes.vars[0], "=")
		if !ok {
			t.Fatalf("malformed session-home variable %q", homes.vars[0])
		}
		return homes, filepath.Join(path, "settings.json")
	}
	old, _ := makeSession()
	a, aSettings := makeSession()
	b, bSettings := makeSession()
	edit := func(path string, change func(map[string]any)) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var settings map[string]any
		if err := json.Unmarshal(data, &settings); err != nil {
			t.Fatalf("decode session settings: %v (%s)", err, data)
		}
		change(settings)
		data, err = json.Marshal(settings)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	edit(aSettings, func(settings map[string]any) {
		settings["sessionA"] = "A"
		delete(settings, "theme") // a tombstone relative to this home's baseline
		settings["packages"] = append(settings["packages"].([]any), "session-package-A")
	})
	edit(bSettings, func(settings map[string]any) {
		settings["sessionB"] = "B"
		settings["packages"] = append(settings["packages"].([]any), "session-package-B")
	})
	for _, homes := range []*hostSessionHomes{a, b, old} {
		t.Cleanup(func() { homes.remove() })
	}
	for _, homes := range []*hostSessionHomes{a, b, old} {
		// End the retained session before capture, as a normally-exited exec does.
		for _, lease := range homes.leases {
			_ = lease.Close()
		}
		homes.leases = nil
	}
	for _, homes := range []*hostSessionHomes{a, b, old} {
		if err := finishHostSessionHomes(homes, io.Discard); err != nil {
			t.Fatalf("capture session: %v", err)
		}
	}

	overlay, err := os.ReadFile(durable.OverlayPath("pi", "settings"))
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	if err := json.Unmarshal(overlay, &captured); err != nil {
		t.Fatalf("decode durable overlay: %v (%s)", err, overlay)
	}
	if captured["preexisting"] != "durable" || captured["oldTombstone"] != nil || captured["sessionA"] != "A" || captured["sessionB"] != "B" {
		t.Errorf("capture lost a durable key/tombstone or an independent session delta: %s", overlay)
	}
	if v, ok := captured["theme"]; !ok || v != nil {
		t.Errorf("session deletion was not retained as a tombstone: %s", overlay)
	}
	listCapture, err := os.ReadFile(durable.ListCapturePath("pi", "settings"))
	if err != nil {
		t.Fatal(err)
	}
	var lists map[string]struct {
		Add    []any `json:"add"`
		Remove []any `json:"remove"`
	}
	if err := json.Unmarshal(listCapture, &lists); err != nil {
		t.Fatalf("decode durable list capture: %v (%s)", err, listCapture)
	}
	contains := func(values []any, want string) bool {
		for _, value := range values {
			if value == want {
				return true
			}
		}
		return false
	}
	record := lists["/packages"]
	for _, want := range []string{"previous", "session-package-A", "session-package-B"} {
		if !contains(record.Add, want) {
			t.Errorf("list capture lost add %q: %s", want, listCapture)
		}
	}
	if !contains(record.Remove, "removed") {
		t.Errorf("list capture lost prior removal: %s", listCapture)
	}
}

// A retained session whose selected pack moved both its declared session-home root and its
// surface paths cannot be captured: the current selection accounts for neither. It must be KEPT
// WHOLE, not deleted and not a launch-blocking refusal (design §5, SHR-D8): the bytes the pack
// relocated are still the user's, and the sweep is janitorial work. The fixture changes the actual
// selected pack manifest after the session ended; renaming files in place would not exercise that
// relocation case.
func TestPruneRunKeepsRelocatedHostSessionHomeWholeAndRetainsOriginalBytes(t *testing.T) {
	piSessionHomeEnv(t)
	packDir := copyHostSessionPack(t)
	// The copied pack replaces the shipped pi, read through a pack source.
	cfg := strings.Replace(ownedSessionHomeConfig(sessionCodexCfg("")), `, "pi"]`, `, "packs/pi"]`, 1) + `
[pack_sources.packs]
path = "` + filepath.Dir(packDir) + `"
bare = true
`
	home := hostGateHome(t, cfg, nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter","theme":"system"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	if launch.err != nil {
		t.Fatal(launch.err)
	}
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.vars) != 1 {
		t.Fatalf("prepare owned session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	_, sessionHome, ok := strings.Cut(homes.vars[0], "=")
	if !ok {
		t.Fatalf("malformed session-home variable %q", homes.vars[0])
	}
	const edited = `{"defaultProvider":"openai-codex","customUserSetting":"must-survive"}`
	original := filepath.Join(sessionHome, "settings.json")
	if err := os.WriteFile(original, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	homes.preserveForRetry()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	manifestPath := packManifestFile(t, packDir)
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.ReplaceAll(string(manifest), ".pi/agent", ".pi/relocated-agent")
	if changed == string(manifest) {
		t.Fatal("fixture pack manifest had no pi surface paths to relocate")
	}
	if err := os.WriteFile(manifestPath, []byte(changed), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	opts := pruneRunOnTemp(t, pruneOptions([]string{"prune"}), paths.GlobalStorage(), &out)
	opts.Now = time.Now
	opts.Apply = true
	opts.NoEmbeddedPacks = true
	// The sweep reports a kept home; it does not fail, and it does not block anything.
	if rc := prune.Run(opts); rc != 0 {
		t.Fatalf("prune failed instead of reporting a kept home; source=%s\n%s", dir, out.String())
	}
	if existsHostPath(dir) {
		t.Fatalf("prune left the unaccountable home in the swept root:\n%s", out.String())
	}
	kept := filepath.Join(paths.GlobalStorage(), prune.HostSessionHomesKeptLeaf, filepath.Base(dir))
	data, err := os.ReadFile(filepath.Join(kept, "home", ".pi", "agent", "settings.json"))
	if err != nil || string(data) != edited {
		t.Fatalf("the kept home did not retain the edited session bytes: %q (%v)", data, err)
	}
	for _, want := range []string{"kept whole, not deleted", kept} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the report must name %q:\n%s", want, out.String())
		}
	}
}

// An ended exec's retained session-home render is captured through the same host sidecars before
// the reaper removes it. The explicit profile is part of the stored baseline, so capture excludes
// it while preserving only the session user's theme edit.
func TestHostPiEndedSessionHomeIsCapturedBeforeReap(t *testing.T) {
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter","theme":"system"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	sessionSettings := filepath.Join(homes.vars[0][strings.Index(homes.vars[0], "=")+1:], "settings.json")
	data, err := os.ReadFile(sessionSettings)
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	settings["customUserSetting"] = "saved-by-user"
	data, err = json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sessionSettings, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// The exec has ended: release its lease and make the home old enough for the normal reaper.
	homes.preserveForRetry()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(dir)
	r := prune.ReapHostSessionHomes(root, true, time.Now(), func(path string) (string, string, error) {
		return captureHostSessionHomePath(path, io.Discard)
	})
	if len(r.Removed) != 1 || r.Removed[0].Err != nil || existsHostPath(dir) {
		t.Fatalf("ended home was not captured and reaped: %+v exists=%v", r.Removed, existsHostPath(dir))
	}
	overlay, err := os.ReadFile(render.Host(home, nil, render.OwnershipOwn).OverlayPath("pi", "settings"))
	if err != nil {
		t.Fatalf("reaper did not persist the normal host capture overlay: %v", err)
	}
	var captured map[string]any
	if err := json.Unmarshal(overlay, &captured); err != nil {
		t.Fatal(err)
	}
	if captured["customUserSetting"] != "saved-by-user" || captured["defaultProvider"] != nil {
		t.Errorf("capture included generated profile values or lost the user edit: %s", overlay)
	}
}

// THE LOCKOUT, as a regression: an ended home holding a directory the program created in its own
// config dir (an extension's state, here `pi-web-access/last-seen-version.json`) used to refuse the
// next launch, forever, over a file no surface ever claimed. It is a stray (design §3): its
// surfaces are captured, the stray is repatriated into the real config directory, the home is
// removed, and the sweep succeeds — and the NEXT launch links that name instead of creating it
// again, which is what makes the recurrence impossible (SHR-D4).
func TestHostPiEndedSessionHomeRepatriatesAStrayExtensionDir(t *testing.T) {
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter","theme":"system"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	_, sessionHome, ok := strings.Cut(homes.vars[0], "=")
	if !ok {
		t.Fatalf("malformed session-home variable %q", homes.vars[0])
	}
	// the launch recorded what it rendered, so a later pack edit cannot make these bytes look lost
	manifest, err := os.ReadFile(filepath.Join(dir, hostSessionManifestName))
	if err != nil {
		t.Fatalf("the launch wrote no manifest: %v", err)
	}
	for _, want := range []string{`"rel": ".pi/agent"`, `"settings.json"`, `"shared"`} {
		if !strings.Contains(string(manifest), want) {
			t.Errorf("the manifest omits %q:\n%s", want, manifest)
		}
	}
	// the user's edit, which capture must still fold into the overlay
	settings := filepath.Join(sessionHome, "settings.json")
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	parsed["customUserSetting"] = "saved-by-user"
	data, err = json.Marshal(parsed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, data, 0o600); err != nil {
		t.Fatal(err)
	}
	// what pi-web-access writes: <agent dir>/<extension>/last-seen-version.json
	strayDir := filepath.Join(sessionHome, "pi-web-access")
	if err := os.MkdirAll(strayDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const stamp = `{"version":"0.45.0"}`
	if err := os.WriteFile(filepath.Join(strayDir, "last-seen-version.json"), []byte(stamp), 0o600); err != nil {
		t.Fatal(err)
	}
	homes.preserveForRetry()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	r := prune.ReapHostSessionHomes(filepath.Dir(dir), true, time.Now(), func(path string) (string, string, error) {
		return captureHostSessionHomePath(path, &errw)
	})
	if len(r.Removed) != 1 || r.Removed[0].Err != nil || len(r.Aside) != 0 {
		t.Fatalf("a repatriated stray must not refuse or keep the home: removed=%+v aside=%+v errw=%s",
			r.Removed, r.Aside, errw.String())
	}
	if existsHostPath(dir) {
		t.Fatal("the captured home survived the sweep")
	}
	real := filepath.Join(home, ".pi", "agent", "pi-web-access", "last-seen-version.json")
	if got, err := os.ReadFile(real); err != nil || string(got) != stamp {
		t.Fatalf("the stray was not repatriated into the real config directory: %q (%v)", got, err)
	}
	overlay, err := os.ReadFile(render.Host(home, nil, render.OwnershipOwn).OverlayPath("pi", "settings"))
	if err != nil {
		t.Fatalf("the stray stopped the user's edit being captured: %v", err)
	}
	var captured map[string]any
	if err := json.Unmarshal(overlay, &captured); err != nil {
		t.Fatal(err)
	}
	if captured["customUserSetting"] != "saved-by-user" {
		t.Errorf("capture lost the user edit: %s", overlay)
	}
	for _, want := range []string{"moved it into your config directory", "pi-web-access → " + filepath.Dir(real)} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the launch line must name %q:\n%s", want, errw.String())
		}
	}
	// The recurrence is gone: the next launch links the repatriated name instead of creating it.
	next, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || next == nil || len(next.vars) != 1 {
		t.Fatalf("second launch: homes=%+v err=%v", next, err)
	}
	defer next.remove()
	_, nextHome, _ := strings.Cut(next.vars[0], "=")
	link := filepath.Join(nextHome, "pi-web-access")
	info, err := os.Lstat(link)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the next launch did not link the repatriated directory: %v (%v)", info, err)
	}
}

// A name the real config directory already holds is two divergent copies: never merged, never
// overwritten. The home is kept whole and the user decides (SHR-D4). The fixture lays the real
// directory BEFORE the launch, so the session home links it, and then replaces that link with a
// real directory the way a program that renames over its target does.
func TestHostPiEndedSessionHomeKeepsAStrayWhoseNameAlreadyExists(t *testing.T) {
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	realDir := filepath.Join(home, ".pi", "agent", "pi-web-access")
	if err := os.MkdirAll(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	const mine = "mine"
	if err := os.WriteFile(filepath.Join(realDir, "last-seen-version.json"), []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	_, sessionHome, _ := strings.Cut(homes.vars[0], "=")
	linked := filepath.Join(sessionHome, "pi-web-access")
	if info, err := os.Lstat(linked); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the fixture expected a link to the real directory: %v (%v)", info, err)
	}
	// The program replaces the link by renaming over it.
	if err := os.Remove(linked); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(linked, "last-seen-version.json"), []byte(`{"version":"0.45.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	homes.preserveForRetry()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	r := prune.ReapHostSessionHomes(filepath.Dir(dir), true, time.Now(), func(path string) (string, string, error) {
		return captureHostSessionHomePath(path, &errw)
	})
	if len(r.Aside) != 1 || len(r.Removed) != 0 {
		t.Fatalf("a colliding stray must keep the home whole: removed=%+v aside=%+v errw=%s", r.Removed, r.Aside, errw.String())
	}
	if got, err := os.ReadFile(filepath.Join(realDir, "last-seen-version.json")); err != nil || string(got) != mine {
		t.Fatalf("repatriation overwrote the user's own directory: %q (%v)", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(r.Aside[0].Aside, "home", ".pi", "agent", "pi-web-access", "last-seen-version.json")); err != nil || !strings.Contains(string(got), "0.45.0") {
		t.Fatalf("the kept home lost the session's copy: %q (%v)", got, err)
	}
	if !strings.Contains(errw.String(), "already holds that name") {
		t.Errorf("the line must say why it was kept:\n%s", errw.String())
	}
}

// A stray INSIDE a directory yolo rendered into is not adopted: the two trees cannot be
// reconciled automatically, so the home is kept whole (SHR-Q4's safe default).
func TestRepatriationKeepsANestedStray(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, hostScratchHomeName, ".pi", "agent")
	if err := os.MkdirAll(filepath.Join(root, "extensions"), 0o700); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "extensions", "brand-new.js")
	if err := os.WriteFile(stray, []byte("// x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	reason := repatriateHostSessionStrays(dir, ".pi/agent", filepath.Join(dir, "real"), []string{"extensions/brand-new.js"}, &errw)
	if reason == "" {
		t.Fatal("a nested stray must keep the home whole")
	}
	if !strings.Contains(reason, "extensions/brand-new.js") {
		t.Errorf("the reason must name the entry: %s", reason)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("the nested stray was moved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "real", "extensions")); !os.IsNotExist(err) {
		t.Errorf("the nested stray reached the real directory: %v", err)
	}
}

// The resident path is the only path that could actually LOSE a byte: it used to delete the home,
// strays included, with no check and no line. One rule on every path (design §5, SHR-D5) means it
// repatriates them instead.
func TestHostPiResidentCloseRepatriatesAStray(t *testing.T) {
	home := hostGateHome(t, ownedSessionHomeConfig(sessionCodexCfg("")), nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	_, sessionHome, ok := strings.Cut(homes.vars[0], "=")
	if !ok {
		t.Fatalf("malformed session-home variable %q", homes.vars[0])
	}
	if err := os.MkdirAll(filepath.Join(sessionHome, "pi-web-access"), 0o700); err != nil {
		t.Fatal(err)
	}
	const stamp = `{"version":"0.45.0"}`
	if err := os.WriteFile(filepath.Join(sessionHome, "pi-web-access", "last-seen-version.json"), []byte(stamp), 0o600); err != nil {
		t.Fatal(err)
	}
	var errw bytes.Buffer
	if err := finishHostSessionHomes(homes, &errw); err != nil {
		t.Fatalf("a stray must not fail the close: %v", err)
	}
	if existsHostPath(dir) {
		t.Error("the close left its own home behind")
	}
	real := filepath.Join(home, ".pi", "agent", "pi-web-access", "last-seen-version.json")
	if got, err := os.ReadFile(real); err != nil || string(got) != stamp {
		t.Fatalf("the close did not repatriate the stray: %q (%v)", got, err)
	}
	if !strings.Contains(errw.String(), "moved it into your config directory") {
		t.Errorf("the close must say what it moved:\n%s", errw.String())
	}
}

// SHR-D8: reaping is janitorial. A capture failure on an ended home keeps that home and says so,
// and the launch it happened during still starts. Refusing here is what made one dead session's
// directory deny service to every later launch.
func TestHostLaunchContinuesWhenAnEndedHomeCannotBeCaptured(t *testing.T) {
	cfg := ownedSessionHomeConfig(sessionCodexCfg(""))
	home := hostGateHome(t, cfg, nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	homes.preserveForRetry()
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	// A host writer holds the capture lock, so this ended home's capture cannot run.
	writer := tryHostApplyLock(paths.Home())
	if writer == nil {
		t.Fatal("could not take the host-apply lock for the concurrency fixture")
	}
	defer writer.Close()
	var errw bytes.Buffer
	next, _, err := prepareHostSessionHomes(launch, os.Environ(), &errw)
	if err != nil {
		t.Fatalf("a reap failure refused the launch: %v\n%s", err, errw.String())
	}
	if next == nil || len(next.dirs) != 1 {
		t.Fatalf("the launch did not get its own session home: %+v", next)
	}
	defer next.remove()
	if !existsHostPath(dir) {
		t.Error("the ended home was not kept for retry")
	}
	for _, want := range []string{"could not capture the ended session home", "this launch continues"} {
		if !strings.Contains(errw.String(), want) {
			t.Errorf("the warning must say %q:\n%s", want, errw.String())
		}
	}
}

func TestHostPiResidentCaptureRefusalKeepsHomeForRetry(t *testing.T) {
	home := hostGateHome(t, ownedSessionHomeConfig(sessionCodexCfg("")), nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	launch := composeHostLaunch("pi", "codex", nil, nil)
	homes, _, err := prepareHostSessionHomes(launch, os.Environ(), io.Discard)
	if err != nil || homes == nil || len(homes.dirs) != 1 {
		t.Fatalf("prepare session home: homes=%+v err=%v", homes, err)
	}
	dir := homes.dirs[0]
	_, sessionHome, ok := strings.Cut(homes.vars[0], "=")
	if !ok {
		t.Fatalf("malformed session-home variable %q", homes.vars[0])
	}
	settingsPath := filepath.Join(sessionHome, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"customUserSetting":"saved-by-user"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	writer := tryHostApplyLock(paths.Home())
	if writer == nil {
		t.Fatal("could not take host-apply lock for the concurrency fixture")
	}
	if err := finishHostSessionHomes(homes, io.Discard); err == nil {
		t.Fatal("capture must refuse while another yolo writer holds the host lock")
	}
	if !homes.preserved {
		t.Fatal("a refused resident capture did not preserve the source for retry")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the resident session source was removed after capture refused: %v", err)
	}
	writer.Close()
	if err := finishHostSessionHomes(homes, io.Discard); err != nil {
		t.Fatalf("retry after the writer finished: %v", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("successful retry did not remove the ended home: %v", err)
	}
	overlay, err := os.ReadFile(render.Host(home, nil, render.OwnershipOwn).OverlayPath("pi", "settings"))
	if err != nil {
		t.Fatalf("retry did not write the host capture overlay: %v", err)
	}
	var captured map[string]any
	if err := json.Unmarshal(overlay, &captured); err != nil {
		t.Fatal(err)
	}
	if captured["customUserSetting"] != "saved-by-user" {
		t.Errorf("retry did not capture the session edit: %s", overlay)
	}
}

func existsHostPath(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// The host env command is an inspection verb, not a command launch, so it remains available under
// `none` and must not enter the SP-R1 launch gate.
func TestHostEnvRemainsAvailableWhenCommandLaunchesAreRefused(t *testing.T) {
	hostGateHomeRaw(t, `{"host_management":"none","packs":["pi"]}`, nil)
	var out, errw bytes.Buffer
	rc := hostMain([]string{"env", "--agent", "pi"}, &out, &errw, false, nil)
	if rc != 0 || strings.Contains(errw.String(), "refusing to launch") {
		t.Fatalf("host env is an inspection command under none (rc=%d):\n%s", rc, errw.String())
	}
}

// A DEAD LAUNCH'S HOME IS REAPED by the next launch: one whose lease nobody holds goes, one whose
// lease is held (a pi still running) stays.
func TestHostPiSessionHomeReapsADeadLaunch(t *testing.T) {
	env := piSessionHomeEnv(t)
	home := hostGateHome(t, ownedSessionHomeConfig(sessionCodexCfg("")), nil)
	piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
	root := filepath.Join(paths.GlobalStorageUnder(home), prune.HostSessionHomesLeaf)
	old := time.Now().Add(-2 * time.Hour)
	mk := func(name string) string {
		t.Helper()
		dir := filepath.Join(root, name)
		writeFile(t, filepath.Join(dir, packload.EmbeddedLeaseName), "")
		writeFile(t, filepath.Join(dir, "home", ".pi", "agent", "settings.json"), "{}")
		target := render.Host(filepath.Join(dir, hostScratchHomeName), nil, render.OwnershipOwn)
		writeFile(t, target.LastRenderPath("pi", "settings"), "{}\n")
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	dead, live := mk("pi-dead"), mk("pi-live")
	f, err := os.Open(filepath.Join(live, packload.EmbeddedLeaseName))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH); err != nil {
		t.Fatal(err)
	}
	run := hostSelectionRun(t, home, []string{"-p", "pi=codex"}, "pi")
	if run.env[env] == "" {
		t.Fatalf("the launch got no session home\n%s", run.errs)
	}
	if _, err := os.Lstat(dead); !os.IsNotExist(err) {
		t.Errorf("the dead launch's session home %s was not reaped (%v)", dead, err)
	}
	if _, err := os.Lstat(live); err != nil {
		t.Errorf("a running launch's session home %s was reaped: %v", live, err)
	}
}

// SP-D6 AT THE HOST: a home that cannot be made refuses the launch, naming the path and the
// command to copy, and pi never starts.
func TestHostPiSessionHomeRefusesWhenItCannotBeMade(t *testing.T) {
	var root string
	rc, env, errs := hostGateRunIn(t, ownedSessionHomeConfig(sessionCodexCfg("")), nil, []string{"-p", "pi=codex"}, "pi", func(home string) {
		piSettingsFixture(t, home, `{"defaultProvider":"openrouter"}`)
		root = filepath.Join(paths.GlobalStorageUnder(home), prune.HostSessionHomesLeaf)
		writeFile(t, root, "not a directory")
	})
	if rc == 0 || env != nil {
		t.Fatalf("a session home that cannot be made must refuse the launch (rc=%d)\n%s", rc, errs)
	}
	for _, want := range []string{"cannot make", root, "ls -la"} {
		if !strings.Contains(errs, want) {
			t.Errorf("the refusal must say %q:\n%s", want, errs)
		}
	}
}

// IN A JAIL `yolo host` makes no session home: the jail's own session pass made one for the
// session already (SP-D25). The guard is the step's first act, so it reads nothing of the launch.
func TestHostSessionHomesDoNothingInAJail(t *testing.T) {
	t.Setenv("YOLO_VERSION", "test")
	env := []string{"A=1"}
	homes, got, err := prepareHostSessionHomes(nil, env, os.Stderr)
	if homes != nil || err != nil || len(got) != 1 || got[0] != "A=1" {
		t.Errorf("in a jail the step must hand the environment back untouched: %v %q %v", homes, got, err)
	}
}

// THE RESIDENT PATH: a launch that stays resident removes its directory when the program exits
// (Close), and one whose exec handed the leases over leaves it for the reaper.
func TestHostSessionHomesCloseRemovesUnlessHandedOver(t *testing.T) {
	root := t.TempDir()
	mk := func() *hostSessionHomes {
		t.Helper()
		dir, lease, err := makeHostSessionDir(root, "pack")
		if err != nil || lease == nil {
			t.Fatalf("makeHostSessionDir: %v (lease %v)", err, lease)
		}
		return &hostSessionHomes{dirs: []string{dir}, leases: []*os.File{lease}}
	}
	resident := mk()
	dir := resident.dirs[0]
	if state, unlock, _ := packload.ProbeLease(dir); state != packload.LeaseHeld {
		unlock()
		t.Fatalf("a fresh launch directory's lease is %s, want held", state)
	}
	resident.Close()
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Errorf("Close left %s (%v)", dir, err)
	}
	execd := mk()
	execd.KeepAcrossExec(os.Stderr)
	execd.Close()
	if _, err := os.Lstat(execd.dirs[0]); err != nil {
		t.Errorf("Close after KeepAcrossExec removed the program's directory: %v", err)
	}
	execd.remove()
}

// SP-D36 AT THE HOST: pi and omp both declare stock pi's variable, and a host launch execs one
// program with one environment, so the launched program's own pack gets the home and the other
// pack's declaration is dropped with one line naming it. Launching each in turn hands each its
// own pack's directory. Fails if prepareHostSessionHomes stops calling oneHostHomePerVariable.
func TestHostLaunchKeepsTheLaunchedPacksHomeWhenTwoDeclareOneVariable(t *testing.T) {
	env := piSessionHomeEnv(t)
	home := hostGateHome(t, ownedSessionHomeConfig(`{"packs": ["claude", "omp", "pi"]}`), nil)
	root := filepath.Join(paths.GlobalStorageUnder(home), prune.HostSessionHomesLeaf)
	for _, tc := range []struct{ agent, pack, other, rel string }{
		{"pi", "pi", "omp", filepath.Join(".pi", "agent")},
		{"oh-omp", "omp", "pi", filepath.Join(".oh-omp", "agent")},
	} {
		run := hostSelectionRun(t, home, nil, tc.agent)
		dir := run.env[env]
		if !strings.HasPrefix(dir, filepath.Join(root, tc.pack+"-")) || !strings.HasSuffix(dir, string(filepath.Separator)+tc.rel) {
			t.Errorf("%s got %s=%q, want its own pack %s's home under %s\n%s", tc.agent, env, dir, tc.pack, root, run.errs)
		}
		want := "pack " + tc.other + ": " + env + " is pack " + tc.pack + "'s for this launch of " + tc.agent
		if strings.Count(run.errs, want) != 1 {
			t.Errorf("launching %s printed no single line for the dropped %s declaration %q:\n%s", tc.agent, tc.other, want, run.errs)
		}
	}
}

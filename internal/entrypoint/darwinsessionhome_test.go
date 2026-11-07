package entrypoint

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/nativehome"
)

const nativeA = "0123456789abcdef"
const nativeB = "fedcba9876543210"

func nativeIdle(t *testing.T, idle bool, err error) {
	t.Helper()
	old := nativeAccountIdle
	nativeAccountIdle = func() (bool, error) { return idle, err }
	t.Cleanup(func() { nativeAccountIdle = old })
}

// nativePass exercises RunDarwinBootstrap itself and its selected pack reader, not a
// second boot list. No program or API is started by bootstrap's pure generators.
func (j *sessionHomeJail) nativePass(t *testing.T, id, profile string, extra map[string]string) (*Env, error) {
	t.Helper()
	_ = os.Unsetenv(j.env)
	use := `{"pi":"` + profile + `"}`
	if profile == "" {
		use = `{}`
	}
	root := t.TempDir()
	for _, p := range j.packs {
		if err := os.CopyFS(filepath.Join(root, p.Name), os.DirFS(p.Root)); err != nil {
			t.Fatal(err)
		}
	}
	e := DarwinEnvFrom(map[string]string{
		"YOLO_DARWIN_WORKSPACE": j.workspace, "YOLO_PACK_ROOT": root,
		"YOLO_PROVIDERS": composedFor(t, twoProviders, ""), "YOLO_USE_PROFILES": use,
		"YOLO_PROFILES": nameAsProviderTable(t, use),
	}, j.home)
	e.Stderr = &bytes.Buffer{}
	for k, v := range extra {
		e.Vars[k] = v
	}
	err := RunDarwinBootstrap(e, DarwinBootstrapOptions{SessionID: id})
	return e, err
}

func (j *sessionHomeJail) nativeDir(t *testing.T, id string) string {
	t.Helper()
	dir, err := nativehome.Dir(j.home, j.workspace, id)
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
func (j *sessionHomeJail) nativeSettings(t *testing.T, id string) string {
	return filepath.Join(j.nativeDir(t, id), ".pi", "agent", "settings.json")
}
func nativeLaunch(t *testing.T, dir string) *os.File {
	t.Helper()
	f, err := nativehome.Enter(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestDarwinSessionHomesKeepProfilesAndSharedStatePrivate(t *testing.T) {
	j := newSessionHomeJail(t)
	nativeIdle(t, false, nil)
	e, err := j.nativePass(t, nativeA, "acme", nil)
	if err != nil {
		t.Fatal(err)
	}
	a := j.nativeSettings(t, nativeA)
	before := readBytes(t, a)
	if e.Getenv(j.env) != filepath.Dir(a) {
		t.Fatal("bootstrap relocation missing")
	}
	if e.Getenv(SessionIDEnv) != nativeA {
		t.Fatal("native identity changed")
	}
	f := nativeLaunch(t, j.nativeDir(t, nativeA))
	defer f.Close()
	for _, id := range []string{nativeB, "cccccccccccccccc"} {
		profile := "beta"
		if id == "cccccccccccccccc" {
			profile = ""
		}
		if _, err := j.nativePass(t, id, profile, nil); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(readBytes(t, a), before) {
			t.Fatal("later profile/default entry rewrote session A")
		}
	}
	if got := defaultProviderIn(t, a); got != "acme" {
		t.Fatalf("A provider = %s", got)
	}
	if _, err := os.Lstat(j.shared("settings.json")); !os.IsNotExist(err) {
		t.Fatalf("native entry rendered shared settings: %v", err)
	}
	sharedAuth := filepath.Join(filepath.Dir(a), "auth.json")
	target, err := os.Readlink(sharedAuth)
	if err != nil {
		t.Fatal(err)
	}
	if target != j.shared("auth.json") {
		t.Fatalf("auth links to %s", target)
	}
	if err := os.WriteFile(sharedAuth, []byte("shared login"), 0600); err != nil {
		t.Fatal(err)
	}
	if string(readBytes(t, j.shared("auth.json"))) != "shared login" {
		t.Fatal("absent-file link did not share first login")
	}
}

func TestDarwinSessionCaptureFirstRunRetiresOnlyProvenGone(t *testing.T) {
	j := newSessionHomeJail(t)
	nativeIdle(t, true, nil)
	if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
		t.Fatal(err)
	}
	a := j.nativeSettings(t, nativeA)
	editSessionSettings(t, a)
	f := nativeLaunch(t, j.nativeDir(t, nativeA))
	if _, err := j.nativePass(t, nativeB, "beta", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatal("held lease failed to protect home")
	}
	if got := jsonKey(t, j.nativeSettings(t, nativeB), "theme"); got == "session-theme" {
		t.Fatal("live session edit captured")
	}
	_ = f.Close()
	if _, err := j.nativePass(t, "cccccccccccccccc", "beta", nil); err != nil {
		t.Fatal(err)
	}
	c := j.nativeSettings(t, "cccccccccccccccc")
	if got := jsonKey(t, c, "theme"); got != "session-theme" {
		t.Fatalf("first-run capture lost theme: %v", got)
	}
	if !hasPackage(t, c, "npm:session-pkg") {
		t.Fatal("normal config-list capture lost install")
	}
	if got := defaultProviderIn(t, c); got != "beta" {
		t.Fatalf("captured provider selection %s", got)
	}
	if _, err := os.Stat(j.nativeDir(t, nativeA)); !os.IsNotExist(err) {
		t.Fatalf("gone home retained: %v", err)
	}
	// No shared baseline exists; the next private render must still fold the accumulated overlay.
	if _, err := j.nativePass(t, "dddddddddddddddd", "acme", nil); err != nil {
		t.Fatal(err)
	}
	if got := jsonKey(t, j.nativeSettings(t, "dddddddddddddddd"), "theme"); got != "session-theme" {
		t.Fatalf("later entry lost overlay: %v", got)
	}
}

func TestDarwinSessionCaptureRetainsDetachedUnknownAndFailedSources(t *testing.T) {
	for _, tc := range []struct {
		name string
		idle bool
		err  error
	}{
		{"detached account child", false, nil}, {"unknown account census", false, fmt.Errorf("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := newSessionHomeJail(t)
			nativeIdle(t, tc.idle, tc.err)
			if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
				t.Fatal(err)
			}
			f := nativeLaunch(t, j.nativeDir(t, nativeA))
			_ = f.Close()
			a := j.nativeSettings(t, nativeA)
			editSessionSettings(t, a)
			before := readBytes(t, a)
			if _, err := j.nativePass(t, nativeB, "beta", nil); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(readBytes(t, a), before) {
				t.Fatal("unknown/live descendant source touched")
			}
		})
	}
	t.Run("parse failure retry and stray", func(t *testing.T) {
		j := newSessionHomeJail(t)
		nativeIdle(t, true, nil)
		if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
			t.Fatal(err)
		}
		a := j.nativeSettings(t, nativeA)
		good := readBytes(t, a)
		f := nativeLaunch(t, j.nativeDir(t, nativeA))
		_ = f.Close()
		if err := os.WriteFile(a, []byte(`{"theme":"half`), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := j.nativePass(t, nativeB, "beta", nil); err != nil {
			t.Fatal(err)
		}
		if string(readBytes(t, a)) != `{"theme":"half` {
			t.Fatal("failed capture removed source")
		}
		if err := os.WriteFile(a, good, 0600); err != nil {
			t.Fatal(err)
		}
		editSessionSettings(t, a)
		stray := filepath.Join(filepath.Dir(a), "user-note")
		if err := os.WriteFile(stray, []byte("keep"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := j.nativePass(t, "cccccccccccccccc", "beta", nil); err != nil {
			t.Fatal(err)
		}
		if string(readBytes(t, stray)) != "keep" {
			t.Fatal("stray removed")
		}
		if got := jsonKey(t, j.nativeSettings(t, "cccccccccccccccc"), "theme"); got != "session-theme" {
			t.Fatal("retry did not capture")
		}
	})
}

func TestDarwinSessionHomesOverrideFailureAndWorkspaceFence(t *testing.T) {
	t.Run("override", func(t *testing.T) {
		j := newSessionHomeJail(t)
		nativeIdle(t, true, nil)
		override := t.TempDir()
		e, err := j.nativePass(t, nativeA, "acme", map[string]string{j.env: override})
		if err != nil {
			t.Fatal(err)
		}
		if e.Getenv(j.env) != override {
			t.Fatal("override replaced")
		}
		if _, err := os.Stat(j.shared("settings.json")); !os.IsNotExist(err) {
			t.Fatal("override rendered shared config")
		}
	})
	t.Run("construction failure", func(t *testing.T) {
		j := newSessionHomeJail(t)
		nativeIdle(t, true, nil)
		if err := os.WriteFile(filepath.Join(j.home, ".yolo-native-sessions"), []byte("occupied"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := j.nativePass(t, nativeA, "acme", nil); err == nil {
			t.Fatal("construction failure not fatal")
		}
		if _, err := os.Stat(j.shared("settings.json")); !os.IsNotExist(err) {
			t.Fatal("refusal rendered shared config")
		}
	})
	t.Run("workspace namespace", func(t *testing.T) {
		j := newSessionHomeJail(t)
		nativeIdle(t, true, nil)
		if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
			t.Fatal(err)
		}
		a := j.nativeSettings(t, nativeA)
		f := nativeLaunch(t, j.nativeDir(t, nativeA))
		_ = f.Close()
		before := readBytes(t, a)
		j.workspace = t.TempDir()
		if _, err := j.nativePass(t, nativeB, "beta", nil); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(readBytes(t, a), before) {
			t.Fatal("other workspace captured/removed home")
		}
	})
	t.Run("bad trusted ID", func(t *testing.T) {
		j := newSessionHomeJail(t)
		if _, err := j.nativePass(t, "missing", "acme", nil); err == nil {
			t.Fatal("malformed identity accepted")
		}
		if _, err := os.Stat(j.shared("settings.json")); !os.IsNotExist(err) {
			t.Fatal("bad identity rendered shared config")
		}
	})
}

func TestDarwinSessionCompatibilityCapturePreservesUserCustomizations(t *testing.T) {
	j := newSessionHomeJail(t)
	nativeIdle(t, true, nil)
	// No historical apply/render baseline: normal initial adoption must remain supported.
	shared := j.shared("settings.json")
	if err := os.WriteFile(shared, []byte(`{"theme":"legacy-theme","packages":["npm:legacy-user"],"defaultProvider":"legacy","defaultModel":"legacy-model"}`), 0600); err != nil {
		t.Fatal(err)
	}
	source := readBytes(t, shared)
	if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
		t.Fatal(err)
	}
	a := j.nativeSettings(t, nativeA)
	if got := jsonKey(t, a, "theme"); got != "legacy-theme" {
		t.Fatalf("first private home lost customization: %v", got)
	}
	if !hasPackage(t, a, "npm:legacy-user") {
		t.Fatal("initial config-list adoption lost user package")
	}
	if got := defaultProviderIn(t, a); got != "acme" {
		t.Fatalf("legacy provider overrode selected profile: %s", got)
	}
	if !bytes.Equal(source, readBytes(t, shared)) {
		t.Fatal("compatibility pass rewrote shared settings")
	}
	f := nativeLaunch(t, j.nativeDir(t, nativeA))
	_ = f.Close()
	setJSONKey(t, a, "theme", "new-session-theme")
	if _, err := j.nativePass(t, nativeB, "beta", nil); err != nil {
		t.Fatal(err)
	}
	b := j.nativeSettings(t, nativeB)
	if got := jsonKey(t, b, "theme"); got != "new-session-theme" {
		t.Fatalf("ended private edit lost: %v", got)
	}
	if _, err := j.nativePass(t, "cccccccccccccccc", "beta", nil); err != nil {
		t.Fatal(err)
	}
	c := j.nativeSettings(t, "cccccccccccccccc")
	if got := jsonKey(t, c, "theme"); got != "new-session-theme" {
		t.Fatalf("unchanged legacy source re-adopted: %v", got)
	}
	// A genuine later user edit to the legacy source is still an ordinary managed delta.
	setJSONKey(t, shared, "theme", "later-shared-theme")
	if _, err := j.nativePass(t, "dddddddddddddddd", "", nil); err != nil {
		t.Fatal(err)
	}
	d := j.nativeSettings(t, "dddddddddddddddd")
	if got := jsonKey(t, d, "theme"); got != "later-shared-theme" {
		t.Fatalf("no-profile later shared edit lost: %v", got)
	}
	if !hasPackage(t, d, "npm:legacy-user") {
		t.Fatal("no-profile entry lost user package")
	}
}

func TestDarwinSessionWithoutDeclaringPackMakesNoHome(t *testing.T) {
	t.Setenv(SessionIDEnv, "")
	home, workspace := t.TempDir(), t.TempDir()
	e := DarwinEnvFrom(map[string]string{"YOLO_DARWIN_WORKSPACE": workspace}, home)
	e.Stderr = &bytes.Buffer{}
	if err := RunDarwinBootstrap(e, DarwinBootstrapOptions{SessionID: nativeA}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(home, ".yolo-native-sessions")); !os.IsNotExist(err) {
		t.Fatalf("no-pack launch created config home: %v", err)
	}
}

func TestDarwinSessionChangedInventoryKeepsUnselectedSourceAndBaseline(t *testing.T) {
	j := newSessionHomeJail(t)
	nativeIdle(t, true, nil)
	if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
		t.Fatal(err)
	}
	dir := j.nativeDir(t, nativeA)
	f := nativeLaunch(t, dir)
	_ = f.Close()
	// A previously selected declaration not present in the next tree must not lose
	// its source or capture baseline when the still-selected Pi declaration retires.
	other := filepath.Join(dir, ".other", "agent", "settings.json")
	if err := os.MkdirAll(filepath.Dir(other), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte(`{"user":"keep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	baseline := filepath.Join(j.workspace, ".yolo", "prism", "sessions", nativeA, "other-settings.last_render")
	if err := os.WriteFile(baseline, []byte(`{"user":"baseline"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := j.nativePass(t, nativeB, "beta", nil); err != nil {
		t.Fatal(err)
	}
	if string(readBytes(t, other)) != `{"user":"keep"}` {
		t.Fatal("unselected source lost")
	}
	if string(readBytes(t, baseline)) != `{"user":"baseline"}` {
		t.Fatal("unselected baseline lost")
	}
	release, gone := nativehome.ClaimGone(dir)
	defer release()
	if !gone {
		t.Fatal("retained source lost its lease/launch evidence")
	}
}

func TestDarwinSessionRefusesAnUnavailableCaptureLock(t *testing.T) {
	j := newSessionHomeJail(t)
	nativeIdle(t, true, nil)
	lock := filepath.Join(j.workspace, ".yolo", "prism", sessionRenderLockName)
	if err := os.MkdirAll(lock, 0700); err != nil {
		t.Fatal(err)
	}
	shared := j.shared("settings.json")
	if err := os.WriteFile(shared, []byte(`{"theme":"untouched"}`), 0600); err != nil {
		t.Fatal(err)
	}
	before := readBytes(t, shared)
	if _, err := j.nativePass(t, nativeA, "acme", nil); err == nil {
		t.Fatal("unserialized native capture was allowed")
	}
	if !bytes.Equal(readBytes(t, shared), before) {
		t.Fatal("lock refusal modified source")
	}
	if _, err := os.Stat(filepath.Join(j.workspace, ".yolo", "prism", "pi-settings.overlay.json")); !os.IsNotExist(err) {
		t.Fatal("lock refusal captured without serialization")
	}
}

// SP-D36 on macos-user: a native launch with two packs on one variable gives each its own home
// under the native namespace, and the session metadata native-session-exec restores carries the
// per-program bindings, so a command started after the bootstrap still binds each program to its
// own home.
func TestDarwinSessionHomeBindingsReachTheNativeCommandEnvironment(t *testing.T) {
	j := newSessionHomeJail(t)
	omp, err := embeddedPack("omp")
	if err != nil {
		t.Fatal(err)
	}
	j.packs = append(j.packs, omp)
	nativeIdle(t, false, nil)
	t.Setenv(SessionHomesEnv, "")
	_ = os.Unsetenv(SessionHomesEnv)
	if _, err := j.nativePass(t, nativeA, "acme", nil); err != nil {
		t.Fatal(err)
	}
	dir := j.nativeDir(t, nativeA)
	// Pack order, which the native loader takes sorted by name.
	want := "oh-omp\t" + j.env + "\t" + filepath.Join(dir, ".oh-omp", "agent") + "\n" +
		"pi\t" + j.env + "\t" + filepath.Join(dir, ".pi", "agent")
	_ = os.Unsetenv(SessionHomesEnv)
	f := nativeLaunch(t, dir)
	defer f.Close()
	if got := os.Getenv(SessionHomesEnv); got != want {
		t.Fatalf("native-session-exec restored %s=%q, want %q", SessionHomesEnv, got, want)
	}
	if fi, err := os.Lstat(filepath.Join(dir, ".oh-omp", "agent", "config.yml")); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("omp's native session config is not its own file: %v", err)
	}
}

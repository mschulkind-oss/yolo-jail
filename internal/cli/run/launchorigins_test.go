package run

// launchorigins_test.go pins the launch's half of config/launchorigins.go THROUGH ITS CALL SITES:
// the generated user scope a launch mounts (userConfigMountArgs → inheritedScope) and the
// assembled copy it writes (loadAndValidateConfig → writeLaunchConfigArtifacts) each carry the
// record that lets a refusal in the jail name the host file that set a key. Deleting the record
// at either site fails here, not only in internal/config.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

const launchCopyRelaunch = " on the host; this jail reads a copy made at launch, so an edit there takes effect " +
	"when the jail is relaunched)"

// hostUserConfigWithInclude writes the host user config as an include of extra.jsonc, the file
// that sets the key the tests ask about: host-only, never mounted into a jail.
func hostUserConfigWithInclude(t *testing.T, home, extra string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(`{"include_if_found": ["extra.jsonc"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.jsonc"), []byte(extra), 0o644); err != nil {
		t.Fatal(err)
	}
}

// jailHomeWith makes a scratch jail home whose user config is body, and enters the jail.
func jailHomeWith(t *testing.T, body, ext string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		if ext == "" {
			ext = ".jsonc"
		}
		if err := os.WriteFile(filepath.Join(dir, "config"+ext), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("YOLO_VERSION", "9.9.9-test")
}

func TestTheMountedUserScopeNamesTheHostFileOfARefusedKey(t *testing.T) {
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(config.UserLayerEnv, "")
	home, wsState := inheritHome(t, "")
	hostUserConfigWithInclude(t, home,
		`{"providers": {"kilo": {"models": {"default": {"id": "m", "wireId": "w"}}}}}`)
	o := inheritOptions(t)

	body, source, found := mountedBody(t, o.userConfigMountArgs("podman", wsState), "/home/agent/"+inheritPreflightLegacyRel)
	if !found {
		t.Fatal("no preflight user config was mounted")
	}

	// What `yolo check` in the jail reads and reports (check.sectionConfigFiles).
	jailHomeWith(t, body, filepath.Ext(source))
	cfg, src, err := config.UserScopeConfigWithSources(true, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	errs, _ := config.ValidateConfig(cfg, t.TempDir(), nil)
	var got string
	for _, e := range src.Annotate(errs) {
		if strings.Contains(e, "wireId: unknown key") {
			got = e
		}
	}
	if !strings.HasPrefix(got, "config.providers.kilo.models.default.wireId: unknown key") ||
		!strings.HasSuffix(got, "(set in ~/.config/yolo-jail/extra.jsonc"+launchCopyRelaunch) {
		t.Errorf("the in-jail refusal does not name the host file that set the key:\n%s\nall: %q", got, errs)
	}
}

func TestTheAssembledCopyNamesTheHostFileOfAKey(t *testing.T) {
	ws := t.TempDir()
	o, _ := approvalOptions(t, ws, false)
	o.Getenv = os.Getenv
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(config.UserLayerEnv, "")
	home := os.Getenv("HOME")
	if err := os.MkdirAll(filepath.Join(home, ".config", "yolo-jail"), 0o755); err != nil {
		t.Fatal(err)
	}
	hostUserConfigWithInclude(t, home, `{"packages": ["ripgrep"]}`)

	cfg, ok := o.loadAndValidateConfig()
	if !ok {
		t.Fatal("the launch refused the config")
	}
	o.writeLaunchConfigArtifacts(cfg)

	// The jail's own-workspace read, with no user config of its own.
	jailHomeWith(t, "", "")
	t.Setenv("YOLO_WORKSPACE", ws)
	t.Setenv("YOLO_SETUP", o.setupName()) // the jail reads its own setup's records (SUP-D37)
	_, src, err := config.LoadConfigWithSources(ws, false, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	got := src.AnnotateOne("config.install.nix[0]: refused")
	if want := "config.install.nix[0]: refused (set in ~/.config/yolo-jail/extra.jsonc" + launchCopyRelaunch; got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

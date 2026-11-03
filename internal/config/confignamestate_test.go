package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/paths"
)

// mintDir is t.TempDir with its symlinks resolved, minted once: on darwin t.TempDir is under
// /var -> /private/var, and the refusal names the path as the scope spells it.
func mintDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// RC-D59, the owner's case: a workspace yolo-jail.jsonc that is a dotfiles link to a target this
// machine lacks, beside a yolo-jail.toml that reads. The launch read the TOML and said nothing.
func TestADanglingConfigLinkRefusesTheLaunchNamingTheLinkAndItsTarget(t *testing.T) {
	dir := mintDir(t)
	write(t, filepath.Join(dir, paths.WorkspaceConfigTOMLName), "[install]\nnix = ['just']\n")
	link := filepath.Join(dir, paths.WorkspaceConfigLegacyJSONCName)
	symlink(t, "swarf/.links/yolo-jail.jsonc", link)

	_, err := LoadWorkspaceConfig(dir, true, nil)
	if err == nil {
		t.Fatal("a dangling yolo-jail.jsonc link beside a valid yolo-jail.toml was read as no file")
	}
	want := link + " is a symbolic link to swarf/.links/yolo-jail.jsonc, which does not exist. " +
		"Restore the target (if a dotfiles manager keeps this file, sync it on this machine), or remove the link: rm " + link + ". Nothing was started."
	if err.Error() != want {
		t.Fatalf("refusal =\n%s\nwant\n%s", err, want)
	}
	var ce *ConfigError
	if !errors.As(err, &ce) || ce.Fact == "" || ce.Step == "" {
		t.Fatalf("refusal %#v carries no fact/step for yolo check", err)
	}
	// Non-strict readers refuse too: the name is broken, not malformed.
	if _, err := LoadWorkspaceConfig(dir, false, func(string) {}); err == nil {
		t.Fatal("a non-strict read skipped the dangling link")
	}
	// The local file's names are candidates as well.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(dir, paths.WorkspaceLocalConfigTOMLName)
	symlink(t, "nowhere.toml", local)
	if _, err := LoadWorkspaceConfig(dir, true, nil); err == nil || !strings.Contains(err.Error(), local+" is a symbolic link to nowhere.toml") {
		t.Fatalf("dangling local link: %v", err)
	}
}

func TestADanglingUserConfigLinkIsRefusedInEveryScope(t *testing.T) {
	home := mintDir(t)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	dir := filepath.Join(home, ".config", "yolo-jail")

	// The top-level user config: a dangling config.toml beside a real config.jsonc is the link.
	write(t, filepath.Join(dir, paths.UserConfigLegacyJSONCName), `{"packs":["git"]}`)
	top := filepath.Join(dir, paths.UserConfigTOMLName)
	symlink(t, "../../.dotfiles/yolo/config.toml", top)
	if _, err := LoadReadableUserScopeConfig(true, nil); err == nil ||
		!strings.HasPrefix(err.Error(), top+" is a symbolic link to ../../.dotfiles/yolo/config.toml, which does not exist.") {
		t.Fatalf("dangling config.toml beside config.jsonc: %v", err)
	}
	if err := os.Remove(top); err != nil {
		t.Fatal(err)
	}

	// An include_if_found entry that is a link to nothing is NOT FOUND: "include if found" is
	// best effort, so the load goes on without it and says once what it skipped (RC-D67).
	write(t, filepath.Join(dir, paths.UserConfigLegacyJSONCName), `{"packs":["git"], "include_if_found":["work.jsonc"]}`)
	inc := filepath.Join(dir, "work.jsonc")
	symlink(t, "missing/work.jsonc", inc)
	var notices []string
	got, err := LoadReadableUserScopeConfig(true, func(msg string) { notices = append(notices, msg) })
	if err != nil {
		t.Fatalf("a dangling include_if_found entry stopped the load: %v", err)
	}
	if packs, _ := got.Get("packs"); packs == nil {
		t.Fatalf("the including file was not read: %v", got)
	}
	if len(notices) != 1 || !strings.HasPrefix(notices[0], "include_if_found[0] in ") ||
		!strings.Contains(notices[0], inc+" is a symbolic link to missing/work.jsonc, which does not exist") ||
		!strings.Contains(notices[0], "skipped") {
		t.Fatalf("dangling include_if_found notice: %q", notices)
	}
	// A loop, a directory and an unreadable entry are not found either.
	if err := os.Remove(inc); err != nil {
		t.Fatal(err)
	}
	symlink(t, "work.jsonc", inc)
	if _, err := LoadReadableUserScopeConfig(true, nil); err != nil {
		t.Fatalf("an include_if_found loop stopped the load: %v", err)
	}
	if err := os.Remove(inc); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(inc, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadReadableUserScopeConfig(true, nil); err != nil {
		t.Fatalf("an include_if_found directory stopped the load: %v", err)
	}
	if err := os.Remove(inc); err != nil {
		t.Fatal(err)
	}
	// A truly absent include stays "if found", silently.
	if _, err := LoadReadableUserScopeConfig(true, nil); err != nil {
		t.Fatalf("an absent include_if_found entry refused: %v", err)
	}

	// A config.d fragment that is a link to nothing, under a TOML user config.
	if err := os.Remove(filepath.Join(dir, paths.UserConfigLegacyJSONCName)); err != nil {
		t.Fatal(err)
	}
	write(t, top, "packs = ['git']\n")
	frag := filepath.Join(dir, "config.d", "work.toml")
	symlink(t, "../gone/work.toml", frag)
	if _, err := LoadReadableUserScopeConfig(true, nil); err == nil ||
		!strings.HasPrefix(err.Error(), "config.d/work.toml: "+frag+" is a symbolic link to ../gone/work.toml, which does not exist.") {
		t.Fatalf("dangling config.d fragment: %v", err)
	}
	if err := os.Remove(frag); err != nil {
		t.Fatal(err)
	}

	// --user-layer takes the same wording.
	layer := filepath.Join(home, "layer.toml")
	symlink(t, "elsewhere.toml", layer)
	if msg := ValidateUserLayer(layer); !strings.HasPrefix(msg, "--user-layer: "+layer+" is a symbolic link to elsewhere.toml, which does not exist.") {
		t.Fatalf("dangling --user-layer: %q", msg)
	}
}

func TestAConfigNameThatIsALoopOrADirectoryOrUnreadableIsRefused(t *testing.T) {
	dir := mintDir(t)
	loop := filepath.Join(dir, paths.WorkspaceConfigTOMLName)
	symlink(t, paths.WorkspaceConfigTOMLName, loop)
	if _, err := LoadWorkspaceConfig(dir, true, nil); err == nil ||
		err.Error() != loop+" is a symbolic link that leads back to itself ("+paths.WorkspaceConfigTOMLName+"). Point it at the config file, or remove it: rm "+loop+"." {
		t.Fatalf("link loop: %v", err)
	}
	if err := os.Remove(loop); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(loop, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadWorkspaceConfig(dir, true, nil); err == nil ||
		!strings.HasPrefix(err.Error(), loop+" is a directory, and a config file is expected there.") {
		t.Fatalf("directory: %v", err)
	}
	if err := os.Remove(loop); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		t.Log("running as root, which reads any file; the unreadable case is not checked here")
		return
	}
	write(t, loop, "[install]\nnix = ['just']\n")
	if err := os.Chmod(loop, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(loop, 0o644) })
	if _, err := LoadWorkspaceConfig(dir, true, nil); err == nil ||
		!strings.HasPrefix(err.Error(), loop+" cannot be read: permission denied. Fix its permissions (chmod u+r "+loop+"), or remove it.") {
		t.Fatalf("unreadable: %v", err)
	}
}

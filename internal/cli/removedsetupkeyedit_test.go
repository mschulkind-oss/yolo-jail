package cli

// removedsetupkeyedit_test.go RUNS the steps the refusal of a removed setup key names
// (config.RemovedSetupKeyMessage; named-setups-plan.md SUP-D17) against a file holding the key,
// JSONC and TOML, at every scope: `yolo config unset <key> --user|--project|--local` removes
// exactly that key, `yolo config set setup <name> --user` is not blocked by the key it
// replaces, and `yolo config migrate` converts a TOML file v0.16.1 wrote as it converts a JSONC
// one. A refusal whose step is itself refused is a stop with no next step.

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// removedKeyRefusal is the refusal a launch would print for key in the files e holds: the
// validation line of the merged config, as `yolo check` and the launch read it.
func removedKeyRefusal(t *testing.T, e migrateEnv, key string) string {
	t.Helper()
	cfg, err := config.LoadConfig(e.ws, true, func(string) {})
	if err != nil {
		t.Fatalf("the config holding %s was not read: %v", key, err)
	}
	errs, _ := config.ValidateConfig(cfg, e.ws, nil)
	for _, l := range errs {
		if strings.HasPrefix(l, "config."+key+": ") {
			return l
		}
	}
	return ""
}

var unsetStep = regexp.MustCompile(`yolo config unset \S+ --(user|project|local)`)

func runRemovedKeyVerb(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errw bytes.Buffer
	rc := configRunW(args, &out, &errw)
	return rc, out.String(), errw.String()
}

func readText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestTheRemovedSetupKeysUnsetRemovesThatKey: for each key, format and scope, the unset the
// refusal names runs, removes the key and nothing else, and the refusal is gone.
func TestTheRemovedSetupKeysUnsetRemovesThatKey(t *testing.T) {
	for _, c := range []struct {
		name, key, scope string
		jsonc, toml      string
	}{
		{name: "user runtime", key: "runtime", scope: "--user",
			jsonc: `{"runtime": "podman", "kvm": false}`, toml: "runtime = \"podman\"\nkvm = false\n"},
		{name: "user confinement", key: "confinement", scope: "--user",
			jsonc: `{"confinement": "host", "kvm": false}`, toml: "confinement = \"host\"\nkvm = false\n"},
		{name: "user macos_user_mode", key: "macos_user_mode", scope: "--user",
			jsonc: `{"macos_user_mode": "self", "kvm": false}`, toml: "macos_user_mode = \"self\"\nkvm = false\n"},
		{name: "project runtime", key: "runtime", scope: "--project",
			jsonc: `{"runtime": "podman", "kvm": false}`, toml: "runtime = \"podman\"\nkvm = false\n"},
		{name: "project confinement", key: "confinement", scope: "--project",
			jsonc: `{"confinement": "guest", "kvm": false}`, toml: "confinement = \"guest\"\nkvm = false\n"},
		{name: "local confinement", key: "confinement", scope: "--local",
			jsonc: `{"confinement": "guest", "kvm": false}`, toml: "confinement = \"guest\"\nkvm = false\n"},
	} {
		for _, toml := range []bool{false, true} {
			format := "JSONC"
			if toml {
				format = "TOML"
			}
			t.Run(c.name+" "+format, func(t *testing.T) {
				e := newMigrateEnv(t)
				ext, body := ".jsonc", c.jsonc
				if toml {
					ext, body = ".toml", c.toml
				}
				var path string
				switch c.scope {
				case "--user":
					path = filepath.Join(e.userDir, "config"+ext)
				case "--project":
					path = filepath.Join(e.ws, "yolo-jail"+ext)
				case "--local":
					path = filepath.Join(e.ws, "yolo-jail.local"+ext)
				}
				writeMigrateFile(t, path, body)
				refusal := removedKeyRefusal(t, e, c.key)
				step := unsetStep.FindString(refusal)
				if step != "yolo config unset "+c.key+" "+c.scope {
					t.Fatalf("the refusal names %q, want the unset for %s:\n%s", step, c.scope, refusal)
				}
				rc, out, errw := runRemovedKeyVerb(t, strings.Fields(step)[2:]...)
				// A workspace edit runs `yolo check` after writing, which this fixture fails for
				// want of a flake bundle; the write is what is asked here.
				if rc != 0 && !strings.Contains(out+errw, "Written") && !strings.Contains(out+errw, "config file was written") {
					t.Fatalf("`%s` was refused: rc=%d\n%s\n%s", step, rc, out, errw)
				}
				after := readText(t, path)
				if strings.Contains(after, c.key) {
					t.Errorf("`%s` left the key in %s:\n%s", step, path, after)
				}
				if !strings.Contains(after, "kvm") {
					t.Errorf("`%s` removed more than the key from %s:\n%s", step, path, after)
				}
				if got := removedKeyRefusal(t, e, c.key); got != "" {
					t.Errorf("the refusal is still there after `%s`:\n%s", step, got)
				}
			})
		}
	}
}

// TestSettingTheSetupIsNotBlockedByTheKeyItReplaces: the refusal's first step, `yolo config set
// setup <name> --user`, runs on a user file that still holds the removed key, JSONC or TOML, and
// writes the setup; the removed key is left for its own unset.
func TestSettingTheSetupIsNotBlockedByTheKeyItReplaces(t *testing.T) {
	for _, c := range []struct{ name, file, body string }{
		{"JSONC", "config.jsonc", `{"runtime": "podman"}`},
		{"TOML", "config.toml", "runtime = \"podman\"\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newMigrateEnv(t)
			path := filepath.Join(e.userDir, c.file)
			writeMigrateFile(t, path, c.body)
			refusal := removedKeyRefusal(t, e, "runtime")
			if !strings.Contains(refusal, "yolo config set setup podman-self --user") {
				t.Fatalf("the refusal does not name the setup step:\n%s", refusal)
			}
			rc, out, errw := runRemovedKeyVerb(t, "set", "setup", "podman-self", "--user")
			// The post-write `yolo check` still reports the removed key, which is the point of its
			// own unset, and this fixture has no flake bundle: the write is what is asked here.
			if rc != 0 && !strings.Contains(out+errw, "Written") {
				t.Fatalf("`yolo config set setup podman-self --user` was refused: rc=%d\n%s\n%s", rc, out, errw)
			}
			if after := readText(t, path); !strings.Contains(after, "podman-self") || !strings.Contains(after, "runtime") {
				t.Errorf("the set did not write the setup beside the removed key:\n%s", after)
			}
		})
	}
}

// TestConfigMigrateConvertsARemovedSetupKeyInATOMLFile RUNS `yolo config migrate --yes` on a TOML
// user file v0.16.1 wrote (`yolo config set runtime podman` wrote TOML, and its migrate copied the
// key verbatim): the key converts in place as a JSONC file's does (SUP-D15, SUP-D17), the rest
// of the file is kept, and the launch's refusal is gone. A value naming no one setup, or a
// project's value, refuses the migration naming its own next step and changes nothing.
func TestConfigMigrateConvertsARemovedSetupKeyInATOMLFile(t *testing.T) {
	for _, c := range []struct {
		name, key, body, want, gone string
	}{
		{"runtime podman", "runtime", "# mine\nruntime = \"podman\"\nkvm = false\n", `setup = "podman-self"`, "runtime"},
		{"runtime container", "runtime", "runtime = \"container\"\nkvm = false\n", `setup = "apple-container-self"`, "runtime"},
		{"confinement jail", "confinement", "confinement = \"jail\"\nkvm = false\n", "", "confinement"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newMigrateEnv(t)
			path := filepath.Join(e.userDir, "config.toml")
			writeMigrateFile(t, path, c.body)
			if removedKeyRefusal(t, e, c.key) == "" {
				t.Fatalf("the fixture's %s is not refused", c.key)
			}
			rc, out, errw := runConfigMigrateVerb(t, "--dry-run")
			if rc != 0 || !strings.Contains(out, "holding keys named setups replace") {
				t.Fatalf("--dry-run: rc=%d\n%s\n%s", rc, out, errw)
			}
			if readText(t, path) != c.body {
				t.Fatalf("--dry-run changed %s", path)
			}
			rc, out, errw = runConfigMigrateVerb(t, "--yes")
			if rc != 0 {
				t.Fatalf("--yes: rc=%d\n%s\n%s", rc, out, errw)
			}
			after := readText(t, path)
			if strings.Contains(after, c.gone) || !strings.Contains(after, "kvm = false") {
				t.Errorf("the migrated file:\n%s", after)
			}
			if c.want != "" && !strings.Contains(after, c.want) {
				t.Errorf("the migrated file does not write %s:\n%s", c.want, after)
			}
			if strings.HasPrefix(c.body, "# mine") && !strings.Contains(after, "# mine") {
				t.Errorf("the migration dropped the comment:\n%s", after)
			}
			if got := removedKeyRefusal(t, e, c.key); got != "" {
				t.Errorf("the refusal is still there after the migration:\n%s", got)
			}
		})
	}
	for _, c := range []struct {
		name, file, key, body, want string
	}{
		{"user confinement host", "user", "confinement", "confinement = \"host\"\n", "yolo host -- <cmd>"},
		{"user macos_user_mode", "user", "macos_user_mode", "macos_user_mode = \"self\"\n", "yolo config unset macos_user_mode --user"},
		{"project runtime", "yolo-jail.toml", "runtime", "runtime = \"podman\"\n", "yolo config unset runtime --project"},
		{"local confinement", "yolo-jail.local.toml", "confinement", "confinement = \"guest\"\n", "yolo config unset confinement --local"},
	} {
		t.Run(c.name, func(t *testing.T) {
			e := newMigrateEnv(t)
			path := filepath.Join(e.ws, c.file)
			if c.file == "user" {
				path = filepath.Join(e.userDir, "config.toml")
			}
			writeMigrateFile(t, path, c.body)
			rc, out, errw := runConfigMigrateVerb(t, "--yes")
			if rc == 0 {
				t.Fatalf("migrate passed over %s holding %s:\n%s", c.file, c.key, out)
			}
			if !strings.Contains(out+errw, "not converted") || !strings.Contains(out+errw, c.want) {
				t.Errorf("the refusal does not name %q:\n%s\n%s", c.want, out, errw)
			}
			if readText(t, path) != c.body {
				t.Errorf("a refused migration changed %s", path)
			}
		})
	}
}

package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// setupsretired_test.go pins the refusal of the three keys named setups replace (named-setups.md
// §10; plan slice 5): each value is refused naming the setup it meant and the verb that removes the
// line, from the file that holds it; inside a jail the same line is a warning (SUP-D6). Every case
// runs through ValidateConfig, so deleting the validateSetupsRetired call fails it.

func retiredSetupLines(t *testing.T, cfg, workspace string) (errs, warns []string) {
	t.Helper()
	return ValidateConfig(decode(t, cfg), workspace, nil)
}

func lineFor(lines []string, key string) string {
	for _, l := range lines {
		if strings.HasPrefix(l, "config."+key+": ") {
			return l
		}
	}
	return ""
}

func TestRemovedSpellingsRefuseNamingTheirSetup(t *testing.T) {
	hostScope(t)
	t.Setenv("HOME", t.TempDir())
	for _, tc := range []struct {
		key, value string
		want       []string
	}{
		{"runtime", "podman", []string{"yolo config set setup podman-self --user"}},
		{"runtime", "container", []string{"yolo config set setup apple-container-self --user"}},
		{"runtime", "macos-user", []string{"yolo config set setup seatbelt-yolo-account --user", "seatbelt-self"}},
		{"runtime", "docker", []string{"yolo config set setup podman-self --user", "yolo setup list"}},
		{"confinement", "jail", []string{"yolo config set setup podman-self --user", "leave it unset"}},
		{"confinement", "guest", []string{"yolo config set setup seatbelt-yolo-account --user", "seatbelt-self"}},
		{"confinement", "host", []string{"yolo host -- <cmd>"}},
		{"macos_user_mode", "self", []string{"yolo config set setup seatbelt-self --user"}},
		{"macos_user_mode", "account", []string{"yolo config set setup seatbelt-yolo-account --user"}},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			errs, _ := retiredSetupLines(t, `{"`+tc.key+`": "`+tc.value+`"}`, t.TempDir())
			got := lineFor(errs, tc.key)
			if got == "" {
				t.Fatalf("%s = %q was not refused on the host: %q", tc.key, tc.value, errs)
			}
			for _, want := range append(tc.want, "REMOVED", "yolo config unset "+tc.key+" --user") {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "--at") {
				t.Errorf("the refusal names the removed --at:\n%s", got)
			}
		})
	}
}

// A project file holding a removed key is refused naming --project: a project cannot choose
// where its agent runs, so the step is to remove the line, never to move it.
func TestARemovedSpellingInAProjectNamesTheProjectUnset(t *testing.T) {
	hostScope(t)
	t.Setenv("HOME", t.TempDir())
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceConfigName), `{"runtime": "podman"}`)
	errs, _ := retiredSetupLines(t, `{"runtime": "podman"}`, ws)
	got := lineFor(errs, "runtime")
	for _, want := range []string{"A project cannot choose where its agent runs", "yolo config unset runtime --project"} {
		if !strings.Contains(got, want) {
			t.Errorf("the project refusal does not say %q:\n%s", want, got)
		}
	}
}

// Inside a jail the config is the host's snapshot: the same line is a warning, and nothing fails.
func TestARemovedSpellingWarnsInsideAJail(t *testing.T) {
	jailScope(t)
	t.Setenv("HOME", t.TempDir())
	errs, warns := retiredSetupLines(t, `{"confinement": "guest", "macos_user_mode": "self"}`, t.TempDir())
	if lineFor(errs, "confinement") != "" || lineFor(errs, "macos_user_mode") != "" {
		t.Errorf("in a jail a removed key was an error: %q", errs)
	}
	for _, key := range []string{"confinement", "macos_user_mode"} {
		if w := lineFor(warns, key); !strings.Contains(w, "remove the key from the HOST config") {
			t.Errorf("in a jail %s did not warn naming the host config: %q", key, warns)
		}
	}
}

// What migrate writes for each value: one setup, the dropped default, or nothing.
func TestSetupForRemovedKeyNamesOneSetupOrNone(t *testing.T) {
	for _, tc := range []struct {
		key, value, want string
		dropped          bool
	}{
		{"runtime", "podman", "podman-self", false},
		{"runtime", "container", "apple-container-self", false},
		{"runtime", "macos-user", "", false},
		{"confinement", "jail", "", true},
		{"confinement", "guest", "", false},
		{"macos_user_mode", "self", "", false},
	} {
		if got := SetupForRemovedKey(tc.key, tc.value); got != tc.want {
			t.Errorf("SetupForRemovedKey(%s, %s) = %q, want %q", tc.key, tc.value, got, tc.want)
		}
		if got := RemovedConfinementIsDefault(tc.key, tc.value); got != tc.dropped {
			t.Errorf("RemovedConfinementIsDefault(%s, %s) = %v", tc.key, tc.value, got)
		}
	}
}

// TestARemovedSpellingInATOMLFileIsReadAndRefusedByItsValue: v0.16.1 wrote these keys to TOML
// (`yolo config set runtime …`, and its migrate copied them verbatim), so a TOML file holding one
// is read, not refused by the generic TOML refusal, and the refusal is the value's own, as a
// JSONC file's is (SUP-D17): `confinement = "host"` is told `yolo host`, never podman-self. A
// user file's value migrate converts names `yolo config migrate`; a project's names --project
// and a local file's --local.
func TestARemovedSpellingInATOMLFileIsReadAndRefusedByItsValue(t *testing.T) {
	for _, tc := range []struct {
		name, file, body, key string
		want, wantNot         []string
	}{
		{"user runtime podman", "user", "runtime = \"podman\"\n", "runtime",
			[]string{"yolo config set setup podman-self --user", "yolo config unset runtime --user", "yolo config migrate"}, nil},
		{"user confinement host", "user", "confinement = \"host\"\n", "confinement",
			[]string{"yolo host -- <cmd>", "yolo config unset confinement --user"}, []string{"podman-self", "yolo config migrate"}},
		{"user macos_user_mode", "user", "macos_user_mode = \"self\"\n", "macos_user_mode",
			[]string{"yolo config set setup seatbelt-self --user", "yolo config unset macos_user_mode --user"}, nil},
		{"project runtime", WorkspaceTOMLConfigName, "runtime = \"podman\"\n", "runtime",
			[]string{"A project cannot choose where its agent runs", "yolo config unset runtime --project"}, []string{"yolo config migrate"}},
		{"local confinement", WorkspaceLocalTOMLConfigName, "confinement = \"guest\"\n", "confinement",
			[]string{"seatbelt-yolo-account", "yolo config unset confinement --local"}, []string{"--project", "podman-self"}},
		{"local JSONC", WorkspaceLocalLegacyConfigName, `{"runtime": "podman"}`, "runtime",
			[]string{"yolo config unset runtime --local"}, []string{"--project"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := setupsHome(t)
			ws := t.TempDir()
			if tc.file == "user" {
				userTOMLWith(t, home, tc.body)
			} else {
				write(t, filepath.Join(ws, tc.file), tc.body)
			}
			cfg, err := LoadConfig(ws, true, nil)
			if err != nil {
				t.Fatalf("a file holding %s was not read: %v", tc.key, err)
			}
			errs, _ := ValidateConfig(cfg, ws, nil)
			got := lineFor(errs, tc.key)
			if got == "" {
				t.Fatalf("%s was not refused: %q", tc.key, errs)
			}
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("the refusal does not say %q:\n%s", want, got)
				}
			}
			for _, not := range tc.wantNot {
				if strings.Contains(got, not) {
					t.Errorf("the refusal says %q:\n%s", not, got)
				}
			}
		})
	}
}

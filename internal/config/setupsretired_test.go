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

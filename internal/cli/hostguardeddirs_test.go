package cli

// hostguardeddirs_test.go pins what claude's guarded posture leaves alone in the user's real
// ~/.claude/settings.json: their own `permissions.additionalDirectories`.
//
// The guarded posture is the half of a pack's `autonomy` contribution that renders when nothing
// confines the agent, which is the host (docs/reference/pack-system.md#autonomy). Its job, as
// ruled, is to keep the agent's permission prompts on there: claude's guarded block is "prompts
// on; NO allow/deny clobber" (docs/plans/environment-manager-plan.md §9.0 and step 9.4).
//
// THE DEFECT. The shipped block also put `permissions.additionalDirectories: []` in the managed
// layer, to take out the jail's whole-filesystem `/` grant. A managed array replaces the file's
// array whole (RFC 7386), and the managed layer outranked the user's file under the retired
// `assert` and outranks their captured edits under `own`. So every `yolo host apply` emptied the
// directories the user had granted claude on their own machine, re-added ones included, and the
// report could only say "no remedy". No published version ever wrote `/` into a host file: host
// apply shipped first in v0.8.0, which already had the guarded posture, so the empty list removed
// only the user's own.
//
// Every test drives applyHost, the `yolo host apply` verb, over the shipped claude pack in a
// temp HOME. Since the `assert` retirement (OQ-CO14) `own` is the one contract that writes:
// claude's settings surface declares no `rmw`, so `own` composes it whole and the rmw arm
// `assert` ran it through has no path to this file any more.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// guardedDirsHome is a temp home selecting claude under the given `host_management` value ("" is
// the unset key), whose settings.json holds the user's own permissions.
func guardedDirsHome(t *testing.T, mode, settings string) (home, path string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	selectPacks(t, home, `"claude"`)
	if mode != "" {
		writeFile(t, filepath.Join(home, ".config", "yolo-jail", "config.jsonc"),
			`{"packs":["claude"],"host_management":"`+mode+`"}`)
	}
	path = filepath.Join(home, ".claude", "settings.json")
	writeFile(t, path, settings)
	return home, path
}

// claudePermissions reads `permissions` out of the real home's claude settings file.
func claudePermissions(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s is not JSON: %v\n%s", path, err, data)
	}
	perms, _ := m["permissions"].(map[string]any)
	if perms == nil {
		t.Fatalf("%s has no permissions object:\n%s", path, data)
	}
	return perms
}

// THE USER'S LIST SURVIVES EVERY APPLY, under the contract that writes and under the ones that
// do not. Applied twice, because the second apply is the one that met a list the first had
// emptied, and under `own` it is the one whose capture step reads the file back. Under `none`
// and the unset key (which is `none` since OQ-CO14) the apply writes nothing at all, so the
// user's file — their list in it — is left byte for byte.
func TestHostApplyKeepsTheUsersOwnAdditionalDirectories(t *testing.T) {
	const users = `{"permissions":{"additionalDirectories":["/home/me/notes","/srv/shared"],` +
		`"allow":["Bash(ls:*)"]}}`
	for _, mode := range []string{"", "none"} {
		name := mode
		if name == "" {
			name = "unset"
		}
		t.Run(name, func(t *testing.T) {
			_, path := guardedDirsHome(t, mode, users)
			for apply := 1; apply <= 2; apply++ {
				if rc, report := applyWith(t, true, nil); rc != 0 {
					t.Fatalf("apply %d: host apply --assert rc=%d\n%s", apply, rc, report)
				}
				if got, err := os.ReadFile(path); err != nil || string(got) != users {
					t.Errorf("apply %d under %s rewrote the user's settings (%v):\n%s", apply, name, err, got)
				}
			}
		})
	}
	t.Run("own", func(t *testing.T) {
		_, path := guardedDirsHome(t, "own", users)
		want := []any{"/home/me/notes", "/srv/shared"}
		for apply := 1; apply <= 2; apply++ {
			rc, report := applyWith(t, true, nil)
			if rc != 0 {
				t.Fatalf("apply %d: host apply --assert rc=%d\n%s", apply, rc, report)
			}
			perms := claudePermissions(t, path)
			if got := perms["additionalDirectories"]; !reflect.DeepEqual(got, want) {
				t.Errorf("apply %d: permissions.additionalDirectories = %#v, want the user's "+
					"own %#v. The guarded posture keeps prompts on; it does not own the "+
					"directories a user granted claude on their own machine.", apply, got, want)
			}
			if strings.Contains(report, "permissions.additionalDirectories") {
				t.Errorf("apply %d: the report names the user's additionalDirectories as "+
					"replaced:\n%s", apply, report)
			}
			// The posture is still in force: the prompts-on value is written, and the
			// user's own allow list is untouched, as it always was.
			if perms["defaultMode"] != "default" {
				t.Errorf("apply %d: permissions.defaultMode = %v, want the guarded \"default\"",
					apply, perms["defaultMode"])
			}
			if got := perms["allow"]; !reflect.DeepEqual(got, []any{"Bash(ls:*)"}) {
				t.Errorf("apply %d: permissions.allow = %#v, want the user's own", apply, got)
			}
		}
	})
}

// A DIRECTORY ADDED AFTER AN APPLY STAYS TOO. This is the "every time" half: before the fix, a
// user who re-added a directory by hand lost it again at the next apply, under `own` because the
// managed layer outranks the capture that recorded the edit (and under the retired `assert`
// because rmw rewrote every managed key). `own` is the one contract left that writes the file.
func TestHostApplyKeepsADirectoryAddedAfterAnApply(t *testing.T) {
	t.Run("own", func(t *testing.T) {
		_, path := guardedDirsHome(t, "own", `{"theme":"dark"}`)
		if rc, report := applyWith(t, true, nil); rc != 0 {
			t.Fatalf("first apply rc=%d\n%s", rc, report)
		}
		// The user edits the file yolo just wrote, the way claude's own /add-dir does.
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("settings is not JSON: %v\n%s", err, data)
		}
		perms, _ := m["permissions"].(map[string]any)
		if perms == nil {
			perms = map[string]any{}
			m["permissions"] = perms
		}
		perms["additionalDirectories"] = []any{"/home/me/later"}
		edited, err := json.MarshalIndent(m, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, path, string(edited))

		if rc, report := applyWith(t, true, nil); rc != 0 {
			t.Fatalf("second apply rc=%d\n%s", rc, report)
		}
		if got := claudePermissions(t, path)["additionalDirectories"]; !reflect.DeepEqual(got,
			[]any{"/home/me/later"}) {
			t.Errorf("permissions.additionalDirectories = %#v after the next apply, want the "+
				"directory the user added", got)
		}
	})
}

package configmigrate

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAWorkspaceSwitchUnderATOMLUserWritesTheCeilingLine: a legacy workspace `enabled: true` is
// the request and its admission (docs/reference/loophole-grants.md LG-D16), so under a user
// config already in TOML its kind is in the launch's ceiling with no line written. Converting the
// workspace writes `[requests.<pack>]`, and the user ceiling's `on_request` line the request
// needs into the TOML user file, in place and byte for byte elsewhere; without it the converted
// workspace would hold less. A kind the user's ceiling already lists gains nothing.
func TestAWorkspaceSwitchUnderATOMLUserWritesTheCeilingLine(t *testing.T) {
	for _, c := range []struct {
		name, user, want string
		edits            bool
	}{
		{"no table", "# mine\npacks = [\"audio\", \"serial\"]\n",
			"# mine\npacks = [\"audio\", \"serial\"]\n\n[allow.audio]\non_request = [\"use\"]\n\n[allow.serial]\non_request = [\"devices\"]\n", true},
		{"a table with a setting", "packs = [\"audio\", \"serial\"]\n\n[allow.serial]\ndefault_baud = 9600\n",
			"[allow.serial]\ndefault_baud = 9600\non_request = [\"devices\"]\n", true},
		{"already listed", "packs = [\"audio\", \"serial\"]\n\n[allow.audio]\non_request = [\"use\"]\n\n[allow.serial]\non_request = [\"devices\"]\n", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			home := env(t)
			dir := filepath.Join(home, ".config", "yolo-jail")
			ws := realDir(t)
			write(t, filepath.Join(dir, "config.toml"), c.user)
			write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"audio": {"enabled": true}, "serial": {"enabled": true}}}`)
			p := plan(t, Request{UserConfigDir: dir, Workspace: ws})
			if !p.Equal || len(p.NotConverted) != 0 {
				t.Fatalf("the proof did not pass: equal=%v not converted=%+v differences=%+v", p.Equal, p.NotConverted, p.Differences)
			}
			userPath := filepath.Join(dir, "config.toml")
			var userTarget *Target
			for i := range p.Targets {
				if p.Targets[i].Old == userPath {
					userTarget = &p.Targets[i]
				}
			}
			if !c.edits {
				if userTarget != nil {
					t.Fatalf("the user ceiling already lists both kinds, and the plan rewrites it:\n%s", userTarget.Bytes)
				}
				return
			}
			if userTarget == nil || userTarget.New != userPath {
				t.Fatalf("the plan does not rewrite the TOML user config in place: %+v", p.Targets)
			}
			if got := string(userTarget.Bytes); !strings.Contains(got, c.want) {
				t.Errorf("the user config is\n%s\nwant it to hold\n%s", got, c.want)
			}
			if !strings.Contains(p.Transcript(), "on_request") {
				t.Errorf("the transcript does not show the ceiling line:\n%s", p.Transcript())
			}
			if _, err := Apply(p, ApplyOptions{}); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(userPath)
			if err != nil || string(data) != string(userTarget.Bytes) {
				t.Errorf("after Apply the user config is %q (%v), want the planned bytes", data, err)
			}
		})
	}
}

// TestAUserOnRequestListIsNeverRewritten: an `on_request` the user wrote is the user's answer,
// and an empty one means no project may ask (docs/reference/loophole-grants.md LG-D14, "a
// user's own on_request list replaces the default"). Converting a workspace whose legacy switch
// the launch admits for itself therefore writes nothing into that list; the converted workspace
// would hold less, so the proof refuses, naming the user's own command that admits it, and nothing
// is written.
func TestAUserOnRequestListIsNeverRewritten(t *testing.T) {
	home := env(t)
	dir := filepath.Join(home, ".config", "yolo-jail")
	ws := realDir(t)
	const user = "packs = [\"serial\"]\n\n[allow.serial]\non_request = []\n"
	write(t, filepath.Join(dir, "config.toml"), user)
	write(t, filepath.Join(ws, "yolo-jail.jsonc"), `{"loopholes": {"serial": {"enabled": true}}}`)
	g, err := Discover(Request{UserConfigDir: dir, Workspace: ws})
	if err != nil {
		t.Fatal(err)
	}
	p, err := Plan(g)
	var ineq *InequalityError
	if !errors.As(err, &ineq) {
		t.Fatalf("the proof passes (or fails otherwise: %v), though the converted workspace's serial request is no longer admitted", err)
	}
	userPath := filepath.Join(dir, "config.toml")
	if p != nil {
		for _, tg := range p.Targets {
			if tg.Old == userPath && string(tg.Bytes) != user {
				t.Fatalf("the plan rewrites the user's own on_request list:\n%s", tg.Bytes)
			}
		}
	}
	if !strings.Contains(err.Error(), "yolo allow serial on_request devices") {
		t.Errorf("the refusal does not name the user's command that admits the request:\n%v", err)
	}
	if data, _ := os.ReadFile(userPath); string(data) != user {
		t.Errorf("the user config changed: %q", data)
	}
}

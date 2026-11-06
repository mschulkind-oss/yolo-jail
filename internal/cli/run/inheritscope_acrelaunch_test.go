package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// An Apple Container launch COPIES the generated user config into the jail's home (the whole
// HOME is one bind), so the copy is still there at the next launch. The overwrite refusal is
// there for a file the user put at that path; it refused the previous launch's own unchanged
// copy too, so no workspace with an inherited user scope could relaunch on Apple Container
// (the Mac run of 2026-11-06, §2: TestOfferAnsweredNoLaunchesOnTheOldFiles and
// TestConvertLaterSilencesTheOffer). The user config's format was not the trigger: both
// formats deliver TOML and both refused.

// acRelaunchFixture writes a user config of the given file name under a fresh HOME and returns
// a workspace and the Apple Container home it launches into.
func acRelaunchFixture(t *testing.T, name, body string) (home, workspace, wsState string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("HOME", home)
	cfgDir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	wsState = filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(wsState, 0o755); err != nil {
		t.Fatal(err)
	}
	return home, t.TempDir(), wsState
}

// acLaunch assembles one Apple Container launch at the given clock, returning its assembly error.
func acLaunch(t *testing.T, workspace, home, wsState string, at time.Time) error {
	t.Helper()
	o := goldenOptions(workspace, home)
	o.Now = func() time.Time { return at }
	in := relocationInput(t, "container", wsState, nil)
	_ = o.assembleRunCmd(in)
	return in.assemblyErr
}

func TestAppleContainerRelaunchReplacesItsOwnPreviousUserConfigCopy(t *testing.T) {
	first := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	second := first.Add(time.Hour)
	for _, tc := range []struct{ name, file, body string }{
		{name: "legacy JSONC user config", file: "config.jsonc", body: `{"packs": ["claude"]}`},
		{name: "TOML user config", file: "config.toml", body: "packs = [\"claude\"]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, workspace, wsState := acRelaunchFixture(t, tc.file, tc.body)
			if err := acLaunch(t, workspace, home, wsState, first); err != nil {
				t.Fatalf("first launch: %v", err)
			}
			dest := filepath.Join(wsState, filepath.FromSlash(inheritPreflightRel))
			before, err := os.ReadFile(dest)
			if err != nil {
				t.Fatalf("first launch left no copy at %s: %v", dest, err)
			}
			if err := acLaunch(t, workspace, home, wsState, second); err != nil {
				t.Fatalf("relaunch refused the previous launch's own copy: %v", err)
			}
			after, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) == string(before) || !strings.Contains(string(after), second.Format("2006-01-02T15:04:05Z")) {
				t.Errorf("relaunch did not deliver its own user config over the previous copy:\n%s", after)
			}
			// And a third: the relaunch's copy is as much yolo's own as the first one was.
			if err := acLaunch(t, workspace, home, wsState, second.Add(time.Hour)); err != nil {
				t.Fatalf("second relaunch: %v", err)
			}
		})
	}
}

func TestAppleContainerRelaunchStillRefusesAnEditedCopy(t *testing.T) {
	first := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	home, workspace, wsState := acRelaunchFixture(t, "config.jsonc", `{"packs": ["claude"]}`)
	if err := acLaunch(t, workspace, home, wsState, first); err != nil {
		t.Fatalf("first launch: %v", err)
	}
	dest := filepath.Join(wsState, filepath.FromSlash(inheritPreflightRel))
	edited := []byte("# the user's own line\npacks = [\"codex\"]\n")
	if err := os.WriteFile(dest, edited, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		t.Fatal(err)
	}
	err := acLaunch(t, workspace, home, wsState, first.Add(time.Hour))
	if err == nil || !strings.Contains(err.Error(), dest) || !strings.Contains(err.Error(), "in the way") {
		t.Fatalf("an edited copy was not refused by name: %v", err)
	}
	if got, _ := os.ReadFile(dest); string(got) != string(edited) {
		t.Errorf("the refusal changed the user's bytes: %q", got)
	}
	if info, err := os.Stat(dest); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("the refusal changed the user's mode: %v %v", info, err)
	}
}

// A hard link to the copy shares its data with another name, so it is not yolo's alone to
// replace even when its bytes match.
func TestAppleContainerRelaunchRefusesAHardLinkedCopy(t *testing.T) {
	first := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	home, workspace, wsState := acRelaunchFixture(t, "config.jsonc", `{"packs": ["claude"]}`)
	if err := acLaunch(t, workspace, home, wsState, first); err != nil {
		t.Fatalf("first launch: %v", err)
	}
	dest := filepath.Join(wsState, filepath.FromSlash(inheritPreflightRel))
	if err := os.Link(dest, filepath.Join(wsState, "kept.toml")); err != nil {
		t.Fatal(err)
	}
	if err := acLaunch(t, workspace, home, wsState, first.Add(time.Hour)); err == nil {
		t.Fatal("a hard-linked copy was replaced")
	}
}

// A previous launch's copy in the OTHER format (a legacy JSONC delivery, before the scope crossed
// as TOML) is removed when unchanged, so the jail does not see both formats; edited, it is refused.
func TestAppleContainerRelaunchClearsItsOwnCopyOfTheOtherFormat(t *testing.T) {
	first := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	for _, edited := range []bool{false, true} {
		name := "unchanged"
		if edited {
			name = "edited"
		}
		t.Run(name, func(t *testing.T) {
			home, workspace, wsState := acRelaunchFixture(t, "config.jsonc", `{"packs": ["claude"]}`)
			old := []byte("// generated by yolo\n{\"packs\": [\"claude\"]}\n")
			legacy := filepath.Join(wsState, filepath.FromSlash(inheritPreflightLegacyRel))
			if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(wsState, "inherit-preflight.jsonc"), old, 0o644); err != nil {
				t.Fatal(err)
			}
			atDest := old
			if edited {
				atDest = append([]byte("// mine\n"), old...)
			}
			if err := os.WriteFile(legacy, atDest, 0o644); err != nil {
				t.Fatal(err)
			}
			err := acLaunch(t, workspace, home, wsState, first)
			if edited {
				if err == nil || !strings.Contains(err.Error(), legacy) {
					t.Fatalf("an edited legacy copy was not refused by name: %v", err)
				}
				if got, _ := os.ReadFile(legacy); string(got) != string(atDest) {
					t.Errorf("the refusal changed the user's bytes: %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("relaunch refused the previous launch's own legacy copy: %v", err)
			}
			if _, err := os.Lstat(legacy); !os.IsNotExist(err) {
				t.Errorf("the previous launch's own legacy copy stayed beside the TOML delivery: %v", err)
			}
			if _, err := os.Stat(filepath.Join(wsState, filepath.FromSlash(inheritPreflightRel))); err != nil {
				t.Errorf("no TOML delivered: %v", err)
			}
		})
	}
}

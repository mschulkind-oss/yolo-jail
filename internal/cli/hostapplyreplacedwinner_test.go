package cli

// hostapplyreplacedwinner_test.go pins the replaced-value report to its REAL WINNER, at the
// command. Measured on the maintainer's host 2026-09-28 under `host_management: own`: every
// apply printed
//
//	⚠ overwrote your existing value for: hooks.Notification (config-overlay from matt)
//	1 of your values were replaced in 1 file: hooks.Notification (config-overlay from matt)
//	  no remedy: … a config-overlay folds BELOW the managed layer, which still wins …
//
// while settings.json kept the user's own hook — the captured edit outranks every
// config-overlay — and the counts said nothing changed. Three false statements: the overlay
// was not the writer, nothing was overwritten, and the overlay (in the user's own pack) is the
// remedy, not a layer that cannot win.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const notificationPackJSON = `{"name":"mine","description":"d","contributes":[
  {"kind":"config-overlay","surface":"claude/settings",
   "config":{"managed":{"hooks":{"Notification":[{"matcher":"idle_prompt",
     "hooks":[{"type":"command","command":"printf bell"}]}]}}}}]}`

const usersSettings = `{"theme":"dark","hooks":{"Notification":[{"matcher":"",` +
	`"hooks":[{"type":"command","command":"printf '\\a' > /dev/tty"}]}]}}` + "\n"

// notificationFixture is claude plus a user pack overlaying hooks.Notification, over a home
// whose settings.json already holds the user's own hook. Returns the home and the pack dir.
func notificationFixture(t *testing.T, management string) (string, string) {
	t.Helper()
	home := t.TempDir()
	packDir := filepath.Join(home, "packs", "mine")
	writeFile(t, filepath.Join(packDir, "pack.json"), notificationPackJSON)
	selectPacksWith(t, home, `"claude",{"source":"file://`+packDir+`","name":"mine"}`,
		`,"host_management":"`+management+`"`)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("YOLO_VERSION", "")
	writeFile(t, hostSettingsPath(home), usersSettings)
	return home, packDir
}

func TestUnderOwnTheReportSaysTheCapturedEditIsKeptOverTheOverlay(t *testing.T) {
	home, _ := notificationFixture(t, "own")
	for run := 1; run <= 2; run++ {
		rc, report := applyWith(t, true, nil)
		if rc != 0 {
			t.Fatalf("run %d: rc=%d\n%s", run, rc, report)
		}
		data, err := os.ReadFile(hostSettingsPath(home))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), `printf '\\a' > /dev/tty`) {
			t.Fatalf("run %d: fixture premise — the captured edit outranks the overlay, so the "+
				"user's hook stays:\n%s", run, data)
		}
		for _, never := range []string{"overwrote", "were replaced", "no remedy",
			"folds BELOW the managed layer"} {
			if strings.Contains(report, never) {
				t.Errorf("run %d: the report says %q about a value the write kept:\n%s", run,
					never, report)
			}
		}
		if !strings.Contains(report, "hooks.Notification: your own edit is kept over mine's config-overlay") ||
			!strings.Contains(report, "set hooks.Notification in ~/.claude/settings.json to it") {
			t.Errorf("run %d: the report does not name the kept value and the way to take the "+
				"pack's:\n%s", run, report)
		}
		if run == 2 {
			if strings.Contains(report, "claude/settings      rendered") {
				t.Errorf("run 2: claude/settings is listed as rendered over a file the apply "+
					"did not change:\n%s", report)
			}
			if !strings.Contains(report, "Nothing to apply — this home is up to date.") {
				t.Errorf("run 2: the verdict disagrees with a settled home:\n%s", report)
			}
		}
	}
}

func TestUnderAssertAnOverlayReplacementNamesTheOverlayAndItsPackAsTheRemedy(t *testing.T) {
	home, packDir := notificationFixture(t, "assert")
	rc, report := applyWith(t, true, nil)
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, report)
	}
	for _, want := range []string{
		"1 value of yours was replaced by mine's config-overlay: hooks.Notification in ~/.claude/settings.json",
		"remove that key from the `config-overlay` for claude/settings in " +
			prettyHomePath(home, filepath.Join(packDir, "pack.json")),
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report does not say %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "no remedy") || strings.Contains(report, "overwrote your existing value") {
		t.Errorf("the replaced value is stated with a false no-remedy, or twice:\n%s", report)
	}
}

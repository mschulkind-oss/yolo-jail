package cli

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

// setupsHome points HOME and the cwd at empty temp trees, on the host side (YOLO_VERSION
// blank), with no YOLO_SETUP, on Linux, with every technology "installed", so nothing here
// reads or writes the invoking user's real config or depends on the machine.
func setupsHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("YOLO_VERSION", "")
	t.Setenv(setup.EnvVar, "")
	t.Chdir(t.TempDir())
	prevGOOS, prevHas := setupGOOS, setupHasBinary
	setupGOOS, setupHasBinary = "linux", func(string) bool { return true }
	t.Cleanup(func() { setupGOOS, setupHasBinary = prevGOOS, prevHas })
	return home
}

func userConfigWith(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, ".config", "yolo-jail", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runSetupCapture(args ...string) (int, string, string) {
	var out, errw bytes.Buffer
	rc := setupMain(args, &out, &errw, false)
	return rc, out.String(), errw.String()
}

func TestSetupShowPrintsTheBuiltInAsATable(t *testing.T) {
	setupsHome(t)
	var out bytes.Buffer
	if rc := setupMain([]string{"show", "podman-self"}, &out, io.Discard, false); rc != 0 {
		t.Fatal(rc)
	}
	want := "[setups.podman-self]\nconfinement = \"podman\"\nrun_as = \"self\"\n"
	if out.String() != want {
		t.Fatalf("got %q", out.String())
	}
}

func TestSetupShowPrintsAUserSetupWithBothFields(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, "[setups.mac-as-me]\nconfinement = \"seatbelt\"\n")
	rc, out, errw := runSetupCapture("show", "mac-as-me")
	if rc != 0 || !strings.HasSuffix(out, "[setups.mac-as-me]\nconfinement = \"seatbelt\"\nrun_as = \"self\"\n") ||
		!strings.Contains(out, "# yours, in ") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errw)
	}
}

func TestSetupShowRefusesAnUnknownNameNamingTheList(t *testing.T) {
	setupsHome(t)
	rc, out, errw := runSetupCapture("show", "fast")
	if rc == 0 || out != "" || !strings.Contains(errw, `setup "fast" is not defined`) || !strings.Contains(errw, "yolo setup list") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errw)
	}
	rc, _, errw = runSetupCapture("show", "Not_A_Name")
	if rc == 0 || !strings.Contains(errw, "is not a setup name") || !strings.Contains(errw, "yolo setup list") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
}

// A table that does not decode is not "not defined": show names its own refusal.
func TestSetupShowNamesABrokenTablesRefusal(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, "[setups.lab]\nconfinement = \"seatbelt\"\npolicy = \"allow\"\n")
	rc, _, errw := runSetupCapture("show", "lab")
	if rc == 0 || !strings.Contains(errw, "`policy` is not a setup field") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
}

func TestSetupListMarksTheSelectionAndItsSource(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, `setup = "podman-self"`+"\n[setups.mac-as-me]\nconfinement = \"seatbelt\"\n")
	var out bytes.Buffer
	setupMain([]string{"list"}, &out, io.Discard, false)
	for _, want := range []string{"podman-self", "built-in, your default", "mac-as-me", "yours", "Seatbelt, as you",
		"Selected: podman-self, from your user config."} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("missing %q in\n%s", want, out.String())
		}
	}
	for _, b := range setup.BuiltIns() {
		if !strings.Contains(out.String(), b.Name) {
			t.Errorf("list leaves out the built-in %s:\n%s", b.Name, out.String())
		}
	}
}

func TestSetupListWithNothingChosenNamesTheDefaultAndHowToChoose(t *testing.T) {
	setupsHome(t)
	rc, out, errw := runSetupCapture("list")
	if rc != 0 || errw != "" {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
	for _, want := range []string{"built-in, this OS's default", "Selected: podman-self, this OS's default.",
		"yolo config set setup <name> --user", "seatbelt-self", "not available here"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestSetupListHonorsYOLOSETUPOverTheUserConfig(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, `setup = "podman-self"`+"\n[setups.lab]\nconfinement = \"podman\"\n")
	t.Setenv(setup.EnvVar, "lab")
	rc, out, _ := runSetupCapture("list")
	if rc != 0 || !strings.Contains(out, "Selected: lab, from YOLO_SETUP.") || !strings.Contains(out, "yours, chosen by YOLO_SETUP") {
		t.Fatalf("rc=%d out=\n%s", rc, out)
	}
}

func TestSetupListSaysWhenTheSelectionCannotRun(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, `setup = "seatbelt-self"`+"\n")
	rc, _, errw := runSetupCapture("list")
	if rc == 0 || !strings.Contains(errw, "which only macOS has") || !strings.Contains(errw, "yolo --setup podman-self") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
	setupHasBinary = func(string) bool { return false }
	userConfigWith(t, home, "")
	rc, _, errw = runSetupCapture("list")
	if rc == 0 || !strings.Contains(errw, "The default setup on this machine, podman-self, needs Podman") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
}

func TestSetupNewCopiesTheSourceIntoTheUserConfig(t *testing.T) {
	home := setupsHome(t)
	path := userConfigWith(t, home, "")
	if rc := setupMain([]string{"new", "lab", "--from", "seatbelt-yolo-account"}, io.Discard, io.Discard, false); rc != 0 {
		t.Fatal(rc)
	}
	_, user, _ := config.SelectedSetupFromConfig()
	if s := user["lab"]; s.Confinement != setup.ConfinementSeatbelt || s.RunAs != setup.RunAsYoloAccount {
		t.Fatalf("%+v", s)
	}
	if got, _ := os.ReadFile(path); string(got) != "[setups.lab]\nconfinement = \"seatbelt\"\nrun_as = \"yolo-account\"\n" {
		t.Fatalf("user config is %q", got)
	}
	if rc := setupMain([]string{"new", "podman-self", "--from", "podman-self"}, io.Discard, io.Discard, false); rc == 0 {
		t.Fatal("a built-in name is refused")
	}
}

func TestSetupNewCopiesAUserSetupIntoALegacyUserConfig(t *testing.T) {
	home := setupsHome(t)
	path := filepath.Join(home, ".config", "yolo-jail", "config.jsonc")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"setups": {"mine": {"confinement": "apple-container"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rc, out, errw := runSetupCapture("new", "copy", "--from=mine")
	if rc != 0 || !strings.Contains(out, "yolo config set setup copy --user") {
		t.Fatalf("rc=%d out=%q err=%q", rc, out, errw)
	}
	_, user, _ := config.SelectedSetupFromConfig()
	if s := user["copy"]; s.Confinement != setup.ConfinementAppleContainer || s.RunAs != setup.RunAsSelf {
		t.Fatalf("%+v", s)
	}
}

func TestSetupNewRefusesEachBadAskWithItsNextStep(t *testing.T) {
	home := setupsHome(t)
	path := userConfigWith(t, home, "[setups.lab]\nconfinement = \"podman\"\n")
	before, _ := os.ReadFile(path)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"new", "podman-self", "--from", "podman-self"}, []string{"[setups.podman-self] redefines a built-in setup", "yolo setup new my-podman --from podman-self"}},
		{[]string{"new", "Lab_2", "--from", "podman-self"}, []string{"is not a setup name", "yolo setup new lab-2 --from podman-self"}},
		{[]string{"new", "fresh"}, []string{"--from", "yolo setup new fresh --from podman-self"}},
		{[]string{"new", "fresh", "--from"}, []string{"--from needs the setup to copy"}},
		{[]string{"new"}, []string{"yolo setup new lab --from seatbelt-self"}},
		{[]string{"new", "lab", "--from", "podman-self"}, []string{`setup "lab" is already yours`, "yolo config unset setups.lab --user"}},
		{[]string{"new", "fresh", "--from", "fast"}, []string{`setup "fast" is not defined`, "yolo setup list"}},
		{[]string{"new", "fresh", "--from", "podman-self", "--force"}, []string{`unknown flag "--force"`}},
	} {
		rc, _, errw := runSetupCapture(tc.args...)
		if rc == 0 {
			t.Errorf("%v: rc=0, want a refusal", tc.args)
		}
		for _, w := range tc.want {
			if !strings.Contains(errw, w) {
				t.Errorf("%v: missing %q in %q", tc.args, w, errw)
			}
		}
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(after, before) {
		t.Fatalf("a refused `setup new` wrote the user config: %q", after)
	}
}

// In a jail the user config is the host's to write: the refusal names the typed command, run
// on the host, and nothing is written.
func TestSetupNewInAJailNamesTheHostRun(t *testing.T) {
	home := setupsHome(t)
	path := userConfigWith(t, home, "")
	t.Setenv("YOLO_VERSION", "test")
	rc, _, errw := runSetupCapture("new", "lab", "--from", "podman-self")
	if rc == 0 || !strings.Contains(errw, "yolo host-run -C") || !strings.Contains(errw, "-- yolo setup new lab --from podman-self") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
	if got, _ := os.ReadFile(path); len(got) != 0 {
		t.Fatalf("wrote %q", got)
	}
}

func TestSetupRefusesAnUnknownVerbAndNoVerb(t *testing.T) {
	setupsHome(t)
	if rc, _, errw := runSetupCapture("bogus"); rc != 2 || !strings.Contains(errw, "yolo setup --help") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
	if rc, _, errw := runSetupCapture(); rc != 2 || !strings.Contains(errw, "list, show or new") {
		t.Fatalf("rc=%d err=%q", rc, errw)
	}
}

// The front door routes `yolo setup` to this command: deleting the registry row fails here.
func TestSetupIsRoutedByTheFrontDoor(t *testing.T) {
	if _, ok := registry["setup"]; !ok {
		t.Fatal("`setup` is not a registered command")
	}
	if got := Subcommand([]string{"setup", "list"}); got != "setup" {
		t.Fatalf("Subcommand = %q", got)
	}
}

// TestSetupListMakesTheLaunchsSelection: `yolo setup list` selects through config.SelectSetup,
// the call a launch makes, so it weighs the removed inputs as a launch does — a removed
// YOLO_RUNTIME on the host is refused naming YOLO_SETUP, and a workspace's removed `runtime` key
// still chooses its built-in, named as the source. Fails if setupList selects any other way.
func TestSetupListMakesTheLaunchsSelection(t *testing.T) {
	setupsHome(t)
	t.Setenv(config.RemovedRuntimeEnv, "podman")
	rc, _, errw := runSetupCapture("list")
	if rc != 1 || !strings.Contains(errw, "No setup is selected: ") || !strings.Contains(errw, "YOLO_SETUP") {
		t.Fatalf("a removed YOLO_RUNTIME: rc=%d err=%q, want 1 and the refusal naming YOLO_SETUP", rc, errw)
	}
	t.Setenv(config.RemovedRuntimeEnv, "")
	writeFile(t, "yolo-jail.jsonc", `{"runtime": "container"}`)
	setupGOOS = "darwin"
	rc, out, errw := runSetupCapture("list")
	if rc != 0 || !strings.Contains(out, `Selected: apple-container-self, from runtime = "container" in your config.`) {
		t.Fatalf("a workspace `runtime` key: rc=%d err=%q out=\n%s", rc, errw, out)
	}
}

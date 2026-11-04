package config

// The config half of named setups (docs/design/named-setups.md, plan slice 2): `setup` chooses
// your default and `[setups.<name>]` defines one, both in your user config alone (OQ-MT5,
// design §8). Each test fails if its call site is deleted: the reader, the validator's wiring
// in ValidateConfig, and the readable walk's per-key advice.

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/setup"
)

func setupsHome(t *testing.T) string {
	t.Helper()
	t.Setenv("YOLO_VERSION", "")
	return programsHome(t)
}

func userTOMLWith(t *testing.T, home, body string) {
	t.Helper()
	writeConfigAt(t, filepath.Join(home, ".config", "yolo-jail", "config.toml"), body)
}

func wantAll(t *testing.T, got []string, subs ...string) {
	t.Helper()
	joined := strings.Join(got, "\n")
	for _, s := range subs {
		if !strings.Contains(joined, s) {
			t.Errorf("missing %q in:\n%s", s, joined)
		}
	}
}

// validateUserTOML loads the user config from HOME, as a launch does, merges an empty
// workspace, and validates.
func validateUserTOML(t *testing.T) []string {
	t.Helper()
	ws := t.TempDir()
	merged, err := LoadConfig(ws, false, discard)
	if err != nil {
		return []string{err.Error()}
	}
	errs, _ := ValidateConfig(merged, ws, nil)
	return errs
}

func TestSelectedSetupReadsUserScopeOnly(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, `setup = "seatbelt-self"`+"\n\n[setups.mac-as-me]\nconfinement = \"seatbelt\"\n")
	name, user, set := SelectedSetupFromConfig()
	if !set || name != "seatbelt-self" {
		t.Fatalf("got %q set=%v, want seatbelt-self", name, set)
	}
	got, ok := user["mac-as-me"]
	if !ok || got.Confinement != setup.ConfinementSeatbelt || got.RunAs != setup.RunAsSelf || got.Name != "mac-as-me" {
		t.Fatalf("user setups = %+v", user)
	}
}

func TestSelectedSetupIsUnsetWithoutTheKey(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, "[setups.lab]\nrun_as = \"yolo-account\"\n")
	name, user, set := SelectedSetupFromConfig()
	if set || name != "" {
		t.Fatalf("got %q set=%v, want unset", name, set)
	}
	if user["lab"].Confinement != setup.DefaultConfinement || user["lab"].RunAs != setup.RunAsYoloAccount {
		t.Fatalf("each field takes its own default: %+v", user["lab"])
	}
}

// A legacy JSONC user config holds the same keys and reads the same way.
func TestSelectedSetupReadsALegacyUserConfig(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, `{"setup": "mac-as-me", "setups": {"mac-as-me": {"confinement": "seatbelt"}}}`)
	name, user, set := SelectedSetupFromConfig()
	if !set || name != "mac-as-me" || user["mac-as-me"].Confinement != setup.ConfinementSeatbelt {
		t.Fatalf("got %q %+v %v", name, user, set)
	}
	if errs := validateUserTOML(t); len(errs) != 0 {
		t.Fatalf("a valid legacy user config must validate clean: %v", errs)
	}
}

func TestAValidUserSetupValidatesClean(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, `setup = "mac-as-me"`+"\n\n[setups.mac-as-me]\nconfinement = \"seatbelt\"\nrun_as = \"self\"\n")
	if errs := validateUserTOML(t); len(errs) != 0 {
		t.Fatalf("errors = %v", errs)
	}
}

// A TOML project file is refused at load, by the readable walk, with the design's sentence and
// the file:line the walk adds (design §6).
func TestAProjectFileCannotChooseASetup(t *testing.T) {
	setupsHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceTOMLConfigName), "setup = \"seatbelt-yolo-account\"\n")
	_, err := LoadConfig(ws, false, discard)
	if err == nil {
		t.Fatal("a project `setup` loaded")
	}
	wantAll(t, []string{err.Error()},
		WorkspaceTOMLConfigName+":1:",
		"setup: a project cannot choose where its agent runs; that is yours to choose.",
		"For this launch:       yolo --setup seatbelt-yolo-account -- claude",
		"As your own default:   yolo config set setup seatbelt-yolo-account --user",
		"Then remove the line:  yolo config unset setup --project")
}

func TestAProjectFileCannotDefineASetup(t *testing.T) {
	setupsHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceTOMLConfigName), "[network]\nmode = \"bridge\"\n\n[setups.fast]\nconfinement = \"seatbelt\"\n")
	_, err := LoadConfig(ws, false, discard)
	if err == nil {
		t.Fatal("a project `[setups.fast]` loaded")
	}
	wantAll(t, []string{err.Error()},
		WorkspaceTOMLConfigName+":4:",
		"setups.fast: a project cannot define a setup; setups are yours.",
		"Define it in your user config instead:\n    yolo setup new fast --from seatbelt-self")
}

// The local file is a workspace file too, and its unset names --local.
func TestALocalFileCannotChooseASetup(t *testing.T) {
	setupsHome(t)
	ws := t.TempDir()
	write(t, filepath.Join(ws, WorkspaceLocalTOMLConfigName), "setup = \"podman-self\"\n")
	_, err := LoadConfig(ws, false, discard)
	if err == nil {
		t.Fatal("a local `setup` loaded")
	}
	wantAll(t, []string{err.Error()}, "a project cannot choose where its agent runs", "yolo config unset setup --local")
}

// A legacy JSONC project file has no readable walk; ValidateConfig refuses both keys.
func TestALegacyProjectFileCannotChooseOrDefineASetup(t *testing.T) {
	setupsHome(t)
	ws := t.TempDir()
	errs, _ := validateScopedIn(t, ws, "", `{"setup": "podman-self", "setups": {"fast": {"confinement": "podman"}}}`, nil)
	wantAll(t, errs,
		"setup: a project cannot choose where its agent runs; that is yours to choose.",
		"yolo config set setup podman-self --user",
		"yolo config unset setup --project",
		"setups.fast: a project cannot define a setup; setups are yours.",
		"yolo setup new fast --from podman-self")
	if hits := containing(errs, "is not defined"); len(hits) != 0 {
		t.Errorf("a refused project value must not also be read as yours: %v", hits)
	}
}

func TestABuiltInCannotBeRedefined(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, "[setups.podman-self]\nconfinement = \"podman\"\n")
	wantAll(t, validateUserTOML(t),
		"[setups.podman-self] redefines a built-in setup. Give yours its own name:\n    yolo setup new my-podman --from podman-self")
}

func TestAHostSetupIsNeverChosenByConfig(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, `setup = "host-self"`+"\n")
	wantAll(t, validateUserTOML(t),
		"setup \"host-self\" puts no walls around the agent, and only `yolo host` runs one:\n    yolo host -- claude")
}

// A user setup whose confinement is host is a host setup too.
func TestAUserHostSetupIsNeverChosenByConfig(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, `setup = "bare"`+"\n\n[setups.bare]\nconfinement = \"host\"\n")
	wantAll(t, validateUserTOML(t),
		"setup \"bare\" puts no walls around the agent, and only `yolo host` runs one:\n    yolo host --setup bare -- claude")
}

func TestAnUndefinedSetupIsRefused(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, `setup = "fast"`+"\n")
	wantAll(t, validateUserTOML(t), `setup "fast" is not defined. Defined here: podman-self`, "(yolo setup list)")
}

func TestAnUnknownSetupFieldIsRefused(t *testing.T) {
	home := setupsHome(t)
	userTOMLWith(t, home, "[setups.lab]\nconfinement = \"seatbelt\"\npolicy = \"allow-default\"\n")
	wantAll(t, validateUserTOML(t),
		"setups.lab: `policy` is not a setup field; Seatbelt has one rule list. Remove it:\n    yolo config unset setups.lab.policy --user")
}

func TestASetupFieldMustBeAString(t *testing.T) {
	home := setupsHome(t)
	userConfigWith(t, home, `{"setup": 3, "setups": {"lab": {"confinement": true}, "odd": "podman"}}`)
	wantAll(t, validateUserTOML(t),
		"config.setup: expected a setup name",
		"config.setups.lab.confinement: expected a string",
		"config.setups.odd: expected a table")
}

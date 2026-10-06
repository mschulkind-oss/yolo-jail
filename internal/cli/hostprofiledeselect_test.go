package cli

// hostprofiledeselect_test.go pins the HOST notch's "reaches nothing" warning ending
// (docs/design/providers-and-profiles-redesign.md PP-D12): where the selection came from and
// the spelling that selects none for the command there. The host's -p refuses `-p <cli>=`, so
// the warning never offers it; the `profile` key's null works here as in a jail, because the
// host folds the key through the same FoldProfiles. Each test drives the verb (hostMain), so
// deleting the Deselect the host hands its profile disclosure fails here.

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/jsonx"
)

// hostDeselectConfig is a user config whose `profile` (the value spelled, "" for none) reaches
// copilot, which has no Bedrock client of its own, so a bedrock selection reaches nothing for it.
func hostDeselectConfig(profile string) string {
	cfg := "{\n  \"packs\": [\"claude\", \"copilot\", \"bedrock\"],\n" +
		"  \"providers\": {\"bedrock\": {\"region\": \"eu-west-1\"}}"
	if profile != "" {
		cfg += ",\n  \"profile\": " + profile
	}
	return cfg + "\n}\n"
}

const hostDeselectWarning = `Warning: profile "bedrock" reaches nothing for copilot`

// A KEY-MADE SELECTION NAMES THE KEY AND ITS FIX AT THE HOST TOO. The warning used to end
// "or none for copilot" there, naming no way to select none, so `yolo host -- copilot` under
// `"profile": "bedrock"` repeated it on every command. It now names the key's file and line and
// the key respelled with a null for copilot; written back, that spelling stops the warning and
// keeps the command running.
func TestHostReachesNothingWarningNamesTheKeyAndItsFix(t *testing.T) {
	rc, _, errs := hostGateRun(t, hostDeselectConfig(`"bedrock"`), nil, nil, "copilot")
	if rc != 0 {
		t.Fatalf("yolo host -- copilot: rc = %d\n%s", rc, errs)
	}
	if !strings.Contains(errs, hostDeselectWarning) {
		t.Fatalf("the fixture no longer warns %q:\n%s", hostDeselectWarning, errs)
	}
	const spelling = `"profile": {"*": "bedrock", "copilot": null}`
	if want := "the selection is your config's `profile` key at ~/.config/yolo-jail/config.jsonc:4:14, " +
		"so write `" + spelling + "` there to select none for copilot on every command"; !strings.Contains(errs, want) {
		t.Errorf("the host warning does not name the key and its fix (%q):\n%s", want, errs)
	}
	if strings.Contains(errs, "`-p copilot=`") {
		t.Errorf("the host warning offers `-p copilot=`, which `yolo host` refuses:\n%s", errs)
	}

	spelled := regexp.MustCompile("write `\"profile\": ([^`]*)` there").FindStringSubmatch(errs)
	if spelled == nil {
		t.Fatalf("the host warning prints no persistent spelling:\n%s", errs)
	}
	if _, err := jsonx.Decode([]byte(spelled[1])); err != nil {
		t.Fatalf("the printed spelling %s does not parse: %v", spelled[1], err)
	}
	rc, _, fixed := hostGateRun(t, hostDeselectConfig(spelled[1]), nil, nil, "copilot")
	if rc != 0 {
		t.Fatalf("the printed spelling, written back: rc = %d\n%s", rc, fixed)
	}
	if strings.Contains(fixed, "reaches nothing for copilot") {
		t.Errorf("the printed spelling, written back, still warns at the host:\n%s", fixed)
	}
}

// A TYPED -p MADE THE SELECTION, so the clause says to leave it out, the one way the host undoes
// it; with no `profile` key behind it that is the whole fix, and the key is not mentioned.
func TestHostReachesNothingWarningNamesTheTypedFlag(t *testing.T) {
	rc, _, errs := hostGateRun(t, hostDeselectConfig(""), nil, []string{"-p", "bedrock"}, "copilot")
	if rc != 0 {
		t.Fatalf("yolo host -p bedrock -- copilot: rc = %d\n%s", rc, errs)
	}
	if !strings.Contains(errs, hostDeselectWarning) {
		t.Fatalf("the fixture no longer warns %q:\n%s", hostDeselectWarning, errs)
	}
	if want := "or none for copilot: the selection is this command's `-p`, so leave it out"; !strings.Contains(errs, want) {
		t.Errorf("a typed -p's host warning does not name its fix (%q):\n%s", want, errs)
	}
	for _, not := range []string{"`-p copilot=`", "`profile` key"} {
		if strings.Contains(errs, not) {
			t.Errorf("a typed -p's host warning says %q:\n%s", not, errs)
		}
	}
}

// A typed -p over a key that selects for the command too: leaving the -p out hands the command
// the key's selection, so the clause names the key's fix after it.
func TestHostReachesNothingWarningNamesTheKeyBehindATypedFlag(t *testing.T) {
	rc, _, errs := hostGateRun(t, hostDeselectConfig(`"bedrock"`), nil, []string{"-p", "bedrock"}, "copilot")
	if rc != 0 {
		t.Fatalf("yolo host -p bedrock -- copilot: rc = %d\n%s", rc, errs)
	}
	if want := "the selection is this command's `-p`, so leave it out; without it the selection is " +
		"your config's `profile` key at ~/.config/yolo-jail/config.jsonc:4:14, so write " +
		"`\"profile\": {\"*\": \"bedrock\", \"copilot\": null}` there to select none for copilot " +
		"on every command"; !strings.Contains(errs, want) {
		t.Errorf("the host warning does not name the key behind the -p (%q):\n%s", want, errs)
	}
}

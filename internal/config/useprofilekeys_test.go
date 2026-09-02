package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// This file pins the KEY namespace check (profiles-as-pack-variants.md §2.5, §8):
// a `use_profiles` key is a CLI name — a bin some resolvable pack installs — and
// an unknown one is FATAL. Before the check, {"cloude": "bedrock"} validated clean
// and silently did nothing, which is the live hole §2.5 documents.

// useProfileKeysHome isolates the pack universe the check reads. The embedded half
// is fixed by the binary; the CONFIGURED half comes from the user config and the pack
// store, both under $HOME — without isolation these tests would assert whatever this
// machine happens to have installed.
func useProfileKeysHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	return home
}

// writeUseProfileKeysUserConfig lays down a user config whose only key is `packs`.
func writeUseProfileKeysUserConfig(t *testing.T, home, packsJSON string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "yolo-jail")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := "{\n  \"packs\": " + packsJSON + "\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "config.jsonc"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// An unknown key is one fatal error naming the key, the namespace it failed against,
// and the names that ARE installed — the list is the whole fix for a typo, the same
// reason unknownEmbeddedMessage lists the available pack names.
//
// Driven through ValidateConfig (the caller both `yolo check` and the launch preflight
// use), not the validator, so the test fails if the check is unwired from validation.
func TestValidateUseProfilesKeysAreCLINames(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t,
		`{"use_profiles": {"claude": "bedrock", "cloude": "glm"}}`), t.TempDir(), nil)
	if len(errs) != 1 {
		t.Fatalf("want exactly one error (the typo'd key), got %d: %v", len(errs), errs)
	}
	e := errs[0]
	if !strings.HasPrefix(e, "config.use_profiles.cloude:") {
		t.Errorf("the error must name the unknown key: %s", e)
	}
	if !strings.Contains(e, `no pack installs a CLI named "cloude"`) {
		t.Errorf("the error must say what is wrong with the key: %s", e)
	}
	// The installed list must name the key that DID resolve, proving the check read
	// the real embedded packs rather than an empty universe.
	if !strings.Contains(e, "claude") {
		t.Errorf("the error must list the installed CLI names, including claude: %s", e)
	}
}

// Keys the packs install stay legal — including for a pack this config does not
// select, which is §8's split: existence is answered against the resolvable universe,
// selection only governs whether the contribution renders.
func TestValidateUseProfilesAcceptsKeysThePacksInstall(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t,
		`{"use_profiles": {"claude": "bedrock", "pi": "glm", "codex": "default"}}`),
		t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("keys every embedded pack installs must validate clean, got: %v", errs)
	}
}

// A null value removes a profile and asserts nothing about the key, so a nulled key
// is not held to the namespace — the same leniency the retired-key convention gives a
// key being deleted.
func TestValidateUseProfilesSkipsNulledKeys(t *testing.T) {
	useProfileKeysHome(t)
	errs, _ := ValidateConfig(decode(t,
		`{"use_profiles": {"cloude": null}}`), t.TempDir(), nil)
	if len(errs) != 0 {
		t.Fatalf("a nulled key removes the profile and must not error, got: %v", errs)
	}
}

// A configured pack that cannot be resolved makes the universe UNKNOWABLE, and the
// check steps aside rather than report the pack's own failure as a typo'd profile
// key: the launch refuses an unresolvable pack (stagePacks) and `yolo check` fails it
// in its Packs section, both louder and first. Pinning this so the degradation cannot
// silently become either "always skip" or "false fatals".
func TestValidateUseProfilesNamespaceStepsAsideWhenAPackCannotResolve(t *testing.T) {
	home := useProfileKeysHome(t)
	writeUseProfileKeysUserConfig(t, home,
		`[{"name": "gone", "source": "git+ssh://git@example.com/gone/pack.git"}]`)
	errs, _ := ValidateConfig(decode(t,
		`{"use_profiles": {"cloude": "glm"}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.use_profiles.") {
			t.Errorf("an unresolvable pack must not be misdiagnosed as a bad profile key: %s", e)
		}
	}
}

// The namespace stepping aside must not take the SHAPE check with it: a value that is
// not a string is a fact about this config alone, and reporting it depends on no pack
// resolving. Split from the test above so the two halves of the step-aside cannot grow
// back into one blanket return.
func TestValidateUseProfilesStillChecksValuesWhenTheUniverseIsUnknown(t *testing.T) {
	home := useProfileKeysHome(t)
	writeUseProfileKeysUserConfig(t, home,
		`[{"name": "gone", "source": "git+ssh://git@example.com/gone/pack.git"}]`)
	errs, _ := ValidateConfig(decode(t,
		`{"use_profiles": {"cloude": 4}}`), t.TempDir(), nil)
	for _, e := range errs {
		if strings.HasPrefix(e, "config.use_profiles.cloude:") {
			return
		}
	}
	t.Errorf("a non-string profile value must still be reported when a configured pack "+
		"cannot resolve, got %v", errs)
}

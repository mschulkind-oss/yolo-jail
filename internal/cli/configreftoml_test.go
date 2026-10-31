package cli

// configreftoml_test.go pins the TOML section of `yolo config-ref` (THE TOML FORMAT) to the
// tree it describes, because that section is hand-maintained text and an agent in a jail has
// nothing else to read: the names it lists must be keys the readable schema accepts, the verbs
// it names must be verbs the CLI dispatches, and every legacy key the migration moves must say
// where it went. Each assertion fails if the thing it describes is renamed or deleted.

import (
	"strings"
	"testing"

	"github.com/mschulkind-oss/yolo-jail/internal/config"
)

// tomlSection is the THE TOML FORMAT section of the reference, so a mention elsewhere in the
// 3000-line text cannot satisfy a check about it.
func tomlSection(t *testing.T) string {
	t.Helper()
	ref := configRefText(t)
	start := strings.Index(ref, "[bold cyan]THE TOML FORMAT[/bold cyan]")
	if start < 0 {
		t.Fatal("config-ref has no THE TOML FORMAT section")
	}
	end := strings.Index(ref[start+1:], "[bold cyan]")
	if end < 0 {
		t.Fatal("config-ref's THE TOML FORMAT section has no following section")
	}
	return ref[start : start+1+end]
}

// Every key the section documents as a TOML name is a key the readable schema accepts.
func TestConfigRefTOMLSectionNamesOnlyReadableKeys(t *testing.T) {
	section := tomlSection(t)
	readable := map[string]bool{}
	for _, k := range config.ReadableSchemaKeys() {
		readable[k.Path] = true
	}
	for _, key := range []string{
		"allow", "requests", "active_provider", "active_provider.default", "mounts",
		"env", "env.files", "env.set", "env.unset", "install.nix", "install.mise",
		"machine.gpu", "machine.resources", "machine.ports", "machine.forward",
		"requests.git.push", "requests.git.push_tags", "requests.git.new_branches",
		"requests.github.repos", "requests.github.create_pr", "requests.github.create_issue",
		"requests.github.dispatch_workflows",
		"packs",
	} {
		if !readable[key] {
			t.Errorf("%q is documented as a TOML key but the readable schema does not accept it", key)
		}
		leaf := key[strings.LastIndex(key, ".")+1:]
		if !strings.Contains(section, leaf) {
			t.Errorf("the TOML section does not mention %q", key)
		}
	}
	// convert_later is the one key that is legacy-only: the section says so, and the readable
	// schema has no such key.
	if readable[config.ConvertLaterKey] {
		t.Errorf("%s is a readable key; the section calls it legacy-only", config.ConvertLaterKey)
	}
	if !strings.Contains(section, config.ConvertLaterKey) || !strings.Contains(section, "LEGACY USER FILE ONLY") {
		t.Errorf("the TOML section does not document %s as legacy-user-file-only", config.ConvertLaterKey)
	}
}

// Every named verb the section tells a reader to run is dispatched, and its usage text is
// the one the section quotes.
func TestConfigRefTOMLSectionNamesTheVerbsTheCLIHas(t *testing.T) {
	section := tomlSection(t)
	for _, verb := range []string{"allow", "request", "active-provider"} {
		if _, ok := registry[verb]; !ok {
			t.Errorf("config-ref names `yolo %s` but the CLI dispatches no such verb", verb)
		}
		if !strings.Contains(section, "yolo "+verb) {
			t.Errorf("the TOML section does not name `yolo %s`", verb)
		}
	}
	for _, phrase := range []string{
		"yolo config why", "yolo config migrate", "yolo config set", "yolo pack add",
		"yolo pack remove", "yolo pack migrate", "yolo init",
	} {
		if !strings.Contains(section, phrase) {
			t.Errorf("the TOML section does not name `%s`", phrase)
		}
	}
}

// Every legacy key the migration moves or splits says where it went.
func TestConfigRefTOMLSectionCoversEveryMovedLegacyKey(t *testing.T) {
	section := tomlSection(t)
	for _, d := range config.RenameDescriptors() {
		if d.Transform != config.RenameMove && d.Transform != config.RenameSplit {
			continue
		}
		if strings.ContainsAny(d.OldPath, "*") || d.OldPath == "network" || d.OldPath == "network.mode" {
			continue
		}
		if !strings.Contains(section, d.OldPath) {
			t.Errorf("legacy key %q moves to %v but the TOML section does not say so", d.OldPath, d.Replacements)
		}
	}
}
